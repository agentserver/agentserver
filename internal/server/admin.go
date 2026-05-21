package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/agentserver/agentserver/internal/auth"
)

// requireAdmin is a middleware that checks if the authenticated user has the admin role.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := auth.UserIDFromContext(r.Context())
		user, err := s.Auth.GetUserByID(userID)
		if err != nil || user == nil {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		if user.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

//	@Summary   List all users (admin)
//	@Tags      Admin
//	@Produce   json
//	@Success   200  {array}   AdminUserItem
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "admin role required"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/admin/users [get]
func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.DB.ListAllUsers()
	if err != nil {
		log.Printf("admin: failed to list users: %v", err)
		http.Error(w, "failed to list users", http.StatusInternalServerError)
		return
	}

	resp := make([]AdminUserItem, len(users))
	for i, u := range users {
		resp[i] = AdminUserItem{
			ID:        u.ID,
			Email:     u.Email,
			Name:      u.Name,
			Role:      u.Role,
			CreatedAt: u.CreatedAt.Format(time.RFC3339),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

//	@Summary   List all workspaces (admin)
//	@Tags      Admin
//	@Produce   json
//	@Success   200  {array}   AdminWorkspaceItem
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "admin role required"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/admin/workspaces [get]
func (s *Server) handleAdminListWorkspaces(w http.ResponseWriter, r *http.Request) {
	workspaces, err := s.DB.ListAllWorkspacesAdmin()
	if err != nil {
		log.Printf("admin: failed to list workspaces: %v", err)
		http.Error(w, "failed to list workspaces", http.StatusInternalServerError)
		return
	}

	rd := s.getResourceDefaults()

	resp := make([]AdminWorkspaceItem, len(workspaces))
	for i, ws := range workspaces {
		item := AdminWorkspaceItem{
			ID:           ws.ID,
			Name:         ws.Name,
			CreatedAt:    ws.CreatedAt.Format(time.RFC3339),
			UpdatedAt:    ws.UpdatedAt.Format(time.RFC3339),
			SandboxCount: ws.SandboxCount,
			MaxSandboxes: rd.MaxSandboxesPerWorkspace,
		}
		if ws.OwnerID != nil {
			ownerEmail := ""
			if ws.OwnerEmail != nil {
				ownerEmail = *ws.OwnerEmail
			}
			item.Owner = &AdminOwnerInfo{
				ID:      *ws.OwnerID,
				Email:   ownerEmail,
				Name:    ws.OwnerName,
				Picture: ws.OwnerPicture,
			}
		}
		// Check for workspace-level quota override.
		if wq, err := s.DB.GetWorkspaceQuota(ws.ID); err == nil && wq != nil && wq.MaxSandboxes != nil {
			item.MaxSandboxes = *wq.MaxSandboxes
		}
		resp[i] = item
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

//	@Summary   List all sandboxes (admin)
//	@Tags      Admin
//	@Produce   json
//	@Success   200  {array}   AdminSandboxItem
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "admin role required"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/admin/sandboxes [get]
func (s *Server) handleAdminListSandboxes(w http.ResponseWriter, r *http.Request) {
	sandboxes, err := s.DB.ListAllSandboxes()
	if err != nil {
		log.Printf("admin: failed to list sandboxes: %v", err)
		http.Error(w, "failed to list sandboxes", http.StatusInternalServerError)
		return
	}

	resp := make([]AdminSandboxItem, len(sandboxes))
	for i, sbx := range sandboxes {
		item := AdminSandboxItem{
			ID:          sbx.ID,
			Name:        sbx.Name,
			WorkspaceID: sbx.WorkspaceID,
			Type:        sbx.Type,
			Status:      sbx.Status,
			CreatedAt:   sbx.CreatedAt.Format(time.RFC3339),
			IsLocal:     sbx.IsLocal,
		}
		if sbx.LastActivityAt.Valid {
			s := sbx.LastActivityAt.Time.Format(time.RFC3339)
			item.LastActivityAt = &s
		}
		resp[i] = item
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

//	@Summary   Update a user's role (admin)
//	@Tags      Admin
//	@Accept    json
//	@Param     id    path  string                      true  "User ID"
//	@Param     body  body  AdminUpdateUserRoleRequest  true  "New role (user or admin)"
//	@Success   204  "updated"
//	@Failure   400  {string}  string  "bad request / invalid role"
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "admin role required"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/admin/users/{id}/role [put]
func (s *Server) handleAdminUpdateUserRole(w http.ResponseWriter, r *http.Request) {
	targetID := chi.URLParam(r, "id")

	var req AdminUpdateUserRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Role == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Role != "user" && req.Role != "admin" {
		http.Error(w, "invalid role: must be 'user' or 'admin'", http.StatusBadRequest)
		return
	}

	if err := s.DB.UpdateUserRole(targetID, req.Role); err != nil {
		log.Printf("admin: failed to update user role: %v", err)
		http.Error(w, "failed to update user role", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

//	@Summary   Get system-wide quota defaults (admin)
//	@Tags      Admin
//	@Produce   json
//	@Success   200  {object}  AdminQuotaDefaultsResponse
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "admin role required"
//	@Security  CookieAuth
//	@Router    /api/admin/quotas/defaults [get]
func (s *Server) handleAdminGetQuotaDefaults(w http.ResponseWriter, r *http.Request) {
	rd := s.getResourceDefaults()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AdminQuotaDefaultsResponse{
		MaxWorkspacesPerUser:     rd.MaxWorkspacesPerUser,
		MaxSandboxesPerWorkspace: rd.MaxSandboxesPerWorkspace,
		MaxWorkspaceDriveSize:    rd.MaxWorkspaceDriveSize,
		MaxSandboxCPU:            rd.MaxSandboxCPU,
		MaxSandboxMemory:         rd.MaxSandboxMemory,
		MaxIdleTimeout:           rd.MaxIdleTimeout,
		WsMaxTotalCPU:            rd.WsMaxTotalCPU,
		WsMaxTotalMemory:         rd.WsMaxTotalMemory,
		WsMaxIdleTimeout:         rd.WsMaxIdleTimeout,
	})
}

//	@Summary   Update system-wide quota defaults (admin)
//	@Tags      Admin
//	@Accept    json
//	@Produce   json
//	@Param     body  body  AdminQuotaDefaultsUpdateRequest  true  "Quota fields to update (all optional)"
//	@Success   200  {object}  AdminQuotaDefaultsResponse  "Updated defaults"
//	@Failure   400  {string}  string  "bad request"
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "admin role required"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/admin/quotas/defaults [put]
func (s *Server) handleAdminSetQuotaDefaults(w http.ResponseWriter, r *http.Request) {
	var req AdminQuotaDefaultsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if req.MaxWorkspacesPerUser != nil {
		if *req.MaxWorkspacesPerUser < 0 {
			http.Error(w, "max_workspaces_per_user must be >= 0", http.StatusBadRequest)
			return
		}
		if err := s.DB.SetSystemSetting(settingKeyMaxWorkspaces, strconv.Itoa(*req.MaxWorkspacesPerUser)); err != nil {
			log.Printf("admin: failed to set quota default: %v", err)
			http.Error(w, "failed to save setting", http.StatusInternalServerError)
			return
		}
	}
	if req.MaxSandboxesPerWorkspace != nil {
		if *req.MaxSandboxesPerWorkspace < 0 {
			http.Error(w, "max_sandboxes_per_workspace must be >= 0", http.StatusBadRequest)
			return
		}
		if err := s.DB.SetSystemSetting(settingKeyMaxSandboxes, strconv.Itoa(*req.MaxSandboxesPerWorkspace)); err != nil {
			log.Printf("admin: failed to set quota default: %v", err)
			http.Error(w, "failed to save setting", http.StatusInternalServerError)
			return
		}
	}
	if req.MaxWorkspaceDriveSize != nil {
		if err := s.DB.SetSystemSetting(settingKeyMaxWorkspaceDriveSize, strconv.FormatInt(*req.MaxWorkspaceDriveSize, 10)); err != nil {
			log.Printf("admin: failed to set quota default: %v", err)
			http.Error(w, "failed to save setting", http.StatusInternalServerError)
			return
		}
	}
	if req.MaxSandboxCPU != nil {
		if err := s.DB.SetSystemSetting(settingKeyMaxSandboxCPU, strconv.Itoa(*req.MaxSandboxCPU)); err != nil {
			log.Printf("admin: failed to set quota default: %v", err)
			http.Error(w, "failed to save setting", http.StatusInternalServerError)
			return
		}
	}
	if req.MaxSandboxMemory != nil {
		if err := s.DB.SetSystemSetting(settingKeyMaxSandboxMemory, strconv.FormatInt(*req.MaxSandboxMemory, 10)); err != nil {
			log.Printf("admin: failed to set quota default: %v", err)
			http.Error(w, "failed to save setting", http.StatusInternalServerError)
			return
		}
	}
	if req.MaxIdleTimeout != nil {
		if err := s.DB.SetSystemSetting(settingKeyMaxIdleTimeout, strconv.Itoa(*req.MaxIdleTimeout)); err != nil {
			log.Printf("admin: failed to set quota default: %v", err)
			http.Error(w, "failed to save setting", http.StatusInternalServerError)
			return
		}
	}
	if req.WsMaxTotalCPU != nil {
		if err := s.DB.SetSystemSetting(settingKeyWsMaxTotalCPU, strconv.Itoa(*req.WsMaxTotalCPU)); err != nil {
			log.Printf("admin: failed to set quota default: %v", err)
			http.Error(w, "failed to save setting", http.StatusInternalServerError)
			return
		}
	}
	if req.WsMaxTotalMemory != nil {
		if err := s.DB.SetSystemSetting(settingKeyWsMaxTotalMemory, strconv.FormatInt(*req.WsMaxTotalMemory, 10)); err != nil {
			log.Printf("admin: failed to set quota default: %v", err)
			http.Error(w, "failed to save setting", http.StatusInternalServerError)
			return
		}
	}
	if req.WsMaxIdleTimeout != nil {
		if err := s.DB.SetSystemSetting(settingKeyWsMaxIdleTimeout, strconv.Itoa(*req.WsMaxIdleTimeout)); err != nil {
			log.Printf("admin: failed to set quota default: %v", err)
			http.Error(w, "failed to save setting", http.StatusInternalServerError)
			return
		}
	}

	rd := s.getResourceDefaults()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AdminQuotaDefaultsResponse{
		MaxWorkspacesPerUser:     rd.MaxWorkspacesPerUser,
		MaxSandboxesPerWorkspace: rd.MaxSandboxesPerWorkspace,
		MaxWorkspaceDriveSize:    rd.MaxWorkspaceDriveSize,
		MaxSandboxCPU:            rd.MaxSandboxCPU,
		MaxSandboxMemory:         rd.MaxSandboxMemory,
		MaxIdleTimeout:           rd.MaxIdleTimeout,
		WsMaxTotalCPU:            rd.WsMaxTotalCPU,
		WsMaxTotalMemory:         rd.WsMaxTotalMemory,
		WsMaxIdleTimeout:         rd.WsMaxIdleTimeout,
	})
}

//	@Summary   Get per-user quota override (admin)
//	@Tags      Admin
//	@Produce   json
//	@Param     id  path  string  true  "User ID"
//	@Success   200  {object}  AdminUserQuotaResponse
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "admin role required"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/admin/users/{id}/quota [get]
func (s *Server) handleAdminGetUserQuota(w http.ResponseWriter, r *http.Request) {
	targetID := chi.URLParam(r, "id")

	rd := s.getResourceDefaults()

	uq, err := s.DB.GetUserQuota(targetID)
	if err != nil {
		log.Printf("admin: failed to get user quota: %v", err)
		http.Error(w, "failed to get user quota", http.StatusInternalServerError)
		return
	}

	resp := AdminUserQuotaResponse{
		Defaults: AdminUserQuotaDefaults{
			MaxWorkspacesPerUser: rd.MaxWorkspacesPerUser,
		},
	}
	if uq != nil {
		resp.Overrides = &AdminUserQuotaOverrides{
			MaxWorkspaces: uq.MaxWorkspaces,
			UpdatedAt:     uq.UpdatedAt.Format(time.RFC3339),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

//	@Summary   Set per-user quota override (admin)
//	@Tags      Admin
//	@Accept    json
//	@Param     id    path  string                    true  "User ID"
//	@Param     body  body  AdminSetUserQuotaRequest  true  "Quota overrides"
//	@Success   204  "saved"
//	@Failure   400  {string}  string  "bad request"
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "admin role required"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/admin/users/{id}/quota [put]
func (s *Server) handleAdminSetUserQuota(w http.ResponseWriter, r *http.Request) {
	targetID := chi.URLParam(r, "id")

	var req AdminSetUserQuotaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if req.MaxWorkspaces != nil && *req.MaxWorkspaces < 0 {
		http.Error(w, "max_workspaces must be >= 0", http.StatusBadRequest)
		return
	}

	if err := s.DB.SetUserQuota(targetID, req.MaxWorkspaces); err != nil {
		log.Printf("admin: failed to set user quota: %v", err)
		http.Error(w, fmt.Sprintf("failed to set user quota: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

//	@Summary   Delete per-user quota override (admin)
//	@Tags      Admin
//	@Param     id  path  string  true  "User ID"
//	@Success   204  "deleted"
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "admin role required"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/admin/users/{id}/quota [delete]
func (s *Server) handleAdminDeleteUserQuota(w http.ResponseWriter, r *http.Request) {
	targetID := chi.URLParam(r, "id")

	if err := s.DB.DeleteUserQuota(targetID); err != nil {
		log.Printf("admin: failed to delete user quota: %v", err)
		http.Error(w, "failed to delete user quota", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

//	@Summary   Get workspace quota override (admin)
//	@Tags      Admin
//	@Produce   json
//	@Param     id  path  string  true  "Workspace ID"
//	@Success   200  {object}  AdminWorkspaceQuotaResponse
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "admin role required"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/admin/workspaces/{id}/quota [get]
func (s *Server) handleAdminGetWorkspaceQuota(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")

	rd := s.getResourceDefaults()

	wq, err := s.DB.GetWorkspaceQuota(workspaceID)
	if err != nil {
		log.Printf("admin: failed to get workspace quota: %v", err)
		http.Error(w, "failed to get workspace quota", http.StatusInternalServerError)
		return
	}

	resp := AdminWorkspaceQuotaResponse{
		Defaults: AdminWorkspaceQuotaDefaults{
			MaxSandboxes:     rd.MaxSandboxesPerWorkspace,
			MaxSandboxCPU:    rd.MaxSandboxCPU,
			MaxSandboxMemory: rd.MaxSandboxMemory,
			MaxIdleTimeout:   rd.MaxIdleTimeout,
			MaxTotalCPU:      rd.WsMaxTotalCPU,
			MaxTotalMemory:   rd.WsMaxTotalMemory,
			MaxDriveSize:     rd.MaxWorkspaceDriveSize,
		},
	}
	if wq != nil {
		resp.Overrides = &AdminWorkspaceQuotaOverrides{
			MaxSandboxes:     wq.MaxSandboxes,
			MaxSandboxCPU:    wq.MaxSandboxCPU,
			MaxSandboxMemory: wq.MaxSandboxMemory,
			MaxIdleTimeout:   wq.MaxIdleTimeout,
			MaxTotalCPU:      wq.MaxTotalCPU,
			MaxTotalMemory:   wq.MaxTotalMemory,
			MaxDriveSize:     wq.MaxDriveSize,
			UpdatedAt:        wq.UpdatedAt.Format(time.RFC3339),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

//	@Summary   Set workspace quota override (admin)
//	@Tags      Admin
//	@Accept    json
//	@Param     id    path  string                         true  "Workspace ID"
//	@Param     body  body  AdminSetWorkspaceQuotaRequest  true  "Quota overrides (all optional, merged with existing)"
//	@Success   204  "saved"
//	@Failure   400  {string}  string  "bad request"
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "admin role required"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/admin/workspaces/{id}/quota [put]
func (s *Server) handleAdminSetWorkspaceQuota(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")

	var req AdminSetWorkspaceQuotaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if req.MaxSandboxes != nil && *req.MaxSandboxes < 0 {
		http.Error(w, "max_sandboxes must be >= 0", http.StatusBadRequest)
		return
	}

	// Fetch existing to merge partial updates.
	existing, err := s.DB.GetWorkspaceQuota(workspaceID)
	if err != nil {
		log.Printf("admin: failed to get workspace quota: %v", err)
		http.Error(w, "failed to get workspace quota", http.StatusInternalServerError)
		return
	}

	mergedSbx := req.MaxSandboxes
	mergedCPU := req.MaxSandboxCPU
	mergedMemory := req.MaxSandboxMemory
	mergedIdle := req.MaxIdleTimeout
	mergedMaxCPU := req.MaxTotalCPU
	mergedMaxMemory := req.MaxTotalMemory
	mergedDrive := req.MaxDriveSize

	if existing != nil {
		if mergedSbx == nil {
			mergedSbx = existing.MaxSandboxes
		}
		if mergedCPU == nil {
			mergedCPU = existing.MaxSandboxCPU
		}
		if mergedMemory == nil {
			mergedMemory = existing.MaxSandboxMemory
		}
		if mergedIdle == nil {
			mergedIdle = existing.MaxIdleTimeout
		}
		if mergedMaxCPU == nil {
			mergedMaxCPU = existing.MaxTotalCPU
		}
		if mergedMaxMemory == nil {
			mergedMaxMemory = existing.MaxTotalMemory
		}
		if mergedDrive == nil {
			mergedDrive = existing.MaxDriveSize
		}
	}

	if err := s.DB.SetWorkspaceQuota(workspaceID, mergedSbx,
		mergedCPU, mergedMemory, mergedIdle,
		mergedMaxCPU, mergedMaxMemory, mergedDrive); err != nil {
		log.Printf("admin: failed to set workspace quota: %v", err)
		http.Error(w, fmt.Sprintf("failed to set workspace quota: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

//	@Summary   Delete workspace quota override (admin)
//	@Tags      Admin
//	@Param     id  path  string  true  "Workspace ID"
//	@Success   204  "deleted"
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "admin role required"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/admin/workspaces/{id}/quota [delete]
func (s *Server) handleAdminDeleteWorkspaceQuota(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")

	if err := s.DB.DeleteWorkspaceQuota(workspaceID); err != nil {
		log.Printf("admin: failed to delete workspace quota: %v", err)
		http.Error(w, "failed to delete workspace quota", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// proxyLLMProxyRequest forwards an HTTP request to the llmproxy internal API.
func (s *Server) proxyLLMProxyRequest(w http.ResponseWriter, method, path string, body []byte) {
	if s.LLMProxyURL == "" {
		http.Error(w, "llmproxy not configured", http.StatusServiceUnavailable)
		return
	}
	url := s.LLMProxyURL + path
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if body != nil {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("llmproxy proxy error: %v", err)
		http.Error(w, "llmproxy unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

//	@Summary   Get workspace LLM quota (admin, proxied to llmproxy)
//	@Tags      Admin
//	@Produce   json
//	@Param     id  path  string  true  "Workspace ID"
//	@Success   200  {object}  LLMQuotaResponse
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "admin role required"
//	@Failure   502  {string}  string  "llmproxy unavailable"
//	@Failure   503  {string}  string  "llmproxy not configured"
//	@Security  CookieAuth
//	@Router    /api/admin/workspaces/{id}/llm-quota [get]
func (s *Server) handleAdminGetWorkspaceLLMQuota(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")
	s.proxyLLMProxyRequest(w, http.MethodGet, "/internal/quotas/"+workspaceID, nil)
}

//	@Summary   Set workspace LLM quota override (admin, proxied to llmproxy)
//	@Tags      Admin
//	@Accept    json
//	@Param     id  path  string  true  "Workspace ID"
//	@Success   200  "saved"
//	@Failure   400  {string}  string  "bad request"
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "admin role required"
//	@Failure   502  {string}  string  "llmproxy unavailable"
//	@Failure   503  {string}  string  "llmproxy not configured"
//	@Security  CookieAuth
//	@Router    /api/admin/workspaces/{id}/llm-quota [put]
func (s *Server) handleAdminSetWorkspaceLLMQuota(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.proxyLLMProxyRequest(w, http.MethodPut, "/internal/quotas/"+workspaceID, body)
}

//	@Summary   Delete workspace LLM quota override (admin, proxied to llmproxy)
//	@Tags      Admin
//	@Param     id  path  string  true  "Workspace ID"
//	@Success   204  "deleted"
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "admin role required"
//	@Failure   502  {string}  string  "llmproxy unavailable"
//	@Failure   503  {string}  string  "llmproxy not configured"
//	@Security  CookieAuth
//	@Router    /api/admin/workspaces/{id}/llm-quota [delete]
func (s *Server) handleAdminDeleteWorkspaceLLMQuota(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")
	s.proxyLLMProxyRequest(w, http.MethodDelete, "/internal/quotas/"+workspaceID, nil)
}
