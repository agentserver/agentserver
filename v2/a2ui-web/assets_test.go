package a2uiweb

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestBrowserBundleServesOnlyRegisteredProductRoutesAndAssets(t *testing.T) {
	handler := Handler()
	for _, route := range []string{"/", "/index.html", "/workspaces/9271bfe5-68a4-484b-a2d3-e9f450a42d0c"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://browser.example"+route, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-agentserver-browser-web="v2"`) || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("GET %s = %d headers=%v body=%q", route, response.Code, response.Header(), response.Body.String())
		}
	}
	assetPath := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindStringSubmatch(string(bundle.index))[1]
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "https://browser.example"+assetPath, nil))
	if asset.Code != http.StatusOK || asset.Body.Len() == 0 || asset.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || asset.Header().Get("ETag") == "" {
		t.Fatalf("GET asset %s = %d headers=%v", assetPath, asset.Code, asset.Header())
	}
	for _, route := range []string{"/reference/app.js", "/v2/unknown", "/assets/missing.js", "/workspaces/not-a-uuid", "/workspaces/9271bfe5-68a4-484b-a2d3-e9f450a42d0c/extra"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://browser.example"+route, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", route, response.Code)
		}
	}
}

func TestBrowserBundleConnectionAuthoritiesMethodsAndSecurity(t *testing.T) {
	handler, err := HandlerForConnectionOrigins("https://browser-gateway.byted.bps.dev", "https://auth-sg.byted.bps.dev")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "https://browser.byted.bps.dev/", nil))
	if response.Code != http.StatusOK || response.Body.Len() != 0 || response.Header().Get("Content-Security-Policy") != contentSecurityPolicy+" https://browser-gateway.byted.bps.dev https://auth-sg.byted.bps.dev" || response.Header().Get("Cross-Origin-Opener-Policy") != "same-origin-allow-popups" {
		t.Fatalf("Browser HEAD = %d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	write := httptest.NewRecorder()
	handler.ServeHTTP(write, httptest.NewRequest(http.MethodPost, "https://browser.byted.bps.dev/", strings.NewReader("ignored")))
	if write.Code != http.StatusMethodNotAllowed || write.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("Browser POST = %d headers=%v", write.Code, write.Header())
	}
	for _, origins := range [][]string{{}, {"http://browser.example"}, {"https://browser.example/path"}, {"https://browser.example", "https://browser.example"}} {
		if _, err := HandlerForConnectionOrigins(origins...); err == nil {
			t.Fatalf("invalid origins %v were accepted", origins)
		}
	}
}
