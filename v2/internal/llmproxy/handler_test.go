package llmproxy

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHandlerAuthorizesEveryRequestAndUsesCoreResolvedRouteAndBearer(t *testing.T) {
	type upstreamRequest struct {
		method  string
		url     string
		headers http.Header
		body    string
	}
	captured := make(chan upstreamRequest, 2)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		captured <- upstreamRequest{request.Method, request.URL.String(), request.Header.Clone(), string(body)}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
				"X-Request-Id": []string{"upstream-request"},
				"Set-Cookie":   []string{"must-not-cross=1"},
			},
			Body:    io.NopCloser(strings.NewReader("event: response.completed\ndata: {}\n\n")),
			Request: request,
		}, nil
	})}
	authenticator := &recordingModelAuthenticator{principal: testProxyPrincipal()}
	handler := mustHandler(t, HandlerConfig{Authenticator: authenticator, HTTPClient: client})
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
		if wire.method != http.MethodPost || wire.url != "https://gateway.example.com/v1/responses" || wire.body != body ||
			wire.headers.Get("Authorization") != "Bearer upstream-secret" || wire.headers.Get("api-key") != "" ||
			wire.headers.Get("Cookie") != "" || wire.headers.Get("X-Forwarded-For") != "" ||
			wire.headers.Get("OpenAI-Beta") != "responses=v1" {
			t.Fatalf("upstream request = %+v", wire)
		}
	}
	if authenticator.callCount() != 2 {
		t.Fatalf("per-request live authorization calls = %d", authenticator.callCount())
	}
}

func TestHandlerRejectsMalformedRequestBeforeAuthorization(t *testing.T) {
	for _, test := range []struct {
		name, method, target, contentType, body string
		tls                                     bool
		want                                    int
	}{
		{name: "TLS", method: http.MethodPost, target: ResponsesPath, contentType: "application/json", body: validProxyBody(), want: http.StatusBadRequest},
		{name: "method", method: http.MethodGet, target: ResponsesPath, contentType: "application/json", body: validProxyBody(), tls: true, want: http.StatusMethodNotAllowed},
		{name: "path", method: http.MethodPost, target: "/v1/chat/completions", contentType: "application/json", body: validProxyBody(), tls: true, want: http.StatusNotFound},
		{name: "query", method: http.MethodPost, target: ResponsesPath + "?key=value", contentType: "application/json", body: validProxyBody(), tls: true, want: http.StatusNotFound},
		{name: "media type", method: http.MethodPost, target: ResponsesPath, contentType: "application/json; charset=utf-8", body: validProxyBody(), tls: true, want: http.StatusUnsupportedMediaType},
		{name: "duplicate model", method: http.MethodPost, target: ResponsesPath, contentType: "application/json", body: `{"model":"a","model":"b","stream":true}`, tls: true, want: http.StatusBadRequest},
		{name: "nonstream", method: http.MethodPost, target: ResponsesPath, contentType: "application/json", body: `{"model":"gpt-5.6-codex","stream":false}`, tls: true, want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			authenticator := &recordingModelAuthenticator{principal: testProxyPrincipal()}
			handler := newUnreachedUpstreamHandler(t, authenticator)
			request := httptest.NewRequest(test.method, "https://llmproxy.test"+test.target, strings.NewReader(test.body))
			if !test.tls {
				request.TLS = nil
			}
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want || authenticator.callCount() != 0 {
				t.Fatalf("malformed request = status %d auth %d", response.Code, authenticator.callCount())
			}
		})
	}
}

func TestHandlerFailsClosedBeforeUpstream(t *testing.T) {
	for _, test := range []struct {
		name    string
		authErr error
		mutate  func(*Principal)
		want    int
	}{
		{name: "unauthenticated", authErr: ErrUnauthenticated, want: http.StatusUnauthorized},
		{name: "not live", authErr: ErrForbidden, want: http.StatusForbidden},
		{name: "missing bearer", mutate: func(p *Principal) { p.UpstreamAuthorization = "" }, want: http.StatusServiceUnavailable},
		{name: "unsafe route", mutate: func(p *Principal) { p.ResponsesURL = "https://127.0.0.1/v1/responses" }, want: http.StatusServiceUnavailable},
		{name: "expired bearer", mutate: func(p *Principal) { p.BearerExpiresAt = time.Now().Add(-time.Second) }, want: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			principal := testProxyPrincipal()
			if test.mutate != nil {
				test.mutate(&principal)
			}
			authenticator := &recordingModelAuthenticator{principal: principal, err: test.authErr}
			handler := newUnreachedUpstreamHandler(t, authenticator)
			request := newTLSModelRequest(validProxyBody())
			request.Header.Set("Authorization", "Bearer run-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want || strings.Contains(response.Body.String(), "run-secret") ||
				strings.Contains(response.Body.String(), "upstream-secret") {
				t.Fatalf("fail-closed result = status %d body %q", response.Code, response.Body.String())
			}
		})
	}
}

