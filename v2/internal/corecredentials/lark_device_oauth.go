package corecredentials

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	larkDeviceOAuthAuthType           = "device_oauth"
	larkDefaultDeviceAuthorizationURL = "https://accounts.feishu.cn/oauth/v1/device_authorization"
	larkDefaultTokenURL               = "https://open.feishu.cn/open-apis/authen/v2/oauth/token"
	larkDefaultUserInfoURL            = "https://open.feishu.cn/open-apis/authen/v1/user_info"
	DefaultManagedLarkScopes          = "docx:document:readonly search:docs:read offline_access"
	maximumProviderResponseBytes      = int64(1024 * 1024)
)

type LarkDeviceFlowConfig struct {
	AppID                  string
	AppSecret              string
	Scopes                 string
	DeviceAuthorizationURL string
	TokenURL               string
	UserInfoURL            string
	HTTPClient             *http.Client
	Now                    func() time.Time
	AllowInsecureHTTP      bool
}

type LarkProvider struct {
	BearerProvider
	device *larkDeviceFlowClient
}

type larkDeviceFlowClient struct {
	appID, appSecret, scopes         string
	deviceURL, tokenURL, userInfoURL string
	httpClient                       *http.Client
	now                              func() time.Time
}

type larkDeviceProviderState struct {
	Version    int    `json:"version"`
	DeviceCode string `json:"deviceCode"`
	Scope      string `json:"scope"`
}

type larkOAuthCredential struct {
	Version          int       `json:"version"`
	AppID            string    `json:"appId"`
	AccessToken      string    `json:"accessToken"`
	RefreshToken     string    `json:"refreshToken"`
	Scope            string    `json:"scope"`
	UserOpenID       string    `json:"userOpenId,omitempty"`
	UserName         string    `json:"userName,omitempty"`
	GrantedAt        time.Time `json:"grantedAt"`
	AccessExpiresAt  time.Time `json:"accessExpiresAt"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt"`
}

func NewLarkProvider() LarkProvider {
	return LarkProvider{BearerProvider: BearerProvider{KindValue: "lark", HostValue: "open.feishu.cn"}}
}

func NewLarkDeviceFlowProvider(config LarkDeviceFlowConfig) (LarkProvider, error) {
	provider := NewLarkProvider()
	if config.Scopes == "" {
		config.Scopes = DefaultManagedLarkScopes
	}
	if config.DeviceAuthorizationURL == "" {
		config.DeviceAuthorizationURL = larkDefaultDeviceAuthorizationURL
	}
	if config.TokenURL == "" {
		config.TokenURL = larkDefaultTokenURL
	}
	if config.UserInfoURL == "" {
		config.UserInfoURL = larkDefaultUserInfoURL
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 20 * time.Second}
	}
	if !validProviderIdentifier(config.AppID, 512) || !validProviderSecret(config.AppSecret, 4096) {
		return LarkProvider{}, errors.New("Lark device-flow application identity is invalid")
	}
	scopes, err := normalizeLarkScopes(config.Scopes)
	if err != nil {
		return LarkProvider{}, err
	}
	for name, raw := range map[string]string{
		"device authorization": config.DeviceAuthorizationURL,
		"token":                config.TokenURL,
		"user info":            config.UserInfoURL,
	} {
		if err := validateFixedProviderURL(raw, config.AllowInsecureHTTP); err != nil {
			return LarkProvider{}, fmt.Errorf("Lark %s URL: %w", name, err)
		}
	}
	copyClient := *config.HTTPClient
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("provider redirects are forbidden") }
	provider.device = &larkDeviceFlowClient{
		appID: config.AppID, appSecret: config.AppSecret, scopes: scopes,
		deviceURL: config.DeviceAuthorizationURL, tokenURL: config.TokenURL,
		userInfoURL: config.UserInfoURL, httpClient: &copyClient, now: config.Now,
	}
	return provider, nil
}

func (provider LarkProvider) Schema() ProviderSchema {
	schema := provider.BearerProvider.Schema()
	schema.DisplayName = "Lark"
	if provider.device != nil {
		schema.AuthTypes = append(schema.AuthTypes, larkDeviceOAuthAuthType)
		schema.AuthorizationMethods = append(schema.AuthorizationMethods, AuthorizationMethodDeviceFlow)
		schema.SecretFormat = "opaque-token-or-lark-device-oauth-envelope"
	}
	return schema
}

