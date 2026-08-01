package llmproxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHandlerAuthorizesEveryRequestAndReplacesRunBearerAtUpstream(t *testing.T) {
	type upstreamRequest struct {
		method  string
		path    string
		headers http.Header
		body    string
	}
	captured := make(chan upstreamRequest, 2)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		captured <- upstreamRequest{
			method: request.Method, path: request.URL.Path, headers: request.Header.Clone(), body: string(body),
		}
		response.Header().Set("Content-Type", "text/event-stream")
		response.Header().Set("X-Request-Id", "upstream-request")
		response.Header().Set("Set-Cookie", "must-not-cross=1")
		_, _ = response.Write([]byte("event: response.completed\ndata: {}\n\n"))
	}))
	defer upstream.Close()
	authenticator := &recordingModelAuthenticator{principal: testProxyPrincipal()}
	credentials := &recordingCredentialSource{credential: UpstreamCredential{
		HeaderName: "Authorization", HeaderValue: "Bearer upstream-secret",
	}}
	handler, err := NewHandler(HandlerConfig{
		Authenticator: authenticator, Credentials: credentials,
		UpstreamURL: upstream.URL + ResponsesPath, HTTPClient: upstream.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"model":"gpt-5.6-codex","stream":true,"input":[]}`
	for range 2 {
		request := newTLSModelRequest(body)
		request.Header.Set("Authorization", "Bearer run-capability")
		request.Header.Set("Accept", "text/event-stream")
		request.Header.Set("OpenAI-Beta", "responses=v1")
		request.Header.Set("Cookie", "must-not-cross=1")
		request.Header.Set("api-key", "client-controlled")
		request.Header.Set("X-Forwarded-For", "203.0.113.1")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" ||
			response.Header().Get("Content-Type") != "text/event-stream" ||
			response.Header().Get("X-Request-Id") != "upstream-request" || response.Header().Get("Set-Cookie") != "" ||
			response.Body.String() != "event: response.completed\ndata: {}\n\n" {
			t.Fatalf("proxied Responses result = status %d headers %v body %q", response.Code, response.Header(), response.Body.String())
		}
		wire := <-captured
		if wire.method != http.MethodPost || wire.path != ResponsesPath || wire.body != body ||
			wire.headers.Get("Authorization") != "Bearer upstream-secret" ||
			wire.headers.Get("api-key") != "" || wire.headers.Get("Cookie") != "" ||
			wire.headers.Get("X-Forwarded-For") != "" || wire.headers.Get("OpenAI-Beta") != "responses=v1" {
			t.Fatalf("upstream request = %+v", wire)
		}
	}
	if authenticator.callCount() != 2 || credentials.callCount() != 2 {
		t.Fatalf("per-request auth/credential calls = %d/%d", authenticator.callCount(), credentials.callCount())
	}
	for _, model := range authenticator.modelsSnapshot() {
		if model != testModel {
			t.Fatalf("authenticated model = %q", model)
		}
	}
}

func TestHandlerRejectsMalformedRequestBeforeAuthorization(t *testing.T) {
	for _, test := range []struct {
		name        string
		method      string
		target      string
		contentType string
		body        string
		tls         bool
		want        int
	}{
		{name: "TLS", method: http.MethodPost, target: ResponsesPath, contentType: "application/json", body: validProxyBody(), want: http.StatusBadRequest},
		{name: "method", method: http.MethodGet, target: ResponsesPath, contentType: "application/json", body: validProxyBody(), tls: true, want: http.StatusMethodNotAllowed},
		{name: "path", method: http.MethodPost, target: "/v1/chat/completions", contentType: "application/json", body: validProxyBody(), tls: true, want: http.StatusNotFound},
		{name: "query", method: http.MethodPost, target: ResponsesPath + "?key=value", contentType: "application/json", body: validProxyBody(), tls: true, want: http.StatusNotFound},
		{name: "media type", method: http.MethodPost, target: ResponsesPath, contentType: "application/json; charset=utf-8", body: validProxyBody(), tls: true, want: http.StatusUnsupportedMediaType},
		{name: "duplicate model", method: http.MethodPost, target: ResponsesPath, contentType: "application/json", body: `{"model":"gpt-5.6-codex","model":"other","stream":true}`, tls: true, want: http.StatusBadRequest},
		{name: "nonstream", method: http.MethodPost, target: ResponsesPath, contentType: "application/json", body: `{"model":"gpt-5.6-codex","stream":false}`, tls: true, want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			authenticator := &recordingModelAuthenticator{principal: testProxyPrincipal()}
			credentials := &recordingCredentialSource{credential: UpstreamCredential{HeaderName: "Authorization", HeaderValue: "Bearer secret"}}
			handler := newUnreachedUpstreamHandler(t, authenticator, credentials)
			request := httptest.NewRequest(test.method, "https://llmproxy.test"+test.target, strings.NewReader(test.body))
			if !test.tls {
				request.TLS = nil
			}
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want || authenticator.callCount() != 0 || credentials.callCount() != 0 {
				t.Fatalf("malformed request = status %d auth %d credentials %d", response.Code, authenticator.callCount(), credentials.callCount())
			}
		})
	}
}

func TestHandlerFailsClosedBeforeCredentialOrUpstream(t *testing.T) {
	for _, test := range []struct {
		name      string
		authErr   error
		credErr   error
		want      int
		wantCreds int
	}{
		{name: "unauthenticated", authErr: ErrUnauthenticated, want: http.StatusUnauthorized},
		{name: "not live", authErr: ErrForbidden, want: http.StatusForbidden},
		{name: "credential unavailable", credErr: errors.New("secret unavailable"), want: http.StatusServiceUnavailable, wantCreds: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var upstreamCalls atomic.Int64
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { upstreamCalls.Add(1) }))
			defer upstream.Close()
			authenticator := &recordingModelAuthenticator{principal: testProxyPrincipal(), err: test.authErr}
			credentials := &recordingCredentialSource{
				credential: UpstreamCredential{HeaderName: "Authorization", HeaderValue: "Bearer upstream-secret"}, err: test.credErr,
			}
			handler, err := NewHandler(HandlerConfig{
				Authenticator: authenticator, Credentials: credentials,
				UpstreamURL: upstream.URL + ResponsesPath, HTTPClient: upstream.Client(),
			})
			if err != nil {
				t.Fatal(err)
			}
			request := newTLSModelRequest(validProxyBody())
			request.Header.Set("Authorization", "Bearer run-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want || credentials.callCount() != test.wantCreds || upstreamCalls.Load() != 0 ||
				strings.Contains(response.Body.String(), "run-secret") || strings.Contains(response.Body.String(), "upstream-secret") {
				t.Fatalf("fail-closed result = status %d credentials %d upstream %d body %q", response.Code, credentials.callCount(), upstreamCalls.Load(), response.Body.String())
			}
		})
	}
}

func TestHandlerRejectsUpstreamRedirectWithoutFollowing(t *testing.T) {
	var redirected atomic.Int64
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/redirected" {
			redirected.Add(1)
			response.WriteHeader(http.StatusOK)
			return
		}
		response.Header().Set("Location", "/redirected")
		response.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()
	handler, err := NewHandler(HandlerConfig{
		Authenticator: &recordingModelAuthenticator{principal: testProxyPrincipal()},
		Credentials:   &recordingCredentialSource{credential: UpstreamCredential{HeaderName: "Authorization", HeaderValue: "Bearer upstream-secret"}},
		UpstreamURL:   upstream.URL + ResponsesPath, HTTPClient: upstream.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := newTLSModelRequest(validProxyBody())
	request.Header.Set("Authorization", "Bearer run-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || redirected.Load() != 0 || response.Header().Get("Location") != "" {
		t.Fatalf("redirect result = status %d followed %d headers %v", response.Code, redirected.Load(), response.Header())
	}
}

func TestHandlerRechecksHardRunDeadlineBeforeCredentialOrUpstream(t *testing.T) {
	now := time.Date(2026, 8, 2, 18, 0, 0, 0, time.UTC)
	principal := testProxyPrincipal()
	principal.RunDeadline = now
	authenticator := &recordingModelAuthenticator{principal: principal}
	credentials := &recordingCredentialSource{credential: UpstreamCredential{
		HeaderName: "Authorization", HeaderValue: "Bearer upstream-secret",
	}}
	handler := mustHandler(t, HandlerConfig{
		Authenticator: authenticator, Credentials: credentials,
		UpstreamURL: "https://upstream.example.test" + ResponsesPath,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("expired run reached upstream")
			return nil, errors.New("unreachable")
		})},
		Now: func() time.Time { return now },
	})
	request := newTLSModelRequest(validProxyBody())
	request.Header.Set("Authorization", "Bearer run-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || credentials.callCount() != 0 ||
		decodeProxyErrorCode(t, response) != "run_deadline_exceeded" {
		t.Fatalf("deadline result = status %d credentials %d body %q", response.Code, credentials.callCount(), response.Body.String())
	}
}

func TestHandlerValidatesConstruction(t *testing.T) {
	authenticator := &recordingModelAuthenticator{principal: testProxyPrincipal()}
	credentials := &recordingCredentialSource{credential: UpstreamCredential{HeaderName: "Authorization", HeaderValue: "Bearer secret"}}
	for _, upstream := range []string{
		"http://api.example.test/v1/responses",
		"https://user@api.example.test/v1/responses",
		"https://api.example.test/v1",
		"https://api.example.test/v1/responses?key=value",
	} {
		if _, err := NewHandler(HandlerConfig{
			Authenticator: authenticator, Credentials: credentials, UpstreamURL: upstream, HTTPClient: http.DefaultClient,
		}); err == nil {
			t.Fatalf("unsafe upstream %q was accepted", upstream)
		}
	}
}

func newUnreachedUpstreamHandler(t *testing.T, authenticator ModelRequestAuthenticator, credentials UpstreamCredentialSource) *Handler {
	t.Helper()
	return mustHandler(t, HandlerConfig{
		Authenticator: authenticator, Credentials: credentials,
		UpstreamURL: "https://upstream.example.test" + ResponsesPath,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("malformed llmproxy request reached upstream")
			return nil, errors.New("unreachable")
		})},
	})
}

func mustHandler(t *testing.T, config HandlerConfig) *Handler {
	t.Helper()
	handler, err := NewHandler(config)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func newTLSModelRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "https://llmproxy.test"+ResponsesPath, strings.NewReader(body))
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("Content-Type", "application/json")
	return request
}

func validProxyBody() string {
	return `{"model":"gpt-5.6-codex","stream":true,"input":[]}`
}

func testProxyPrincipal() Principal {
	return Principal{
		CapabilityID:         "97000000-0000-4000-8000-000000000001",
		WorkspaceID:          "97000000-0000-4000-8000-000000000002",
		SessionID:            "97000000-0000-4000-8000-000000000003",
		RunID:                "97000000-0000-4000-8000-000000000004",
		RunAttemptID:         "97000000-0000-4000-8000-000000000005",
		RunAttemptGeneration: 3, ActorID: "97000000-0000-4000-8000-000000000006",
		HolderID: "pool/holder", Model: testModel, Provider: testProvider,
		RunDeadline: time.Now().Add(time.Hour), CapabilityExpiresAt: time.Now().Add(2 * time.Hour),
		AuthorizedAt: time.Now(),
	}
}

type recordingModelAuthenticator struct {
	mu        sync.Mutex
	models    []string
	principal Principal
	err       error
}

func (authenticator *recordingModelAuthenticator) AuthenticateModelRequest(_ *http.Request, model string) (Principal, error) {
	authenticator.mu.Lock()
	defer authenticator.mu.Unlock()
	authenticator.models = append(authenticator.models, model)
	return authenticator.principal, authenticator.err
}

func (authenticator *recordingModelAuthenticator) callCount() int {
	authenticator.mu.Lock()
	defer authenticator.mu.Unlock()
	return len(authenticator.models)
}

func (authenticator *recordingModelAuthenticator) modelsSnapshot() []string {
	authenticator.mu.Lock()
	defer authenticator.mu.Unlock()
	return append([]string(nil), authenticator.models...)
}

type recordingCredentialSource struct {
	mu         sync.Mutex
	principals []Principal
	credential UpstreamCredential
	err        error
}

func (source *recordingCredentialSource) Credential(_ context.Context, principal Principal) (UpstreamCredential, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.principals = append(source.principals, principal)
	return source.credential, source.err
}

func (source *recordingCredentialSource) callCount() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return len(source.principals)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func decodeProxyErrorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Error.Code
}
