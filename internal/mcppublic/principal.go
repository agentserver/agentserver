// Package mcppublic implements the envmcp public gateway — the bridge
// between external MCP clients (Claude Desktop / Codex Desktop / etc.)
// and the in-cluster envtools/tools surface that talks to user-registered
// executors via codex-exec-gateway.
//
// This file defines the authorization principal — the resolved identity
// that downstream tool dispatch authorizes against — and the resolver
// interface that adapts incoming bearer credentials into one. Phase 1
// resolves PATs (Personal Access Tokens, agpat_…); Phase 2 will add an
// OAuth introspection resolver that produces the same Principal shape.
//
// Spec: docs/superpowers/specs/2026-06-09-envmcp-public-gateway-design.md
// §§ 4.3 (PAT) and 4.4 (token exchange to cap-token), plus the
// 2026-06-15 amendment that hard-binds each PAT to exactly one
// workspace — the WorkspaceID field below reflects that.
package mcppublic

import "context"

// Principal is the resolved identity behind an incoming MCP request.
// All authorization decisions in the gateway derive from this struct;
// downstream code is auth-method-agnostic.
//
// Field semantics:
//   - UserID is the agentserver user id; required, non-empty.
//   - WorkspaceID is the single workspace this principal can access
//     (post 2026-06-15 amendment: PATs are workspace-scoped at the
//     table level, so the principal carries exactly one workspace id;
//     OAuth-derived principals in Phase 2 will be issued the same way,
//     one token per workspace via RFC 8707 resource indicators).
//   - Tools is the set of envmcp tool names the principal can call.
//     Resolved from scopes: mcp:read → ToolsRead, mcp:exec → ToolsExec.
//     Empty means "none allowed" (which would 403 every tools/call —
//     defensive default in case a resolver forgets to populate it).
//   - PATId is the agpat_… id when the principal came from a PAT;
//     "" otherwise. Carried through to the audit log so a leaked PAT
//     can be tracked back to its specific row.
//   - OAuthSub is the OAuth subject identifier when the principal came
//     from an introspected access token; "" otherwise. Same audit-log
//     purpose as PATId.
type Principal struct {
	UserID      string
	WorkspaceID string
	Tools       map[string]struct{}
	PATId       string
	OAuthSub    string
}

// HasWorkspace reports whether the principal can access the workspace.
// Trivial post-amendment — the principal owns exactly one — but kept
// as a method so callers don't sprinkle `p.WorkspaceID == wid` checks
// (and so the OAuth Phase 2 path can swap the body if it ends up
// needing a richer check, without rippling through callers).
func (p *Principal) HasWorkspace(wsID string) bool {
	if p == nil {
		return false
	}
	return p.WorkspaceID == wsID
}

// HasTool reports whether the principal can invoke the named tool.
// Tool names match the envmcp tool surface (shell, read_file, etc.).
func (p *Principal) HasTool(name string) bool {
	if p == nil {
		return false
	}
	_, ok := p.Tools[name]
	return ok
}

// ToolsRead is the tool set granted by the mcp:read scope. These are
// the side-effect-free tools — listing executors and reading file
// contents. Kept in this file (not envtools/tools/) so the gateway's
// authorization surface is greppable in one place.
//
// list_environments is a special case: it does not read any executor —
// it queries the workspace_executors table — but it's the necessary
// discovery step for everything else, so it lives in mcp:read.
var ToolsRead = map[string]struct{}{
	"list_environments": {},
	"read_file":         {},
}

// ToolsExec is the tool set granted by the mcp:exec scope. These tools
// can mutate executor state arbitrarily (shell command execution, file
// writes, process control, cross-executor file transfer). Spec § 4.7
// calls out that this should NOT be the default scope — users must
// explicitly opt in.
//
// Tool name source-of-truth note: the wire names below match what the
// underlying envtools/tools.Tool.Name() actually returns. In particular
// the long-form session tool is `exec_command`, not `unified_exec` —
// the type is named UnifiedExecTool but it has always returned the
// shorter wire name (carried over from codex's pre-rename API). The
// public gateway's tools/list / tools/call surface follows what the
// LLM client actually sees, so keep these strings aligned with the
// Tool.Name() returns. copy_path was added 2026-05-18 (post the
// 2026-06-09 spec draft's "8 tools" list); it's an exec-scope tool by
// nature (writes files on user-registered executors).
var ToolsExec = map[string]struct{}{
	"shell":        {},
	"exec_command": {},
	"write_stdin":  {},
	"read_output":  {},
	"terminate":    {},
	"apply_patch":  {},
	"copy_path":    {},
}

// PrincipalResolver turns an opaque bearer credential into a Principal,
// or returns an error if the credential is malformed / expired / unknown.
// Implementations are stateless from the caller's perspective; any
// caching of DB lookups is the resolver's internal concern.
//
// The middleware dispatches to a resolver based on token prefix
// (agpat_… → PATResolver; Phase 2: any other prefix → OAuthResolver).
type PrincipalResolver interface {
	// Resolve validates raw (the full bearer credential as presented by
	// the client) and returns the derived Principal. Returns ErrUnknown
	// if the credential is malformed/unrecognized; ErrInvalid if it
	// parses but doesn't authenticate (revoked, expired, hash mismatch).
	// Other errors are surfaced as 500.
	Resolve(ctx context.Context, raw string) (*Principal, error)
}

// ctxKey is the context key used to plumb the resolved Principal from
// middleware to handlers. Unexported so external packages can't collide.
type ctxKey struct{}

// WithPrincipal returns a derived context carrying p. Set by the auth
// middleware before the handler chain runs; nil is permitted (means
// "anonymous", which every gated handler rejects).
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// PrincipalFromContext returns the Principal plumbed by the middleware,
// or nil if none. Handlers MUST check for nil before using.
func PrincipalFromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}
