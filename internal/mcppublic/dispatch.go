package mcppublic

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"

	"github.com/agentserver/agentserver/internal/envtools/tools"
)

// jsonrpcErr is a minimal JSON-RPC error envelope. Defined locally
// (rather than importing envtools/bridge.JSONRPCError) so the public
// gateway's transport layer can stay free of the in-pod WebSocket
// bridge package; the wire shape is the JSON-RPC 2.0 standard.
type jsonrpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSON-RPC error codes the dispatcher emits. The spec-defined codes
// (-32601, -32603) are used for protocol-level problems; the
// implementation-defined range (-32000..-32099) carries authorization
// and routing failures so clients can surface them distinctly.
const (
	codeMethodNotFound = -32601
	codeInternal       = -32603

	codeAuthMissing       = -32001 // no principal in ctx — shouldn't happen post-middleware
	codeForbiddenTool     = -32002 // principal scope doesn't include this tool
	codeUpstreamUnavail   = -32010 // executor source / bridge dial / etc unreachable
	codeToolExecutionFail = -32011 // tool returned a hard error
)

// ToolBackend is the boundary between the dispatcher's routing logic
// and the actual transport that talks to executors. PR E ships the
// dispatcher with a stub-friendly interface; PR F wires the production
// implementation that maintains per-Principal toolkits (bridge.Pool +
// in-pod nameresolver + sessions + envtools/tools instances, built
// once and reused).
//
// Implementations:
//   - must be safe for concurrent use
//   - receive the workspace_id from the principal (post the
//     2026-06-15 amendment that pins each PAT to one workspace) plus
//     a freshly-minted cap-token; resolution of environment_id →
//     exe_id is the backend's job (handled by the embedded in-pod
//     nameresolver — names are unique per workspace, no qualifier
//     dance needed)
type ToolBackend interface {
	// Call executes toolName against an executor in the principal's
	// workspace. rawArgs are the MCP tools/call arguments as supplied
	// by the client; the backend's embedded nameresolver translates
	// the environment_id field internally before dialing /bridge.
	Call(ctx context.Context, in ToolBackendCall) (tools.MCPCallToolResult, error)
}

// ToolBackendCall packages everything ToolBackend.Call needs. Kept as a
// struct so future fields (per-tool quotas, audit hooks) don't break
// the interface signature.
//
// No ExeID field: name → exe_id resolution lives inside the backend
// (it has the in-pod nameresolver) since one Principal = one workspace
// = no cross-workspace disambiguation work for the dispatcher to do.
type ToolBackendCall struct {
	Tool      string
	CapToken  string
	RawArgs   json.RawMessage
	Principal *Principal
}

// PublicToolMeta carries the metadata the gateway returns for a single
// MCP tool. Sourced directly from envtools/tools.Tool so the public
// surface stays byte-identical to the in-pod env-mcp's tools/list.
type PublicToolMeta struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// Dispatcher is the transport-agnostic core of the public MCP gateway.
// It handles the three RPC methods the gateway actually serves:
// initialize, tools/list, tools/call (plus the no-op
// notifications/initialized). PR F's Streamable HTTP transport calls
// into these methods and serialises the results onto the wire.
//
// Authorization gates: tools/list filters by the principal's tool
// scope; tools/call refuses any tool the principal lacks the scope
// for. Workspace ownership is intrinsic to the Principal (one PAT,
// one workspace) — no cross-workspace lookups, no @workspace_id
// qualifier handling, no WorkspaceCache.
type Dispatcher struct {
	// Executors lists workspace executors for list_environments.
	// Called once per list_environments call (with the principal's
	// workspace_id). The backend has its own nameresolver-fed-by-
	// ExecutorsSource for tools/call name resolution.
	Executors ExecutorsSource
	Minter    *CapMinter
	Backend   ToolBackend
	ToolMeta  []PublicToolMeta // sorted; the order is the tools/list order
	Logger    *slog.Logger

	// ServerName / ServerVersion populate initialize. Default to
	// "agentserver-mcp" / "0.1" when zero so tests don't need to set
	// them and prod can override via NewDispatcher options.
	ServerName    string
	ServerVersion string
}

