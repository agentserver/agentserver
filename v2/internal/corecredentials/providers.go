package corecredentials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// BearerProvider implements a static Lark/GitHub bearer credential. The secret
// may be a bounded opaque token or a JSON object containing accessToken/token;
// neither form is ever returned by the provider API. OAuth refresh and GitHub
// App minting are separate adapters and are not advertised by this default
// provider unless explicitly configured.
type BearerProvider struct {
	KindValue string
	HostValue string
}

func (provider BearerProvider) Kind() string { return provider.KindValue }

func (provider BearerProvider) AllowedHeaders() []string { return []string{"Authorization"} }

func (provider BearerProvider) Schema() ProviderSchema {
	return ProviderSchema{
		Kind: provider.KindValue, DisplayName: provider.KindValue,
		AuthTypes:      []string{"static"},
		AllowedHosts:   []string{provider.HostValue},
		AllowedHeaders: provider.AllowedHeaders(), SecretFormat: "opaque-token-or-json",
		AuthorizationMethods: []string{AuthorizationMethodManual},
	}
}

func (provider BearerProvider) ValidateUpload(authType string, raw []byte) (UploadResult, error) {
	if strings.TrimSpace(authType) == "" {
		authType = "static"
	}
	if authType != "static" {
		return UploadResult{}, errors.New("bearer provider does not support the requested auth type")
	}
	secret, err := parseBearerSecret(raw)
	if err != nil {
		return UploadResult{}, err
	}
	public := json.RawMessage(`{}`)
	return UploadResult{AuthType: "static", PublicMetadata: public, Secret: []byte(secret)}, nil
}

func (provider BearerProvider) Materialize(_ context.Context, binding Binding, secret []byte, request UseRequest) (HeaderMutation, error) {
	if provider.KindValue == "" || binding.Kind != provider.KindValue || binding.AuthType != "static" || request.Host != provider.HostValue {
		return HeaderMutation{}, errors.New("bearer provider binding or host mismatch")
	}
	value, err := parseBearerSecret(secret)
	if err != nil {
		return HeaderMutation{}, err
	}
	return HeaderMutation{Headers: map[string]string{"Authorization": "Bearer " + value}}, nil
}

// GitHubProvider supports PAT/OAuth bearer material. GitHub App installation
// token minting is represented by the same Materialize contract and can be
// supplied through a TokenMinter without changing the sandbox contract.
type GitHubProvider struct {
	BearerProvider
	InstallationMinter func(context.Context, Binding, []byte, UseRequest) (string, time.Time, error)
}

func NewGitHubProvider() GitHubProvider {
	return GitHubProvider{BearerProvider: BearerProvider{KindValue: "github", HostValue: "api.github.com"}}
}

func (provider GitHubProvider) Materialize(ctx context.Context, binding Binding, secret []byte, request UseRequest) (HeaderMutation, error) {
	if provider.InstallationMinter != nil && binding.AuthType == "github_app_installation" {
		token, expiry, err := provider.InstallationMinter(ctx, binding, secret, request)
		if err != nil {
			return HeaderMutation{}, err
		}
		if !validOpaqueToken(token, maximumHeaderValueBytes) || expiry.Before(time.Now().UTC().Add(time.Second)) {
			return HeaderMutation{}, errors.New("GitHub installation token is not ready")
		}
		return HeaderMutation{Headers: map[string]string{"Authorization": "Bearer " + token}}, nil
	}
	if binding.AuthType != "static" {
		return HeaderMutation{}, errors.New("GitHub credential auth type is not supported by the configured provider")
	}
	return provider.BearerProvider.Materialize(ctx, binding, secret, request)
}

func (provider GitHubProvider) Schema() ProviderSchema {
	schema := provider.BearerProvider.Schema()
	schema.DisplayName = "GitHub"
	if provider.InstallationMinter != nil {
		schema.AuthTypes = append(schema.AuthTypes, "github_app_installation")
	}
	return schema
}

func (provider GitHubProvider) ValidateUpload(authType string, raw []byte) (UploadResult, error) {
	if strings.TrimSpace(authType) == "" {
		authType = "static"
	}
	if authType == "github_app_installation" {
		if provider.InstallationMinter == nil {
			return UploadResult{}, errors.New("GitHub App installation credentials are not configured")
		}
		if err := validateGitHubAppSecret(raw); err != nil {
			return UploadResult{}, err
		}
		return UploadResult{AuthType: authType, PublicMetadata: json.RawMessage(`{}`), Secret: append([]byte(nil), raw...)}, nil
	}
	return provider.BearerProvider.ValidateUpload(authType, raw)
}

// ByteCloudProvider intentionally requires a TokenExchanger. It never sends
// workspace AK/SK to a sandbox and never treats the TAE control-plane
// application identity as a workspace credential.
type ByteCloudProvider struct {
	HostValue      string
	TokenExchanger func(context.Context, string, string) (token string, expiry time.Time, err error)
	device         *byteCloudDeviceFlowClient
}

