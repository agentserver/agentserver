package mcppublic

import (
	"context"
	"errors"
	"testing"

	"github.com/agentserver/agentserver/internal/db"
)

// stubIntrospector lets tests inject a canned introspection response
// (or error) without standing up Hydra.
type stubIntrospector struct {
	res *IntrospectionResult
	err error
	// calls records every token Introspect was called with, so tests
	// can assert prefix-gating worked (the resolver must NOT call
	// Introspect for non-ory_at_ tokens).
	calls []string
}

func (s *stubIntrospector) Introspect(_ context.Context, token string) (*IntrospectionResult, error) {
	s.calls = append(s.calls, token)
	return s.res, s.err
}

// newOAuthResolver builds a resolver with the stub introspector wired
// to f and the audience pinned to a stable test value. Tests that
// want to exercise the disabled-introspector branch just stick nil
// into Introspector.
func newOAuthResolver(f *fakeDB, s *stubIntrospector) *OAuthResolver {
	return &OAuthResolver{
		DB:               f,
		Introspector:     s,
		ExpectedAudience: "https://mcp.example.com/mcp",
	}
}

// TestOAuthResolver_NilIntrospectorIsErrUnknown — the resolver must
// be a no-op when wiring forgot to set Introspector (e.g. dev env
// without HYDRA_ADMIN_URL). Without this the middleware would 500
// every request the moment OAuth is "disabled".
func TestOAuthResolver_NilIntrospectorIsErrUnknown(t *testing.T) {
	r := OAuthResolver{DB: &fakeDB{}, ExpectedAudience: "https://x/mcp"}
	_, err := r.Resolve(context.Background(), "ory_at_anything")
	if !errors.Is(err, ErrUnknown) {
		t.Fatalf("want ErrUnknown when introspector unset, got %v", err)
	}
}

// TestOAuthResolver_WrongPrefixIsErrUnknown — gates introspection
// behind ory_at_ prefix so PATs (agpat_) and arbitrary junk never
// cost a Hydra round trip.
func TestOAuthResolver_WrongPrefixIsErrUnknown(t *testing.T) {
	s := &stubIntrospector{}
	r := newOAuthResolver(&fakeDB{}, s)
	for _, raw := range []string{
		"agpat_aaaaaaaaaaaaaaaa_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbcccccc",
		"random_bearer_value",
		"",
		"ory_rt_refresh_token_not_an_access_token",
	} {
		_, err := r.Resolve(context.Background(), raw)
		if !errors.Is(err, ErrUnknown) {
			t.Errorf("want ErrUnknown for %q, got %v", raw, err)
		}
	}
	if len(s.calls) != 0 {
		t.Fatalf("introspector should not have been called for non-ory_at_ tokens, got %d calls", len(s.calls))
	}
}

// TestOAuthResolver_HappyPath — Hydra returns an active token with
// the right audience + workspace claims; the user is still a member
// of that workspace; both scopes present → both tool sets granted.
func TestOAuthResolver_HappyPath(t *testing.T) {
	f := &fakeDB{
		workspaces: []*db.Workspace{{ID: "ws_42"}},
	}
	s := &stubIntrospector{
		res: &IntrospectionResult{
			Active:   true,
			Subject:  "user_abc",
			ClientID: "client_xyz",
			Scope:    "mcp:read mcp:exec openid",
			Audience: audience{"https://mcp.example.com/mcp"},
			Ext: map[string]interface{}{
				"workspace_id":   "ws_42",
				"workspace_role": "developer",
			},
		},
	}
	r := newOAuthResolver(f, s)
	p, err := r.Resolve(context.Background(), "ory_at_validtoken")
	if err != nil {
		t.Fatalf("happy path: %v", err)
	}
	if p.UserID != "user_abc" || p.WorkspaceID != "ws_42" {
		t.Errorf("unexpected principal: %+v", p)
	}
	if p.OAuthSub != "user_abc" {
		t.Errorf("OAuthSub should be the introspect Subject, got %q", p.OAuthSub)
	}
	if p.PATId != "" {
		t.Errorf("OAuth-derived principal must not have PATId set, got %q", p.PATId)
	}
	if !p.HasTool("read_file") {
		t.Error("mcp:read scope should grant read_file")
	}
	if !p.HasTool("shell") {
		t.Error("mcp:exec scope should grant shell")
	}
}

// TestOAuthResolver_InactiveTokenIsErrInvalid — Hydra returns
// {"active": false} for expired/revoked tokens; that's a successful
// introspection of an unusable token, not an error.
func TestOAuthResolver_InactiveTokenIsErrInvalid(t *testing.T) {
	s := &stubIntrospector{res: &IntrospectionResult{Active: false}}
	r := newOAuthResolver(&fakeDB{}, s)
	_, err := r.Resolve(context.Background(), "ory_at_revoked")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid for inactive token, got %v", err)
	}
}

// TestOAuthResolver_AudienceMismatchIsErrInvalid — RFC 8707
// resource-indicator binding. A token issued for a different MCP
// server in the same Hydra deployment must not authenticate here.
func TestOAuthResolver_AudienceMismatchIsErrInvalid(t *testing.T) {
	f := &fakeDB{workspaces: []*db.Workspace{{ID: "ws_42"}}}
	s := &stubIntrospector{
		res: &IntrospectionResult{
			Active:   true,
			Subject:  "user_abc",
			Scope:    "mcp:read",
			Audience: audience{"https://other-mcp.example.com/mcp"},
			Ext:      map[string]interface{}{"workspace_id": "ws_42", "workspace_role": "developer"},
		},
	}
	r := newOAuthResolver(f, s)
	_, err := r.Resolve(context.Background(), "ory_at_wrongaud")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid for audience mismatch, got %v", err)
	}
}

