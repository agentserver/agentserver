package corecredentials

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBearerProviderRejectsUnknownJSONEnvelopeFields(t *testing.T) {
	provider := NewLarkProvider()
	for _, raw := range []string{
		`{"foo":"bar"}`,
		`{"accessToken":"token","token":"other"}`,
		`{"accessToken":""}`,
		`{"accessToken":"token"} {"token":"other"}`,
	} {
		if _, err := provider.ValidateUpload("static", []byte(raw)); err == nil {
			t.Fatalf("bearer envelope %s was accepted", raw)
		}
	}
	if upload, err := provider.ValidateUpload("static", []byte(`{"accessToken":"valid-token"}`)); err != nil || string(upload.Secret) != "valid-token" {
		t.Fatalf("valid bearer envelope = %#v, %v", upload, err)
	}
	if upload, err := provider.ValidateUpload("static", []byte("opaque-token")); err != nil || string(upload.Secret) != "opaque-token" {
		t.Fatalf("valid opaque bearer = %#v, %v", upload, err)
	}
}

func TestBearerProviderMaterializeRequiresStaticBindingAndCanonicalToken(t *testing.T) {
	provider := NewLarkProvider()
	request := UseRequest{Host: "open.feishu.cn"}
	for _, binding := range []Binding{
		{Kind: "lark", AuthType: "aksk"},
		{Kind: "github", AuthType: "static"},
	} {
		if _, err := provider.Materialize(context.Background(), binding, []byte("token"), request); err == nil {
			t.Fatalf("binding %#v was accepted", binding)
		}
	}
	for _, raw := range []string{"token with spaces", "token\nvalue", `{"foo":"bar"}`} {
		if _, err := provider.Materialize(context.Background(), Binding{Kind: "lark", AuthType: "static"}, []byte(raw), request); err == nil {
			t.Fatalf("invalid bearer %q was materialized", raw)
		}
	}
}

func TestByteCloudProviderRequiresStrictAKSKEnvelope(t *testing.T) {
	provider := NewByteCloudProvider("cloud-i18n-sg.bytedance.net", func(context.Context, string, string) (string, time.Time, error) {
		return "jwt-value", time.Now().Add(time.Hour), nil
	})
	for _, raw := range []string{
		`{"accessKeyId":"ak","secretAccessKey":"sk","extra":"nope"}`,
		`{"accessKeyId":"","secretAccessKey":"sk"}`,
		`{"accessKeyId":"ak"}`,
		`{"accessKeyId":"ak","secretAccessKey":"sk"} {}`,
	} {
		if _, err := provider.ValidateUpload("aksk", []byte(raw)); err == nil {
			t.Fatalf("invalid ByteCloud envelope %s was accepted", raw)
		}
	}
	upload, err := provider.ValidateUpload("aksk", []byte(`{"accessKeyId":"ak","secretAccessKey":"sk"}`))
	if err != nil || upload.AuthType != "aksk" {
		t.Fatalf("valid ByteCloud envelope = %#v, %v", upload, err)
	}
	request := UseRequest{Host: "cloud-i18n-sg.bytedance.net"}
	mutation, err := provider.Materialize(context.Background(), Binding{Kind: "bytecloud", AuthType: "aksk"}, upload.Secret, request)
	if err != nil || mutation.Headers["X-Jwt-Token"] != "jwt-value" {
		t.Fatalf("ByteCloud mutation = %#v, %v", mutation, err)
	}
}

func TestGitHubAppEnvelopeRequiresKnownFields(t *testing.T) {
	provider := NewGitHubProvider()
	provider.InstallationMinter = func(context.Context, Binding, []byte, UseRequest) (string, time.Time, error) {
		return "token", time.Now().Add(time.Hour), nil
	}
	if _, err := provider.ValidateUpload("github_app_installation", []byte(`{"foo":"bar"}`)); err == nil {
		t.Fatal("unknown GitHub App envelope was accepted")
	}
	secret := map[string]string{"appId": "123", "installationId": "456", "privateKey": strings.Repeat("x", 64) + "BEGIN"}
	raw, _ := json.Marshal(secret)
	if _, err := provider.ValidateUpload("github_app_installation", raw); err != nil {
		t.Fatalf("valid GitHub App envelope rejected: %v", err)
	}
}
