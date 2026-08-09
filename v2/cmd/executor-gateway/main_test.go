package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executorgateway"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
)

func TestRunRejectsUnknownMode(t *testing.T) {
	var stderr bytes.Buffer
	exitCode := run(t.Context(), []string{"unknown"}, func(string) string { return "" }, &bytes.Buffer{}, &stderr, nil)
	if exitCode != 2 || !strings.Contains(stderr.String(), "executor-gateway serve") || !strings.Contains(stderr.String(), "--insecure-dev") {
		t.Fatalf("run() = %d, stderr %q", exitCode, stderr.String())
	}
}

func TestRunProductionServe(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	called := false
	exitCode := run(t.Context(), []string{"serve"}, func(string) string { return "configured" }, &stdout, &stderr,
		func(_ context.Context, getenv func(string) string, output io.Writer, mode gatewayServeMode) error {
			called = true
			if mode != gatewayServeProduction || getenv("value") != "configured" {
				t.Fatalf("production serve mode = %d", mode)
			}
			fmt.Fprintln(output, "serving production")
			return nil
		})
	if exitCode != 0 || !called || stdout.String() != "serving production\n" || stderr.Len() != 0 {
		t.Fatalf("run() = %d, called %v, stdout %q, stderr %q", exitCode, called, stdout.String(), stderr.String())
	}
}

func TestRunInsecureDevServe(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	called := false
	exitCode := run(t.Context(), []string{"serve", "--insecure-dev"}, func(string) string { return "configured" }, &stdout, &stderr,
		func(_ context.Context, getenv func(string) string, output io.Writer, mode gatewayServeMode) error {
			called = true
			if mode != gatewayServeInsecureDevelopment || getenv("value") != "configured" {
				t.Fatal("getenv was not forwarded")
			}
			fmt.Fprintln(output, "serving")
			return nil
		})
	if exitCode != 0 || !called || stdout.String() != "serving\n" || stderr.Len() != 0 {
		t.Fatalf("run() = %d, called %v, stdout %q, stderr %q", exitCode, called, stdout.String(), stderr.String())
	}
}

func TestRequireLoopbackAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8443", "[::1]:8443", "localhost:8443"} {
		if err := requireLoopbackAddress(address); err != nil {
			t.Fatalf("requireLoopbackAddress(%q) error = %v", address, err)
		}
	}
	if err := requireLoopbackAddress(":8443"); err == nil {
		t.Fatal("wildcard insecure-dev listen address was accepted")
	}
}

func TestGatewayHealthAndDevelopmentRouteSurface(t *testing.T) {
	readiness := &gatewayReadiness{}
	handler := gatewayRoutes(nil, nil, nil, readiness)
	for _, test := range []struct {
		method string
		path   string
		status int
	}{
		{method: http.MethodGet, path: "/healthz", status: http.StatusOK},
		{method: http.MethodGet, path: "/readyz", status: http.StatusServiceUnavailable},
		{method: http.MethodGet, path: "/healthz?query=1", status: http.StatusNotFound},
		{method: http.MethodGet, path: "/healthz?", status: http.StatusNotFound},
		{method: http.MethodPost, path: "/healthz", status: http.StatusNotFound},
		{method: http.MethodGet, path: "//healthz", status: http.StatusNotFound},
		{method: http.MethodGet, path: executorgateway.AgentxEnrollmentPath, status: http.StatusNotFound},
		{method: http.MethodGet, path: executorgateway.AgentxChallengePath, status: http.StatusNotFound},
	} {
		request := httptest.NewRequest(test.method, "https://gateway.test"+test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Location") != "" {
			t.Fatalf("%s %s = %d headers %+v", test.method, test.path, response.Code, response.Header())
		}
	}
	readiness.ready.Store(true)
	request := httptest.NewRequest(http.MethodGet, "https://gateway.test/readyz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "ready") {
		t.Fatalf("ready response = %d %s", response.Code, response.Body)
	}
}

func TestProductionGatewayListenersHaveDisjointRouteSurfaces(t *testing.T) {
	mcpCalls := 0
	agentxCalls := 0
	mcp := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		mcpCalls++
		response.WriteHeader(http.StatusAccepted)
	})
	agentx := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		agentxCalls++
		response.WriteHeader(http.StatusSwitchingProtocols)
	})
	internal := gatewayInternalRoutes(mcp, &gatewayReadiness{})
	public := gatewayPublicRoutes(agentx, nil, &gatewayReadiness{})

	request := httptest.NewRequest(http.MethodPost, "https://executor.internal"+executorgateway.ExecutorMCPPath, nil)
	response := httptest.NewRecorder()
	internal.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || mcpCalls != 1 {
		t.Fatalf("internal MCP = %d calls=%d", response.Code, mcpCalls)
	}
	request = httptest.NewRequest(http.MethodPost, "http://executor.public"+executorgateway.ExecutorMCPPath, nil)
	response = httptest.NewRecorder()
	public.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || mcpCalls != 1 {
		t.Fatalf("public MCP = %d internal calls=%d", response.Code, mcpCalls)
	}
	request = httptest.NewRequest(http.MethodGet, "http://executor.public"+executorgateway.AgentxConnectPath, nil)
	response = httptest.NewRecorder()
	public.ServeHTTP(response, request)
	if response.Code != http.StatusSwitchingProtocols || agentxCalls != 1 {
		t.Fatalf("public agentx = %d calls=%d", response.Code, agentxCalls)
	}
	request = httptest.NewRequest(http.MethodGet, "https://executor.internal"+executorgateway.AgentxConnectPath, nil)
	response = httptest.NewRecorder()
	internal.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || agentxCalls != 1 {
		t.Fatalf("internal agentx = %d public calls=%d", response.Code, agentxCalls)
	}
}

