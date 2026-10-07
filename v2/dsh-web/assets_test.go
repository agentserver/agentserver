package dshweb

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestHandlerAllowsDSHRuntimeAssets(t *testing.T) {
	requireBuiltBundle(t)
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
	if !strings.Contains(response.Body.String(), "<title>agentserver</title>") || !strings.Contains(response.Body.String(), `"previewNotice":false`) {
		t.Fatal("deployment title or preview-notice setting is missing")
	}
	for _, disabled := range []string{"@deepseek-ai/dsh-client-ui-directory-picker-native", "@deepseek-ai/dsh-client-hmr"} {
		if strings.Contains(response.Body.String(), disabled) {
			t.Fatalf("production DSH graph still contains disabled entry %q", disabled)
		}
	}
}

func TestOfficialBootGraphResourcesAreServedWithoutFrontendURLRewrites(t *testing.T) {
	requireBuiltBundle(t)
	match := regexp.MustCompile(`globalThis\["__DSH_BOOT__"\]\s*=\s*(\{[^<]*\})</script>`).FindSubmatch(bundle.index)
	if len(match) != 2 {
		t.Fatal("official boot graph missing")
	}
	var graph struct {
		Entries []struct {
			URL string `json:"url"`
		} `json:"entries"`
		Batches []struct {
			URL string `json:"url"`
		} `json:"batches"`
	}
	if err := json.Unmarshal(match[1], &graph); err != nil {
		t.Fatal(err)
	}
	urls := []string{}
	for _, entry := range graph.Entries {
		urls = append(urls, entry.URL)
	}
	for _, batch := range graph.Batches {
		urls = append(urls, batch.URL)
	}
	for uri := range bundle.pluginResources {
		urls = append(urls, uri)
	}
	if len(urls) < 2 {
		t.Fatal("empty plugin graph")
	}
	for _, uri := range urls {
		response := httptest.NewRecorder()
		Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://dsh.example/"+strings.TrimPrefix(uri, "/"), nil))
		if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/javascript") || !strings.Contains(response.Body.String(), "__ModuleLoader__") {
			t.Fatalf("plugin %s returned %d/%s", uri, response.Code, response.Header().Get("Content-Type"))
		}
	}
	for _, uri := range []string{"/plugins/??missing/client.js&rev=unknown", "/plugins/", "/plugins/??../../index.html&rev=unknown"} {
		response := httptest.NewRecorder()
		Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://dsh.example"+uri, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("unknown plugin %s returned %d", uri, response.Code)
		}
	}
}

func requireBuiltBundle(t *testing.T) {
	t.Helper()
	if bundle.err == nil {
		return
	}
	if os.Getenv("AGENTSERVER_REQUIRE_DSH_ASSETS") == "1" {
		t.Fatal(bundle.err)
	}
	t.Skip(bundle.err)
}

func TestUnbuiltFrontendCannotStartOrServeProduction(t *testing.T) {
	previous := bundle
	bundle = staticBundle{err: errors.New("DSH frontend not built")}
	t.Cleanup(func() { bundle = previous })
	if _, err := HandlerForOAuthOrigin("https://auth.example"); err == nil {
		t.Fatal("unbuilt frontend accepted at startup")
	}
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://dsh.example/", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unbuilt frontend status=%d", response.Code)
	}
}
