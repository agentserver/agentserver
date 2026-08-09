package egressgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
)

const (
	testZTIToken    = "verified-tae-zti"
	testAccessToken = "real-lark-access-token"
)

type testZTIVerifier struct {
	mu        sync.Mutex
	token     string
	principal ZTIPrincipal
	err       error
	wait      <-chan struct{}
}

func (verifier *testZTIVerifier) VerifyZTI(ctx context.Context, token string) (ZTIPrincipal, error) {
	verifier.mu.Lock()
	verifier.token = token
	principal, err, wait := verifier.principal, verifier.err, verifier.wait
	verifier.mu.Unlock()
	if wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
			return ZTIPrincipal{}, ctx.Err()
		}
	}
	return principal, err
}

type testLiveAuthority struct {
	mu         sync.Mutex
	claims     egresscapability.Claims
	principal  ZTIPrincipal
	credential Credential
	err        error
	wait       <-chan struct{}
	ignoreCtx  bool
}

func (authority *testLiveAuthority) AuthorizeLarkReadOnly(ctx context.Context, claims egresscapability.Claims, principal ZTIPrincipal) (Credential, error) {
	authority.mu.Lock()
	authority.claims = claims
	authority.principal = principal
	credential, err, wait, ignoreCtx := authority.credential, authority.err, authority.wait, authority.ignoreCtx
	authority.mu.Unlock()
	if wait != nil {
		if ignoreCtx {
			<-wait
		} else {
			select {
			case <-wait:
			case <-ctx.Done():
				return Credential{}, ctx.Err()
			}
		}
	}
	return credential, err
}

type testAuditSink struct {
	mu      sync.Mutex
	records []AuditRecord
	err     error
	wait    <-chan struct{}
}

func (sink *testAuditSink) RecordEgressDecision(ctx context.Context, record AuditRecord) error {
	sink.mu.Lock()
	sink.records = append(sink.records, record)
	err, wait := sink.err, sink.wait
	sink.mu.Unlock()
	if wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (sink *testAuditSink) snapshot() []AuditRecord {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]AuditRecord(nil), sink.records...)
}

type egressGatewayFixture struct {
	now         time.Time
	claims      egresscapability.Claims
	signer      *egresscapability.Signer
	placeholder string
	zti         *testZTIVerifier
	authority   *testLiveAuthority
	audit       *testAuditSink
	handler     *Handler
}

