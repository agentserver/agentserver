package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/agentserver/agentserver/internal/db"
)

func strictBearerToken(r *http.Request) (string, bool) {
	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, "Bearer ") {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	if token == "" {
		return "", false
	}
	return token, true
}

// handleAgentWhoami returns the identity represented by a sandbox proxy_token.
//
// The endpoint verifies identity only: a valid proxy_token whose
// user_id is non-null and whose user is still a workspace member
// returns 200. The sandbox's current runtime status (creating / running /
// pausing / paused / resuming / offline / deleting) is reported in the
// response body as SandboxStatus, but it does NOT influence the HTTP
// status code. See issue #290 for the rationale: callers (notably
// observer-server) were treating whoami as a liveness probe, which
// turned every routine tunnel disconnect into a hard auth failure
// for the next caller — a deadlock reachable through ordinary
// `serve-mcp` exit. Identity is identity; runtime state belongs in
// the body.
//
// GET /api/agent/whoami
//
//	@Summary   Inspect the calling agent identity (proxy_token auth)
//	@Tags      Agent
//	@Produce   json
//	@Success   200  {object}  AgentWhoamiResponse
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "identity not valid for this token (missing user binding or workspace membership)"
//	@Failure   500  {string}  string  "internal error"
//	@Router    /api/agent/whoami [get]
func (s *Server) handleAgentWhoami(w http.ResponseWriter, r *http.Request) {
	token, ok := strictBearerToken(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	who, state, err := s.DB.GetAgentWhoamiByProxyToken(token)
	if err != nil {
		log.Printf("agent whoami: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	switch state {
	case db.AgentWhoamiUnknown:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	case db.AgentWhoamiForbidden:
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if who == nil {
		// Defensive: AgentWhoamiOK guarantees a non-nil row, but if the
		// invariant ever changes we'd rather 500 than panic on nil deref.
		log.Printf("agent whoami: nil identity with state=ok")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(AgentWhoamiResponse{
		UserID:        who.UserID,
		WorkspaceID:   who.WorkspaceID,
		WorkspaceName: who.WorkspaceName,
		SandboxID:     who.SandboxID,
		ShortID:       who.ShortID,
		DisplayName:   who.DisplayName,
		Role:          who.Role,
		SandboxStatus: who.SandboxStatus,
	})
}
