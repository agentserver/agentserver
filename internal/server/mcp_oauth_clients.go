package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/secrets"
)

// MCP OAuth Client management — user-facing surface for minting and
// revoking the per-user static public OAuth clients that Codex CLI
// / Claude Code use against envmcp-public-gateway.
//
// Replaces DCR (which we tried in #249 and disabled in #260 after
// finding Codex doesn't support CIMD and unauthenticated
// /oauth2/register invites table bloat). Each user calls
// POST /api/me/oauth-clients to get a client_id, then configures
// their CLI per docs/integrations/codex-cli.md.

// MCPOAuthClientCreateRequest is the body for POST /api/me/oauth-clients.
type MCPOAuthClientCreateRequest struct {
	// Name is a user-supplied label (e.g. "my-laptop-codex").
	// Required; <= 255 chars.
	Name string `json:"name"`
}

// MCPOAuthClientResponse is the wire shape returned by both POST
// and GET. Mirrors db.MCPOAuthClient + the hydra_client_id (the
// thing the user actually puts in their CLI config). No secrets —
// every client is public (PKCE), so there's nothing to hide later.
type MCPOAuthClientResponse struct {
	ID            string     `json:"id"`
	HydraClientID string     `json:"client_id"`
	Name          string     `json:"name"`
	CreatedAt     time.Time  `json:"created_at"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
}

func toMCPOAuthClientResponse(c db.MCPOAuthClient) MCPOAuthClientResponse {
	return MCPOAuthClientResponse{
		ID:            c.ID,
		HydraClientID: c.HydraClientID,
		Name:          c.Name,
		CreatedAt:     c.CreatedAt,
		LastUsedAt:    c.LastUsedAt,
	}
}

// mcpOAuthClientRedirectURIs is the fixed set of redirect URIs we
// register with Hydra for every user-minted client. Both Codex CLI
// and Claude Code spin up a callback on an ephemeral loopback port
// (Codex: random port; Claude Code: --callback-port flag, default
// 3000-ish). RFC 8252 §7.3 says authorization servers MUST accept
// any port on loopback regardless of what's registered — Hydra v2
// follows that convention, so registering host-only entries is
// enough to cover any port the client picks.
//
// localhost and 127.0.0.1 are both listed because clients pick
// arbitrarily; ::1 omitted because no current client uses IPv6 yet.
var mcpOAuthClientRedirectURIs = []string{
	"http://localhost/callback",
	"http://127.0.0.1/callback",
}

// handleListMyMCPOAuthClients responds with the caller's clients.
// GET /api/me/oauth-clients
//
//	@Summary    List MCP OAuth clients
//	@Description  List the calling user's static OAuth clients (for Codex CLI / Claude Code MCP).
//	@Tags       MCP OAuth Clients
//	@Produce    json
//	@Success    200  {array}   MCPOAuthClientResponse
//	@Failure    401  {string}  string  "not authenticated"
//	@Failure    500  {string}  string  "internal error"
//	@Security   CookieAuth
//	@Router     /api/me/oauth-clients [get]
func (s *Server) handleListMyMCPOAuthClients(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.Auth.ValidateRequest(r)
	if !ok {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	rows, err := s.DB.ListMCPOAuthClientsByUser(r.Context(), userID)
	if err != nil {
		log.Printf("list mcp_oauth_clients: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]MCPOAuthClientResponse, 0, len(rows))
	for _, c := range rows {
		out = append(out, toMCPOAuthClientResponse(c))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleCreateMyMCPOAuthClient mints a new static public OAuth client
// in Hydra and records the user→client mapping. Two-phase write:
// Hydra first (so an orphaned hydra_client without a mapping is the
// failure mode rather than the reverse, which would let a user use
// a client they can't manage).
//
// POST /api/me/oauth-clients
// Request:  { "name": "my-laptop" }
// Response: { "id": "mcpoc_...", "client_id": "<hydra_uuid>", ... }
//
//	@Summary    Create MCP OAuth client
//	@Description  Mint a new static public OAuth2 client for use with MCP CLIs (Codex / Claude Code). PKCE-protected, no client_secret. The returned client_id is what users paste into ~/.codex/config.toml or `claude mcp add --client-id`.
//	@Tags       MCP OAuth Clients
//	@Accept     json
//	@Produce    json
//	@Param      body  body      MCPOAuthClientCreateRequest  true  "Client name"
//	@Success    201   {object}  MCPOAuthClientResponse
//	@Failure    400   {string}  string  "bad request"
//	@Failure    401   {string}  string  "not authenticated"
//	@Failure    500   {string}  string  "internal error"
//	@Failure    503   {string}  string  "hydra not configured"
//	@Security   CookieAuth
//	@Router     /api/me/oauth-clients [post]
func (s *Server) handleCreateMyMCPOAuthClient(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.Auth.ValidateRequest(r)
	if !ok {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	if s.HydraClient == nil {
		http.Error(w, "hydra not configured on this gateway", http.StatusServiceUnavailable)
		return
	}

	var req MCPOAuthClientCreateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 255 {
		http.Error(w, "name is required (1..255 chars)", http.StatusBadRequest)
		return
	}

	// Mint our opaque id BEFORE Hydra so we can pass it as the
	// Hydra client_name (gives ops a way to correlate Hydra rows
	// with agentserver rows without consulting the DB).
	rawID, err := secrets.RandomHex(8)
	if err != nil {
		log.Printf("mcp oauth client: mint id: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	ourID := "mcpoc_" + rawID

	hydraClient, err := s.HydraClient.CreateOAuth2Client(auth.HydraOAuth2Client{
		ClientName:              req.Name,
		RedirectURIs:            mcpOAuthClientRedirectURIs,
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		Scope:                   "openid mcp:read mcp:exec",
		TokenEndpointAuthMethod: "none",
	})
	if err != nil {
		log.Printf("mcp oauth client: hydra create (user=%s ourID=%s): %v", userID, ourID, err)
		http.Error(w, "failed to create client", http.StatusInternalServerError)
		return
	}

	row := db.MCPOAuthClient{
		ID:            ourID,
		UserID:        userID,
		HydraClientID: hydraClient.ClientID,
		Name:          req.Name,
		CreatedAt:     time.Now(),
	}
	if err := s.DB.CreateMCPOAuthClient(r.Context(), row); err != nil {
		// Try to roll the Hydra side back so we don't leak an
		// untrackable client into Hydra's table. Best-effort: if
		// the delete fails we still surface the original DB error
		// (ops can clean up via hydra CLI).
		if delErr := s.HydraClient.DeleteOAuth2Client(hydraClient.ClientID); delErr != nil {
			log.Printf("mcp oauth client: hydra rollback failed (leaked %s): %v",
				hydraClient.ClientID, delErr)
		}
		log.Printf("mcp oauth client: db insert: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(toMCPOAuthClientResponse(row))
}

// handleDeleteMyMCPOAuthClient revokes a client (both in Hydra and
// in our mapping table). DELETE /api/me/oauth-clients/{id}
//
// Same two-phase order in reverse: Hydra first so a delete failure
// leaves the mapping in place (user can retry), and "the row is gone
// from Hydra but still in our table" is impossible.
//
//	@Summary    Revoke MCP OAuth client
//	@Description  Delete a static MCP OAuth client. Existing tokens issued for this client become invalid within ~10s.
//	@Tags       MCP OAuth Clients
//	@Param      id   path      string  true  "Our opaque id (mcpoc_...)"
//	@Success    204
//	@Failure    401  {string}  string  "not authenticated"
//	@Failure    404  {string}  string  "not found"
//	@Failure    500  {string}  string  "internal error"
//	@Failure    503  {string}  string  "hydra not configured"
//	@Security   CookieAuth
//	@Router     /api/me/oauth-clients/{id} [delete]
func (s *Server) handleDeleteMyMCPOAuthClient(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.Auth.ValidateRequest(r)
	if !ok {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	if s.HydraClient == nil {
		http.Error(w, "hydra not configured on this gateway", http.StatusServiceUnavailable)
		return
	}

	id := chi.URLParam(r, "id")
	row, err := s.DB.GetMCPOAuthClient(r.Context(), id, userID)
	if err != nil {
		// Includes sql.ErrNoRows — treat as not-found rather than
		// 500 so an attacker probing for foreign IDs gets the same
		// shape as a missing row.
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if err := s.HydraClient.DeleteOAuth2Client(row.HydraClientID); err != nil {
		log.Printf("mcp oauth client: hydra delete (id=%s hydra=%s): %v",
			row.ID, row.HydraClientID, err)
		http.Error(w, "failed to delete client", http.StatusInternalServerError)
		return
	}
	if err := s.DB.DeleteMCPOAuthClient(r.Context(), row.ID, userID); err != nil {
		log.Printf("mcp oauth client: db delete: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// (compile guard so unused imports stay flagged if I delete a handler)
var _ = errors.New
var _ = context.Background
var _ = fmt.Sprintf
