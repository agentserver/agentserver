package devfixtures

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const (
	maximumAuthorizationTransactions = 4096
	maximumAuthorizationBodyBytes    = int64(64 * 1024)
	authorizationTransactionTTL      = 5 * time.Minute
	externalOIDCSigningKeyID         = "agentserver-dev-idp-ed25519"

	hydraLoginPending        = "pending"
	hydraLoginAccepted       = "accepted"
	hydraLoginConsentPending = "consent_pending"
	hydraLoginCodeIssued     = "code_issued"
	hydraLoginRejected       = "rejected"

	hydraConsentPending  = "pending"
	hydraConsentAccepted = "accepted"
	hydraConsentConsumed = "consumed"
	hydraConsentRejected = "rejected"
)

type hydraLoginFixture struct {
	clientID       string
	redirectURI    string
	state          string
	nonce          string
	codeChallenge  string
	scopes         []string
	audience       []string
	requestURL     string
	subject        string
	loginSessionID string
	status         string
	expiresAt      time.Time
}

type hydraConsentFixture struct {
	loginChallenge string
	clientID       string
	redirectURI    string
	state          string
	codeChallenge  string
	scopes         []string
	audience       []string
	requestURL     string
	grantedScopes  []string
	authority      corecontract.UserOAuthAuthority
	subject        string
	loginSessionID string
	status         string
	expiresAt      time.Time
}

type idpCodeFixture struct {
	clientID      string
	redirectURI   string
	codeChallenge string
	nonce         string
	expiresAt     time.Time
}

type hydraCodeFixture struct {
	clientID      string
	redirectURI   string
	codeChallenge string
	scopes        []string
	audience      []string
	authority     corecontract.UserOAuthAuthority
	subject       string
	expiresAt     time.Time
}

type accessTokenFixture struct {
	subject   string
	clientID  string
	scopes    []string
	audience  []string
	authority corecontract.UserOAuthAuthority
	expiresAt time.Time
}

func (runtime *fixtureRuntime) serveHydraAuthorization(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || !emptyFixtureRequest(request) || request.Header.Get("Authorization") != "" {
		writeFixtureError(writer, http.StatusBadRequest, "development authorization request rejected")
		return
	}
	query, err := parseFixtureQuery(request.URL.RawQuery)
	if err != nil {
		writeFixtureError(writer, http.StatusBadRequest, "development authorization request rejected")
		return
	}
	if _, found := query["login_verifier"]; found {
		runtime.continueHydraLogin(writer, query)
		return
	}
	if _, found := query["consent_verifier"]; found {
		runtime.continueHydraConsent(writer, query)
		return
	}
	runtime.beginHydraAuthorization(writer, request, query)
}

func (runtime *fixtureRuntime) beginHydraAuthorization(writer http.ResponseWriter, request *http.Request, query url.Values) {
	values, err := exactFixtureParameters(query, []string{
		"response_type", "client_id", "redirect_uri", "scope", "state", "nonce",
		"code_challenge", "code_challenge_method", "audience", "resource",
	})
	if err != nil || values["response_type"] != "code" ||
		values["client_id"] != runtime.bundle.document.Hydra.BrowserClientID ||
		values["redirect_uri"] != runtime.bundle.document.Hydra.BrowserRedirectURI ||
		values["code_challenge_method"] != "S256" ||
		!validFixtureSecret(values["state"]) || !validFixtureSecret(values["nonce"]) ||
		!validPKCEChallenge(values["code_challenge"]) ||
		!sameFixtureSet(strings.Fields(values["scope"]), browserAuthorizationScopes()) ||
		values["audience"] != BrowserTokenAudience ||
		values["resource"] != corecontract.UserOAuthWorkspaceURNPrefix+runtime.bundle.document.Authority.WorkspaceID {
		writeFixtureError(writer, http.StatusBadRequest, "development authorization request rejected")
		return
	}
	now, ok := runtime.authorizationNow(writer)
	if !ok {
		return
	}
	challenge, err := newFixtureOpaque("hydra-login")
	if err != nil {
		writeFixtureError(writer, http.StatusServiceUnavailable, "development authorization unavailable")
		return
	}
	runtime.authMu.Lock()
	runtime.initializeAuthorizationStateLocked()
	runtime.pruneAuthorizationStateLocked(now)
	if len(runtime.hydraLogins) >= maximumAuthorizationTransactions {
		runtime.authMu.Unlock()
		writeFixtureError(writer, http.StatusServiceUnavailable, "development authorization capacity reached")
		return
	}
	runtime.hydraLogins[challenge] = hydraLoginFixture{
		clientID: values["client_id"], redirectURI: values["redirect_uri"], state: values["state"],
		nonce: values["nonce"], codeChallenge: values["code_challenge"],
		scopes: browserAuthorizationScopes(), audience: []string{BrowserTokenAudience},
		requestURL: "http://" + request.Host + request.URL.RequestURI(),
		status:     hydraLoginPending, expiresAt: now.Add(authorizationTransactionTTL),
	}
	runtime.authMu.Unlock()
	redirect, err := fixtureURL(runtime.bundle.document.Hydra.LoginURL, url.Values{"login_challenge": {challenge}})
	if err != nil {
		writeFixtureError(writer, http.StatusInternalServerError, "development authorization unavailable")
		return
	}
	writeFixtureRedirect(writer, redirect)
}

