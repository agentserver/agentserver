package server

import "strings"

// MCP PAT scope catalog. Spec'd in
// docs/superpowers/specs/2026-06-09-envmcp-public-gateway-design.md § 4.3
// (post 2026-06-15 amendment).
//
// Two static capability scopes:
//   - mcp:read   — read_file, list_environments
//   - mcp:exec   — shell, exec_command, apply_patch, write_stdin,
//                  read_output, terminate, copy_path
//
// The pre-amendment draft also had a dynamic `workspace:<ws_id>`
// scope family; that's gone. Workspace binding is now intrinsic to
// the PAT row (mcp_pats.workspace_id NOT NULL) and PAT creation is a
// workspace-scoped endpoint, so callers can't even express
// "workspace X" as a scope — the URL says which workspace.
const (
	MCPScopeRead = "mcp:read"
	MCPScopeExec = "mcp:exec"
)

// MCPPATScope is one entry in the catalog returned by GET .../scopes.
type MCPPATScope struct {
	Name        string
	Description string
	Available   bool
}

// mcpPATScopeCatalog enumerates the static (capability) scopes the
// SPA presents as checkboxes in the mint modal.
var mcpPATScopeCatalog = []MCPPATScope{
	{Name: MCPScopeRead, Description: "Read-only MCP tools: read_file, list_environments", Available: true},
	{Name: MCPScopeExec, Description: "Execution MCP tools: shell, exec_command, apply_patch, write_stdin, read_output, terminate, copy_path. Grants full command execution on the user's executors.", Available: true},
}

// validateMCPPATScopes returns an error if scopes are empty or
// reference an unknown/unavailable scope. The post-2026-06-15 catalog
// has no dynamic workspace scopes, so there is no DB hit on this path
// — validation is a fast in-memory set lookup.
//
// Defensive check: an explicit "workspace:..." in the request now
// rejects, so any old client paste-job surfaces immediately rather
// than silently dropping the workspace context.
func (s *Server) validateMCPPATScopes(requested []string) error {
	if len(requested) == 0 {
		return errInvalidScope("at least one scope required")
	}
	allowed := map[string]bool{}
	for _, sc := range mcpPATScopeCatalog {
		if sc.Available {
			allowed[sc.Name] = true
		}
	}
	for _, r := range requested {
		switch {
		case allowed[r]:
			// OK
		case strings.HasPrefix(r, "workspace:"):
			return errInvalidScope("workspace:<id> scope is no longer supported; workspace is implicit in the PAT URL")
		default:
			return errInvalidScope("scope not available: " + r)
		}
	}
	return nil
}
