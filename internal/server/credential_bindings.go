package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"
	"github.com/agentserver/agentserver/internal/credentialproxy/k8s"
	"github.com/agentserver/agentserver/internal/credentialproxy/provider"
	"github.com/agentserver/agentserver/internal/crypto"
	"github.com/agentserver/agentserver/internal/db"
	"golang.org/x/oauth2"
)

// sweepExpiredDeviceFlows periodically removes expired entries from the
// deviceFlows map to prevent unbounded memory growth from abandoned flows.
func (s *Server) sweepExpiredDeviceFlows() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		s.deviceFlowsMu.Lock()
		for id, flow := range s.deviceFlows {
			if now.After(flow.expiresAt) {
				delete(s.deviceFlows, id)
			}
		}
		s.deviceFlowsMu.Unlock()
	}
}

// pendingDeviceFlow holds state for an in-progress OIDC device code flow.
type pendingDeviceFlow struct {
	oauth2Cfg      oauth2.Config
	deviceAuth     *oauth2.DeviceAuthResponse
	oidcConfig     k8s.OIDCAuthConfig
	bindingID      string
	wsID           string
	kind           string
	displayName    string
	serverURL      string
	publicMetaJSON json.RawMessage
	isDefault      bool
	expiresAt      time.Time
	completing     sync.Once
}