func newEgressGatewayFixture(t *testing.T) *egressGatewayFixture {
	t.Helper()
	now := time.Date(2026, time.August, 6, 20, 0, 0, 0, time.UTC)
	seed := bytes.Repeat([]byte{0x73}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	signer, err := egresscapability.NewSigner("execution-gateway", "egress-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := egresscapability.NewVerifier([]egresscapability.TrustedKey{{
		Issuer: "execution-gateway", KeyID: "egress-key-1", PublicKey: privateKey.Public().(ed25519.PublicKey),
	}})
	if err != nil {
		t.Fatal(err)
	}
	claims := egresscapability.Claims{
		Version: egresscapability.Version, Issuer: "execution-gateway",
		Audience: egresscapability.AudienceLarkReadOnly, CapabilityID: "capability-1",
		WorkspaceID: "workspace-1", SessionID: "session-1", ActorID: "actor-1", EnvironmentID: "environment-1",
		RunID: "run-1", RunAttemptID: "attempt-1", RunAttemptGeneration: 3,
		ExecutionID: "execution-1", OperationID: "operation-1", SandboxID: "sandbox-1", TargetGeneration: 7,
		PackID: egresscapability.PackLarkReadOnly, GrantID: "grant-1", GrantVersion: 5,
		PolicySHA256: larkegresspolicy.SHA256Hex(), Executable: "lark-cli",
		IssuedAtUnixMS: now.Add(-time.Second).UnixMilli(), ExpiresAtUnixMS: now.Add(time.Minute).UnixMilli(),
	}
	placeholder, err := signer.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	zti := &testZTIVerifier{principal: ZTIPrincipal{PSM: "prod.tae.agent-gateway", User: "tae-session"}}
	authority := &testLiveAuthority{credential: Credential{AccessToken: testAccessToken, ExpiresAt: now.Add(10 * time.Minute)}}
	audit := &testAuditSink{}
	service, err := NewService(Config{
		Placeholders: verifier, ZTI: zti, Authority: authority, Audit: audit,
		AllowedPSM: "prod.tae.agent-gateway", Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(service, 0)
	if err != nil {
		t.Fatal(err)
	}
	return &egressGatewayFixture{
		now: now, claims: claims, signer: signer, placeholder: placeholder,
		zti: zti, authority: authority, audit: audit, handler: handler,
	}
}

func (fixture *egressGatewayFixture) originalRequest() OriginalRequest {
	return OriginalRequest{
		Host: LarkOpenAPIHost, Path: "/open-apis/docx/v1/documents/document_1/raw_content", Method: http.MethodGet,
		Headers: map[string]string{ZTIHeader: testZTIToken, AuthorizationHeader: "Bearer " + fixture.placeholder},
	}
}

func marshalWebhookRequest(t *testing.T, original OriginalRequest, extensions map[string]any) []byte {
	t.Helper()
	envelope := map[string]any{"request": original}
	for name, value := range extensions {
		envelope[name] = value
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func performWebhook(handler http.Handler, raw []byte, mutate func(*http.Request)) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "http://egress-authorizer.test"+PolicyPath, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("version", ProtocolVersion)
	request.Header.Set(ZTIHeader, testZTIToken)
	if mutate != nil {
		mutate(request)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeWebhookResponse(t *testing.T, response *httptest.ResponseRecorder) WebhookResponse {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response headers = %#v", response.Header())
	}
	decoder := json.NewDecoder(response.Body)
	decoder.DisallowUnknownFields()
	var result WebhookResponse
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, response.Body.String())
	}
	if result.Code != 0 || result.Version != ProtocolVersion {
		t.Fatalf("protocol response = %#v", result)
	}
	return result
}

func requireWebhookDeny(t *testing.T, response *httptest.ResponseRecorder) WebhookResponse {
	t.Helper()
	result := decodeWebhookResponse(t, response)
	if result.Data.Result != "deny" || len(result.Data.Header) != 0 || result.Data.ApplicationInfo == "" {
		t.Fatalf("deny response = %#v", result)
	}
	return result
}

func TestHandlerAllowsExactLarkReadOnlyRequestAndReplacesAuthorization(t *testing.T) {
	fixture := newEgressGatewayFixture(t)
	raw := marshalWebhookRequest(t, fixture.originalRequest(), nil)
	result := decodeWebhookResponse(t, performWebhook(fixture.handler, raw, nil))
	if result.Data.Result != "allow" || result.Data.ApplicationInfo != "" ||
		!reflect.DeepEqual(result.Data.Header, map[string]string{AuthorizationHeader: "Bearer " + testAccessToken}) {
		t.Fatalf("allow response = %#v", result)
	}
	fixture.zti.mu.Lock()
	verifiedZTI := fixture.zti.token
	fixture.zti.mu.Unlock()
	if verifiedZTI != testZTIToken {
		t.Fatalf("verified ZTI = %q", verifiedZTI)
	}
	fixture.authority.mu.Lock()
	authorizedClaims, authorizedPrincipal := fixture.authority.claims, fixture.authority.principal
	fixture.authority.mu.Unlock()
	if !reflect.DeepEqual(authorizedClaims, fixture.claims) || authorizedPrincipal.PSM != "prod.tae.agent-gateway" {
		t.Fatalf("authority input = claims %#v principal %#v", authorizedClaims, authorizedPrincipal)
	}
	records := fixture.audit.snapshot()
	if len(records) != 1 || records[0].Decision != "allow" || records[0].ReasonCode != "allowed" ||
		records[0].CapabilityID != fixture.claims.CapabilityID || records[0].TargetGeneration != fixture.claims.TargetGeneration {
		t.Fatalf("audit records = %#v", records)
	}
	assertAuditContainsNoSecrets(t, records, fixture.placeholder, testAccessToken, testZTIToken)
}

func TestHandlerAcceptsUnknownTopLevelExtensions(t *testing.T) {
	fixture := newEgressGatewayFixture(t)
	raw := marshalWebhookRequest(t, fixture.originalRequest(), map[string]any{
		"trace_id": "future-extension", "metadata": map[string]any{"nested": true},
	})
	result := decodeWebhookResponse(t, performWebhook(fixture.handler, raw, nil))
	if result.Data.Result != "allow" {
		t.Fatalf("response = %#v", result)
	}
}

func TestHandlerRejectsDuplicateJSONKeys(t *testing.T) {
	fixture := newEgressGatewayFixture(t)
	original, err := json.Marshal(fixture.originalRequest())
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{
		"outer":  []byte(`{"request":` + string(original) + `,"request":` + string(original) + `}`),
		"inner":  []byte(`{"request":{"host":"open.feishu.cn","host":"evil.test","path":"/open-apis/docx/v1/documents/x","method":"GET","headers":{}}}`),
		"header": []byte(`{"request":{"host":"open.feishu.cn","path":"/open-apis/docx/v1/documents/x","method":"GET","headers":{"Authorization":"a","Authorization":"b"}}}`),
	} {
		t.Run(name, func(t *testing.T) {
			requireWebhookDeny(t, performWebhook(fixture.handler, raw, nil))
		})
	}
}

func TestHandlerRequiresMatchingVerifiedZTI(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*egressGatewayFixture, *OriginalRequest, *http.Request)
	}{
		{name: "missing body", mutate: func(_ *egressGatewayFixture, request *OriginalRequest, _ *http.Request) {
			delete(request.Headers, ZTIHeader)
		}},
		{name: "missing outer", mutate: func(_ *egressGatewayFixture, _ *OriginalRequest, request *http.Request) {
			request.Header.Del(ZTIHeader)
		}},
		{name: "mismatch", mutate: func(_ *egressGatewayFixture, request *OriginalRequest, _ *http.Request) {
			request.Headers[ZTIHeader] = "other-zti"
		}},
		{name: "unverified", mutate: func(fixture *egressGatewayFixture, _ *OriginalRequest, _ *http.Request) {
			fixture.zti.err = errors.New("invalid signature")
		}},
		{name: "wrong psm", mutate: func(fixture *egressGatewayFixture, _ *OriginalRequest, _ *http.Request) {
			fixture.zti.principal.PSM = "prod.untrusted"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEgressGatewayFixture(t)
			original := fixture.originalRequest()
			var requestMutation func(*http.Request)
			if test.mutate != nil {
				requestMutation = func(request *http.Request) { test.mutate(fixture, &original, request) }
			}
			raw := marshalWebhookRequest(t, original, nil)
			// Body mutations must run before marshaling; HTTP header mutations run here.
			if test.name == "missing body" || test.name == "mismatch" {
				test.mutate(fixture, &original, nil)
				raw = marshalWebhookRequest(t, original, nil)
				requestMutation = nil
			}
			requireWebhookDeny(t, performWebhook(fixture.handler, raw, requestMutation))
		})
	}
}

