package devfixtures

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/coreserver"
)

func TestAuthorizationFixturesCompleteCodePKCEAndMintIntrospectableToken(t *testing.T) {
	runtime, _ := newTestRuntime(t)
	browserVerifier := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, 32))
	browserChallengeRaw := sha256.Sum256([]byte(browserVerifier))
	browserChallenge := base64.RawURLEncoding.EncodeToString(browserChallengeRaw[:])
	browserState := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x12}, 32))
	browserNonce := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x13}, 32))

	authorize := url.Values{
		"response_type": {"code"}, "client_id": {BrowserOAuthClientID},
		"redirect_uri": {runtime.bundle.document.Hydra.BrowserRedirectURI},
		"scope":        {strings.Join(browserAuthorizationScopes(), " ")}, "audience": {BrowserTokenAudience},
		"state": {browserState}, "nonce": {browserNonce},
		"code_challenge": {browserChallenge}, "code_challenge_method": {"S256"},
	}
	begin := callAuthorizationFixture(t, runtime, http.MethodGet, "/oauth2/auth?"+authorize.Encode(), nil, nil)
	loginRedirect := requireFixtureRedirect(t, begin)
	loginChallenge := requireSingleQuery(t, loginRedirect, "login_challenge")

	loginRequest := callAuthorizationFixture(
		t, runtime, http.MethodGet,
		"/admin/oauth2/auth/requests/login?"+(url.Values{"login_challenge": {loginChallenge}}).Encode(),
		nil, map[string]string{"Accept": "application/json"},
	)
	if loginRequest.Code != http.StatusOK {
		t.Fatalf("get login request = %d %s", loginRequest.Code, loginRequest.Body.String())
	}
	var loginDocument struct {
		Challenge string `json:"challenge"`
		Client    struct {
			ClientID string `json:"client_id"`
		} `json:"client"`
		RequestedScope    []string `json:"requested_scope"`
		RequestedAudience []string `json:"requested_access_token_audience"`
	}
	decodeFixtureResponse(t, loginRequest, &loginDocument)
	if loginDocument.Challenge != loginChallenge || loginDocument.Client.ClientID != BrowserOAuthClientID ||
		!sameFixtureSet(loginDocument.RequestedScope, browserAuthorizationScopes()) ||
		!sameFixtureSet(loginDocument.RequestedAudience, []string{BrowserTokenAudience}) {
		t.Fatalf("login request authority = %+v", loginDocument)
	}

	idpVerifier := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x21}, 32))
	idpChallengeRaw := sha256.Sum256([]byte(idpVerifier))
	idpState := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x22}, 32))
	idpNonce := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x23}, 32))
	idpAuthorize := url.Values{
		"response_type": {"code"}, "client_id": {ExternalOIDCClientID},
		"redirect_uri": {runtime.bundle.document.Hydra.ExternalOIDC.RedirectURI}, "scope": {"openid"},
		"state": {idpState}, "nonce": {idpNonce},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(idpChallengeRaw[:])}, "code_challenge_method": {"S256"},
	}
	idpBegin := callAuthorizationFixture(t, runtime, http.MethodGet, "/idp/authorize?"+idpAuthorize.Encode(), nil, nil)
	idpCallback := requireFixtureRedirect(t, idpBegin)
	if requireSingleQuery(t, idpCallback, "state") != idpState ||
		requireSingleQuery(t, idpCallback, "iss") != runtime.bundle.document.Hydra.ExternalOIDC.Issuer {
		t.Fatalf("external callback = %s", idpCallback)
	}
	idpCode := requireSingleQuery(t, idpCallback, "code")
	idpTokenForm := url.Values{
		"grant_type": {"authorization_code"}, "code": {idpCode},
		"redirect_uri": {runtime.bundle.document.Hydra.ExternalOIDC.RedirectURI}, "code_verifier": {idpVerifier},
	}
	idpToken := callAuthorizationFixture(
		t, runtime, http.MethodPost, "/idp/token", strings.NewReader(idpTokenForm.Encode()),
		map[string]string{
			"Content-Type":  "application/x-www-form-urlencoded",
			"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(ExternalOIDCClientID+":"+string(runtime.bundle.externalOIDCClientSecret))),
		},
	)
	if idpToken.Code != http.StatusOK {
		t.Fatalf("external token = %d %s", idpToken.Code, idpToken.Body.String())
	}
	var idpTokenDocument struct {
		IDToken string `json:"id_token"`
	}
	decodeFixtureResponse(t, idpToken, &idpTokenDocument)
	verifyFixtureIDToken(t, runtime, idpTokenDocument.IDToken, idpNonce)

	acceptLoginBody := []byte(`{"subject":"10000000-0000-4000-8000-000000000001","remember":false,"remember_for":0,"context":{}}`)
	acceptedLogin := callAuthorizationFixture(
		t, runtime, http.MethodPut,
		"/admin/oauth2/auth/requests/login/accept?"+(url.Values{"login_challenge": {loginChallenge}}).Encode(),
		bytes.NewReader(acceptLoginBody), map[string]string{"Accept": "application/json", "Content-Type": "application/json"},
	)
	var acceptedLoginDocument struct {
		RedirectTo string `json:"redirect_to"`
	}
	decodeFixtureResponse(t, acceptedLogin, &acceptedLoginDocument)
	loginContinuation := fixturePathAndQuery(t, acceptedLoginDocument.RedirectTo)
	continuedLogin := callAuthorizationFixture(t, runtime, http.MethodGet, loginContinuation, nil, nil)
	consentRedirect := requireFixtureRedirect(t, continuedLogin)
	consentChallenge := requireSingleQuery(t, consentRedirect, "consent_challenge")
	loginReplay := callAuthorizationFixture(t, runtime, http.MethodGet, loginContinuation, nil, nil)
	if loginReplay.Code != http.StatusBadRequest {
		t.Fatalf("login verifier replay = %d %s", loginReplay.Code, loginReplay.Body.String())
	}

	consentRequest := callAuthorizationFixture(
		t, runtime, http.MethodGet,
		"/admin/oauth2/auth/requests/consent?"+(url.Values{"consent_challenge": {consentChallenge}}).Encode(),
		nil, map[string]string{"Accept": "application/json"},
	)
	if consentRequest.Code != http.StatusOK {
		t.Fatalf("get consent request = %d %s", consentRequest.Code, consentRequest.Body.String())
	}
	acceptConsentBody := []byte(`{"grant_scope":["openid","runs:write","executors:write","llm-gateways:write"],"grant_access_token_audience":["agentserver-api"],"remember":false,"remember_for":0,"session":{"access_token":{},"id_token":{}}}`)
	acceptedConsent := callAuthorizationFixture(
		t, runtime, http.MethodPut,
		"/admin/oauth2/auth/requests/consent/accept?"+(url.Values{"consent_challenge": {consentChallenge}}).Encode(),
		bytes.NewReader(acceptConsentBody), map[string]string{"Accept": "application/json", "Content-Type": "application/json"},
	)
	var acceptedConsentDocument struct {
		RedirectTo string `json:"redirect_to"`
	}
	decodeFixtureResponse(t, acceptedConsent, &acceptedConsentDocument)
	continuedConsent := callAuthorizationFixture(t, runtime, http.MethodGet, fixturePathAndQuery(t, acceptedConsentDocument.RedirectTo), nil, nil)
	browserCallback := requireFixtureRedirect(t, continuedConsent)
	if requireSingleQuery(t, browserCallback, "state") != browserState {
		t.Fatalf("browser callback state = %s", browserCallback)
	}
	browserCode := requireSingleQuery(t, browserCallback, "code")

	browserTokenForm := url.Values{
		"grant_type": {"authorization_code"}, "code": {browserCode},
		"redirect_uri": {runtime.bundle.document.Hydra.BrowserRedirectURI},
		"client_id":    {BrowserOAuthClientID}, "code_verifier": {browserVerifier},
	}
	browserToken := callAuthorizationFixture(
		t, runtime, http.MethodPost, "/oauth2/token", strings.NewReader(browserTokenForm.Encode()),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
	)
	if browserToken.Code != http.StatusOK {
		t.Fatalf("browser token = %d %s", browserToken.Code, browserToken.Body.String())
	}
	var browserTokenDocument struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Scope       string `json:"scope"`
	}
	decodeFixtureResponse(t, browserToken, &browserTokenDocument)
	if browserTokenDocument.AccessToken == "" || browserTokenDocument.TokenType != "Bearer" ||
		!sameFixtureSet(strings.Fields(browserTokenDocument.Scope), browserAuthorizationScopes()) {
		t.Fatalf("browser token authority = %+v", browserTokenDocument)
	}
	introspection := httptest.NewRecorder()
	runtime.serveHydra(introspection, introspectionRequest(t, runtime, browserTokenDocument.AccessToken))
	if introspection.Code != http.StatusOK || !strings.Contains(introspection.Body.String(), `"active":true`) ||
		!strings.Contains(introspection.Body.String(), `"sub":"10000000-0000-4000-8000-000000000001"`) {
		t.Fatalf("dynamic token introspection = %d %s", introspection.Code, introspection.Body.String())
	}
	replay := callAuthorizationFixture(
		t, runtime, http.MethodPost, "/oauth2/token", strings.NewReader(browserTokenForm.Encode()),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
	)
	if replay.Code != http.StatusBadRequest || !strings.Contains(replay.Body.String(), "invalid_grant") {
		t.Fatalf("authorization code replay = %d %s", replay.Code, replay.Body.String())
	}
}

