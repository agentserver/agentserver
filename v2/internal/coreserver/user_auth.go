package coreserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const maxIntrospectionResponseBytes = int64(64 * 1024)

type UserTokenIntrospection struct {
	Active    bool
	Subject   string
	ClientID  string
	Audience  []string
	Scope     string
	ExpiresAt int64
	IssuedAt  int64
	NotBefore int64
	Issuer    string
	TokenType string
	TokenUse  string
	Authority corecontract.UserOAuthAuthority
}

type UserTokenIntrospector interface {
	IntrospectUserToken(context.Context, string) (UserTokenIntrospection, error)
}

type IntrospectedUserAuthorizerConfig struct {
	Introspector      UserTokenIntrospector
	ExpectedIssuer    string
	ExpectedClientID  string
	ExpectedAudience  string
	ExpectedAuthority string
	AllowedScopes     []string
	ActionPermissions map[string]corecontract.UserOAuthActionAuthority
	Now               func() time.Time
}

type IntrospectedUserAuthorizer struct {
	introspector      UserTokenIntrospector
	issuer            string
	clientID          string
	audience          string
	authority         string
	allowedScopes     map[string]struct{}
	actionPermissions map[string]corecontract.UserOAuthActionAuthority
	now               func() time.Time
}

func NewIntrospectedUserAuthorizer(config IntrospectedUserAuthorizerConfig) (*IntrospectedUserAuthorizer, error) {
	if config.Introspector == nil {
		return nil, errors.New("user token introspector is required")
	}
	for name, value := range map[string]string{
		"issuer": config.ExpectedIssuer, "client ID": config.ExpectedClientID,
		"audience": config.ExpectedAudience, "authority": config.ExpectedAuthority,
	} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, " \t\r\n\x00") {
			return nil, fmt.Errorf("expected user token %s is required and must be canonical text", name)
		}
	}
	if config.ExpectedAuthority != corecontract.UserOAuthPlatformAuthority &&
		config.ExpectedAuthority != corecontract.UserOAuthBrowserAuthority {
		return nil, errors.New("expected user token authority is unsupported")
	}
	if len(config.AllowedScopes) == 0 {
		return nil, errors.New("at least one allowed user OAuth scope is required")
	}
	allowedScopes, err := canonicalOAuthTextSet(config.AllowedScopes, 32)
	if err != nil {
		return nil, fmt.Errorf("configure allowed user OAuth scopes: %w", err)
	}
	if _, ok := allowedScopes[corecontract.OAuthOpenIDScope]; !ok {
		return nil, errors.New("allowed user OAuth scopes must include openid")
	}
	if len(config.ActionPermissions) == 0 {
		return nil, errors.New("at least one user action permission is required")
	}
	actions := make(map[string]corecontract.UserOAuthActionAuthority, len(config.ActionPermissions))
	for action, authority := range config.ActionPermissions {
		if action == "" || strings.TrimSpace(action) != action || strings.ContainsAny(action, " \t\r\n\x00") ||
			(authority.Resource != corecontract.UserOAuthGlobalResource && authority.Resource != corecontract.UserOAuthWorkspaceResource) {
			return nil, errors.New("user action and resource names must be non-empty canonical authority")
		}
		if len(authority.Permissions) == 0 {
			return nil, fmt.Errorf("user action %s requires no permissions", action)
		}
		permissions, err := canonicalOAuthTextSet(authority.Permissions, 8)
		if err != nil {
			return nil, fmt.Errorf("configure permissions for user action %s: %w", action, err)
		}
		for permission := range permissions {
			if permission == corecontract.OAuthOpenIDScope {
				return nil, errors.New("openid cannot authorize a user business action")
			}
			if _, ok := allowedScopes[permission]; !ok {
				return nil, fmt.Errorf("user action %s requires a scope outside the client profile", action)
			}
		}
		actions[action] = corecontract.UserOAuthActionAuthority{
			Resource: authority.Resource, Permissions: append([]string(nil), authority.Permissions...),
		}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &IntrospectedUserAuthorizer{
		introspector: config.Introspector,
		issuer:       config.ExpectedIssuer, clientID: config.ExpectedClientID,
		audience: config.ExpectedAudience, authority: config.ExpectedAuthority,
		allowedScopes: allowedScopes, actionPermissions: actions, now: config.Now,
	}, nil
}

