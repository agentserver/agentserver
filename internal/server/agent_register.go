package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/agentserver/agentserver/internal/shortid"
	"github.com/google/uuid"
)

// handleAgentRegister processes a CLI agent registration using an OAuth Bearer token.
// The token must contain workspace_id and agent:register scope from the Hydra consent flow.
//
//	@Summary   Register an agent (obtain sandbox credentials)
//	@Tags      Agent
//	@Accept    json
//	@Produce   json
//	@Param     body  body      AgentRegisterRequest  true  "Agent registration info"
//	@Success   201   {object}  AgentRegisterResponse
//	@Failure   400   {string}  string  "bad request"
//	@Failure   401   {string}  string  "unauthorized"
//	@Failure   403   {string}  string  "no permission"
//	@Failure   500   {string}  string  "internal error"
//	@Router    /api/agent/register [post]
func (s *Server) handleAgentRegister(w http.ResponseWriter, r *http.Request) {
	// Extract Bearer token.
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "missing or invalid Authorization header", http.StatusUnauthorized)
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")

	// Introspect token via Hydra.
	if s.HydraClient == nil {
		http.Error(w, "OAuth not configured", http.StatusServiceUnavailable)
		return
	}
	introspection, err := s.HydraClient.IntrospectToken(token)
	if err != nil {
		log.Printf("agent register: introspect token: %v", err)
		http.Error(w, "token introspection failed", http.StatusInternalServerError)
		return
	}
	if !introspection.Active {
		http.Error(w, "invalid or expired token", http.StatusUnauthorized)
		return
	}
	if !introspection.HasScope("agent:register") {
		http.Error(w, "insufficient scope: agent:register required", http.StatusForbidden)
		return
	}

	// Extract workspace_id from token claims.
	workspaceID, _ := introspection.Extra["workspace_id"].(string)
	if workspaceID == "" {
		http.Error(w, "token missing workspace_id claim", http.StatusBadRequest)
		return
	}
	userID := introspection.Subject

	// Verify workspace membership (defense in depth).
	role, err := s.DB.GetWorkspaceMemberRole(workspaceID, userID)
	if err != nil {
		log.Printf("agent register: check role: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if role == "" || role == "guest" {
		http.Error(w, "no permission to register agent in this workspace", http.StatusForbidden)
		return
	}

	// Parse request body.
	var req struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		req.Name = "Local Agent"
	}
	sandboxType := req.Type
	if sandboxType == "" {
		sandboxType = "opencode"
	}
	// jupyter sandboxes are created via POST /api/workspaces/{wid}/sandboxes only;
	// they don't self-register through this endpoint.
	if sandboxType != "opencode" && sandboxType != "claudecode" && sandboxType != "custom" {
		http.Error(w, "invalid type: must be opencode, claudecode, or custom", http.StatusBadRequest)
		return
	}

	// Create sandbox.
	sandboxID := uuid.New().String()
	tunnelToken, err := generatePassword()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	proxyToken, err := generatePassword()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var opencodePassword string
	if sandboxType == "opencode" {
		opencodePassword, err = generatePassword()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	sid := shortid.Generate()
	var createErr error
	for attempts := 0; attempts < 3; attempts++ {
		createErr = s.DB.CreateLocalSandbox(sandboxID, workspaceID, userID, req.Name, sandboxType, opencodePassword, proxyToken, tunnelToken, sid)
		if createErr == nil {
			break
		}
		sid = shortid.Generate()
	}
	if createErr != nil {
		log.Printf("agent register: create sandbox: %v", createErr)
		http.Error(w, "failed to register agent", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"sandbox_id":   sandboxID,
		"tunnel_token": tunnelToken,
		"proxy_token":  proxyToken,
		"workspace_id": workspaceID,
		"short_id":     sid,
	})
}
