package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/objectruntime"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
)

func TestServeCoreRequiresDistinctHarnessPoolIdentityBeforeOpeningDatabase(t *testing.T) {
	configuration := map[string]string{
		databaseURLEnvironment:             "postgres://unused",
		coreListenAddressEnvironment:       "127.0.0.1:0",
		coreTLSCertificateEnvironment:      "/unused/server.crt",
		coreTLSKeyEnvironment:              "/unused/server.key",
		coreClientCAEnvironment:            "/unused/client-ca.crt",
		coreGatewayIdentityEnvironment:     "spiffe://agentserver.local/ns/agentserver/sa/executor-gateway",
		coreHarnessPoolIdentityEnvironment: "",
	}
	getenv := func(name string) string { return configuration[name] }
	err := serveCore(t.Context(), getenv, io.Discard, coreServeInsecureDevelopment)
	if err == nil || !strings.Contains(err.Error(), coreHarnessPoolIdentityEnvironment+" is required") {
		t.Fatalf("missing harness-pool identity error = %v", err)
	}

	configuration[coreHarnessPoolIdentityEnvironment] = configuration[coreGatewayIdentityEnvironment]
	err = serveCore(t.Context(), getenv, io.Discard, coreServeInsecureDevelopment)
	if err == nil || !strings.Contains(err.Error(), "must be distinct") {
		t.Fatalf("shared workload identity error = %v", err)
	}

	configuration[coreHarnessPoolIdentityEnvironment] = "spiffe://agentserver.local/ns/agentserver/sa/harness-pool"
	err = serveCore(t.Context(), getenv, io.Discard, coreServeInsecureDevelopment)
	if err == nil || !strings.Contains(err.Error(), coreBrowserIdentityEnvironment+" is required") {
		t.Fatalf("missing browser-gateway identity error = %v", err)
	}
	configuration[coreBrowserIdentityEnvironment] = configuration[coreGatewayIdentityEnvironment]
	err = serveCore(t.Context(), getenv, io.Discard, coreServeInsecureDevelopment)
	if err == nil || !strings.Contains(err.Error(), "browser-gateway, executor-gateway, and harness-pool") {
		t.Fatalf("shared browser workload identity error = %v", err)
	}
}

func TestServeCoreProductionRequiresDistinctLLMProxyIdentity(t *testing.T) {
	configuration := map[string]string{
		databaseURLEnvironment:             "postgres://unused",
		coreListenAddressEnvironment:       "127.0.0.1:0",
		coreTLSCertificateEnvironment:      "/unused/server.crt",
		coreTLSKeyEnvironment:              "/unused/server.key",
		coreClientCAEnvironment:            "/unused/client-ca.crt",
		coreGatewayIdentityEnvironment:     "spiffe://agentserver.local/ns/agentserver/sa/executor-gateway",
		coreHarnessPoolIdentityEnvironment: "spiffe://agentserver.local/ns/agentserver/sa/harness-pool",
		coreBrowserIdentityEnvironment:     "spiffe://agentserver.local/ns/agentserver/sa/browser-gateway",
	}
	getenv := func(name string) string { return configuration[name] }
	if err := serveCore(t.Context(), getenv, io.Discard, coreServeProduction); err == nil || !strings.Contains(err.Error(), coreLLMProxyIdentityEnvironment+" is required") {
		t.Fatalf("missing llmproxy identity error = %v", err)
	}
	configuration[coreLLMProxyIdentityEnvironment] = configuration[coreGatewayIdentityEnvironment]
	if err := serveCore(t.Context(), getenv, io.Discard, coreServeProduction); err == nil || !strings.Contains(err.Error(), "must be distinct") {
		t.Fatalf("shared llmproxy identity error = %v", err)
	}
	configuration[coreLLMProxyIdentityEnvironment] = "spiffe://agentserver.local/ns/agentserver/sa/llmproxy"
	if err := serveCore(t.Context(), getenv, io.Discard, coreServeProduction); err == nil || !strings.Contains(err.Error(), coreHydraIntrospectionEnvironment+" is required") {
		t.Fatalf("distinct llmproxy identity next-boundary error = %v", err)
	}
}

func TestConfigureCorePromptStoreSeparatesProductionAndDevelopment(t *testing.T) {
	root := t.TempDir()
	development, description, err := configureCorePromptStore(
		t.Context(),
		func(name string) string {
			if name == coreDevPromptObjectRootEnvironment {
				return root
			}
			return ""
		},
		coreServeInsecureDevelopment,
	)
	if err != nil || development == nil || !strings.Contains(description, "INSECURE DEV") {
		t.Fatalf("development prompt store = %T, %q, %v", development, description, err)
	}

	_, _, err = configureCorePromptStore(t.Context(), func(string) string { return "" }, coreServeProduction)
	if err == nil || !strings.Contains(err.Error(), objectruntime.ObjectPrefixEnvironment+" is required") {
		t.Fatalf("production prompt store missing routing error = %v", err)
	}
	_, _, err = configureCorePromptStore(t.Context(), func(string) string { return "" }, coreServeMode(255))
	if err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("invalid Core serve mode error = %v", err)
	}
}

