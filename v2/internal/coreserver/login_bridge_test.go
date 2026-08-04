package coreserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

const (
	loginBridgeTestTransactionID = "71000000-0000-4000-8000-000000000007"
	loginBridgeTestUserID        = "10000000-0000-4000-8000-000000000001"
	loginBridgeTestWorkspaceID   = "20000000-0000-4000-8000-000000000002"
)

func TestLoginBridgeBindsAndConsumesExternalOIDCCallbackOnce(t *testing.T) {
	bridge, store, hydra, provider := newLoginBridgeFixture(t)
	started, err := bridge.BeginLogin(t.Context(), "login-challenge", "")
	if err != nil {
		t.Fatal(err)
	}
	if !started.External || started.BrowserBinding == "" || started.RedirectTo == "" || store.login.Status != coredb.OIDCLoginStatusPending {
		t.Fatalf("started login = %+v stored=%+v", started, store.login)
	}
	for label, secret := range map[string]string{
		"challenge": "login-challenge", "state": provider.state, "nonce": provider.nonce,
		"verifier": provider.verifier, "binding": started.BrowserBinding,
	} {
		if bytes.Contains(store.login.SealedSecrets, []byte(secret)) {
			t.Fatalf("sealed transaction exposes %s", label)
		}
	}
	parsed, err := url.Parse(started.RedirectTo)
	if err != nil || parsed.Query().Get("state") != provider.state || parsed.Query().Get("nonce") != provider.nonce {
		t.Fatalf("external authorization redirect = %q", started.RedirectTo)
	}

	completed, err := bridge.CompleteCallback(t.Context(), provider.state, "external-code", "", started.BrowserBinding)
	if err != nil {
		t.Fatal(err)
	}
	if !completed.ClearBinding || completed.RedirectTo != hydraTestContinuationURL(hydra.loginRequest.RequestURL, hydraLoginVerifierQuery, "accepted") ||
		store.login.Status != coredb.OIDCLoginStatusAccepted || store.login.UserID != loginBridgeTestUserID ||
		hydra.acceptLoginCalls != 1 || hydra.acceptedSubject != loginBridgeTestUserID || provider.exchangeCalls != 1 {
		t.Fatalf("completed login = %+v stored=%+v hydra=%+v provider=%+v", completed, store.login, hydra, provider)
	}
	if len(store.login.SealedRedirect) == 0 || bytes.Contains(store.login.SealedRedirect, []byte(completed.RedirectTo)) {
		t.Fatal("accepted Hydra redirect was not sealed at rest")
	}

	if _, err := bridge.CompleteCallback(t.Context(), provider.state, "external-code", "", started.BrowserBinding); err == nil {
		t.Fatal("replayed callback was accepted")
	}
	if hydra.acceptLoginCalls != 1 || provider.exchangeCalls != 1 {
		t.Fatalf("callback replay crossed an external boundary: accept=%d exchange=%d", hydra.acceptLoginCalls, provider.exchangeCalls)
	}
}

func TestLoginBridgeRejectsWrongBrowserBindingBeforeCodeExchange(t *testing.T) {
	bridge, store, hydra, provider := newLoginBridgeFixture(t)
	started, err := bridge.BeginLogin(t.Context(), "login-challenge", "")
	if err != nil {
		t.Fatal(err)
	}
	wrongBinding := "wrong-binding-value-that-is-exactly-long-enough-1234567890"
	if _, err := bridge.CompleteCallback(t.Context(), provider.state, "external-code", "", wrongBinding); err == nil {
		t.Fatal("callback with a different browser binding was accepted")
	}
	if store.login.Status != coredb.OIDCLoginStatusPending || provider.exchangeCalls != 0 || hydra.acceptLoginCalls != 0 {
		t.Fatalf("wrong binding mutated authority: stored=%+v exchange=%d accept=%d original=%q", store.login, provider.exchangeCalls, hydra.acceptLoginCalls, started.BrowserBinding)
	}
}