func (runtime *fixtureRuntime) continueHydraLogin(writer http.ResponseWriter, query url.Values) {
	values, err := exactFixtureParameters(query, []string{"login_verifier"})
	if err != nil {
		writeFixtureError(writer, http.StatusBadRequest, "development login continuation rejected")
		return
	}
	now, ok := runtime.authorizationNow(writer)
	if !ok {
		return
	}
	consentChallenge, err := newFixtureOpaque("hydra-consent")
	if err != nil {
		writeFixtureError(writer, http.StatusServiceUnavailable, "development authorization unavailable")
		return
	}
	runtime.authMu.Lock()
	runtime.initializeAuthorizationStateLocked()
	runtime.pruneAuthorizationStateLocked(now)
	loginChallenge, found := runtime.hydraLoginProofs[values["login_verifier"]]
	login, loginFound := runtime.hydraLogins[loginChallenge]
	if !found || !loginFound || login.status != hydraLoginAccepted || !login.expiresAt.After(now) ||
		len(runtime.hydraConsents) >= maximumAuthorizationTransactions {
		runtime.authMu.Unlock()
		writeFixtureError(writer, http.StatusBadRequest, "development login continuation rejected")
		return
	}
	delete(runtime.hydraLoginProofs, values["login_verifier"])
	login.status = hydraLoginConsentPending
	runtime.hydraLogins[loginChallenge] = login
	runtime.hydraConsents[consentChallenge] = hydraConsentFixture{
		loginChallenge: loginChallenge, clientID: login.clientID, redirectURI: login.redirectURI,
		state: login.state, codeChallenge: login.codeChallenge,
		scopes: append([]string(nil), login.scopes...), audience: append([]string(nil), login.audience...),
		requestURL: login.requestURL,
		subject:    login.subject, loginSessionID: login.loginSessionID,
		status: hydraConsentPending, expiresAt: login.expiresAt,
	}
	runtime.authMu.Unlock()
	redirect, err := fixtureURL(runtime.bundle.document.Hydra.ConsentURL, url.Values{"consent_challenge": {consentChallenge}})
	if err != nil {
		writeFixtureError(writer, http.StatusInternalServerError, "development authorization unavailable")
		return
	}
	writeFixtureRedirect(writer, redirect)
}

func (runtime *fixtureRuntime) continueHydraConsent(writer http.ResponseWriter, query url.Values) {
	values, err := exactFixtureParameters(query, []string{"consent_verifier"})
	if err != nil {
		writeFixtureError(writer, http.StatusBadRequest, "development consent continuation rejected")
		return
	}
	now, ok := runtime.authorizationNow(writer)
	if !ok {
		return
	}
	code, err := newFixtureOpaque("hydra-code")
	if err != nil {
		writeFixtureError(writer, http.StatusServiceUnavailable, "development authorization unavailable")
		return
	}
	runtime.authMu.Lock()
	runtime.initializeAuthorizationStateLocked()
	runtime.pruneAuthorizationStateLocked(now)
	consentChallenge, found := runtime.hydraConsentProofs[values["consent_verifier"]]
	consent, consentFound := runtime.hydraConsents[consentChallenge]
	login, loginFound := runtime.hydraLogins[consent.loginChallenge]
	if !found || !consentFound || !loginFound || consent.status != hydraConsentAccepted ||
		login.status != hydraLoginConsentPending || !consent.expiresAt.After(now) ||
		len(runtime.hydraCodes) >= maximumAuthorizationTransactions {
		runtime.authMu.Unlock()
		writeFixtureError(writer, http.StatusBadRequest, "development consent continuation rejected")
		return
	}
	delete(runtime.hydraConsentProofs, values["consent_verifier"])
	consent.status = hydraConsentConsumed
	runtime.hydraConsents[consentChallenge] = consent
	login.status = hydraLoginCodeIssued
	runtime.hydraLogins[consent.loginChallenge] = login
	runtime.hydraCodes[code] = hydraCodeFixture{
		clientID: consent.clientID, redirectURI: consent.redirectURI, codeChallenge: consent.codeChallenge,
		scopes: append([]string(nil), consent.grantedScopes...), audience: append([]string(nil), consent.audience...),
		authority: consent.authority,
		subject:   consent.subject, expiresAt: consent.expiresAt,
	}
	runtime.authMu.Unlock()
	redirect, err := fixtureURL(consent.redirectURI, url.Values{"code": {code}, "state": {consent.state}})
	if err != nil {
		writeFixtureError(writer, http.StatusInternalServerError, "development authorization unavailable")
		return
	}
	writeFixtureRedirect(writer, redirect)
}