func TestConfigureCoreProductionRunCapabilitiesLoadsOnlyProductionAuthority(t *testing.T) {
	configuration := productionRunCapabilityEnvironment(t, true)
	getenv := func(name string) string { return configuration[name] }
	loaded, err := configureCoreProductionRunCapabilities(getenv, coreServeProduction)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || loaded.signer.Issuer() != configuration[coreCapabilityIssuerEnvironment] ||
		loaded.signer.KeyID() != configuration[coreCapabilityKeyIDEnvironment] ||
		len(loaded.verifier.KeyIDs()) != 1 || loaded.verifier.KeyIDs()[0] != loaded.signer.KeyID() ||
		loaded.policy.ExecutorID != configuration[coreProductionExecutorEnvironment] ||
		loaded.policy.MaxRunDuration != 30*time.Minute || loaded.policy.MaxApprovalTTL != 10*time.Second ||
		loaded.policy.ExpiryGrace != 45*time.Second {
		t.Fatalf("production capability config = %+v", loaded)
	}
	development, err := configureCoreProductionRunCapabilities(func(name string) string {
		t.Fatalf("insecure-development mode read production setting %s", name)
		return ""
	}, coreServeInsecureDevelopment)
	if err != nil || development != nil {
		t.Fatalf("development production capability config = %+v, %v", development, err)
	}
	if _, err := configureCoreProductionRunCapabilities(getenv, coreServeMode(255)); err == nil {
		t.Fatal("invalid Core mode was accepted")
	}

	missingActive := productionRunCapabilityEnvironment(t, false)
	if _, err := configureCoreProductionRunCapabilities(func(name string) string { return missingActive[name] }, coreServeProduction); err == nil || !strings.Contains(err.Error(), "active signing key") {
		t.Fatalf("missing active key error = %v", err)
	}
	invalidPolicy := productionRunCapabilityEnvironment(t, true)
	invalidPolicy[coreMaxApprovalTTLEnvironment] = "31m"
	if _, err := configureCoreProductionRunCapabilities(func(name string) string { return invalidPolicy[name] }, coreServeProduction); err == nil || !strings.Contains(err.Error(), "maximum approval TTL") {
		t.Fatalf("invalid capability policy error = %v", err)
	}
}

func TestMountCoreRunCapabilityRoutesIsProductionOnly(t *testing.T) {
	development := http.NewServeMux()
	mountCoreRunCapabilityRoutes(development, nil)
	response := httptest.NewRecorder()
	development.ServeHTTP(response, httptest.NewRequest(http.MethodPost, corecontract.IssueRunCapabilitiesPath, nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("development run capability route status = %d", response.Code)
	}

	production := http.NewServeMux()
	mountCoreRunCapabilityRoutes(production, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	for _, path := range []string{
		corecontract.IssueRunCapabilitiesPath,
		corecontract.AuthorizeExecutorRunCapabilityPath,
		corecontract.AuthorizeLLMProxyRunCapabilityPath,
	} {
		response = httptest.NewRecorder()
		production.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusNoContent {
			t.Fatalf("production route %s status = %d", path, response.Code)
		}
	}
}

func TestConfigureCoreProductionEnrollmentLoadsRestrictedAuthorityOnlyInProduction(t *testing.T) {
	capabilityEnvironment := productionRunCapabilityEnvironment(t, true)
	capabilityConfig, err := configureCoreProductionRunCapabilities(
		func(name string) string { return capabilityEnvironment[name] },
		coreServeProduction,
	)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	keyPath := filepath.Join(root, "enrollment.key")
	key := base64.RawURLEncoding.EncodeToString(bytesOfForCoreTest(0x6b, 32))
	if err := os.WriteFile(keyPath, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration := map[string]string{coreEnrollmentKeyEnvironment: keyPath, coreEnrollmentTTLEnvironment: "10m"}
	loaded, err := configureCoreProductionEnrollment(func(name string) string { return configuration[name] }, coreServeProduction, capabilityConfig)
	if err != nil || loaded == nil || loaded.tokens.Issuer() != capabilityConfig.signer.Issuer() || loaded.ttl != 10*time.Minute {
		t.Fatalf("production enrollment config = %+v, %v", loaded, err)
	}
	development, err := configureCoreProductionEnrollment(func(name string) string {
		t.Fatalf("development enrollment read %s", name)
		return ""
	}, coreServeInsecureDevelopment, nil)
	if err != nil || development != nil {
		t.Fatalf("development enrollment config = %+v, %v", development, err)
	}
	configuration[coreEnrollmentTTLEnvironment] = "16m"
	if _, err := configureCoreProductionEnrollment(func(name string) string { return configuration[name] }, coreServeProduction, capabilityConfig); err == nil {
		t.Fatal("oversized executor enrollment TTL was accepted")
	}
}

func TestMountCoreExecutorIdentityRoutesIsProductionOnly(t *testing.T) {
	development := http.NewServeMux()
	mountCoreExecutorIdentityRoutes(development, nil, nil)
	response := httptest.NewRecorder()
	development.ServeHTTP(response, httptest.NewRequest(http.MethodPost, corecontract.CompleteExecutorEnrollmentPath, nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("development executor identity route status = %d", response.Code)
	}

	production := http.NewServeMux()
	users := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusCreated) })
	internal := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusNoContent) })
	mountCoreExecutorIdentityRoutes(production, users, internal)
	for path, want := range map[string]int{
		corecontract.CreateExecutorResourcePath("71000000-0000-4000-8000-000000000002"):                                               http.StatusCreated,
		corecontract.IssueExecutorEnrollmentTokenPath("71000000-0000-4000-8000-000000000002", "71000000-0000-4000-8000-000000000003"): http.StatusCreated,
		corecontract.CompleteExecutorEnrollmentPath:                                                                                   http.StatusNoContent,
		corecontract.AuthorizeExecutorConnectionPath:                                                                                  http.StatusNoContent,
	} {
		response = httptest.NewRecorder()
		production.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != want {
			t.Fatalf("production executor route %s status = %d, want %d", path, response.Code, want)
		}
	}
}

