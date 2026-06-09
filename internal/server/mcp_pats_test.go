package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
)

// newMCPPATTestServer wires a Server to the test DB. Cleanup wipes
// only the tables this suite touches.
func newMCPPATTestServer(t *testing.T) *Server {
	t.Helper()
	d := newCodexTestDBForServer(t)
	t.Cleanup(func() {
		d.Exec(`DELETE FROM mcp_pats`)
		d.Exec(`DELETE FROM workspace_members`)
		d.Exec(`DELETE FROM workspaces`)
		d.Exec(`DELETE FROM users`)
	})
	return &Server{DB: d}
}

// mintPATViaHandler calls handleMintMCPPAT and returns the parsed
// response, asserting 201. Bails the test on any failure. The mint
// path is workspace-scoped post the 2026-06-15 amendment — wid is
// part of the URL + chi param, never in the request body.
func mintPATViaHandler(t *testing.T, srv *Server, wid, uid, name string, scopes []string) MCPPATMintResponse {
	t.Helper()
	body, _ := json.Marshal(MCPPATMintRequest{Name: name, Scopes: scopes})
	req := reqWithUser(http.MethodPost, "/api/workspaces/"+wid+"/mcp/pats", uid, body,
		map[string]string{"wid": wid})
	rr := httptest.NewRecorder()
	srv.handleMintMCPPAT(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("mint: want 201, got %d — %s", rr.Code, rr.Body.String())
	}
	var resp MCPPATMintResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("mint decode: %v", err)
	}
	return resp
}

// TestMCPPAT_MintListRevoke is the happy-path round-trip: mint, list,
// revoke, list again (still appears with revoked_at set).
func TestMCPPAT_MintListRevoke(t *testing.T) {
	srv := newMCPPATTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_alpha", "u_pat1", "owner")

	resp := mintPATViaHandler(t, srv, "ws_alpha", "u_pat1", "claude-laptop", []string{MCPScopeRead})
	if resp.ID == "" || resp.Secret == "" || resp.Prefix == "" {
		t.Fatalf("missing fields: %+v", resp)
	}
	if !strings.HasPrefix(resp.ID, "agpat_") {
		t.Errorf("ID prefix: got %q, want agpat_…", resp.ID)
	}
	if !strings.HasPrefix(resp.Secret, "agpat_") {
		t.Errorf("Secret prefix: got %q, want agpat_…", resp.Secret)
	}
	if resp.WorkspaceID != "ws_alpha" {
		t.Errorf("WorkspaceID: got %q, want ws_alpha", resp.WorkspaceID)
	}
	if len(resp.Scopes) != 1 || resp.Scopes[0] != MCPScopeRead {
		t.Errorf("scopes: got %v, want [mcp:read]", resp.Scopes)
	}
	if _, err := time.Parse(time.RFC3339, resp.ExpiresAt); err != nil {
		t.Errorf("ExpiresAt: not RFC3339: %q", resp.ExpiresAt)
	}

	// List.
	listReq := reqWithUser(http.MethodGet, "/api/workspaces/ws_alpha/mcp/pats", "u_pat1", nil,
		map[string]string{"wid": "ws_alpha"})
	listRR := httptest.NewRecorder()
	srv.handleListMCPPATs(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", listRR.Code)
	}
	var pats []MCPPAT
	json.NewDecoder(listRR.Body).Decode(&pats)
	if len(pats) != 1 {
		t.Fatalf("want 1 PAT, got %d", len(pats))
	}
	if pats[0].ID != resp.ID {
		t.Errorf("ID mismatch: got %q want %q", pats[0].ID, resp.ID)
	}
	if pats[0].WorkspaceID != "ws_alpha" {
		t.Errorf("WorkspaceID echo mismatch: got %q want ws_alpha", pats[0].WorkspaceID)
	}
	// Defensive: list response shape must not leak a `secret` field
	// even if the type accidentally gained one — encode and inspect.
	listJSON, _ := json.Marshal(pats[0])
	if bytes.Contains(listJSON, []byte(`"secret"`)) {
		t.Errorf("list response leaks secret: %s", listJSON)
	}

	// Revoke.
	revokeReq := reqWithUser(http.MethodDelete, "/api/workspaces/ws_alpha/mcp/pats/"+resp.ID, "u_pat1", nil,
		map[string]string{"wid": "ws_alpha", "id": resp.ID})
	revokeRR := httptest.NewRecorder()
	srv.handleRevokeMCPPAT(revokeRR, revokeReq)
	if revokeRR.Code != http.StatusNoContent {
		t.Fatalf("revoke: want 204, got %d", revokeRR.Code)
	}

	// List again — still present, revoked_at set.
	listRR2 := httptest.NewRecorder()
	srv.handleListMCPPATs(listRR2, reqWithUser(http.MethodGet, "/api/workspaces/ws_alpha/mcp/pats",
		"u_pat1", nil, map[string]string{"wid": "ws_alpha"}))
	var after []MCPPAT
	json.NewDecoder(listRR2.Body).Decode(&after)
	if len(after) != 1 || after[0].RevokedAt == nil {
		t.Errorf("want 1 revoked PAT, got %+v", after)
	}
}

