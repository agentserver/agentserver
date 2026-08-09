package adapter

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHTTPDataPlaneNeverReturnsSecretPayloadOrResponse(t *testing.T) {
	const placeholder = "agentserver-placeholder-super-secret"
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		if !strings.Contains(string(body), placeholder) {
			t.Fatalf("process payload did not contain test placeholder: %s", body)
		}
		if request.Header.Get("X-Zti-Token") != "test-zti" {
			t.Fatalf("X-Zti-Token = %q", request.Header.Get("X-Zti-Token"))
		}
		response.Header().Set("X-Tt-Logid", "provider-log-1")
		response.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(response, `{"message":"`+placeholder+`","authorization":"Bearer real-token"}`)
	}))
	defer server.Close()
	dataPlane := newHTTPTestDataPlane(t, server)
	_, err := dataPlane.StartProcess(t.Context(), "session-1", StartProcessInput{
		RequestID: "request-secret-1", Executable: "lark-cli", WorkingDirectory: "/workspace",
		Environment: map[string]string{"LARK_TOKEN": placeholder}, Timeout: time.Second,
	})
	if err == nil {
		t.Fatal("StartProcess() succeeded")
	}
	if strings.Contains(err.Error(), placeholder) || strings.Contains(err.Error(), "real-token") {
		t.Fatalf("secret leaked through error: %v", err)
	}
	var requestError *RequestError
	if !errors.As(err, &requestError) || !requestError.WroteRequest || requestError.StatusCode != 500 || requestError.RequestID != "provider-log-1" {
		t.Fatalf("request error = %#v", err)
	}
}

