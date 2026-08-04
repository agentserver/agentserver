package platformweb

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestPlatformBundleServesClosedProductRoutesAndHashedAssets(t *testing.T) {
	handler := Handler()
	for _, route := range []string{
		"/", "/index.html", "/workspaces", "/workspaces/9271bfe5-68a4-484b-a2d3-e9f450a42d0c",
		"/workspaces/9271bfe5-68a4-484b-a2d3-e9f450a42d0c/overview",
		"/workspaces/9271bfe5-68a4-484b-a2d3-e9f450a42d0c/members",
		"/workspaces/9271bfe5-68a4-484b-a2d3-e9f450a42d0c/executors",
		"/workspaces/9271bfe5-68a4-484b-a2d3-e9f450a42d0c/gateways",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://agent.example"+route, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-agentserver-platform-web="v2"`) || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("GET %s = %d headers=%v body=%q", route, response.Code, response.Header(), response.Body.String())
		}
	}

	assetPath := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindStringSubmatch(string(bundle.index))[1]
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "https://agent.example"+assetPath, nil))
	if asset.Code != http.StatusOK || asset.Body.Len() == 0 || asset.Header().Get("ETag") == "" || asset.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("GET asset %s = %d headers=%v", assetPath, asset.Code, asset.Header())
	}

	for _, route := range []string{"/v2/not-an-api", "/platform/app.js", "/assets/missing.js", "/workspaces/not-a-uuid", "/workspaces/9271bfe5-68a4-484b-a2d3-e9f450a42d0c/unknown"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://agent.example"+route, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", route, response.Code)
		}
	}
}

func TestPlatformBundleSecurityMethodsAndOAuthOrigin(t *testing.T) {
	handler, err := HandlerForOAuthOrigin("https://auth-sg.byted.bps.dev")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "https://agent.byted.bps.dev/", nil))
	if response.Code != http.StatusOK || response.Body.Len() != 0 || response.Header().Get("Content-Security-Policy") != contentSecurityPolicy+" https://auth-sg.byted.bps.dev" || response.Header().Get("Cross-Origin-Opener-Policy") != "same-origin-allow-popups" {
		t.Fatalf("Platform HEAD = %d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	write := httptest.NewRecorder()
	handler.ServeHTTP(write, httptest.NewRequest(http.MethodPost, "https://agent.byted.bps.dev/", strings.NewReader("ignored")))
	if write.Code != http.StatusMethodNotAllowed || write.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("Platform POST = %d headers=%v", write.Code, write.Header())
	}
	for _, invalid := range []string{"", "http://auth-sg.byted.bps.dev", "https://auth-sg.byted.bps.dev/path"} {
		if _, err := HandlerForOAuthOrigin(invalid); err == nil {
			t.Fatalf("invalid OAuth origin %q was accepted", invalid)
		}
	}
}
