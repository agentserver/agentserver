package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/db"
)

// toMCPOAuthClientResponse is the only piece of the file that doesn't
// touch DB or Hydra — pin its shape, since the frontend / docs / CLI
// users all depend on the exact field names and casing.
func TestMCPOAuthClientResponse_FieldsAreStable(t *testing.T) {
	now := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	used := now.Add(time.Minute)
	resp := toMCPOAuthClientResponse(db.MCPOAuthClient{
		ID:            "mcpoc_abcd1234abcd1234",
		UserID:        "usr_secret_should_not_leak",
		HydraClientID: "df66ecfa-25ad-404c-b364-1d94ca7f986c",
		Name:          "my-laptop",
		CreatedAt:     now,
		LastUsedAt:    &used,
	})
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	// Must include the docs-stable field names.
	for _, want := range []string{
		`"id":"mcpoc_abcd1234abcd1234"`,
		`"client_id":"df66ecfa-25ad-404c-b364-1d94ca7f986c"`,
		`"name":"my-laptop"`,
		`"created_at":"2026-06-17T09:00:00Z"`,
		`"last_used_at":"2026-06-17T09:01:00Z"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("response missing %q in: %s", want, got)
		}
	}
	// Must NOT leak the user_id (response is rendered to the owning
	// user themselves so it's not exactly secret, but exposing the
	// underlying primary key opens enumeration risks if the response
	// ever leaks to logs / third-party MCP server proxies — keep the
	// public shape minimal).
	if strings.Contains(got, "usr_secret_should_not_leak") {
		t.Errorf("response leaked user_id: %s", got)
	}
	// HydraClientID must serialize as `client_id`, not `hydra_client_id`
	// — that's the literal string the user pastes into Codex / Claude
	// Code config and is the wire-protocol field name. Changing the
	// alias is a breaking API change.
	if strings.Contains(got, "hydra_client_id") {
		t.Errorf("response uses internal name hydra_client_id instead of client_id: %s", got)
	}
}

// TestMCPOAuthClientRedirectURIs pins the loopback hostnames we
// register every public client with. Codex CLI / Claude Code both
// pick ephemeral ports at runtime — RFC 8252 §7.3 requires the auth
// server to accept any port on a registered loopback host, so a
// host-only registration suffices. If we ever shrink this list,
// users on the dropped host (e.g. someone forcing 127.0.0.1 callbacks
// when only localhost is registered) get a confusing
// "invalid_redirect_uri" error.
func TestMCPOAuthClientRedirectURIs_CoversBothLoopbackHosts(t *testing.T) {
	want := map[string]bool{
		"http://localhost/callback": false,
		"http://127.0.0.1/callback": false,
	}
	for _, u := range mcpOAuthClientRedirectURIs {
		if _, ok := want[u]; ok {
			want[u] = true
		}
	}
	for u, seen := range want {
		if !seen {
			t.Errorf("mcpOAuthClientRedirectURIs missing %q (clients bound there will fail OAuth)", u)
		}
	}
}
