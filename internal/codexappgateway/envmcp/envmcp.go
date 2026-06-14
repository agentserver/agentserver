// Package envmcp implements the `codex-app-gateway env-mcp` subcommand:
// a stateless MCP server that codex spawns as a child process. It
// exposes a fixed tool set (list_environments, shell, exec_command,
// write_stdin, read_output, terminate, read_file, apply_patch) to
// codex; tool calls are multiplexed across the workspace's connected
// executors via a per-exe BridgeClient pool keyed by environment name.
package envmcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/agentserver/agentserver/internal/codexappgateway/envmcp/scheduling"
	"github.com/agentserver/agentserver/internal/envtools/bridge"
	"github.com/agentserver/agentserver/internal/envtools/nameresolver"
	"github.com/agentserver/agentserver/internal/envtools/tools"
)

// OpenLogSink returns a writer that fans out logger output to stderr
// and, when path is non-empty, to the file at path (opened append).
// On file-open failure it falls back to stderr-only and returns a
// non-nil error so the caller can log the degradation. Exported so
// `runEnvMcp` in cmd/codex-app-gateway can build the slog handler
// before envmcp.Run takes over.
func OpenLogSink(stderr io.Writer, path string) (io.Writer, error) {
	if path == "" {
		return stderr, nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return stderr, fmt.Errorf("open log file %q: %w", path, err)
	}
	return io.MultiWriter(stderr, f), nil
}

// RunArgs is the parsed CLI input for `codex-app-gateway env-mcp`.
// Per the 2026-05-16 fixed-tools redesign, env-mcp is workspace-scoped
// rather than per-executor; one child binary handles every executor in
// the workspace via environment_id routing.
type RunArgs struct {
	WorkspaceID    string // --workspace-id
	ExecGatewayURL string // --exec-gateway-url; pool appends /<exe_id> (ws://)
	// AppGatewayInternal is the http://127.0.0.1:<port> loopback URL.
	// Used by scheduling tools (which still go through the loopback
	// proxy → agentserver-main's scheduled-tasks API). list_environments
	// used to go through this too via /internal/connected; as of
	// 2026-06-14 list_environments calls codex-exec-gateway directly
	// with the workspace cap-token (see ExecGatewayInternalURL).
	AppGatewayInternal string // --app-gateway-internal
	WorkspaceTokenEnv  string // --workspace-token-env (workspace-scoped cap token)
	LoopbackTokenEnv   string // --loopback-token-env (still used by scheduling)
	// LogFile, when non-empty, is opened in append mode and added to the
	// logger's writer alongside stderr. Codex pipes MCP-child stderr into
	// its own buffer where it is invisible from outside the codex process,
	// so without this knob env-mcp is effectively logless from an ops
	// standpoint. The file lives under the per-workspace CODEX_HOME so it
	// is co-located with the rest of the subprocess state and reaped with
	// the subprocess.
	LogFile string // --log-file (optional)
	// ExecGatewayInternalURL is the http(s):// base for codex-exec-gateway's
	// internal API. Required as of 2026-06-14: list_environments calls
	// <base>/api/exec-gateway/connected with the workspace cap-token, and
	// copy_path's HTTP relay path POSTs to <base>/api/exec-gateway/relay/create
	// (still optional for relay — copy_path falls back to the ws cat-pump if
	// ExecGatewayInternalSecret is empty; the connected call always uses it).
	ExecGatewayInternalURL    string // --exec-gateway-internal-url
	ExecGatewayInternalSecret string // --exec-gateway-internal-secret-env (env var name; value injected by gateway)
}

