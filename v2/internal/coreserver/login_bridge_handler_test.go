package coreserver

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestLoginBridgeHandlerSetsHostCookieAndClearsItAfterCallback(t *testing.T) {
	bridge, _, hydra, provider := newLoginBridgeFixture(t)
	workload := &recordingRunAttemptAuthorizer{}
	handler, err := NewLoginBridgeHandler(workload, bridge)
	if err != nil {
		t.Fatal(err)
	}
	loginRequest := httptest.NewRequest(
		http.MethodGet,
		"https://core.internal"+corecontract.HydraLoginBridgePath+"?login_challenge=login-challenge",
		nil,
	)
	loginResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusFound || workload.action != "auth.login" ||
		!strings.HasPrefix(loginResponse.Header().Get("Location"), "https://idp.example/authorize?") {
		t.Fatalf("login response = %d headers=%v action=%q", loginResponse.Code, loginResponse.Header(), workload.action)
	}
	cookies := loginResponse.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != LoginBridgeCookieName || cookies[0].Value == "" ||
		!cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Path != "/" || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("login transaction cookies = %+v", cookies)
	}

	callbackRequest := httptest.NewRequest(
		http.MethodGet,
		"https://core.internal"+corecontract.OIDCCallbackBridgePath+"?state="+provider.state+"&code=external-code&scope=openid&iss=https%3A%2F%2Fidp.example",
		nil,
	)
	callbackRequest.AddCookie(cookies[0])
	callbackResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(callbackResponse, callbackRequest)
	if callbackResponse.Code != http.StatusFound || workload.action != "auth.callback" ||
		callbackResponse.Header().Get("Location") != "https://browser.example/oauth2/auth?login_verifier=accepted" ||
		hydra.acceptLoginCalls != 1 {
		t.Fatalf("callback response = %d headers=%v action=%q hydra=%+v", callbackResponse.Code, callbackResponse.Header(), workload.action, hydra)
	}
	cleared := callbackResponse.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != LoginBridgeCookieName || cleared[0].MaxAge >= 0 || !cleared[0].Secure || !cleared[0].HttpOnly {
		t.Fatalf("cleared callback cookies = %+v", cleared)
	}
}

func TestLoginBridgeHandlerRejectsDuplicateCallbackStateBeforeConsumption(t *testing.T) {
	bridge, store, hydra, provider := newLoginBridgeFixture(t)
	started, err := bridge.BeginLogin(t.Context(), "login-challenge", "")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewLoginBridgeHandler(&recordingRunAttemptAuthorizer{}, bridge)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"https://core.internal"+corecontract.OIDCCallbackBridgePath+"?state="+provider.state+"&state=second&code=external-code",
		nil,
	)
	request.AddCookie(&http.Cookie{Name: LoginBridgeCookieName, Value: started.BrowserBinding})
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || store.login.Status != "pending" || provider.exchangeCalls != 0 || hydra.acceptLoginCalls != 0 {
		t.Fatalf("duplicate state response=%d stored=%+v provider=%+v hydra=%+v", response.Code, store.login, provider, hydra)
	}
}

func TestLoginBridgeHandlerRejectsMalformedQueryBeforeHydra(t *testing.T) {
	bridge, store, hydra, provider := newLoginBridgeFixture(t)
	handler, err := NewLoginBridgeHandler(&recordingRunAttemptAuthorizer{}, bridge)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"https://core.internal"+corecontract.HydraLoginBridgePath+"?login_challenge=login-challenge",
		nil,
	)
	request.URL.RawQuery = "login_challenge=login-challenge&discard=%zz"
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || store.login.ID != "" || provider.state != "" ||
		hydra.acceptLoginCalls != 0 || hydra.rejectLoginCalls != 0 {
		t.Fatalf("malformed query response=%d stored=%+v provider=%+v hydra=%+v", response.Code, store.login, provider, hydra)
	}
}

func TestLoginBridgeHandlerLogsFailureStageWithoutAuthorizationSecrets(t *testing.T) {
	bridge, _, _, provider := newLoginBridgeFixture(t)
	started, err := bridge.BeginLogin(t.Context(), "login-challenge", "")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewLoginBridgeHandler(&recordingRunAttemptAuthorizer{}, bridge)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	handler.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	request := httptest.NewRequest(
		http.MethodGet,
		"https://core.internal"+corecontract.OIDCCallbackBridgePath+"?state="+provider.state+"&state=secret-duplicate-state&code=secret-code",
		nil,
	)
	request.AddCookie(&http.Cookie{Name: LoginBridgeCookieName, Value: started.BrowserBinding})
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	logged := logs.String()
	if response.Code != http.StatusBadRequest || !strings.Contains(logged, `"operation":"callback"`) ||
		!strings.Contains(logged, `"stage":"state"`) || !strings.Contains(logged, `"status":400`) {
		t.Fatalf("failure response=%d log=%s", response.Code, logged)
	}
	for _, secret := range []string{provider.state, "secret-duplicate-state", "secret-code", started.BrowserBinding} {
		if strings.Contains(logged, secret) {
			t.Fatalf("authorization secret %q crossed the diagnostic log boundary: %s", secret, logged)
		}
	}

	redacted := safeLoginBridgeLogError(errors.New(`GET "https://hydra.internal/oauth2/auth?login_challenge=secret-challenge": timeout`))
	if strings.Contains(redacted, "secret-challenge") || !strings.Contains(redacted, "?<redacted>") {
		t.Fatalf("Hydra URL query was not redacted: %q", redacted)
	}
}
