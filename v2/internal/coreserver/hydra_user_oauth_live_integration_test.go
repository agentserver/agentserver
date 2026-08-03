package coreserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const (
	hydraLiveBrowserWorkspaceID = "20000000-0000-4000-8000-000000000002"
	hydraLiveBrowserSubjectID   = "10000000-0000-4000-8000-000000000001"
	hydraLiveBrowserRedirectURI = "http://127.0.0.1:5556/callback"
)

// TestHydraV262UserResourceAuthorityLive establishes the external facts the
// Platform/Browser authorization design relies on. It deliberately exercises
// the exact Hydra process selected by deployment instead of a fixture:
//
//   - an OAuth resource indicator survives in login and consent request_url;
//   - consent session.access_token.agentserver is returned as introspection
//     ext.agentserver without widening scope or audience; and
//   - server-side consent-session revocation makes the token inactive on the
//     very next introspection.
func TestHydraV262UserResourceAuthorityLive(t *testing.T) {
	if testing.Short() {
		t.Skip("live Hydra conformance is disabled in short mode")
	}
	if os.Getenv(hydraLiveTestEnvironment) != "1" {
		t.Skip(hydraLiveTestEnvironment + "=1 is required")
	}
	adminOrigin := requiredHydraLiveOrigin(t, hydraLiveAdminOriginEnvironment)
	publicOrigin := requiredHydraLiveOrigin(t, hydraLivePublicOriginEnvironment)
	issuer := publicOrigin + "/"
	publicClient := newHydraLiveBrowserClient(t)
	adminClient := &http.Client{Timeout: 10 * time.Second}
	admin, err := NewHydraAdminClient(adminOrigin, adminClient, true)
	if err != nil {
		t.Fatal(err)
	}

	clientUUID, err := newCoreUUID()
	if err != nil {
		t.Fatal(err)
	}
	clientID := "agentserver-browser-live-" + clientUUID
	requestedScopes := []string{
		corecontract.OAuthOpenIDScope,
		corecontract.BrowserOAuthSessionsReadScope,
		corecontract.BrowserOAuthRunsReadScope,
		corecontract.BrowserOAuthRunsCreateScope,
	}
	createHydraLivePublicClient(t, adminClient, adminOrigin, clientID, requestedScopes)
	t.Cleanup(func() { deleteHydraLiveClient(t, adminClient, adminOrigin, clientID) })

	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challengeDigest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeDigest[:])
	resource := corecontract.UserOAuthWorkspaceURNPrefix + hydraLiveBrowserWorkspaceID
	authorizationURL := publicOrigin + "/oauth2/auth?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {hydraLiveBrowserRedirectURI},
		"scope":                 {strings.Join(requestedScopes, " ")},
		"audience":              {corecontract.BrowserOAuthAudience},
		"state":                 {"agentserver-hydra-live-state"},
		"nonce":                 {"agentserver-hydra-live-nonce"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"resource":              {resource},
	}.Encode()

	loginLocation := hydraLiveRedirectLocation(t, publicClient, authorizationURL)
	loginChallenge := hydraLiveRedirectChallenge(t, loginLocation, "login_challenge")
	login, err := admin.GetLoginRequest(t.Context(), loginChallenge)
	if err != nil {
		t.Fatalf("read Hydra user login request %q from %q: %v", loginChallenge, loginLocation, err)
	}
	assertHydraLiveUserRequest(t, login.Client.ClientID, login.RequestedScope,
		login.RequestedAccessTokenAudience, login.RequestURL, clientID, requestedScopes, resource)

	// The production Admin adapter deliberately rejects non-HTTPS redirect_to
	// values. This conformance process is bound to loopback HTTP, so accept the
	// request through a test-only helper without weakening the production
	// redirect contract.
	loginRedirect := acceptHydraLiveLoginRequest(t, adminClient, adminOrigin, loginChallenge, hydraLiveBrowserSubjectID)
	assertHydraV262ContinuationQuery(t, authorizationURL, loginRedirect.RedirectTo, hydraLoginVerifierQuery)
	consentLocation := hydraLiveRedirectLocation(t, publicClient, loginRedirect.RedirectTo)
	consentChallenge := hydraLiveRedirectChallenge(t, consentLocation, "consent_challenge")
	consent, err := admin.GetConsentRequest(t.Context(), consentChallenge)
	if err != nil {
		t.Fatalf("read Hydra user consent request: %v", err)
	}
	// Hydra 26.2.0 exposes its internal flow ID as consent.login_challenge,
	// not the encrypted challenge sent to the login UI. It is correlation
	// metadata only; the consent challenge remains the acceptance authority.
	if consent.Subject != hydraLiveBrowserSubjectID || consent.LoginChallenge == "" || consent.LoginSessionID == "" {
		t.Fatalf("Hydra consent login authority drifted: %+v", consent)
	}
	assertHydraLiveUserRequest(t, consent.Client.ClientID, consent.RequestedScope,
		consent.RequestedAccessTokenAudience, consent.RequestURL, clientID, requestedScopes, resource)

	permissions := []string{
		corecontract.BrowserOAuthRunsCreateScope,
		corecontract.BrowserOAuthRunsReadScope,
		corecontract.BrowserOAuthSessionsReadScope,
	}
	authority := corecontract.UserOAuthAuthority{
		Version: corecontract.UserOAuthAuthorityVersion, Authority: corecontract.UserOAuthBrowserAuthority,
		GlobalPermissions: []string{},
		WorkspaceGrants: []corecontract.UserOAuthWorkspaceGrant{{
			WorkspaceID: hydraLiveBrowserWorkspaceID, Generation: 7, Permissions: permissions,
		}},
	}
	consentRedirect := acceptHydraLiveConsentRequest(t, adminClient, adminOrigin, consentChallenge, HydraConsentGrant{
		Scope: requestedScopes, Audience: []string{corecontract.BrowserOAuthAudience}, Authority: authority,
	})
	assertHydraV262ContinuationQuery(t, authorizationURL, consentRedirect.RedirectTo, hydraConsentVerifierQuery)
	callbackLocation := hydraLiveRedirectLocation(t, publicClient, consentRedirect.RedirectTo)
	callback, err := url.Parse(callbackLocation)
	if err != nil || callback.Scheme+"://"+callback.Host+callback.Path != hydraLiveBrowserRedirectURI ||
		callback.Query().Get("state") != "agentserver-hydra-live-state" || callback.Query().Get("code") == "" {
		t.Fatalf("Hydra authorization callback is invalid: %q", callbackLocation)
	}

	token := exchangeHydraLiveAuthorizationCode(t, publicClient, publicOrigin+"/oauth2/token", clientID,
		callback.Query().Get("code"), verifier)
	if token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") ||
		token.Scope != strings.Join(requestedScopes, " ") {
		t.Fatalf("Hydra user token response drifted: type=%q scope=%q has_token=%t",
			token.TokenType, token.Scope, token.AccessToken != "")
	}

	introspector, err := NewHydraUserIntrospector(adminOrigin+"/admin/oauth2/introspect", adminClient, true)
	if err != nil {
		t.Fatal(err)
	}
	introspection, err := introspector.IntrospectUserToken(t.Context(), token.AccessToken)
	if err != nil {
		t.Fatalf("introspect live Hydra user token: %v", err)
	}
	if !introspection.Active || introspection.Subject != hydraLiveBrowserSubjectID || introspection.ClientID != clientID ||
		introspection.Issuer != issuer || len(introspection.Audience) != 1 ||
		introspection.Audience[0] != corecontract.BrowserOAuthAudience ||
		introspection.Scope != strings.Join(requestedScopes, " ") ||
		!reflect.DeepEqual(introspection.Authority, authority) {
		t.Fatalf("Hydra introspection lost user resource authority: %+v", introspection)
	}

	authorizer, err := NewIntrospectedUserAuthorizer(IntrospectedUserAuthorizerConfig{
		Introspector: introspector, ExpectedIssuer: issuer, ExpectedClientID: clientID,
		ExpectedAudience: corecontract.BrowserOAuthAudience, ExpectedAuthority: corecontract.UserOAuthBrowserAuthority,
		AllowedScopes: corecontract.BrowserOAuthScopes(), ActionPermissions: corecontract.BrowserOAuthActionPermissions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://browser-gateway.example/v2/workspaces/"+hydraLiveBrowserWorkspaceID+"/sessions/50000000-0000-4000-8000-000000000005/runs", nil)
	request.SetPathValue("workspaceId", hydraLiveBrowserWorkspaceID)
	request.Header.Set("Authorization", "Bearer "+token.AccessToken)
	if actor, err := authorizer.AuthorizeUser(request, "runs.create"); err != nil || actor != hydraLiveBrowserSubjectID {
		t.Fatalf("Core rejected live Hydra Browser authority: actor=%q err=%v", actor, err)
	}
	request.SetPathValue("workspaceId", "20000000-0000-4000-8000-000000000003")
	if _, err := authorizer.AuthorizeUser(request, "runs.create"); !errors.Is(err, ErrInvalidUserAccessToken) {
		t.Fatalf("live Browser token crossed its workspace resource: %v", err)
	}

	revokeHydraLiveConsentSessions(t, adminClient, adminOrigin, hydraLiveBrowserSubjectID, clientID)
	revoked, err := introspector.IntrospectUserToken(t.Context(), token.AccessToken)
	if err != nil {
		t.Fatalf("introspect server-revoked Hydra user token: %v", err)
	}
	if revoked.Active {
		t.Fatal("Hydra consent-session revocation did not immediately deactivate the user token")
	}
	request.SetPathValue("workspaceId", hydraLiveBrowserWorkspaceID)
	if _, err := authorizer.AuthorizeUser(request, "runs.create"); !errors.Is(err, ErrInvalidUserAccessToken) {
		t.Fatalf("Core accepted server-revoked Hydra user token: %v", err)
	}
}

func assertHydraV262ContinuationQuery(t *testing.T, authorizationURL, continuationURL, verifierQuery string) {
	t.Helper()
	authorization, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	continuation, err := url.Parse(continuationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := continuation.Query()
	verifiers := query[verifierQuery]
	if len(verifiers) != 1 || verifiers[0] == "" {
		t.Fatalf("Hydra 26.2 continuation is missing its singular %s", verifierQuery)
	}
	expected := authorization.Query()
	if _, exists := expected[hydraLoginVerifierQuery]; exists {
		t.Fatal("authorization request unexpectedly contains a login verifier")
	}
	if _, exists := expected[hydraConsentVerifierQuery]; exists {
		t.Fatal("authorization request unexpectedly contains a consent verifier")
	}
	expected.Set(verifierQuery, verifiers[0])
	if continuation.Scheme != authorization.Scheme || continuation.Host != authorization.Host ||
		continuation.Path != authorization.Path || continuation.RawPath != "" || continuation.Fragment != "" ||
		continuation.RawQuery != expected.Encode() {
		t.Fatalf("Hydra 26.2 continuation did not preserve the exact authorization query plus %s", verifierQuery)
	}
}

func newHydraLiveBrowserClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Jar:     jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func createHydraLivePublicClient(t *testing.T, client *http.Client, adminOrigin, clientID string, scopes []string) {
	t.Helper()
	document := map[string]any{
		"client_id": clientID, "client_name": "AgentServer Browser live conformance",
		"grant_types": []string{"authorization_code"}, "response_types": []string{"code"},
		"redirect_uris": []string{hydraLiveBrowserRedirectURI}, "scope": strings.Join(scopes, " "),
		"audience": []string{corecontract.BrowserOAuthAudience}, "token_endpoint_auth_method": "none",
		"access_token_strategy": "opaque", "subject_type": "public",
	}
	hydraLiveJSONRequest(t, client, http.MethodPost, adminOrigin+"/admin/clients", document, http.StatusCreated)
}

func acceptHydraLiveLoginRequest(
	t *testing.T,
	client *http.Client,
	adminOrigin, challenge, subject string,
) HydraRedirect {
	t.Helper()
	body := struct {
		Subject     string         `json:"subject"`
		Remember    bool           `json:"remember"`
		RememberFor int64          `json:"remember_for"`
		Context     map[string]any `json:"context"`
	}{Subject: subject, Remember: false, RememberFor: 0, Context: map[string]any{}}
	endpoint := adminOrigin + "/admin/oauth2/auth/requests/login/accept?" + url.Values{
		"login_challenge": {challenge},
	}.Encode()
	return hydraLiveAcceptRequest(t, client, endpoint, body)
}

func acceptHydraLiveConsentRequest(
	t *testing.T,
	client *http.Client,
	adminOrigin, challenge string,
	grant HydraConsentGrant,
) HydraRedirect {
	t.Helper()
	body := struct {
		GrantScope               []string `json:"grant_scope"`
		GrantAccessTokenAudience []string `json:"grant_access_token_audience"`
		Remember                 bool     `json:"remember"`
		RememberFor              int64    `json:"remember_for"`
		Session                  struct {
			AccessToken map[string]any `json:"access_token"`
			IDToken     map[string]any `json:"id_token"`
		} `json:"session"`
	}{
		GrantScope:               append([]string(nil), grant.Scope...),
		GrantAccessTokenAudience: append([]string(nil), grant.Audience...),
		Remember:                 false,
		RememberFor:              0,
	}
	body.Session.AccessToken = map[string]any{"agentserver": grant.Authority}
	body.Session.IDToken = map[string]any{}
	endpoint := adminOrigin + "/admin/oauth2/auth/requests/consent/accept?" + url.Values{
		"consent_challenge": {challenge},
	}.Encode()
	return hydraLiveAcceptRequest(t, client, endpoint, body)
}

func hydraLiveAcceptRequest(t *testing.T, client *http.Client, endpoint string, body any) HydraRedirect {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, endpoint, strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("accept live Hydra authorization request: %v", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, hydraLiveMaximumBodyBytes+1))
	if err != nil || int64(len(responseBody)) > hydraLiveMaximumBodyBytes {
		t.Fatal("Hydra authorization acceptance response is unreadable or oversized")
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Hydra authorization acceptance status=%d body=%s", response.StatusCode, responseBody)
	}
	var redirect HydraRedirect
	if err := json.Unmarshal(responseBody, &redirect); err != nil {
		t.Fatalf("decode Hydra authorization acceptance response: %v", err)
	}
	parsed, err := url.Parse(redirect.RedirectTo)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		t.Fatalf("Hydra authorization acceptance returned an invalid local redirect: %q", redirect.RedirectTo)
	}
	return redirect
}

