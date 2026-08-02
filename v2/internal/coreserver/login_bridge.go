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
)

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
}

type LoginBridgeConfig struct {
	Store                LoginBridgeStore
	Hydra                HydraAdminAPI
	IdentityProvider     ExternalOIDCProvider
	Sealer               *LoginTransactionSealer
	HydraBrowserClientID string
	HydraPublicOrigin    string
	TransactionTTL       time.Duration
	NewID                func() (string, error)
	Random               io.Reader
}

type LoginBridge struct {
	store             LoginBridgeStore
	hydra             HydraAdminAPI
	identityProvider  ExternalOIDCProvider
	sealer            *LoginTransactionSealer
	hydraClientID     string
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
	if config.HydraBrowserClientID == "" || len(config.HydraBrowserClientID) > 512 || strings.ContainsAny(config.HydraBrowserClientID, "\x00\r\n") {
		return nil, errors.New("login bridge Hydra browser client ID is empty or outside protocol bounds")
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
		hydraClientID: config.HydraBrowserClientID, hydraPublicOrigin: canonicalOrigin,
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
	if err := bridge.validateBrowserAuthorizationRequest(
		loginRequest.Client.ClientID,
		loginRequest.RequestedScope,
		loginRequest.RequestedAccessTokenAudience,
	); err != nil {
		return bridge.rejectUntrackedLogin(ctx, challenge, "invalid_request", "browser authorization request is not allowed")
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
		HydraClientID: bridge.hydraClientID, TTL: bridge.transactionTTL,
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
	if err := bridge.validateBrowserAuthorizationRequest(
		request.Client.ClientID,
		request.RequestedScope,
		request.RequestedAccessTokenAudience,
	); err != nil {
		return bridge.rejectUntrackedConsent(ctx, challenge, "invalid_scope", "requested browser authority is not allowed")
	}
	challengeHash := sha256.Sum256([]byte(challenge))
	requestHash, err := consentRequestHash(request)
	if err != nil {
		return ConsentResult{}, err
	}
	created, err := bridge.store.CreateHydraConsentTransaction(ctx, coredb.CreateHydraConsentTransactionCommand{
		ConsentChallengeSHA256: challengeHash, RequestSHA256: requestHash,
		HydraClientID: bridge.hydraClientID, UserID: request.Subject, TTL: bridge.transactionTTL,
	})
	if err != nil {
		return ConsentResult{}, err
	}
	redirect, err := bridge.hydra.AcceptConsentRequest(
		ctx, challenge, append([]string(nil), defaultBrowserOAuthScopes...), append([]string(nil), defaultBrowserAudience...),
	)
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
		transaction.OIDCIssuer != bridge.identityProvider.Issuer() || transaction.HydraClientID != bridge.hydraClientID {
		return oidcLoginSecrets{}, errors.New("sealed login transaction does not match its database correlation hashes")
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

func (bridge *LoginBridge) validateBrowserAuthorizationRequest(clientID string, scopes, audience []string) error {
	if clientID != bridge.hydraClientID {
		return errors.New("Hydra authorization request belongs to a different client")
	}
	if !sameUniqueTextSet(scopes, defaultBrowserOAuthScopes) {
		return errors.New("Hydra authorization request contains an unsupported scope set")
	}
	if !sameUniqueTextSet(audience, defaultBrowserAudience) {
		return errors.New("Hydra authorization request contains an unsupported audience set")
	}
	return nil
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
	if err != nil || len(query) != 1 {
		return errors.New("Hydra continuation redirect has invalid query authority")
	}
	verifiers, ok := query[verifierQuery]
	if !ok || len(verifiers) != 1 || validateHydraChallenge(verifiers[0]) != nil {
		return errors.New("Hydra continuation redirect has invalid verifier authority")
	}
	return nil
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

func consentRequestHash(request HydraConsentRequest) ([32]byte, error) {
	if validateHydraChallenge(request.Challenge) != nil || validateHydraChallenge(request.LoginChallenge) != nil ||
		request.LoginSessionID == "" || len(request.LoginSessionID) > 4096 || strings.ContainsAny(request.LoginSessionID, "\x00\r\n") {
		return [32]byte{}, errors.New("Hydra consent correlation authority is outside protocol bounds")
	}
	scopes := append([]string(nil), request.RequestedScope...)
	audience := append([]string(nil), request.RequestedAccessTokenAudience...)
	sort.Strings(scopes)
	sort.Strings(audience)
	canonical, err := json.Marshal(struct {
		Challenge      string   `json:"challenge"`
		LoginChallenge string   `json:"loginChallenge"`
		ClientID       string   `json:"clientId"`
		Subject        string   `json:"subject"`
		Scopes         []string `json:"scopes"`
		Audience       []string `json:"audience"`
		LoginID        string   `json:"loginSessionId"`
		Skip           bool     `json:"skip"`
	}{
		Challenge: request.Challenge, LoginChallenge: request.LoginChallenge,
		ClientID: request.Client.ClientID, Subject: request.Subject,
		Scopes: scopes, Audience: audience, LoginID: request.LoginSessionID, Skip: request.Skip,
	})
	if err != nil {
		return [32]byte{}, fmt.Errorf("encode Hydra consent request fingerprint: %w", err)
	}
	return sha256.Sum256(append([]byte("agentserver-v2/hydra-consent/v1\x00"), canonical...)), nil
}

func oidcLoginTerminalStatus(status string) bool {
	return status == coredb.OIDCLoginStatusRejected || status == coredb.OIDCLoginStatusFailed || status == coredb.OIDCLoginStatusExpired
}
