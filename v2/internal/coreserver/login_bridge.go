package coreserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

const (
	defaultLoginTransactionTTL = 5 * time.Minute
	loginSecretsPurpose        = "oidc-secrets"
	loginRedirectPurpose       = "hydra-login-redirect"
	consentRedirectPurpose     = "hydra-consent-redirect"
	hydraLoginVerifierQuery    = "login_verifier"
	hydraConsentVerifierQuery  = "consent_verifier"
)

var (
	defaultBrowserOAuthScopes = corecontract.BrowserOAuthScopes()
	defaultBrowserAudience    = []string{corecontract.BrowserOAuthAudience}
	hydraAuthorizationQuery   = []string{
		"audience", "client_id", "code_challenge", "code_challenge_method", "nonce",
		"redirect_uri", "response_type", "scope", "state",
	}
)

// LoginBridgeOAuthProfile is one closed Hydra public-client authority accepted
// by the login bridge. Client ID, scopes, and audience are matched as a unit;
// authority from two profiles can never be combined in one authorization.
type LoginBridgeOAuthProfile struct {
	Authority string
	ClientID  string
	Scopes    []string
	Audience  []string
}

type LoginBridgeStore interface {
	CreateOIDCLoginTransaction(context.Context, coredb.CreateOIDCLoginTransactionCommand) (coredb.OIDCLoginTransaction, error)
	ResumeOIDCLoginTransaction(context.Context, [32]byte, [32]byte) (coredb.OIDCLoginTransaction, error)
	ClaimOIDCLoginCallback(context.Context, coredb.ClaimOIDCLoginCallbackCommand) (coredb.OIDCLoginTransaction, error)
	AuthenticateOIDCLogin(context.Context, coredb.AuthenticateOIDCLoginCommand) (coredb.OIDCLoginTransaction, error)
	BeginOIDCLoginAcceptance(context.Context, string) (coredb.OIDCLoginTransaction, error)
	CompleteOIDCLogin(context.Context, coredb.CompleteOIDCLoginCommand) (coredb.OIDCLoginTransaction, error)
	FailOIDCLogin(context.Context, coredb.FailOIDCLoginCommand) (coredb.OIDCLoginTransaction, error)
	RequireActiveUser(context.Context, string) error
	CreateHydraConsentTransaction(context.Context, coredb.CreateHydraConsentTransactionCommand) (coredb.HydraConsentTransaction, error)
	CompleteHydraConsent(context.Context, coredb.CompleteHydraConsentCommand) (coredb.HydraConsentTransaction, error)
	FailHydraConsent(context.Context, coredb.FailHydraConsentCommand) (coredb.HydraConsentTransaction, error)
	ResolveUserOAuthMemberships(context.Context, coredb.ResolveUserOAuthMembershipsCommand) ([]coredb.UserOAuthMembership, error)
}

type LoginBridgeConfig struct {
	Store             LoginBridgeStore
	Hydra             HydraAdminAPI
	IdentityProvider  ExternalOIDCProvider
	Sealer            *LoginTransactionSealer
	OAuthProfiles     []LoginBridgeOAuthProfile
	HydraPublicOrigin string
	TransactionTTL    time.Duration
	NewID             func() (string, error)
	Random            io.Reader
}

type LoginBridge struct {
	store             LoginBridgeStore
	hydra             HydraAdminAPI
	identityProvider  ExternalOIDCProvider
	sealer            *LoginTransactionSealer
	oauthProfiles     map[string]LoginBridgeOAuthProfile
	hydraPublicOrigin string
	transactionTTL    time.Duration
	newID             func() (string, error)
	random            io.Reader
}

type BeginLoginResult struct {
	RedirectTo     string
	BrowserBinding string
	ExpiresAt      time.Time
	External       bool
}

type CallbackResult struct {
	RedirectTo   string
	ClearBinding bool
}

type ConsentResult struct {
	RedirectTo string
}

type oidcLoginSecrets struct {
	LoginChallenge string `json:"loginChallenge"`
	State          string `json:"state"`
	Nonce          string `json:"nonce"`
	PKCEVerifier   string `json:"pkceVerifier"`
	BrowserBinding string `json:"browserBinding"`
}

