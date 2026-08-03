package platformgateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestAuthorizationConfigPublishesExactPlatformAuthority(t *testing.T) {
	handler, err := NewAuthorizationConfigHandler(
		corecontract.PlatformOAuthClientID, corecontract.PlatformOAuthAudience, corecontract.PlatformOAuthScopes(),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://agent.example/auth/config", nil))
	var document AuthorizationConfig
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &document) != nil ||
		document.ClientID != corecontract.PlatformOAuthClientID || document.Audience != corecontract.PlatformOAuthAudience ||
		len(document.Scopes) != len(corecontract.PlatformOAuthScopes()) || document.AuthorizationEndpoint != "/oauth2/auth" ||
		document.TokenEndpoint != "/oauth2/token" {
		t.Fatalf("platform authorization config = %d %+v body=%q", response.Code, document, response.Body.String())
	}
}

func TestAuthorizationConfigRejectsDuplicateScopes(t *testing.T) {
	if _, err := NewAuthorizationConfigHandler("client", "audience", []string{"openid", "openid"}); err == nil {
		t.Fatal("duplicate Platform scopes were accepted")
	}
}
