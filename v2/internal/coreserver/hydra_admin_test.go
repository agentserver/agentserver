package coreserver

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHydraAdminClientUsesExactChallengeContracts(t *testing.T) {
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		body := ""
		switch request.URL.Path {
		case "/admin/oauth2/auth/requests/login":
			if request.Method != http.MethodGet || request.URL.Query().Get("login_challenge") != "login-1" || len(request.URL.Query()) != 1 {
				t.Fatalf("login request = %s %s", request.Method, request.URL.String())
			}
			body = `{"challenge":"login-1","skip":false,"subject":"","client":{"client_id":"agentserver-web"},"requested_scope":["openid","runs:write"],"requested_access_token_audience":["agentserver-api"]}`
		case "/admin/oauth2/auth/requests/login/accept":
			if request.Method != http.MethodPut || request.URL.Query().Get("login_challenge") != "login-1" {
				t.Fatalf("login accept = %s %s", request.Method, request.URL.String())
			}
			var decoded struct {
				Subject     string `json:"subject"`
				Remember    bool   `json:"remember"`
				RememberFor int64  `json:"remember_for"`
			}
			if err := json.NewDecoder(request.Body).Decode(&decoded); err != nil || decoded.Subject != loginBridgeTestUserID || decoded.Remember || decoded.RememberFor != 0 {
				t.Fatalf("login accept body = %+v, %v", decoded, err)
			}
			body = `{"redirect_to":"https://browser.example/oauth2/auth?login_verifier=ok"}`
		case "/admin/oauth2/auth/requests/consent":
			if request.Method != http.MethodGet || request.URL.Query().Get("consent_challenge") != "consent-1" || len(request.URL.Query()) != 1 {
				t.Fatalf("consent request = %s %s", request.Method, request.URL.String())
			}
			body = `{"challenge":"consent-1","skip":false,"subject":"` + loginBridgeTestUserID + `","client":{"client_id":"agentserver-web"},"requested_scope":["openid","runs:write"],"requested_access_token_audience":["agentserver-api"],"login_challenge":"login-1","login_session_id":"session-1"}`
		case "/admin/oauth2/auth/requests/consent/accept":
			if request.Method != http.MethodPut || request.URL.Query().Get("consent_challenge") != "consent-1" {
				t.Fatalf("consent accept = %s %s", request.Method, request.URL.String())
			}
			var decoded struct {
				Scopes   []string `json:"grant_scope"`
				Audience []string `json:"grant_access_token_audience"`
				Remember bool     `json:"remember"`
			}
			if err := json.NewDecoder(request.Body).Decode(&decoded); err != nil || !sameUniqueTextSet(decoded.Scopes, defaultBrowserOAuthScopes) || !sameUniqueTextSet(decoded.Audience, defaultBrowserAudience) || decoded.Remember {
				t.Fatalf("consent accept body = %+v, %v", decoded, err)
			}
			body = `{"redirect_to":"https://browser.example/oauth2/auth?consent_verifier=ok"}`
		default:
			t.Fatalf("unexpected Hydra Admin path %q", request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})}

	client, err := NewHydraAdminClient("https://hydra-admin.example", httpClient, false)
	if err != nil {
		t.Fatal(err)
	}
	login, err := client.GetLoginRequest(t.Context(), "login-1")
	if err != nil || login.Client.ClientID != "agentserver-web" {
		t.Fatalf("login = %+v, %v", login, err)
	}
	if _, err := client.AcceptLoginRequest(t.Context(), "login-1", loginBridgeTestUserID); err != nil {
		t.Fatal(err)
	}
	consent, err := client.GetConsentRequest(t.Context(), "consent-1")
	if err != nil || consent.Subject != loginBridgeTestUserID {
		t.Fatalf("consent = %+v, %v", consent, err)
	}
	if _, err := client.AcceptConsentRequest(t.Context(), "consent-1", defaultBrowserOAuthScopes, defaultBrowserAudience); err != nil {
		t.Fatal(err)
	}
	if requests != 4 {
		t.Fatalf("Hydra Admin request count = %d", requests)
	}
}

func TestHydraAdminClientRejectsRedirectsDuplicateJSONAndCleartextByDefault(t *testing.T) {
	redirectRequests := 0
	redirectHTTPClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		redirectRequests++
		return &http.Response{
			StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": []string{"https://token-sink.invalid/leak"}},
			Body: io.NopCloser(strings.NewReader("redirect")), Request: request,
		}, nil
	})}
	if _, err := NewHydraAdminClient("http://hydra-admin.example", redirectHTTPClient, false); err == nil {
		t.Fatal("cleartext Hydra Admin origin was accepted without opt-in")
	}
	client, err := NewHydraAdminClient("https://hydra-admin.example", redirectHTTPClient, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetLoginRequest(t.Context(), "login-1"); err == nil || redirectRequests != 1 {
		t.Fatalf("redirect result error=%v requests=%d", err, redirectRequests)
	}

	duplicateHTTPClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"challenge":"login-1","challenge":"login-2","client":{"client_id":"agentserver-web"}}`)), Request: request,
		}, nil
	})}
	duplicateClient, err := NewHydraAdminClient("https://hydra-admin.example", duplicateHTTPClient, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := duplicateClient.GetLoginRequest(t.Context(), "login-1"); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate Hydra JSON error = %v", err)
	}
}