// Run constructs the bridge.Pool, builds the tool registry, and serves
// the MCP loop on stdin/stdout until EOF or context cancellation.
//
// stdout is the MCP JSON-RPC stream; do not write to it from outside
// MCPServer.Serve. Diagnostic output flows through logger (gateway
// supervisor pipes our stderr into the pod's stderr with a
// `[codex-subproc]` prefix). The `stderr` parameter is reserved for
// future direct writes (e.g., panic dumps) and currently unused.
func Run(ctx context.Context, args RunArgs, stdin io.Reader, stdout, stderr io.Writer, logger *slog.Logger) error {
	_ = stderr
	wsToken := os.Getenv(args.WorkspaceTokenEnv)
	if wsToken == "" {
		return fmt.Errorf("env var %s is empty; cannot authenticate to bridge", args.WorkspaceTokenEnv)
	}
	lbToken := os.Getenv(args.LoopbackTokenEnv)
	if lbToken == "" {
		return fmt.Errorf("env var %s is empty; cannot authenticate to app-gateway loopback", args.LoopbackTokenEnv)
	}
	if args.WorkspaceID == "" || args.ExecGatewayURL == "" || args.AppGatewayInternal == "" || args.ExecGatewayInternalURL == "" {
		return fmt.Errorf("env-mcp: workspace-id, exec-gateway-url, app-gateway-internal, exec-gateway-internal-url all required")
	}
	// ExecGatewayInternalSecret is optional — if its env var holds a value,
	// copy_path can use the HTTPS relay path; otherwise it falls back to
	// the ws cat-pump. We resolve here so the tool sees the value directly.
	var execGwSecret string
	if args.ExecGatewayInternalSecret != "" {
		execGwSecret = os.Getenv(args.ExecGatewayInternalSecret)
	}

	logger.Info("env-mcp starting",
		"workspace_id", args.WorkspaceID,
		"exec_gateway_url", args.ExecGatewayURL,
		"app_gateway_internal", args.AppGatewayInternal,
		"exec_gateway_internal_url", args.ExecGatewayInternalURL,
		"http_relay_enabled", args.ExecGatewayInternalURL != "" && execGwSecret != "",
	)

	pool := bridge.NewPool(args.ExecGatewayURL, wsToken, logger)
	defer pool.Close()

	sessions := tools.NewSessionStore()
	// Direct call to codex-exec-gateway with the workspace cap-token
	// — workspace_id is encoded in the HMAC-signed token payload, so
	// the endpoint authenticates and identifies the workspace in one
	// step. The pre-2026-06-14 design routed this through the
	// app-gateway loopback /internal/connected handler, which then
	// reverse-looked-up workspace_id from a per-spawn loopback token
	// and forwarded with the cluster-shared secret — that extra hop
	// existed only because the exec-gateway endpoint hadn't yet been
	// taught to accept cap-tokens.
	connectedURL := strings.TrimRight(args.ExecGatewayInternalURL, "/") + "/api/exec-gateway/connected"
	resolver := nameresolver.NewResolver(connectedURL, wsToken, logger)

	relayClient := bridge.NewRelayClient(args.ExecGatewayInternalURL, execGwSecret, args.WorkspaceID, logger)
	toolList := []tools.Tool{
		tools.NewListEnvironmentsTool(resolver),
		tools.NewShellTool(pool, resolver),
		tools.NewUnifiedExecTool(pool, sessions, resolver),
		tools.NewWriteStdinTool(pool, sessions),
		tools.NewReadOutputTool(pool, sessions),
		tools.NewTerminateTool(pool, sessions),
		tools.NewReadFileTool(pool, resolver),
		tools.NewApplyPatchTool(pool, resolver),
		tools.NewCopyPathTool(pool, resolver, relayClient),
	}
	// Register scheduling tools — forward via loopback to app-gateway which
	// then proxies to agentserver-main's internal workspace-scoped endpoints.
	schedTransport := scheduling.NewLoopbackTransport(
		strings.TrimRight(args.AppGatewayInternal, "/")+"/internal/scheduled-tasks",
		lbToken,
	)
	toolList = append(toolList, scheduling.NewSchedulingTools(schedTransport)...)
	srv := NewMCPServer("agentserver", toolList, logger)
	if err := srv.Serve(ctx, stdin, stdout); err != nil {
		return fmt.Errorf("mcp serve: %w", err)
	}
	logger.Info("env-mcp clean exit (stdin closed)")
	return nil
}
