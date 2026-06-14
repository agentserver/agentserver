package codexappgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/agentserver/agentserver/internal/codexappgateway/approvalfilter"
	"github.com/agentserver/agentserver/internal/codexappgateway/auth"
	"github.com/agentserver/agentserver/internal/codexappgateway/broker"
	"github.com/agentserver/agentserver/internal/codexappgateway/codexhome"
	"github.com/agentserver/agentserver/internal/codexappgateway/scheduler"
	"github.com/agentserver/agentserver/internal/codexappgateway/supervisor"
	"github.com/agentserver/agentserver/internal/clientmeta"
	"github.com/agentserver/agentserver/internal/shortid"
	"github.com/agentserver/agentserver/internal/wsbridge"

	"github.com/go-chi/chi/v5"
	"nhooyr.io/websocket"
)

// ctxKey is the unexported type for context values stashed by
// requireInternalOrAPIKey so there are no collisions with other packages.
type ctxKey int

const (
	// ctxKeyAuthorizedWorkspace holds the workspace_id the bearer key is
	// scoped to. Set only on the bearer path; absent on X-Internal-Secret path.
	ctxKeyAuthorizedWorkspace ctxKey = iota
	// ctxKeyAuthorizedScopes holds the []string scopes granted by the bearer
	// key. Set only on the bearer path; absent on X-Internal-Secret path.
	ctxKeyAuthorizedScopes
)

// Server is the codex-app-gateway HTTP/WS server.
type Server struct {
	cfg      ServeConfig
	codexBin string
	auth     auth.Authenticator
	sup      *supervisor.Supervisor
	homeMgr  *codexhome.Manager
	logger   *slog.Logger

	// apiKeyValidator validates Bearer wak_<...> tokens against agentserver's
	// /internal/workspace-api-keys/validate RPC. May be nil in dev/test
	// configurations that don't have agentserver wired up; when nil, bearer
	// auth returns 401 (X-Internal-Secret still works).
	apiKeyValidator *auth.APIKeyValidator

	// buildConfig produces the per-spawn config + env vars (e.g. a
	// workspace-scoped LLM API key). Receives the per-spawn loopback
	// token so the agentserver MCP entry in config.toml can embed it.
	// Allowed to hit the network. Errors abort the spawn.
	buildConfig func(ctx context.Context, workspaceID, userID string) (supervisor.SpawnConfig, error)

	// brokerPool caches per-workspace broker.Conn instances. Idle TTL is
	// cfg.BrokerPoolIdleTTL (default 30m, override via CXG_BROKER_POOL_IDLE_TTL).
	// Initialized in NewServer; nil in lightweight test Server literals.
	brokerPool *broker.Pool
}

// workspaceTokenFetcher is the subset of *WorkspaceTokenClient buildConfig
// needs. Defined here so tests can stub.
type workspaceTokenFetcher interface {
	GetOrCreate(ctx context.Context, workspaceID string) (string, error)
}

// maxWSFrameBytes bounds each ws read on the user-facing and
// app-server-facing connections. 64 MiB is well above any legitimate
// codex frame (conversation history + tool output) while still
// preventing a runaway or hostile client from pinning gateway memory.
const maxWSFrameBytes int64 = 64 << 20