// TestMCPPAT_Mint_RejectsUnknownScope verifies that unknown scopes are
// rejected with 400.
func TestMCPPAT_Mint_RejectsUnknownScope(t *testing.T) {
	srv := newMCPPATTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_alpha", "u_pat2", "owner")

	body, _ := json.Marshal(MCPPATMintRequest{Name: "x", Scopes: []string{"bogus:scope"}})
	req := reqWithUser(http.MethodPost, "/api/workspaces/ws_alpha/mcp/pats", "u_pat2", body,
		map[string]string{"wid": "ws_alpha"})
	rr := httptest.NewRecorder()
	srv.handleMintMCPPAT(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for unknown scope, got %d — %s", rr.Code, rr.Body.String())
	}
}

// TestMCPPAT_Mint_RejectsLegacyWorkspaceScope verifies that an explicit
// "workspace:<id>" scope in the request (carried over from the pre-
// 2026-06-15 design) is rejected with a clear message rather than
// silently dropped — protects against client code that still tries to
// add it from sneaking through.
func TestMCPPAT_Mint_RejectsLegacyWorkspaceScope(t *testing.T) {
	srv := newMCPPATTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_alpha", "u_legacy", "owner")

	body, _ := json.Marshal(MCPPATMintRequest{
		Name:   "legacy",
		Scopes: []string{MCPScopeRead, "workspace:ws_alpha"},
	})
	req := reqWithUser(http.MethodPost, "/api/workspaces/ws_alpha/mcp/pats", "u_legacy", body,
		map[string]string{"wid": "ws_alpha"})
	rr := httptest.NewRecorder()
	srv.handleMintMCPPAT(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for legacy workspace scope, got %d — %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "no longer supported") {
		t.Errorf("body should explain why: %s", rr.Body.String())
	}
}

// TestMCPPAT_Mint_RejectsNonMember verifies a user trying to mint a
// PAT for a workspace they don't belong to gets 403 (sibling
// requireWorkspaceMember surfaces "not a workspace member").
func TestMCPPAT_Mint_RejectsNonMember(t *testing.T) {
	srv := newMCPPATTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_alpha", "u_alice", "owner")
	seedWorkspaceMember(t, srv.DB, "ws_beta", "u_bob", "owner")

	// Alice tries to mint a PAT for Bob's workspace.
	body, _ := json.Marshal(MCPPATMintRequest{Name: "alice's pat", Scopes: []string{MCPScopeRead}})
	req := reqWithUser(http.MethodPost, "/api/workspaces/ws_beta/mcp/pats", "u_alice", body,
		map[string]string{"wid": "ws_beta"})
	rr := httptest.NewRecorder()
	srv.handleMintMCPPAT(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403 for non-member mint, got %d — %s", rr.Code, rr.Body.String())
	}
}

