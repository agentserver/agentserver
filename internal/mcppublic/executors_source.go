package mcppublic

import "context"

// ExecutorEntry is the public-gateway view of one workspace_executors
// row. Carries the binding-level name + description (per v0.54.0,
// surfaced to the LLM through list_environments) plus the executor's
// last_seen_at for staleness reporting.
//
// Defined here (not in envtools/tools or codexexecgateway) so the
// public gateway has a single, narrow ExecutorsSource interface to
// stub in tests. The HTTP-backed production implementation lives in
// PR F alongside the rest of the transport wiring.
//
// Note: WorkspaceID is intentionally absent — the principal carries
// exactly one workspace (post the 2026-06-15 amendment) and the
// source is asked per-call which workspace to list. The ExeID stays
// because it's used as the bridge dial path and the audit key.
type ExecutorEntry struct {
	ExeID       string
	Name        string
	Description string
	IsDefault   bool
	// LastSeenISO is the executor's last_seen_at timestamp formatted as
	// RFC3339. Empty means "never seen / never connected", which the
	// MCP-facing view should report as such rather than fabricating a
	// zero time.
	LastSeenISO string
}

// ExecutorsSource is the upstream the public gateway queries to
// enumerate executors in one workspace. Two reasons for the interface:
//
//   - PR F wires the production impl (HTTP fanout to codex-exec-gateway's
//     internal API, secret-authenticated). Keeping it behind an interface
//     means PR E lands the dispatch logic without depending on either
//     codexexecgateway internals or a live gateway pod.
//   - Unit tests for dispatch.go can supply a deterministic fake without
//     standing up postgres or HTTP servers.
//
// Implementations must be safe for concurrent use.
//
// Single-workspace contract: post the 2026-06-15 amendment, each
// Principal owns exactly one workspace. The dispatcher calls this
// once per request (with the principal's workspace_id) and feeds the
// result to the in-pod nameresolver, which caches name → exe_id
// internally. No cross-workspace union; no @workspace_id qualifier.
type ExecutorsSource interface {
	// ListWorkspaceExecutors returns the executors bound to the
	// supplied workspace. Order is implementation-defined; the
	// dispatcher's nameresolver doesn't depend on it.
	ListWorkspaceExecutors(ctx context.Context, workspaceID string) ([]ExecutorEntry, error)
}