func TestLoginBridgeFailsClosedForUnmappedIdentity(t *testing.T) {
	bridge, store, hydra, provider := newLoginBridgeFixture(t)
	provider.identity.Subject = "unmapped-user"
	started, err := bridge.BeginLogin(t.Context(), "login-challenge", "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := bridge.CompleteCallback(t.Context(), provider.state, "external-code", "", started.BrowserBinding)
	if err != nil {
		t.Fatal(err)
	}
	if result.RedirectTo != hydraTestContinuationURL(hydra.loginRequest.RequestURL, hydraLoginVerifierQuery, "rejected") ||
		store.login.Status != coredb.OIDCLoginStatusFailed || store.login.FailureCode != "identity_not_mapped" ||
		hydra.rejectLoginCalls != 1 || hydra.acceptLoginCalls != 0 {
		t.Fatalf("unmapped identity result=%+v stored=%+v hydra=%+v", result, store.login, hydra)
	}
}

func TestLoginBridgeConsentAllowsProfileSubsetAndIsOneShot(t *testing.T) {
	bridge, store, hydra, _ := newLoginBridgeFixture(t)
	hydra.consentRequest = HydraConsentRequest{
		Challenge: "consent-challenge", Subject: loginBridgeTestUserID,
		Client:                       HydraOAuth2Client{ClientID: corecontract.BrowserOAuthClientID},
		RequestedScope:               []string{corecontract.BrowserOAuthRunsCreateScope, corecontract.OAuthOpenIDScope, corecontract.BrowserOAuthSessionsCreateScope},
		RequestedAccessTokenAudience: []string{corecontract.BrowserOAuthAudience},
		LoginChallenge:               "login-challenge", LoginSessionID: "login-session",
		RequestURL: browserAuthorizationRequestURL(loginBridgeTestWorkspaceID),
	}
	result, err := bridge.Consent(t.Context(), "consent-challenge")
	if err != nil {
		t.Fatal(err)
	}
	if result.RedirectTo != hydraTestContinuationURL(hydra.consentRequest.RequestURL, hydraConsentVerifierQuery, "accepted") ||
		store.consent.Status != coredb.HydraConsentStatusAccepted || hydra.acceptConsentCalls != 1 ||
		!sameUniqueTextSet(hydra.acceptedConsentScopes, hydra.consentRequest.RequestedScope) ||
		!sameUniqueTextSet(hydra.acceptedConsentAudience, hydra.consentRequest.RequestedAccessTokenAudience) ||
		hydra.acceptedConsentAuthority.Authority != corecontract.UserOAuthBrowserAuthority ||
		len(hydra.acceptedConsentAuthority.WorkspaceGrants) != 1 ||
		hydra.acceptedConsentAuthority.WorkspaceGrants[0].WorkspaceID != loginBridgeTestWorkspaceID {
		t.Fatalf("accepted consent=%+v stored=%+v hydra=%+v", result, store.consent, hydra)
	}
	if _, err := bridge.Consent(t.Context(), "consent-challenge"); err == nil {
		t.Fatal("replayed consent challenge was accepted")
	}
	if hydra.acceptConsentCalls != 1 {
		t.Fatalf("consent replay reached Hydra accept %d times", hydra.acceptConsentCalls)
	}

	otherBridge, otherStore, otherHydra, _ := newLoginBridgeFixture(t)
	otherHydra.consentRequest = hydra.consentRequest
	otherHydra.consentRequest.RequestedScope = append(
		append([]string(nil), hydra.consentRequest.RequestedScope...),
		corecontract.PlatformOAuthExecutorsCreateScope,
	)
	rejected, err := otherBridge.Consent(t.Context(), "consent-challenge")
	if err != nil {
		t.Fatal(err)
	}
	if rejected.RedirectTo != hydraTestContinuationURL(otherHydra.consentRequest.RequestURL, hydraConsentVerifierQuery, "rejected") ||
		otherHydra.rejectConsentCalls != 1 || otherHydra.acceptConsentCalls != 0 || otherStore.consent.Status != "" {
		t.Fatalf("overbroad consent=%+v stored=%+v hydra=%+v", rejected, otherStore.consent, otherHydra)
	}
}

func TestLoginBridgeSelectsPlatformAndBrowserProfilesWithoutMixingAuthority(t *testing.T) {
	bridge, store, hydra, _ := newLoginBridgeFixture(t)
	hydra.loginRequest.Client.ClientID = corecontract.PlatformOAuthClientID
	hydra.loginRequest.RequestedScope = []string{
		corecontract.OAuthOpenIDScope,
		corecontract.PlatformOAuthWorkspacesReadScope,
		corecontract.PlatformOAuthWorkspacesCreateScope,
	}
	hydra.loginRequest.RequestedAccessTokenAudience = []string{corecontract.PlatformOAuthAudience}
	hydra.loginRequest.RequestURL = "https://hydra.internal/oauth2/auth?client_id=" + corecontract.PlatformOAuthClientID
	if _, err := bridge.BeginLogin(t.Context(), "login-challenge", ""); err != nil {
		t.Fatal(err)
	}
	if store.login.HydraClientID != corecontract.PlatformOAuthClientID {
		t.Fatalf("stored login client = %q", store.login.HydraClientID)
	}

	otherBridge, otherStore, otherHydra, _ := newLoginBridgeFixture(t)
	otherHydra.loginRequest.RequestedAccessTokenAudience = []string{corecontract.PlatformOAuthAudience}
	result, err := otherBridge.BeginLogin(t.Context(), "login-challenge", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.RedirectTo != hydraTestContinuationURL(otherHydra.loginRequest.RequestURL, hydraLoginVerifierQuery, "rejected") ||
		otherHydra.rejectLoginCalls != 1 || otherStore.login.ID != "" {
		t.Fatalf("mixed profile result=%+v store=%+v hydra=%+v", result, otherStore.login, otherHydra)
	}
}

func TestLoginBridgeRequiresExactHydraContinuationRedirect(t *testing.T) {
	bridge, _, _, _ := newLoginBridgeFixture(t)
	browserRequestURL := browserAuthorizationRequestURL(loginBridgeTestWorkspaceID)
	platformRequestURL := platformAuthorizationRequestURL()
	loginRedirect := hydraTestContinuationURL(browserRequestURL, hydraLoginVerifierQuery, "opaque")
	consentRedirect := hydraTestContinuationURL(browserRequestURL, hydraConsentVerifierQuery, "opaque")
	for _, valid := range []struct {
		name, redirect, verifier string
	}{
		{name: "Hydra 26.2 Browser login", redirect: loginRedirect, verifier: hydraLoginVerifierQuery},
		{name: "Hydra 26.2 Browser consent", redirect: consentRedirect, verifier: hydraConsentVerifierQuery},
		{name: "Hydra 26.2 Platform login", redirect: hydraTestContinuationURL(platformRequestURL, hydraLoginVerifierQuery, "opaque"), verifier: hydraLoginVerifierQuery},
		{name: "Hydra 26.2 Platform consent", redirect: hydraTestContinuationURL(platformRequestURL, hydraConsentVerifierQuery, "opaque"), verifier: hydraConsentVerifierQuery},
	} {
		t.Run(valid.name, func(t *testing.T) {
			if err := bridge.validateHydraRedirect(valid.redirect, valid.verifier); err != nil {
				t.Fatalf("valid redirect rejected: %v", err)
			}
		})
	}

	for _, invalid := range []struct {
		name, redirect, verifier string
	}{
		{name: "wrong origin", redirect: strings.Replace(loginRedirect, "https://browser.example", "https://sink.invalid", 1), verifier: hydraLoginVerifierQuery},
		{name: "wrong path", redirect: strings.Replace(loginRedirect, "/oauth2/auth?", "/other?", 1), verifier: hydraLoginVerifierQuery},
		{name: "encoded path", redirect: strings.Replace(loginRedirect, "/oauth2/auth?", "/oauth2/%61uth?", 1), verifier: hydraLoginVerifierQuery},
		{name: "wrong verifier", redirect: consentRedirect, verifier: hydraLoginVerifierQuery},
		{name: "duplicate verifier", redirect: loginRedirect + "&login_verifier=second", verifier: hydraLoginVerifierQuery},
		{name: "extra query", redirect: loginRedirect + "&next=https%3A%2F%2Fsink.invalid", verifier: hydraLoginVerifierQuery},
		{name: "empty verifier", redirect: hydraTestContinuationURL(browserRequestURL, hydraLoginVerifierQuery, ""), verifier: hydraLoginVerifierQuery},
		{name: "fragment", redirect: loginRedirect + "#fragment", verifier: hydraLoginVerifierQuery},
		{name: "empty fragment", redirect: loginRedirect + "#", verifier: hydraLoginVerifierQuery},
		{name: "unknown verifier type", redirect: loginRedirect, verifier: "other_verifier"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			if err := bridge.validateHydraRedirect(invalid.redirect, invalid.verifier); err == nil {
				t.Fatal("invalid redirect was accepted")
			}
		})
	}
}

func TestLoginBridgeHydraContinuationDiagnosticsExposeOnlyKnownParameterNames(t *testing.T) {
	bridge, _, _, _ := newLoginBridgeFixture(t)
	redirect := hydraTestContinuationURL(
		browserAuthorizationRequestURL(loginBridgeTestWorkspaceID),
		hydraLoginVerifierQuery,
		"opaque",
	) + "&secret-parameter-name=secret-parameter-value"
	err := bridge.validateHydraRedirect(redirect, hydraLoginVerifierQuery)
	if err == nil || !strings.Contains(err.Error(), "unknown_parameters=1") {
		t.Fatalf("unknown continuation parameter was not diagnosed safely: %v", err)
	}
	for _, secret := range []string{"secret-parameter-name", "secret-parameter-value"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("continuation diagnostic exposed %q: %v", secret, err)
		}
	}
}

