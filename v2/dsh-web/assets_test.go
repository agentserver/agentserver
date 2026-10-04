package dshweb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerAllowsDSHRuntimeAssets(t *testing.T) {
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://dsh.example/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	policy := response.Header().Get("Content-Security-Policy")
	for _, directive := range []string{"font-src 'self' data:", "script-src 'self' 'unsafe-inline' 'unsafe-eval'"} {
		if !strings.Contains(policy, directive) {
			t.Fatalf("CSP %q missing %q", policy, directive)
		}
	}
	if !strings.Contains(response.Body.String(), "__DSH_AUTH_READY__") {
		t.Fatal("DSH authentication bootstrap is missing")
	}
}
