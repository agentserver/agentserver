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

func seedWhoamiSandbox(t *testing.T, srv *Server, token, tunnelToken, status, role, displayName string, withUser, isLocal bool) {
	t.Helper()
	seedWorkspaceMember(t, srv.DB, "ws_whoami", "u_whoami", role)
	if _, err := srv.DB.Exec(
		`INSERT INTO sandboxes (id, workspace_id, name, type, status, is_local, proxy_token, tunnel_token, short_id)
		 VALUES ('sbx_whoami', 'ws_whoami', 'Sandbox Name', 'custom', $1, $2, $3, $4, 'short-whoami')
		 ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, is_local = EXCLUDED.is_local, proxy_token = EXCLUDED.proxy_token, tunnel_token = EXCLUDED.tunnel_token`,
		status, isLocal, token, tunnelToken,
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

func TestAgentWhoami_HappyPath(t *testing.T) {
	srv := newWhoamiTestServer(t)
	seedWhoamiSandbox(t, srv, "proxy-good", "tunnel-good", "running", "developer", "Display Agent", true, false)

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
	if out.SandboxStatus != "running" {
		t.Fatalf("sandbox_status = %q, want %q", out.SandboxStatus, "running")
	}
}

func TestAgentWhoami_UnauthorizedCases(t *testing.T) {
	srv := newWhoamiTestServer(t)
	seedWhoamiSandbox(t, srv, "proxy-good", "tunnel-good", "running", "developer", "", true, false)
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

// TestAgentWhoami_ForbiddenIdentity covers the two cases that legitimately
// fail identity verification and must continue to return 403:
//
//   - legacy_null_user: a proxy_tokens row whose user_id is NULL (predates
//     migration 034). The handler short-circuits in
//     internal/db/agent_whoami.go:38.
//   - membership_removed: the user was removed from the workspace after the
//     token was issued, so the JOIN against workspace_members returns no
//     rows. The handler short-circuits in internal/db/agent_whoami.go:67.
func TestAgentWhoami_ForbiddenIdentity(t *testing.T) {
	t.Run("legacy_null_user", func(t *testing.T) {
		srv := newWhoamiTestServer(t)
		seedWhoamiSandbox(t, srv, "proxy-legacy", "tunnel-legacy", "running", "developer", "", false, false)
		rr := callWhoami(t, srv, "Bearer proxy-legacy")
		if rr.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body.String())
		}
		if rr.Body.String() != "forbidden\n" {
			t.Fatalf("body = %q", rr.Body.String())
		}
	})

	t.Run("membership_removed", func(t *testing.T) {
		srv := newWhoamiTestServer(t)
		seedWhoamiSandbox(t, srv, "proxy-removed", "tunnel-removed", "running", "developer", "", true, false)
		if _, err := srv.DB.Exec(`DELETE FROM workspace_members WHERE workspace_id = 'ws_whoami' AND user_id = 'u_whoami'`); err != nil {
			t.Fatalf("delete membership: %v", err)
		}
		rr := callWhoami(t, srv, "Bearer proxy-removed")
		if rr.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body.String())
		}
		if rr.Body.String() != "forbidden\n" {
			t.Fatalf("body = %q", rr.Body.String())
		}
	})
}

// TestAgentWhoami_RuntimeStatusReportedInBody asserts the post-#290 contract:
// once identity verification passes, whoami returns 200 regardless of the
// sandbox's current runtime status, and the status appears verbatim in the
// response body so callers (observer-server etc.) can decide how to react.
// Previously paused/pausing/resuming/offline/deleting all returned 403.
func TestAgentWhoami_RuntimeStatusReportedInBody(t *testing.T) {
	// Status values from internal/sbxstore/state.go:5-12, plus the empty
	// string to pin behavior for legacy rows where status is unset.
	statuses := []string{"creating", "running", "pausing", "paused", "resuming", "offline", "deleting", ""}
	for _, status := range statuses {
		for _, isLocal := range []bool{false, true} {
			name := "status_" + status
			if status == "" {
				name = "status_empty"
			}
			if isLocal {
				name += "_local"
			} else {
				name += "_cloud"
			}
			t.Run(name, func(t *testing.T) {
				srv := newWhoamiTestServer(t)
				seedWhoamiSandbox(t, srv, "proxy-rt", "tunnel-rt", status, "developer", "", true, isLocal)
				rr := callWhoami(t, srv, "Bearer proxy-rt")
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
				if out.SandboxStatus != status {
					t.Fatalf("sandbox_status = %q, want %q", out.SandboxStatus, status)
				}
				// Identity fields must still be populated.
				if out.UserID != "u_whoami" || out.WorkspaceID != "ws_whoami" ||
					out.SandboxID != "sbx_whoami" || out.Role != "developer" {
					t.Fatalf("identity fields missing in response: %+v", out)
				}
			})
		}
	}
}

func TestAgentWhoami_DisplayNameFallsBackToSandboxName(t *testing.T) {
	srv := newWhoamiTestServer(t)
	seedWhoamiSandbox(t, srv, "proxy-fallback", "tunnel-fallback", "running", "developer", "", true, false)
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
	if out.SandboxStatus != "running" {
		t.Fatalf("sandbox_status = %q, want %q", out.SandboxStatus, "running")
	}
}