// NewDefaultRegistry returns the provider catalog used by the SG deployment.
// A nil exchanger uses the pinned SG ByteCloud Auth SDK adapter. This is an
// in-process provider implementation in v2 Core, not a separately deployed
// corecredentials service; workspace AK/SK remain sealed and are exchanged
// only for the one-hop header mutation.
func NewDefaultRegistry(byteCloudHost string, exchanger func(context.Context, string, string) (string, time.Time, error)) (*ProviderRegistry, error) {
	return NewConfiguredRegistry(DefaultRegistryConfig{ByteCloudHost: byteCloudHost, ByteCloudTokenExchanger: exchanger})
}

type DefaultRegistryConfig struct {
	ByteCloudHost           string
	ByteCloudTokenExchanger func(context.Context, string, string) (string, time.Time, error)
	LarkDeviceFlow          *LarkDeviceFlowConfig
	ByteCloudDeviceFlow     *ByteCloudDeviceFlowConfig
}

func NewConfiguredRegistry(config DefaultRegistryConfig) (*ProviderRegistry, error) {
	byteCloudHost := config.ByteCloudHost
	exchanger := config.ByteCloudTokenExchanger
	if byteCloudHost == "" {
		byteCloudHost = "cloud-i18n-sg.bytedance.net"
	}
	if exchanger == nil {
		defaultExchanger, err := NewByteCloudTokenExchanger(0, nil)
		if err != nil {
			return nil, err
		}
		exchanger = defaultExchanger.Exchange
	}
	lark := NewLarkProvider()
	var err error
	if config.LarkDeviceFlow != nil {
		lark, err = NewLarkDeviceFlowProvider(*config.LarkDeviceFlow)
		if err != nil {
			return nil, err
		}
	}
	byteCloud := NewByteCloudProvider(byteCloudHost, exchanger)
	if config.ByteCloudDeviceFlow != nil {
		byteCloud, err = NewByteCloudDeviceFlowProvider(byteCloudHost, exchanger, *config.ByteCloudDeviceFlow)
		if err != nil {
			return nil, err
		}
	}
	return NewRegistry(lark, NewGitHubProvider(), byteCloud)
}

func NewByteCloudProvider(host string, exchanger func(context.Context, string, string) (string, time.Time, error)) ByteCloudProvider {
	return ByteCloudProvider{HostValue: host, TokenExchanger: exchanger}
}

func (provider ByteCloudProvider) Kind() string { return "bytecloud" }

func (provider ByteCloudProvider) AllowedHeaders() []string { return []string{"X-Jwt-Token"} }

func (provider ByteCloudProvider) Schema() ProviderSchema {
	schema := ProviderSchema{
		Kind: provider.Kind(), DisplayName: "ByteCloud",
		AuthTypes:      []string{"aksk"},
		AllowedHosts:   []string{provider.HostValue},
		AllowedHeaders: provider.AllowedHeaders(), SecretFormat: "json-access-key-secret-key",
		AuthorizationMethods: []string{AuthorizationMethodManual},
	}
	if provider.device != nil {
		schema.AuthTypes = append(schema.AuthTypes, byteCloudDeviceOAuthAuthType)
		schema.AuthorizationMethods = append(schema.AuthorizationMethods, AuthorizationMethodDeviceFlow)
		schema.SecretFormat = "json-aksk-or-bytecloud-device-oauth-envelope"
	}
	return schema
}

func (provider ByteCloudProvider) ValidateUpload(authType string, raw []byte) (UploadResult, error) {
	if strings.TrimSpace(authType) == "" {
		authType = "aksk"
	}
	if authType == byteCloudDeviceOAuthAuthType && provider.device != nil {
		return provider.validateByteCloudOAuthCredential(raw)
	}
	if authType != "aksk" {
		return UploadResult{}, errors.New("ByteCloud provider requires the aksk auth type")
	}
	var document struct {
		AccessKeyID     string `json:"accessKeyId"`
		SecretAccessKey string `json:"secretAccessKey"`
	}
	if err := decodeByteCloudSecret(raw, &document); err != nil {
		return UploadResult{}, err
	}
	public, _ := json.Marshal(map[string]string{"site": "i18n-tt"})
	return UploadResult{AuthType: "aksk", PublicMetadata: public, Secret: append([]byte(nil), raw...)}, nil
}