// NewDispatcher constructs a dispatcher with sensible defaults.
// ToolMeta is taken from the supplied tool registry; for production
// use, pass DefaultPublicToolMeta which mirrors the in-pod env-mcp's
// tool surface. For tests, pass a smaller list.
func NewDispatcher(executors ExecutorsSource, minter *CapMinter, backend ToolBackend, meta []PublicToolMeta, logger *slog.Logger) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	// Defensive copy + sort so the gateway's tools/list output is
	// stable regardless of map iteration order in the caller.
	cp := make([]PublicToolMeta, len(meta))
	copy(cp, meta)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Name < cp[j].Name })
	return &Dispatcher{
		Executors:     executors,
		Minter:        minter,
		Backend:       backend,
		ToolMeta:      cp,
		Logger:        logger,
		ServerName:    "agentserver-mcp",
		ServerVersion: "0.1",
	}
}

// Initialize returns the result of an MCP initialize request. The
// protocol version matches the in-pod env-mcp (2025-06-18); the public
// gateway speaks the same MCP profile as the rest of the stack so
// behavior across both surfaces stays consistent.
func (d *Dispatcher) Initialize() tools.MCPInitializeResult {
	return tools.MCPInitializeResult{
		ProtocolVersion: "2025-06-18",
		Capabilities:    map[string]any{"tools": map[string]any{}},
		ServerInfo:      tools.MCPServerInfo{Name: d.ServerName, Version: d.ServerVersion},
	}
}