func TestDevMCPAuthenticatorRequiresExactBearer(t *testing.T) {
	const bearer = "0123456789abcdef0123456789abcdef"
	authenticator, err := newDevMCPAuthenticator(
		bearer,
		"40000000-0000-4000-8000-000000000004",
		"43000000-0000-4000-8000-000000000004",
		"44000000-0000-4000-8000-000000000004",
		"20000000-0000-4000-8000-000000000002",
		strings.Repeat("a", 64),
		time.Minute,
		executorgateway.ExecutorMCPRunContext{
			RunID:                     "41000000-0000-4000-8000-000000000004",
			RunAttemptID:              "42000000-0000-4000-8000-000000000004",
			RunAttemptGeneration:      3,
			HolderID:                  "dev-holder",
			ExpectedRunVersion:        4,
			ExpectedRunAttemptVersion: 5,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://gateway.test/mcp", nil)
	request.Header.Set("Authorization", "Bearer "+bearer)
	principal, err := authenticator.AuthenticateExecutorMCP(request)
	if err != nil {
		t.Fatal(err)
	}
	if principal.WorkspaceID != "40000000-0000-4000-8000-000000000004" ||
		principal.SessionID != "43000000-0000-4000-8000-000000000004" ||
		principal.ActorID != "44000000-0000-4000-8000-000000000004" || principal.MaxApprovalTTL != time.Minute ||
		principal.ExecutorID != "20000000-0000-4000-8000-000000000002" ||
		principal.ToolCatalogDigest != strings.Repeat("a", 64) ||
		!strings.HasPrefix(principal.CapabilityID, "insecure-dev:") || strings.Contains(principal.CapabilityID, bearer) {
		t.Fatalf("development MCP principal = %+v", principal)
	}
	request.Header.Add("Authorization", "Bearer "+bearer)
	if _, err := authenticator.AuthenticateExecutorMCP(request); err == nil {
		t.Fatal("duplicate Authorization headers were accepted")
	}
	if principal.Run.RunID != "41000000-0000-4000-8000-000000000004" || principal.Run.ExpectedRunAttemptVersion != 5 {
		t.Fatalf("development MCP run context = %+v", principal.Run)
	}
	if _, err := newDevMCPAuthenticator("short", principal.WorkspaceID, principal.SessionID, principal.ActorID, principal.ExecutorID, principal.ToolCatalogDigest, principal.MaxApprovalTTL, principal.Run); err == nil {
		t.Fatal("short development MCP bearer was accepted")
	}
}

func TestDevRunCapabilityAuthenticatorMapsDynamicAttemptAuthority(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000).UTC()
	codec, err := runcapability.NewDevelopmentCodec(bytes.Repeat([]byte{0x41}, 32))
	if err != nil {
		t.Fatal(err)
	}
	const executorID = "20000000-0000-4000-8000-000000000002"
	authenticator, err := newDevRunCapabilityAuthenticator(codec, executorID, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	claims := testDevRunCapabilityClaims(now, executorID, runcapability.AudienceExecutorMCP)
	token, err := codec.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://gateway.test/mcp", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	principal, err := authenticator.AuthenticateExecutorMCP(request)
	if err != nil {
		t.Fatal(err)
	}
	if principal.CapabilityID != "insecure-dev:"+claims.CapabilityID ||
		principal.WorkspaceID != claims.WorkspaceID || principal.ActorID != claims.ActorID || principal.ExecutorID != claims.ExecutorID ||
		principal.ToolCatalogDigest != claims.ToolCatalogDigest || principal.Run.RunID != claims.RunID ||
		principal.Run.RunAttemptID != claims.RunAttemptID || principal.Run.RunAttemptGeneration != claims.RunAttemptGeneration ||
		principal.Run.HolderID != claims.HolderID || principal.Run.ExpectedRunVersion != claims.ExpectedRunVersion ||
		principal.Run.ExpectedRunAttemptVersion != claims.ExpectedRunAttemptVersion ||
		principal.MaxApprovalTTL != time.Duration(claims.MaxApprovalTTLMillis)*time.Millisecond ||
		!principal.RunDeadline.Equal(time.UnixMilli(claims.RunDeadlineUnixMS)) ||
		!principal.CapabilityExpiresAt.Equal(time.UnixMilli(claims.ExpiresAtUnixMS)) {
		t.Fatalf("dynamic development MCP principal = %+v", principal)
	}

	request.Header.Add("Authorization", "Bearer "+token)
	if _, err := authenticator.AuthenticateExecutorMCP(request); err == nil {
		t.Fatal("duplicate dynamic capability headers were accepted")
	}
}

func TestDevRunCapabilityAuthenticatorRejectsWrongAuthorityAndExpiry(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000).UTC()
	codec, err := runcapability.NewDevelopmentCodec(bytes.Repeat([]byte{0x51}, 32))
	if err != nil {
		t.Fatal(err)
	}
	const executorID = "20000000-0000-4000-8000-000000000002"
	authenticator, err := newDevRunCapabilityAuthenticator(codec, executorID, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]runcapability.Claims{
		"wrong-executor": testDevRunCapabilityClaims(now, "21000000-0000-4000-8000-000000000002", runcapability.AudienceExecutorMCP),
		"wrong-audience": testDevRunCapabilityClaims(now, executorID, runcapability.AudienceLLMProxy),
		"expired": func() runcapability.Claims {
			claims := testDevRunCapabilityClaims(now, executorID, runcapability.AudienceExecutorMCP)
			claims.IssuedAtUnixMS = now.Add(-2 * time.Hour).UnixMilli()
			claims.RunDeadlineUnixMS = now.Add(-time.Minute).UnixMilli()
			claims.ExpiresAtUnixMS = now.UnixMilli()
			return claims
		}(),
	}
	for name, claims := range tests {
		t.Run(name, func(t *testing.T) {
			token, err := codec.Sign(claims)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "https://gateway.test/mcp", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			if _, err := authenticator.AuthenticateExecutorMCP(request); err == nil {
				t.Fatal("invalid dynamic development authority was accepted")
			}
		})
	}
	for name, authorization := range map[string]string{
		"missing-prefix": "not-bearer",
		"padded":         "Bearer  token",
		"comma":          "Bearer token,other",
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "https://gateway.test/mcp", nil)
			request.Header.Set("Authorization", authorization)
			if _, err := authenticator.AuthenticateExecutorMCP(request); err == nil {
				t.Fatal("malformed dynamic development authorization was accepted")
			}
		})
	}
}

