package mcppublic

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/db"
)

// fakeDB stubs the dbReader surface so the resolver tests stay pure-Go
// (no postgres dependency). Each test sets up the fields it needs.
type fakeDB struct {
	pat        *db.MCPPAT
	patErr     error
	workspaces []*db.Workspace
	wsErr      error
	touched    []string
}

func (f *fakeDB) ValidateMCPPATSecret(_ context.Context, prefix, secret string) (*db.MCPPAT, error) {
	_, _ = prefix, secret
	return f.pat, f.patErr
}

func (f *fakeDB) TouchMCPPATLastUsed(_ context.Context, id string) error {
	f.touched = append(f.touched, id)
	return nil
}

func (f *fakeDB) ListWorkspacesByUser(_ string) ([]*db.Workspace, error) {
	return f.workspaces, f.wsErr
}

// validPATForm returns a syntactically valid agpat_ token: 6-char
// prefix, 16-char id, '_', 48-char secret, 6-char CRC. Only its
// shape matters — the fake DB short-circuits the hash check.
const validPAT = "agpat_" +
	"abcdefghijklmnop" + "_" +
	"abcdefghijklmnopabcdefghijklmnopabcdefghijklmnop" +
	"zzzzzz" // wrong CRC; tests that need parse success use validPATGoodCRC

// validPATGoodCRC is a real agpat_ token built by secrets.Mint at
// init time — needed for the resolver tests that rely on secrets.Parse
// returning ok. Lives in auth_helper_test.go to keep this file pure
// strings (build doesn't depend on global init order across files
// in the same package, but reading test-only code is clearer this way).

// TestPATResolver_ParseRejected verifies a malformed agpat_ token
// (bad CRC) returns ErrUnknown without hitting the DB.
func TestPATResolver_ParseRejected(t *testing.T) {
	f := &fakeDB{}
	r := PATResolver{DB: f}
	_, err := r.Resolve(context.Background(), validPAT) // bad CRC
	if !errors.Is(err, ErrUnknown) {
		t.Fatalf("want ErrUnknown for bad CRC, got %v", err)
	}
	if f.pat != nil || f.patErr != nil {
		t.Fatal("DB should not be hit on parse failure")
	}
}

// TestPATResolver_WrongPrefix verifies non-agpat tokens are
// ErrUnknown (so the middleware tries the next resolver).
func TestPATResolver_WrongPrefix(t *testing.T) {
	r := PATResolver{DB: &fakeDB{}}
	for _, raw := range []string{
		"ast_aaaaaaaaaaaaaaaa_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbcccccc",
		"oauth_opaque_token",
		"",
	} {
		_, err := r.Resolve(context.Background(), raw)
		if !errors.Is(err, ErrUnknown) {
			t.Errorf("want ErrUnknown for %q, got %v", raw, err)
		}
	}
}

