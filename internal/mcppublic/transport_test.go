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
	resp, err := http.Post(ts.URL+"/mcp", "application/json", strings.NewReader(body))
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
	resp, err := http.Post(hs.URL+"/mcp", "application/json",
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
	resp, err := http.Get(hs.URL + "/mcp")
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
	req, _ := http.NewRequest(http.MethodDelete, hs.URL+"/mcp", nil)
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

// TestTransport_OAuthProtectedResource_RFC9728RootPath pins the
// RFC 9728 §3.1 spec-compliant location of the Protected Resource
// Metadata doc — at the resource server ROOT, not nested under
// /v1/. Claude Code (and other MCP 2025-06-18-compliant clients)
// look here first; if it 404s they fall back to RFC 8414's
// "guess the AS by stripping path" path, which lands them at
// https://mcp.<host>/authorize — a 404 page, broken UX.
//
// We keep /.well-known/oauth-protected-resource as an alias for
// the WWW-Authenticate header that older deployments wired in via
// the MCP_PUBLIC_RESOURCE_METADATA_URL env var. Both paths must
// return the same doc.
func TestTransport_OAuthProtectedResource_RFC9728RootPath(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	resp, err := http.Get(hs.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("root-path PRM should be 200; got %d", resp.StatusCode)
	}
	var doc struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasSuffix(doc.Resource, "/mcp") {
		t.Errorf("resource: %q must end in /mcp", doc.Resource)
	}
	if len(doc.AuthorizationServers) == 0 || doc.AuthorizationServers[0] != "https://app.example.com" {
		t.Errorf("authorization_servers wrong: %+v", doc.AuthorizationServers)
	}
}

func TestTransport_OAuthProtectedResource_Public(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	resp, err := http.Get(hs.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var doc struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&doc)
	// plain httptest server (no TLS, no X-Forwarded-Proto) → http://
	if !strings.HasPrefix(doc.Resource, "http://") {
		t.Errorf("plain-http resource: got %q, want http:// prefix", doc.Resource)
	}
	if !strings.HasSuffix(doc.Resource, "/mcp") {
		t.Errorf("resource missing /mcp suffix: %q", doc.Resource)
	}
	if len(doc.AuthorizationServers) == 0 {
		t.Errorf("no authorization_servers: %+v", doc)
	}
	if doc.AuthorizationServers[0] != "https://app.example.com" {
		t.Errorf("authorization_servers[0]: got %q, want %q",
			doc.AuthorizationServers[0], "https://app.example.com")
	}
}

// TestTransport_OAuthProtectedResource_HonorsXForwardedProto pins the
// production behavior behind istio-ingress (TLS terminated at ingress,
// pod sees plain http but the X-Forwarded-Proto header tells us the
// client connected over https). Without this honored, the protected-
// resource doc would advertise http://mcp.agent.cs.ac.cn/mcp and
// OAuth clients would refuse to use it.
func TestTransport_OAuthProtectedResource_HonorsXForwardedProto(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	req, _ := http.NewRequest(http.MethodGet, hs.URL+"/.well-known/oauth-protected-resource", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var doc struct {
		Resource string `json:"resource"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&doc)
	if !strings.HasPrefix(doc.Resource, "https://") {
		t.Errorf("resource: got %q, want https:// prefix (X-Forwarded-Proto: https)", doc.Resource)
	}
}

// TestTransport_OAuthProtectedResource_AdvertisesScopesAndBearerMethods
// pins the MCP-OAuth-discovery contract that the gateway makes to
// `codex mcp login`, Claude Code, and Claude Desktop:
//
//   - `scopes_supported` must list the two scopes consent screens
//     can grant. Codex CLI prefers these over `--scopes` config
//     (openai/codex#20503 confirms it reads the doc).
//   - `bearer_methods_supported` must include `header` since the
//     resolver only accepts Authorization: Bearer ...
//
// Without these, clients fall back to guessing, which causes
// scope-missing tokens (no mcp:exec → all shell calls 403) or
// query-string-bearer attempts that the resolver doesn't honor.
func TestTransport_OAuthProtectedResource_AdvertisesScopesAndBearerMethods(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	resp, err := http.Get(hs.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var doc struct {
		ScopesSupported        []string `json:"scopes_supported"`
		BearerMethodsSupported []string `json:"bearer_methods_supported"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	wantScopes := map[string]bool{"mcp:read": false, "mcp:exec": false}
	for _, s := range doc.ScopesSupported {
		if _, ok := wantScopes[s]; ok {
			wantScopes[s] = true
		}
	}
	for s, seen := range wantScopes {
		if !seen {
			t.Errorf("scopes_supported missing %q: got %v", s, doc.ScopesSupported)
		}
	}
	hasHeader := false
	for _, m := range doc.BearerMethodsSupported {
		if m == "header" {
			hasHeader = true
		}
	}
	if !hasHeader {
		t.Errorf("bearer_methods_supported missing \"header\": got %v", doc.BearerMethodsSupported)
	}
}

// TestTransport_OAuthProtectedResource_XForwardedProtoChain verifies
// the comma-separated form (RFC 7239-style: "https, http") picks the
// first entry, since proxies may chain.
func TestTransport_OAuthProtectedResource_XForwardedProtoChain(t *testing.T) {
	hs := newTestServer(t, nil, &stubBackend{}, principalReadOnly("ws_1"))
	req, _ := http.NewRequest(http.MethodGet, hs.URL+"/.well-known/oauth-protected-resource", nil)
	req.Header.Set("X-Forwarded-Proto", "https, http")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var doc struct {
		Resource string `json:"resource"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&doc)
	if !strings.HasPrefix(doc.Resource, "https://") {
		t.Errorf("chain header: got %q, want https:// prefix", doc.Resource)
	}
}

func TestTransport_AuthMiddleware_401AdvertisesResourceMetadata(t *testing.T) {
	// Verify the auth middleware emits WWW-Authenticate that points
	// at our oauth-protected-resource doc. End-to-end-ish: mount the
	// real Middleware + a resolver that always rejects, hit /mcp,
	// inspect the 401's headers.
	d := newTestDispatcher(t, nil, &stubBackend{})
	srv := NewServer(d, "https://app.example.com", nil)
	resolver := stubResolverErr{err: ErrInvalid}
	mw := AuthMiddleware([]PrincipalResolver{resolver},
		"https://mcp.example.com/.well-known/oauth-protected-resource", nil)
	hs := httptest.NewServer(srv.Mount(mw))
	defer hs.Close()

	req, _ := http.NewRequest(http.MethodPost, hs.URL+"/mcp",
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