func TestLoginBridgeHydraContinuationDiagnosesShortWebCorrelationWithoutExposingIt(t *testing.T) {
	bridge, _, _, _ := newLoginBridgeFixture(t)
	parsed, err := url.Parse(hydraTestContinuationURL(
		browserAuthorizationRequestURL(loginBridgeTestWorkspaceID),
		hydraLoginVerifierQuery,
		"opaque",
	))
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	const shortState = "web-12345678901234567890123456789012"
	query.Set("state", shortState)
	parsed.RawQuery = query.Encode()
	err = bridge.validateHydraRedirect(parsed.String(), hydraLoginVerifierQuery)
	if err == nil || !strings.Contains(err.Error(), "OAuth state authority") || strings.Contains(err.Error(), shortState) {
		t.Fatalf("short web correlation was not diagnosed safely: %v", err)
	}
}

func TestConsentRequestHashCoversLoginAuthority(t *testing.T) {
	request := HydraConsentRequest{
		Challenge: "consent-challenge", LoginChallenge: "login-challenge", LoginSessionID: "login-session",
		Subject: loginBridgeTestUserID, Client: HydraOAuth2Client{ClientID: corecontract.BrowserOAuthClientID},
		RequestedScope: corecontract.BrowserOAuthScopes(), RequestedAccessTokenAudience: []string{corecontract.BrowserOAuthAudience},
		RequestURL: browserAuthorizationRequestURL(loginBridgeTestWorkspaceID),
	}
	grant := HydraConsentGrant{
		Scope:    []string{corecontract.OAuthOpenIDScope, corecontract.BrowserOAuthRunsReadScope},
		Audience: []string{corecontract.BrowserOAuthAudience},
		Authority: corecontract.UserOAuthAuthority{
			Version: corecontract.UserOAuthAuthorityVersion, Authority: corecontract.UserOAuthBrowserAuthority,
			GlobalPermissions: []string{}, WorkspaceGrants: []corecontract.UserOAuthWorkspaceGrant{{
				WorkspaceID: loginBridgeTestWorkspaceID, Generation: 1, Permissions: []string{corecontract.BrowserOAuthRunsReadScope},
			}},
		},
	}
	original, err := consentRequestHash(request, grant)
	if err != nil {
		t.Fatal(err)
	}
	request.LoginChallenge = "different-login-challenge"
	changed, err := consentRequestHash(request, grant)
	if err != nil {
		t.Fatal(err)
	}
	if original == changed {
		t.Fatal("login challenge did not change the consent authority fingerprint")
	}
	request.LoginChallenge = ""
	if _, err := consentRequestHash(request, grant); err == nil {
		t.Fatal("empty login challenge was fingerprinted")
	}
}