// TestPATResolver_DBMiss verifies ValidateMCPPATSecret returning
// sql.ErrNoRows (revoked / expired / wrong hash) becomes ErrInvalid.
func TestPATResolver_DBMiss(t *testing.T) {
	tok := mintTestPAT(t)
	f := &fakeDB{patErr: sql.ErrNoRows}
	r := PATResolver{DB: f}
	_, err := r.Resolve(context.Background(), tok)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

// TestPATResolver_ReadOnlyDerivesReadTools verifies an mcp:read PAT
// gets only the read-tool set.
func TestPATResolver_ReadOnlyDerivesReadTools(t *testing.T) {
	tok := mintTestPAT(t)
	f := &fakeDB{
		pat: &db.MCPPAT{
			ID:          "agpat_test",
			UserID:      "u_alice",
			WorkspaceID: "ws_alpha",
			Scopes:      []string{"mcp:read"},
		},
		workspaces: []*db.Workspace{{ID: "ws_alpha", Name: "alpha"}},
	}
	r := PATResolver{DB: f}
	p, err := r.Resolve(context.Background(), tok)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.WorkspaceID != "ws_alpha" {
		t.Errorf("WorkspaceID: got %q, want ws_alpha", p.WorkspaceID)
	}
	if !p.HasTool("read_file") || !p.HasTool("list_environments") {
		t.Errorf("read scope missing read tools: %+v", p.Tools)
	}
	if p.HasTool("shell") {
		t.Errorf("read-only PAT must not grant shell")
	}
}

// TestPATResolver_ExecAddsExecTools verifies mcp:exec stacks on top
// of mcp:read.
func TestPATResolver_ExecAddsExecTools(t *testing.T) {
	tok := mintTestPAT(t)
	f := &fakeDB{
		pat: &db.MCPPAT{
			ID:          "agpat_test",
			UserID:      "u_alice",
			WorkspaceID: "ws_alpha",
			Scopes:      []string{"mcp:read", "mcp:exec"},
		},
		workspaces: []*db.Workspace{{ID: "ws_alpha", Name: "alpha"}},
	}
	r := PATResolver{DB: f}
	p, _ := r.Resolve(context.Background(), tok)
	for _, want := range []string{"shell", "exec_command", "apply_patch", "copy_path"} {
		if !p.HasTool(want) {
			t.Errorf("exec scope missing %s", want)
		}
	}
}

// TestPATResolver_WorkspaceFromPATRow verifies the Principal's
// WorkspaceID comes from the PAT row's intrinsic workspace_id column
// (post the 2026-06-15 amendment). The pre-amendment design derived
// it from `workspace:<id>` scope strings — this test exists to pin
// the new contract.
func TestPATResolver_WorkspaceFromPATRow(t *testing.T) {
	tok := mintTestPAT(t)
	f := &fakeDB{
		pat: &db.MCPPAT{
			ID:          "agpat_test",
			UserID:      "u_alice",
			WorkspaceID: "ws_personal", // distinguishable from membership list below
			Scopes:      []string{"mcp:read"},
		},
		workspaces: []*db.Workspace{
			{ID: "ws_personal", Name: "personal"},
			{ID: "ws_work", Name: "work"},
		},
	}
	r := PATResolver{DB: f}
	p, err := r.Resolve(context.Background(), tok)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.WorkspaceID != "ws_personal" {
		t.Errorf("WorkspaceID: got %q, want ws_personal (from PAT row, not membership union)", p.WorkspaceID)
	}
	if !p.HasWorkspace("ws_personal") {
		t.Errorf("HasWorkspace(ws_personal) = false; principal claims to own no workspace")
	}
	if p.HasWorkspace("ws_work") {
		t.Errorf("HasWorkspace(ws_work) = true; PAT is bound only to ws_personal")
	}
}

// TestPATResolver_MembershipRevoked verifies that a user kicked out of
// the PAT's workspace after PAT issuance gets ErrInvalid — the PAT
// row still exists (FK CASCADE only fires on workspace delete, not on
// membership removal) but the resolver cross-checks.
func TestPATResolver_MembershipRevoked(t *testing.T) {
	tok := mintTestPAT(t)
	f := &fakeDB{
		pat: &db.MCPPAT{
			ID:          "agpat_test",
			UserID:      "u_alice",
			WorkspaceID: "ws_kicked",
			Scopes:      []string{"mcp:read"},
		},
		// Alice's current memberships do NOT include ws_kicked.
		workspaces: []*db.Workspace{{ID: "ws_other", Name: "other"}},
	}
	r := PATResolver{DB: f}
	_, err := r.Resolve(context.Background(), tok)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid for revoked membership, got %v", err)
	}
}

// TestPATResolver_LegacyWorkspaceScopeIgnored verifies that a leftover
// `workspace:<id>` scope from a pre-amendment DB row grants nothing
// extra — the scope is dropped with a warn log; the principal's
// workspace still comes from the row's intrinsic workspace_id.
func TestPATResolver_LegacyWorkspaceScopeIgnored(t *testing.T) {
	tok := mintTestPAT(t)
	f := &fakeDB{
		pat: &db.MCPPAT{
			ID:          "agpat_test",
			UserID:      "u_alice",
			WorkspaceID: "ws_alpha",
			Scopes:      []string{"mcp:read", "workspace:ws_smuggle"},
		},
		workspaces: []*db.Workspace{
			{ID: "ws_alpha", Name: "alpha"},
			{ID: "ws_smuggle", Name: "smuggle"},
		},
	}
	r := PATResolver{DB: f}
	p, _ := r.Resolve(context.Background(), tok)
	if p.WorkspaceID != "ws_alpha" {
		t.Errorf("WorkspaceID: got %q, want ws_alpha (legacy scope must not pivot the binding)", p.WorkspaceID)
	}
	if p.HasWorkspace("ws_smuggle") {
		t.Errorf("legacy workspace:<id> scope leaked an extra workspace")
	}
}

