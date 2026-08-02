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
			body = `{"challenge":"login-1","skip":false,"subject":"","client":{"client_id":"agentserver-web"},"requested_scope":["openid","runs:write","executors:write"],"requested_access_token_audience":["agentserver-api"]}`
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
			body = `{"challenge":"consent-1","skip":false,"subject":"` + loginBridgeTestUserID + `","client":{"client_id":"agentserver-web"},"requested_scope":["openid","runs:write","executors:write"],"requested_access_token_audience":["agentserver-api"],"login_challenge":"login-1","login_session_id":"session-1"}`
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

func TestHydraAdminClientUsesExactPrivateKeyJWTExecutorClientContract(t *testing.T) {
	publicKeyX, publicKeyY, thumbprint := enrollmentTestOAuthAuthority()
	document := executorOAuthClientDocument(
		"agentserver-executor-71000000-0000-4000-8000-000000000003",
		"71000000-0000-4000-8000-000000000003",
		publicKeyX, publicKeyY, thumbprint,
	)
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		status := http.StatusOK
		provisioningResponse := false
		switch requests {
		case 1:
			if request.Method != http.MethodPost || request.URL.Path != "/admin/clients" || request.URL.RawQuery != "" || request.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("Hydra create request = %s %s", request.Method, request.URL.String())
			}
			var actual HydraExecutorOAuthClient
			if err := json.NewDecoder(request.Body).Decode(&actual); err != nil || !equalHydraExecutorClient(actual, document) {
				t.Fatalf("Hydra create document = %+v, %v", actual, err)
			}
			status = http.StatusCreated
			provisioningResponse = true
		case 2:
			if request.Method != http.MethodGet || request.URL.Path != "/admin/clients/"+document.ClientID || request.URL.RawQuery != "" || request.Body != nil {
				t.Fatalf("Hydra get request = %s %s body=%v", request.Method, request.URL.String(), request.Body)
			}
		default:
			t.Fatalf("unexpected Hydra client request %d", requests)
		}
		raw := hydraExecutorClientResponseJSON(t, document, provisioningResponse, nil)
		return &http.Response{
			StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(string(raw))), Request: request,
		}, nil
	})}
	client, err := NewHydraAdminClient("https://hydra-admin.example", httpClient, false)
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.CreateExecutorOAuthClient(t.Context(), document)
	if err != nil || !equalHydraExecutorClient(created, document) {
		t.Fatalf("created Hydra executor client = %+v, %v", created, err)
	}
	read, err := client.GetExecutorOAuthClient(t.Context(), document.ClientID)
	if err != nil || !equalHydraExecutorClient(read, document) || requests != 2 {
		t.Fatalf("read Hydra executor client = %+v, %v, requests=%d", read, err, requests)
	}
}

func TestHydraAdminClientRejectsExecutorClientCapabilitySmuggling(t *testing.T) {
	publicKeyX, publicKeyY, thumbprint := enrollmentTestOAuthAuthority()
	document := executorOAuthClientDocument(
		"agentserver-executor-71000000-0000-4000-8000-000000000003",
		"71000000-0000-4000-8000-000000000003", publicKeyX, publicKeyY, thumbprint,
	)
	for name, mutation := range map[string]map[string]any{
		"unknown future field": {"future_grant_surface": true},
		"redirect URI":         {"redirect_uris": []string{"https://attacker.example/callback"}},
		"CORS origin":          {"allowed_cors_origins": []string{"https://attacker.example"}},
		"metadata":             {"metadata": map[string]any{"privileged": true}},
		"alternate lifespan":   {"authorization_code_grant_access_token_lifespan": "1h"},
	} {
		t.Run(name, func(t *testing.T) {
			client := hydraExecutorTestClient(t, http.StatusOK, hydraExecutorClientResponseJSON(t, document, false, mutation))
			if _, err := client.GetExecutorOAuthClient(t.Context(), document.ClientID); err == nil {
				t.Fatal("Hydra executor client capability smuggling was accepted")
			}
		})
	}

	t.Run("private JWK material", func(t *testing.T) {
		raw := hydraExecutorClientResponseJSON(t, document, false, nil)
		var response map[string]any
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		jwks := response["jwks"].(map[string]any)
		keys := jwks["keys"].([]any)
		keys[0].(map[string]any)["d"] = "private-key-material"
		mutated, _ := json.Marshal(response)
		client := hydraExecutorTestClient(t, http.StatusOK, mutated)
		if _, err := client.GetExecutorOAuthClient(t.Context(), document.ClientID); err == nil {
			t.Fatal("Hydra executor client private JWK was accepted")
		}
	})

	t.Run("read response provisioning secret", func(t *testing.T) {
		client := hydraExecutorTestClient(t, http.StatusOK, hydraExecutorClientResponseJSON(t, document, true, nil))
		if _, err := client.GetExecutorOAuthClient(t.Context(), document.ClientID); err == nil {
			t.Fatal("Hydra client read leaked provisioning credentials without failing closed")
		}
	})
}

func hydraExecutorClientResponseJSON(t *testing.T, document HydraExecutorOAuthClient, provisioning bool, mutation map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	response["redirect_uris"] = []string{}
	response["allowed_cors_origins"] = []string{}
	response["contacts"] = []string{}
	response["request_uris"] = []string{}
	response["post_logout_redirect_uris"] = []string{}
	response["client_secret_expires_at"] = float64(0)
	response["owner"] = ""
	response["policy_uri"] = ""
	response["tos_uri"] = ""
	response["client_uri"] = ""
	response["logo_uri"] = ""
	response["subject_type"] = "public"
	response["userinfo_signed_response_alg"] = "none"
	response["skip_consent"] = false
	response["skip_logout_consent"] = false
	response["metadata"] = map[string]any{}
	response["created_at"] = "2026-08-02T00:00:00Z"
	response["updated_at"] = "2026-08-02T00:00:00Z"
	if provisioning {
		response["client_secret"] = "discarded-generated-client-secret"
		response["registration_access_token"] = "ory_at_discarded-registration-token"
		response["registration_client_uri"] = "https://hydra.example/oauth2/register/" + document.ClientID
	}
	for key, value := range mutation {
		response[key] = value
	}
	result, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func hydraExecutorTestClient(t *testing.T, status int, body []byte) *HydraAdminClient {
	t.Helper()
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(string(body))), Request: request,
		}, nil
	})}
	client, err := NewHydraAdminClient("https://hydra-admin.example", httpClient, false)
	if err != nil {
		t.Fatal(err)
	}
	return client
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
