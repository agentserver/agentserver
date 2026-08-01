package coreserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type ExternalOIDCIdentity struct {
	Issuer  string
	Subject string
}

type ExternalOIDCProvider interface {
	Issuer() string
	AuthorizationURL(state, nonce, verifier string) (string, error)
	Exchange(context.Context, string, string, string) (ExternalOIDCIdentity, error)
}

type DiscoveredExternalOIDCProvider struct {
	issuer     string
	redirect   string
	oauth      oauth2.Config
	verifier   *oidc.IDTokenVerifier
	httpClient *http.Client
}

func NewDiscoveredExternalOIDCProvider(
	ctx context.Context,
	issuer, clientID, clientSecret, redirectURL string,
	httpClient *http.Client,
	allowInsecureHTTP bool,
) (*DiscoveredExternalOIDCProvider, error) {
	if ctx == nil || httpClient == nil {
		return nil, errors.New("external OIDC discovery context and HTTP client are required")
	}
	if err := validateOIDCIssuer(issuer, allowInsecureHTTP); err != nil {
		return nil, err
	}
	if clientID == "" || len(clientID) > 512 || strings.ContainsAny(clientID, "\x00\r\n") {
		return nil, errors.New("external OIDC client ID is empty or outside protocol bounds")
	}
	if clientSecret == "" || len(clientSecret) > 8192 || strings.ContainsAny(clientSecret, "\x00\r\n") {
		return nil, errors.New("external OIDC client secret is empty or outside protocol bounds")
	}
	if err := validateOIDCRedirectURL(redirectURL, allowInsecureHTTP); err != nil {
		return nil, err
	}
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("external OIDC metadata, token, and JWKS redirects are forbidden")
	}
	discoveryContext := oidc.ClientContext(ctx, &clientCopy)
	provider, err := oidc.NewProvider(discoveryContext, issuer)
	if err != nil {
		return nil, fmt.Errorf("discover external OIDC provider: %w", err)
	}
	endpoint := provider.Endpoint()
	if err := validateOIDCEndpoint("authorization", endpoint.AuthURL, allowInsecureHTTP); err != nil {
		return nil, err
	}
	if err := validateOIDCEndpoint("token", endpoint.TokenURL, allowInsecureHTTP); err != nil {
		return nil, err
	}
	var metadata struct {
		Issuer  string `json:"issuer"`
		JWKSURL string `json:"jwks_uri"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return nil, fmt.Errorf("decode external OIDC discovery claims: %w", err)
	}
	if metadata.Issuer != issuer {
		return nil, errors.New("external OIDC discovery issuer does not exactly match configuration")
	}
	if err := validateOIDCEndpoint("JWKS", metadata.JWKSURL, allowInsecureHTTP); err != nil {
		return nil, err
	}
	return &DiscoveredExternalOIDCProvider{
		issuer: issuer, redirect: redirectURL,
		oauth: oauth2.Config{
			ClientID: clientID, ClientSecret: clientSecret, Endpoint: endpoint,
			RedirectURL: redirectURL, Scopes: []string{oidc.ScopeOpenID},
		},
		verifier:   provider.VerifierContext(discoveryContext, &oidc.Config{ClientID: clientID}),
		httpClient: &clientCopy,
	}, nil
}

func (provider *DiscoveredExternalOIDCProvider) Issuer() string {
	if provider == nil {
		return ""
	}
	return provider.issuer
}

func (provider *DiscoveredExternalOIDCProvider) AuthorizationURL(state, nonce, verifier string) (string, error) {
	if provider == nil || provider.verifier == nil || provider.httpClient == nil {
		return "", errors.New("external OIDC provider is not initialized")
	}
	if err := validateOIDCCorrelation(state, nonce, verifier); err != nil {
		return "", err
	}
	authorizationURL := provider.oauth.AuthCodeURL(
		state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	)
	if len(authorizationURL) > 8192 || strings.ContainsAny(authorizationURL, "\x00\r\n") {
		return "", errors.New("external OIDC authorization URL is outside protocol bounds")
	}
	return authorizationURL, nil
}

func (provider *DiscoveredExternalOIDCProvider) Exchange(
	ctx context.Context,
	code, verifier, expectedNonce string,
) (ExternalOIDCIdentity, error) {
	if provider == nil || provider.verifier == nil || provider.httpClient == nil {
		return ExternalOIDCIdentity{}, errors.New("external OIDC provider is not initialized")
	}
	if code == "" || len(code) > 8192 || strings.ContainsAny(code, "\x00\r\n") {
		return ExternalOIDCIdentity{}, errors.New("external OIDC authorization code is empty or outside protocol bounds")
	}
	if err := validateOIDCSecret("nonce", expectedNonce); err != nil {
		return ExternalOIDCIdentity{}, err
	}
	if err := validateOIDCSecret("PKCE verifier", verifier); err != nil {
		return ExternalOIDCIdentity{}, err
	}
	exchangeContext := oidc.ClientContext(ctx, provider.httpClient)
	token, err := provider.oauth.Exchange(exchangeContext, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return ExternalOIDCIdentity{}, fmt.Errorf("exchange external OIDC authorization code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" || len(rawIDToken) > 64*1024 {
		return ExternalOIDCIdentity{}, errors.New("external OIDC token response omitted a bounded ID token")
	}
	idToken, err := provider.verifier.Verify(exchangeContext, rawIDToken)
	if err != nil {
		return ExternalOIDCIdentity{}, fmt.Errorf("verify external OIDC ID token: %w", err)
	}
	if !constantTimeTextEqual(idToken.Nonce, expectedNonce) {
		return ExternalOIDCIdentity{}, errors.New("external OIDC ID token nonce does not match the login transaction")
	}
	if idToken.AccessTokenHash != "" {
		if token.AccessToken == "" || idToken.VerifyAccessToken(token.AccessToken) != nil {
			return ExternalOIDCIdentity{}, errors.New("external OIDC access token hash does not match the ID token")
		}
	}
	if idToken.Issuer != provider.issuer || idToken.Subject == "" || len(idToken.Subject) > 2048 || strings.ContainsRune(idToken.Subject, '\x00') {
		return ExternalOIDCIdentity{}, errors.New("external OIDC ID token identity is outside protocol bounds")
	}
	return ExternalOIDCIdentity{Issuer: idToken.Issuer, Subject: idToken.Subject}, nil
}

func validateOIDCIssuer(raw string, allowInsecureHTTP bool) error {
	if len(raw) < 8 || len(raw) > 2048 || strings.TrimSpace(raw) != raw || strings.HasSuffix(raw, "/") {
		return errors.New("external OIDC issuer must be bounded canonical URL text without a trailing slash")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("external OIDC issuer must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && allowInsecureHTTP) {
		return errors.New("cleartext external OIDC issuer requires an explicit insecure-cluster opt-in")
	}
	return nil
}

func validateOIDCRedirectURL(raw string, allowInsecureHTTP bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || len(raw) > 4096 || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "/auth/oidc/callback" {
		return errors.New("external OIDC redirect URL must be an absolute callback URL at /auth/oidc/callback without credentials, query, or fragment")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && allowInsecureHTTP) {
		return errors.New("cleartext external OIDC callback requires an explicit insecure-cluster opt-in")
	}
	return nil
}

func validateOIDCEndpoint(name, raw string, allowInsecureHTTP bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || len(raw) > 4096 || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("external OIDC %s endpoint is not an absolute bounded URL", name)
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && allowInsecureHTTP) {
		return fmt.Errorf("cleartext external OIDC %s endpoint requires an explicit insecure-cluster opt-in", name)
	}
	return nil
}

func validateOIDCCorrelation(state, nonce, verifier string) error {
	for name, value := range map[string]string{"state": state, "nonce": nonce, "PKCE verifier": verifier} {
		if err := validateOIDCSecret(name, value); err != nil {
			return err
		}
	}
	return nil
}

func validateOIDCSecret(name, value string) error {
	if len(value) < 43 || len(value) > 128 {
		return fmt.Errorf("external OIDC %s must contain between 43 and 128 bytes", name)
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' || character == '~') {
			return fmt.Errorf("external OIDC %s contains a non-PKCE-unreserved character", name)
		}
	}
	return nil
}

func constantTimeTextEqual(left, right string) bool {
	leftHash := sha256.Sum256([]byte(left))
	rightHash := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}
