package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
	"github.com/agentserver/agentserver/internal/envtools/bridge"
	"github.com/agentserver/agentserver/internal/envtools/nameresolver"
	"github.com/agentserver/agentserver/internal/envtools/processes"
	"github.com/agentserver/agentserver/internal/envtools/tools"
)

// ConnectedExecutor mirrors the fields codex-exec-gateway's existing
// /api/exec-gateway/connected handler returns. Defined here to avoid
// importing the handler package from sdk.
type ConnectedExecutor struct {
	ExeID      string `json:"exe_id,omitempty"`
	Name       string `json:"name"`
	IsDefault  bool   `json:"is_default,omitempty"`
	LastSeenAt string `json:"last_seen_at,omitempty"`
}

// ConnectedLister is the subset of the gateway's executor registry the
// sdk package needs. The B6 wiring step provides an adapter that
// satisfies this interface from the existing store + registry types.
type ConnectedLister interface {
	Connected(ctx context.Context, workspaceID string) ([]ConnectedExecutor, error)
}

// RelayClientFactory builds a workspace-scoped bridge.RelayClient on
// demand for copy_path. The CopyPathTool constructor wants a concrete
// *bridge.RelayClient (not an interface), and the relay token is a
// workspace-scoped cap-token — so each workspace gets its own.
type RelayClientFactory func(workspaceID, capToken string) *bridge.RelayClient

// Server holds the SDK REST surface. Construct in cmd/codex-exec-gateway/main.go
// and call Mount(r chi.Router) once at startup.
//
// Per-workspace state — Pool, Resolver, tool registry — is built lazily
// on the first request for a workspace and cached for the Server's
// lifetime in wsCache. Each workspace gets its own cap-token (so the
// bridge layer authorises only that workspace's executors) and its own
// resolver Fetcher (so name → exe_id lookups are scoped to its
// connected list).
type Server struct {
	Auth     *ProxyTokenAuth
	Sessions *processes.Manager
	Registry ConnectedLister

	// ExecGatewayWSURL is the ws(s):// base URL the per-workspace Pool
	// uses to dial /bridge/<exe_id>. The exe_id is appended per dial
	// (see bridge.NewPool — first arg is treated as the base, the
	// pool's own .Dial appends /<exe_id>). Must end without a trailing
	// slash; e.g. "wss://codex-exec.example.com/bridge" or
	// "ws://localhost:6060/bridge".
	ExecGatewayWSURL string

	// CapTokenSecret is the HMAC secret used to mint per-workspace
	// cap-tokens consumed by the same process's /bridge verifier. Must
	// match cfg.CapTokenHMACSecret in production.
	CapTokenSecret []byte

	// RelayFactory, if non-nil, builds a workspace-scoped
	// bridge.RelayClient used by copy_path. Optional — copy_path is
	// only registered when this is set.
	RelayFactory RelayClientFactory

	// Recorder is the audit recorder used to capture handler-level
	// CallStart/CallEnd for every /api/connectors/* tool call. The
	// embedding codexexecgateway.Server wires its own audit.Recorder
	// here at construction time so REST calls and envmcp bridge sessions
	// share one Recorder. Mount() fills in a NewNoopRecorder if nil so
	// handlers can call methods unconditionally.
	Recorder audit.Recorder

	Logger *slog.Logger

	mu      sync.Mutex
	wsCache map[string]*workspaceCtx
}

// workspaceCtx caches the per-workspace bridge.Pool, name resolver,
// and tool registry for the duration of the Server's life. Pool itself
// already reconnects on connection loss (see bridge.Pool.Get); the
// cached entry survives transient executor churn.
type workspaceCtx struct {
	pool     *bridge.Pool
	resolver *nameresolver.Resolver
	tools    map[string]tools.Tool
}

