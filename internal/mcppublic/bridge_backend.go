package mcppublic

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/agentserver/agentserver/internal/envtools/bridge"
	"github.com/agentserver/agentserver/internal/envtools/nameresolver"
	"github.com/agentserver/agentserver/internal/envtools/tools"
)

// BridgeBackend is the production ToolBackend. It maintains a
// per-Principal "toolkit" — bridge.Pool + nameresolver.Resolver +
// tools.SessionStore + envtools/tools instances — built once on first
// tools/call for a Principal and reused across subsequent calls.
//
// The toolkit is keyed by (UserID, WorkspaceID, cap-token). The
// cap-token is part of the key because bridge.Pool's auth token is
// fixed at construction; when CapMinter rotates the workspace token
// (every ~9 minutes), the next request constructs a fresh toolkit
// with the new token. Idle toolkits get reaped after IdleTimeout.
//
// Architecture (post the 2026-06-15 amendment that pins each PAT to
// one workspace):
//
//   Principal{UserID, WorkspaceID} + CapToken
//        ↓
//   principalToolkit{
//      pool *bridge.Pool       — workspace cap-token bearer, lazy WS dials
//      resolver *Resolver      — Fetcher → HTTPExecutorsSource for WorkspaceID
//      sessions *SessionStore  — write_stdin/read_output/terminate continuity
//      tools map[name]Tool     — 9 envtools instances (cheap struct allocs)
//   }
//
// This mirrors the in-pod env-mcp's Run() function near-1:1 — same
// pool/resolver/sessions/tools shapes, same workspace-scoped lifetime.
// The only structural differences are:
//   - the resolver's Fetcher reads from ExecutorsSource (an interface,
//     prod = HTTP to exec-gateway) instead of app-gateway's loopback
//     /internal/connected (which doesn't exist post the 2026-06-14
//     loopback removal anyway)
//   - the cap-token is rotated by CapMinter, not minted once at codex
//     spawn time
type BridgeBackend struct {
	// ExecGatewayBridgeBase is the WS base URL to which `/<exe_id>` is
	// appended by bridge.Pool.Get. Same value the in-pod env-mcp
	// already uses (CXG_BRIDGE_URL).
	ExecGatewayBridgeBase string

	// Executors is the source the per-Principal nameresolver's
	// Fetcher reads from. Production: HTTPExecutorsSource (calls
	// codex-exec-gateway's internal HTTP API for one workspace's
	// executor list).
	Executors ExecutorsSource

	// RelayClient is forwarded to copy_path. May be nil; if it is,
	// copy_path falls back to the ws cat-pump (existing behaviour).
	RelayClient *bridge.RelayClient

	// IdleTimeout is how long a toolkit must sit unused before the
	// reaper closes it. Defaults to 15 minutes — long enough that the
	// 9-minute cap-token reuse window finishes inside one toolkit's
	// lifetime, short enough that a one-shot user doesn't pin a
	// connection past the cap-token TTL.
	IdleTimeout time.Duration

	Logger *slog.Logger

	mu       sync.Mutex
	toolkits map[string]*principalToolkit // key: userID + "|" + workspaceID + "|" + capToken

	reaperOnce sync.Once
	reaperStop chan struct{}
}

// principalToolkit holds the per-Principal state. Identical shape to
// what `internal/codexappgateway/envmcp/envmcp.go` builds for the
// in-pod path — same pool, same resolver, same sessions, same tool
// registry.
type principalToolkit struct {
	pool     *bridge.Pool
	resolver *nameresolver.Resolver
	sessions *tools.SessionStore
	tools    map[string]tools.Tool
	lastUsed time.Time
}

const defaultBackendIdleTimeout = 15 * time.Minute

