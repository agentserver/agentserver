package corecredentials

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	byteCloudDeviceOAuthAuthType     = "device_oauth"
	byteCloudDefaultSite             = "i18n-tt"
	DefaultByteCloudDeviceAPIBaseURL = "https://cloud.tiktok-row.net"
	byteCloudCLIRegistrationPath     = "/api/v1/ai_auth/ai/auth/service_account_app/cli_registration"
	byteCloudCLIPollingPath          = "/api/v1/ai_auth/ai/auth/service_account_app/cli_login_polling"
	byteCloudRefreshPath             = "/api/v1/ai_auth/ai/auth/service_account_app/get_user_access_token"
)

type ByteCloudDeviceFlowConfig struct {
	Site              string
	APIBaseURL        string
	HTTPClient        *http.Client
	Now               func() time.Time
	Random            io.Reader
	AllowInsecureHTTP bool
}

type byteCloudDeviceFlowClient struct {
	site, apiBaseURL string
	httpClient       *http.Client
	now              func() time.Time
	random           io.Reader
}

type byteCloudDeviceProviderState struct {
	Version    int    `json:"version"`
	Site       string `json:"site"`
	DeviceCode string `json:"deviceCode"`
	Ticket     string `json:"ticket"`
}

type byteCloudOAuthCredential struct {
	Version          int       `json:"version"`
	Site             string    `json:"site"`
	AppID            string    `json:"appId"`
	DeviceCode       string    `json:"deviceCode"`
	AccessToken      string    `json:"accessToken"`
	RefreshToken     string    `json:"refreshToken"`
	TokenType        string    `json:"tokenType"`
	Scope            string    `json:"scope,omitempty"`
	Username         string    `json:"username,omitempty"`
	GrantedAt        time.Time `json:"grantedAt"`
	AccessExpiresAt  time.Time `json:"accessExpiresAt"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt"`
}

func NewByteCloudDeviceFlowProvider(host string, exchanger func(context.Context, string, string) (string, time.Time, error), config ByteCloudDeviceFlowConfig) (ByteCloudProvider, error) {
	provider := NewByteCloudProvider(host, exchanger)
	if config.Site == "" {
		config.Site = byteCloudDefaultSite
	}
	if config.Site != byteCloudDefaultSite {
		return ByteCloudProvider{}, errors.New("ByteCloud device flow site is not supported by this deployment")
	}
	if config.APIBaseURL == "" {
		config.APIBaseURL = DefaultByteCloudDeviceAPIBaseURL
	}
	if err := validateProviderOrigin(config.APIBaseURL, config.AllowInsecureHTTP); err != nil {
		return ByteCloudProvider{}, fmt.Errorf("ByteCloud device-flow API origin: %w", err)
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 20 * time.Second}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	copyClient := *config.HTTPClient
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("provider redirects are forbidden") }
	provider.device = &byteCloudDeviceFlowClient{
		site: config.Site, apiBaseURL: strings.TrimRight(config.APIBaseURL, "/"),
		httpClient: &copyClient, now: config.Now, random: config.Random,
	}
	return provider, nil
}