func TestExternalOIDCFixtureWorksWithProductionDiscoveryAndVerifier(t *testing.T) {
	runtime, _ := newTestRuntime(t)
	runtime.now = time.Now
	client := &http.Client{Transport: authorizationFixtureRoundTripper{runtime: runtime}}
	provider, err := coreserver.NewDiscoveredExternalOIDCProvider(
		t.Context(), runtime.bundle.document.Hydra.ExternalOIDC.Issuer,
		ExternalOIDCClientID, string(runtime.bundle.externalOIDCClientSecret),
		runtime.bundle.document.Hydra.ExternalOIDC.RedirectURI, client, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x31}, 32))
	state := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x32}, 32))
	nonce := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x33}, 32))
	authorizationURL, err := provider.AuthorizationURL(state, nonce, verifier)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "https" || parsed.Host != "127.0.0.1:17444" || parsed.Path != "/auth/idp/authorize" {
		t.Fatalf("discovered browser authorization URL = %s", authorizationURL)
	}
	response := callAuthorizationFixture(t, runtime, http.MethodGet, "/idp/authorize?"+parsed.RawQuery, nil, nil)
	callback := requireFixtureRedirect(t, response)
	code := requireSingleQuery(t, callback, "code")
	identity, err := provider.Exchange(t.Context(), code, verifier, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Issuer != runtime.bundle.document.Hydra.ExternalOIDC.Issuer || identity.Subject != ExternalOIDCSubject {
		t.Fatalf("verified external identity = %+v", identity)
	}
	if _, err := provider.Exchange(t.Context(), code, verifier, nonce); err == nil {
		t.Fatal("external OIDC authorization code replay succeeded")
	}
}