// NewBridgeBackend wires a backend ready to serve dispatcher calls.
// Starts no background goroutines until the first Call; the reaper is
// lazy so tests don't accumulate ticker leaks.
func NewBridgeBackend(execGatewayBridgeBase string, executors ExecutorsSource, relay *bridge.RelayClient, logger *slog.Logger) (*BridgeBackend, error) {
	if execGatewayBridgeBase == "" {
		return nil, fmt.Errorf("mcppublic: exec-gateway bridge base URL required")
	}
	if executors == nil {
		return nil, fmt.Errorf("mcppublic: ExecutorsSource required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &BridgeBackend{
		ExecGatewayBridgeBase: strings.TrimRight(execGatewayBridgeBase, "/"),
		Executors:             executors,
		RelayClient:           relay,
		IdleTimeout:           defaultBackendIdleTimeout,
		Logger:                logger,
		toolkits:              map[string]*principalToolkit{},
		reaperStop:            make(chan struct{}),
	}, nil
}

// Close stops the reaper goroutine and closes every cached toolkit's
// bridge pool. Idempotent — safe to call from cmd-binary shutdown paths.
func (b *BridgeBackend) Close() {
	close(b.reaperStop)
	b.mu.Lock()
	tk := b.toolkits
	b.toolkits = map[string]*principalToolkit{}
	b.mu.Unlock()
	for _, t := range tk {
		t.pool.Close()
	}
}

// Call implements ToolBackend.Call. The dispatcher has already
// validated the principal can hit this workspace + minted the
// cap-token; the backend's job is purely transport:
//
//  1. Get-or-build a per-Principal toolkit for (UserID, WorkspaceID,
//     CapToken).
//  2. Look up the requested tool by name from the toolkit.
//  3. Invoke Tool.Call with the dispatcher's raw args. The toolkit's
//     nameresolver translates the args' environment_id field (a name)
//     to an exe_id internally; the dispatcher doesn't pre-resolve
//     anything (post the 2026-06-15 amendment, names are per-workspace
//     unique by table constraint — no qualifier dance needed).
func (b *BridgeBackend) Call(ctx context.Context, in ToolBackendCall) (tools.MCPCallToolResult, error) {
	if in.Principal == nil {
		return tools.MCPCallToolResult{}, fmt.Errorf("mcppublic: nil principal in ToolBackendCall")
	}
	tk := b.toolkitFor(in.Principal.UserID, in.Principal.WorkspaceID, in.CapToken)
	tool, ok := tk.tools[in.Tool]
	if !ok {
		return tools.MCPCallToolResult{}, fmt.Errorf("mcppublic: unknown tool %q in toolkit", in.Tool)
	}
	return tool.Call(ctx, in.RawArgs)
}

// toolkitFor returns the per-Principal toolkit, lazily building one
// on first call. Also bumps lastUsed and starts the reaper on first
// use.
//
// The cap-token is part of the key on purpose: when CapMinter rotates
// the token (every ~9 minutes), the next request lands a fresh
// toolkit with a fresh pool dialing under the new token. The old
// toolkit lingers until the reaper closes it — its already-open
// bridge connections were authenticated at dial time, so they
// continue to work until they idle out or get reaped.
func (b *BridgeBackend) toolkitFor(userID, workspaceID, capToken string) *principalToolkit {
	b.reaperOnce.Do(func() { go b.reapLoop() })

	key := userID + "|" + workspaceID + "|" + capToken
	b.mu.Lock()
	defer b.mu.Unlock()
	if t, ok := b.toolkits[key]; ok {
		t.lastUsed = time.Now()
		return t
	}
	tk := b.buildToolkit(workspaceID, capToken)
	tk.lastUsed = time.Now()
	b.toolkits[key] = tk
	return tk
}

// buildToolkit constructs one principalToolkit. Same wiring shape as
// internal/codexappgateway/envmcp/envmcp.go::Run — pool first, then
// resolver fed by a Fetcher closure that hits our ExecutorsSource
// (scoped to this toolkit's workspaceID), then sessions, then tool
// instances. Cheap (no IO during construction — the WS dials happen
// lazily on first tool call that needs a bridge).
func (b *BridgeBackend) buildToolkit(workspaceID, capToken string) *principalToolkit {
	pool := bridge.NewPool(b.ExecGatewayBridgeBase, capToken, b.Logger)
	resolver := nameresolver.NewResolverWithFetcher(
		func(ctx context.Context) ([]nameresolver.ConnectedEntry, error) {
			rows, err := b.Executors.ListWorkspaceExecutors(ctx, workspaceID)
			if err != nil {
				return nil, err
			}
			out := make([]nameresolver.ConnectedEntry, 0, len(rows))
			for _, r := range rows {
				out = append(out, nameresolver.ConnectedEntry{
					ExeID:       r.ExeID,
					Name:        r.Name,
					Description: r.Description,
					IsDefault:   r.IsDefault,
					LastSeenAt:  r.LastSeenISO,
				})
			}
			return out, nil
		},
		b.Logger,
	)
	sessions := tools.NewSessionStore()
	tk := &principalToolkit{
		pool:     pool,
		resolver: resolver,
		sessions: sessions,
	}
	tk.tools = map[string]tools.Tool{
		"list_environments": tools.NewListEnvironmentsTool(resolver),
		"shell":             tools.NewShellTool(pool, resolver),
		"exec_command":      tools.NewUnifiedExecTool(pool, sessions, resolver),
		"write_stdin":       tools.NewWriteStdinTool(pool, sessions),
		"read_output":       tools.NewReadOutputTool(pool, sessions),
		"terminate":         tools.NewTerminateTool(pool, sessions),
		"read_file":         tools.NewReadFileTool(pool, resolver),
		"apply_patch":       tools.NewApplyPatchTool(pool, resolver),
		"copy_path":         tools.NewCopyPathTool(pool, resolver, b.RelayClient),
	}
	return tk
}

// reapLoop closes toolkits idle longer than IdleTimeout. Runs at
// IdleTimeout/4 cadence so an entry is reaped within ~25% of its
// nominal idle ceiling. Stops cleanly on b.reaperStop close.
func (b *BridgeBackend) reapLoop() {
	tick := time.NewTicker(b.IdleTimeout / 4)
	defer tick.Stop()
	for {
		select {
		case <-b.reaperStop:
			return
		case <-tick.C:
			b.reapOnce()
		}
	}
}

func (b *BridgeBackend) reapOnce() {
	cutoff := time.Now().Add(-b.IdleTimeout)
	b.mu.Lock()
	var toClose []*principalToolkit
	for k, t := range b.toolkits {
		if t.lastUsed.Before(cutoff) {
			toClose = append(toClose, t)
			delete(b.toolkits, k)
		}
	}
	b.mu.Unlock()
	for _, t := range toClose {
		t.pool.Close()
	}
}

// DefaultPublicToolMeta returns the tools/list catalog the public
// gateway advertises. Built by constructing each tool once just to
// pull out its name + description + schema; cheap (no IO) and keeps
// the public surface byte-identical to the in-pod env-mcp's. PR F's
// cmd binary calls this once at startup and passes the result into
// NewDispatcher.
//
// TODO(mcppublic-scheduling): the 6 scheduling tools the in-pod env-mcp
// also registers (schedule_task, list_tasks, update_task, cancel_task,
// pause_task, resume_task — see internal/codexappgateway/envmcp/
// scheduling/) are not yet on the public surface. They need a separate
// HTTPSchedulingTransport that calls agentserver-main's
// /api/internal/workspaces/{wid}/scheduled-tasks/* endpoint with the
// workspace cap-token. Tracked separately to keep this PR scoped to
// the executor-targeted tools.
func DefaultPublicToolMeta() []PublicToolMeta {
	// Build a dummy toolkit just to scrape Tool.Name() / Description /
	// InputSchema for the catalog. The pool + resolver + sessions
	// inside never see a real request.
	dummyPool := bridge.NewPool("ws://dummy", "dummy", nil)
	defer dummyPool.Close()
	dummyResolver := nameresolver.NewResolverWithFetcher(
		func(context.Context) ([]nameresolver.ConnectedEntry, error) { return nil, nil },
		nil,
	)
	dummySessions := tools.NewSessionStore()
	dummyTools := map[string]tools.Tool{
		"list_environments": tools.NewListEnvironmentsTool(dummyResolver),
		"shell":             tools.NewShellTool(dummyPool, dummyResolver),
		"exec_command":      tools.NewUnifiedExecTool(dummyPool, dummySessions, dummyResolver),
		"write_stdin":       tools.NewWriteStdinTool(dummyPool, dummySessions),
		"read_output":       tools.NewReadOutputTool(dummyPool, dummySessions),
		"terminate":         tools.NewTerminateTool(dummyPool, dummySessions),
		"read_file":         tools.NewReadFileTool(dummyPool, dummyResolver),
		"apply_patch":       tools.NewApplyPatchTool(dummyPool, dummyResolver),
		"copy_path":         tools.NewCopyPathTool(dummyPool, dummyResolver, nil),
	}
	out := make([]PublicToolMeta, 0, len(dummyTools))
	for _, t := range dummyTools {
		out = append(out, PublicToolMeta{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return out
}
