package main

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestServePlatformGatewayRequiresConfigurationBeforeListening(t *testing.T) {
	err := servePlatformGateway(t.Context(), func(string) string { return "" }, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), platformListenAddressEnvironment+" is required") {
		t.Fatalf("servePlatformGateway() error = %v", err)
	}
}

func TestValidatePlatformOAuthAuthority(t *testing.T) {
	expected := corecontract.PlatformOAuthScopes()
	scopes, err := validatePlatformOAuthAuthority(
		corecontract.PlatformOAuthClientID, corecontract.PlatformOAuthAudience, strings.Join(expected, ","),
	)
	if err != nil || !slices.Equal(scopes, expected) {
		t.Fatalf("platform OAuth authority = %v, %v", scopes, err)
	}
	if _, err := validatePlatformOAuthAuthority(corecontract.BrowserOAuthClientID, corecontract.PlatformOAuthAudience, strings.Join(expected, ",")); err == nil {
		t.Fatal("Browser client was accepted as Platform authority")
	}
}

func TestPlatformPublicBoundaryAllowsOnlyBrowserTokenCORS(t *testing.T) {
	calls := 0
	next := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { calls++; response.WriteHeader(http.StatusOK) })
	handler := platformPublicBoundary(next, "agent.byted.bps.dev", "https://agent.byted.bps.dev", "https://browser.byted.bps.dev")

	preflight := httptest.NewRequest(http.MethodOptions, "http://agent.byted.bps.dev/oauth2/token", nil)
	preflight.Host = "agent.byted.bps.dev"
	preflight.Header.Set("Origin", "https://browser.byted.bps.dev")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "content-type")
	preflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent || preflightResponse.Header().Get("Access-Control-Allow-Origin") != "https://browser.byted.bps.dev" ||
		preflightResponse.Header().Get("Access-Control-Allow-Credentials") != "" || calls != 0 {
		t.Fatalf("token preflight = %d headers=%v calls=%d", preflightResponse.Code, preflightResponse.Header(), calls)
	}

	token := httptest.NewRequest(http.MethodPost, "http://agent.byted.bps.dev/oauth2/token", nil)
	token.Host = "agent.byted.bps.dev"
	token.Header.Set("Origin", "https://browser.byted.bps.dev")
	tokenResponse := httptest.NewRecorder()
	handler.ServeHTTP(tokenResponse, token)
	if tokenResponse.Code != http.StatusOK || tokenResponse.Header().Get("Access-Control-Allow-Origin") != "https://browser.byted.bps.dev" || calls != 1 {
		t.Fatalf("browser token request = %d headers=%v calls=%d", tokenResponse.Code, tokenResponse.Header(), calls)
	}

	for _, path := range []string{"/oauth2/auth", "/v2/workspaces"} {
		request := httptest.NewRequest(http.MethodGet, "http://agent.byted.bps.dev"+path, nil)
		request.Host = "agent.byted.bps.dev"
		request.Header.Set("Origin", "https://browser.byted.bps.dev")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden || calls != 1 {
			t.Fatalf("browser cross-origin %s = %d calls=%d", path, response.Code, calls)
		}
	}

	wrongHost := httptest.NewRequest(http.MethodGet, "http://other.example/", nil)
	wrongHost.Host = "other.example"
	wrongHostResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongHostResponse, wrongHost)
	if wrongHostResponse.Code != http.StatusNotFound || calls != 1 {
		t.Fatalf("wrong host = %d calls=%d", wrongHostResponse.Code, calls)
	}
}

func TestPlatformGatewayRoutesKeepPlatformAndAuthSurfaces(t *testing.T) {
	called := map[string]int{}
	h := func(name string) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			called[name]++
			response.WriteHeader(http.StatusOK)
		})
	}
	readiness := &platformReadiness{}
	readiness.ready.Store(true)
	handler, err := platformGatewayRoutes(
		h("executors"), h("llm"), h("auth"), h("hydra"), h("config"), h("callback"), h("web"), readiness,
		"https://agent.byted.bps.dev", "https://browser.byted.bps.dev",
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ method, path, call string }{
		{http.MethodPost, "/v2/workspaces/40000000-0000-4000-8000-000000000004/executors", "executors"},
		{http.MethodGet, "/v2/workspaces/40000000-0000-4000-8000-000000000004/llm-gateways", "llm"},
		{http.MethodGet, "/auth/hydra/login?login_challenge=x", "auth"},
		{http.MethodGet, "/oauth2/auth?client_id=x", "hydra"},
		{http.MethodGet, "/auth/config", "config"},
		{http.MethodGet, corecontract.LLMGatewayOIDCCallbackPath, "callback"},
		{http.MethodGet, "/", "web"},
	} {
		request := httptest.NewRequest(test.method, "http://agent.byted.bps.dev"+test.path, nil)
		request.Host = "agent.byted.bps.dev"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || called[test.call] != 1 {
			t.Fatalf("%s %s = %d calls=%v", test.method, test.path, response.Code, called)
		}
	}
}