// handleListCredentialBindings lists credential bindings for a workspace+kind.
// GET /api/workspaces/{id}/credentials/{kind}
//
//	@Summary   List credential bindings for a workspace
//	@Tags      Misc
//	@Produce   json
//	@Param     id    path  string  true  "Workspace ID"
//	@Param     kind  path  string  true  "Credential kind (e.g. kubernetes)"
//	@Success   200  {array}   CredentialBindingItem
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{id}/credentials/{kind} [get]
func (s *Server) handleListCredentialBindings(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	kind := chi.URLParam(r, "kind")

	bindings, err := s.DB.ListCredentialBindingsMeta(wsID, kind)
	if err != nil {
		log.Printf("list credential bindings: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	result := make([]CredentialBindingItem, 0, len(bindings))
	for _, b := range bindings {
		result = append(result, CredentialBindingItem{
			ID:          b.ID,
			DisplayName: b.DisplayName,
			ServerURL:   b.ServerURL,
			AuthType:    b.AuthType,
			PublicMeta:  b.PublicMeta,
			IsDefault:   b.IsDefault,
			CreatedAt:   b.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleCreateCredentialBinding creates a new credential binding.
// POST /api/workspaces/{id}/credentials/{kind}
//
//	@Summary   Create a credential binding for a workspace
//	@Description On success returns 201 with the new binding. When the provider requires OIDC device code authentication returns 202 with verification_uri and user_code; poll device-complete to finalize.
//	@Tags      Misc
//	@Accept    json
//	@Produce   json
//	@Param     id    path  string                          true  "Workspace ID"
//	@Param     kind  path  string                          true  "Credential kind (e.g. kubernetes)"
//	@Param     body  body  CredentialBindingCreateRequest  true  "Credential config"
//	@Success   201  {object}  CredentialBindingCreateResponse  "Binding created"
//	@Success   202  {object}  CredentialBindingCreateResponse  "OIDC device code flow initiated"
//	@Failure   400  {string}  string  "bad request"
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   409  {string}  string  "duplicate display_name"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{id}/credentials/{kind} [post]
func (s *Server) handleCreateCredentialBinding(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	kind := chi.URLParam(r, "kind")

	if len(s.EncryptionKey) == 0 {
		http.Error(w, "credential proxy not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		DisplayName string `json:"display_name"`
		Config      string `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.DisplayName == "" || req.Config == "" {
		http.Error(w, "display_name and config are required", http.StatusBadRequest)
		return
	}

	// Look up the provider and validate the upload.
	prov, err := provider.Lookup(kind)
	if err != nil {
		http.Error(w, fmt.Sprintf("unknown credential kind %q", kind), http.StatusBadRequest)
		return
	}

	result, err := prov.ParseUpload(r.Header.Get("Content-Type"), []byte(req.Config))
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid config: %v", err), http.StatusBadRequest)
		return
	}

	// Use the user-provided display name if given, otherwise fall back to what the parser returned.
	displayName := req.DisplayName
	if displayName == "" {
		displayName = result.DisplayName
	}

	// Generate binding ID.
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		log.Printf("generate binding id: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	bindingID := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(idBytes)

	// Check if this is the first binding for the (workspace, kind) pair.
	count, err := s.DB.CountCredentialBindings(wsID, kind)
	if err != nil {
		log.Printf("count credential bindings: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	isDefault := count == 0

	publicMetaJSON, _ := json.Marshal(result.PublicMeta)

	// OIDC device code flow: initiate and return 202 instead of creating the binding immediately.
	if result.PendingDeviceAuth {
		s.handleCreateOIDCBinding(w, r, wsID, kind, bindingID, displayName, result, publicMetaJSON, isDefault)
		return
	}

	// Encrypt auth secret.
	authBlob, err := crypto.Encrypt(s.EncryptionKey, result.AuthSecret)
	if err != nil {
		log.Printf("encrypt credential: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	binding := &db.CredentialBinding{
		ID:          bindingID,
		WorkspaceID: wsID,
		Kind:        kind,
		DisplayName: displayName,
		ServerURL:   result.ServerURL,
		PublicMeta:  publicMetaJSON,
		AuthType:    result.AuthType,
		AuthBlob:    authBlob,
		IsDefault:   isDefault,
	}

	if err := s.DB.CreateCredentialBinding(binding); err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			http.Error(w, "a binding with this display name already exists", http.StatusConflict)
			return
		}
		log.Printf("create credential binding: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":           bindingID,
		"display_name": displayName,
		"server_url":   result.ServerURL,
		"auth_type":    result.AuthType,
		"public_meta":  result.PublicMeta,
		"is_default":   isDefault,
	})
}

// handleDeleteCredentialBinding deletes a credential binding.
// DELETE /api/workspaces/{id}/credentials/{kind}/{bindingId}
//
//	@Summary   Delete a credential binding
//	@Tags      Misc
//	@Param     id         path  string  true  "Workspace ID"
//	@Param     kind       path  string  true  "Credential kind"
//	@Param     bindingId  path  string  true  "Binding ID"
//	@Success   204  "deleted"
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   404  {string}  string  "not found"
//	@Failure   409  {string}  string  "cannot delete default binding while others exist"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{id}/credentials/{kind}/{bindingId} [delete]
func (s *Server) handleDeleteCredentialBinding(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	kind := chi.URLParam(r, "kind")
	bindingID := chi.URLParam(r, "bindingId")

	binding, err := s.DB.GetCredentialBinding(bindingID)
	if err != nil {
		log.Printf("get credential binding: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if binding == nil || binding.WorkspaceID != wsID || binding.Kind != kind {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// If this is the default and not the last one, reject.
	if binding.IsDefault {
		count, err := s.DB.CountCredentialBindings(wsID, kind)
		if err != nil {
			log.Printf("count credential bindings: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if count > 1 {
			http.Error(w, "cannot delete the default binding while others exist; use set-default first", http.StatusConflict)
			return
		}
	}

	if err := s.DB.DeleteCredentialBinding(bindingID); err != nil {
		log.Printf("delete credential binding: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleSetDefaultCredentialBinding sets a binding as the default for its kind.
// POST /api/workspaces/{id}/credentials/{kind}/{bindingId}/set-default
//
//	@Summary   Set a credential binding as the default
//	@Tags      Misc
//	@Param     id         path  string  true  "Workspace ID"
//	@Param     kind       path  string  true  "Credential kind"
//	@Param     bindingId  path  string  true  "Binding ID"
//	@Success   204  "updated"
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{id}/credentials/{kind}/{bindingId}/set-default [post]
func (s *Server) handleSetDefaultCredentialBinding(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	kind := chi.URLParam(r, "kind")
	bindingID := chi.URLParam(r, "bindingId")

	if err := s.DB.SetCredentialBindingDefault(wsID, kind, bindingID); err != nil {
		log.Printf("set default credential binding: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handlePatchCredentialBinding updates mutable fields of a credential binding.
// PATCH /api/workspaces/{id}/credentials/{kind}/{bindingId}
//
//	@Summary   Patch a credential binding (rename)
//	@Tags      Misc
//	@Accept    json
//	@Param     id         path  string                          true  "Workspace ID"
//	@Param     kind       path  string                          true  "Credential kind"
//	@Param     bindingId  path  string                          true  "Binding ID"
//	@Param     body       body  CredentialBindingPatchRequest   true  "Fields to update"
//	@Success   204  "updated"
//	@Failure   400  {string}  string  "bad request"
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   404  {string}  string  "not found"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{id}/credentials/{kind}/{bindingId} [patch]
func (s *Server) handlePatchCredentialBinding(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	kind := chi.URLParam(r, "kind")
	bindingID := chi.URLParam(r, "bindingId")

	binding, err := s.DB.GetCredentialBinding(bindingID)
	if err != nil {
		log.Printf("get credential binding: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if binding == nil || binding.WorkspaceID != wsID || binding.Kind != kind {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	var req struct {
		DisplayName *string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.DisplayName != nil && *req.DisplayName != "" {
		_, err := s.DB.Exec(
			`UPDATE credential_bindings SET display_name = $1, updated_at = NOW() WHERE id = $2`,
			*req.DisplayName, bindingID,
		)
		if err != nil {
			log.Printf("update credential binding: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleCreateOIDCBinding initiates the OIDC device code flow for an OIDC credential binding.
func (s *Server) handleCreateOIDCBinding(
	w http.ResponseWriter, r *http.Request,
	wsID, kind, bindingID, displayName string,
	result *provider.UploadResult,
	publicMetaJSON json.RawMessage,
	isDefault bool,
) {
	var oidcCfg k8s.OIDCAuthConfig
	if err := json.Unmarshal(result.AuthSecret, &oidcCfg); err != nil {
		log.Printf("unmarshal oidc config: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Validate issuer URL (SSRF guard — must be https, not a private IP).
	if err := k8s.ValidateIssuerURL(oidcCfg.IssuerURL); err != nil {
		http.Error(w, fmt.Sprintf("invalid OIDC issuer: %v", err), http.StatusBadRequest)
		return
	}

	// OIDC discovery.
	oidcProvider, err := gooidc.NewProvider(r.Context(), oidcCfg.IssuerURL)
	if err != nil {
		log.Printf("oidc discovery for %s: %v", oidcCfg.IssuerURL, err)
		http.Error(w, "OIDC discovery failed", http.StatusBadGateway)
		return
	}

	oauth2Cfg := oauth2.Config{
		ClientID: oidcCfg.ClientID,
		Endpoint: oidcProvider.Endpoint(),
		Scopes:   oidcCfg.Scopes,
	}

	// Initiate device code flow.
	deviceAuth, err := oauth2Cfg.DeviceAuth(r.Context())
	if err != nil {
		log.Printf("device auth request: %v", err)
		http.Error(w, "device code flow initiation failed", http.StatusBadGateway)
		return
	}

	// Store pending flow state.
	flow := &pendingDeviceFlow{
		oauth2Cfg:      oauth2Cfg,
		deviceAuth:     deviceAuth,
		oidcConfig:     oidcCfg,
		bindingID:      bindingID,
		wsID:           wsID,
		kind:           kind,
		displayName:    displayName,
		serverURL:      result.ServerURL,
		publicMetaJSON: publicMetaJSON,
		isDefault:      isDefault,
		expiresAt:      deviceAuth.Expiry,
	}

	s.deviceFlowsMu.Lock()
	s.deviceFlows[bindingID] = flow
	s.deviceFlowsMu.Unlock()

	expiresIn := int(time.Until(deviceAuth.Expiry).Seconds())
	if expiresIn <= 0 {
		expiresIn = 900
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":                bindingID,
		"status":            "pending_device_code",
		"verification_uri":  deviceAuth.VerificationURI,
		"user_code":         deviceAuth.UserCode,
		"expires_in":        expiresIn,
	})
}

// handleDeviceCodeComplete long-polls until the OIDC device code flow completes.
// POST /api/workspaces/{id}/credentials/{kind}/{bindingId}/device-complete
//
//	@Summary   Complete an OIDC device code flow for a pending credential binding
//	@Description Long-polls until the user authorizes the device code. Returns 201 with the created binding on success.
//	@Tags      Misc
//	@Produce   json
//	@Param     id         path  string  true  "Workspace ID"
//	@Param     kind       path  string  true  "Credential kind"
//	@Param     bindingId  path  string  true  "Pending binding ID"
//	@Success   201  {object}  CredentialBindingCreateResponse  "Binding created"
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "device code authorization failed"
//	@Failure   404  {string}  string  "no pending device code flow found"
//	@Failure   409  {string}  string  "duplicate display_name or flow already being completed"
//	@Failure   410  {string}  string  "device code flow expired"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{id}/credentials/{kind}/{bindingId}/device-complete [post]
func (s *Server) handleDeviceCodeComplete(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	kind := chi.URLParam(r, "kind")
	bindingID := chi.URLParam(r, "bindingId")

	s.deviceFlowsMu.Lock()
	flow, ok := s.deviceFlows[bindingID]
	s.deviceFlowsMu.Unlock()

	if !ok || flow.wsID != wsID {
		http.Error(w, "no pending device code flow found", http.StatusNotFound)
		return
	}

	// Validate kind matches the flow's original kind.
	if flow.kind != kind {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if time.Now().After(flow.expiresAt) {
		s.deviceFlowsMu.Lock()
		delete(s.deviceFlows, bindingID)
		s.deviceFlowsMu.Unlock()
		http.Error(w, "device code flow expired", http.StatusGone)
		return
	}

	// Use sync.Once to prevent concurrent callers from both completing the flow.
	var (
		token    *oauth2.Token
		tokenErr error
	)
	flow.completing.Do(func() {
		token, tokenErr = flow.oauth2Cfg.DeviceAccessToken(r.Context(), flow.deviceAuth)
	})
	if token == nil && tokenErr == nil {
		// Another goroutine is already completing this flow.
		http.Error(w, "device code flow already being completed", http.StatusConflict)
		return
	}
	if tokenErr != nil {
		s.deviceFlowsMu.Lock()
		delete(s.deviceFlows, bindingID)
		s.deviceFlowsMu.Unlock()
		log.Printf("device code token exchange: %v", tokenErr)
		http.Error(w, "device code authorization failed", http.StatusForbidden)
		return
	}

	// Build the full OIDCAuthConfig with tokens.
	oidcCfg := flow.oidcConfig
	oidcCfg.RefreshToken = token.RefreshToken
	oidcCfg.AccessToken = token.AccessToken
	if !token.Expiry.IsZero() {
		oidcCfg.TokenExpiry = token.Expiry.Format(time.RFC3339)
	}

	authSecretJSON, err := json.Marshal(oidcCfg)
	if err != nil {
		log.Printf("marshal oidc auth secret: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	authBlob, err := crypto.Encrypt(s.EncryptionKey, authSecretJSON)
	if err != nil {
		log.Printf("encrypt oidc credential: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	binding := &db.CredentialBinding{
		ID:          flow.bindingID,
		WorkspaceID: flow.wsID,
		Kind:        flow.kind,
		DisplayName: flow.displayName,
		ServerURL:   flow.serverURL,
		PublicMeta:  flow.publicMetaJSON,
		AuthType:    "oidc",
		AuthBlob:    authBlob,
		IsDefault:   flow.isDefault,
	}

	if err := s.DB.CreateCredentialBinding(binding); err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			http.Error(w, "a binding with this display name already exists", http.StatusConflict)
			return
		}
		log.Printf("create credential binding: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Clean up flow state.
	s.deviceFlowsMu.Lock()
	delete(s.deviceFlows, bindingID)
	s.deviceFlowsMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":           flow.bindingID,
		"display_name": flow.displayName,
		"server_url":   flow.serverURL,
		"auth_type":    "oidc",
		"public_meta":  json.RawMessage(flow.publicMetaJSON),
		"is_default":   flow.isDefault,
	})
}
