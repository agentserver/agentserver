package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newWhoamiTestServer(t *testing.T) *Server {
	t.Helper()
	d := newCodexTestDBForServer(t)
	t.Cleanup(func() {
		d.Exec(`DELETE FROM agent_cards`)
		d.Exec(`DELETE FROM proxy_tokens`)
		d.Exec(`DELETE FROM sandboxes`)
		d.Exec(`DELETE FROM workspace_members`)
		d.Exec(`DELETE FROM workspaces`)
		d.Exec(`DELETE FROM users`)
	})
	return &Server{DB: d}
}

func seedWhoamiSandbox(t *testing.T, srv *Server, token, tunnelToken, status, role, displayName string, withUser bool) {
	t.Helper()
	seedWorkspaceMember(t, srv.DB, "ws_whoami", "u_whoami", role)
	if _, err := srv.DB.Exec(
		`INSERT INTO sandboxes (id, workspace_id, name, type, status, proxy_token, tunnel_token, short_id)
		 VALUES ('sbx_whoami', 'ws_whoami', 'Sandbox Name', 'custom', $1, $2, $3, 'short-whoami')
		 ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, proxy_token = EXCLUDED.proxy_token, tunnel_token = EXCLUDED.tunnel_token`,
		status, token, tunnelToken,
	); err != nil {
		t.Fatalf("insert sandbox: %v", err)
	}
	userID := any(nil)
	if withUser {
		userID = "u_whoami"
	}
	if _, err := srv.DB.Exec(
		`INSERT INTO proxy_tokens (token, token_type, sandbox_id, workspace_id, user_id)
		 VALUES ($1, 'sandbox', 'sbx_whoami', 'ws_whoami', $2)
		 ON CONFLICT (token) DO UPDATE SET user_id = EXCLUDED.user_id`,
		token, userID,
	); err != nil {
		t.Fatalf("insert proxy token: %v", err)
	}
	if displayName != "" {
		if _, err := srv.DB.Exec(
			`INSERT INTO agent_cards (sandbox_id, workspace_id, agent_type, display_name)
			 VALUES ('sbx_whoami', 'ws_whoami', 'custom', $1)
			 ON CONFLICT (sandbox_id) DO UPDATE SET display_name = EXCLUDED.display_name`,
			displayName,
		); err != nil {
			t.Fatalf("insert agent card: %v", err)
		}
	}
}

func callWhoami(t *testing.T, srv *Server, authz string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/agent/whoami", nil)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rr := httptest.NewRecorder()
	srv.handleAgentWhoami(rr, req)
	return rr
}

func TestStrictBearerToken(t *testing.T) {
	cases := []struct {
		name      string
		authz     string
		wantToken string
		wantOK    bool
	}{
		{"missing", "", "", false},
		{"basic", "Basic token", "", false},
		{"empty bearer", "Bearer ", "", false},
		{"valid", "Bearer proxy-token", "proxy-token", true},
		{"trims token spaces", "Bearer   proxy-token  ", "proxy-token", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/agent/whoami", nil)
			if tc.authz != "" {
				req.Header.Set("Authorization", tc.authz)
			}
			got, ok := strictBearerToken(req)
			if got != tc.wantToken || ok != tc.wantOK {
				t.Fatalf("strictBearerToken() = (%q, %v), want (%q, %v)", got, ok, tc.wantToken, tc.wantOK)
			}
		})
	}
}

func TestActiveWhoamiSandboxStatus(t *testing.T) {
	for _, status := range []string{"creating", "running"} {
		if !activeWhoamiSandboxStatus(status) {
			t.Fatalf("%q should be active", status)
		}
	}
	for _, status := range []string{"paused", "offline", "deleting", "pausing", ""} {
		if activeWhoamiSandboxStatus(status) {
			t.Fatalf("%q should not be active", status)
		}
	}
}

func TestAgentWhoami_HappyPath(t *testing.T) {
	srv := newWhoamiTestServer(t)
	seedWhoamiSandbox(t, srv, "proxy-good", "tunnel-good", "running", "developer", "Display Agent", true)

	rr := callWhoami(t, srv, "Bearer proxy-good")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rr.Header().Get("Cache-Control"))
	}
	var out AgentWhoamiResponse
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.UserID != "u_whoami" || out.WorkspaceID != "ws_whoami" || out.WorkspaceName != "test ws" ||
		out.SandboxID != "sbx_whoami" || out.ShortID != "short-whoami" ||
		out.DisplayName != "Display Agent" || out.Role != "developer" {
		t.Fatalf("unexpected response: %+v", out)
	}
}

func TestAgentWhoami_UnauthorizedCases(t *testing.T) {
	srv := newWhoamiTestServer(t)
	seedWhoamiSandbox(t, srv, "proxy-good", "tunnel-good", "running", "developer", "", true)
	if _, err := srv.DB.Exec(
		`INSERT INTO proxy_tokens (token, token_type, workspace_id)
		 VALUES ('workspace-token', 'workspace', 'ws_whoami')
		 ON CONFLICT DO NOTHING`,
	); err != nil {
		t.Fatalf("insert workspace token: %v", err)
	}

	cases := []struct {
		name  string
		authz string
	}{
		{"missing", ""},
		{"malformed", "Basic proxy-good"},
		{"empty bearer", "Bearer "},
		{"unknown", "Bearer nope"},
		{"workspace token", "Bearer workspace-token"},
		{"tunnel token", "Bearer tunnel-good"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := callWhoami(t, srv, tc.authz)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d: %s", rr.Code, rr.Body.String())
			}
			if rr.Body.String() != "unauthorized\n" {
				t.Fatalf("body = %q", rr.Body.String())
			}
		})
	}
}

func TestAgentWhoami_ForbiddenCases(t *testing.T) {
	for _, status := range []string{"paused", "offline", "deleting", "pausing"} {
		t.Run("status_"+status, func(t *testing.T) {
			srv := newWhoamiTestServer(t)
			seedWhoamiSandbox(t, srv, "proxy-forbidden", "tunnel-forbidden", status, "developer", "", true)
			rr := callWhoami(t, srv, "Bearer proxy-forbidden")
			if rr.Code != http.StatusForbidden {
				t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body.String())
			}
		})
	}

	t.Run("legacy_null_user", func(t *testing.T) {
		srv := newWhoamiTestServer(t)
		seedWhoamiSandbox(t, srv, "proxy-legacy", "tunnel-legacy", "running", "developer", "", false)
		rr := callWhoami(t, srv, "Bearer proxy-legacy")
		if rr.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("membership_removed", func(t *testing.T) {
		srv := newWhoamiTestServer(t)
		seedWhoamiSandbox(t, srv, "proxy-removed", "tunnel-removed", "running", "developer", "", true)
		if _, err := srv.DB.Exec(`DELETE FROM workspace_members WHERE workspace_id = 'ws_whoami' AND user_id = 'u_whoami'`); err != nil {
			t.Fatalf("delete membership: %v", err)
		}
		rr := callWhoami(t, srv, "Bearer proxy-removed")
		if rr.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body.String())
		}
	})
}

func TestAgentWhoami_DisplayNameFallsBackToSandboxName(t *testing.T) {
	srv := newWhoamiTestServer(t)
	seedWhoamiSandbox(t, srv, "proxy-fallback", "tunnel-fallback", "running", "developer", "", true)
	rr := callWhoami(t, srv, "Bearer proxy-fallback")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var out AgentWhoamiResponse
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.DisplayName != "Sandbox Name" {
		t.Fatalf("display_name = %q, want Sandbox Name", out.DisplayName)
	}
}