func (runtime *fixtureRuntime) serveHydraAdminLogin(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || !emptyFixtureRequest(request) {
		writeFixtureError(writer, http.StatusMethodNotAllowed, "development Hydra Admin login request rejected")
		return
	}
	query, err := parseFixtureQuery(request.URL.RawQuery)
	values, parameterErr := exactFixtureParameters(query, []string{"login_challenge"})
	if err != nil || parameterErr != nil {
		writeFixtureError(writer, http.StatusBadRequest, "development Hydra Admin login request rejected")
		return
	}
	now, ok := runtime.authorizationNow(writer)
	if !ok {
		return
	}
	runtime.authMu.Lock()
	runtime.initializeAuthorizationStateLocked()
	runtime.pruneAuthorizationStateLocked(now)
	login, found := runtime.hydraLogins[values["login_challenge"]]
	runtime.authMu.Unlock()
	if !found || login.status != hydraLoginPending || !login.expiresAt.After(now) {
		writeFixtureError(writer, http.StatusNotFound, "development Hydra login challenge not found")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"challenge": values["login_challenge"], "skip": false, "subject": "",
		"client":                          map[string]any{"client_id": login.clientID},
		"requested_scope":                 append([]string(nil), login.scopes...),
		"requested_access_token_audience": append([]string(nil), login.audience...),
		"request_url":                     login.requestURL,
	})
}

func (runtime *fixtureRuntime) serveHydraAdminLoginAccept(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPut {
		writeFixtureError(writer, http.StatusMethodNotAllowed, "development Hydra login acceptance rejected")
		return
	}
	query, err := parseFixtureQuery(request.URL.RawQuery)
	values, parameterErr := exactFixtureParameters(query, []string{"login_challenge"})
	var body struct {
		Subject     string         `json:"subject"`
		Remember    bool           `json:"remember"`
		RememberFor int64          `json:"remember_for"`
		Context     map[string]any `json:"context"`
	}
	if err != nil || parameterErr != nil || readFixtureJSON(writer, request, &body) != nil ||
		!canonicalFixtureUUID(body.Subject) || body.Remember || body.RememberFor != 0 || len(body.Context) != 0 {
		writeFixtureError(writer, http.StatusBadRequest, "development Hydra login acceptance rejected")
		return
	}
	now, ok := runtime.authorizationNow(writer)
	if !ok {
		return
	}
	proof, proofErr := newFixtureOpaque("hydra-login-proof")
	loginSessionID, sessionErr := newFixtureOpaque("hydra-session")
	if proofErr != nil || sessionErr != nil {
		writeFixtureError(writer, http.StatusServiceUnavailable, "development authorization unavailable")
		return
	}
	runtime.authMu.Lock()
	runtime.initializeAuthorizationStateLocked()
	runtime.pruneAuthorizationStateLocked(now)
	login, found := runtime.hydraLogins[values["login_challenge"]]
	if !found || login.status != hydraLoginPending || !login.expiresAt.After(now) {
		runtime.authMu.Unlock()
		writeFixtureError(writer, http.StatusConflict, "development Hydra login challenge is not pending")
		return
	}
	login.subject = body.Subject
	login.loginSessionID = loginSessionID
	login.status = hydraLoginAccepted
	runtime.hydraLogins[values["login_challenge"]] = login
	runtime.hydraLoginProofs[proof] = values["login_challenge"]
	runtime.authMu.Unlock()
	redirect, _ := fixtureURL(runtime.bundle.document.Hydra.PublicOrigin+"/oauth2/auth", url.Values{"login_verifier": {proof}})
	writeJSON(writer, http.StatusOK, map[string]string{"redirect_to": redirect})
}

func (runtime *fixtureRuntime) serveHydraAdminLoginReject(writer http.ResponseWriter, request *http.Request) {
	runtime.rejectHydraRequest(writer, request, true)
}

func (runtime *fixtureRuntime) serveHydraAdminConsent(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || !emptyFixtureRequest(request) {
		writeFixtureError(writer, http.StatusMethodNotAllowed, "development Hydra Admin consent request rejected")
		return
	}
	query, err := parseFixtureQuery(request.URL.RawQuery)
	values, parameterErr := exactFixtureParameters(query, []string{"consent_challenge"})
	if err != nil || parameterErr != nil {
		writeFixtureError(writer, http.StatusBadRequest, "development Hydra Admin consent request rejected")
		return
	}
	now, ok := runtime.authorizationNow(writer)
	if !ok {
		return
	}
	runtime.authMu.Lock()
	runtime.initializeAuthorizationStateLocked()
	runtime.pruneAuthorizationStateLocked(now)
	consent, found := runtime.hydraConsents[values["consent_challenge"]]
	runtime.authMu.Unlock()
	if !found || consent.status != hydraConsentPending || !consent.expiresAt.After(now) {
		writeFixtureError(writer, http.StatusNotFound, "development Hydra consent challenge not found")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"challenge": values["consent_challenge"], "skip": false, "subject": consent.subject,
		"client":                          map[string]any{"client_id": consent.clientID},
		"requested_scope":                 append([]string(nil), consent.scopes...),
		"requested_access_token_audience": append([]string(nil), consent.audience...),
		"login_challenge":                 consent.loginChallenge, "login_session_id": consent.loginSessionID,
		"request_url": consent.requestURL,
	})
}

