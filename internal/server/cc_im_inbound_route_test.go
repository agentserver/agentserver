package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestServer creates a minimal *Server suitable for route-registration tests.
// It does NOT open a database connection (DB is nil); the misconfiguration
// safeguard in Router() guards against nil DB, so this is safe.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	return &Server{}
}

// TestServer_CcInboundRouteRegistered verifies that the cc inbound route is
// registered (i.e. NOT 404) when CC_APP_GATEWAY_REST_URL is set.
func TestServer_CcInboundRouteRegistered(t *testing.T) {
	t.Setenv("CC_APP_GATEWAY_REST_URL", "http://cc-app-gateway:8087")
	srv := newTestServer(t)
	defer srv.Close()

	// The route is registered; an unauthenticated POST should return 401 (auth
	// middleware fires), NOT 404 (route not found).
	req := httptest.NewRequest("POST", "/api/internal/imbridge/cc/turn", bytes.NewReader([]byte(`{}`)))
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)
	if rr.Code == http.StatusNotFound {
		t.Errorf("cc inbound route should be registered when CC_APP_GATEWAY_REST_URL is set; got 404")
	}
}

// TestServer_CcInboundRouteSkippedWhenURLEmpty verifies that the cc inbound
// route is absent (404) when CC_APP_GATEWAY_REST_URL is empty.
func TestServer_CcInboundRouteSkippedWhenURLEmpty(t *testing.T) {
	t.Setenv("CC_APP_GATEWAY_REST_URL", "")
	srv := newTestServer(t)
	defer srv.Close()

	req := httptest.NewRequest("POST", "/api/internal/imbridge/cc/turn", bytes.NewReader([]byte(`{}`)))
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("cc inbound route should NOT be registered when CC_APP_GATEWAY_REST_URL is empty; got %d", rr.Code)
	}
}