func (authorizer *IntrospectedUserAuthorizer) AuthorizeUser(request *http.Request, action string) (string, error) {
	if authorizer == nil || authorizer.introspector == nil || request == nil {
		return "", ErrUserAuthUnavailable
	}
	requirement, ok := authorizer.actionPermissions[action]
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
	if !introspection.Active || !canonicalPublicUUID(introspection.Subject) ||
		introspection.Issuer != authorizer.issuer || introspection.ClientID != authorizer.clientID ||
		!strings.EqualFold(introspection.TokenType, "Bearer") ||
		(introspection.TokenUse != "" && introspection.TokenUse != "access_token") {
		return "", ErrInvalidUserAccessToken
	}
	if introspection.ExpiresAt <= authorizer.now().Unix() {
		return "", ErrInvalidUserAccessToken
	}
	// Every user OAuth profile has exactly one audience. Rejecting mixed
	// audiences prevents a token accepted by another application or an internal
	// component from being replayed against this authority.
	if len(introspection.Audience) != 1 || introspection.Audience[0] != authorizer.audience {
		return "", ErrInvalidUserAccessToken
	}
	scopes, err := parseCanonicalOAuthScope(introspection.Scope)
	if err != nil || !sameTextKeys(scopes, authorityPermissionSet(introspection.Authority)) {
		return "", ErrInvalidUserAccessToken
	}
	for scope := range scopes {
		if _, ok := authorizer.allowedScopes[scope]; !ok {
			return "", ErrInvalidUserAccessToken
		}
	}
	if err := validateUserOAuthAuthority(introspection.Authority, authorizer.authority, scopes, authorizer.allowedScopes); err != nil {
		return "", ErrInvalidUserAccessToken
	}
	var granted map[string]struct{}
	switch requirement.Resource {
	case corecontract.UserOAuthGlobalResource:
		if request.PathValue("workspaceId") != "" {
			return "", ErrInvalidUserAccessToken
		}
		granted, err = canonicalOAuthTextSet(introspection.Authority.GlobalPermissions, 32)
	case corecontract.UserOAuthWorkspaceResource:
		workspaceID := request.PathValue("workspaceId")
		if !canonicalPublicUUID(workspaceID) {
			return "", ErrInvalidUserAccessToken
		}
		granted, err = workspaceGrantPermissions(introspection.Authority.WorkspaceGrants, workspaceID)
	default:
		return "", ErrInvalidUserAccessToken
	}
	if err != nil || !containsAllPermissions(granted, requirement.Permissions) {
		return "", ErrInvalidUserAccessToken
	}
	return introspection.Subject, nil
}

func validateUserOAuthAuthority(
	authority corecontract.UserOAuthAuthority,
	expected string,
	tokenScopes, allowedScopes map[string]struct{},
) error {
	if authority.Version != corecontract.UserOAuthAuthorityVersion || authority.Authority != expected {
		return errors.New("user OAuth authority version or type is invalid")
	}
	global, err := canonicalOAuthTextSet(authority.GlobalPermissions, 32)
	if err != nil {
		return err
	}
	if len(authority.WorkspaceGrants) > 256 {
		return errors.New("user OAuth authority contains too many workspace grants")
	}
	if expected == corecontract.UserOAuthBrowserAuthority &&
		(len(global) != 0 || len(authority.WorkspaceGrants) != 1) {
		return errors.New("Browser OAuth authority must contain exactly one workspace grant and no global permissions")
	}
	union := make(map[string]struct{}, len(global)+8)
	for permission := range global {
		union[permission] = struct{}{}
	}
	seenWorkspaces := make(map[string]struct{}, len(authority.WorkspaceGrants))
	for _, grant := range authority.WorkspaceGrants {
		if !canonicalPublicUUID(grant.WorkspaceID) || grant.Generation < 1 {
			return errors.New("user OAuth workspace grant has invalid identity or generation")
		}
		if _, duplicate := seenWorkspaces[grant.WorkspaceID]; duplicate {
			return errors.New("user OAuth workspace grants contain a duplicate workspace")
		}
		seenWorkspaces[grant.WorkspaceID] = struct{}{}
		permissions, err := canonicalOAuthTextSet(grant.Permissions, 32)
		if err != nil {
			return err
		}
		if len(permissions) == 0 {
			return errors.New("user OAuth workspace grant has no permissions")
		}
		if !slices.IsSorted(grant.Permissions) {
			return errors.New("user OAuth workspace permissions are not canonically sorted")
		}
		for permission := range permissions {
			union[permission] = struct{}{}
		}
	}
	if !slices.IsSorted(authority.GlobalPermissions) {
		return errors.New("user OAuth global permissions are not canonically sorted")
	}
	for permission := range union {
		if permission == corecontract.OAuthOpenIDScope {
			return errors.New("openid appears as a business permission")
		}
		if _, ok := allowedScopes[permission]; !ok {
			return errors.New("user OAuth authority contains a permission outside the client profile")
		}
	}
	expectedTokenScopes := make(map[string]struct{}, len(union)+1)
	expectedTokenScopes[corecontract.OAuthOpenIDScope] = struct{}{}
	for permission := range union {
		expectedTokenScopes[permission] = struct{}{}
	}
	if !sameTextKeys(tokenScopes, expectedTokenScopes) {
		return errors.New("user OAuth scope is not the exact authority permission union")
	}
	return nil
}

