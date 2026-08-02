package coreserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/publichttps"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	maximumLLMGatewayOAuthTokenBytes   = 256 * 1024
	maximumLLMGatewayBearerBytes       = 16*1024 - len("Bearer ")
	maximumLLMGatewayOIDCResponseBytes = 2 * 1024 * 1024
	workspaceGatewayProvider           = "workspace-gateway"
)

var errLLMGatewayOIDCResponseTooLarge = errors.New("workspace LLM gateway OIDC response exceeds its size limit")

type WorkspaceLLMGatewayOIDCConfig struct {
	Issuer      string
	ClientID    string
	Scopes      []string
	RedirectURL string
}

type WorkspaceLLMGatewayOIDCTokenSet struct {
	AccessToken          string    `json:"accessToken"`
	AccessTokenExpiresAt time.Time `json:"accessTokenExpiresAt"`
	IDToken              string    `json:"idToken"`
	IDTokenExpiresAt     time.Time `json:"idTokenExpiresAt"`
	RefreshToken         string    `json:"refreshToken,omitempty"`
}

type WorkspaceLLMGatewayOIDCGrant struct {
	Issuer  string
	Subject string
	Tokens  WorkspaceLLMGatewayOIDCTokenSet
}

type WorkspaceLLMGatewayOIDCProvider interface {
	AuthorizationURL(state, nonce, verifier string) (string, error)
	Exchange(context.Context, string, string, string) (WorkspaceLLMGatewayOIDCGrant, error)
	Refresh(context.Context, WorkspaceLLMGatewayOIDCTokenSet, string, string) (WorkspaceLLMGatewayOIDCGrant, error)
}

type WorkspaceLLMGatewayOIDCProviderFactory interface {
	Discover(context.Context, WorkspaceLLMGatewayOIDCConfig) (WorkspaceLLMGatewayOIDCProvider, error)
}

type DiscoveredWorkspaceLLMGatewayOIDCFactory struct {
	httpClient *http.Client
}

func NewDiscoveredWorkspaceLLMGatewayOIDCFactory(httpClient *http.Client) (*DiscoveredWorkspaceLLMGatewayOIDCFactory, error) {
	if httpClient == nil {
		return nil, errors.New("workspace LLM gateway OIDC HTTP client is required")
	}
	clientCopy := *httpClient
	transport := clientCopy.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	clientCopy.Transport = boundedLLMGatewayOIDCRoundTripper{
		next: transport, maximumResponseBytes: maximumLLMGatewayOIDCResponseBytes,
	}
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("workspace LLM gateway OIDC redirects are forbidden")
	}
	return &DiscoveredWorkspaceLLMGatewayOIDCFactory{httpClient: &clientCopy}, nil
}

// The upstream OIDC package bounds token responses but reads discovery and
// JWKS documents to EOF. Every endpoint here is workspace-configured, so the
// client itself must impose one shared response-body ceiling before any OIDC
// decoder sees the body.
type boundedLLMGatewayOIDCRoundTripper struct {
	next                 http.RoundTripper
	maximumResponseBytes int64
}

func (transport boundedLLMGatewayOIDCRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if transport.next == nil || transport.maximumResponseBytes < 1 {
		return nil, errors.New("workspace LLM gateway OIDC response limiter is unavailable")
	}
	response, err := transport.next.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, errors.New("workspace LLM gateway OIDC endpoint returned an invalid response")
	}
	if response.ContentLength > transport.maximumResponseBytes {
		_ = response.Body.Close()
		return nil, errLLMGatewayOIDCResponseTooLarge
	}
	response.Body = &boundedLLMGatewayOIDCResponseBody{
		body: response.Body, remaining: transport.maximumResponseBytes,
	}
	return response, nil
}

type boundedLLMGatewayOIDCResponseBody struct {
	body      io.ReadCloser
	remaining int64
}

