package coreserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxIntrospectionResponseBytes = int64(64 * 1024)

type UserTokenIntrospection struct {
	Active    bool
	Subject   string
	Audience  []string
	Scope     string
	ExpiresAt int64
}

type UserTokenIntrospector interface {
	IntrospectUserToken(context.Context, string) (UserTokenIntrospection, error)
}

type IntrospectedUserAuthorizerConfig struct {
	Introspector     UserTokenIntrospector
	ExpectedAudience string
	ActionScopes     map[string]string
	Now              func() time.Time
}

type IntrospectedUserAuthorizer struct {
	introspector UserTokenIntrospector
	audience     string
	actionScopes map[string]string
	now          func() time.Time
}

func NewIntrospectedUserAuthorizer(config IntrospectedUserAuthorizerConfig) (*IntrospectedUserAuthorizer, error) {
	if config.Introspector == nil {
		return nil, errors.New("user token introspector is required")
	}
	if strings.TrimSpace(config.ExpectedAudience) == "" || strings.ContainsAny(config.ExpectedAudience, " \t\r\n\x00") {
		return nil, errors.New("expected user token audience is required and must be canonical text")
	}
	if len(config.ActionScopes) == 0 {
		return nil, errors.New("at least one user action scope is required")
	}
	scopes := make(map[string]string, len(config.ActionScopes))
	for action, scope := range config.ActionScopes {
		if action == "" || scope == "" || strings.ContainsAny(action+scope, " \t\r\n\x00") {
			return nil, errors.New("user action and scope names must be non-empty canonical text")
		}
		scopes[action] = scope
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &IntrospectedUserAuthorizer{
		introspector: config.Introspector, audience: config.ExpectedAudience,
		actionScopes: scopes, now: config.Now,
	}, nil
}

func (authorizer *IntrospectedUserAuthorizer) AuthorizeUser(request *http.Request, action string) (string, error) {
	if authorizer == nil || authorizer.introspector == nil || request == nil {
		return "", ErrUserAuthUnavailable
	}
	requiredScope, ok := authorizer.actionScopes[action]
	if !ok {
		return "", ErrInvalidUserAccessToken
	}
	token, err := exactUserBearer(request.Header)
	if err != nil {
		return "", ErrInvalidUserAccessToken
	}
	introspection, err := authorizer.introspector.IntrospectUserToken(request.Context(), token)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUserAuthUnavailable, err)
	}
	if !introspection.Active || !canonicalPublicUUID(introspection.Subject) {
		return "", ErrInvalidUserAccessToken
	}
	if introspection.ExpiresAt <= authorizer.now().Unix() {
		return "", ErrInvalidUserAccessToken
	}
	// Phase 1 browser tokens have exactly one audience. Rejecting mixed
	// audiences prevents a token accepted by an internal component from being
	// replayed merely because it also names agentserver-api.
	if len(introspection.Audience) != 1 || introspection.Audience[0] != authorizer.audience {
		return "", ErrInvalidUserAccessToken
	}
	if !containsOAuthScope(introspection.Scope, requiredScope) {
		return "", ErrInvalidUserAccessToken
	}
	return introspection.Subject, nil
}

type HydraUserIntrospector struct {
	endpoint   *url.URL
	httpClient *http.Client
}

func NewHydraUserIntrospector(endpoint string, httpClient *http.Client, allowInsecureHTTP bool) (*HydraUserIntrospector, error) {
	if httpClient == nil {
		return nil, errors.New("Hydra introspection HTTP client is required")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Hydra introspection endpoint must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	if parsed.Scheme == "http" && !allowInsecureHTTP && !isLoopbackURLHost(parsed.Hostname()) {
		return nil, errors.New("cleartext Hydra introspection requires an explicit insecure-cluster opt-in")
	}
	if parsed.Path == "" || parsed.Path == "/" || strings.HasSuffix(parsed.Path, "/") {
		return nil, errors.New("Hydra introspection endpoint must contain the exact endpoint path")
	}
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HydraUserIntrospector{endpoint: parsed, httpClient: &clientCopy}, nil
}

func (introspector *HydraUserIntrospector) IntrospectUserToken(ctx context.Context, token string) (UserTokenIntrospection, error) {
	if introspector == nil || introspector.endpoint == nil || introspector.httpClient == nil {
		return UserTokenIntrospection{}, errors.New("Hydra user introspector is not initialized")
	}
	form := url.Values{"token": {token}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, introspector.endpoint.String(), strings.NewReader(form))
	if err != nil {
		return UserTokenIntrospection{}, fmt.Errorf("construct Hydra introspection request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := introspector.httpClient.Do(request)
	if err != nil {
		return UserTokenIntrospection{}, fmt.Errorf("execute Hydra introspection: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxIntrospectionResponseBytes+1))
	if err != nil {
		return UserTokenIntrospection{}, fmt.Errorf("read Hydra introspection response: %w", err)
	}
	if len(body) > int(maxIntrospectionResponseBytes) {
		return UserTokenIntrospection{}, errors.New("Hydra introspection response exceeds size limit")
	}
	if response.StatusCode != http.StatusOK {
		return UserTokenIntrospection{}, fmt.Errorf("Hydra introspection returned HTTP %d", response.StatusCode)
	}
	var wire struct {
		Active    bool          `json:"active"`
		Subject   string        `json:"sub"`
		Audience  oauthAudience `json:"aud"`
		Scope     string        `json:"scope"`
		ExpiresAt int64         `json:"exp"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&wire); err != nil {
		return UserTokenIntrospection{}, fmt.Errorf("decode Hydra introspection response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return UserTokenIntrospection{}, errors.New("Hydra introspection response contains trailing JSON")
	}
	return UserTokenIntrospection{
		Active: wire.Active, Subject: wire.Subject, Audience: append([]string(nil), wire.Audience...),
		Scope: wire.Scope, ExpiresAt: wire.ExpiresAt,
	}, nil
}

type oauthAudience []string

func (audience *oauthAudience) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(raw, []byte("null")) {
		return nil
	}
	if len(raw) != 0 && raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		*audience = []string{value}
		return nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return err
	}
	*audience = append((*audience)[:0], values...)
	return nil
}

func exactUserBearer(header http.Header) (string, error) {
	values := header.Values("Authorization")
	if len(values) != 1 || strings.Contains(values[0], ",") || !strings.HasPrefix(values[0], "Bearer ") {
		return "", errors.New("exactly one bearer authorization value is required")
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if token == "" || len(token) > 8192 || strings.ContainsAny(token, " \t\r\n\x00") {
		return "", errors.New("bearer token is not canonical")
	}
	return token, nil
}

func containsOAuthScope(scopes, required string) bool {
	for _, scope := range strings.Fields(scopes) {
		if scope == required {
			return true
		}
	}
	return false
}

func isLoopbackURLHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
