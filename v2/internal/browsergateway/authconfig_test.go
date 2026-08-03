package browsergateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestBrowserAuthorizationConfigIsPublicBoundedAndSecretFree(t *testing.T) {
	handler, err := NewBrowserAuthorizationConfigHandler(corecontract.BrowserOAuthClientID, corecontract.BrowserOAuthAudience, corecontract.BrowserOAuthScopes(), "https://browser-gateway.byted.bps.dev")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://browser.example/auth/config", nil)
	request.Header.Set("Cookie", "__Host-agentserver-oidc=stale-browser-binding")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" ||
		response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("authorization config = %d %q headers=%v", response.Code, response.Body.String(), response.Header())
	}
	var document BrowserAuthorizationConfig
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil || document.ClientID != corecontract.BrowserOAuthClientID ||
		document.Audience != corecontract.BrowserOAuthAudience || len(document.Scopes) != len(corecontract.BrowserOAuthScopes()) {
		t.Fatalf("authorization config document = %+v, %v", document, err)
	}
	if strings.Contains(response.Body.String(), "secret") {
		t.Fatal("authorization config exposed a secret field")
	}
}

func TestBrowserAuthorizationConfigRejectsDuplicateScopesAndBrowserAuthority(t *testing.T) {
	if _, err := NewBrowserAuthorizationConfigHandler(corecontract.BrowserOAuthClientID, corecontract.BrowserOAuthAudience, []string{"openid", "openid"}, ""); err == nil {
		t.Fatal("duplicate OAuth scopes were accepted")
	}
	handler, err := NewBrowserAuthorizationConfigHandler(corecontract.BrowserOAuthClientID, corecontract.BrowserOAuthAudience, []string{"openid"}, "")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://browser.example/auth/config", nil)
	request.Header.Set("Authorization", "Bearer must-not-be-needed")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("authenticated config request = %d", response.Code)
	}
}

func TestBrowserAuthorizationConfigAcceptsOneExactExternalOAuthAuthority(t *testing.T) {
	handler, err := NewBrowserAuthorizationConfigHandlerWithEndpoints(
		corecontract.BrowserOAuthClientID,
		corecontract.BrowserOAuthAudience,
		corecontract.BrowserOAuthScopes(),
		"https://browser-gateway.byted.bps.dev",
		"https://agent.byted.bps.dev/oauth2/auth",
		"https://agent.byted.bps.dev/oauth2/token",
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://browser.byted.bps.dev/auth/config", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var document BrowserAuthorizationConfig
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil ||
		document.AuthorizationEndpoint != "https://agent.byted.bps.dev/oauth2/auth" ||
		document.TokenEndpoint != "https://agent.byted.bps.dev/oauth2/token" {
		t.Fatalf("external authorization config = %+v, %v", document, err)
	}
	for _, endpoints := range [][2]string{
		{"https://agent.byted.bps.dev/oauth2/auth", "https://other.example/oauth2/token"},
		{"https://agent.byted.bps.dev/oauth2/auth", "/oauth2/token"},
		{"https://agent.byted.bps.dev/oauth2/auth?unsafe=1", "https://agent.byted.bps.dev/oauth2/token"},
	} {
		if _, err := NewBrowserAuthorizationConfigHandlerWithEndpoints(
			corecontract.BrowserOAuthClientID, corecontract.BrowserOAuthAudience, corecontract.BrowserOAuthScopes(), "",
			endpoints[0], endpoints[1],
		); err == nil {
			t.Fatalf("invalid OAuth endpoints were accepted: %q", endpoints)
		}
	}
}