func assertHydraLiveUserRequest(
	t *testing.T,
	actualClientID string,
	actualScopes, actualAudience []string,
	requestURL, expectedClientID string,
	expectedScopes []string,
	expectedResource string,
) {
	t.Helper()
	parsed, err := url.Parse(requestURL)
	resources := []string(nil)
	if err == nil {
		resources = parsed.Query()["resource"]
	}
	if actualClientID != expectedClientID || !slices.Equal(actualScopes, expectedScopes) ||
		!reflect.DeepEqual(actualAudience, []string{corecontract.BrowserOAuthAudience}) ||
		!reflect.DeepEqual(resources, []string{expectedResource}) {
		t.Fatalf("Hydra request lost client/scope/audience/resource: client=%q scopes=%v audience=%v request_url=%q",
			actualClientID, actualScopes, actualAudience, requestURL)
	}
}

func hydraLiveRedirectLocation(t *testing.T, client *http.Client, endpoint string) string {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("follow Hydra authorization continuation: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, hydraLiveMaximumBodyBytes+1))
	if err != nil || int64(len(body)) > hydraLiveMaximumBodyBytes {
		t.Fatal("Hydra authorization continuation response is unreadable or oversized")
	}
	location := response.Header.Get("Location")
	if (response.StatusCode != http.StatusFound && response.StatusCode != http.StatusSeeOther) || location == "" {
		t.Fatalf("Hydra authorization continuation status=%d location=%q body=%s", response.StatusCode, location, body)
	}
	return location
}

func hydraLiveRedirectChallenge(t *testing.T, location, name string) string {
	t.Helper()
	parsed, err := url.Parse(location)
	if err != nil || len(parsed.Query()[name]) != 1 || parsed.Query().Get(name) == "" {
		t.Fatalf("Hydra redirect has no exact %s: %q", name, location)
	}
	return parsed.Query().Get(name)
}

func exchangeHydraLiveAuthorizationCode(
	t *testing.T,
	client *http.Client,
	endpoint, clientID, code, verifier string,
) hydraLiveTokenResponse {
	t.Helper()
	form := url.Values{
		"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code},
		"redirect_uri": {hydraLiveBrowserRedirectURI}, "code_verifier": {verifier},
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("exchange live Hydra authorization code: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, hydraLiveMaximumBodyBytes+1))
	if err != nil || int64(len(body)) > hydraLiveMaximumBodyBytes {
		t.Fatal("Hydra authorization-code response is unreadable or oversized")
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Hydra authorization-code exchange status=%d body=%s", response.StatusCode, body)
	}
	var token hydraLiveTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		t.Fatalf("decode Hydra authorization-code response: %v", err)
	}
	return token
}

func revokeHydraLiveConsentSessions(t *testing.T, client *http.Client, adminOrigin, subject, clientID string) {
	t.Helper()
	endpoint := adminOrigin + "/admin/oauth2/auth/sessions/consent?" + url.Values{
		"subject": {subject}, "client": {clientID},
	}.Encode()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("revoke live Hydra consent sessions: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, hydraLiveMaximumBodyBytes+1))
	if err != nil || int64(len(body)) > hydraLiveMaximumBodyBytes {
		t.Fatal("Hydra consent revocation response is unreadable or oversized")
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("Hydra consent revocation status=%d body=%s", response.StatusCode, body)
	}
}

func hydraLiveJSONRequest(t *testing.T, client *http.Client, method, endpoint string, document any, wantStatus int) {
	t.Helper()
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, endpoint, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("execute Hydra JSON request: %v", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, hydraLiveMaximumBodyBytes+1))
	if err != nil || int64(len(responseBody)) > hydraLiveMaximumBodyBytes {
		t.Fatal("Hydra JSON response is unreadable or oversized")
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("Hydra JSON request status=%d, want=%d body=%s", response.StatusCode, wantStatus, responseBody)
	}
}