func (provider ByteCloudProvider) BeginDeviceAuthorization(ctx context.Context, parameters json.RawMessage) (DeviceAuthorizationChallenge, error) {
	if provider.device == nil {
		return DeviceAuthorizationChallenge{}, errors.New("ByteCloud device flow is not configured")
	}
	if err := requireEmptyProviderParameters(parameters); err != nil {
		return DeviceAuthorizationChallenge{}, err
	}
	random := make([]byte, 16)
	if _, err := io.ReadFull(provider.device.random, random); err != nil {
		return DeviceAuthorizationChallenge{}, errors.New("generate ByteCloud device code")
	}
	deviceCode := hex.EncodeToString(random)
	clear(random)
	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Code                    string `json:"code"`
			Ticket                  string `json:"ticket"`
			ExpireAt                int64  `json:"expire_at"`
			ServiceAccountCreateURL string `json:"service_account_create_url"`
		} `json:"data"`
	}
	status, err := provider.doByteCloudJSON(ctx, byteCloudCLIRegistrationPath, deviceCode, map[string]string{"device_code": deviceCode}, &response)
	if err != nil {
		return DeviceAuthorizationChallenge{}, err
	}
	if status < 200 || status >= 300 || response.Code != 0 || !validOpaqueToken(response.Data.Ticket, 8192) {
		return DeviceAuthorizationChallenge{}, errors.New("ByteCloud rejected the device registration request")
	}
	if err := validateVerificationURL(response.Data.ServiceAccountCreateURL); err != nil {
		return DeviceAuthorizationChallenge{}, errors.New("ByteCloud returned an invalid verification URL")
	}
	expiresAt := parseProviderExpiry(provider.device.now().UTC(), response.Data.ExpireAt, 10*time.Minute)
	if !expiresAt.After(provider.device.now().UTC().Add(30*time.Second)) || expiresAt.After(provider.device.now().UTC().Add(24*time.Hour)) {
		return DeviceAuthorizationChallenge{}, errors.New("ByteCloud returned an invalid device authorization expiry")
	}
	state, err := json.Marshal(byteCloudDeviceProviderState{Version: 1, Site: provider.device.site, DeviceCode: deviceCode, Ticket: response.Data.Ticket})
	if err != nil {
		return DeviceAuthorizationChallenge{}, err
	}
	public, _ := json.Marshal(map[string]any{"site": provider.device.site})
	return DeviceAuthorizationChallenge{
		UserCode:                strings.TrimSpace(response.Data.Code),
		VerificationURI:         response.Data.ServiceAccountCreateURL,
		VerificationURIComplete: response.Data.ServiceAccountCreateURL,
		ExpiresAt:               expiresAt, Interval: 2 * time.Second, ProviderState: state, ProviderPublic: public,
	}, nil
}

func (provider ByteCloudProvider) PollDeviceAuthorization(ctx context.Context, raw []byte) (DeviceAuthorizationPollResult, error) {
	if provider.device == nil {
		return DeviceAuthorizationPollResult{}, errors.New("ByteCloud device flow is not configured")
	}
	var state byteCloudDeviceProviderState
	if err := decodeStrictProviderJSON(raw, &state); err != nil || state.Version != 1 || state.Site != provider.device.site ||
		!validOpaqueToken(state.DeviceCode, 128) || !validOpaqueToken(state.Ticket, 8192) {
		return DeviceAuthorizationPollResult{}, errors.New("ByteCloud device authorization state is invalid")
	}
	var response byteCloudPollingResponse
	status, err := provider.doByteCloudJSON(ctx, byteCloudCLIPollingPath, state.DeviceCode, map[string]string{"ticket": state.Ticket}, &response)
	if err != nil {
		return DeviceAuthorizationPollResult{}, err
	}
	message := strings.ToLower(strings.TrimSpace(response.Message))
	if status == http.StatusBadRequest {
		switch {
		case response.Code == 1 && message == "authorization_pending":
			return DeviceAuthorizationPollResult{Status: DeviceAuthorizationPending}, nil
		case response.Code == 2 && message == "access_denied":
			return DeviceAuthorizationPollResult{Status: DeviceAuthorizationDenied, ErrorCode: "access_denied"}, nil
		case response.Code == 3 && message == "expired_ticket":
			return DeviceAuthorizationPollResult{Status: DeviceAuthorizationExpired, ErrorCode: "expired_token"}, nil
		case response.Code == 4 && message == "invalid_ticket":
			return DeviceAuthorizationPollResult{Status: DeviceAuthorizationFailed, ErrorCode: "invalid_ticket"}, nil
		}
	}
	if status >= 500 && message == "server_error" {
		return DeviceAuthorizationPollResult{}, errors.New("ByteCloud device authorization is temporarily unavailable")
	}
	if status < 200 || status >= 300 || response.Code != 0 || message != "ok" {
		return DeviceAuthorizationPollResult{}, errors.New("ByteCloud returned an unexpected polling response")
	}
	upload, err := provider.byteCloudUploadFromPolling(state, response)
	if err != nil {
		return DeviceAuthorizationPollResult{}, err
	}
	return DeviceAuthorizationPollResult{Status: DeviceAuthorizationSucceeded, Credential: upload}, nil
}

type byteCloudPollingResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Verbose string `json:"verbose"`
	Data    struct {
		TokenInfo struct {
			AccessToken          string `json:"access_token"`
			TokenType            string `json:"token_type"`
			RefreshToken         string `json:"refresh_token"`
			ExpiresIn            int64  `json:"expires_in"`
			RefreshTokenExpireIn int64  `json:"refresh_token_expire_in"`
			Scope                string `json:"scope"`
		} `json:"token_info"`
		Username string `json:"username"`
		AppID    string `json:"app_id"`
	} `json:"data"`
}

func (provider ByteCloudProvider) byteCloudUploadFromPolling(state byteCloudDeviceProviderState, response byteCloudPollingResponse) (UploadResult, error) {
	now := provider.device.now().UTC()
	access := strings.TrimSpace(response.Data.TokenInfo.AccessToken)
	refresh := strings.TrimSpace(response.Data.TokenInfo.RefreshToken)
	if !validOpaqueToken(access, maximumHeaderValueBytes) || !validOpaqueToken(refresh, maximumHeaderValueBytes) ||
		!validProviderIdentifier(strings.TrimSpace(response.Data.AppID), 512) {
		return UploadResult{}, errors.New("ByteCloud returned incomplete OAuth material")
	}
	accessExpiry := byteCloudAccessExpiry(now, access, response.Data.TokenInfo.ExpiresIn)
	refreshExpiry := parseProviderExpiry(now, response.Data.TokenInfo.RefreshTokenExpireIn, 7*24*time.Hour)
	if !accessExpiry.After(now.Add(time.Minute)) || !refreshExpiry.After(accessExpiry) {
		return UploadResult{}, errors.New("ByteCloud returned invalid OAuth expiry")
	}
	credential := byteCloudOAuthCredential{
		Version: 1, Site: state.Site, AppID: strings.TrimSpace(response.Data.AppID), DeviceCode: state.DeviceCode,
		AccessToken: access, RefreshToken: refresh, TokenType: firstNonEmpty(strings.TrimSpace(response.Data.TokenInfo.TokenType), "Bearer"),
		Scope: strings.TrimSpace(response.Data.TokenInfo.Scope), Username: boundedProviderText(response.Data.Username, 512),
		GrantedAt: now, AccessExpiresAt: accessExpiry, RefreshExpiresAt: refreshExpiry,
	}
	return provider.byteCloudCredentialUpload(credential)
}

func (provider ByteCloudProvider) RefreshDeviceCredential(ctx context.Context, binding Binding, raw []byte) (UploadResult, bool, error) {
	if provider.device == nil || binding.AuthType != byteCloudDeviceOAuthAuthType {
		return UploadResult{}, true, errors.New("ByteCloud device OAuth refresh is not configured")
	}
	current, err := parseByteCloudOAuthCredential(raw, provider.device.site)
	if err != nil {
		return UploadResult{}, true, errors.New("ByteCloud refresh authority is invalid")
	}
	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Verbose string `json:"verbose"`
		Data    struct {
			AccessToken          string `json:"access_token"`
			RefreshToken         string `json:"refresh_token"`
			ExpireAt             int64  `json:"expire_at"`
			RefreshTokenExpireIn int64  `json:"refresh_token_expire_in"`
		} `json:"data"`
	}
	status, err := provider.doByteCloudJSON(ctx, byteCloudRefreshPath, current.DeviceCode, map[string]string{"refresh_token": current.RefreshToken}, &response)
	if err != nil {
		return UploadResult{}, false, err
	}
	if status < 200 || status >= 300 || response.Code != 0 {
		terminal := status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusBadRequest
		return UploadResult{}, terminal, errors.New("ByteCloud rejected the refresh token")
	}
	access := strings.TrimSpace(response.Data.AccessToken)
	refresh := strings.TrimSpace(response.Data.RefreshToken)
	if refresh == "" {
		refresh = current.RefreshToken
	}
	if !validOpaqueToken(access, maximumHeaderValueBytes) || !validOpaqueToken(refresh, maximumHeaderValueBytes) {
		return UploadResult{}, true, errors.New("ByteCloud returned incomplete refresh material")
	}
	now := provider.device.now().UTC()
	current.AccessToken, current.RefreshToken = access, refresh
	current.AccessExpiresAt = byteCloudAccessExpiry(now, access, response.Data.ExpireAt)
	if response.Data.RefreshTokenExpireIn > 0 {
		current.RefreshExpiresAt = parseProviderExpiry(now, response.Data.RefreshTokenExpireIn, 7*24*time.Hour)
	}
	if !current.AccessExpiresAt.After(now.Add(time.Minute)) || !current.RefreshExpiresAt.After(current.AccessExpiresAt) {
		return UploadResult{}, true, errors.New("ByteCloud returned invalid refresh expiry")
	}
	upload, err := provider.byteCloudCredentialUpload(current)
	return upload, false, err
}

