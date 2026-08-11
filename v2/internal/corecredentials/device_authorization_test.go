package corecredentials

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestLarkDeviceAuthorizationLifecycle(t *testing.T) {
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	polls := 0
	client := &http.Client{Transport: credentialRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/oauth/v1/device_authorization":
			if request.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("cli_app:app-secret")) {
				t.Fatal("Lark device begin omitted the application Basic authority")
			}
			form := readCredentialForm(t, request)
			if form.Get("client_id") != "cli_app" || form.Get("scope") != "docx:document:readonly offline_access search:docs:read" {
				t.Fatalf("Lark device begin form = %v", form)
			}
			return credentialJSONResponse(http.StatusOK, `{"device_code":"device-secret","user_code":"ABCD-EFGH","verification_uri":"https://accounts.feishu.cn/device","verification_uri_complete":"https://accounts.feishu.cn/device?code=ABCD-EFGH","expires_in":240,"interval":5}`), nil
		case "/open-apis/authen/v2/oauth/token":
			form := readCredentialForm(t, request)
			if form.Get("client_id") != "cli_app" || form.Get("client_secret") != "app-secret" {
				t.Fatalf("Lark token form omitted application authority: %v", form)
			}
			if form.Get("grant_type") == "urn:ietf:params:oauth:grant-type:device_code" {
				polls++
				if polls == 1 {
					return credentialJSONResponse(http.StatusBadRequest, `{"error":"authorization_pending"}`), nil
				}
				return credentialJSONResponse(http.StatusOK, `{"access_token":"lark-access-1","refresh_token":"lark-refresh-1","expires_in":7200,"refresh_token_expires_in":604800,"scope":"search:docs:read docx:document:readonly offline_access"}`), nil
			}
			if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "lark-refresh-1" {
				t.Fatalf("Lark refresh form = %v", form)
			}
			return credentialJSONResponse(http.StatusOK, `{"access_token":"lark-access-2","refresh_token":"lark-refresh-2","expires_in":7200,"refresh_token_expires_in":604800,"scope":"docx:document:readonly search:docs:read offline_access"}`), nil
		case "/open-apis/authen/v1/user_info":
			if request.Header.Get("Authorization") != "Bearer lark-access-1" {
				t.Fatalf("Lark user-info bearer = %q", request.Header.Get("Authorization"))
			}
			return credentialJSONResponse(http.StatusOK, `{"code":0,"data":{"open_id":"ou_agentserver","name":"Agent Server"}}`), nil
		default:
			t.Fatalf("unexpected Lark device-flow request %s", request.URL)
			return nil, nil
		}
	})}
	provider, err := NewLarkDeviceFlowProvider(LarkDeviceFlowConfig{
		AppID: "cli_app", AppSecret: "app-secret", HTTPClient: client, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if schema := provider.Schema(); !containsText(schema.AuthorizationMethods, AuthorizationMethodDeviceFlow) || !containsText(schema.AuthTypes, larkDeviceOAuthAuthType) {
		t.Fatalf("Lark device schema = %+v", schema)
	}
	challenge, err := provider.BeginDeviceAuthorization(t.Context(), nil)
	if err != nil || challenge.UserCode != "ABCD-EFGH" || challenge.Interval != 5*time.Second || !challenge.ExpiresAt.Equal(now.Add(4*time.Minute)) {
		t.Fatalf("Lark challenge = %+v, %v", challenge, err)
	}
	pending, err := provider.PollDeviceAuthorization(t.Context(), challenge.ProviderState)
	if err != nil || pending.Status != DeviceAuthorizationPending {
		t.Fatalf("Lark pending poll = %+v, %v", pending, err)
	}
	succeeded, err := provider.PollDeviceAuthorization(t.Context(), challenge.ProviderState)
	if err != nil || succeeded.Status != DeviceAuthorizationSucceeded || succeeded.Credential.AuthType != larkDeviceOAuthAuthType {
		t.Fatalf("Lark successful poll = %+v, %v", succeeded, err)
	}
	credential, err := parseLarkOAuthCredential(succeeded.Credential.Secret, "cli_app")
	if err != nil || credential.UserOpenID != "ou_agentserver" || credential.UserName != "Agent Server" {
		t.Fatalf("Lark credential = %+v, %v", credential, err)
	}
	mutation, err := provider.Materialize(t.Context(), Binding{Kind: "lark", AuthType: larkDeviceOAuthAuthType}, succeeded.Credential.Secret, UseRequest{Host: "open.feishu.cn"})
	if err != nil || mutation.Headers["Authorization"] != "Bearer lark-access-1" {
		t.Fatalf("Lark materialization = %+v, %v", mutation, err)
	}
	refreshed, terminal, err := provider.RefreshDeviceCredential(t.Context(), Binding{Kind: "lark", AuthType: larkDeviceOAuthAuthType}, succeeded.Credential.Secret)
	if err != nil || terminal {
		t.Fatalf("Lark refresh = terminal %t, %v", terminal, err)
	}
	refreshCredential, err := parseLarkOAuthCredential(refreshed.Secret, "cli_app")
	if err != nil || refreshCredential.AccessToken != "lark-access-2" || refreshCredential.RefreshToken != "lark-refresh-2" {
		t.Fatalf("refreshed Lark credential = %+v, %v", refreshCredential, err)
	}
}

func TestByteCloudDeviceAuthorizationLifecycle(t *testing.T) {
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	deviceCode := strings.Repeat("01", 16)
	client := &http.Client{Transport: credentialRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("x-real-psm") != "bytecloud.auth."+deviceCode {
			t.Fatalf("ByteCloud x-real-psm = %q", request.Header.Get("x-real-psm"))
		}
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch request.URL.Path {
		case byteCloudCLIRegistrationPath:
			if body["device_code"] != deviceCode {
				t.Fatalf("ByteCloud registration body = %v", body)
			}
			return credentialJSONResponse(http.StatusOK, `{"code":0,"message":"ok","data":{"code":"BC-CODE","ticket":"ticket-secret","expire_at":600,"service_account_create_url":"https://cloud.tiktok-row.net/device"}}`), nil
		case byteCloudCLIPollingPath:
			if body["ticket"] != "ticket-secret" {
				t.Fatalf("ByteCloud polling body = %v", body)
			}
			return credentialJSONResponse(http.StatusOK, `{"code":0,"message":"ok","data":{"token_info":{"access_token":"bytecloud-access-1","token_type":"Bearer","refresh_token":"bytecloud-refresh-1","expires_in":3600,"refresh_token_expire_in":7200,"scope":"openid"},"username":"zhangyao.dev","app_id":"app-bytecloud"}}`), nil
		case byteCloudRefreshPath:
			if body["refresh_token"] != "bytecloud-refresh-1" {
				t.Fatalf("ByteCloud refresh body = %v", body)
			}
			return credentialJSONResponse(http.StatusOK, `{"code":0,"message":"ok","data":{"access_token":"bytecloud-access-2","refresh_token":"bytecloud-refresh-2","expire_at":3600,"refresh_token_expire_in":7200}}`), nil
		default:
			t.Fatalf("unexpected ByteCloud device-flow request %s", request.URL)
			return nil, nil
		}
	})}
	provider, err := NewByteCloudDeviceFlowProvider("cloud-i18n-sg.bytedance.net", func(context.Context, string, string) (string, time.Time, error) {
		return "", time.Time{}, nil
	}, ByteCloudDeviceFlowConfig{
		APIBaseURL: "https://cloud.tiktok-row.net", HTTPClient: client,
		Now: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{1}, 16)),
	})
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := provider.BeginDeviceAuthorization(t.Context(), json.RawMessage(`{}`))
	if err != nil || challenge.UserCode != "BC-CODE" || !challenge.ExpiresAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("ByteCloud challenge = %+v, %v", challenge, err)
	}
	succeeded, err := provider.PollDeviceAuthorization(t.Context(), challenge.ProviderState)
	if err != nil || succeeded.Status != DeviceAuthorizationSucceeded {
		t.Fatalf("ByteCloud poll = %+v, %v", succeeded, err)
	}
	credential, err := parseByteCloudOAuthCredential(succeeded.Credential.Secret, byteCloudDefaultSite)
	if err != nil || credential.Username != "zhangyao.dev" || credential.AppID != "app-bytecloud" {
		t.Fatalf("ByteCloud credential = %+v, %v", credential, err)
	}
	mutation, err := provider.Materialize(t.Context(), Binding{Kind: "bytecloud", AuthType: byteCloudDeviceOAuthAuthType}, succeeded.Credential.Secret, UseRequest{Host: "cloud-i18n-sg.bytedance.net"})
	if err != nil || mutation.Headers["X-Jwt-Token"] != "bytecloud-access-1" {
		t.Fatalf("ByteCloud materialization = %+v, %v", mutation, err)
	}
	refreshed, terminal, err := provider.RefreshDeviceCredential(t.Context(), Binding{Kind: "bytecloud", AuthType: byteCloudDeviceOAuthAuthType}, succeeded.Credential.Secret)
	if err != nil || terminal {
		t.Fatalf("ByteCloud refresh = terminal %t, %v", terminal, err)
	}
	refreshCredential, err := parseByteCloudOAuthCredential(refreshed.Secret, byteCloudDefaultSite)
	if err != nil || refreshCredential.AccessToken != "bytecloud-access-2" || refreshCredential.RefreshToken != "bytecloud-refresh-2" {
		t.Fatalf("refreshed ByteCloud credential = %+v, %v", refreshCredential, err)
	}
}

