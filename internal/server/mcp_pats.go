// MCP Personal Access Token CRUD. Spec'd in
// docs/superpowers/specs/2026-06-09-envmcp-public-gateway-design.md § 4.3
// (post the 2026-06-15 amendment that hard-binds each PAT to one
// workspace). All routes are workspace-scoped:
//
//   GET    /api/workspaces/{wid}/mcp/pats/scopes  — static catalog
//   GET    /api/workspaces/{wid}/mcp/pats         — list THIS workspace's PATs
//   POST   /api/workspaces/{wid}/mcp/pats         — mint a PAT bound to {wid}
//   DELETE /api/workspaces/{wid}/mcp/pats/{id}    — revoke a PAT bound to {wid}
//
// Every handler gates on the caller being a member of {wid} — same
// pattern as the workspace API keys above. The workspace_id never
// appears in any request/response body — it's intrinsic to the URL.
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/secrets"
	"github.com/go-chi/chi/v5"
)

// Role gates on the four handlers match workspace_api_keys.go (the
// sibling for the workspace REST API keys), per the security review
// of the first revision of this file:
//
//   list      — any member (developer + maintainer + owner)
//   scopes    — any member (developer + maintainer + owner)
//   mint      — owner or maintainer only
//   revoke    — owner or maintainer only
//
// The minted PAT carries `mcp:exec` (full shell on any executor in
// the workspace), so mint is a workspace-level privilege escalation
// surface — gating it to owner/maintainer matches how we gate the
// equivalent action on workspace API keys.

