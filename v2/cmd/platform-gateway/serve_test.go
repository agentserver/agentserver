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

func TestPlatformPublicBoundaryRejectsCrossOriginRequests(t *testing.T) {
	calls := 0
	next := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { calls++; response.WriteHeader(http.StatusOK) })
	handler := platformPublicBoundary(next, "agent.byted.bps.dev", "https://agent.byted.bps.dev")

	crossOrigin := httptest.NewRequest(http.MethodPost, "http://agent.byted.bps.dev/v2/workspaces", nil)
	crossOrigin.Host = "agent.byted.bps.dev"
	crossOrigin.Header.Set("Origin", "https://browser.byted.bps.dev")
	crossOriginResponse := httptest.NewRecorder()
	handler.ServeHTTP(crossOriginResponse, crossOrigin)
	if crossOriginResponse.Code != http.StatusForbidden || calls != 0 {
		t.Fatalf("cross-origin request = %d calls=%d", crossOriginResponse.Code, calls)
	}

	sameOrigin := httptest.NewRequest(http.MethodGet, "http://agent.byted.bps.dev/", nil)
	sameOrigin.Host = "agent.byted.bps.dev"
	sameOrigin.Header.Set("Origin", "https://agent.byted.bps.dev")
	sameOriginResponse := httptest.NewRecorder()
	handler.ServeHTTP(sameOriginResponse, sameOrigin)
	if sameOriginResponse.Code != http.StatusOK || calls != 1 {
		t.Fatalf("same-origin request = %d calls=%d", sameOriginResponse.Code, calls)
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
		h("resources"), h("executors"), h("llm"), h("auth"), h("config"), h("callback"), h("web"), readiness,
		"https://agent.byted.bps.dev", "https://auth-sg.byted.bps.dev",
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ method, path, call string }{
		{http.MethodGet, "/v2/workspaces", "resources"},
		{http.MethodPost, "/v2/workspaces/40000000-0000-4000-8000-000000000004/executors", "executors"},
		{http.MethodGet, "/v2/workspaces/40000000-0000-4000-8000-000000000004/llm-gateways", "llm"},
		{http.MethodPatch, "/v2/workspaces/40000000-0000-4000-8000-000000000004/llm-gateways/40000000-0000-4000-8000-000000000005", "llm"},
		{http.MethodGet, "/auth/config", "config"},
		{http.MethodGet, corecontract.LLMGatewayOIDCCallbackPath, "callback"},
		{http.MethodGet, "/", "web"},
	} {
		before := called[test.call]
		request := httptest.NewRequest(test.method, "http://agent.byted.bps.dev"+test.path, nil)
		request.Host = "agent.byted.bps.dev"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || called[test.call] != before+1 {
			t.Fatalf("%s %s = %d calls=%v", test.method, test.path, response.Code, called)
		}
	}
	for _, path := range []string{
		"/auth/hydra/login?login_challenge=x",
		"/auth/hydra/consent?consent_challenge=x",
		"/auth/oidc/callback?code=x&state=y",
	} {
		request := httptest.NewRequest(http.MethodGet, "http://auth-sg.byted.bps.dev"+path, nil)
		request.Host = "auth-sg.byted.bps.dev"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s on auth host = %d", path, response.Code)
		}
	}
	if called["auth"] != 3 {
		t.Fatalf("auth host calls = %v", called)
	}
	request := httptest.NewRequest(http.MethodGet, "http://agent.byted.bps.dev/auth/hydra/login?login_challenge=x", nil)
	request.Host = "agent.byted.bps.dev"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || called["auth"] != 3 {
		t.Fatalf("Platform host exposed Hydra login: status=%d calls=%v", response.Code, called)
	}
}