func TestDefaultByteCloudDeviceAPIUsesI18NTTProductionGateway(t *testing.T) {
	if DefaultByteCloudDeviceAPIBaseURL != "https://paas-gw-i18n.byted.org" {
		t.Fatalf("default ByteCloud device API = %q", DefaultByteCloudDeviceAPIBaseURL)
	}
}

func TestAuthorizationCiphertextCannotBeOpenedAsBindingSecret(t *testing.T) {
	keyring, err := NewKeyring("key-1", map[string][]byte{"key-1": bytes.Repeat([]byte{7}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	authorizationScope := AuthorizationSealScope{WorkspaceID: "workspace-1", AuthorizationID: "authorization-1", ProviderStateVersion: 1}
	sealed, err := keyring.SealAuthorization(authorizationScope, []byte(`{"deviceCode":"provider-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keyring.Open(BindingSealScope{WorkspaceID: "workspace-1", BindingID: "authorization-1", CredentialVersion: 1}, sealed); err == nil {
		t.Fatal("authorization provider state was replayable as binding secret material")
	}
	opened, err := keyring.OpenAuthorization(authorizationScope, sealed)
	if err != nil || string(opened) != `{"deviceCode":"provider-secret"}` {
		t.Fatalf("authorization state round trip = %q, %v", opened, err)
	}
}

type credentialRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip credentialRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func credentialJSONResponse(status int, raw string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(raw)),
	}
}

func readCredentialForm(t *testing.T, request *http.Request) url.Values {
	t.Helper()
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	form, err := url.ParseQuery(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return form
}

func containsText(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