// wsCtxFor returns (and lazily builds) the per-workspace pool +
// resolver + tool registry. Cached forever on this Server (no eviction
// today; deployments restart frequently enough that workspace turnover
// won't accrue meaningful state). The Pool inside survives across
// /bridge reconnects on its own.
func (s *Server) wsCtxFor(workspaceID string) (*workspaceCtx, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace_id empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.wsCache[workspaceID]; ok {
		return c, nil
	}
	tok, err := mintWorkspaceToken(s.CapTokenSecret, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("mint cap token: %w", err)
	}

	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}

	pool := bridge.NewPool(s.ExecGatewayWSURL, tok, logger)
	resolver := s.newWorkspaceResolver(workspaceID)

	toolMap := map[string]tools.Tool{
		"shell":        tools.NewShellTool(pool, resolver),
		"read_file":    tools.NewReadFileTool(pool, resolver),
		"write_file":   tools.NewWriteFileTool(pool, resolver),
		"apply_patch":  tools.NewApplyPatchTool(pool, resolver),
		"exec_command": tools.NewUnifiedExecTool(pool, tools.NewSessionStore(), resolver),
	}
	if s.RelayFactory != nil {
		toolMap["copy_path"] = tools.NewCopyPathTool(pool, resolver, s.RelayFactory(workspaceID, tok))
	}

	c := &workspaceCtx{pool: pool, resolver: resolver, tools: toolMap}
	if s.wsCache == nil {
		s.wsCache = map[string]*workspaceCtx{}
	}
	s.wsCache[workspaceID] = c
	return c, nil
}

// newWorkspaceResolver returns a Resolver scoped to workspaceID by
// hitting the in-process Registry directly — no loopback HTTP, no
// X-Loopback-Token. Each workspace gets its own Resolver so the cached
// name → exe_id map can't leak across tenants.
func (s *Server) newWorkspaceResolver(workspaceID string) *nameresolver.Resolver {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	fetch := func(ctx context.Context) ([]nameresolver.ConnectedEntry, error) {
		rows, err := s.Registry.Connected(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		out := make([]nameresolver.ConnectedEntry, 0, len(rows))
		for _, r := range rows {
			out = append(out, nameresolver.ConnectedEntry{
				ExeID:      r.ExeID,
				Name:       r.Name,
				IsDefault:  r.IsDefault,
				LastSeenAt: r.LastSeenAt,
			})
		}
		return out, nil
	}
	return nameresolver.NewResolverWithFetcher(fetch, logger)
}

// Mount registers every SDK route under /api/connectors/*. Each handler runs
// through authMiddleware which extracts and validates the Bearer token.
func (s *Server) Mount(r chi.Router) {
	if s.Recorder == nil {
		s.Recorder = audit.NewNoopRecorder()
	}
	r.Group(func(r chi.Router) {
		r.Use(s.authMiddleware)
		r.Post("/api/connectors/envs/list", s.handleEnvsList)
		r.Post("/api/connectors/envs/{name}/tool/call", s.handleToolCall)
		r.Post("/api/connectors/processes/{sid}/stdin", s.handleStdin)
		r.Get("/api/connectors/processes/{sid}/output", s.handleOutput)
		r.Post("/api/connectors/processes/{sid}/terminate", s.handleTerminate)
	})
}

type ctxKey int

const (
	ctxWorkspaceID ctxKey = iota
	ctxUserID
)

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			writeErr(w, http.StatusUnauthorized, "missing_bearer", "Authorization: Bearer <token> required")
			return
		}
		tok := strings.TrimPrefix(h, "Bearer ")
		wsID, userID, err := s.Auth.Verify(r.Context(), tok)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
			return
		}
		ctx := context.WithValue(r.Context(), ctxWorkspaceID, wsID)
		ctx = context.WithValue(ctx, ctxUserID, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func workspaceFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(ctxWorkspaceID).(string); ok {
		return v
	}
	return ""
}

func userIDFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(ctxUserID).(string); ok {
		return v
	}
	return ""
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": msg},
	})
}