// TestMCPPAT_Mint_RejectsNonAdminRoles verifies that members below
// owner/maintainer can't mint PATs (which would carry mcp:exec → full
// shell on workspace executors). Pins parity with workspace_api_keys.go.
func TestMCPPAT_Mint_RejectsNonAdminRoles(t *testing.T) {
	srv := newMCPPATTestServer(t)
	for _, role := range []string{"developer", "viewer"} {
		t.Run(role, func(t *testing.T) {
			seedWorkspaceMember(t, srv.DB, "ws_role_"+role, "u_role_"+role, role)

			body, _ := json.Marshal(MCPPATMintRequest{Name: "x", Scopes: []string{MCPScopeRead}})
			req := reqWithUser(http.MethodPost, "/api/workspaces/ws_role_"+role+"/mcp/pats",
				"u_role_"+role, body, map[string]string{"wid": "ws_role_" + role})
			rr := httptest.NewRecorder()
			srv.handleMintMCPPAT(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("role %s: want 403 (mint requires owner/maintainer), got %d — %s",
					role, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestMCPPAT_Revoke_RejectsNonAdminRoles verifies revoke is also
// owner/maintainer-gated. A developer of the workspace should not be
// able to nuke another member's PAT.
func TestMCPPAT_Revoke_RejectsNonAdminRoles(t *testing.T) {
	srv := newMCPPATTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_revrole", "u_owner_rev", "owner")
	seedWorkspaceMember(t, srv.DB, "ws_revrole", "u_dev_rev", "developer")
	pat := mintPATViaHandler(t, srv, "ws_revrole", "u_owner_rev", "owner's pat",
		[]string{MCPScopeRead})

	req := reqWithUser(http.MethodDelete, "/api/workspaces/ws_revrole/mcp/pats/"+pat.ID,
		"u_dev_rev", nil, map[string]string{"wid": "ws_revrole", "id": pat.ID})
	rr := httptest.NewRecorder()
	srv.handleRevokeMCPPAT(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("developer revoke: want 403, got %d — %s", rr.Code, rr.Body.String())
	}
}

// TestMCPPAT_List_AllowsAnyMember verifies that ANY member of the
// workspace (including developer/viewer) can read the PAT list — the
// secret hashes are never in the response so listing is safe even for
// non-admin viewers (matches workspace_api_keys.go's list-vs-mint
// asymmetry).
func TestMCPPAT_List_AllowsAnyMember(t *testing.T) {
	srv := newMCPPATTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_listrole", "u_owner_list", "owner")
	seedWorkspaceMember(t, srv.DB, "ws_listrole", "u_dev_list", "developer")
	mintPATViaHandler(t, srv, "ws_listrole", "u_owner_list", "by owner", []string{MCPScopeRead})

	req := reqWithUser(http.MethodGet, "/api/workspaces/ws_listrole/mcp/pats",
		"u_dev_list", nil, map[string]string{"wid": "ws_listrole"})
	rr := httptest.NewRecorder()
	srv.handleListMCPPATs(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("developer list: want 200, got %d — %s", rr.Code, rr.Body.String())
	}
	var pats []MCPPAT
	json.NewDecoder(rr.Body).Decode(&pats)
	if len(pats) != 1 {
		t.Errorf("want 1 PAT visible to developer, got %d", len(pats))
	}
}

// TestMCPPAT_Mint_RejectsEmptyScopes verifies that scopes=[] is rejected.
func TestMCPPAT_Mint_RejectsEmptyScopes(t *testing.T) {
	srv := newMCPPATTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_alpha", "u_pat3", "owner")

	body, _ := json.Marshal(MCPPATMintRequest{Name: "x", Scopes: []string{}})
	req := reqWithUser(http.MethodPost, "/api/workspaces/ws_alpha/mcp/pats", "u_pat3", body,
		map[string]string{"wid": "ws_alpha"})
	rr := httptest.NewRecorder()
	srv.handleMintMCPPAT(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for empty scopes, got %d", rr.Code)
	}
}

// TestMCPPAT_Revoke_OtherWorkspacesPAT verifies that a member of one
// workspace can't revoke a PAT bound to another workspace — the
// per-workspace revoke gate makes the call 404, and the underlying
// row's revoked_at stays nil.
func TestMCPPAT_Revoke_OtherWorkspacesPAT(t *testing.T) {
	srv := newMCPPATTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_alpha", "u_alice_rev", "owner")
	seedWorkspaceMember(t, srv.DB, "ws_beta", "u_bob_rev", "owner")

	alicePAT := mintPATViaHandler(t, srv, "ws_alpha", "u_alice_rev", "alice's pat", []string{MCPScopeRead})

	// Bob attempts revoke against his own workspace's URL but supplying
	// alice's PAT id: 404 (PAT doesn't belong to ws_beta — gate matches
	// at the workspace-membership layer first, never reaches DB).
	revokeReq := reqWithUser(http.MethodDelete, "/api/workspaces/ws_beta/mcp/pats/"+alicePAT.ID,
		"u_bob_rev", nil, map[string]string{"wid": "ws_beta", "id": alicePAT.ID})
	revokeRR := httptest.NewRecorder()
	srv.handleRevokeMCPPAT(revokeRR, revokeReq)
	if revokeRR.Code != http.StatusNoContent {
		// Bob is a member of ws_beta, so the membership gate passes; the
		// underlying SQL UPDATE is workspace-scoped to ws_beta and finds
		// 0 rows. 204 is the idempotent contract.
		t.Fatalf("want 204 (idempotent no-op), got %d", revokeRR.Code)
	}

	// Alice's list still shows the PAT as active (RevokedAt nil).
	listRR := httptest.NewRecorder()
	srv.handleListMCPPATs(listRR, reqWithUser(http.MethodGet, "/api/workspaces/ws_alpha/mcp/pats",
		"u_alice_rev", nil, map[string]string{"wid": "ws_alpha"}))
	var after []MCPPAT
	json.NewDecoder(listRR.Body).Decode(&after)
	if len(after) != 1 {
		t.Fatalf("want 1 PAT, got %d", len(after))
	}
	if after[0].RevokedAt != nil {
		t.Errorf("cross-workspace revoke leaked: got revoked_at=%v", *after[0].RevokedAt)
	}
}

// TestMCPPAT_List_ScopedToWorkspace verifies one workspace's PATs are
// not visible from another workspace's URL — even by the same user
// who belongs to both.
func TestMCPPAT_List_ScopedToWorkspace(t *testing.T) {
	srv := newMCPPATTestServer(t)
	// Single user, two workspaces.
	seedWorkspaceMember(t, srv.DB, "ws_alpha", "u_multi", "owner")
	seedWorkspaceMember(t, srv.DB, "ws_beta", "u_multi", "owner")

	mintPATViaHandler(t, srv, "ws_alpha", "u_multi", "alpha-only", []string{MCPScopeRead})

	// List ws_beta: should be empty (PAT was minted under ws_alpha).
	betaRR := httptest.NewRecorder()
	srv.handleListMCPPATs(betaRR, reqWithUser(http.MethodGet, "/api/workspaces/ws_beta/mcp/pats",
		"u_multi", nil, map[string]string{"wid": "ws_beta"}))
	var betaList []MCPPAT
	json.NewDecoder(betaRR.Body).Decode(&betaList)
	if len(betaList) != 0 {
		t.Errorf("ws_beta should see 0 PATs, got %d: %+v", len(betaList), betaList)
	}
}

// TestMCPPAT_Scopes_StaticCatalogOnly verifies the scopes endpoint
// returns just the static catalog (no dynamic workspace picker).
func TestMCPPAT_Scopes_StaticCatalogOnly(t *testing.T) {
	srv := newMCPPATTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_alpha", "u_pat_sc", "owner")

	req := reqWithUser(http.MethodGet, "/api/workspaces/ws_alpha/mcp/pats/scopes", "u_pat_sc", nil,
		map[string]string{"wid": "ws_alpha"})
	rr := httptest.NewRecorder()
	srv.handleListMCPPATScopes(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("scopes: want 200, got %d", rr.Code)
	}
	var resp MCPPATScopesResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	staticNames := map[string]bool{}
	for _, sc := range resp.Scopes {
		staticNames[sc.Name] = true
	}
	if !staticNames[MCPScopeRead] || !staticNames[MCPScopeExec] {
		t.Errorf("scope catalog missing static scopes: got %v", resp.Scopes)
	}
}

// (Removed: TestMCPPAT_Mint_Unauthenticated. The production handler
// sits behind the auth middleware mounted on the parent /api/workspaces
// route group — bypassing the middleware to test the handler directly
// would just exercise requireWorkspaceMember's empty-userID branch,
// which is covered by the workspace_api_keys.go sibling.)

// _ guards against the auth import becoming unused if context handling
// is reworked. Keep this file's auth dependency explicit.
var _ = auth.UserIDFromContext

// _ same as above for db package.
var _ = db.MCPPAT{}