// ToolsList returns tools/list filtered to the tools the principal is
// allowed to invoke. An anonymous (nil) principal sees an empty list —
// callers ahead of the dispatcher (the auth middleware) should
// guarantee a non-nil principal, but defending here means a transport
// bug in PR F can't accidentally publish the full tool catalog to an
// unauthenticated client.
func (d *Dispatcher) ToolsList(p *Principal) tools.MCPListToolsResult {
	if p == nil {
		return tools.MCPListToolsResult{Tools: []tools.MCPTool{}}
	}
	out := make([]tools.MCPTool, 0, len(d.ToolMeta))
	for _, t := range d.ToolMeta {
		if !p.HasTool(t.Name) {
			continue
		}
		out = append(out, tools.MCPTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return tools.MCPListToolsResult{Tools: out}
}

// ToolsCall dispatches one tools/call. Returns either the tool result
// (which may itself be an in-band error via MCPCallToolResult.IsError)
// or a jsonrpcErr to be surfaced as a JSON-RPC error response.
//
// Routing:
//   - list_environments is handled in-process from ExecutorsSource;
//     never reaches the backend. The view returned to the client is
//     the LLM-facing shape — same as the in-pod nameresolver.LLMView
//     output, so a client moving between in-pod env-mcp and the
//     public gateway sees consistent output.
//   - every other tool: auth-check, mint a cap-token for the
//     principal's workspace, hand off to ToolBackend. Name → exe_id
//     resolution + bridge dial happens inside the backend.
func (d *Dispatcher) ToolsCall(ctx context.Context, p *Principal, params tools.MCPCallToolParams) (*tools.MCPCallToolResult, *jsonrpcErr) {
	if p == nil {
		return nil, &jsonrpcErr{Code: codeAuthMissing, Message: "authentication required"}
	}
	if _, known := d.findToolMeta(params.Name); !known {
		return nil, &jsonrpcErr{Code: codeMethodNotFound, Message: "unknown tool: " + params.Name}
	}
	if !p.HasTool(params.Name) {
		return nil, &jsonrpcErr{Code: codeForbiddenTool, Message: "tool " + params.Name + " not granted to this principal"}
	}

	if params.Name == "list_environments" {
		return d.handleListEnvironments(ctx, p)
	}

	tok, err := d.Minter.MintForPrincipal(p, p.WorkspaceID)
	if err != nil {
		d.Logger.Error("mcppublic: cap-token mint failed",
			"user_id", p.UserID, "workspace_id", p.WorkspaceID, "err", err)
		return nil, &jsonrpcErr{Code: codeInternal, Message: "cap-token mint failed"}
	}

	res, err := d.Backend.Call(ctx, ToolBackendCall{
		Tool:      params.Name,
		CapToken:  tok,
		RawArgs:   params.Arguments,
		Principal: p,
	})
	if err != nil {
		// Log the full underlying error server-side (includes the
		// dialer's TCP error which may name internal pod IPs, the
		// bridge URL, etc.) but return only an opaque message to the
		// public client. Including err.Error() in the wire response
		// would leak our cluster topology (e.g. "dial tcp
		// 10.0.5.7:6060: connection refused" → internal CIDR
		// disclosure) to anyone holding a valid PAT.
		d.Logger.Warn("mcppublic: tool backend error",
			"tool", params.Name, "workspace_id", p.WorkspaceID, "err", err)
		return nil, &jsonrpcErr{
			Code:    codeToolExecutionFail,
			Message: params.Name + ": tool execution failed",
		}
	}
	return &res, nil
}

// handleListEnvironments returns the principal's workspace's
// executors in the same JSON shape the in-pod
// nameresolver.LLMView produces — name, description, is_default,
// last_seen. exe_id is never on the wire (the backend resolves names
// internally; the LLM has no need for it).
//
// Single-workspace simplification (post 2026-06-15 amendment): no
// cross-workspace union, no duplicate detection, no @workspace_id
// qualifier. Just one workspace, one flat list.
func (d *Dispatcher) handleListEnvironments(ctx context.Context, p *Principal) (*tools.MCPCallToolResult, *jsonrpcErr) {
	rows, err := d.Executors.ListWorkspaceExecutors(ctx, p.WorkspaceID)
	if err != nil {
		// Same redaction rationale as ToolsCall's backend-error path:
		// upstream errors can name internal hostnames (HTTPExecutors
		// hits an in-cluster Service). Log full server-side, return
		// generic to the client.
		d.Logger.Warn("mcppublic: list_environments upstream error",
			"workspace_id", p.WorkspaceID, "err", err)
		return nil, &jsonrpcErr{Code: codeUpstreamUnavail, Message: "list_environments: upstream unavailable"}
	}
	type llmEntry struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		IsDefault   bool   `json:"is_default,omitempty"`
		LastSeen    string `json:"last_seen,omitempty"`
	}
	out := make([]llmEntry, 0, len(rows))
	for _, e := range rows {
		out = append(out, llmEntry{
			Name:        e.Name,
			Description: e.Description,
			IsDefault:   e.IsDefault,
			LastSeen:    e.LastSeenISO,
		})
	}
	// Stable ordering — easier to read in logs, and a few clients
	// hash the response for cache-key purposes.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	body, err := json.Marshal(out)
	if err != nil {
		d.Logger.Error("mcppublic: marshal list_environments failed",
			"workspace_id", p.WorkspaceID, "err", err)
		return nil, &jsonrpcErr{Code: codeInternal, Message: "internal error"}
	}
	return &tools.MCPCallToolResult{
		Content: []tools.MCPToolContent{{Type: "text", Text: string(body)}},
	}, nil
}

// findToolMeta returns the metadata for name (and whether it exists).
// Linear scan over d.ToolMeta is fine — the surface is fixed at ~9
// entries; a map would save nothing measurable and complicate the
// stable-ordering guarantee for tools/list.
func (d *Dispatcher) findToolMeta(name string) (PublicToolMeta, bool) {
	for _, t := range d.ToolMeta {
		if t.Name == name {
			return t, true
		}
	}
	return PublicToolMeta{}, false
}