func TestHandlerRejectsDuplicateCaseInsensitiveAuthorization(t *testing.T) {
	fixture := newEgressGatewayFixture(t)
	original := fixture.originalRequest()
	original.Headers["authorization"] = original.Headers[AuthorizationHeader]
	result := requireWebhookDeny(t, performWebhook(fixture.handler, marshalWebhookRequest(t, original, nil), nil))
	if result.Data.ApplicationInfo != "invalid_request" {
		t.Fatalf("reason = %q", result.Data.ApplicationInfo)
	}
}

func TestHandlerRejectsExpiredAndTamperedPlaceholders(t *testing.T) {
	for _, test := range []struct {
		name   string
		make   func(*testing.T, *egressGatewayFixture) string
		reason string
	}{
		{name: "expired", make: func(t *testing.T, fixture *egressGatewayFixture) string {
			claims := fixture.claims
			claims.IssuedAtUnixMS = fixture.now.Add(-time.Minute).UnixMilli()
			claims.ExpiresAtUnixMS = fixture.now.Add(-time.Millisecond).UnixMilli()
			token, err := fixture.signer.Sign(claims)
			if err != nil {
				t.Fatal(err)
			}
			return token
		}, reason: "placeholder_invalid"},
		{name: "tampered", make: func(_ *testing.T, fixture *egressGatewayFixture) string {
			token := fixture.placeholder
			last := byte('A')
			if token[len(token)-1] == last {
				last = 'B'
			}
			return token[:len(token)-1] + string(last)
		}, reason: "placeholder_invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEgressGatewayFixture(t)
			original := fixture.originalRequest()
			original.Headers[AuthorizationHeader] = "Bearer " + test.make(t, fixture)
			result := requireWebhookDeny(t, performWebhook(fixture.handler, marshalWebhookRequest(t, original, nil), nil))
			if result.Data.ApplicationInfo != test.reason {
				t.Fatalf("reason = %q", result.Data.ApplicationInfo)
			}
		})
	}
}