func TestHydraAdminFixtureWorksWithProductionClient(t *testing.T) {
	runtime, _ := newTestRuntime(t)
	runtime.now = time.Now
	transport := authorizationFixtureRoundTripper{runtime: runtime}
	admin, err := coreserver.NewHydraAdminClient(
		"http://"+runtime.bundle.hydraEndpoint.Host, &http.Client{Transport: transport}, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x41}, 32))
	challengeRaw := sha256.Sum256([]byte(verifier))
	state := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	nonce := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x43}, 32))
	authorize := url.Values{
		"response_type": {"code"}, "client_id": {BrowserOAuthClientID},
		"redirect_uri": {runtime.bundle.document.Hydra.BrowserRedirectURI},
		"scope":        {strings.Join(browserAuthorizationScopes(), " ")}, "audience": {BrowserTokenAudience},
		"state": {state}, "nonce": {nonce},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challengeRaw[:])}, "code_challenge_method": {"S256"},
	}
	begin := callAuthorizationFixture(t, runtime, http.MethodGet, "/oauth2/auth?"+authorize.Encode(), nil, nil)
	loginChallenge := requireSingleQuery(t, requireFixtureRedirect(t, begin), "login_challenge")
	login, err := admin.GetLoginRequest(t.Context(), loginChallenge)
	if err != nil {
		t.Fatal(err)
	}
	if login.Challenge != loginChallenge || login.Client.ClientID != BrowserOAuthClientID || login.Skip {
		t.Fatalf("Hydra Admin login request = %+v", login)
	}
	loginRedirect, err := admin.AcceptLoginRequest(t.Context(), loginChallenge, "10000000-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	continued := callAuthorizationFixture(t, runtime, http.MethodGet, fixturePathAndQuery(t, loginRedirect.RedirectTo), nil, nil)
	consentChallenge := requireSingleQuery(t, requireFixtureRedirect(t, continued), "consent_challenge")
	consent, err := admin.GetConsentRequest(t.Context(), consentChallenge)
	if err != nil {
		t.Fatal(err)
	}
	if consent.Subject != "10000000-0000-4000-8000-000000000001" || consent.LoginChallenge != loginChallenge {
		t.Fatalf("Hydra Admin consent request = %+v", consent)
	}
	consentRedirect, err := admin.AcceptConsentRequest(
		t.Context(), consentChallenge, browserAuthorizationScopes(), []string{BrowserTokenAudience},
	)
	if err != nil || consentRedirect.RedirectTo == "" {
		t.Fatalf("Hydra Admin consent acceptance = %+v, %v", consentRedirect, err)
	}
}