// NewServer wires up the production server. selfBin is the absolute path
// to the codex-app-gateway binary itself, used as the `command =` for
// each per-executor `[mcp_servers.exe_*]` entry (codex spawns it as the
// env-mcp child).
func NewServer(cfg ServeConfig, codexBin, selfBin string, logger *slog.Logger) (*Server, error) {
	store, err := newS3Store(cfg.S3)
	if err != nil {
		return nil, fmt.Errorf("s3 store: %w", err)
	}
	mgr := codexhome.NewManager(cfg.TmpRoot)
	// Static fallback env: only used if the per-spawn ModelServer token
	// fetch returns empty (e.g. workspace hasn't connected ModelServer yet).
	supEnv := []string{}
	if cfg.CodexAPIKey != "" && cfg.ModelProviderEnvKey != "" {
		supEnv = append(supEnv, cfg.ModelProviderEnvKey+"="+cfg.CodexAPIKey)
	}
	sup := supervisor.NewSupervisor(supervisor.SupervisorConfig{
		CodexBin: codexBin,
		HomeMgr:  mgr,
		Store:    store,
		ExtraEnv: supEnv,
		Logger:   logger,
	})
	wsTokenClient := NewWorkspaceTokenClient(cfg.AgentserverInternalURL, cfg.AgentserverInternalSecret)
	var apiKeyVal *auth.APIKeyValidator
	if cfg.AgentserverInternalURL != "" && cfg.AgentserverInternalSecret != "" {
		apiKeyVal = auth.NewAPIKeyValidator(cfg.AgentserverInternalURL, cfg.AgentserverInternalSecret)
	}
	s := &Server{
		cfg:             cfg,
		codexBin:        codexBin,
		auth:            auth.NewRemoteVerifier(cfg.AgentserverInternalURL, cfg.AgentserverInternalSecret),
		sup:             sup,
		homeMgr:         mgr,
		logger:          logger,
		apiKeyValidator: apiKeyVal,
	}
	s.buildConfig = makeBuildConfig(cfg, wsTokenClient, selfBin, logger)
	s.brokerPool = broker.NewPool(
		makeSupervisorResolver(s.sup, s.buildConfig),
		cfg.BrokerPoolIdleTTL,
	)
	return s, nil
}

// makeBuildConfig returns the per-spawn SpawnConfig producer. Split out
// so server_test.go can construct a Server with stub clients.
func makeBuildConfig(cfg ServeConfig, wsTokenClient workspaceTokenFetcher, selfBin string, logger *slog.Logger) func(context.Context, string, string) (supervisor.SpawnConfig, error) {
	return func(ctx context.Context, workspaceID, userID string) (supervisor.SpawnConfig, error) {
		// Per 2026-05-16 redesign, the executor list is no longer
		// fixed at spawn time — env-mcp reads it live (post the
		// 2026-06-14 loopback removal, directly from exec-gateway
		// using the workspace cap-token we mint here). We still mint
		// a per-spawn turn so /api/exec-gateway/revoke-turn semantics
		// survive.
		turnID := "trn_" + shortid.Generate()
		ttl := cfg.CapTokenTTL
		if ttl <= 0 {
			ttl = time.Hour
		}
		workspaceTok, err := MintCapToken(cfg.CapTokenHMACSecret, turnID, workspaceID, userID, ttl)
		if err != nil {
			return supervisor.SpawnConfig{}, fmt.Errorf("mint workspace cap token: %w", err)
		}
		trusted := cfg.ProjectTrustedPaths
		if len(trusted) == 0 {
			trusted = []string{"/tmp"}
		}

		// Per-spawn env: fetch a workspace-scoped proxy token (long
		// lived, cached server-side). codex sends this as Bearer to
		// llmproxy, which validates and swaps it for a fresh
		// modelserver JWT per request — meaning OAuth refreshes
		// server-side reach the running pod without a respawn.
		var spawnEnv []string
		if cfg.ModelProviderEnvKey != "" {
			tok, err := wsTokenClient.GetOrCreate(ctx, workspaceID)
			if err != nil {
				logger.Warn("workspace-token: fetch failed; falling back to static CodexAPIKey",
					"workspace_id", workspaceID, "err", err)
			} else {
				spawnEnv = append(spawnEnv, cfg.ModelProviderEnvKey+"="+tok)
			}
		}

		return supervisor.SpawnConfig{
			Config: codexhome.ConfigInput{
				ModelProvider:   cfg.ModelProvider,
				Model:           cfg.Model,
				ReasoningEffort: cfg.ReasoningEffort,
				ModelProviders: map[string]codexhome.ModelProvider{
					cfg.ModelProvider: {
						Name:    cfg.ModelProvider,
						BaseURL: cfg.ModelProviderBaseURL,
						EnvKey:  cfg.ModelProviderEnvKey,
						WireAPI: cfg.ModelProviderWireAPI,
					},
				},
				AgentServer: codexhome.AgentServerMCP{
					CodexBin:                  selfBin,
					WorkspaceID:               workspaceID,
					ExecGatewayURL:            strings.TrimRight(cfg.ExecGatewayWSURL, "/") + "/bridge",
					WorkspaceToken:            workspaceTok,
					ExecGatewayInternalURL:    cfg.ExecGatewayInternalURL,
					ExecGatewayInternalSecret: cfg.ExecGatewayInternalSecret,
					AgentserverInternalURL:    cfg.AgentserverInternalURL,
				},
				ProjectTrustedPaths: trusted,
			},
			Env: spawnEnv,
		}, nil
	}
}