func TestHTTPDataPlaneParsesDocumentedProcessSSE(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/process/start" || request.Method != http.MethodPost ||
			request.Header.Get("Accept") != "text/event-stream" || request.Header.Get("X-Tt-Logid") != "request-2" {
			t.Fatalf("request = %s %s Accept=%q", request.Method, request.URL.Path, request.Header.Get("Accept"))
		}
		var payload struct {
			Command struct {
				Path string            `json:"path"`
				Args []string          `json:"args"`
				Envs map[string]string `json:"envs"`
			} `json:"command"`
			Timeout   int  `json:"timeout"`
			NonStream bool `json:"non_stream"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Command.Path != "lark-cli" || payload.Command.Envs["LARK_TOKEN"] != "placeholder" || payload.Timeout != 2000 || payload.NonStream {
			t.Fatalf("payload = %+v", payload)
		}
		response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		response.Header().Set("X-Tt-Logid", "provider-log-2")
		_, _ = io.WriteString(response, "event: process.start\ndata: {\"pid\":123}\n\n")
		_, _ = io.WriteString(response, "event: process.data\ndata: {\"stdout\":\"ok\\n\"}\n\n")
		_, _ = io.WriteString(response, "event: process.exit\ndata: {\"exit_code\":0}\n\n")
	}))
	defer server.Close()
	dataPlane := newHTTPTestDataPlane(t, server)
	stream, err := dataPlane.StartProcess(t.Context(), "session-1", StartProcessInput{
		RequestID: "request-2", Executable: "lark-cli", Arguments: []string{"doc", "get"}, WorkingDirectory: "/workspace",
		Environment: map[string]string{"LARK_TOKEN": "placeholder"}, Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if stream.RequestID() != "provider-log-2" {
		t.Fatalf("request ID = %q", stream.RequestID())
	}
	for index, name := range []string{"process.start", "process.data", "process.exit"} {
		event, err := stream.Next(t.Context())
		if err != nil || event.Name != name {
			t.Fatalf("event %d = %+v, %v", index, event, err)
		}
	}
	if _, err := stream.Next(t.Context()); !errors.Is(err, io.EOF) {
		t.Fatalf("stream end = %v", err)
	}
}

func TestHTTPDataPlaneRejectsInvalidProcessCorrelationIDBeforeDispatch(t *testing.T) {
	called := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()
	dataPlane := newHTTPTestDataPlane(t, server)
	_, err := dataPlane.StartProcess(t.Context(), "session-1", StartProcessInput{
		Executable: "true", WorkingDirectory: "/workspace", Timeout: time.Second,
	})
	var requestError *RequestError
	if !errors.As(err, &requestError) || requestError.Code != "bad_request" || requestError.WroteRequest || called {
		t.Fatalf("invalid correlation result = %#v, called=%v", err, called)
	}
}

func TestHTTPDataPlaneSSEProtocolErrorRetainsProviderRequestID(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.Header().Set("X-Tt-Logid", "provider-malformed-log")
		_, _ = io.WriteString(response, "event: process.start\ndata: not-json\n\n")
	}))
	defer server.Close()
	dataPlane := newHTTPTestDataPlane(t, server)
	stream, err := dataPlane.StartProcess(t.Context(), "session-1", StartProcessInput{
		RequestID: "request-malformed", Executable: "true", WorkingDirectory: "/workspace", Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	_, err = stream.Next(t.Context())
	var requestError *RequestError
	if !errors.As(err, &requestError) || requestError.Code != "invalid_response" || requestError.RequestID != "provider-malformed-log" {
		t.Fatalf("malformed SSE error = %#v", err)
	}
}

func TestHTTPDataPlaneRejectsTrailingStatJSON(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("X-Tt-Logid", "provider-stat-log")
		_, _ = io.WriteString(response, `{"entry":{"type":"file","size":1}} {"trailing":true}`)
	}))
	defer server.Close()
	dataPlane := newHTTPTestDataPlane(t, server)
	_, _, err := dataPlane.Stat(t.Context(), "session-1", "/workspace/file")
	var requestError *RequestError
	if !errors.As(err, &requestError) || requestError.Code != "invalid_response" || requestError.RequestID != "provider-stat-log" {
		t.Fatalf("trailing stat error = %#v", err)
	}
}

func TestHTTPDataPlaneRejectsRedirectWithoutFollowing(t *testing.T) {
	followed := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/redirected" {
			followed = true
			response.WriteHeader(http.StatusOK)
			return
		}
		response.Header().Set("Location", "/redirected")
		response.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	dataPlane := newHTTPTestDataPlane(t, server)
	_, _, err := dataPlane.Stat(t.Context(), "session-1", "/workspace/file")
	var requestError *RequestError
	if !errors.As(err, &requestError) || requestError.StatusCode != http.StatusFound || followed {
		t.Fatalf("redirect result = %#v, followed=%v", err, followed)
	}
}

func TestHTTPDataPlaneRefreshesRejectedJWTWithoutReplaying(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests++
		response.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	source := &refreshingTestHeaderSource{token: "header.payload.signature"}
	client := server.Client()
	client.Timeout = 0
	dataPlane, err := NewHTTPDataPlane(HTTPDataPlaneConfig{
		Client: client, Headers: source, RequireHTTPS: true,
		Endpoint: EndpointResolverFunc(func(string) (*url.URL, error) {
			clone := *base
			return &clone, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = dataPlane.Stat(t.Context(), "session-1", "/workspace/file")
	var requestError *RequestError
	if !errors.As(err, &requestError) || requestError.StatusCode != http.StatusUnauthorized || requests != 1 {
		t.Fatalf("data-plane 401 result = %#v requests=%d", err, requests)
	}
	if source.refreshes != 1 || source.rejected != source.token {
		t.Fatalf("refresh result = count:%d rejected:%q", source.refreshes, source.rejected)
	}
}

func TestStrictHTTPClientCannotDisableCertificateVerification(t *testing.T) {
	client, err := NewStrictHTTPClient(StrictHTTPClientConfig{})
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil {
		t.Fatalf("transport = %#v", client.Transport)
	}
	if transport.TLSClientConfig.InsecureSkipVerify || transport.TLSClientConfig.MinVersion != tls.VersionTLS13 || transport.Proxy != nil {
		t.Fatalf("unsafe strict transport = %+v", transport.TLSClientConfig)
	}
}

func TestSandboxdEndpointResolverPinsSessionBelowSGSuffix(t *testing.T) {
	resolver, err := NewSandboxdEndpointResolver("sg.ai-sandbox-i18n.byted.org")
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := resolver.Resolve("v1-session-123")
	if err != nil || endpoint.String() != "https://v1-session-123.sg.ai-sandbox-i18n.byted.org" {
		t.Fatalf("Resolve() = %v, %v", endpoint, err)
	}
	for _, invalid := range []string{"../metadata", "a.b", "-bad", strings.Repeat("a", 64)} {
		if _, err := resolver.Resolve(invalid); err == nil {
			t.Fatalf("invalid session %q was accepted", invalid)
		}
	}
}

func newHTTPTestDataPlane(t *testing.T, server *httptest.Server) *HTTPDataPlane {
	t.Helper()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Timeout = 0
	dataPlane, err := NewHTTPDataPlane(HTTPDataPlaneConfig{
		Client: client,
		Headers: HeaderSourceFunc(func(context.Context) (http.Header, error) {
			return http.Header{"X-Zti-Token": []string{"test-zti"}}, nil
		}),
		Endpoint: EndpointResolverFunc(func(string) (*url.URL, error) {
			clone := *base
			return &clone, nil
		}),
		RequireHTTPS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return dataPlane
}

type refreshingTestHeaderSource struct {
	token     string
	rejected  string
	refreshes int
}

func (source *refreshingTestHeaderSource) Headers(context.Context) (http.Header, error) {
	return http.Header{"X-Jwt-Token": []string{source.token}}, nil
}

func (source *refreshingTestHeaderSource) refreshRejectedIdentity(_ context.Context, rejected string) error {
	source.refreshes++
	source.rejected = rejected
	return nil
}