func (provider LarkProvider) ValidateUpload(authType string, raw []byte) (UploadResult, error) {
	if strings.TrimSpace(authType) == "" || authType == "static" {
		return provider.BearerProvider.ValidateUpload(authType, raw)
	}
	if authType != larkDeviceOAuthAuthType || provider.device == nil {
		return UploadResult{}, errors.New("Lark credential auth type is not supported")
	}
	credential, err := parseLarkOAuthCredential(raw, provider.device.appID)
	if err != nil {
		return UploadResult{}, err
	}
	normalized, err := json.Marshal(credential)
	if err != nil {
		return UploadResult{}, errors.New("encode Lark OAuth credential")
	}
	public, err := larkCredentialPublicMetadata(credential)
	if err != nil {
		clear(normalized)
		return UploadResult{}, err
	}
	access, refresh := credential.AccessExpiresAt.UTC(), credential.RefreshExpiresAt.UTC()
	return UploadResult{
		AuthType: larkDeviceOAuthAuthType, PublicMetadata: public, Secret: normalized,
		AccessExpiresAt: &access, RefreshExpiresAt: &refresh,
	}, nil
}

func (provider LarkProvider) Materialize(ctx context.Context, binding Binding, secret []byte, request UseRequest) (HeaderMutation, error) {
	if binding.AuthType == "static" {
		return provider.BearerProvider.Materialize(ctx, binding, secret, request)
	}
	if provider.device == nil || binding.AuthType != larkDeviceOAuthAuthType || binding.Kind != "lark" || request.Host != "open.feishu.cn" {
		return HeaderMutation{}, errors.New("Lark OAuth credential binding or host mismatch")
	}
	credential, err := parseLarkOAuthCredential(secret, provider.device.appID)
	if err != nil {
		return HeaderMutation{}, errors.New("Lark OAuth credential envelope is invalid")
	}
	return HeaderMutation{Headers: map[string]string{"Authorization": "Bearer " + credential.AccessToken}}, nil
}

func (provider LarkProvider) BeginDeviceAuthorization(ctx context.Context, parameters json.RawMessage) (DeviceAuthorizationChallenge, error) {
	if provider.device == nil {
		return DeviceAuthorizationChallenge{}, errors.New("Lark device flow is not configured")
	}
	if err := requireEmptyProviderParameters(parameters); err != nil {
		return DeviceAuthorizationChallenge{}, err
	}
	form := url.Values{"client_id": {provider.device.appID}, "scope": {provider.device.scopes}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.device.deviceURL, strings.NewReader(form.Encode()))
	if err != nil {
		return DeviceAuthorizationChallenge{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(provider.device.appID+":"+provider.device.appSecret)))
	var response struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
		Error                   string `json:"error"`
		ErrorDescription        string `json:"error_description"`
	}
	status, err := doProviderJSON(provider.device.httpClient, request, &response)
	if err != nil {
		return DeviceAuthorizationChallenge{}, err
	}
	if status < 200 || status >= 300 || response.Error != "" {
		return DeviceAuthorizationChallenge{}, errors.New("Lark rejected the device authorization request")
	}
	if !validOpaqueToken(response.DeviceCode, 8192) || len(response.UserCode) > 1024 || response.ExpiresIn < 30 || response.ExpiresIn > 15*60 || response.Interval < 1 || response.Interval > 60 {
		return DeviceAuthorizationChallenge{}, errors.New("Lark returned an invalid device authorization challenge")
	}
	if err := validateVerificationURL(response.VerificationURI); err != nil {
		return DeviceAuthorizationChallenge{}, errors.New("Lark returned an invalid verification URL")
	}
	if response.VerificationURIComplete == "" {
		response.VerificationURIComplete = response.VerificationURI
	}
	if err := validateVerificationURL(response.VerificationURIComplete); err != nil {
		return DeviceAuthorizationChallenge{}, errors.New("Lark returned an invalid complete verification URL")
	}
	state, err := json.Marshal(larkDeviceProviderState{Version: 1, DeviceCode: response.DeviceCode, Scope: provider.device.scopes})
	if err != nil {
		return DeviceAuthorizationChallenge{}, err
	}
	public, _ := json.Marshal(map[string]any{"requestedScopes": strings.Fields(provider.device.scopes)})
	return DeviceAuthorizationChallenge{
		UserCode: response.UserCode, VerificationURI: response.VerificationURI,
		VerificationURIComplete: response.VerificationURIComplete,
		ExpiresAt:               provider.device.now().UTC().Add(time.Duration(response.ExpiresIn) * time.Second),
		Interval:                time.Duration(response.Interval) * time.Second,
		ProviderState:           state, ProviderPublic: public,
	}, nil
}

