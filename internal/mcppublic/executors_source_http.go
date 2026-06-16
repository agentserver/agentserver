package mcppublic

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/agentserver/agentserver/internal/server"
)

// HTTPExecutorsSource is the production ExecutorsSource. Calls
// codex-exec-gateway's internal "list workspace executors" endpoint
// (via the existing server.ExecutorsClient surface) for one workspace
// at a time.
//
// Authorization model: the underlying endpoint is gated by the
// X-Internal-Secret shared between agentserver and codex-exec-gateway
// (RequireAgentserverSecret middleware); per v0.54.x it does NOT
// consult X-User-Id for listing. The per-principal workspace ACL
// already happens upstream: the Principal carries exactly one
// workspace_id (post the 2026-06-15 amendment), and the dispatcher
// only calls this with that one id. The source has no opportunity
// to leak across workspaces by construction.
//
// Single-workspace contract: post the 2026-06-15 amendment, no
// fan-out — the old draft of this file ran an errgroup over a
// principal's whole workspace set. With one PAT = one workspace,
// the call is a single HTTP request per cache-miss.
type HTTPExecutorsSource struct {
	Client *server.ExecutorsClient
}

// NewHTTPExecutorsSource wraps an ExecutorsClient. One instance is
// shared across all principals — the per-workspace ACL is the
// dispatcher's job (Principal.WorkspaceID), not the source's. The
// ExecutorsClient itself already carries the shared internal secret
// needed to call the gateway's internal API.
func NewHTTPExecutorsSource(client *server.ExecutorsClient) (*HTTPExecutorsSource, error) {
	if client == nil {
		return nil, fmt.Errorf("mcppublic: ExecutorsClient is required")
	}
	return &HTTPExecutorsSource{Client: client}, nil
}

// ListWorkspaceExecutors returns the executors bound to workspaceID.
// userID is empty: the ListBinding handler on the gateway side
// doesn't consult X-User-Id (only the shared secret). Passing the
// resolved Principal's UserID would be a no-op here and would also
// paper over an audit-attribution question the spec wants answered
// at the per-call layer (cap-token UserID), not the per-listing
// layer.
func (s *HTTPExecutorsSource) ListWorkspaceExecutors(ctx context.Context, workspaceID string) ([]ExecutorEntry, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("mcppublic: empty workspaceID")
	}
	rows, err := s.Client.List(ctx, "", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspace %s: %w", workspaceID, err)
	}
	out := make([]ExecutorEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, ExecutorEntry{
			ExeID:       row.ExeID,
			Name:        row.Name,
			Description: row.Description,
			IsDefault:   row.IsDefault,
			LastSeenISO: isoOrEmpty(row.LastSeenAt),
		})
	}
	// Deterministic order so two callers with the same workspace
	// observe the same snapshot iteration order — keeps the in-pod
	// nameresolver cache iteration stable for tests and debugging.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func isoOrEmpty(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