func callAuthorizationFixture(
	t *testing.T,
	runtime *fixtureRuntime,
	method, path string,
	body io.Reader,
	headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "http://"+runtime.bundle.hydraEndpoint.Host+path, body)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	runtime.serveHydra(response, request)
	return response
}

func requireFixtureRedirect(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	if response.Code != http.StatusFound {
		t.Fatalf("fixture redirect = %d %s", response.Code, response.Body.String())
	}
	location := response.Header().Get("Location")
	if location == "" {
		t.Fatal("fixture redirect omitted Location")
	}
	return location
}

func requireSingleQuery(t *testing.T, raw, name string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	values := parsed.Query()[name]
	if len(values) != 1 || values[0] == "" {
		t.Fatalf("redirect %s has invalid %s", raw, name)
	}
	return values[0]
}

func fixturePathAndQuery(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Path == "" {
		t.Fatalf("invalid fixture continuation %q", raw)
	}
	if parsed.RawQuery == "" {
		return parsed.Path
	}
	return parsed.Path + "?" + parsed.RawQuery
}

func decodeFixtureResponse(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("fixture JSON response = %d %s", response.Code, response.Body.String())
	}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(destination); err != nil {
		t.Fatal(err)
	}
}

func verifyFixtureIDToken(t *testing.T, runtime *fixtureRuntime, raw, nonce string) {
	t.Helper()
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatalf("ID token compact parts = %d", len(parts))
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(runtime.externalOIDCPublicKey(), []byte(parts[0]+"."+parts[1]), signature) {
		t.Fatal("ID token signature is invalid")
	}
	claimsRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Issuer  string `json:"iss"`
		Subject string `json:"sub"`
		Nonce   string `json:"nonce"`
	}
	if err := json.Unmarshal(claimsRaw, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Issuer != runtime.bundle.document.Hydra.ExternalOIDC.Issuer ||
		claims.Subject != ExternalOIDCSubject || claims.Nonce != nonce {
		t.Fatalf("ID token claims = %+v", claims)
	}
}

type authorizationFixtureRoundTripper struct {
	runtime *fixtureRuntime
}

func (transport authorizationFixtureRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if transport.runtime == nil {
		return nil, context.Canceled
	}
	response := httptest.NewRecorder()
	transport.runtime.serveHydra(response, request)
	return response.Result(), nil
}