func (provider LarkProvider) PollDeviceAuthorization(ctx context.Context, raw []byte) (DeviceAuthorizationPollResult, error) {
	if provider.device == nil {
		return DeviceAuthorizationPollResult{}, errors.New("Lark device flow is not configured")
	}
	var state larkDeviceProviderState
	if err := decodeStrictProviderJSON(raw, &state); err != nil || state.Version != 1 || !validOpaqueToken(state.DeviceCode, 8192) || state.Scope != provider.device.scopes {
		return DeviceAuthorizationPollResult{}, errors.New("Lark device authorization state is invalid")
	}
	form := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {state.DeviceCode}, "client_id": {provider.device.appID}, "client_secret": {provider.device.appSecret},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.device.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return DeviceAuthorizationPollResult{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var response larkTokenResponse
	status, err := doProviderJSON(provider.device.httpClient, request, &response)
	if err != nil {
		return DeviceAuthorizationPollResult{}, err
	}
	if response.Error != "" || status < 200 || status >= 300 {
		switch response.Error {
		case "authorization_pending":
			return DeviceAuthorizationPollResult{Status: DeviceAuthorizationPending}, nil
		case "slow_down":
			return DeviceAuthorizationPollResult{Status: DeviceAuthorizationPending, RetryAfter: 5 * time.Second}, nil
		case "access_denied":
			return DeviceAuthorizationPollResult{Status: DeviceAuthorizationDenied, ErrorCode: "access_denied"}, nil
		case "expired_token", "invalid_grant":
			return DeviceAuthorizationPollResult{Status: DeviceAuthorizationExpired, ErrorCode: "expired_token"}, nil
		default:
			return DeviceAuthorizationPollResult{}, errors.New("Lark returned an unexpected device token response")
		}
	}
	credential, upload, err := provider.larkUploadFromToken(ctx, response, state.Scope, larkOAuthCredential{})
	if err != nil {
		return DeviceAuthorizationPollResult{}, err
	}
	_ = credential
	return DeviceAuthorizationPollResult{Status: DeviceAuthorizationSucceeded, Credential: upload}, nil
}

func (provider LarkProvider) RefreshDeviceCredential(ctx context.Context, binding Binding, raw []byte) (UploadResult, bool, error) {
	if provider.device == nil || binding.AuthType != larkDeviceOAuthAuthType {
		return UploadResult{}, true, errors.New("Lark device OAuth refresh is not configured")
	}
	current, err := parseLarkOAuthCredential(raw, provider.device.appID)
	if err != nil || current.RefreshToken == "" {
		return UploadResult{}, true, errors.New("Lark refresh authority is invalid")
	}
	form := url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {current.RefreshToken},
		"client_id": {provider.device.appID}, "client_secret": {provider.device.appSecret},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.device.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return UploadResult{}, false, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var response larkTokenResponse
	status, err := doProviderJSON(provider.device.httpClient, request, &response)
	if err != nil {
		return UploadResult{}, false, err
	}
	if response.Error != "" || status < 200 || status >= 300 {
		terminal := response.Error == "invalid_grant" || response.Error == "invalid_refresh_token" || response.Error == "access_denied"
		return UploadResult{}, terminal, errors.New("Lark rejected the refresh token")
	}
	_, upload, err := provider.larkUploadFromToken(ctx, response, current.Scope, current)
	return upload, false, err
}

type larkTokenResponse struct {
	AccessToken           string `json:"access_token"`
	RefreshToken          string `json:"refresh_token"`
	ExpiresIn             int    `json:"expires_in"`
	RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
	Scope                 string `json:"scope"`
	Error                 string `json:"error"`
	ErrorDescription      string `json:"error_description"`
}

func (provider LarkProvider) larkUploadFromToken(ctx context.Context, response larkTokenResponse, requestedScope string, current larkOAuthCredential) (larkOAuthCredential, UploadResult, error) {
	now := provider.device.now().UTC()
	if !validOpaqueToken(response.AccessToken, maximumHeaderValueBytes) || response.ExpiresIn < 60 || response.ExpiresIn > 24*60*60 {
		return larkOAuthCredential{}, UploadResult{}, errors.New("Lark returned invalid access-token material")
	}
	refresh := response.RefreshToken
	if refresh == "" {
		refresh = current.RefreshToken
	}
	if !validOpaqueToken(refresh, maximumHeaderValueBytes) {
		return larkOAuthCredential{}, UploadResult{}, errors.New("Lark returned invalid refresh-token material")
	}
	scope := response.Scope
	if scope == "" {
		scope = requestedScope
	}
	normalizedScope, err := normalizeLarkScopes(scope)
	if err != nil {
		return larkOAuthCredential{}, UploadResult{}, errors.New("Lark returned invalid scopes")
	}
	refreshExpiry := current.RefreshExpiresAt
	if response.RefreshTokenExpiresIn > 0 {
		if response.RefreshTokenExpiresIn > 90*24*60*60 {
			return larkOAuthCredential{}, UploadResult{}, errors.New("Lark returned an invalid refresh expiry")
		}
		refreshExpiry = now.Add(time.Duration(response.RefreshTokenExpiresIn) * time.Second)
	}
	if refreshExpiry.IsZero() {
		refreshExpiry = now.Add(7 * 24 * time.Hour)
	}
	credential := larkOAuthCredential{
		Version: 1, AppID: provider.device.appID, AccessToken: response.AccessToken,
		RefreshToken: refresh, Scope: normalizedScope, UserOpenID: current.UserOpenID, UserName: current.UserName,
		GrantedAt: current.GrantedAt, AccessExpiresAt: now.Add(time.Duration(response.ExpiresIn) * time.Second),
		RefreshExpiresAt: refreshExpiry,
	}
	if credential.GrantedAt.IsZero() {
		credential.GrantedAt = now
	}
	if credential.UserOpenID == "" {
		credential.UserOpenID, credential.UserName = provider.fetchLarkUserInfo(ctx, credential.AccessToken)
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		return larkOAuthCredential{}, UploadResult{}, err
	}
	public, err := larkCredentialPublicMetadata(credential)
	if err != nil {
		clear(raw)
		return larkOAuthCredential{}, UploadResult{}, err
	}
	access, refreshAt := credential.AccessExpiresAt, credential.RefreshExpiresAt
	return credential, UploadResult{
		AuthType: larkDeviceOAuthAuthType, PublicMetadata: public, Secret: raw,
		AccessExpiresAt: &access, RefreshExpiresAt: &refreshAt,
	}, nil
}

func (provider LarkProvider) fetchLarkUserInfo(ctx context.Context, token string) (string, string) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.device.userInfoURL, nil)
	if err != nil {
		return "", ""
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	var response struct {
		Code int `json:"code"`
		Data struct {
			OpenID      string `json:"open_id"`
			Name        string `json:"name"`
			EnglishName string `json:"en_name"`
		} `json:"data"`
	}
	status, err := doProviderJSON(provider.device.httpClient, request, &response)
	if err != nil || status < 200 || status >= 300 || response.Code != 0 || !validProviderIdentifier(response.Data.OpenID, 512) {
		return "", ""
	}
	name := strings.TrimSpace(response.Data.Name)
	if name == "" {
		name = strings.TrimSpace(response.Data.EnglishName)
	}
	if len(name) > 512 || strings.ContainsAny(name, "\x00\r\n") {
		name = ""
	}
	return response.Data.OpenID, name
}

