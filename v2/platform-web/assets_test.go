package platformweb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlatformAssetsAreClosedAndHardened(t *testing.T) {
	handler := Handler()
	request := httptest.NewRequest(http.MethodGet, "https://agent.example/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-agentserver-platform-web="v2"`) ||
		response.Header().Get("Content-Security-Policy") == "" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("platform index = %d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	auth := httptest.NewRecorder()
	handler.ServeHTTP(auth, httptest.NewRequest(http.MethodGet, "https://agent.example/platform/auth.js", nil))
	if auth.Code != http.StatusOK || !strings.Contains(auth.Body.String(), "readAuthorizationCallback") {
		t.Fatalf("platform authorization asset = %d body=%q", auth.Code, auth.Body.String())
	}
	resources := httptest.NewRecorder()
	handler.ServeHTTP(resources, httptest.NewRequest(http.MethodGet, "https://agent.example/platform/resources.js", nil))
	if resources.Code != http.StatusOK || !strings.Contains(resources.Body.String(), "validateWorkspaceList") {
		t.Fatalf("platform resource asset = %d body=%q", resources.Code, resources.Body.String())
	}
	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "https://agent.example/missing", nil))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown platform asset = %d", unknown.Code)
	}
}

func TestPlatformAssetsAllowOneExactOAuthAuthority(t *testing.T) {
	handler, err := HandlerForOAuthOrigin("https://auth-sg.byted.bps.dev")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://agent.byted.bps.dev/", nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Security-Policy") !=
		contentSecurityPolicy+" https://auth-sg.byted.bps.dev" {
		t.Fatalf("platform OAuth CSP = %d headers=%v", response.Code, response.Header())
	}
	for _, invalid := range []string{"", "http://auth-sg.byted.bps.dev", "https://auth-sg.byted.bps.dev/path"} {
		if _, err := HandlerForOAuthOrigin(invalid); err == nil {
			t.Fatalf("invalid OAuth origin %q was accepted", invalid)
		}
	}
}