func TestHandlerEnforcesExactLarkReadOnlyPolicy(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*OriginalRequest)
	}{
		{name: "other host", mutate: func(request *OriginalRequest) { request.Host = "api.example.com" }},
		{name: "IPv4 literal", mutate: func(request *OriginalRequest) { request.Host = "127.0.0.1" }},
		{name: "IPv6 literal", mutate: func(request *OriginalRequest) { request.Host = "[::1]" }},
		{name: "host port", mutate: func(request *OriginalRequest) { request.Host += ":443" }},
		{name: "post", mutate: func(request *OriginalRequest) { request.Method = http.MethodPost }},
		{name: "connect", mutate: func(request *OriginalRequest) { request.Method = http.MethodConnect }},
		{name: "method whitespace", mutate: func(request *OriginalRequest) { request.Method = " GET " }},
		{name: "encoded path", mutate: func(request *OriginalRequest) { request.Path = "/open-apis/docx/v1/documents/%2e%2e/raw_content" }},
		{name: "traversal", mutate: func(request *OriginalRequest) { request.Path = "/open-apis/docx/v1/documents/x/../raw_content" }},
		{name: "trailing slash", mutate: func(request *OriginalRequest) { request.Path += "/" }},
		{name: "redirect-like path", mutate: func(request *OriginalRequest) { request.Path = "/open-apis/docx/v1/documents/x/redirect" }},
		{name: "unknown read path", mutate: func(request *OriginalRequest) { request.Path = "/open-apis/contact/v3/users" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEgressGatewayFixture(t)
			original := fixture.originalRequest()
			test.mutate(&original)
			requireWebhookDeny(t, performWebhook(fixture.handler, marshalWebhookRequest(t, original, nil), nil))
		})
	}
}

func TestHandlerRejectsUnsafeOriginalHeaders(t *testing.T) {
	for _, header := range []string{
		"Host", "Connection", "Content-Length", "Cookie", "Forwarded", "Proxy-Authorization",
		"Transfer-Encoding", "X-Forwarded-Host", "X-HTTP-Method-Override",
	} {
		t.Run(header, func(t *testing.T) {
			fixture := newEgressGatewayFixture(t)
			original := fixture.originalRequest()
			original.Headers[header] = "unsafe"
			result := requireWebhookDeny(t, performWebhook(fixture.handler, marshalWebhookRequest(t, original, nil), nil))
			if result.Data.ApplicationInfo != "invalid_request" {
				t.Fatalf("reason = %q", result.Data.ApplicationInfo)
			}
		})
	}
}

func TestHandlerAllowsOnlyEnumeratedLarkPaths(t *testing.T) {
	paths := []string{
		"/open-apis/wiki/v2/spaces/get_node",
		"/open-apis/docx/v1/documents/document_1",
		"/open-apis/docx/v1/documents/document_1/raw_content",
		"/open-apis/docx/v1/documents/document_1/blocks",
		"/open-apis/docx/v1/documents/document_1/blocks/block_1",
		"/open-apis/docx/v1/documents/document_1/blocks/block_1/children",
	}
	for _, requestPath := range paths {
		t.Run(requestPath, func(t *testing.T) {
			fixture := newEgressGatewayFixture(t)
			original := fixture.originalRequest()
			original.Path = requestPath
			result := decodeWebhookResponse(t, performWebhook(fixture.handler, marshalWebhookRequest(t, original, nil), nil))
			if result.Data.Result != "allow" {
				t.Fatalf("response = %#v", result)
			}
		})
	}
}

func TestHandlerFailsClosedWhenAuthorityOrAuditFails(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*egressGatewayFixture)
		reason string
	}{
		{name: "authority error", mutate: func(fixture *egressGatewayFixture) { fixture.authority.err = errors.New("core unavailable") }, reason: "live_authority_denied"},
		{name: "empty credential", mutate: func(fixture *egressGatewayFixture) { fixture.authority.credential.AccessToken = "" }, reason: "live_authority_denied"},
		{name: "expiring credential", mutate: func(fixture *egressGatewayFixture) {
			fixture.authority.credential.ExpiresAt = fixture.now.Add(time.Second)
		}, reason: "live_authority_denied"},
		{name: "audit error", mutate: func(fixture *egressGatewayFixture) { fixture.audit.err = errors.New("audit unavailable") }, reason: "audit_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEgressGatewayFixture(t)
			test.mutate(fixture)
			result := requireWebhookDeny(t, performWebhook(fixture.handler, marshalWebhookRequest(t, fixture.originalRequest(), nil), nil))
			if result.Data.ApplicationInfo != test.reason {
				t.Fatalf("reason = %q, want %q", result.Data.ApplicationInfo, test.reason)
			}
			assertAuditContainsNoSecrets(t, fixture.audit.snapshot(), fixture.placeholder, testAccessToken, testZTIToken)
		})
	}
}