// handleListMCPPATs returns the PATs bound to this workspace, newest
// first. Includes both active and revoked rows. Secret hashes are
// never included.
//
//	@Summary    List MCP Personal Access Tokens for a workspace
//	@Tags       MCP PATs
//	@Param      wid  path  string  true  "Workspace ID"
//	@Produce    json
//	@Success    200  {array}   MCPPAT
//	@Failure    401  {string}  string  "not authenticated"
//	@Failure    404  {string}  string  "not found (or not a member)"
//	@Failure    500  {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces/{wid}/mcp/pats [get]
func (s *Server) handleListMCPPATs(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "wid")
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}
	rows, err := s.DB.ListMCPPATsByWorkspace(r.Context(), wsID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]MCPPAT, 0, len(rows))
	for _, p := range rows {
		scopes := p.Scopes
		if scopes == nil {
			scopes = []string{}
		}
		out = append(out, MCPPAT{
			ID:          p.ID,
			Name:        p.Name,
			Prefix:      p.Prefix,
			WorkspaceID: p.WorkspaceID,
			Scopes:      scopes,
			CreatedAt:   p.CreatedAt.UTC().Format(time.RFC3339),
			ExpiresAt:   p.ExpiresAt.UTC().Format(time.RFC3339),
			LastUsedAt:  rfc3339Ptr(p.LastUsedAt),
			RevokedAt:   rfc3339Ptr(p.RevokedAt),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleMintMCPPAT creates a new PAT bound to the URL's {wid}. The
// secret is returned ONCE — the caller must persist it on their side
// because it never appears again. Scopes are validated against the
// catalog (no workspace:* — that's intrinsic to the URL now).
//
//	@Summary     Mint an MCP Personal Access Token for a workspace
//	@Description Returns the secret ONCE in the response body. At least one scope must be provided. The PAT is bound to the URL's workspace; the body has no workspace field.
//	@Tags        MCP PATs
//	@Param       wid   path  string  true  "Workspace ID"
//	@Accept      json
//	@Produce     json
//	@Param       body  body  MCPPATMintRequest  true  "PAT metadata"
//	@Success     201   {object}  MCPPATMintResponse
//	@Failure     400   {string}  string  "name required / scope not available"
//	@Failure     401   {string}  string  "not authenticated"
//	@Failure     404   {string}  string  "not found (or not a member of the workspace)"
//	@Failure     422   {string}  string  "expires_at invalid"
//	@Failure     500   {string}  string  "internal error"
//	@Security    CookieAuth
//	@Router      /api/workspaces/{wid}/mcp/pats [post]
func (s *Server) handleMintMCPPAT(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "wid")
	if !s.requireWorkspaceRole(w, r, wsID, "owner", "maintainer") {
		return
	}
	// requireWorkspaceRole already validated authentication + role;
	// pull the userID out separately so we can persist it on the PAT
	// row for audit. (The sibling workspace_api_keys.go does the same.)
	userID := auth.UserIDFromContext(r.Context())
	var req MCPPATMintRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	if err := s.validateMCPPATScopes(req.Scopes); err != nil {
		var bad errInvalidScope
		if errors.As(err, &bad) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	exp, err := resolveExpiresAt(req.ExpiresAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	tok, err := secrets.Mint(secrets.MCPPATSpec)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	row := db.MCPPAT{
		ID:          tok.ID,
		UserID:      userID,
		WorkspaceID: wsID,
		Name:        req.Name,
		Prefix:      tok.ID, // prefix == ID for agpat_ tokens (both = "agpat_<16chars>")
		SecretHash:  tok.Hash,
		Scopes:      req.Scopes,
		ExpiresAt:   exp,
	}
	if err := s.DB.CreateMCPPAT(r.Context(), row); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(MCPPATMintResponse{
		ID:          tok.ID,
		Name:        req.Name,
		Prefix:      tok.ID,
		WorkspaceID: wsID,
		Secret:      tok.Full, // full wire-format token returned once to the user
		Scopes:      req.Scopes,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   exp.Format(time.RFC3339),
	})
}

// handleRevokeMCPPAT soft-deletes a PAT bound to this workspace.
// Scoped to ({wid}, {id}) at the DB layer — a member of one workspace
// can't revoke another workspace's PAT even by guessing the id.
// Returns 204 either way (idempotent — distinguishing "didn't exist"
// from "already revoked" gives no useful signal to a caller).
//
//	@Summary    Revoke an MCP Personal Access Token
//	@Tags       MCP PATs
//	@Param      wid  path  string  true  "Workspace ID"
//	@Param      id   path  string  true  "PAT id (= prefix, e.g. agpat_a1b2...)"
//	@Success    204
//	@Failure    401  {string}  string  "not authenticated"
//	@Failure    404  {string}  string  "not found (or not a member of the workspace)"
//	@Failure    500  {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces/{wid}/mcp/pats/{id} [delete]
func (s *Server) handleRevokeMCPPAT(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "wid")
	if !s.requireWorkspaceRole(w, r, wsID, "owner", "maintainer") {
		return
	}
	patID := chi.URLParam(r, "id")
	if err := s.DB.RevokeMCPPAT(r.Context(), wsID, patID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListMCPPATScopes returns the static scope catalog. The
// pre-amendment version of this endpoint also returned the caller's
// workspaces for the multi-select picker; that's gone — workspace is
// now implicit in the URL, so the mint modal just needs the scope
// checkboxes.
//
//	@Summary    List MCP PAT scope catalog for a workspace
//	@Description Static capability scopes (mcp:read, mcp:exec). No dynamic workspace scopes — workspace binding is implicit in the route's {wid}.
//	@Tags       MCP PATs
//	@Param      wid  path  string  true  "Workspace ID"
//	@Produce    json
//	@Success    200  {object}  MCPPATScopesResponse
//	@Failure    401  {string}  string  "not authenticated"
//	@Failure    404  {string}  string  "not found (or not a member of the workspace)"
//	@Failure    500  {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/workspaces/{wid}/mcp/pats/scopes [get]
func (s *Server) handleListMCPPATScopes(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "wid")
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}
	scopes := make([]MCPPATScopeDescriptor, 0, len(mcpPATScopeCatalog))
	for _, sc := range mcpPATScopeCatalog {
		scopes = append(scopes, MCPPATScopeDescriptor{
			Name:        sc.Name,
			Description: sc.Description,
			Available:   sc.Available,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(MCPPATScopesResponse{
		Scopes: scopes,
	})
}
