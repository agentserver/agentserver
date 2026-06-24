package codexexecgateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodexExec_Register_RequiresInternalSecret(t *testing.T) {
	srv, err := newServerNoStoreForTesting(Config{
		AgentserverInternalSecret: "s3cret",
		CapTokenHMACSecret:        []byte("k"),
		InternalSharedSecret:      "is",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/codex-exec/register",
		strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
}

func TestServer_HealthZ(t *testing.T) {
	cfg := Config{
		CapTokenHMACSecret:   []byte("test-hmac-key"),
		InternalSharedSecret: "test-internal-secret",
	}
	srv, err := newServerNoStoreForTesting(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("healthz: want 200, got %d", rr.Code)
	}
	if rr.Body.String() != "ok" {
		t.Fatalf("healthz body: want ok, got %q", rr.Body.String())
	}
}

func TestConfig_Validate_RequiresHMACSecret(t *testing.T) {
	err := Config{InternalSharedSecret: "x"}.Validate()
	if err == nil || !strings.Contains(err.Error(), "CapTokenHMACSecret") {
		t.Fatalf("want HMAC required, got %v", err)
	}
}

func TestConfig_Validate_RequiresInternalSecret(t *testing.T) {
	err := Config{CapTokenHMACSecret: []byte("k")}.Validate()
	if err == nil || !strings.Contains(err.Error(), "InternalSharedSecret") {
		t.Fatalf("want internal-secret required, got %v", err)
	}
}

func TestNewServer_RejectsZeroConfig(t *testing.T) {
	_, err := NewServer(Config{}, nil)
	if err == nil {
		t.Fatal("NewServer should reject zero-value Config")
	}
}

// TestRoutes_AgentxRegisterPath confirms that the chi router mounts the
// register handler at /agentx/environment/{env_id}/register. Without auth
// headers it should fall through to 401 — the point is that it does not
// return 404 (which would mean the route is not mounted).
// The legacy /cloud/executor/* and /cloud/environment/* paths are gone
// (hard cut in C2).
func TestRoutes_AgentxRegisterPath(t *testing.T) {
	srv, err := newServerNoStoreForTesting(Config{
		CapTokenHMACSecret:        []byte("k"),
		InternalSharedSecret:      "is",
		AgentserverInternalSecret: "tic",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	path := "/agentx/environment/exe_x/register"
	req := httptest.NewRequest(http.MethodPost, path, nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code == http.StatusNotFound {
		t.Errorf("%s: route not mounted (404)", path)
		return
	}
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("%s: got %d body=%s (want 401)", path, rr.Code, rr.Body.String())
	}
}