func parseLarkOAuthCredential(raw []byte, appID string) (larkOAuthCredential, error) {
	var credential larkOAuthCredential
	if err := decodeStrictProviderJSON(raw, &credential); err != nil {
		return larkOAuthCredential{}, err
	}
	if credential.Version != 1 || credential.AppID != appID ||
		!validOpaqueToken(credential.AccessToken, maximumHeaderValueBytes) ||
		!validOpaqueToken(credential.RefreshToken, maximumHeaderValueBytes) ||
		credential.GrantedAt.IsZero() || credential.AccessExpiresAt.IsZero() || credential.RefreshExpiresAt.IsZero() ||
		!credential.RefreshExpiresAt.After(credential.AccessExpiresAt) || len(credential.UserOpenID) > 512 ||
		len(credential.UserName) > 512 || strings.ContainsAny(credential.UserOpenID+credential.UserName, "\x00\r\n") {
		return larkOAuthCredential{}, errors.New("Lark OAuth credential envelope is invalid")
	}
	if _, err := normalizeLarkScopes(credential.Scope); err != nil {
		return larkOAuthCredential{}, err
	}
	return credential, nil
}

func larkCredentialPublicMetadata(credential larkOAuthCredential) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"appId": credential.AppID, "scope": strings.Fields(credential.Scope),
		"userOpenId": credential.UserOpenID, "userName": credential.UserName,
	})
}

