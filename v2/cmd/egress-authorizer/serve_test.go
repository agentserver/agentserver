package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/egressgateway"
	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
)

func TestPolicyBootstrapAlwaysReturnsProtocolDenyWithoutDependencies(t *testing.T) {
	service := policyBootstrapDenyService{}
	handler, err := egressgateway.NewHandler(service, defaultEgressDecisionTimeout)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"request":{"host":"open.feishu.cn","path":"/open-apis/docx/v1/documents/d/raw_content","method":"GET","headers":{"X-Zti-Token":"untrusted-test-value"}}}`
	request := httptest.NewRequest(http.MethodPost, "https://egress.test"+egressPolicyPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("version", egressgateway.ProtocolVersion)
	request.Header.Set(egressgateway.ZTIHeader, "untrusted-test-value")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var result egressgateway.WebhookResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || result.Code != 0 || result.Version != egressgateway.ProtocolVersion ||
		result.Data.Result != "deny" || result.Data.ApplicationInfo != "policy_bootstrap_inactive" || len(result.Data.Header) != 0 {
		t.Fatalf("bootstrap response = status %d body %#v", response.Code, result)
	}
}

func TestEgressRoutesExposeExactHealthAndPolicySurface(t *testing.T) {
	forwarded := 0
	policy := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		forwarded++
		response.WriteHeader(http.StatusAccepted)
	})
	readiness := &egressReadiness{}
	handler := egressRoutes(policy, readiness)
	for _, test := range []struct {
		method string
		path   string
		status int
	}{
		{method: http.MethodPost, path: egressPolicyPath, status: http.StatusAccepted},
		{method: http.MethodPost, path: egressPolicyPath + "?future=1", status: http.StatusAccepted},
		{method: http.MethodGet, path: "/healthz", status: http.StatusOK},
		{method: http.MethodGet, path: "/readyz", status: http.StatusServiceUnavailable},
		{method: http.MethodGet, path: "/healthz?query=1", status: http.StatusNotFound},
		{method: http.MethodPost, path: "/healthz", status: http.StatusNotFound},
		{method: http.MethodPost, path: "/unknown", status: http.StatusNotFound},
	} {
		request := httptest.NewRequest(test.method, "http://egress.test"+test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status || response.Header().Get("Location") != "" ||
			(test.status != http.StatusAccepted && response.Header().Get("Cache-Control") != "no-store") {
			t.Fatalf("%s %s = %d headers=%v", test.method, test.path, response.Code, response.Header())
		}
	}
	if forwarded != 2 {
		t.Fatalf("policy forwards = %d", forwarded)
	}
	readiness.ready.Store(true)
	request := httptest.NewRequest(http.MethodGet, "http://egress.test/readyz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ready"`) {
		t.Fatalf("ready response = %d %s", response.Code, response.Body.String())
	}
}

func TestInsecureDevelopmentCompositionPerformsPlaceholderSwapWithoutLoggingSecrets(t *testing.T) {
	environment := validEgressDevelopmentEnvironment(t)
	signer := writeEgressTestKeyring(t, environment[egressPlaceholderKeyringEnvironment])
	var stderr bytes.Buffer
	config, err := loadEgressAuthorizerConfig(func(name string) string { return environment[name] }, egressAuthorizerServeInsecureDevelopment)
	if err != nil {
		t.Fatal(err)
	}
	dependencies, err := defaultEgressDependencies(t.Context(), config, &stderr, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	placeholders, err := egresscapability.LoadVerifier(config.placeholderKeyring)
	if err != nil {
		t.Fatal(err)
	}
	service, err := egressgateway.NewService(egressgateway.Config{
		Placeholders: placeholders, ZTI: dependencies.ZTI, Authority: dependencies.Authority,
		Audit: dependencies.Audit, AllowedPSM: config.allowedTAEPSM, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	policyHandler, err := egressgateway.NewHandler(service, config.decisionTimeout)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	claims := egresscapability.Claims{
		Version: egresscapability.Version, Issuer: "execution-gateway", Audience: egresscapability.AudienceLarkReadOnly,
		CapabilityID: "capability-1", WorkspaceID: "workspace-1", SessionID: "session-1", ActorID: "actor-1",
		EnvironmentID: "environment-1", RunID: "run-1", RunAttemptID: "attempt-1", RunAttemptGeneration: 1,
		ExecutionID: "execution-1", OperationID: "operation-1", SandboxID: "sandbox-1", TargetGeneration: 1,
		PackID: egresscapability.PackLarkReadOnly, GrantID: "grant-1", GrantVersion: 1,
		PolicySHA256: larkegresspolicy.SHA256Hex(), Executable: "lark-cli",
		IssuedAtUnixMS: now.Add(-time.Second).UnixMilli(), ExpiresAtUnixMS: now.Add(time.Minute).UnixMilli(),
	}
	placeholder, err := signer.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	original := egressgateway.OriginalRequest{
		Host: egressgateway.LarkOpenAPIHost, Path: "/open-apis/docx/v1/documents/document_1/raw_content", Method: http.MethodGet,
		Headers: map[string]string{
			egressgateway.ZTIHeader:           environment[egressDevZTITokenEnvironment],
			egressgateway.AuthorizationHeader: "Bearer " + placeholder,
		},
	}
	body, err := json.Marshal(map[string]any{"request": original})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://egress.test"+egressPolicyPath, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("version", egressgateway.ProtocolVersion)
	request.Header.Set(egressgateway.ZTIHeader, environment[egressDevZTITokenEnvironment])
	response := httptest.NewRecorder()
	egressRoutes(policyHandler, &egressReadiness{}).ServeHTTP(response, request)
	var result egressgateway.WebhookResponse
	decodeErr := json.NewDecoder(response.Body).Decode(&result)
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if response.Code != http.StatusOK || result.Data.Result != "allow" ||
		result.Data.Header[egressgateway.AuthorizationHeader] != "Bearer "+environment[egressDevLarkAccessTokenEnvironment] {
		t.Fatalf("webhook response = status %d body %#v", response.Code, result)
	}
	logs := stderr.String()
	for name, secret := range map[string]string{
		"ZTI": environment[egressDevZTITokenEnvironment], "placeholder": placeholder,
		"real Lark token": environment[egressDevLarkAccessTokenEnvironment],
	} {
		if strings.Contains(logs, secret) {
			t.Fatalf("audit log contains %s", name)
		}
	}
	if !strings.Contains(logs, `"Decision":"allow"`) || !strings.Contains(logs, claims.OperationID) {
		t.Fatalf("audit log = %q", logs)
	}
}

func writeEgressTestKeyring(t *testing.T, path string) *egresscapability.Signer {
	t.Helper()
	seed := bytes.Repeat([]byte{0x62}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	signer, err := egresscapability.NewSigner("execution-gateway", "egress-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	document := egresscapability.KeyringDocument{Version: egresscapability.KeyringVersion, Keys: []egresscapability.VerificationKeyDocument{{
		Issuer: "execution-gateway", Audience: egresscapability.AudienceLarkReadOnly,
		KeyID: "egress-key-1", Algorithm: egresscapability.SignatureAlgorithm,
		PublicKey: base64.RawURLEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
	}}}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return signer
}