func TestCoreBrowserConfigurationParsersFailClosed(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	encoded := base64.RawURLEncoding.EncodeToString(key)
	decoded, err := decodeRunCursorKey(encoded)
	if err != nil || string(decoded) != string(key) {
		t.Fatalf("decodeRunCursorKey() = %x, %v", decoded, err)
	}
	for _, invalid := range []string{"short", encoded + "=", base64.RawURLEncoding.EncodeToString(key[:31])} {
		if _, err := decodeRunCursorKey(invalid); err == nil {
			t.Fatalf("invalid cursor key %q was accepted", invalid)
		}
	}
	if value, err := strictOptionalBoolean("true", "TEST"); err != nil || !value {
		t.Fatalf("strictOptionalBoolean(true) = %v, %v", value, err)
	}
	if _, err := strictOptionalBoolean("1", "TEST"); err == nil {
		t.Fatal("noncanonical boolean was accepted")
	}
	if tools := commaSeparatedTools("read_file,shell"); len(tools) != 2 || tools[0] != "read_file" || tools[1] != "shell" {
		t.Fatalf("commaSeparatedTools() = %q", tools)
	}
}

func productionRunCapabilityEnvironment(t *testing.T, includeActiveKey bool) map[string]string {
	t.Helper()
	root := t.TempDir()
	active := ed25519.NewKeyFromSeed(bytesOfForCoreTest(0x61, ed25519.SeedSize))
	verification := active
	keyID := "core-production-active"
	if !includeActiveKey {
		verification = ed25519.NewKeyFromSeed(bytesOfForCoreTest(0x62, ed25519.SeedSize))
		keyID = "core-production-other"
	}
	privateKeyPath := filepath.Join(root, "active.seed")
	if err := os.WriteFile(privateKeyPath, active.Seed(), 0o600); err != nil {
		t.Fatal(err)
	}
	keyringRaw, err := json.Marshal(runcapability.ProductionKeyringDocument{
		Version: runcapability.ProductionKeyringVersion,
		Keys: []runcapability.ProductionVerificationKeyDocument{{
			KeyID: keyID, Algorithm: runcapability.ProductionSignatureAlgorithm,
			PublicKey: base64.RawURLEncoding.EncodeToString(verification.Public().(ed25519.PublicKey)),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	keyringPath := filepath.Join(root, "keyring.json")
	if err := os.WriteFile(keyringPath, keyringRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		coreCapabilityIssuerEnvironment:      "https://agentserver.example.test/core",
		coreCapabilityKeyIDEnvironment:       "core-production-active",
		coreCapabilityPrivateKeyEnvironment:  privateKeyPath,
		coreCapabilityKeyringEnvironment:     keyringPath,
		coreProductionExecutorEnvironment:    "63000000-0000-4000-8000-000000000001",
		coreMaxRunDurationEnvironment:        "30m",
		coreMaxApprovalTTLEnvironment:        "10s",
		coreCapabilityExpiryGraceEnvironment: "45s",
	}
}

func bytesOfForCoreTest(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}