func TestHandlerRejectsUpstreamRedirectWithoutFollowing(t *testing.T) {
	var calls atomic.Int64
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusTemporaryRedirect,
			Header:     http.Header{"Location": []string{"https://other.example.com/v1/responses"}},
			Body:       io.NopCloser(strings.NewReader("redirect")), Request: request,
		}, nil
	})}
	handler := mustHandler(t, HandlerConfig{
		Authenticator: &recordingModelAuthenticator{principal: testProxyPrincipal()}, HTTPClient: client,
	})
	request := newTLSModelRequest(validProxyBody())
	request.Header.Set("Authorization", "Bearer run-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || calls.Load() != 1 || response.Header().Get("Location") != "" {
		t.Fatalf("redirect result = status %d calls %d headers %v", response.Code, calls.Load(), response.Header())
	}
}

func TestHandlerLogsOnlySafeUpstreamDisposition(t *testing.T) {
	var logs bytes.Buffer
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"provider-secret-must-not-enter-logs"}`)),
			Request:    request,
		}, nil
	})}
	handler := mustHandler(t, HandlerConfig{
		Authenticator: &recordingModelAuthenticator{principal: testProxyPrincipal()}, HTTPClient: client,
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	request := newTLSModelRequest(validProxyBody())
	request.Header.Set("Authorization", "Bearer run-secret-must-not-enter-logs")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(logs.String(), `"stage":"upstream_response"`) ||
		!strings.Contains(logs.String(), `"status":403`) || strings.Contains(logs.String(), "secret-must-not-enter-logs") ||
		strings.Contains(logs.String(), "gpt-5.6-codex") || strings.Contains(logs.String(), "gateway.example.com") {
		t.Fatalf("unsafe or incomplete llmproxy diagnostic log = %q", logs.String())
	}
}

func TestHandlerRechecksHardRunDeadlineBeforeUpstream(t *testing.T) {
	now := time.Date(2026, 8, 2, 18, 0, 0, 0, time.UTC)
	principal := testProxyPrincipal()
	principal.RunDeadline = now
	handler := mustHandler(t, HandlerConfig{
		Authenticator: &recordingModelAuthenticator{principal: principal},
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
	if response.Code != http.StatusForbidden || decodeProxyErrorCode(t, response) != "run_deadline_exceeded" {
		t.Fatalf("deadline result = status %d body %q", response.Code, response.Body.String())
	}
}

func TestHandlerValidatesConstruction(t *testing.T) {
	principal := &recordingModelAuthenticator{principal: testProxyPrincipal()}
	if _, err := NewHandler(HandlerConfig{Authenticator: principal}); err == nil {
		t.Fatal("handler without HTTP client was accepted")
	}
	if _, err := NewHandler(HandlerConfig{HTTPClient: http.DefaultClient}); err == nil {
		t.Fatal("handler without authenticator was accepted")
	}
}

func newUnreachedUpstreamHandler(t *testing.T, authenticator ModelRequestAuthenticator) *Handler {
	t.Helper()
	return mustHandler(t, HandlerConfig{
		Authenticator: authenticator,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("rejected llmproxy request reached upstream")
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
	now := time.Now().UTC()
	return Principal{
		CapabilityID: "97000000-0000-4000-8000-000000000001",
		WorkspaceID:  "97000000-0000-4000-8000-000000000002", SessionID: "97000000-0000-4000-8000-000000000003",
		RunID: "97000000-0000-4000-8000-000000000004", RunAttemptID: "97000000-0000-4000-8000-000000000005",
		RunAttemptGeneration: 3, ActorID: "97000000-0000-4000-8000-000000000006", HolderID: "pool/holder",
		Model: testModel, Provider: testProvider,
		LLMGatewayID: "97000000-0000-4000-8000-000000000007", LLMGatewayVersion: 2,
		LLMGatewayGrantUserID: "97000000-0000-4000-8000-000000000006",
		ResponsesURL:          "https://gateway.example.com/v1/responses", UpstreamAuthorization: "Bearer upstream-secret",
		BearerExpiresAt: now.Add(30 * time.Minute), RunDeadline: now.Add(time.Hour),
		CapabilityExpiresAt: now.Add(2 * time.Hour), AuthorizedAt: now,
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