func TestLoginTransactionSealerAuthenticatesScopePurposeAndCiphertext(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	sealer, err := NewLoginTransactionSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal(loginBridgeTestTransactionID, loginSecretsPurpose, []byte("secret-value"))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := sealer.Unseal(loginBridgeTestTransactionID, loginSecretsPurpose, sealed)
	if err != nil || string(opened) != "secret-value" {
		t.Fatalf("unsealed = %q, %v", opened, err)
	}
	for name, mutate := range map[string]func() (string, string, []byte){
		"scope":   func() (string, string, []byte) { return loginBridgeTestUserID, loginSecretsPurpose, sealed },
		"purpose": func() (string, string, []byte) { return loginBridgeTestTransactionID, loginRedirectPurpose, sealed },
		"ciphertext": func() (string, string, []byte) {
			changed := append([]byte(nil), sealed...)
			changed[len(changed)-1] ^= 1
			return loginBridgeTestTransactionID, loginSecretsPurpose, changed
		},
	} {
		t.Run(name, func(t *testing.T) {
			scope, purpose, ciphertext := mutate()
			if _, err := sealer.Unseal(scope, purpose, ciphertext); err == nil {
				t.Fatal("tampered sealed value was accepted")
			}
		})
	}
}

func newLoginBridgeFixture(t *testing.T) (*LoginBridge, *memoryLoginBridgeStore, *recordingHydraAdmin, *recordingExternalOIDC) {
	t.Helper()
	store := &memoryLoginBridgeStore{memberships: []coredb.UserOAuthMembership{{
		WorkspaceID: loginBridgeTestWorkspaceID, Role: "owner", Generation: 1,
	}}}
	hydra := &recordingHydraAdmin{loginRequest: HydraLoginRequest{
		Challenge: "login-challenge", Client: HydraOAuth2Client{ClientID: corecontract.BrowserOAuthClientID},
		RequestedScope: corecontract.BrowserOAuthScopes(), RequestedAccessTokenAudience: []string{corecontract.BrowserOAuthAudience},
		RequestURL: browserAuthorizationRequestURL(loginBridgeTestWorkspaceID),
	}}
	provider := &recordingExternalOIDC{identity: ExternalOIDCIdentity{Issuer: "https://idp.example", Subject: "external-user"}}
	sealer, err := NewLoginTransactionSealer(bytes.Repeat([]byte{0x29}, 32))
	if err != nil {
		t.Fatal(err)
	}
	randomBytes := make([]byte, 128)
	for index := range randomBytes {
		randomBytes[index] = byte(index + 1)
	}
	bridge, err := NewLoginBridge(LoginBridgeConfig{
		Store: store, Hydra: hydra, IdentityProvider: provider, Sealer: sealer,
		OAuthProfiles: []LoginBridgeOAuthProfile{
			{Authority: corecontract.UserOAuthPlatformAuthority, ClientID: corecontract.PlatformOAuthClientID, Scopes: corecontract.PlatformOAuthScopes(), Audience: []string{corecontract.PlatformOAuthAudience}},
			{Authority: corecontract.UserOAuthBrowserAuthority, ClientID: corecontract.BrowserOAuthClientID, Scopes: corecontract.BrowserOAuthScopes(), Audience: []string{corecontract.BrowserOAuthAudience}},
		},
		HydraPublicOrigin: "https://browser.example",
		TransactionTTL:    5 * time.Minute, Random: bytes.NewReader(randomBytes),
		NewID: func() (string, error) { return loginBridgeTestTransactionID, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return bridge, store, hydra, provider
}

type recordingExternalOIDC struct {
	state, nonce, verifier string
	identity               ExternalOIDCIdentity
	exchangeErr            error
	exchangeCalls          int
}

func (provider *recordingExternalOIDC) Issuer() string { return "https://idp.example" }

func (provider *recordingExternalOIDC) AuthorizationURL(state, nonce, verifier string) (string, error) {
	provider.state, provider.nonce, provider.verifier = state, nonce, verifier
	query := url.Values{"state": {state}, "nonce": {nonce}, "code_challenge": {sha256Text(verifier)}}
	return "https://idp.example/authorize?" + query.Encode(), nil
}

func (provider *recordingExternalOIDC) Exchange(_ context.Context, code, verifier, nonce string) (ExternalOIDCIdentity, error) {
	provider.exchangeCalls++
	if code != "external-code" || verifier != provider.verifier || nonce != provider.nonce {
		return ExternalOIDCIdentity{}, errors.New("exchange correlation mismatch")
	}
	return provider.identity, provider.exchangeErr
}

type recordingHydraAdmin struct {
	loginRequest             HydraLoginRequest
	consentRequest           HydraConsentRequest
	acceptLoginCalls         int
	rejectLoginCalls         int
	acceptConsentCalls       int
	rejectConsentCalls       int
	acceptedSubject          string
	acceptedConsentScopes    []string
	acceptedConsentAudience  []string
	acceptedConsentAuthority corecontract.UserOAuthAuthority
}

func (hydra *recordingHydraAdmin) GetLoginRequest(_ context.Context, challenge string) (HydraLoginRequest, error) {
	if challenge != hydra.loginRequest.Challenge {
		return HydraLoginRequest{}, errors.New("unknown login challenge")
	}
	return hydra.loginRequest, nil
}

func (hydra *recordingHydraAdmin) AcceptLoginRequest(_ context.Context, challenge, subject string) (HydraRedirect, error) {
	if challenge != hydra.loginRequest.Challenge {
		return HydraRedirect{}, errors.New("unknown login challenge")
	}
	hydra.acceptLoginCalls++
	hydra.acceptedSubject = subject
	return HydraRedirect{RedirectTo: hydraTestContinuationURL(hydra.loginRequest.RequestURL, hydraLoginVerifierQuery, "accepted")}, nil
}

func (hydra *recordingHydraAdmin) RejectLoginRequest(_ context.Context, challenge, _, _ string) (HydraRedirect, error) {
	if challenge != hydra.loginRequest.Challenge {
		return HydraRedirect{}, errors.New("unknown login challenge")
	}
	hydra.rejectLoginCalls++
	return HydraRedirect{RedirectTo: hydraTestContinuationURL(hydra.loginRequest.RequestURL, hydraLoginVerifierQuery, "rejected")}, nil
}

func (hydra *recordingHydraAdmin) GetConsentRequest(_ context.Context, challenge string) (HydraConsentRequest, error) {
	if challenge != hydra.consentRequest.Challenge {
		return HydraConsentRequest{}, errors.New("unknown consent challenge")
	}
	return hydra.consentRequest, nil
}

func (hydra *recordingHydraAdmin) AcceptConsentRequest(_ context.Context, challenge string, grant HydraConsentGrant) (HydraRedirect, error) {
	if challenge != hydra.consentRequest.Challenge {
		return HydraRedirect{}, errors.New("invalid consent acceptance")
	}
	hydra.acceptConsentCalls++
	hydra.acceptedConsentScopes = append([]string(nil), grant.Scope...)
	hydra.acceptedConsentAudience = append([]string(nil), grant.Audience...)
	hydra.acceptedConsentAuthority = grant.Authority
	return HydraRedirect{RedirectTo: hydraTestContinuationURL(hydra.consentRequest.RequestURL, hydraConsentVerifierQuery, "accepted")}, nil
}

func (hydra *recordingHydraAdmin) RejectConsentRequest(_ context.Context, challenge, _, _ string) (HydraRedirect, error) {
	if challenge != hydra.consentRequest.Challenge {
		return HydraRedirect{}, errors.New("unknown consent challenge")
	}
	hydra.rejectConsentCalls++
	return HydraRedirect{RedirectTo: hydraTestContinuationURL(hydra.consentRequest.RequestURL, hydraConsentVerifierQuery, "rejected")}, nil
}

type memoryLoginBridgeStore struct {
	login       coredb.OIDCLoginTransaction
	consent     coredb.HydraConsentTransaction
	memberships []coredb.UserOAuthMembership
}

func (store *memoryLoginBridgeStore) CreateOIDCLoginTransaction(_ context.Context, command coredb.CreateOIDCLoginTransactionCommand) (coredb.OIDCLoginTransaction, error) {
	if store.login.ID != "" {
		return coredb.OIDCLoginTransaction{}, loginBridgeStateError(coredb.ErrorIdempotencyConflict)
	}
	now := time.Now().UTC()
	store.login = coredb.OIDCLoginTransaction{
		ID: command.ID, LoginChallengeSHA256: command.LoginChallengeSHA256,
		OIDCStateSHA256: command.OIDCStateSHA256, BrowserBindingSHA256: command.BrowserBindingSHA256,
		SealedSecrets: append([]byte(nil), command.SealedSecrets...), OIDCIssuer: command.OIDCIssuer,
		HydraClientID: command.HydraClientID, Status: coredb.OIDCLoginStatusPending,
		ExpiresAt: now.Add(command.TTL), Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	return store.login, nil
}

func (store *memoryLoginBridgeStore) ResumeOIDCLoginTransaction(_ context.Context, challenge, binding [32]byte) (coredb.OIDCLoginTransaction, error) {
	if store.login.Status != coredb.OIDCLoginStatusPending || store.login.LoginChallengeSHA256 != challenge || store.login.BrowserBindingSHA256 != binding {
		return coredb.OIDCLoginTransaction{}, loginBridgeStateError(coredb.ErrorNotFound)
	}
	return store.login, nil
}

func (store *memoryLoginBridgeStore) ClaimOIDCLoginCallback(_ context.Context, command coredb.ClaimOIDCLoginCallbackCommand) (coredb.OIDCLoginTransaction, error) {
	if store.login.OIDCStateSHA256 != command.OIDCStateSHA256 {
		return coredb.OIDCLoginTransaction{}, loginBridgeStateError(coredb.ErrorNotFound)
	}
	if store.login.BrowserBindingSHA256 != command.BrowserBindingSHA256 {
		return coredb.OIDCLoginTransaction{}, loginBridgeStateError(coredb.ErrorForbidden)
	}
	if store.login.Status != coredb.OIDCLoginStatusPending {
		return coredb.OIDCLoginTransaction{}, loginBridgeStateError(coredb.ErrorIdempotencyConflict)
	}
	now := time.Now().UTC()
	store.login.Status = coredb.OIDCLoginStatusCallbackClaimed
	store.login.CallbackClaimedAt = &now
	store.login.Version++
	return store.login, nil
}

func (store *memoryLoginBridgeStore) AuthenticateOIDCLogin(_ context.Context, command coredb.AuthenticateOIDCLoginCommand) (coredb.OIDCLoginTransaction, error) {
	if store.login.ID != command.TransactionID || store.login.Status != coredb.OIDCLoginStatusCallbackClaimed {
		return coredb.OIDCLoginTransaction{}, loginBridgeStateError(coredb.ErrorInvalidState)
	}
	now := time.Now().UTC()
	if command.OIDCIssuer != "https://idp.example" || command.Subject != "external-user" {
		store.login.Status = coredb.OIDCLoginStatusFailed
		store.login.FailureCode = "identity_not_mapped"
		store.login.CompletedAt = &now
		return store.login, nil
	}
	store.login.Status = coredb.OIDCLoginStatusAuthenticated
	store.login.UserID = loginBridgeTestUserID
	store.login.AuthenticatedAt = &now
	store.login.Version++
	return store.login, nil
}

func (store *memoryLoginBridgeStore) BeginOIDCLoginAcceptance(_ context.Context, transactionID string) (coredb.OIDCLoginTransaction, error) {
	if store.login.ID != transactionID || store.login.Status != coredb.OIDCLoginStatusAuthenticated {
		return coredb.OIDCLoginTransaction{}, loginBridgeStateError(coredb.ErrorInvalidState)
	}
	store.login.Status = coredb.OIDCLoginStatusAccepting
	store.login.Version++
	return store.login, nil
}

func (store *memoryLoginBridgeStore) CompleteOIDCLogin(_ context.Context, command coredb.CompleteOIDCLoginCommand) (coredb.OIDCLoginTransaction, error) {
	if store.login.ID != command.TransactionID || store.login.Status != coredb.OIDCLoginStatusAccepting {
		return coredb.OIDCLoginTransaction{}, loginBridgeStateError(coredb.ErrorInvalidState)
	}
	now := time.Now().UTC()
	store.login.Status = coredb.OIDCLoginStatusAccepted
	store.login.SealedRedirect = append([]byte(nil), command.SealedRedirect...)
	store.login.CompletedAt = &now
	store.login.Version++
	return store.login, nil
}

func (store *memoryLoginBridgeStore) FailOIDCLogin(_ context.Context, command coredb.FailOIDCLoginCommand) (coredb.OIDCLoginTransaction, error) {
	now := time.Now().UTC()
	store.login.Status, store.login.FailureCode, store.login.CompletedAt = command.Status, command.FailureCode, &now
	store.login.Version++
	return store.login, nil
}

func (store *memoryLoginBridgeStore) RequireActiveUser(_ context.Context, userID string) error {
	if userID != loginBridgeTestUserID {
		return loginBridgeStateError(coredb.ErrorForbidden)
	}
	return nil
}

func (store *memoryLoginBridgeStore) ResolveUserOAuthMemberships(_ context.Context, command coredb.ResolveUserOAuthMembershipsCommand) ([]coredb.UserOAuthMembership, error) {
	if command.UserID != loginBridgeTestUserID {
		return nil, loginBridgeStateError(coredb.ErrorForbidden)
	}
	result := make([]coredb.UserOAuthMembership, 0, len(store.memberships))
	for _, membership := range store.memberships {
		if command.WorkspaceID == "" || command.WorkspaceID == membership.WorkspaceID {
			result = append(result, membership)
		}
	}
	if len(result) > command.Limit {
		return nil, loginBridgeStateError(coredb.ErrorConflict)
	}
	return result, nil
}

func (store *memoryLoginBridgeStore) CreateHydraConsentTransaction(_ context.Context, command coredb.CreateHydraConsentTransactionCommand) (coredb.HydraConsentTransaction, error) {
	if store.consent.Status != "" {
		return coredb.HydraConsentTransaction{}, loginBridgeStateError(coredb.ErrorIdempotencyConflict)
	}
	now := time.Now().UTC()
	store.consent = coredb.HydraConsentTransaction{
		ConsentChallengeSHA256: command.ConsentChallengeSHA256, RequestSHA256: command.RequestSHA256,
		HydraClientID: command.HydraClientID, UserID: command.UserID, Status: coredb.HydraConsentStatusAccepting,
		ExpiresAt: now.Add(command.TTL), Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	return store.consent, nil
}

func (store *memoryLoginBridgeStore) CompleteHydraConsent(_ context.Context, command coredb.CompleteHydraConsentCommand) (coredb.HydraConsentTransaction, error) {
	now := time.Now().UTC()
	store.consent.Status = coredb.HydraConsentStatusAccepted
	store.consent.SealedRedirect = append([]byte(nil), command.SealedRedirect...)
	store.consent.CompletedAt = &now
	store.consent.Version++
	return store.consent, nil
}

func (store *memoryLoginBridgeStore) FailHydraConsent(_ context.Context, command coredb.FailHydraConsentCommand) (coredb.HydraConsentTransaction, error) {
	now := time.Now().UTC()
	store.consent.Status, store.consent.FailureCode, store.consent.CompletedAt = command.Status, command.FailureCode, &now
	store.consent.Version++
	return store.consent, nil
}

func loginBridgeStateError(code coredb.StateErrorCode) error {
	return &coredb.StateError{Code: code, Operation: "login bridge test"}
}

func sha256Text(value string) string {
	digest := sha256.Sum256([]byte(value))
	return string(digest[:])
}

func browserAuthorizationRequestURL(workspaceID string) string {
	return testAuthorizationRequestURL(
		corecontract.BrowserOAuthClientID,
		corecontract.BrowserOAuthAudience,
		corecontract.BrowserOAuthScopes(),
		corecontract.UserOAuthWorkspaceURNPrefix+workspaceID,
	)
}

func platformAuthorizationRequestURL() string {
	return testAuthorizationRequestURL(
		corecontract.PlatformOAuthClientID,
		corecontract.PlatformOAuthAudience,
		corecontract.PlatformOAuthScopes(),
		"",
	)
}

func testAuthorizationRequestURL(clientID, audience string, scopes []string, resource string) string {
	query := url.Values{
		"audience":              {audience},
		"client_id":             {clientID},
		"code_challenge":        {strings.Repeat("c", 43)},
		"code_challenge_method": {"S256"},
		"nonce":                 {strings.Repeat("n", 43)},
		"redirect_uri":          {"https://browser-client.example/"},
		"response_type":         {"code"},
		"scope":                 {strings.Join(scopes, " ")},
		"state":                 {strings.Repeat("s", 43)},
	}
	if resource != "" {
		query.Set("resource", resource)
	}
	return "https://browser.example/oauth2/auth?" + query.Encode()
}

func hydraTestContinuationURL(requestURL, verifierQuery, verifier string) string {
	parsed, err := url.Parse(requestURL)
	if err != nil {
		panic(err)
	}
	parsed.Scheme = "https"
	parsed.Host = "browser.example"
	query := parsed.Query()
	query.Set(verifierQuery, verifier)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
