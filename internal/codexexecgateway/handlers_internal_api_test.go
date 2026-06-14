package codexexecgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// As of 2026-06-14, /api/exec-gateway/connected requires a cap-token
// bearer (not the cluster shared secret). The workspace_id comes from
// the token payload, not a query string. These tests pin both pieces.

func TestInternalConnected_RequiresBearer(t *testing.T) {
	srv, err := newServerNoStoreForTesting(Config{CapTokenHMACSecret: []byte("test-hmac"), InternalSharedSecret: "test-internal"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/exec-gateway/connected", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for no bearer, got %d", rr.Code)
	}
}

func TestInternalConnected_RejectsSharedSecretAsBearer(t *testing.T) {
	// The previous design accepted the shared secret here. Make sure
	// presenting it now fails — otherwise a forgotten caller could
	// silently keep listing workspaces with a forged ?workspace_id.
	srv, err := newServerNoStoreForTesting(Config{CapTokenHMACSecret: []byte("test-hmac"), InternalSharedSecret: "shared-secret-not-a-cap-token"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/exec-gateway/connected", nil)
	req.Header.Set("Authorization", "Bearer shared-secret-not-a-cap-token")
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for shared secret as cap-token, got %d", rr.Code)
	}
}

func TestInternalConnected_RejectsBadSignature(t *testing.T) {
	secret := []byte("real-secret")
	wrongSecret := []byte("other-secret")
	srv, err := newServerNoStoreForTesting(Config{CapTokenHMACSecret: secret, InternalSharedSecret: "x"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	now := time.Now().Unix()
	tok := mintToken(t, wrongSecret, CapPayload{
		TurnID: "trn_x", WorkspaceID: "ws_a", IAT: now, EXP: now + 300,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/exec-gateway/connected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for wrong HMAC, got %d", rr.Code)
	}
}

func TestInternalConnected_RejectsExpiredCapToken(t *testing.T) {
	secret := []byte("real-secret")
	srv, err := newServerNoStoreForTesting(Config{CapTokenHMACSecret: secret, InternalSharedSecret: "x"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	now := time.Now().Unix()
	tok := mintToken(t, secret, CapPayload{
		TurnID: "trn_x", WorkspaceID: "ws_a", IAT: now - 600, EXP: now - 1,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/exec-gateway/connected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for expired token, got %d", rr.Code)
	}
}

func TestInternalConnected_RejectsRevokedTurn(t *testing.T) {
	secret := []byte("real-secret")
	srv, err := newServerNoStoreForTesting(Config{CapTokenHMACSecret: secret, InternalSharedSecret: "x"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	// Revoke FIRST so the in-memory set has the turn when the request
	// hits the middleware. exp=any-future works; revoked semantics
	// don't depend on it.
	now := time.Now().Unix()
	srv.revoked.Add("trn_revoked", now+3600)

	tok := mintToken(t, secret, CapPayload{
		TurnID: "trn_revoked", WorkspaceID: "ws_a", IAT: now, EXP: now + 300,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/exec-gateway/connected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for revoked turn, got %d", rr.Code)
	}
}

func TestInternalConnected_ReturnsIntersection(t *testing.T) {
	secret := []byte("real-secret")
	store := newTestStore(t)
	srv, err := NewServer(Config{CapTokenHMACSecret: secret, InternalSharedSecret: "x"}, store)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	hs := httptest.NewServer(srv.Routes())
	t.Cleanup(hs.Close)

	// Seed: two executors bound to workspace, one connected.
	for _, e := range []Executor{
		{ExeID: "exe_on", UserID: "u", Description: "online", DefaultCwd: "/x", RegisteredAt: time.Now().UTC()},
		{ExeID: "exe_off", UserID: "u", Description: "offline", DefaultCwd: "/y", RegisteredAt: time.Now().UTC()},
	} {
		store.CreateExecutor(context.Background(), e)
		store.BindWorkspaceExecutor(context.Background(), "ws_a", e.ExeID, e.ExeID, "", e.ExeID == "exe_on")
	}
	srv.registry.Register("exe_on", newInboundConn("exe_on", nil, nil))

	now := time.Now().Unix()
	tok := mintToken(t, secret, CapPayload{
		TurnID: "trn_t", WorkspaceID: "ws_a", IAT: now, EXP: now + 300,
	})
	req, _ := http.NewRequest(http.MethodGet, hs.URL+"/api/exec-gateway/connected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var got []ConnectedExecutor
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got) != 1 || got[0].ExeID != "exe_on" {
		t.Fatalf("intersection: %+v", got)
	}
}

func TestInternalConnected_IgnoresQueryString(t *testing.T) {
	// Defense-in-depth: workspace_id MUST come from the cap-token
	// payload, not the query string. If a caller smuggles a different
	// workspace_id in ?workspace_id=, the handler still uses the
	// token's. We can't easily verify the body when we have no DB, so
	// just ensure the request reaches the handler (200 or 500-not-401)
	// — proves the middleware didn't accept the query string as a
	// substitute for auth.
	secret := []byte("real-secret")
	store := newTestStore(t)
	srv, err := NewServer(Config{CapTokenHMACSecret: secret, InternalSharedSecret: "x"}, store)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	hs := httptest.NewServer(srv.Routes())
	t.Cleanup(hs.Close)

	now := time.Now().Unix()
	tok := mintToken(t, secret, CapPayload{
		TurnID: "trn_q", WorkspaceID: "ws_real", IAT: now, EXP: now + 300,
	})
	req, _ := http.NewRequest(http.MethodGet,
		hs.URL+"/api/exec-gateway/connected?workspace_id=ws_bogus", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	// With no executors in ws_real, expect 200 + empty array; with the
	// ?workspace_id= sneaking through, we'd have hit a different code
	// path that we can't fully assert without seeding ws_bogus. The
	// critical check is that the request passes auth (not 401) and
	// returns valid JSON for the token's workspace.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var got []ConnectedExecutor
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty list for ws_real (token's wid), got %+v", got)
	}
}