func (provider ByteCloudProvider) validateByteCloudOAuthCredential(raw []byte) (UploadResult, error) {
	credential, err := parseByteCloudOAuthCredential(raw, provider.device.site)
	if err != nil {
		return UploadResult{}, err
	}
	return provider.byteCloudCredentialUpload(credential)
}

func (provider ByteCloudProvider) byteCloudCredentialUpload(credential byteCloudOAuthCredential) (UploadResult, error) {
	raw, err := json.Marshal(credential)
	if err != nil {
		return UploadResult{}, err
	}
	public, err := json.Marshal(map[string]any{
		"site": credential.Site, "appId": credential.AppID, "username": credential.Username, "scope": credential.Scope,
	})
	if err != nil {
		clear(raw)
		return UploadResult{}, err
	}
	access, refresh := credential.AccessExpiresAt, credential.RefreshExpiresAt
	return UploadResult{AuthType: byteCloudDeviceOAuthAuthType, PublicMetadata: public, Secret: raw, AccessExpiresAt: &access, RefreshExpiresAt: &refresh}, nil
}

func parseByteCloudOAuthCredential(raw []byte, site string) (byteCloudOAuthCredential, error) {
	var credential byteCloudOAuthCredential
	if err := decodeStrictProviderJSON(raw, &credential); err != nil || credential.Version != 1 || credential.Site != site ||
		!validProviderIdentifier(credential.AppID, 512) || !validOpaqueToken(credential.DeviceCode, 128) ||
		!validOpaqueToken(credential.AccessToken, maximumHeaderValueBytes) || !validOpaqueToken(credential.RefreshToken, maximumHeaderValueBytes) ||
		credential.TokenType != "Bearer" || credential.GrantedAt.IsZero() || credential.AccessExpiresAt.IsZero() ||
		credential.RefreshExpiresAt.IsZero() || !credential.RefreshExpiresAt.After(credential.AccessExpiresAt) ||
		len(credential.Scope) > 4096 || len(credential.Username) > 512 || strings.ContainsAny(credential.Scope+credential.Username, "\x00\r\n") {
		return byteCloudOAuthCredential{}, errors.New("ByteCloud OAuth credential envelope is invalid")
	}
	return credential, nil
}

func (provider ByteCloudProvider) doByteCloudJSON(ctx context.Context, path, deviceCode string, payload any, target any) (int, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.device.apiBaseURL+path, strings.NewReader(string(raw)))
	clear(raw)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "agentserver-v2/credential-device-flow")
	request.Header.Set("x-real-psm", "bytecloud.auth."+deviceCode)
	return doProviderJSON(provider.device.httpClient, request, target)
}

func validateProviderOrigin(raw string, allowInsecure bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("provider origin is invalid")
	}
	if parsed.Scheme != "https" && !(allowInsecure && parsed.Scheme == "http") {
		return errors.New("provider origin must use HTTPS")
	}
	return nil
}

func parseProviderExpiry(now time.Time, raw int64, fallback time.Duration) time.Time {
	if raw <= 0 {
		return now.Add(fallback)
	}
	if raw > now.Unix()+300 {
		return time.Unix(raw, 0).UTC()
	}
	return now.Add(time.Duration(raw) * time.Second)
}

func byteCloudAccessExpiry(now time.Time, token string, responseExpiry int64) time.Time {
	if expiry, ok := jwtExpiry(token); ok {
		return expiry
	}
	return parseProviderExpiry(now, responseExpiry, time.Hour)
}

func jwtExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) > 64*1024 {
		return time.Time{}, false
	}
	defer clear(payload)
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, false
	}
	var number json.Number
	decoder := json.NewDecoder(strings.NewReader(string(claims["exp"])))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return time.Time{}, false
	}
	seconds, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0).UTC(), true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func boundedProviderText(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if len(value) > maximum || strings.ContainsAny(value, "\x00\r\n") {
		return ""
	}
	return value
}
