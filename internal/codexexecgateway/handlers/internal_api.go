package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/execmodel"
)

// InternalConnectedStore is the subset of storage required by Connected.
type InternalConnectedStore interface {
	ConnectedExecutorsForWorkspace(ctx context.Context, workspaceID string, connectedIDs []string) ([]execmodel.ConnectedExecutor, error)
}

// Registry is satisfied by *codexexecgateway.ConnRegistry.
type Registry interface {
	ConnectedIDs() []string
}

// Connected returns the intersection of (workspace's bound executors) ∩
// (currently-connected exe_ids). Called directly by env-mcp's in-pod
// nameresolver — see internal/codexappgateway/envmcp/envmcp.go and
// internal/envtools/nameresolver. Used to populate the LLM-facing
// list_environments tool output.
//
// Workspace id comes from the cap-token claims (set by
// handlers.RequireCapToken) — NOT from a query-string parameter. The
// token is HMAC-signed, so the workspace_id is cryptographically bound
// to a valid bearer. The pre-2026-06-14 design accepted ?workspace_id=
// alongside a shared-secret bearer; that was forgeable by any holder
// of the shared secret and required a loopback proxy hop just to
// inject the workspace_id from an opaque per-spawn token. Both are now
// gone — see the loopback-removal PR for the full rationale.
func Connected(store InternalConnectedStore, reg Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := CapTokenClaimsFromContext(r.Context())
		if !ok || claims.WorkspaceID == "" {
			// Should be unreachable: RequireCapToken guarantees claims
			// or rejects with 401 before reaching us. Defensive 500 so
			// a future middleware-chain change can't silently let a
			// no-workspace request through.
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "missing cap-token claims"})
			return
		}
		ids := reg.ConnectedIDs()
		rows, err := store.ConnectedExecutorsForWorkspace(r.Context(), claims.WorkspaceID, ids)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list"})
			return
		}
		if rows == nil {
			rows = []execmodel.ConnectedExecutor{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rows) //nolint:errcheck
	}
}

// RevokedAdder is satisfied by *codexexecgateway.RevokedSet.
type RevokedAdder interface {
	Add(turnID string, exp int64) (evictedLive bool)
}

type revokeRequest struct {
	TurnID string `json:"turn_id"`
	Exp    int64  `json:"exp"`
}

// RevokeTurn adds a turn_id to the in-memory revoked set so future bridge
// connect attempts presenting that turn's CODEX_EXEC_GATEWAY_TOKEN are
// rejected even within the token's exp window.
func RevokeTurn(rev RevokedAdder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req revokeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		if req.TurnID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "turn_id required"})
			return
		}
		// If caller omits exp, default to "1 hour from now" (spec turn slack).
		if req.Exp == 0 {
			req.Exp = timeNowUnix() + 3600
		}
		if evictedLive := rev.Add(req.TurnID, req.Exp); evictedLive {
			slog.Warn("revoke-turn: revoked-set at capacity, evicted a still-live revocation; previously-revoked token may be usable until its own expiry",
				"turn_id", req.TurnID)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// timeNowUnix exists as a small indirection so tests could later swap time.
func timeNowUnix() int64 { return time.Now().Unix() }