func TestConfiguredDevMCPAuthenticatorSelectsDynamicKeyWithoutStaticAttempt(t *testing.T) {
	key := bytes.Repeat([]byte{0x61}, 32)
	encodedKey := base64.RawURLEncoding.EncodeToString(key)
	const executorID = "20000000-0000-4000-8000-000000000002"
	configuration := map[string]string{gatewayDevRunCapabilityKeyEnvironment: encodedKey}
	authenticator, err := configuredDevMCPAuthenticator(func(name string) string { return configuration[name] }, executorID)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := runcapability.NewDevelopmentCodec(key)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	token, err := codec.Sign(testDevRunCapabilityClaims(now, executorID, runcapability.AudienceExecutorMCP))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://gateway.test/mcp", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if _, err := authenticator.AuthenticateExecutorMCP(request); err != nil {
		t.Fatal(err)
	}

	configuration[gatewayDevRunCapabilityKeyEnvironment] = encodedKey + "="
	if _, err := configuredDevMCPAuthenticator(func(name string) string { return configuration[name] }, executorID); err == nil {
		t.Fatal("invalid dynamic key fell back to static attempt configuration")
	}
}

func TestRequiredPositiveGatewayInt64(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "1.5", "9223372036854775808"} {
		if _, err := requiredPositiveGatewayInt64(func(string) string { return value }, "TEST_VALUE"); err == nil {
			t.Errorf("invalid positive integer %q was accepted", value)
		}
	}
	value, err := requiredPositiveGatewayInt64(func(string) string { return "17" }, "TEST_VALUE")
	if err != nil || value != 17 {
		t.Fatalf("requiredPositiveGatewayInt64() = %d, %v", value, err)
	}
}

func testDevRunCapabilityClaims(now time.Time, executorID, audience string) runcapability.Claims {
	claims := runcapability.Claims{
		Version:      runcapability.DevelopmentVersion,
		CapabilityID: "22000000-0000-4000-8000-000000000002", Audience: audience,
		WorkspaceID: "40000000-0000-4000-8000-000000000004",
		SessionID:   "41000000-0000-4000-8000-000000000004", RunID: "42000000-0000-4000-8000-000000000004",
		RunAttemptID: "43000000-0000-4000-8000-000000000004", RunAttemptGeneration: 3,
		ActorID: "44000000-0000-4000-8000-000000000004", HolderID: "dev-holder",
		IssuedAtUnixMS: now.Add(-time.Minute).UnixMilli(), RunDeadlineUnixMS: now.Add(30 * time.Minute).UnixMilli(),
		ExpiresAtUnixMS: now.Add(time.Hour).UnixMilli(),
	}
	if audience == runcapability.AudienceExecutorMCP {
		claims.ExecutorID = executorID
		claims.ToolCatalogDigest = strings.Repeat("a", 64)
		claims.ExpectedRunVersion = 4
		claims.ExpectedRunAttemptVersion = 5
		claims.MaxApprovalTTLMillis = 60_000
	} else {
		claims.Model = "gpt-5"
		claims.Provider = "development-llmproxy"
	}
	return claims
}
