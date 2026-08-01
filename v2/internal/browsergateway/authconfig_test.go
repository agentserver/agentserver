package browsergateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserAuthorizationConfigIsPublicBoundedAndSecretFree(t *testing.T) {
	handler, err := NewBrowserAuthorizationConfigHandler("agentserver-web", "agentserver-api", []string{"openid", "runs:write"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://browser.example/auth/config", nil)
	request.Header.Set("Cookie", "__Host-agentserver-oidc=stale-browser-binding")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" ||
		response.Header().Get("Content-Type") != "application/json" ||
		response.Body.String() != `{"version":1,"authorizationEndpoint":"/oauth2/auth","tokenEndpoint":"/oauth2/token","redirectPath":"/","clientId":"agentserver-web","scopes":["openid","runs:write"],"audience":"agentserver-api"}`+"\n" {
		t.Fatalf("authorization config = %d %q headers=%v", response.Code, response.Body.String(), response.Header())
	}
	if strings.Contains(response.Body.String(), "secret") {
		t.Fatal("authorization config exposed a secret field")
	}
}

func TestBrowserAuthorizationConfigRejectsDuplicateScopesAndBrowserAuthority(t *testing.T) {
	if _, err := NewBrowserAuthorizationConfigHandler("agentserver-web", "agentserver-api", []string{"openid", "openid"}); err == nil {
		t.Fatal("duplicate OAuth scopes were accepted")
	}
	handler, err := NewBrowserAuthorizationConfigHandler("agentserver-web", "agentserver-api", []string{"openid"})
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