func authorityPermissionSet(authority corecontract.UserOAuthAuthority) map[string]struct{} {
	result := map[string]struct{}{corecontract.OAuthOpenIDScope: {}}
	for _, permission := range authority.GlobalPermissions {
		result[permission] = struct{}{}
	}
	for _, grant := range authority.WorkspaceGrants {
		for _, permission := range grant.Permissions {
			result[permission] = struct{}{}
		}
	}
	return result
}

func workspaceGrantPermissions(grants []corecontract.UserOAuthWorkspaceGrant, workspaceID string) (map[string]struct{}, error) {
	for _, grant := range grants {
		if grant.WorkspaceID == workspaceID {
			return canonicalOAuthTextSet(grant.Permissions, 32)
		}
	}
	return nil, errors.New("workspace grant is unavailable")
}

func containsAllPermissions(granted map[string]struct{}, required []string) bool {
	for _, permission := range required {
		if _, ok := granted[permission]; !ok {
			return false
		}
	}
	return true
}

func parseCanonicalOAuthScope(value string) (map[string]struct{}, error) {
	fields := strings.Fields(value)
	if len(fields) == 0 || strings.Join(fields, " ") != value {
		return nil, errors.New("OAuth scope is not canonical")
	}
	return canonicalOAuthTextSet(fields, 32)
}

func canonicalOAuthTextSet(values []string, maximum int) (map[string]struct{}, error) {
	if len(values) > maximum {
		return nil, errors.New("OAuth authority text set is outside bounds")
	}
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, " \t\r\n\x00") {
			return nil, errors.New("OAuth authority contains non-canonical text")
		}
		if _, duplicate := result[value]; duplicate {
			return nil, errors.New("OAuth authority contains duplicate text")
		}
		result[value] = struct{}{}
	}
	return result, nil
}

func sameTextKeys(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for value := range left {
		if _, ok := right[value]; !ok {
			return false
		}
	}
	return true
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
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return UserTokenIntrospection{}, errors.New("Hydra introspection response Content-Type is not application/json")
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxJSONValues = 8192
	limits.MaxJSONDepth = 16
	value, canonical, err := braincatalog.DecodeCanonicalJSON(body, int(maxIntrospectionResponseBytes), limits)
	if err != nil {
		return UserTokenIntrospection{}, fmt.Errorf("validate Hydra introspection response: %w", err)
	}
	if _, ok := value.(map[string]any); !ok {
		return UserTokenIntrospection{}, errors.New("Hydra introspection response is not a JSON object")
	}
	var wire struct {
		Active    bool            `json:"active"`
		Subject   string          `json:"sub"`
		ClientID  string          `json:"client_id"`
		Audience  oauthAudience   `json:"aud"`
		Scope     string          `json:"scope"`
		ExpiresAt int64           `json:"exp"`
		IssuedAt  int64           `json:"iat"`
		NotBefore int64           `json:"nbf"`
		Issuer    string          `json:"iss"`
		TokenType string          `json:"token_type"`
		TokenUse  string          `json:"token_use"`
		Ext       json.RawMessage `json:"ext"`
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	if err := decoder.Decode(&wire); err != nil {
		return UserTokenIntrospection{}, fmt.Errorf("decode Hydra introspection response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return UserTokenIntrospection{}, errors.New("Hydra introspection response contains trailing JSON")
	}
	authority, err := decodeIntrospectedUserOAuthAuthority(wire.Ext)
	if err != nil {
		return UserTokenIntrospection{}, fmt.Errorf("decode Hydra AgentServer authority: %w", err)
	}
	return UserTokenIntrospection{
		Active: wire.Active, Subject: wire.Subject, ClientID: wire.ClientID, Audience: append([]string(nil), wire.Audience...),
		Scope: wire.Scope, ExpiresAt: wire.ExpiresAt, IssuedAt: wire.IssuedAt, NotBefore: wire.NotBefore,
		Issuer: wire.Issuer, TokenType: wire.TokenType, TokenUse: wire.TokenUse, Authority: authority,
	}, nil
}

func decodeIntrospectedUserOAuthAuthority(raw json.RawMessage) (corecontract.UserOAuthAuthority, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return corecontract.UserOAuthAuthority{}, nil
	}
	var extensions map[string]json.RawMessage
	if err := json.Unmarshal(raw, &extensions); err != nil || extensions == nil {
		return corecontract.UserOAuthAuthority{}, errors.New("introspection ext is not a JSON object")
	}
	rawAuthority, ok := extensions["agentserver"]
	if !ok {
		return corecontract.UserOAuthAuthority{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(rawAuthority))
	decoder.DisallowUnknownFields()
	var authority corecontract.UserOAuthAuthority
	if err := decoder.Decode(&authority); err != nil {
		return corecontract.UserOAuthAuthority{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return corecontract.UserOAuthAuthority{}, errors.New("AgentServer introspection authority contains trailing JSON")
	}
	return authority, nil
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