func NewLoginBridge(config LoginBridgeConfig) (*LoginBridge, error) {
	if config.Store == nil || config.Hydra == nil || config.IdentityProvider == nil || config.Sealer == nil {
		return nil, errors.New("login bridge store, Hydra Admin API, external OIDC provider, and sealer are required")
	}
	profiles, err := validateLoginBridgeOAuthProfiles(config.OAuthProfiles)
	if err != nil {
		return nil, err
	}
	canonicalOrigin, err := validateHydraPublicOrigin(config.HydraPublicOrigin)
	if err != nil {
		return nil, err
	}
	if config.TransactionTTL == 0 {
		config.TransactionTTL = defaultLoginTransactionTTL
	}
	if config.TransactionTTL < time.Minute || config.TransactionTTL > coredb.MaxOIDCLoginTransactionTTL || config.TransactionTTL%time.Millisecond != 0 {
		return nil, errors.New("login bridge transaction TTL must be a whole number of milliseconds between one and ten minutes")
	}
	if config.NewID == nil {
		config.NewID = newCoreUUID
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.IdentityProvider.Issuer() == "" {
		return nil, errors.New("login bridge external OIDC issuer is required")
	}
	return &LoginBridge{
		store: config.Store, hydra: config.Hydra, identityProvider: config.IdentityProvider, sealer: config.Sealer,
		oauthProfiles: profiles, hydraPublicOrigin: canonicalOrigin,
		transactionTTL: config.TransactionTTL, newID: config.NewID, random: config.Random,
	}, nil
}

func (bridge *LoginBridge) BeginLogin(ctx context.Context, challenge, browserBinding string) (BeginLoginResult, error) {
	if bridge == nil {
		return BeginLoginResult{}, errors.New("login bridge is not initialized")
	}
	if err := validateHydraChallenge(challenge); err != nil {
		return BeginLoginResult{}, err
	}
	loginRequest, err := bridge.hydra.GetLoginRequest(ctx, challenge)
	if err != nil {
		return BeginLoginResult{}, err
	}
	if loginRequest.Challenge != challenge {
		return BeginLoginResult{}, errors.New("Hydra login request does not match the requested challenge")
	}
	profile, err := bridge.oauthProfileForAuthorizationRequest(
		loginRequest.Client.ClientID,
		loginRequest.RequestedScope,
		loginRequest.RequestedAccessTokenAudience,
	)
	if err != nil {
		return bridge.rejectUntrackedLogin(ctx, challenge, "invalid_request", "authorization request is not allowed")
	}
	if _, err := userOAuthWorkspaceResource(profile.Authority, loginRequest.RequestURL); err != nil {
		return bridge.rejectUntrackedLogin(ctx, challenge, "invalid_target", "authorization resource is not allowed")
	}
	if loginRequest.Skip {
		if !canonicalPublicUUID(loginRequest.Subject) {
			return bridge.rejectUntrackedLogin(ctx, challenge, "login_required", "remembered login subject is invalid")
		}
		if err := bridge.store.RequireActiveUser(ctx, loginRequest.Subject); err != nil {
			return bridge.rejectUntrackedLogin(ctx, challenge, "access_denied", "remembered login subject is inactive")
		}
		redirect, err := bridge.hydra.AcceptLoginRequest(ctx, challenge, loginRequest.Subject)
		if err != nil {
			return BeginLoginResult{}, err
		}
		if err := bridge.validateHydraRedirect(redirect.RedirectTo, hydraLoginVerifierQuery); err != nil {
			return BeginLoginResult{}, err
		}
		return BeginLoginResult{RedirectTo: redirect.RedirectTo}, nil
	}

	challengeHash := sha256.Sum256([]byte(challenge))
	if validateOIDCSecret("browser binding", browserBinding) == nil {
		bindingHash := sha256.Sum256([]byte(browserBinding))
		resumed, resumeErr := bridge.store.ResumeOIDCLoginTransaction(ctx, challengeHash, bindingHash)
		if resumeErr == nil {
			if resumed.HydraClientID != profile.ClientID {
				return BeginLoginResult{}, errors.New("resumed login transaction belongs to a different OAuth client")
			}
			secrets, err := bridge.openLoginSecrets(resumed)
			if err != nil {
				return BeginLoginResult{}, err
			}
			if secrets.LoginChallenge != challenge || !constantTimeTextEqual(secrets.BrowserBinding, browserBinding) {
				return BeginLoginResult{}, errors.New("resumed login transaction does not match its sealed authority")
			}
			authorizationURL, err := bridge.identityProvider.AuthorizationURL(secrets.State, secrets.Nonce, secrets.PKCEVerifier)
			if err != nil {
				return BeginLoginResult{}, err
			}
			return BeginLoginResult{
				RedirectTo: authorizationURL, BrowserBinding: secrets.BrowserBinding,
				ExpiresAt: resumed.ExpiresAt, External: true,
			}, nil
		}
		if !coredb.HasStateErrorCode(resumeErr, coredb.ErrorNotFound) {
			return BeginLoginResult{}, resumeErr
		}
	}

	transactionID, err := bridge.newID()
	if err != nil || !canonicalPublicUUID(transactionID) {
		return BeginLoginResult{}, errors.New("login bridge could not allocate a canonical transaction identity")
	}
	secrets := oidcLoginSecrets{LoginChallenge: challenge}
	for _, secret := range []struct {
		destination *string
		name        string
	}{
		{destination: &secrets.State, name: "state"},
		{destination: &secrets.Nonce, name: "nonce"},
		{destination: &secrets.PKCEVerifier, name: "PKCE verifier"},
		{destination: &secrets.BrowserBinding, name: "browser binding"},
	} {
		value, err := bridge.randomSecret(secret.name)
		if err != nil {
			return BeginLoginResult{}, err
		}
		*secret.destination = value
	}
	authorizationURL, err := bridge.identityProvider.AuthorizationURL(secrets.State, secrets.Nonce, secrets.PKCEVerifier)
	if err != nil {
		return BeginLoginResult{}, err
	}
	sealedSecrets, err := bridge.sealLoginSecrets(transactionID, secrets)
	if err != nil {
		return BeginLoginResult{}, err
	}
	created, err := bridge.store.CreateOIDCLoginTransaction(ctx, coredb.CreateOIDCLoginTransactionCommand{
		ID: transactionID, LoginChallengeSHA256: challengeHash,
		OIDCStateSHA256:      sha256.Sum256([]byte(secrets.State)),
		BrowserBindingSHA256: sha256.Sum256([]byte(secrets.BrowserBinding)),
		SealedSecrets:        sealedSecrets, OIDCIssuer: bridge.identityProvider.Issuer(),
		HydraClientID: profile.ClientID, TTL: bridge.transactionTTL,
	})
	if err != nil {
		return BeginLoginResult{}, err
	}
	if created.Status != coredb.OIDCLoginStatusPending {
		return BeginLoginResult{}, errors.New("login store returned a non-pending transaction after creation")
	}
	return BeginLoginResult{
		RedirectTo: authorizationURL, BrowserBinding: secrets.BrowserBinding,
		ExpiresAt: created.ExpiresAt, External: true,
	}, nil
}

func (bridge *LoginBridge) CompleteCallback(
	ctx context.Context,
	state, code, providerError, browserBinding string,
) (CallbackResult, error) {
	if bridge == nil {
		return CallbackResult{}, errors.New("login bridge is not initialized")
	}
	if err := validateOIDCSecret("state", state); err != nil {
		return CallbackResult{}, err
	}
	if err := validateOIDCSecret("browser binding", browserBinding); err != nil {
		return CallbackResult{}, err
	}
	if (code == "") == (providerError == "") {
		return CallbackResult{}, errors.New("OIDC callback must contain exactly one authorization code or provider error")
	}
	if code != "" && (len(code) > 8192 || strings.ContainsAny(code, "\x00\r\n")) {
		return CallbackResult{}, errors.New("OIDC callback code is outside protocol bounds")
	}
	if providerError != "" && (len(providerError) > 128 || strings.ContainsAny(providerError, "\x00\r\n")) {
		return CallbackResult{}, errors.New("OIDC callback error is outside protocol bounds")
	}
	claimed, err := bridge.store.ClaimOIDCLoginCallback(ctx, coredb.ClaimOIDCLoginCallbackCommand{
		OIDCStateSHA256:      sha256.Sum256([]byte(state)),
		BrowserBindingSHA256: sha256.Sum256([]byte(browserBinding)),
	})
	if err != nil {
		return CallbackResult{}, err
	}
	secrets, err := bridge.openLoginSecrets(claimed)
	if err != nil {
		return CallbackResult{}, err
	}
	if !constantTimeTextEqual(secrets.State, state) || !constantTimeTextEqual(secrets.BrowserBinding, browserBinding) {
		return CallbackResult{}, errors.New("OIDC callback does not match sealed login authority")
	}
	if claimed.Status == coredb.OIDCLoginStatusExpired {
		return bridge.rejectClaimedLogin(ctx, claimed, secrets, coredb.OIDCLoginStatusExpired, "login_transaction_expired", "login_required", "login transaction expired", false)
	}
	if claimed.Status != coredb.OIDCLoginStatusCallbackClaimed {
		return CallbackResult{}, errors.New("OIDC callback claim returned an invalid transaction status")
	}
	if providerError != "" {
		return bridge.rejectClaimedLogin(ctx, claimed, secrets, coredb.OIDCLoginStatusRejected, "identity_provider_denied", "access_denied", "external identity provider denied login", true)
	}

	identity, err := bridge.identityProvider.Exchange(ctx, code, secrets.PKCEVerifier, secrets.Nonce)
	if err != nil {
		return bridge.rejectClaimedLogin(ctx, claimed, secrets, coredb.OIDCLoginStatusFailed, "identity_exchange_failed", "access_denied", "external identity verification failed", true)
	}
	authenticated, err := bridge.store.AuthenticateOIDCLogin(ctx, coredb.AuthenticateOIDCLoginCommand{
		TransactionID: claimed.ID, OIDCIssuer: identity.Issuer, Subject: identity.Subject,
	})
	if err != nil {
		return CallbackResult{}, err
	}
	if authenticated.Status == coredb.OIDCLoginStatusFailed && authenticated.FailureCode == "identity_not_mapped" {
		redirect, rejectErr := bridge.hydra.RejectLoginRequest(ctx, secrets.LoginChallenge, "access_denied", "external identity is not provisioned")
		if rejectErr != nil {
			return CallbackResult{}, rejectErr
		}
		if err := bridge.validateHydraRedirect(redirect.RedirectTo, hydraLoginVerifierQuery); err != nil {
			return CallbackResult{}, err
		}
		return CallbackResult{RedirectTo: redirect.RedirectTo, ClearBinding: true}, nil
	}
	if authenticated.Status != coredb.OIDCLoginStatusAuthenticated || !canonicalPublicUUID(authenticated.UserID) {
		return CallbackResult{}, errors.New("identity mapping returned an invalid local user authority")
	}
	accepting, err := bridge.store.BeginOIDCLoginAcceptance(ctx, authenticated.ID)
	if err != nil {
		return CallbackResult{}, err
	}
	redirect, err := bridge.hydra.AcceptLoginRequest(ctx, secrets.LoginChallenge, accepting.UserID)
	if err != nil {
		_, _ = bridge.store.FailOIDCLogin(ctx, coredb.FailOIDCLoginCommand{
			TransactionID: accepting.ID, Status: coredb.OIDCLoginStatusFailed, FailureCode: "hydra_login_accept_failed",
		})
		return CallbackResult{}, err
	}
	if err := bridge.validateHydraRedirect(redirect.RedirectTo, hydraLoginVerifierQuery); err != nil {
		_, _ = bridge.store.FailOIDCLogin(ctx, coredb.FailOIDCLoginCommand{
			TransactionID: accepting.ID, Status: coredb.OIDCLoginStatusFailed, FailureCode: "hydra_login_redirect_invalid",
		})
		return CallbackResult{}, err
	}
	sealedRedirect, err := bridge.sealer.Seal(accepting.ID, loginRedirectPurpose, []byte(redirect.RedirectTo))
	if err != nil {
		return CallbackResult{}, err
	}
	completed, err := bridge.store.CompleteOIDCLogin(ctx, coredb.CompleteOIDCLoginCommand{
		TransactionID: accepting.ID, SealedRedirect: sealedRedirect,
	})
	if err != nil {
		return CallbackResult{}, err
	}
	if completed.Status != coredb.OIDCLoginStatusAccepted {
		return CallbackResult{}, errors.New("login store did not commit accepted Hydra redirect")
	}
	return CallbackResult{RedirectTo: redirect.RedirectTo, ClearBinding: true}, nil
}

func (bridge *LoginBridge) Consent(ctx context.Context, challenge string) (ConsentResult, error) {
	if bridge == nil {
		return ConsentResult{}, errors.New("login bridge is not initialized")
	}
	if err := validateHydraChallenge(challenge); err != nil {
		return ConsentResult{}, err
	}
	request, err := bridge.hydra.GetConsentRequest(ctx, challenge)
	if err != nil {
		return ConsentResult{}, err
	}
	if request.Challenge != challenge {
		return ConsentResult{}, errors.New("Hydra consent request does not match the requested challenge")
	}
	if !canonicalPublicUUID(request.Subject) {
		return bridge.rejectUntrackedConsent(ctx, challenge, "access_denied", "consent subject is invalid")
	}
	profile, err := bridge.oauthProfileForAuthorizationRequest(
		request.Client.ClientID,
		request.RequestedScope,
		request.RequestedAccessTokenAudience,
	)
	if err != nil {
		return bridge.rejectUntrackedConsent(ctx, challenge, "invalid_scope", "requested authority is not allowed")
	}
	workspaceID, err := userOAuthWorkspaceResource(profile.Authority, request.RequestURL)
	if err != nil {
		return bridge.rejectUntrackedConsent(ctx, challenge, "invalid_target", "authorization resource is not allowed")
	}
	membershipLimit := 256
	if profile.Authority == corecontract.UserOAuthBrowserAuthority {
		membershipLimit = 1
	}
	memberships, err := bridge.store.ResolveUserOAuthMemberships(ctx, coredb.ResolveUserOAuthMembershipsCommand{
		UserID: request.Subject, WorkspaceID: workspaceID, Limit: membershipLimit,
	})
	if err != nil {
		if coredb.HasStateErrorCode(err, coredb.ErrorForbidden) || coredb.HasStateErrorCode(err, coredb.ErrorNotFound) {
			return bridge.rejectUntrackedConsent(ctx, challenge, "access_denied", "consent subject has no active resource authority")
		}
		return ConsentResult{}, err
	}
	grant, err := compileUserOAuthConsentGrant(profile, request.RequestedScope, workspaceID, memberships)
	if err != nil {
		return bridge.rejectUntrackedConsent(ctx, challenge, "access_denied", "consent subject has no requested resource authority")
	}
	challengeHash := sha256.Sum256([]byte(challenge))
	requestHash, err := consentRequestHash(request, grant)
	if err != nil {
		return ConsentResult{}, err
	}
	created, err := bridge.store.CreateHydraConsentTransaction(ctx, coredb.CreateHydraConsentTransactionCommand{
		ConsentChallengeSHA256: challengeHash, RequestSHA256: requestHash,
		HydraClientID: profile.ClientID, UserID: request.Subject, TTL: bridge.transactionTTL,
	})
	if err != nil {
		return ConsentResult{}, err
	}
	redirect, err := bridge.hydra.AcceptConsentRequest(ctx, challenge, grant)
	if err != nil {
		_, _ = bridge.store.FailHydraConsent(ctx, coredb.FailHydraConsentCommand{
			ConsentChallengeSHA256: challengeHash, Status: coredb.HydraConsentStatusFailed, FailureCode: "hydra_consent_accept_failed",
		})
		return ConsentResult{}, err
	}
	if err := bridge.validateHydraRedirect(redirect.RedirectTo, hydraConsentVerifierQuery); err != nil {
		_, _ = bridge.store.FailHydraConsent(ctx, coredb.FailHydraConsentCommand{
			ConsentChallengeSHA256: challengeHash, Status: coredb.HydraConsentStatusFailed, FailureCode: "hydra_consent_redirect_invalid",
		})
		return ConsentResult{}, err
	}
	scope := hex.EncodeToString(challengeHash[:])
	sealedRedirect, err := bridge.sealer.Seal(scope, consentRedirectPurpose, []byte(redirect.RedirectTo))
	if err != nil {
		return ConsentResult{}, err
	}
	completed, err := bridge.store.CompleteHydraConsent(ctx, coredb.CompleteHydraConsentCommand{
		ConsentChallengeSHA256: challengeHash, SealedRedirect: sealedRedirect,
	})
	if err != nil {
		return ConsentResult{}, err
	}
	if created.Status != coredb.HydraConsentStatusAccepting || completed.Status != coredb.HydraConsentStatusAccepted {
		return ConsentResult{}, errors.New("consent store returned an invalid transaction transition")
	}
	return ConsentResult{RedirectTo: redirect.RedirectTo}, nil
}

func (bridge *LoginBridge) rejectClaimedLogin(
	ctx context.Context,
	transaction coredb.OIDCLoginTransaction,
	secrets oidcLoginSecrets,
	status, failureCode, oauthCode, description string,
	commitFailure bool,
) (CallbackResult, error) {
	redirect, err := bridge.hydra.RejectLoginRequest(ctx, secrets.LoginChallenge, oauthCode, description)
	if err != nil {
		if commitFailure && !oidcLoginTerminalStatus(transaction.Status) {
			_, _ = bridge.store.FailOIDCLogin(ctx, coredb.FailOIDCLoginCommand{
				TransactionID: transaction.ID, Status: coredb.OIDCLoginStatusFailed, FailureCode: "hydra_login_reject_failed",
			})
		}
		return CallbackResult{}, err
	}
	if err := bridge.validateHydraRedirect(redirect.RedirectTo, hydraLoginVerifierQuery); err != nil {
		return CallbackResult{}, err
	}
	if commitFailure {
		if _, err := bridge.store.FailOIDCLogin(ctx, coredb.FailOIDCLoginCommand{
			TransactionID: transaction.ID, Status: status, FailureCode: failureCode,
		}); err != nil {
			return CallbackResult{}, err
		}
	}
	return CallbackResult{RedirectTo: redirect.RedirectTo, ClearBinding: true}, nil
}

func (bridge *LoginBridge) rejectUntrackedLogin(ctx context.Context, challenge, code, description string) (BeginLoginResult, error) {
	redirect, err := bridge.hydra.RejectLoginRequest(ctx, challenge, code, description)
	if err != nil {
		return BeginLoginResult{}, err
	}
	if err := bridge.validateHydraRedirect(redirect.RedirectTo, hydraLoginVerifierQuery); err != nil {
		return BeginLoginResult{}, err
	}
	return BeginLoginResult{RedirectTo: redirect.RedirectTo}, nil
}

func (bridge *LoginBridge) rejectUntrackedConsent(ctx context.Context, challenge, code, description string) (ConsentResult, error) {
	redirect, err := bridge.hydra.RejectConsentRequest(ctx, challenge, code, description)
	if err != nil {
		return ConsentResult{}, err
	}
	if err := bridge.validateHydraRedirect(redirect.RedirectTo, hydraConsentVerifierQuery); err != nil {
		return ConsentResult{}, err
	}
	return ConsentResult{RedirectTo: redirect.RedirectTo}, nil
}

func (bridge *LoginBridge) sealLoginSecrets(transactionID string, secrets oidcLoginSecrets) ([]byte, error) {
	raw, err := json.Marshal(secrets)
	if err != nil {
		return nil, fmt.Errorf("encode login transaction secrets: %w", err)
	}
	return bridge.sealer.Seal(transactionID, loginSecretsPurpose, raw)
}

func (bridge *LoginBridge) openLoginSecrets(transaction coredb.OIDCLoginTransaction) (oidcLoginSecrets, error) {
	raw, err := bridge.sealer.Unseal(transaction.ID, loginSecretsPurpose, transaction.SealedSecrets)
	if err != nil {
		return oidcLoginSecrets{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var secrets oidcLoginSecrets
	if err := decoder.Decode(&secrets); err != nil {
		return oidcLoginSecrets{}, errors.New("sealed login transaction secrets are invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return oidcLoginSecrets{}, errors.New("sealed login transaction secrets contain trailing data")
	}
	if validateHydraChallenge(secrets.LoginChallenge) != nil ||
		validateOIDCCorrelation(secrets.State, secrets.Nonce, secrets.PKCEVerifier) != nil ||
		validateOIDCSecret("browser binding", secrets.BrowserBinding) != nil {
		return oidcLoginSecrets{}, errors.New("sealed login transaction secrets are outside protocol bounds")
	}
	if sha256.Sum256([]byte(secrets.LoginChallenge)) != transaction.LoginChallengeSHA256 ||
		sha256.Sum256([]byte(secrets.State)) != transaction.OIDCStateSHA256 ||
		sha256.Sum256([]byte(secrets.BrowserBinding)) != transaction.BrowserBindingSHA256 ||
		transaction.OIDCIssuer != bridge.identityProvider.Issuer() {
		return oidcLoginSecrets{}, errors.New("sealed login transaction does not match its database correlation hashes")
	}
	if _, ok := bridge.oauthProfiles[transaction.HydraClientID]; !ok {
		return oidcLoginSecrets{}, errors.New("sealed login transaction belongs to an unsupported OAuth client")
	}
	return secrets, nil
}

func (bridge *LoginBridge) randomSecret(name string) (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(bridge.random, raw); err != nil {
		return "", fmt.Errorf("generate OIDC %s: %w", name, err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (bridge *LoginBridge) oauthProfileForAuthorizationRequest(
	clientID string,
	scopes, audience []string,
) (LoginBridgeOAuthProfile, error) {
	profile, ok := bridge.oauthProfiles[clientID]
	if !ok {
		return LoginBridgeOAuthProfile{}, errors.New("Hydra authorization request belongs to an unsupported client")
	}
	if !uniqueCanonicalTextSubset(scopes, profile.Scopes) || !slices.Contains(scopes, corecontract.OAuthOpenIDScope) {
		return LoginBridgeOAuthProfile{}, errors.New("Hydra authorization request contains an unsupported scope set")
	}
	if !sameUniqueTextSet(audience, profile.Audience) {
		return LoginBridgeOAuthProfile{}, errors.New("Hydra authorization request contains an unsupported audience set")
	}
	return profile, nil
}

func validateLoginBridgeOAuthProfiles(profiles []LoginBridgeOAuthProfile) (map[string]LoginBridgeOAuthProfile, error) {
	if len(profiles) != 2 {
		return nil, errors.New("login bridge requires exactly the Platform and Browser OAuth profiles")
	}
	validated := make(map[string]LoginBridgeOAuthProfile, len(profiles))
	authorities := make(map[string]struct{}, len(profiles))
	for _, profile := range profiles {
		if profile.ClientID == "" || len(profile.ClientID) > 512 || strings.TrimSpace(profile.ClientID) != profile.ClientID ||
			strings.ContainsAny(profile.ClientID, " \t\r\n\x00") {
			return nil, errors.New("login bridge OAuth client ID is empty or outside protocol bounds")
		}
		if _, exists := validated[profile.ClientID]; exists {
			return nil, errors.New("login bridge OAuth client IDs must be unique")
		}
		if _, exists := authorities[profile.Authority]; exists {
			return nil, errors.New("login bridge OAuth authorities must be unique")
		}
		var expectedClientID, expectedAudience string
		var expectedScopes []string
		switch profile.Authority {
		case corecontract.UserOAuthPlatformAuthority:
			expectedClientID, expectedAudience, expectedScopes = corecontract.PlatformOAuthClientID, corecontract.PlatformOAuthAudience, corecontract.PlatformOAuthScopes()
		case corecontract.UserOAuthBrowserAuthority:
			expectedClientID, expectedAudience, expectedScopes = corecontract.BrowserOAuthClientID, corecontract.BrowserOAuthAudience, corecontract.BrowserOAuthScopes()
		default:
			return nil, errors.New("login bridge OAuth authority is unsupported")
		}
		if len(profile.Scopes) == 0 || len(profile.Scopes) > 32 || !sameUniqueTextSet(profile.Scopes, profile.Scopes) ||
			!slices.Contains(profile.Scopes, corecontract.OAuthOpenIDScope) {
			return nil, errors.New("login bridge OAuth scopes must be a non-empty unique canonical set")
		}
		if len(profile.Audience) != 1 || !sameUniqueTextSet(profile.Audience, profile.Audience) {
			return nil, errors.New("login bridge OAuth profile must have exactly one canonical audience")
		}
		if profile.ClientID != expectedClientID || !sameUniqueTextSet(profile.Scopes, expectedScopes) ||
			!sameUniqueTextSet(profile.Audience, []string{expectedAudience}) {
			return nil, errors.New("login bridge OAuth profile does not match its closed authority contract")
		}
		clone := LoginBridgeOAuthProfile{
			Authority: profile.Authority,
			ClientID:  profile.ClientID,
			Scopes:    append([]string(nil), profile.Scopes...),
			Audience:  append([]string(nil), profile.Audience...),
		}
		validated[clone.ClientID] = clone
		authorities[clone.Authority] = struct{}{}
	}
	return validated, nil
}

func uniqueCanonicalTextSubset(actual, allowed []string) bool {
	if len(actual) == 0 || len(actual) > len(allowed) {
		return false
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		allowedSet[value] = struct{}{}
	}
	seen := make(map[string]struct{}, len(actual))
	for _, value := range actual {
		if value == "" || strings.ContainsAny(value, " \t\r\n\x00") {
			return false
		}
		if _, ok := allowedSet[value]; !ok {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func (bridge *LoginBridge) validateHydraRedirect(raw, verifierQuery string) error {
	if len(raw) > 8192 || strings.ContainsAny(raw, "\x00\r\n") || strings.Contains(raw, "#") {
		return errors.New("Hydra continuation redirect is outside protocol bounds")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Opaque != "" || parsed.Scheme+"://"+parsed.Host != bridge.hydraPublicOrigin {
		return errors.New("Hydra continuation redirect escaped the configured public origin")
	}
	if parsed.Path != "/oauth2/auth" || parsed.RawPath != "" || parsed.Fragment != "" || parsed.RawFragment != "" || parsed.ForceQuery {
		return errors.New("Hydra continuation redirect has an invalid public path")
	}
	if verifierQuery != hydraLoginVerifierQuery && verifierQuery != hydraConsentVerifierQuery {
		return errors.New("Hydra continuation redirect verifier type is invalid")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return errors.New("Hydra continuation redirect has invalid query encoding")
	}
	shape := hydraContinuationQueryShape(query)
	allowed := make(map[string]struct{}, len(hydraAuthorizationQuery)+2)
	for _, name := range hydraAuthorizationQuery {
		allowed[name] = struct{}{}
	}
	allowed["resource"] = struct{}{}
	allowed[verifierQuery] = struct{}{}
	for name, values := range query {
		_, permitted := allowed[name]
		if !permitted || len(values) != 1 || values[0] == "" || len(values[0]) > 4096 || strings.ContainsAny(values[0], "\x00\r\n") {
			return fmt.Errorf("Hydra continuation redirect has invalid query authority (%s)", shape)
		}
	}
	for _, required := range append(append([]string(nil), hydraAuthorizationQuery...), verifierQuery) {
		if _, ok := query[required]; !ok {
			return fmt.Errorf("Hydra continuation redirect has invalid query authority (%s)", shape)
		}
	}
	verifiers := query[verifierQuery]
	if validateHydraChallenge(verifiers[0]) != nil {
		return errors.New("Hydra continuation redirect has invalid verifier authority")
	}
	profile, ok := bridge.oauthProfiles[query.Get("client_id")]
	if !ok || query.Get("response_type") != "code" || query.Get("code_challenge_method") != "S256" ||
		query.Get("audience") != profile.Audience[0] ||
		!uniqueCanonicalTextSubset(strings.Split(query.Get("scope"), " "), profile.Scopes) ||
		!slices.Contains(strings.Split(query.Get("scope"), " "), corecontract.OAuthOpenIDScope) ||
		validateOIDCSecret("authorization state", query.Get("state")) != nil ||
		validateOIDCSecret("authorization nonce", query.Get("nonce")) != nil ||
		validateOIDCSecret("authorization PKCE challenge", query.Get("code_challenge")) != nil {
		return fmt.Errorf("Hydra continuation redirect has invalid OAuth authority (%s)", shape)
	}
	redirectURI, err := url.Parse(query.Get("redirect_uri"))
	if err != nil || redirectURI.Scheme != "https" || redirectURI.Host == "" || redirectURI.Hostname() == "" ||
		redirectURI.User != nil || redirectURI.Opaque != "" || redirectURI.Path != "/" || redirectURI.RawPath != "" ||
		redirectURI.RawQuery != "" || redirectURI.Fragment != "" || redirectURI.RawFragment != "" || redirectURI.ForceQuery {
		return fmt.Errorf("Hydra continuation redirect has invalid client callback authority (%s)", shape)
	}
	if _, err := userOAuthWorkspaceResource(profile.Authority, parsed.String()); err != nil {
		return fmt.Errorf("Hydra continuation redirect has invalid resource authority (%s)", shape)
	}
	expectedParameters := len(hydraAuthorizationQuery) + 1
	if profile.Authority == corecontract.UserOAuthBrowserAuthority {
		expectedParameters++
	}
	if len(query) != expectedParameters || parsed.RawQuery != query.Encode() {
		return fmt.Errorf("Hydra continuation redirect has invalid query authority (%s)", shape)
	}
	return nil
}

func hydraContinuationQueryShape(query url.Values) string {
	known := make([]string, 0, len(query))
	unknown := 0
	for name := range query {
		switch name {
		case "audience", "client_id", "code_challenge", "code_challenge_method", "consent_verifier",
			"login_verifier", "nonce", "redirect_uri", "resource", "response_type", "scope", "state":
			known = append(known, name)
		default:
			unknown++
		}
	}
	sort.Strings(known)
	return fmt.Sprintf("parameters=%s unknown_parameters=%d", strings.Join(known, ","), unknown)
}

func validateHydraPublicOrigin(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("Hydra public URL must be an HTTPS origin without credentials, path, query, or fragment")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func validateHydraChallenge(challenge string) error {
	if challenge == "" || len(challenge) > 4096 || strings.ContainsAny(challenge, "\x00\r\n") {
		return errors.New("Hydra challenge is empty or outside protocol bounds")
	}
	return nil
}

func sameUniqueTextSet(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	copyActual := append([]string(nil), actual...)
	copyExpected := append([]string(nil), expected...)
	sort.Strings(copyActual)
	sort.Strings(copyExpected)
	for index, value := range copyActual {
		if value == "" || strings.ContainsAny(value, " \t\r\n\x00") || (index > 0 && value == copyActual[index-1]) {
			return false
		}
	}
	return slices.Equal(copyActual, copyExpected)
}

func consentRequestHash(request HydraConsentRequest, grant HydraConsentGrant) ([32]byte, error) {
	if validateHydraChallenge(request.Challenge) != nil || validateHydraChallenge(request.LoginChallenge) != nil ||
		request.LoginSessionID == "" || len(request.LoginSessionID) > 4096 || strings.ContainsAny(request.LoginSessionID, "\x00\r\n") {
		return [32]byte{}, errors.New("Hydra consent correlation authority is outside protocol bounds")
	}
	scopes := append([]string(nil), request.RequestedScope...)
	audience := append([]string(nil), request.RequestedAccessTokenAudience...)
	sort.Strings(scopes)
	sort.Strings(audience)
	canonical, err := json.Marshal(struct {
		Challenge      string                          `json:"challenge"`
		LoginChallenge string                          `json:"loginChallenge"`
		ClientID       string                          `json:"clientId"`
		Subject        string                          `json:"subject"`
		Scopes         []string                        `json:"scopes"`
		Audience       []string                        `json:"audience"`
		LoginID        string                          `json:"loginSessionId"`
		RequestURL     string                          `json:"requestUrl"`
		Skip           bool                            `json:"skip"`
		GrantScope     []string                        `json:"grantScope"`
		GrantAudience  []string                        `json:"grantAudience"`
		Authority      corecontract.UserOAuthAuthority `json:"authority"`
	}{
		Challenge: request.Challenge, LoginChallenge: request.LoginChallenge,
		ClientID: request.Client.ClientID, Subject: request.Subject,
		Scopes: scopes, Audience: audience, LoginID: request.LoginSessionID,
		RequestURL: request.RequestURL, Skip: request.Skip,
		GrantScope: append([]string(nil), grant.Scope...), GrantAudience: append([]string(nil), grant.Audience...),
		Authority: grant.Authority,
	})
	if err != nil {
		return [32]byte{}, fmt.Errorf("encode Hydra consent request fingerprint: %w", err)
	}
	return sha256.Sum256(append([]byte("agentserver-v2/hydra-consent/v2\x00"), canonical...)), nil
}

func oidcLoginTerminalStatus(status string) bool {
	return status == coredb.OIDCLoginStatusRejected || status == coredb.OIDCLoginStatusFailed || status == coredb.OIDCLoginStatusExpired
}