// TestPATResolver_UnknownScopeDropped verifies scopes the gateway
// doesn't understand grant nothing (no panic, no tool surface).
func TestPATResolver_UnknownScopeDropped(t *testing.T) {
	tok := mintTestPAT(t)
	f := &fakeDB{
		pat: &db.MCPPAT{
			ID:          "agpat_test",
			UserID:      "u_alice",
			WorkspaceID: "ws_alpha",
			Scopes:      []string{"mcp:future-feature"},
		},
		workspaces: []*db.Workspace{{ID: "ws_alpha", Name: "alpha"}},
	}
	r := PATResolver{DB: f}
	p, _ := r.Resolve(context.Background(), tok)
	if len(p.Tools) != 0 {
		t.Errorf("unknown scope should grant no tools, got %+v", p.Tools)
	}
}

// --- Middleware tests ---

// noopHandler records whether it ran and what Principal it saw.
type noopHandler struct {
	called    bool
	principal *Principal
}

func (h *noopHandler) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	h.called = true
	h.principal = PrincipalFromContext(r.Context())
}

// stubResolver returns a fixed (Principal, err) pair regardless of input.
type stubResolver struct {
	p   *Principal
	err error
}

func (s stubResolver) Resolve(_ context.Context, _ string) (*Principal, error) {
	return s.p, s.err
}

func TestMiddleware_NoAuthHeader(t *testing.T) {
	h := &noopHandler{}
	mw := &Middleware{Resolvers: []PrincipalResolver{stubResolver{p: &Principal{UserID: "u"}}}}
	w := httptest.NewRecorder()
	mw.Wrap(h).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	if h.called {
		t.Fatal("handler should not be called when auth missing")
	}
	if got := w.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
		t.Errorf("want WWW-Authenticate Bearer…, got %q", got)
	}
}

func TestMiddleware_MalformedScheme(t *testing.T) {
	for _, h := range []string{"Basic abc", "Token xyz", "Bearer", "Bearer "} {
		t.Run(h, func(t *testing.T) {
			handler := &noopHandler{}
			mw := &Middleware{Resolvers: []PrincipalResolver{stubResolver{p: &Principal{UserID: "u"}}}}
			r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			r.Header.Set("Authorization", h)
			w := httptest.NewRecorder()
			mw.Wrap(handler).ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("want 401 for %q, got %d", h, w.Code)
			}
		})
	}
}

func TestMiddleware_ResolverReturnsErrUnknown_FallsThrough(t *testing.T) {
	// Two resolvers: first returns ErrUnknown (prefix didn't match),
	// second returns OK. Middleware should consult both.
	wantP := &Principal{UserID: "u_alice"}
	mw := &Middleware{Resolvers: []PrincipalResolver{
		stubResolver{err: ErrUnknown},
		stubResolver{p: wantP},
	}}
	h := &noopHandler{}
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer something")
	w := httptest.NewRecorder()
	mw.Wrap(h).ServeHTTP(w, r)
	if !h.called {
		t.Fatal("handler should have been called")
	}
	if h.principal != wantP {
		t.Errorf("handler saw wrong principal: %+v", h.principal)
	}
}

func TestMiddleware_ResolverReturnsErrInvalid_NoFallthrough(t *testing.T) {
	// First resolver claims the token (ErrInvalid, not ErrUnknown).
	// Middleware MUST stop there — falling through to a second resolver
	// would let a revoked agpat_ try to validate via OAuth introspection.
	calledSecond := false
	mw := &Middleware{Resolvers: []PrincipalResolver{
		stubResolver{err: ErrInvalid},
		stubResolverFn(func() {
			calledSecond = true
		}),
	}}
	h := &noopHandler{}
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer agpat_revoked")
	w := httptest.NewRecorder()
	mw.Wrap(h).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	if calledSecond {
		t.Error("second resolver should not be consulted after ErrInvalid")
	}
}

func TestMiddleware_AdvertisesResourceMetadata(t *testing.T) {
	mw := &Middleware{
		Resolvers:           []PrincipalResolver{stubResolver{err: ErrInvalid}},
		ResourceMetadataURL: "https://mcp.example.com/.well-known/oauth-protected-resource",
	}
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer x")
	w := httptest.NewRecorder()
	mw.Wrap(&noopHandler{}).ServeHTTP(w, r)
	got := w.Header().Get("WWW-Authenticate")
	if !strings.Contains(got, `resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`) {
		t.Errorf("WWW-Authenticate missing resource_metadata: %q", got)
	}
}

// stubResolverFn lets a test observe whether Resolve was called.
type stubResolverFn func()

func (f stubResolverFn) Resolve(_ context.Context, _ string) (*Principal, error) {
	f()
	return nil, ErrUnknown
}