func TestHandlerReturnsDenyWithinConfiguredDeadline(t *testing.T) {
	fixture := newEgressGatewayFixture(t)
	blocked := make(chan struct{})
	fixture.authority.wait = blocked
	fixture.authority.ignoreCtx = true
	t.Cleanup(func() { close(blocked) })
	handler, err := NewHandler(fixture.handler.service, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result := requireWebhookDeny(t, performWebhook(handler, marshalWebhookRequest(t, fixture.originalRequest(), nil), nil))
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("webhook took %s", elapsed)
	}
	if result.Data.ApplicationInfo != "decision_timeout" {
		t.Fatalf("reason = %q", result.Data.ApplicationInfo)
	}
}

func TestHandlerRejectsAmbiguousOuterHeaders(t *testing.T) {
	fixture := newEgressGatewayFixture(t)
	raw := marshalWebhookRequest(t, fixture.originalRequest(), nil)
	for _, test := range []struct {
		name   string
		mutate func(*http.Request)
	}{
		{name: "duplicate version", mutate: func(request *http.Request) { request.Header["Version"] = []string{"v1", "v1"} }},
		{name: "case split version", mutate: func(request *http.Request) { request.Header["version"] = []string{"v1"} }},
		{name: "duplicate zti", mutate: func(request *http.Request) { request.Header[ZTIHeader] = []string{testZTIToken, testZTIToken} }},
		{name: "case split zti", mutate: func(request *http.Request) { request.Header["x-zti-token"] = []string{testZTIToken} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			requireWebhookDeny(t, performWebhook(fixture.handler, raw, test.mutate))
		})
	}
}

func TestHandlerAlwaysUsesProtocolDenyForMalformedWebhook(t *testing.T) {
	fixture := newEgressGatewayFixture(t)
	valid := marshalWebhookRequest(t, fixture.originalRequest(), nil)
	for _, test := range []struct {
		name   string
		raw    []byte
		mutate func(*http.Request)
	}{
		{name: "wrong method", raw: valid, mutate: func(request *http.Request) { request.Method = http.MethodGet }},
		{name: "wrong path", raw: valid, mutate: func(request *http.Request) { request.URL.Path = "/policy" }},
		{name: "query", raw: valid, mutate: func(request *http.Request) { request.URL.RawQuery = "x=1" }},
		{name: "missing version", raw: valid, mutate: func(request *http.Request) { request.Header.Del("version") }},
		{name: "wrong content type", raw: valid, mutate: func(request *http.Request) { request.Header.Set("Content-Type", "text/plain") }},
		{name: "content type parameters", raw: valid, mutate: func(request *http.Request) { request.Header.Set("Content-Type", "application/json; charset=utf-8") }},
		{name: "duplicate content type", raw: valid, mutate: func(request *http.Request) {
			request.Header["Content-Type"] = []string{"application/json", "application/json"}
		}},
		{name: "empty body", raw: nil},
		{name: "unknown inner field", raw: []byte(`{"request":{"host":"open.feishu.cn","path":"/open-apis/docx/v1/documents/x","method":"GET","headers":{},"future":true}}`)},
		{name: "oversize body", raw: bytes.Repeat([]byte(" "), maximumWebhookBodyBytes+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := requireWebhookDeny(t, performWebhook(fixture.handler, test.raw, test.mutate))
			if result.Code != 0 || result.Version != ProtocolVersion {
				t.Fatalf("response = %#v", result)
			}
		})
	}
}

func TestServiceAndHandlerConfigurationIsFailClosed(t *testing.T) {
	fixture := newEgressGatewayFixture(t)
	if _, err := NewService(Config{}); err == nil {
		t.Fatal("NewService accepted empty dependencies")
	}
	if _, err := NewHandler(nil, time.Second); err == nil {
		t.Fatal("NewHandler accepted nil service")
	}
	for _, timeout := range []time.Duration{time.Millisecond, 451 * time.Millisecond} {
		if _, err := NewHandler(fixture.handler.service, timeout); err == nil {
			t.Fatalf("NewHandler accepted timeout %s", timeout)
		}
	}
}

func assertAuditContainsNoSecrets(t *testing.T, records []AuditRecord, secrets ...string) {
	t.Helper()
	for recordIndex, record := range records {
		value := reflect.ValueOf(record)
		typeOf := value.Type()
		for fieldIndex := 0; fieldIndex < value.NumField(); fieldIndex++ {
			if value.Field(fieldIndex).Kind() != reflect.String {
				continue
			}
			field := value.Field(fieldIndex).String()
			for _, secret := range secrets {
				if secret != "" && strings.Contains(field, secret) {
					t.Fatalf("audit record %d field %s contains a secret", recordIndex, typeOf.Field(fieldIndex).Name)
				}
			}
		}
	}
}
