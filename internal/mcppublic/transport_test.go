package mcppublic

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/envtools/tools"
)

// newTestServer wires Dispatcher + Server with the in-package fakes
// from dispatch_test.go (fakeExecutorsSource, stubBackend).
func newTestServer(t *testing.T, src *fakeExecutorsSource, backend ToolBackend, p *Principal) *httptest.Server {
	t.Helper()
	d := newTestDispatcher(t, src, backend)
	srv := NewServer(d, "https://app.example.com", nil)

	// "Auth middleware" for tests: just plant p in ctx and pass through.
	stubAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
		})
	}
	hs := httptest.NewServer(srv.Mount(stubAuth))
	t.Cleanup(hs.Close)
	return hs
}

// post is a small helper that POSTs a JSON-RPC envelope and returns
// the parsed response.
func post(t *testing.T, ts *httptest.Server, body string) (*http.Response, jsonrpcResp) {
	t.Helper()
	resp, err := http.Post(ts.URL+"/v1/mcp", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	var out jsonrpcResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp, out
}

func TestTransport_Initialize_ReturnsProtocolVersion(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	_, out := post(t, hs, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if out.Error != nil {
		t.Fatalf("unexpected error: %+v", out.Error)
	}
	var init tools.MCPInitializeResult
	_ = json.Unmarshal(out.Result, &init)
	if init.ProtocolVersion != "2025-06-18" {
		t.Errorf("ProtocolVersion: got %q, want 2025-06-18", init.ProtocolVersion)
	}
}

func TestTransport_ToolsList_FilteredByPrincipal(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	_, out := post(t, hs, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if out.Error != nil {
		t.Fatalf("unexpected error: %+v", out.Error)
	}
	var lr tools.MCPListToolsResult
	_ = json.Unmarshal(out.Result, &lr)
	names := toolNames(lr.Tools)
	if !contains(names, "read_file") || !contains(names, "list_environments") {
		t.Errorf("missing read-tools: %v", names)
	}
	if contains(names, "shell") {
		t.Errorf("read-only principal saw shell: %v", names)
	}
}

func TestTransport_ToolsCall_HappyPath(t *testing.T) {
	backend := &stubBackend{result: tools.MCPCallToolResult{
		Content: []tools.MCPToolContent{{Type: "text", Text: "ok"}},
	}}
	hs := newTestServer(t, nil, backend, principalReadExec("ws_1"))
	_, out := post(t, hs, `{
	  "jsonrpc":"2.0","id":3,"method":"tools/call",
	  "params":{"name":"shell","arguments":{"environment_id":"laptop"}}
	}`)
	if out.Error != nil {
		t.Fatalf("unexpected error: %+v", out.Error)
	}
	var res tools.MCPCallToolResult
	_ = json.Unmarshal(out.Result, &res)
	if len(res.Content) != 1 || res.Content[0].Text != "ok" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestTransport_ToolsCall_ForbiddenToolReturnsJSONRPCError(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	_, out := post(t, hs, `{
	  "jsonrpc":"2.0","id":4,"method":"tools/call",
	  "params":{"name":"shell","arguments":{"environment_id":"laptop"}}
	}`)
	if out.Error == nil {
		t.Fatal("expected JSON-RPC error for forbidden tool")
	}
	if out.Error.Code != codeForbiddenTool {
		t.Errorf("error code: got %d, want %d", out.Error.Code, codeForbiddenTool)
	}
}

func TestTransport_UnknownMethod_ReturnsMethodNotFound(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	_, out := post(t, hs, `{"jsonrpc":"2.0","id":5,"method":"frob/quux"}`)
	if out.Error == nil || out.Error.Code != codeMethodNotFound {
		t.Fatalf("want codeMethodNotFound, got %+v", out.Error)
	}
}

func TestTransport_NotificationsInitialized_NoResponseBody(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	resp, err := http.Post(hs.URL+"/v1/mcp", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestTransport_MalformedJSON_ReturnsParseError(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	_, out := post(t, hs, `{not even json`)
	if out.Error == nil || out.Error.Code != codeParseError {
		t.Fatalf("want codeParseError, got %+v", out.Error)
	}
	// id must be null since the request never parsed.
	if string(out.ID) != "null" {
		t.Errorf("id should be null on parse error, got %s", out.ID)
	}
}

func TestTransport_EmptyBody_ReturnsInvalidRequest(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	_, out := post(t, hs, "")
	if out.Error == nil || out.Error.Code != codeInvalidRequest {
		t.Fatalf("want codeInvalidRequest, got %+v", out.Error)
	}
}

func TestTransport_BatchRequest_RejectedClearly(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	_, out := post(t, hs, `[{"jsonrpc":"2.0","id":1,"method":"initialize"}]`)
	if out.Error == nil || out.Error.Code != codeInvalidRequest {
		t.Fatalf("want codeInvalidRequest for batch, got %+v", out.Error)
	}
	if !strings.Contains(out.Error.Message, "batch") {
		t.Errorf("error should name 'batch': %s", out.Error.Message)
	}
}

func TestTransport_BadJSONRPCVersion_Rejected(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	_, out := post(t, hs, `{"jsonrpc":"1.0","id":6,"method":"initialize"}`)
	if out.Error == nil || out.Error.Code != codeInvalidRequest {
		t.Fatalf("want codeInvalidRequest, got %+v", out.Error)
	}
}

func TestTransport_GET_Returns405(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	resp, err := http.Get(hs.URL + "/v1/mcp")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET: want 405, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Allow") != "POST" {
		t.Errorf("Allow header: got %q, want POST", resp.Header.Get("Allow"))
	}
}

func TestTransport_DELETE_Returns405(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	req, _ := http.NewRequest(http.MethodDelete, hs.URL+"/v1/mcp", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE: want 405, got %d", resp.StatusCode)
	}
}

func TestTransport_Healthz_Public(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	resp, err := http.Get(hs.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "ok" {
		t.Errorf("/healthz: status=%d body=%q", resp.StatusCode, body)
	}
}

func TestTransport_OAuthProtectedResource_Public(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	resp, err := http.Get(hs.URL + "/v1/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var doc map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&doc)
	if _, ok := doc["resource"]; !ok {
		t.Errorf("missing resource field: %v", doc)
	}
	if _, ok := doc["authorization_servers"]; !ok {
		t.Errorf("missing authorization_servers field: %v", doc)
	}
}

func TestTransport_AuthMiddleware_401AdvertisesResourceMetadata(t *testing.T) {
	// Verify the auth middleware emits WWW-Authenticate that points
	// at our oauth-protected-resource doc. End-to-end-ish: mount the
	// real Middleware + a resolver that always rejects, hit /v1/mcp,
	// inspect the 401's headers.
	d := newTestDispatcher(t, nil, &stubBackend{})
	srv := NewServer(d, "https://app.example.com", nil)
	resolver := stubResolverErr{err: ErrInvalid}
	mw := AuthMiddleware([]PrincipalResolver{resolver},
		"https://mcp.example.com/v1/.well-known/oauth-protected-resource", nil)
	hs := httptest.NewServer(srv.Mount(mw))
	defer hs.Close()

	req, _ := http.NewRequest(http.MethodPost, hs.URL+"/v1/mcp",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)))
	req.Header.Set("Authorization", "Bearer some-thing")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
	wa := resp.Header.Get("WWW-Authenticate")
	if !strings.Contains(wa, "resource_metadata=") {
		t.Errorf("WWW-Authenticate missing resource_metadata: %q", wa)
	}
}

// stubResolverErr always returns the configured err — handy when a
// test wants to exercise auth-failure paths without minting a real
// PAT.
type stubResolverErr struct{ err error }

func (s stubResolverErr) Resolve(_ context.Context, _ string) (*Principal, error) {
	return nil, s.err
}
