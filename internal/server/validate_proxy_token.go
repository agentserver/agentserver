package server

import (
	"encoding/json"
	"log"
	"net/http"
)

// handleValidateProxyToken is an internal API for the LLM proxy to validate
// sandbox proxy tokens. It returns sandbox metadata without requiring cookie auth.
func (s *Server) handleValidateProxyToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProxyToken string `json:"proxy_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProxyToken == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	sbx, err := s.DB.GetSandboxByProxyToken(req.ProxyToken)
	if err != nil {
		log.Printf("validate-proxy-token: db error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if sbx == nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"sandbox_id":   sbx.ID,
		"workspace_id": sbx.WorkspaceID,
		"status":       sbx.Status,
	})
}