func normalizeLarkScopes(raw string) (string, error) {
	fields := strings.Fields(strings.ReplaceAll(raw, ",", " "))
	if len(fields) < 1 || len(fields) > 128 {
		return "", errors.New("Lark device-flow scopes are invalid")
	}
	seen := make(map[string]struct{}, len(fields))
	result := make([]string, 0, len(fields)+1)
	for _, scope := range fields {
		if len(scope) > 256 || strings.ContainsAny(scope, "\x00\r\n\t ") {
			return "", errors.New("Lark device-flow scope is invalid")
		}
		if _, ok := seen[scope]; !ok {
			seen[scope] = struct{}{}
			result = append(result, scope)
		}
	}
	if _, ok := seen["offline_access"]; !ok {
		result = append(result, "offline_access")
	}
	for index := 1; index < len(result); index++ {
		for current := index; current > 0 && result[current] < result[current-1]; current-- {
			result[current], result[current-1] = result[current-1], result[current]
		}
	}
	return strings.Join(result, " "), nil
}

func requireEmptyProviderParameters(raw json.RawMessage) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var value map[string]json.RawMessage
	if err := decodeStrictProviderJSON(raw, &value); err != nil || len(value) != 0 {
		return errors.New("provider parameters are not supported")
	}
	return nil
}

func decodeStrictProviderJSON(raw []byte, target any) error {
	if len(raw) == 0 || len(raw) > 256*1024 {
		return errors.New("provider JSON is outside bounds")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("provider JSON contains trailing data")
	}
	return nil
}

func doProviderJSON(client *http.Client, request *http.Request, target any) (int, error) {
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if rawType := response.Header.Get("Content-Type"); rawType != "" {
		mediaType, _, err := mime.ParseMediaType(rawType)
		if err != nil || (mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json")) {
			return response.StatusCode, errors.New("provider response is not JSON")
		}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maximumProviderResponseBytes+1))
	if err != nil || int64(len(raw)) > maximumProviderResponseBytes || len(raw) == 0 {
		return response.StatusCode, errors.New("provider response exceeds its bound")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return response.StatusCode, errors.New("provider response JSON is invalid")
	}
	return response.StatusCode, nil
}

func validateFixedProviderURL(raw string, allowInsecure bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" || parsed.Path == "" {
		return errors.New("provider URL must be a canonical absolute URL")
	}
	if parsed.Scheme != "https" && !(allowInsecure && parsed.Scheme == "http") {
		return errors.New("provider URL must use HTTPS")
	}
	return nil
}

func validateVerificationURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || len(raw) < 8 || len(raw) > 8192 || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || strings.ContainsAny(raw, "\x00\r\n") {
		return errors.New("verification URL is invalid")
	}
	return nil
}

func validProviderIdentifier(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func validProviderSecret(value string, maximum int) bool {
	return validProviderIdentifier(value, maximum) && !strings.Contains(value, " ")
}