func (runtime *fixtureRuntime) serveHydraAdminConsentAccept(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPut {
		writeFixtureError(writer, http.StatusMethodNotAllowed, "development Hydra consent acceptance rejected")
		return
	}
	query, err := parseFixtureQuery(request.URL.RawQuery)
	values, parameterErr := exactFixtureParameters(query, []string{"consent_challenge"})
	var body struct {
		GrantScope               []string `json:"grant_scope"`
		GrantAccessTokenAudience []string `json:"grant_access_token_audience"`
		Remember                 bool     `json:"remember"`
		RememberFor              int64    `json:"remember_for"`
		Session                  struct {
			AccessToken map[string]corecontract.UserOAuthAuthority `json:"access_token"`
			IDToken     map[string]any                             `json:"id_token"`
		} `json:"session"`
	}
	readErr := readFixtureJSON(writer, request, &body)
	authority := body.Session.AccessToken["agentserver"]
	if err != nil || parameterErr != nil || readErr != nil ||
		!sameFixtureSet(body.GrantScope, browserAuthorizationScopes()) ||
		!sameFixtureSet(body.GrantAccessTokenAudience, []string{BrowserTokenAudience}) ||
		body.Remember || body.RememberFor != 0 || len(body.Session.AccessToken) != 1 || len(body.Session.IDToken) != 0 ||
		!validFixtureBrowserAuthority(authority, runtime.bundle.document.Authority.WorkspaceID, body.GrantScope) {
		writeFixtureError(writer, http.StatusBadRequest, "development Hydra consent acceptance rejected")
		return
	}
	now, ok := runtime.authorizationNow(writer)
	if !ok {
		return
	}
	proof, err := newFixtureOpaque("hydra-consent-proof")
	if err != nil {
		writeFixtureError(writer, http.StatusServiceUnavailable, "development authorization unavailable")
		return
	}
	runtime.authMu.Lock()
	runtime.initializeAuthorizationStateLocked()
	runtime.pruneAuthorizationStateLocked(now)
	consent, found := runtime.hydraConsents[values["consent_challenge"]]
	if !found || consent.status != hydraConsentPending || !consent.expiresAt.After(now) {
		runtime.authMu.Unlock()
		writeFixtureError(writer, http.StatusConflict, "development Hydra consent challenge is not pending")
		return
	}
	consent.status = hydraConsentAccepted
	consent.grantedScopes = append([]string(nil), body.GrantScope...)
	consent.authority = authority
	runtime.hydraConsents[values["consent_challenge"]] = consent
	runtime.hydraConsentProofs[proof] = values["consent_challenge"]
	runtime.authMu.Unlock()
	redirect, _ := fixtureURL(runtime.bundle.document.Hydra.PublicOrigin+"/oauth2/auth", url.Values{"consent_verifier": {proof}})
	writeJSON(writer, http.StatusOK, map[string]string{"redirect_to": redirect})
}

func (runtime *fixtureRuntime) serveHydraAdminConsentReject(writer http.ResponseWriter, request *http.Request) {
	runtime.rejectHydraRequest(writer, request, false)
}