// Run serves HTTP until ctx is done.
func (s *Server) Run(ctx context.Context, listenAddr string) error {
	httpSrv := &http.Server{Addr: listenAddr, Handler: s.Routes()}
	reaper := supervisor.NewIdleReaper(s.sup, 1*time.Minute, s.cfg.IdleShutdown, s.logger)
	reaperCtx, reaperCancel := context.WithCancel(context.Background())
	defer reaperCancel()
	go reaper.Run(reaperCtx)

	if s.cfg.AgentserverInternalURL != "" {
		schedCfg := scheduler.Config{
			AgentserverBase: s.cfg.AgentserverInternalURL,
			InternalSecret:  s.cfg.AgentserverInternalSecret,
			ImbridgeBase:    s.cfg.ImbridgeBaseURL,
			ImbridgeSecret:  s.cfg.ImbridgeInternalSecret,
			BrokerPool:      s.brokerPool,
			PodID:           os.Getenv("POD_NAME"),
			PID:             os.Getpid(),
			TickInterval:    s.cfg.SchedulerTickInterval,
			LeaseSeconds:    s.cfg.SchedulerLeaseSeconds,
			Concurrency:     s.cfg.SchedulerConcurrency,
		}
		sched := scheduler.New(schedCfg, s.logger)
		schedCtx, schedCancel := context.WithCancel(context.Background())
		defer schedCancel()
		go sched.Run(schedCtx)
		s.logger.Info("scheduler enabled",
			"agentserver", s.cfg.AgentserverInternalURL,
			"tick", schedCfg.TickInterval,
			"concurrency", schedCfg.Concurrency)
	} else {
		s.logger.Info("scheduler disabled (CXG_AGENTSERVER_INTERNAL_URL unset)")
	}

	ln, err := wsbridge.ListenWithKeepAlive(ctx, "tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		s.sup.ShutdownAll(shutdownCtx)
		if s.brokerPool != nil {
			s.brokerPool.Close()
		}
		return nil
	case err := <-errCh:
		s.sup.ShutdownAll(context.Background())
		if s.brokerPool != nil {
			s.brokerPool.Close()
		}
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Routes builds the chi router. Public for tests.
//
// Two paths serve the same handler for the inbound TUI ws upgrade:
//   - "/"             — required by upstream codex's --remote URL parser,
//                       which only accepts ws[s]://host:port and connects
//                       to "/" (no path component).
//   - "/codex-app/ws" — kept for direct in-cluster testing (curl, kubectl
//                       port-forward) and path-based ingress setups.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	r.Get("/", s.handleCodexAppWS)
	r.Get("/codex-app/ws", s.handleCodexAppWS)
	// /internal/* loopback handlers (connected, scheduled-tasks) were
	// removed 2026-06-14. env-mcp now calls codex-exec-gateway and
	// agentserver-main directly with its workspace cap-token.
	turnHandler := &turnAPIHandler{
		runner: newPoolRunner(s.brokerPool),
	}
	r.With(s.requireInternalOrAPIKey).Post("/api/turns", turnHandler.ServeHTTP)
	return r
}

// requireInternalSecret is chi middleware that validates the
// X-Internal-Secret header against cfg.AgentserverInternalSecret.
func (s *Server) requireInternalSecret(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AgentserverInternalSecret == "" {
			http.Error(w, "internal secret not configured", http.StatusInternalServerError)
			return
		}
		if r.Header.Get("X-Internal-Secret") != s.cfg.AgentserverInternalSecret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireInternalOrAPIKey accepts EITHER X-Internal-Secret (legacy
// in-cluster path; trusts body.workspaceId as-is, scope check bypassed)
// OR Authorization: Bearer wak_<...> (public path; stashes the validated
// workspace_id and scopes into the request context so the handler can
// enforce consistency with the request body AND scope presence).
//
// X-Internal-Secret is checked first because it's a cheap constant
// compare; Bearer adds an in-cluster HTTP roundtrip to agentserver.
func (s *Server) requireInternalOrAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path A: trusted in-cluster caller (imbridge). Cheap compare first.
		if s.cfg.AgentserverInternalSecret != "" &&
			r.Header.Get("X-Internal-Secret") == s.cfg.AgentserverInternalSecret {
			next.ServeHTTP(w, r)
			return
		}
		// Path B: public Bearer wak_ token.
		authz := r.Header.Get("Authorization")
		if strings.HasPrefix(authz, "Bearer wak_") {
			if s.apiKeyValidator == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			secret := strings.TrimPrefix(authz, "Bearer ")
			key, err := s.apiKeyValidator.Validate(r.Context(), secret)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyAuthorizedWorkspace, key.WorkspaceID)
			ctx = context.WithValue(ctx, ctxKeyAuthorizedScopes, key.Scopes)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

// scopesFromContext returns the scopes the bearer key authorized, or nil
// when the caller authenticated via X-Internal-Secret (scope check bypassed).
func scopesFromContext(r *http.Request) []string {
	v := r.Context().Value(ctxKeyAuthorizedScopes)
	if v == nil {
		return nil
	}
	scopes, _ := v.([]string)
	return scopes
}

// requireBearerScope returns an error when the request was bearer-authenticated
// and the scope list does not include `required`. Internal-secret callers
// bypass (scopesFromContext returns nil → no enforcement).
func requireBearerScope(r *http.Request, required string) error {
	v := r.Context().Value(ctxKeyAuthorizedScopes)
	if v == nil {
		return nil // internal secret path — bypass scope enforcement
	}
	scopes, _ := v.([]string)
	for _, s := range scopes {
		if s == required {
			return nil
		}
	}
	return errors.New("missing scope: " + required)
}

// Close releases per-server resources. Must be called on shutdown.
func (s *Server) Close() {
	if s.brokerPool != nil {
		s.brokerPool.Close()
	}
}

func (s *Server) handleCodexAppWS(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.ExtractBearer(r)
	if !ok {
		http.Error(w, "missing Bearer", http.StatusUnauthorized)
		return
	}

	// Prefer OpenSession when the authenticator supports it (RemoteVerifier
	// in production). HMAC (local-test only) falls through to plain Verify
	// and leaves sessionID empty so the deferred close is a no-op.
	clientIP := clientmeta.ClientIP(r)
	clientUA := r.Header.Get("User-Agent")
	codexVersion, osStr := clientmeta.ParseCodexUA(clientUA)
	var (
		id        auth.Identity
		sessionID string
		err       error
	)
	if tracker, ok := s.auth.(auth.SessionTracker); ok {
		id, sessionID, err = tracker.OpenSession(r.Context(), tok, clientIP, clientUA, codexVersion, osStr)
	} else {
		id, err = s.auth.Verify(r.Context(), tok)
	}
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if sessionID != "" {
		// Close session in the background — must not block ws shutdown.
		defer func() {
			go func(sid string) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if tracker, ok := s.auth.(auth.SessionTracker); ok {
					if cerr := tracker.CloseSession(ctx, sid); cerr != nil {
						s.logger.Warn("close session", "err", cerr, "session", sid)
					}
				}
			}(sessionID)
		}()
	}

	userWS, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.logger.Warn("ws accept failed", "err", err)
		return
	}
	// codex client streams large frames (tool listings, prompts, file
	// contents); nhooyr's 32 KiB default would slam the connection shut
	// mid-session with "read limited at 32769 bytes". 64 MiB is well
	// above any legitimate codex frame and still bounds a runaway
	// client.
	userWS.SetReadLimit(maxWSFrameBytes)
	defer userWS.Close(websocket.StatusNormalClosure, "client closing")

	key := supervisor.Key{WorkspaceID: id.WorkspaceID}
	ctx := r.Context()
	handle, err := s.sup.EnsureSubprocess(ctx, key, func() (supervisor.SpawnConfig, error) {
		return s.buildConfig(ctx, id.WorkspaceID, id.UserID)
	})
	if err != nil {
		s.logger.Error("ensure subprocess", "err", err, "key", key)
		_ = userWS.Close(websocket.StatusInternalError, "subprocess unavailable")
		return
	}

	childWS, _, err := websocket.Dial(ctx, handle.WSURL, &websocket.DialOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		s.logger.Error("dial child", "err", err, "url", handle.WSURL)
		_ = userWS.Close(websocket.StatusInternalError, "subprocess dial failed")
		return
	}
	childWS.SetReadLimit(maxWSFrameBytes)
	defer childWS.Close(websocket.StatusNormalClosure, "gateway closing")

	s.sup.Touch(key)
	// Snoop the first client→server frame for JSON-RPC `initialize` so we
	// can backfill codex_version / client_ua on the session row — codex's
	// ws upgrade carries no UA, so the row inserted by OpenSession is
	// otherwise blank. Best-effort; failures are logged and dropped.
	var initOnce sync.Once
	intc := wsbridge.Interceptor{
		OnClientFrame: func(frame []byte) []byte {
			initOnce.Do(func() {
				if sessionID == "" {
					return
				}
				updater, ok := s.auth.(auth.SessionMetaUpdater)
				if !ok {
					return
				}
				ua, version, osStr, ok := parseInitializeClientInfo(frame)
				if !ok {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if uerr := updater.UpdateSessionMeta(ctx, sessionID, ua, version, osStr); uerr != nil {
					s.logger.Warn("session-meta update", "err", uerr, "session", sessionID)
				}
			})
			// Block client→app-server RPCs that would touch the shared
			// codex-app-gateway pod's fs or spawn processes there
			// (thread/shellCommand, command/exec/*, fs/*). See the
			// rationale on blockedClientRPCMethods. Reply directly to
			// the user with a JSON-RPC error and drop the frame.
			if resp, blocked := tryBlockLocalIORPC(frame); blocked {
				if resp != nil {
					if werr := userWS.Write(ctx, websocket.MessageText, resp); werr != nil {
						s.logger.Warn("local-io-block: write reply", "err", werr, "key", key)
					}
				}
				s.logger.Info("local-io-block: dropped client RPC", "key", key, "session", sessionID)
				return wsbridge.DropFrame
			}
			return nil
		},
		OnServerFrame: func(frame []byte) []byte {
			if resp, ok := approvalfilter.TryReply(frame); ok {
				// Auto-accept: write the synthesized response back to upstream
				// and drop the request so the caller never sees it. Codex
				// expects server-to-client requests to be answered on the same
				// ws connection.
				if werr := childWS.Write(ctx, websocket.MessageText, resp); werr != nil {
					s.logger.Warn("approval-filter: write reply", "err", werr, "key", key)
				}
				return wsbridge.DropFrame
			}
			return nil
		},
	}

	if err := wsbridge.RunProxyWithInterceptor(ctx, userWS, childWS, intc, func() { s.sup.Touch(key) }); err != nil {
		s.logger.Info("proxy ended", "err", err, "key", key)
	}
}

// parseInitializeClientInfo inspects a single ws frame and, if it's a
// JSON-RPC `initialize` request with a clientInfo block, returns
// ("<name>/<version>", "<version>", "", true). OS is not present in
// codex's initialize protocol — that column stays empty without a
// codex client patch.
//
// Tolerant on shape: anything that doesn't decode or doesn't look like
// initialize returns ok=false so the caller silently skips.
func parseInitializeClientInfo(frame []byte) (ua, version, osStr string, ok bool) {
	var msg struct {
		Method string `json:"method"`
		Params struct {
			ClientInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"clientInfo"`
		} `json:"params"`
	}
	if err := json.Unmarshal(frame, &msg); err != nil {
		return "", "", "", false
	}
	if msg.Method != "initialize" {
		return "", "", "", false
	}
	name := msg.Params.ClientInfo.Name
	v := msg.Params.ClientInfo.Version
	if name == "" && v == "" {
		return "", "", "", false
	}
	ua = name
	if v != "" {
		if ua != "" {
			ua += "/" + v
		} else {
			ua = v
		}
	}
	return ua, v, "", true
}