// TestOAuthResolver_AudienceArrayMatches — `aud` may be a JSON
// array; the resolver must accept a match anywhere in the list, not
// just the first entry. This is the common case for Hydra clients
// registered with multiple audiences.
func TestOAuthResolver_AudienceArrayMatches(t *testing.T) {
	f := &fakeDB{workspaces: []*db.Workspace{{ID: "ws_42"}}}
	s := &stubIntrospector{
		res: &IntrospectionResult{
			Active:   true,
			Subject:  "user_abc",
			Scope:    "mcp:read",
			Audience: audience{"https://something-else", "https://mcp.example.com/mcp"},
			Ext:      map[string]interface{}{"workspace_id": "ws_42", "workspace_role": "developer"},
		},
	}
	r := newOAuthResolver(f, s)
	if _, err := r.Resolve(context.Background(), "ory_at_multi"); err != nil {
		t.Fatalf("multi-aud should match if want is in list: %v", err)
	}
}

// TestOAuthResolver_MissingWorkspaceClaimIsErrInvalid — tokens that
// didn't go through our consent screen (e.g. issued by codex-auth's
// device flow which doesn't pick a workspace) must not authenticate.
func TestOAuthResolver_MissingWorkspaceClaimIsErrInvalid(t *testing.T) {
	s := &stubIntrospector{
		res: &IntrospectionResult{
			Active:   true,
			Subject:  "user_abc",
			Scope:    "mcp:read",
			Audience: audience{"https://mcp.example.com/mcp"},
			Ext:      map[string]interface{}{}, // no workspace_id
		},
	}
	r := newOAuthResolver(&fakeDB{}, s)
	_, err := r.Resolve(context.Background(), "ory_at_noworkspace")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid for missing workspace_id, got %v", err)
	}
}

// TestOAuthResolver_KickedOutUserIsErrInvalid — token has a valid
// workspace claim, but the user got kicked from that workspace
// between issuance and use. Mirrors the PATResolver step-4 check.
func TestOAuthResolver_KickedOutUserIsErrInvalid(t *testing.T) {
	f := &fakeDB{
		// User has SOME workspaces, just not ws_42 (the one the
		// token names) — kicked.
		workspaces: []*db.Workspace{{ID: "ws_other"}},
	}
	s := &stubIntrospector{
		res: &IntrospectionResult{
			Active:   true,
			Subject:  "user_abc",
			Scope:    "mcp:read",
			Audience: audience{"https://mcp.example.com/mcp"},
			Ext:      map[string]interface{}{"workspace_id": "ws_42", "workspace_role": "developer"},
		},
	}
	r := newOAuthResolver(f, s)
	_, err := r.Resolve(context.Background(), "ory_at_kicked")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid when user is no longer a member, got %v", err)
	}
}

// TestOAuthResolver_IntrospectErrorBubblesUp — Hydra outage / 5xx
// must NOT be silently treated as "token invalid" (that would be
// fail-open). The middleware turns this into a 500, which is the
// correct fail-closed posture.
func TestOAuthResolver_IntrospectErrorBubblesUp(t *testing.T) {
	s := &stubIntrospector{err: errors.New("hydra is down")}
	r := newOAuthResolver(&fakeDB{}, s)
	_, err := r.Resolve(context.Background(), "ory_at_anything")
	if err == nil {
		t.Fatal("want bubbled-up error on hydra failure, got nil")
	}
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrUnknown) {
		t.Fatalf("hydra outage must not collapse to a sentinel, got %v", err)
	}
}

// TestOAuthResolver_OnlyReadScopeWithdrawsExecTools — partial scope
// grants must not leak unauthorized tools. A token with only mcp:read
// must not grant any ToolsExec entries.
func TestOAuthResolver_OnlyReadScopeWithdrawsExecTools(t *testing.T) {
	f := &fakeDB{workspaces: []*db.Workspace{{ID: "ws_42"}}}
	s := &stubIntrospector{
		res: &IntrospectionResult{
			Active:   true,
			Subject:  "user_abc",
			Scope:    "mcp:read", // no exec
			Audience: audience{"https://mcp.example.com/mcp"},
			Ext:      map[string]interface{}{"workspace_id": "ws_42", "workspace_role": "developer"},
		},
	}
	r := newOAuthResolver(f, s)
	p, err := r.Resolve(context.Background(), "ory_at_readonly")
	if err != nil {
		t.Fatalf("read-only resolve: %v", err)
	}
	if p.HasTool("shell") || p.HasTool("apply_patch") {
		t.Errorf("read-only principal must not have exec tools: %+v", p.Tools)
	}
	if !p.HasTool("read_file") {
		t.Error("read-only principal should still have read_file")
	}
}

// TestAudienceUnmarshalAcceptsBothShapes — Hydra returns `aud` as
// either a bare string (single-audience clients, which is most of
// them) or a JSON array. The resolver's audience type must accept
// both without a special case at the call site.
func TestAudienceUnmarshalAcceptsBothShapes(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{`"single"`, []string{"single"}},
		{`["a","b"]`, []string{"a", "b"}},
		{`null`, nil},
	}
	for _, c := range cases {
		var a audience
		if err := a.UnmarshalJSON([]byte(c.raw)); err != nil {
			t.Errorf("UnmarshalJSON(%q): %v", c.raw, err)
			continue
		}
		if len(a) != len(c.want) {
			t.Errorf("UnmarshalJSON(%q): len %d, want %d", c.raw, len(a), len(c.want))
			continue
		}
		for i := range a {
			if a[i] != c.want[i] {
				t.Errorf("UnmarshalJSON(%q)[%d] = %q, want %q", c.raw, i, a[i], c.want[i])
			}
		}
	}
}