func (body *boundedLLMGatewayOIDCResponseBody) Read(destination []byte) (int, error) {
	if body == nil || body.body == nil {
		return 0, errors.New("workspace LLM gateway OIDC response body is unavailable")
	}
	if len(destination) == 0 {
		return 0, nil
	}
	if body.remaining > 0 {
		if int64(len(destination)) > body.remaining {
			destination = destination[:body.remaining]
		}
		count, err := body.body.Read(destination)
		body.remaining -= int64(count)
		return count, err
	}
	var probe [1]byte
	count, err := body.body.Read(probe[:])
	if count != 0 {
		return 0, errLLMGatewayOIDCResponseTooLarge
	}
	return 0, err
}

func (body *boundedLLMGatewayOIDCResponseBody) Close() error {
	if body == nil || body.body == nil {
		return nil
	}
	return body.body.Close()
}

type DiscoveredWorkspaceLLMGatewayOIDCProvider struct {
	issuer     string
	oauth      oauth2.Config
	verifier   *oidc.IDTokenVerifier
	httpClient *http.Client
}

func (factory *DiscoveredWorkspaceLLMGatewayOIDCFactory) Discover(
	ctx context.Context,
	config WorkspaceLLMGatewayOIDCConfig,
) (WorkspaceLLMGatewayOIDCProvider, error) {
	if ctx == nil || factory == nil || factory.httpClient == nil {
		return nil, errors.New("workspace LLM gateway OIDC discovery is unavailable")
	}
	if _, err := publichttps.ValidateIssuer(config.Issuer); err != nil {
		return nil, fmt.Errorf("validate workspace LLM gateway OIDC issuer: %w", err)
	}
	if config.ClientID == "" || len(config.ClientID) > 512 || strings.TrimSpace(config.ClientID) != config.ClientID || strings.ContainsAny(config.ClientID, "\x00\r\n") {
		return nil, errors.New("workspace LLM gateway OIDC client ID is outside protocol bounds")
	}
	if err := validateWorkspaceLLMGatewayOIDCScopes(config.Scopes); err != nil {
		return nil, err
	}
	redirect, err := publichttps.ValidateURL(config.RedirectURL, corecontract.LLMGatewayOIDCCallbackPath)
	if err != nil {
		return nil, fmt.Errorf("validate workspace LLM gateway OIDC redirect URL: %w", err)
	}
	if redirect.RawQuery != "" || redirect.Fragment != "" {
		return nil, errors.New("workspace LLM gateway OIDC redirect URL contains query or fragment")
	}
	discoveryContext := oidc.ClientContext(ctx, factory.httpClient)
	provider, err := oidc.NewProvider(discoveryContext, config.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover workspace LLM gateway OIDC provider: %w", err)
	}
	endpoint := provider.Endpoint()
	if _, err := publichttps.ValidateURL(endpoint.AuthURL, ""); err != nil {
		return nil, fmt.Errorf("validate workspace LLM gateway OIDC authorization endpoint: %w", err)
	}
	if _, err := publichttps.ValidateURL(endpoint.TokenURL, ""); err != nil {
		return nil, fmt.Errorf("validate workspace LLM gateway OIDC token endpoint: %w", err)
	}
	var metadata struct {
		Issuer  string `json:"issuer"`
		JWKSURL string `json:"jwks_uri"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return nil, fmt.Errorf("decode workspace LLM gateway OIDC discovery metadata: %w", err)
	}
	if metadata.Issuer != config.Issuer {
		return nil, errors.New("workspace LLM gateway OIDC discovery issuer does not exactly match configuration")
	}
	if _, err := publichttps.ValidateURL(metadata.JWKSURL, ""); err != nil {
		return nil, fmt.Errorf("validate workspace LLM gateway OIDC JWKS endpoint: %w", err)
	}
	endpoint.AuthStyle = oauth2.AuthStyleInParams
	return &DiscoveredWorkspaceLLMGatewayOIDCProvider{
		issuer: config.Issuer,
		oauth: oauth2.Config{
			ClientID: config.ClientID, Endpoint: endpoint, RedirectURL: config.RedirectURL,
			Scopes: append([]string(nil), config.Scopes...),
		},
		verifier:   provider.VerifierContext(discoveryContext, &oidc.Config{ClientID: config.ClientID}),
		httpClient: factory.httpClient,
	}, nil
}

func (provider *DiscoveredWorkspaceLLMGatewayOIDCProvider) AuthorizationURL(state, nonce, verifier string) (string, error) {
	if provider == nil || provider.verifier == nil || provider.httpClient == nil {
		return "", errors.New("workspace LLM gateway OIDC provider is not initialized")
	}
	if err := validateOIDCCorrelation(state, nonce, verifier); err != nil {
		return "", err
	}
	result := provider.oauth.AuthCodeURL(
		state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	)
	if len(result) > 8192 || strings.ContainsAny(result, "\x00\r\n") {
		return "", errors.New("workspace LLM gateway OIDC authorization URL is outside protocol bounds")
	}
	parsed, err := url.Parse(result)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", errors.New("workspace LLM gateway OIDC authorization URL is invalid")
	}
	return result, nil
}

func (provider *DiscoveredWorkspaceLLMGatewayOIDCProvider) Exchange(
	ctx context.Context,
	code, verifier, expectedNonce string,
) (WorkspaceLLMGatewayOIDCGrant, error) {
	if provider == nil || provider.verifier == nil || provider.httpClient == nil {
		return WorkspaceLLMGatewayOIDCGrant{}, errors.New("workspace LLM gateway OIDC provider is not initialized")
	}
	if code == "" || len(code) > 8192 || strings.ContainsAny(code, "\x00\r\n") {
		return WorkspaceLLMGatewayOIDCGrant{}, errors.New("workspace LLM gateway OIDC authorization code is outside protocol bounds")
	}
	if err := validateOIDCSecret("PKCE verifier", verifier); err != nil {
		return WorkspaceLLMGatewayOIDCGrant{}, err
	}
	if err := validateOIDCSecret("nonce", expectedNonce); err != nil {
		return WorkspaceLLMGatewayOIDCGrant{}, err
	}
	exchangeContext := oidc.ClientContext(ctx, provider.httpClient)
	token, err := provider.oauth.Exchange(exchangeContext, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return WorkspaceLLMGatewayOIDCGrant{}, fmt.Errorf("exchange workspace LLM gateway OIDC authorization code: %w", err)
	}
	if !strings.EqualFold(token.TokenType, "Bearer") {
		return WorkspaceLLMGatewayOIDCGrant{}, errors.New("workspace LLM gateway OIDC token response did not return a bearer token")
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return WorkspaceLLMGatewayOIDCGrant{}, errors.New("workspace LLM gateway OIDC token response omitted an ID token")
	}
	idToken, err := provider.verifyIDToken(exchangeContext, rawIDToken, token.AccessToken, "")
	if err != nil {
		return WorkspaceLLMGatewayOIDCGrant{}, err
	}
	if !constantTimeTextEqual(idToken.Nonce, expectedNonce) {
		return WorkspaceLLMGatewayOIDCGrant{}, errors.New("workspace LLM gateway OIDC nonce does not match the authorization transaction")
	}
	tokens := WorkspaceLLMGatewayOIDCTokenSet{
		AccessToken: token.AccessToken, AccessTokenExpiresAt: token.Expiry.UTC(),
		IDToken: rawIDToken, IDTokenExpiresAt: idToken.Expiry.UTC(), RefreshToken: token.RefreshToken,
	}
	if err := validateWorkspaceLLMGatewayOIDCTokenSet(tokens); err != nil {
		return WorkspaceLLMGatewayOIDCGrant{}, err
	}
	return WorkspaceLLMGatewayOIDCGrant{Issuer: idToken.Issuer, Subject: idToken.Subject, Tokens: tokens}, nil
}

func (provider *DiscoveredWorkspaceLLMGatewayOIDCProvider) Refresh(
	ctx context.Context,
	current WorkspaceLLMGatewayOIDCTokenSet,
	expectedSubject, bearerTokenType string,
) (WorkspaceLLMGatewayOIDCGrant, error) {
	if provider == nil || provider.verifier == nil || provider.httpClient == nil {
		return WorkspaceLLMGatewayOIDCGrant{}, errors.New("workspace LLM gateway OIDC provider is not initialized")
	}
	if err := validateWorkspaceLLMGatewayOIDCTokenSet(current); err != nil {
		return WorkspaceLLMGatewayOIDCGrant{}, err
	}
	if current.RefreshToken == "" {
		return WorkspaceLLMGatewayOIDCGrant{}, errors.New("workspace LLM gateway OIDC grant has no refresh token")
	}
	if expectedSubject == "" || len(expectedSubject) > 2048 || strings.ContainsRune(expectedSubject, '\x00') {
		return WorkspaceLLMGatewayOIDCGrant{}, errors.New("workspace LLM gateway OIDC expected subject is invalid")
	}
	if bearerTokenType != "access_token" && bearerTokenType != "id_token" {
		return WorkspaceLLMGatewayOIDCGrant{}, errors.New("workspace LLM gateway bearer token type is invalid")
	}
	refreshContext := oidc.ClientContext(ctx, provider.httpClient)
	// Force oauth2's token source to use the refresh grant instead of returning
	// the current access token from its local validity check.
	refreshed, err := provider.oauth.TokenSource(refreshContext, &oauth2.Token{
		AccessToken: current.AccessToken, TokenType: "Bearer", RefreshToken: current.RefreshToken,
		Expiry: time.Unix(1, 0),
	}).Token()
	if err != nil {
		return WorkspaceLLMGatewayOIDCGrant{}, fmt.Errorf("refresh workspace LLM gateway OIDC grant: %w", err)
	}
	if !strings.EqualFold(refreshed.TokenType, "Bearer") {
		return WorkspaceLLMGatewayOIDCGrant{}, errors.New("workspace LLM gateway OIDC refresh did not return a bearer token")
	}
	tokens := WorkspaceLLMGatewayOIDCTokenSet{
		AccessToken: refreshed.AccessToken, AccessTokenExpiresAt: refreshed.Expiry.UTC(),
		IDToken: current.IDToken, IDTokenExpiresAt: current.IDTokenExpiresAt,
		RefreshToken: refreshed.RefreshToken,
	}
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = current.RefreshToken
	}
	subject := expectedSubject
	if rawIDToken, ok := refreshed.Extra("id_token").(string); ok && rawIDToken != "" {
		idToken, err := provider.verifyIDToken(refreshContext, rawIDToken, refreshed.AccessToken, expectedSubject)
		if err != nil {
			return WorkspaceLLMGatewayOIDCGrant{}, err
		}
		tokens.IDToken = rawIDToken
		tokens.IDTokenExpiresAt = idToken.Expiry.UTC()
		subject = idToken.Subject
	} else if bearerTokenType == "id_token" {
		return WorkspaceLLMGatewayOIDCGrant{}, errors.New("workspace LLM gateway OIDC refresh omitted the required ID token")
	}
	if err := validateWorkspaceLLMGatewayOIDCTokenSet(tokens); err != nil {
		return WorkspaceLLMGatewayOIDCGrant{}, err
	}
	return WorkspaceLLMGatewayOIDCGrant{Issuer: provider.issuer, Subject: subject, Tokens: tokens}, nil
}

func (provider *DiscoveredWorkspaceLLMGatewayOIDCProvider) verifyIDToken(
	ctx context.Context,
	raw, accessToken, expectedSubject string,
) (*oidc.IDToken, error) {
	if raw == "" || len(raw) > maximumLLMGatewayOAuthTokenBytes || strings.ContainsAny(raw, "\x00\r\n") {
		return nil, errors.New("workspace LLM gateway OIDC ID token is outside protocol bounds")
	}
	idToken, err := provider.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("verify workspace LLM gateway OIDC ID token: %w", err)
	}
	if idToken.Issuer != provider.issuer || idToken.Subject == "" || len(idToken.Subject) > 2048 || strings.ContainsRune(idToken.Subject, '\x00') {
		return nil, errors.New("workspace LLM gateway OIDC identity is outside protocol bounds")
	}
	if expectedSubject != "" && !constantTimeTextEqual(idToken.Subject, expectedSubject) {
		return nil, errors.New("workspace LLM gateway OIDC subject changed during refresh")
	}
	if idToken.AccessTokenHash != "" {
		if accessToken == "" || idToken.VerifyAccessToken(accessToken) != nil {
			return nil, errors.New("workspace LLM gateway OIDC access token hash does not match the ID token")
		}
	}
	return idToken, nil
}

func validateWorkspaceLLMGatewayOIDCScopes(scopes []string) error {
	if len(scopes) < 1 || len(scopes) > 16 {
		return errors.New("workspace LLM gateway OIDC scopes must contain between one and sixteen values")
	}
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if scope == "" || len(scope) > 128 || strings.TrimSpace(scope) != scope || strings.ContainsAny(scope, " \t\r\n\x00") {
			return errors.New("workspace LLM gateway OIDC scope is not a bounded token")
		}
		if _, duplicate := seen[scope]; duplicate {
			return errors.New("workspace LLM gateway OIDC scopes must be unique")
		}
		seen[scope] = struct{}{}
	}
	if _, ok := seen[oidc.ScopeOpenID]; !ok {
		return errors.New("workspace LLM gateway OIDC scopes must include openid")
	}
	if _, ok := seen["offline_access"]; !ok {
		return errors.New("workspace LLM gateway OIDC scopes must include offline_access")
	}
	return nil
}

func validateWorkspaceLLMGatewayOIDCTokenSet(tokens WorkspaceLLMGatewayOIDCTokenSet) error {
	for name, value := range map[string]string{"access token": tokens.AccessToken, "ID token": tokens.IDToken} {
		if value == "" || len(value) > maximumLLMGatewayOAuthTokenBytes || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("workspace LLM gateway OIDC %s is outside protocol bounds", name)
		}
	}
	if tokens.RefreshToken != "" && (len(tokens.RefreshToken) > maximumLLMGatewayOAuthTokenBytes ||
		strings.TrimSpace(tokens.RefreshToken) != tokens.RefreshToken || strings.ContainsAny(tokens.RefreshToken, "\x00\r\n")) {
		return errors.New("workspace LLM gateway OIDC refresh token is outside protocol bounds")
	}
	if tokens.AccessTokenExpiresAt.IsZero() || tokens.IDTokenExpiresAt.IsZero() ||
		tokens.AccessTokenExpiresAt.Year() > 9999 || tokens.IDTokenExpiresAt.Year() > 9999 {
		return errors.New("workspace LLM gateway OIDC token expiries are required")
	}
	return nil
}

func workspaceLLMGatewayBearer(tokens WorkspaceLLMGatewayOIDCTokenSet, bearerTokenType string) (string, time.Time, error) {
	if err := validateWorkspaceLLMGatewayOIDCTokenSet(tokens); err != nil {
		return "", time.Time{}, err
	}
	var bearer string
	var expiresAt time.Time
	switch bearerTokenType {
	case "access_token":
		bearer, expiresAt = tokens.AccessToken, tokens.AccessTokenExpiresAt.UTC()
	case "id_token":
		bearer, expiresAt = tokens.IDToken, tokens.IDTokenExpiresAt.UTC()
	default:
		return "", time.Time{}, errors.New("workspace LLM gateway bearer token type is unsupported")
	}
	if len(bearer) > maximumLLMGatewayBearerBytes {
		return "", time.Time{}, errors.New("workspace LLM gateway bearer exceeds the llmproxy authorization header bound")
	}
	return bearer, expiresAt, nil
}

func canonicalWorkspaceLLMGatewayScopes(scopes []string) ([]string, string, error) {
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email", "offline_access"}
	}
	copyScopes := append([]string(nil), scopes...)
	if err := validateWorkspaceLLMGatewayOIDCScopes(copyScopes); err != nil {
		return nil, "", err
	}
	// A canonical storage order makes create retries independent of the order
	// selected by a UI while preserving exact scope set semantics.
	slices.Sort(copyScopes)
	return copyScopes, strings.Join(copyScopes, " "), nil
}