func (runtime *fixtureRuntime) rejectHydraRequest(writer http.ResponseWriter, request *http.Request, loginRequest bool) {
	if request.Method != http.MethodPut {
		writeFixtureError(writer, http.StatusMethodNotAllowed, "development Hydra rejection rejected")
		return
	}
	parameter := "consent_challenge"
	if loginRequest {
		parameter = "login_challenge"
	}
	query, err := parseFixtureQuery(request.URL.RawQuery)
	values, parameterErr := exactFixtureParameters(query, []string{parameter})
	var body struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		StatusCode       int    `json:"status_code"`
	}
	if err != nil || parameterErr != nil || readFixtureJSON(writer, request, &body) != nil ||
		body.Error == "" || len(body.Error) > 128 || strings.ContainsAny(body.Error, "\x00\r\n") ||
		body.ErrorDescription == "" || len(body.ErrorDescription) > 2048 || strings.ContainsAny(body.ErrorDescription, "\x00\r\n") ||
		body.StatusCode != http.StatusForbidden {
		writeFixtureError(writer, http.StatusBadRequest, "development Hydra rejection rejected")
		return
	}
	now, ok := runtime.authorizationNow(writer)
	if !ok {
		return
	}
	var redirectURI, state string
	runtime.authMu.Lock()
	runtime.initializeAuthorizationStateLocked()
	runtime.pruneAuthorizationStateLocked(now)
	if loginRequest {
		login, found := runtime.hydraLogins[values[parameter]]
		if !found || login.status != hydraLoginPending {
			runtime.authMu.Unlock()
			writeFixtureError(writer, http.StatusConflict, "development Hydra login challenge is not pending")
			return
		}
		login.status = hydraLoginRejected
		runtime.hydraLogins[values[parameter]] = login
		redirectURI, state = login.redirectURI, login.state
	} else {
		consent, found := runtime.hydraConsents[values[parameter]]
		if !found || consent.status != hydraConsentPending {
			runtime.authMu.Unlock()
			writeFixtureError(writer, http.StatusConflict, "development Hydra consent challenge is not pending")
			return
		}
		consent.status = hydraConsentRejected
		runtime.hydraConsents[values[parameter]] = consent
		redirectURI, state = consent.redirectURI, consent.state
	}
	runtime.authMu.Unlock()
	redirect, _ := fixtureURL(redirectURI, url.Values{
		"error": {body.Error}, "error_description": {body.ErrorDescription}, "state": {state},
	})
	writeJSON(writer, http.StatusOK, map[string]string{"redirect_to": redirect})
}

