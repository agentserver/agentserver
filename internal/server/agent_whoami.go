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

func activeWhoamiSandboxStatus(status string) bool {
	return status == "creating" || status == "running"
}

// handleAgentWhoami returns the identity represented by a sandbox proxy_token.
// GET /api/agent/whoami
//
//	@Summary   Inspect the calling agent identity (proxy_token auth)
//	@Tags      Agent
//	@Produce   json
//	@Success   200  {object}  AgentWhoamiResponse
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "forbidden"
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
	if who == nil || !activeWhoamiSandboxStatus(who.SandboxStatus) {
		http.Error(w, "forbidden", http.StatusForbidden)
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
	})
}