func (provider ByteCloudProvider) Materialize(ctx context.Context, binding Binding, secret []byte, request UseRequest) (HeaderMutation, error) {
	if request.Host != provider.HostValue || binding.Kind != provider.Kind() || provider.TokenExchanger == nil {
		return HeaderMutation{}, errors.New("ByteCloud provider is not configured for this host")
	}
	if binding.AuthType == byteCloudDeviceOAuthAuthType {
		if provider.device == nil {
			return HeaderMutation{}, errors.New("ByteCloud device OAuth is not configured")
		}
		credential, err := parseByteCloudOAuthCredential(secret, provider.device.site)
		if err != nil {
			return HeaderMutation{}, errors.New("ByteCloud OAuth credential envelope is invalid")
		}
		return HeaderMutation{Headers: map[string]string{"X-Jwt-Token": credential.AccessToken}}, nil
	}
	if binding.AuthType != "aksk" {
		return HeaderMutation{}, errors.New("ByteCloud credential auth type is not supported")
	}
	var document struct {
		AccessKeyID     string `json:"accessKeyId"`
		SecretAccessKey string `json:"secretAccessKey"`
	}
	if err := decodeByteCloudSecret(secret, &document); err != nil {
		return HeaderMutation{}, errors.New("ByteCloud credential envelope is invalid")
	}
	token, expiry, err := provider.TokenExchanger(ctx, document.AccessKeyID, document.SecretAccessKey)
	// Clear parsed values before returning; callers should also clear their
	// envelope as soon as Materialize returns.
	document.AccessKeyID, document.SecretAccessKey = "", ""
	if err != nil {
		return HeaderMutation{}, fmt.Errorf("exchange ByteCloud workspace credential: %w", err)
	}
	if !validOpaqueToken(token, maximumHeaderValueBytes) || !expiry.After(time.Now().UTC().Add(time.Second)) {
		return HeaderMutation{}, errors.New("ByteCloud JWT is empty or expiring")
	}
	return HeaderMutation{Headers: map[string]string{"X-Jwt-Token": token}}, nil
}

// parseBearerSecret accepts either a bounded opaque bearer or the deliberately
// small JSON envelope used by integrations that already store a token object.
// A JSON-looking value is never silently downgraded to an opaque token: this
// prevents {"unexpected":"value"} from becoming a live credential after a
// schema typo or provider upgrade.
func parseBearerSecret(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > maximumHeaderValueBytes {
		return "", errors.New("bearer credential is empty or exceeds bounds")
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || len(trimmed) > maximumHeaderValueBytes {
		return "", errors.New("bearer credential is empty or exceeds bounds")
	}
	if trimmed[0] == '{' {
		value, err := parseBearerJSONEnvelope(trimmed)
		if err != nil {
			return "", err
		}
		return value, nil
	}
	// Arrays, quoted JSON strings, and scalar JSON values are almost always an
	// envelope shape error. Reject them instead of accepting their textual
	// representation as a token.
	if trimmed[0] == '[' || trimmed[0] == '"' || bytes.Equal(trimmed, []byte("null")) ||
		bytes.Equal(trimmed, []byte("true")) || bytes.Equal(trimmed, []byte("false")) {
		return "", errors.New("bearer credential JSON envelope is invalid")
	}
	value := string(trimmed)
	if !validOpaqueToken(value, maximumHeaderValueBytes) {
		return "", errors.New("bearer credential is not a valid opaque token")
	}
	return value, nil
}

func parseBearerJSONEnvelope(raw []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope struct {
		AccessToken *string `json:"accessToken"`
		Token       *string `json:"token"`
	}
	if err := decoder.Decode(&envelope); err != nil {
		return "", errors.New("bearer credential JSON envelope is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("bearer credential JSON envelope contains trailing data")
	}
	if (envelope.AccessToken == nil) == (envelope.Token == nil) {
		return "", errors.New("bearer credential JSON envelope requires exactly one accessToken or token")
	}
	value := envelope.Token
	if envelope.AccessToken != nil {
		value = envelope.AccessToken
	}
	if value == nil || !validOpaqueToken(*value, maximumHeaderValueBytes) {
		return "", errors.New("bearer credential JSON envelope token is invalid")
	}
	return *value, nil
}

func decodeByteCloudSecret(raw []byte, destination *struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
}) error {
	if destination == nil || len(raw) == 0 || len(raw) > 64*1024 {
		return errors.New("ByteCloud credential envelope is empty or exceeds bounds")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("ByteCloud credential requires accessKeyId and secretAccessKey")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("ByteCloud credential envelope contains trailing data")
	}
	if err := validateByteCloudAKSK(destination.AccessKeyID, destination.SecretAccessKey); err != nil {
		return errors.New("ByteCloud credential requires valid accessKeyId and secretAccessKey")
	}
	return nil
}

func validateGitHubAppSecret(raw []byte) error {
	if len(raw) == 0 || len(raw) > 64*1024 {
		return errors.New("GitHub App credential envelope is empty or exceeds bounds")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope struct {
		AppID          string `json:"appId"`
		InstallationID string `json:"installationId"`
		PrivateKey     string `json:"privateKey"`
	}
	if err := decoder.Decode(&envelope); err != nil {
		return errors.New("GitHub App credential envelope is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("GitHub App credential envelope contains trailing data")
	}
	if !validOpaqueToken(envelope.AppID, 256) || !validOpaqueToken(envelope.InstallationID, 256) ||
		!validGitHubPrivateKey(envelope.PrivateKey) {
		return errors.New("GitHub App credential envelope requires appId, installationId, and privateKey")
	}
	return nil
}

func validGitHubPrivateKey(value string) bool {
	return len(value) >= 64 && len(value) <= 64*1024 && strings.Contains(value, "BEGIN") &&
		!strings.ContainsAny(value, "\x00\r")
}

func validOpaqueToken(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value || strings.ContainsAny(value, " \t\r\n\x00") {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}