func (runtime *fixtureRuntime) serveExternalOIDCDiscovery(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || request.URL.RawQuery != "" || !emptyFixtureRequest(request) || request.Header.Get("Authorization") != "" {
		writeFixtureError(writer, http.StatusBadRequest, "development OIDC discovery rejected")
		return
	}
	document := runtime.bundle.document.Hydra.ExternalOIDC
	writeJSON(writer, http.StatusOK, map[string]any{
		"issuer":                                document.Issuer,
		"authorization_endpoint":                document.AuthorizationURL,
		"token_endpoint":                        document.Issuer + "/token",
		"jwks_uri":                              document.Issuer + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"EdDSA"},
		"scopes_supported":                      []string{"openid"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

func (runtime *fixtureRuntime) serveExternalOIDCAuthorization(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || !emptyFixtureRequest(request) || request.Header.Get("Authorization") != "" {
		writeFixtureError(writer, http.StatusBadRequest, "development external OIDC authorization rejected")
		return
	}
	query, err := parseFixtureQuery(request.URL.RawQuery)
	values, parameterErr := exactFixtureParameters(query, []string{
		"response_type", "client_id", "redirect_uri", "scope", "state", "nonce", "code_challenge", "code_challenge_method",
	})
	document := runtime.bundle.document.Hydra.ExternalOIDC
	if err != nil || parameterErr != nil || values["response_type"] != "code" || values["client_id"] != document.ClientID ||
		values["redirect_uri"] != document.RedirectURI || values["scope"] != "openid" ||
		values["code_challenge_method"] != "S256" || !validFixtureSecret(values["state"]) ||
		!validFixtureSecret(values["nonce"]) || !validPKCEChallenge(values["code_challenge"]) {
		writeFixtureError(writer, http.StatusBadRequest, "development external OIDC authorization rejected")
		return
	}
	now, ok := runtime.authorizationNow(writer)
	if !ok {
		return
	}
	code, err := newFixtureOpaque("idp-code")
	if err != nil {
		writeFixtureError(writer, http.StatusServiceUnavailable, "development external OIDC unavailable")
		return
	}
	runtime.authMu.Lock()
	runtime.initializeAuthorizationStateLocked()
	runtime.pruneAuthorizationStateLocked(now)
	if len(runtime.idpCodes) >= maximumAuthorizationTransactions {
		runtime.authMu.Unlock()
		writeFixtureError(writer, http.StatusServiceUnavailable, "development external OIDC capacity reached")
		return
	}
	runtime.idpCodes[code] = idpCodeFixture{
		clientID: values["client_id"], redirectURI: values["redirect_uri"], codeChallenge: values["code_challenge"],
		nonce: values["nonce"], expiresAt: now.Add(authorizationTransactionTTL),
	}
	runtime.authMu.Unlock()
	redirect, _ := fixtureURL(document.RedirectURI, url.Values{
		"code": {code}, "state": {values["state"]}, "iss": {document.Issuer},
	})
	writeFixtureRedirect(writer, redirect)
}

func (runtime *fixtureRuntime) serveExternalOIDCToken(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.RawQuery != "" {
		writeOAuthFixtureError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	clientID, clientSecret, ok := request.BasicAuth()
	document := runtime.bundle.document.Hydra.ExternalOIDC
	if !ok || clientID != document.ClientID || !exactTokenEqual(runtime.bundle.externalOIDCClientSecret, clientSecret) {
		writer.Header().Set("WWW-Authenticate", `Basic realm="agentserver-dev-idp"`)
		writeOAuthFixtureError(writer, http.StatusUnauthorized, "invalid_client")
		return
	}
	form, err := readFixtureForm(writer, request)
	values, parameterErr := exactFixtureParameters(form, []string{"grant_type", "code", "redirect_uri", "code_verifier"})
	if err != nil || parameterErr != nil || values["grant_type"] != "authorization_code" ||
		values["redirect_uri"] != document.RedirectURI || !validFixtureSecret(values["code_verifier"]) {
		writeOAuthFixtureError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	now, validNow := runtime.authorizationNow(writer)
	if !validNow {
		return
	}
	runtime.authMu.Lock()
	runtime.initializeAuthorizationStateLocked()
	runtime.pruneAuthorizationStateLocked(now)
	code, found := runtime.idpCodes[values["code"]]
	if !found || code.clientID != clientID || code.redirectURI != values["redirect_uri"] || !code.expiresAt.After(now) ||
		!verifyFixturePKCE(values["code_verifier"], code.codeChallenge) {
		runtime.authMu.Unlock()
		writeOAuthFixtureError(writer, http.StatusBadRequest, "invalid_grant")
		return
	}
	delete(runtime.idpCodes, values["code"])
	runtime.authMu.Unlock()
	idpAccessToken, err := newFixtureOpaque("idp-access")
	if err != nil {
		writeOAuthFixtureError(writer, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	idToken, err := runtime.signExternalIDToken(code.nonce, now)
	if err != nil {
		writeOAuthFixtureError(writer, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"access_token": idpAccessToken, "token_type": "Bearer", "expires_in": int64(runtime.bundle.responseTTL.Seconds()),
		"scope": "openid", "id_token": idToken,
	})
}

func (runtime *fixtureRuntime) serveExternalOIDCJWKS(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || request.URL.RawQuery != "" || !emptyFixtureRequest(request) || request.Header.Get("Authorization") != "" {
		writeFixtureError(writer, http.StatusBadRequest, "development external OIDC JWKS rejected")
		return
	}
	publicKey := runtime.externalOIDCPublicKey()
	writeJSON(writer, http.StatusOK, map[string]any{"keys": []map[string]string{{
		"kty": "OKP", "use": "sig", "crv": "Ed25519", "alg": "EdDSA", "kid": externalOIDCSigningKeyID,
		"x": base64.RawURLEncoding.EncodeToString(publicKey),
	}}})
}

func (runtime *fixtureRuntime) serveHydraToken(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.RawQuery != "" || request.Header.Get("Authorization") != "" {
		writeOAuthFixtureError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	form, err := readFixtureForm(writer, request)
	values, parameterErr := exactFixtureParameters(form, []string{"grant_type", "code", "redirect_uri", "client_id", "code_verifier"})
	document := runtime.bundle.document.Hydra
	if err != nil || parameterErr != nil || values["grant_type"] != "authorization_code" ||
		values["client_id"] != document.BrowserClientID || values["redirect_uri"] != document.BrowserRedirectURI ||
		!validFixtureSecret(values["code_verifier"]) {
		writeOAuthFixtureError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	now, ok := runtime.authorizationNow(writer)
	if !ok {
		return
	}
	accessToken, err := newFixtureOpaque("hydra-access")
	if err != nil {
		writeOAuthFixtureError(writer, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	runtime.authMu.Lock()
	runtime.initializeAuthorizationStateLocked()
	runtime.pruneAuthorizationStateLocked(now)
	code, found := runtime.hydraCodes[values["code"]]
	if !found || code.clientID != values["client_id"] || code.redirectURI != values["redirect_uri"] ||
		!code.expiresAt.After(now) || !verifyFixturePKCE(values["code_verifier"], code.codeChallenge) ||
		len(runtime.accessTokens) >= maximumAuthorizationTransactions {
		runtime.authMu.Unlock()
		writeOAuthFixtureError(writer, http.StatusBadRequest, "invalid_grant")
		return
	}
	delete(runtime.hydraCodes, values["code"])
	expiresAt := now.Add(runtime.bundle.responseTTL)
	runtime.accessTokens[accessToken] = accessTokenFixture{
		subject: code.subject, clientID: code.clientID, scopes: append([]string(nil), code.scopes...),
		audience: append([]string(nil), code.audience...), authority: code.authority, expiresAt: expiresAt,
	}
	runtime.authMu.Unlock()
	writeJSON(writer, http.StatusOK, map[string]any{
		"access_token": accessToken, "token_type": "Bearer", "expires_in": int64(runtime.bundle.responseTTL.Seconds()),
		"scope": strings.Join(code.scopes, " "),
	})
}

func (runtime *fixtureRuntime) lookupAccessToken(token string, now time.Time) (accessTokenFixture, bool) {
	runtime.authMu.Lock()
	defer runtime.authMu.Unlock()
	runtime.initializeAuthorizationStateLocked()
	runtime.pruneAuthorizationStateLocked(now)
	access, found := runtime.accessTokens[token]
	return access, found && access.expiresAt.After(now)
}

func (runtime *fixtureRuntime) initializeAuthorizationStateLocked() {
	if runtime.hydraLogins == nil {
		runtime.hydraLogins = make(map[string]hydraLoginFixture)
	}
	if runtime.hydraLoginProofs == nil {
		runtime.hydraLoginProofs = make(map[string]string)
	}
	if runtime.hydraConsents == nil {
		runtime.hydraConsents = make(map[string]hydraConsentFixture)
	}
	if runtime.hydraConsentProofs == nil {
		runtime.hydraConsentProofs = make(map[string]string)
	}
	if runtime.idpCodes == nil {
		runtime.idpCodes = make(map[string]idpCodeFixture)
	}
	if runtime.hydraCodes == nil {
		runtime.hydraCodes = make(map[string]hydraCodeFixture)
	}
	if runtime.accessTokens == nil {
		runtime.accessTokens = make(map[string]accessTokenFixture)
	}
}

func (runtime *fixtureRuntime) pruneAuthorizationStateLocked(now time.Time) {
	for challenge, login := range runtime.hydraLogins {
		if !login.expiresAt.After(now) {
			delete(runtime.hydraLogins, challenge)
		}
	}
	for challenge, consent := range runtime.hydraConsents {
		if !consent.expiresAt.After(now) {
			delete(runtime.hydraConsents, challenge)
		}
	}
	for code, value := range runtime.idpCodes {
		if !value.expiresAt.After(now) {
			delete(runtime.idpCodes, code)
		}
	}
	for code, value := range runtime.hydraCodes {
		if !value.expiresAt.After(now) {
			delete(runtime.hydraCodes, code)
		}
	}
	for token, value := range runtime.accessTokens {
		if !value.expiresAt.After(now) {
			delete(runtime.accessTokens, token)
		}
	}
	for proof, challenge := range runtime.hydraLoginProofs {
		if _, found := runtime.hydraLogins[challenge]; !found {
			delete(runtime.hydraLoginProofs, proof)
		}
	}
	for proof, challenge := range runtime.hydraConsentProofs {
		if _, found := runtime.hydraConsents[challenge]; !found {
			delete(runtime.hydraConsentProofs, proof)
		}
	}
}

func (runtime *fixtureRuntime) authorizationNow(writer http.ResponseWriter) (time.Time, bool) {
	if runtime == nil || runtime.bundle == nil || runtime.now == nil {
		writeFixtureError(writer, http.StatusServiceUnavailable, "development authorization unavailable")
		return time.Time{}, false
	}
	now := runtime.now().UTC()
	if now.IsZero() {
		writeFixtureError(writer, http.StatusServiceUnavailable, "development authorization unavailable")
		return time.Time{}, false
	}
	return now, true
}

func (runtime *fixtureRuntime) externalOIDCPrivateKey() ed25519.PrivateKey {
	digest := sha256.New()
	_, _ = io.WriteString(digest, "agentserver-v2/insecure-dev-idp-signing-key/v1\x00")
	_, _ = digest.Write(runtime.bundle.externalOIDCClientSecret)
	seed := digest.Sum(nil)
	return ed25519.NewKeyFromSeed(seed)
}

func (runtime *fixtureRuntime) externalOIDCPublicKey() ed25519.PublicKey {
	privateKey := runtime.externalOIDCPrivateKey()
	defer clear(privateKey)
	return append(ed25519.PublicKey(nil), privateKey.Public().(ed25519.PublicKey)...)
}

func (runtime *fixtureRuntime) signExternalIDToken(nonce string, now time.Time) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "EdDSA", "kid": externalOIDCSigningKeyID, "typ": "JWT"})
	if err != nil {
		return "", err
	}
	document := runtime.bundle.document.Hydra.ExternalOIDC
	claims, err := json.Marshal(map[string]any{
		"iss": document.Issuer, "sub": document.Subject, "aud": document.ClientID,
		"iat": now.Unix(), "exp": now.Add(runtime.bundle.responseTTL).Unix(), "nonce": nonce,
	})
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	privateKey := runtime.externalOIDCPrivateKey()
	signature := ed25519.Sign(privateKey, []byte(signingInput))
	clear(privateKey)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func newFixtureOpaque(label string) (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	defer clear(raw)
	return "asv2dev-" + label + "-" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func parseFixtureQuery(raw string) (url.Values, error) {
	if raw == "" || len(raw) > 32*1024 || strings.ContainsAny(raw, "\x00\r\n") {
		return nil, errors.New("fixture query is empty or outside bounds")
	}
	return url.ParseQuery(raw)
}

func exactFixtureParameters(values url.Values, required []string) (map[string]string, error) {
	if len(values) != len(required) {
		return nil, errors.New("fixture parameter set is not exact")
	}
	allowed := make(map[string]struct{}, len(required))
	result := make(map[string]string, len(required))
	for _, name := range required {
		allowed[name] = struct{}{}
		entries, found := values[name]
		if !found || len(entries) != 1 || entries[0] == "" || len(entries[0]) > 8192 || strings.ContainsAny(entries[0], "\x00\r\n") {
			return nil, errors.New("fixture parameter is missing, duplicate, or outside bounds")
		}
		result[name] = entries[0]
	}
	for name := range values {
		if _, found := allowed[name]; !found {
			return nil, errors.New("fixture parameter is unknown")
		}
	}
	return result, nil
}

func readFixtureForm(writer http.ResponseWriter, request *http.Request) (url.Values, error) {
	mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" || len(parameters) != 0 || len(request.Header.Values("Content-Type")) != 1 ||
		request.Header.Get("Content-Encoding") != "" || len(request.TransferEncoding) != 0 ||
		request.ContentLength < 0 || request.ContentLength > maximumAuthorizationBodyBytes {
		return nil, errors.New("fixture form transport is invalid")
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumAuthorizationBodyBytes)
	body, err := io.ReadAll(request.Body)
	if err != nil || int64(len(body)) != request.ContentLength {
		return nil, errors.New("fixture form body is invalid")
	}
	return url.ParseQuery(string(body))
}

func readFixtureJSON(writer http.ResponseWriter, request *http.Request, destination any) error {
	if !exactHeader(request.Header, "Accept", "application/json") || !exactHeader(request.Header, "Content-Type", "application/json") ||
		request.Header.Get("Content-Encoding") != "" || len(request.TransferEncoding) != 0 ||
		request.ContentLength < 0 || request.ContentLength > maximumAuthorizationBodyBytes {
		return errors.New("fixture JSON transport is invalid")
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumAuthorizationBodyBytes)
	body, err := io.ReadAll(request.Body)
	if err != nil || int64(len(body)) != request.ContentLength {
		return errors.New("fixture JSON body is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("fixture JSON body contains trailing data")
	}
	return nil
}

func emptyFixtureRequest(request *http.Request) bool {
	return request.ContentLength == 0 && len(request.TransferEncoding) == 0 && request.Header.Get("Content-Encoding") == ""
}

func validFixtureSecret(value string) bool {
	if len(value) < 43 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' || character == '~') {
			return false
		}
	}
	return true
}

func validPKCEChallenge(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && base64.RawURLEncoding.EncodeToString(decoded) == value
}

func verifyFixturePKCE(verifier, challenge string) bool {
	digest := sha256.Sum256([]byte(verifier))
	encoded := base64.RawURLEncoding.EncodeToString(digest[:])
	return exactTokenEqual([]byte(encoded), challenge)
}

func sameFixtureSet(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	want := make(map[string]struct{}, len(expected))
	for _, value := range expected {
		want[value] = struct{}{}
	}
	seen := make(map[string]struct{}, len(actual))
	for _, value := range actual {
		if _, found := want[value]; !found {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validFixtureBrowserAuthority(authority corecontract.UserOAuthAuthority, workspaceID string, grantedScopes []string) bool {
	if authority.Version != corecontract.UserOAuthAuthorityVersion ||
		authority.Authority != corecontract.UserOAuthBrowserAuthority || len(authority.GlobalPermissions) != 0 ||
		len(authority.WorkspaceGrants) != 1 {
		return false
	}
	grant := authority.WorkspaceGrants[0]
	if grant.WorkspaceID != workspaceID || grant.Generation != 1 || !slices.IsSorted(grant.Permissions) {
		return false
	}
	permissions := make([]string, 0, len(grantedScopes))
	for _, scope := range grantedScopes {
		if scope != corecontract.OAuthOpenIDScope {
			permissions = append(permissions, scope)
		}
	}
	return sameFixtureSet(grant.Permissions, permissions)
}

func canonicalFixtureUUID(value string) bool {
	return value != "00000000-0000-0000-0000-000000000000" && uuidPattern.MatchString(value)
}

func fixtureURL(raw string, query url.Values) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("fixture redirect base is invalid")
	}
	parsed.RawQuery = query.Encode()
	result := parsed.String()
	if len(result) > 8192 || strings.ContainsAny(result, "\x00\r\n") {
		return "", errors.New("fixture redirect is outside bounds")
	}
	return result, nil
}

func writeFixtureRedirect(writer http.ResponseWriter, location string) {
	if _, err := url.ParseRequestURI(location); err != nil || len(location) > 8192 || strings.ContainsAny(location, "\x00\r\n") {
		writeFixtureError(writer, http.StatusInternalServerError, "development authorization redirect unavailable")
		return
	}
	writer.Header().Set("Location", location)
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Pragma", "no-cache")
	writer.Header().Set("Content-Length", "0")
	writer.WriteHeader(http.StatusFound)
}

func writeOAuthFixtureError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, map[string]string{"error": code})
}
