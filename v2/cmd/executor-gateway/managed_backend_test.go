package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/executorgateway"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

func TestConfigureManagedExecutionSecurityLoadsSeparatedSigners(t *testing.T) {
	configuration, egressPublicKey := validManagedBackendConfiguration(t, true)
	getenv := func(name string) string { return configuration[name] }
	backend, client, err := configureTAEBackend(getenv, gatewayServeInsecureDevelopment, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if backend == nil || client == nil {
		t.Fatal("configured TAE backend or HTTP client is nil")
	}
	t.Cleanup(client.CloseIdleConnections)
	authority := testManagedCredentialAuthority(t)

	issuer, fencer, err := configureManagedExecutionSecurity(
		getenv, gatewayServeInsecureDevelopment, backend, client, authority,
		staticManagedProcessCredentialSource{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if issuer == nil || fencer == nil {
		t.Fatalf("managed security = issuer %T fencer %T", issuer, fencer)
	}

	now := time.Now().UTC()
	request := managedBackendEnvironmentRequest(now)
	environment, err := issuer.IssueManagedProcessEnvironment(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	placeholder := environment[executorgateway.ManagedLarkUserAccessTokenEnvironment]
	if len(environment) != 5 || placeholder == "" ||
		environment[executorgateway.ManagedLarkApplicationIDEnvironment] != "cli_agentserver_sg" ||
		environment[executorgateway.ManagedLarkNoUpdateNotifierEnvironment] != "1" ||
		environment[executorgateway.ManagedLarkNoSkillsNotifierEnvironment] != "1" ||
		environment[executorgateway.ManagedLarkPathEnvironment] != executorgateway.ManagedLarkPathValue {
		t.Fatalf("managed process environment = %#v", environment)
	}
	verifier, err := egresscapability.NewVerifier([]egresscapability.TrustedKey{{
		Issuer:   configuration[gatewayEgressPlaceholderIssuerEnvironment],
		Audience: egresscapability.AudienceForProvider("lark"),
		KeyID:    configuration[gatewayEgressPlaceholderKeyIDEnvironment], PublicKey: egressPublicKey,
	}})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifier.Verify(placeholder, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if claims.OperationID != request.Operation.OperationID || claims.SandboxID != request.Target.ID ||
		claims.TargetGeneration != request.Target.Generation || claims.BindingID != "90000000-0000-4000-8000-000000000009" ||
		claims.AuthorityVersion != 9 || claims.PolicySHA256 != strings.Repeat("a", 64) {
		t.Fatalf("configured placeholder claims = %#v", claims)
	}
}

func TestConfigureManagedExecutionSecuritySelectsWorkspaceProcessEnvironment(t *testing.T) {
	configuration, egressPublicKey := validManagedBackendConfiguration(t, true)
	getenv := func(name string) string { return configuration[name] }
	backend, client, err := configureTAEBackend(getenv, gatewayServeInsecureDevelopment, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if backend == nil || client == nil {
		t.Fatal("configured TAE backend or HTTP client is nil")
	}
	t.Cleanup(client.CloseIdleConnections)
	issuer, fencer, err := configureManagedExecutionSecurity(
		getenv, gatewayServeInsecureDevelopment, backend, client,
		testManagedCredentialAuthorityForMode(t, managedcredential.ModeProcessEnv),
		staticManagedProcessCredentialSource{credential: executorgateway.ManagedLarkProcessCredential{
			Configured: true, CredentialMode: managedcredential.ModeProcessEnv,
			AccessToken: "real-lark-token", ApplicationID: "cli_agentserver_sg", BindingID: "90000000-0000-4000-8000-000000000009",
			AuthorityVersion: 9, CredentialVersion: 1, PolicySHA256: strings.Repeat("a", 64),
			TAEPSM: "bytedance.sandbox.agentserver",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if issuer == nil || fencer == nil {
		t.Fatalf("managed security = issuer %T fencer %T", issuer, fencer)
	}
	now := time.Now().UTC()
	request := managedBackendEnvironmentRequest(now)
	environment, err := issuer.IssueManagedProcessEnvironment(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	proof := environment[executorgateway.ManagedLarkAgentTraceEnvironment]
	if environment[executorgateway.ManagedLarkUserAccessTokenEnvironment] != "real-lark-token" ||
		environment[executorgateway.ManagedLarkApplicationIDEnvironment] != "cli_agentserver_sg" ||
		!egresscapability.IsProcessEnvironmentProof(proof) {
		t.Fatalf("direct process environment = %#v", environment)
	}
	verifier, err := egresscapability.NewVerifier([]egresscapability.TrustedKey{{
		Issuer:   configuration[gatewayEgressPlaceholderIssuerEnvironment],
		Audience: egresscapability.AudienceForProvider("lark"),
		KeyID:    configuration[gatewayEgressPlaceholderKeyIDEnvironment], PublicKey: egressPublicKey,
	}})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifier.VerifyProcessEnvironment(proof, now)
	if err != nil || claims.WorkspaceID != request.Principal.WorkspaceID ||
		claims.OperationID != request.Operation.OperationID || claims.CredentialVersion != 1 {
		t.Fatalf("process environment proof = %#v, %v", claims, err)
	}
}

func TestConfigureManagedExecutionSecurityRejectsPartialOrConfusedAuthority(t *testing.T) {
	for name, mutate := range map[string]func(map[string]string){
		"missing fencer issuer": func(configuration map[string]string) {
			delete(configuration, gatewaySandboxFencerIssuerEnvironment)
		},
		"partial egress": func(configuration map[string]string) {
			delete(configuration, gatewayEgressPlaceholderKeyEnvironment)
		},
		"backend fencer key id reuse": func(configuration map[string]string) {
			configuration[gatewaySandboxFencerKeyIDEnvironment] = configuration[gatewaySandboxCapabilityKeyIDEnvironment]
		},
		"fencer egress key id reuse": func(configuration map[string]string) {
			configuration[gatewayEgressPlaceholderKeyIDEnvironment] = configuration[gatewaySandboxFencerKeyIDEnvironment]
		},
	} {
		t.Run(name, func(t *testing.T) {
			configuration, _ := validManagedBackendConfiguration(t, true)
			mutate(configuration)
			getenv := func(name string) string { return configuration[name] }
			backend, client, err := configureTAEBackend(getenv, gatewayServeInsecureDevelopment, "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if client != nil {
				defer client.CloseIdleConnections()
			}
			if _, _, err := configureManagedExecutionSecurity(
				getenv, gatewayServeInsecureDevelopment, backend, client,
				testManagedCredentialAuthority(t), staticManagedProcessCredentialSource{},
			); err == nil {
				t.Fatal("unsafe managed security configuration was accepted")
			}
		})
	}
}

func TestConfigureManagedExecutionSecurityRequiresBothCoreCredentialSources(t *testing.T) {
	for name, sources := range map[string]struct {
		authority   executorgateway.ManagedLarkEgressAuthoritySource
		credentials executorgateway.ManagedLarkProcessCredentialSource
	}{
		"missing authority":                 {credentials: staticManagedProcessCredentialSource{}},
		"missing process credential source": {authority: testManagedCredentialAuthority(t)},
	} {
		t.Run(name, func(t *testing.T) {
			configuration, _ := validManagedBackendConfiguration(t, true)
			getenv := func(name string) string { return configuration[name] }
			backend, client, err := configureTAEBackend(getenv, gatewayServeInsecureDevelopment, "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if client != nil {
				defer client.CloseIdleConnections()
			}
			if _, _, err := configureManagedExecutionSecurity(
				getenv, gatewayServeInsecureDevelopment, backend, client, sources.authority, sources.credentials,
			); err == nil {
				t.Fatal("managed execution accepted a missing Core credential source")
			}
		})
	}
}

func TestConfigureTAEBackendIsOptionalButAllConfiguredValuesRequireIt(t *testing.T) {
	backend, client, err := configureTAEBackend(func(string) string { return "" }, gatewayServeInsecureDevelopment, "", "", "")
	if err != nil || backend != nil || client != nil {
		t.Fatalf("empty TAE backend = %T %T, %v", backend, client, err)
	}
	for _, configuredName := range []string{
		gatewaySandboxCapabilityIssuerEnvironment,
		gatewaySandboxFencerIssuerEnvironment,
		gatewayEgressPlaceholderIssuerEnvironment,
	} {
		t.Run(configuredName, func(t *testing.T) {
			configuration := map[string]string{configuredName: "configured"}
			if _, _, err := configureTAEBackend(func(name string) string { return configuration[name] }, gatewayServeInsecureDevelopment, "", "", ""); err == nil {
				t.Fatal("managed value without sandbox-gateway URL was accepted")
			}
		})
	}
}

func TestConfigureTAEBackendRejectsUnsafeOrigins(t *testing.T) {
	for name, origin := range map[string]string{
		"remote cleartext": "http://sandbox-gateway.internal",
		"credentials":      "http://user@127.0.0.1:9876",
		"path":             "http://127.0.0.1:9876/api",
		"query":            "http://127.0.0.1:9876?x=1",
		"fragment":         "http://127.0.0.1:9876#x",
		"unknown scheme":   "ftp://127.0.0.1:9876",
	} {
		t.Run(name, func(t *testing.T) {
			configuration, _ := validManagedBackendConfiguration(t, true)
			configuration[gatewaySandboxGatewayURLEnvironment] = origin
			if _, _, err := configureTAEBackend(func(name string) string { return configuration[name] }, gatewayServeInsecureDevelopment, "", "", ""); err == nil {
				t.Fatal("unsafe sandbox-gateway origin was accepted")
			}
		})
	}
	configuration, _ := validManagedBackendConfiguration(t, true)
	if _, _, err := configureTAEBackend(func(name string) string { return configuration[name] }, gatewayServeProduction, "", "", ""); err == nil {
		t.Fatal("production accepted cleartext sandbox-gateway")
	}
}

func validManagedBackendConfiguration(t *testing.T, includeEgress bool) (map[string]string, ed25519.PublicKey) {
	t.Helper()
	root := t.TempDir()
	writeKey := func(name string, value byte) (string, ed25519.PublicKey) {
		path := filepath.Join(root, name)
		seed := bytes.Repeat([]byte{value}, ed25519.SeedSize)
		if err := os.WriteFile(path, seed, 0o600); err != nil {
			t.Fatal(err)
		}
		privateKey := ed25519.NewKeyFromSeed(seed)
		return path, append(ed25519.PublicKey(nil), privateKey.Public().(ed25519.PublicKey)...)
	}
	backendKey, _ := writeKey("backend.key", 0x31)
	fencerKey, _ := writeKey("fencer.key", 0x32)
	egressKey, egressPublicKey := writeKey("egress.key", 0x33)
	configuration := map[string]string{
		gatewaySandboxGatewayURLEnvironment:       "http://127.0.0.1:9876",
		gatewaySandboxCapabilityIssuerEnvironment: "executor-gateway/backend",
		gatewaySandboxCapabilityKeyIDEnvironment:  "sandbox-backend-key-1",
		gatewaySandboxCapabilityKeyEnvironment:    backendKey,
		gatewaySandboxFencerIssuerEnvironment:     "executor-gateway/lifecycle",
		gatewaySandboxFencerKeyIDEnvironment:      "sandbox-fencer-key-1",
		gatewaySandboxFencerKeyEnvironment:        fencerKey,
		gatewayManagedTAEPSMEnvironment:           "bytedance.sandbox.agentserver",
	}
	if includeEgress {
		configuration[gatewayEgressPlaceholderIssuerEnvironment] = "executor-gateway/egress"
		configuration[gatewayEgressPlaceholderKeyIDEnvironment] = "egress-placeholder-key-1"
		configuration[gatewayEgressPlaceholderKeyEnvironment] = egressKey
	}
	return configuration, egressPublicKey
}

type staticManagedProcessCredentialSource struct {
	credential executorgateway.ManagedLarkProcessCredential
}

func (source staticManagedProcessCredentialSource) ResolveManagedLarkProcessCredential(
	_ context.Context,
	_ executorgateway.ManagedProcessEnvironmentRequest,
	_ string,
	_ executorgateway.ManagedLarkEgressAuthority,
) (executorgateway.ManagedLarkProcessCredential, error) {
	return source.credential, nil
}

func testManagedCredentialAuthority(t *testing.T) executorgateway.ManagedLarkEgressAuthoritySource {
	return testManagedCredentialAuthorityForMode(t, managedcredential.ModeWebhookSwap)
}

func testManagedCredentialAuthorityForMode(t *testing.T, mode string) executorgateway.ManagedLarkEgressAuthoritySource {
	t.Helper()
	authority, err := executorgateway.NewFrozenManagedLarkEgressAuthoritySource(executorgateway.ManagedLarkEgressAuthority{
		CredentialMode: mode,
		ApplicationID:  "cli_agentserver_sg",
		BindingID:      "90000000-0000-4000-8000-000000000009", AuthorityVersion: 9,
		CredentialVersion: 1, PolicySHA256: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func managedBackendEnvironmentRequest(now time.Time) executorgateway.ManagedProcessEnvironmentRequest {
	principal := executorgateway.ExecutorMCPPrincipal{
		CapabilityID: "mcp-capability-1", WorkspaceID: "40000000-0000-4000-8000-000000000004",
		SessionID: "41000000-0000-4000-8000-000000000004", ActorID: "42000000-0000-4000-8000-000000000004",
		ToolCatalogDigest: strings.Repeat("b", 64), MaxApprovalTTL: time.Minute,
		RunDeadline: now.Add(time.Minute), CapabilityExpiresAt: now.Add(2 * time.Minute),
		Run: executorgateway.ExecutorMCPRunContext{
			RunID: "43000000-0000-4000-8000-000000000004", RunAttemptID: "44000000-0000-4000-8000-000000000004",
			RunAttemptGeneration: 3, HolderID: "holder-1", ExpectedRunVersion: 4, ExpectedRunAttemptVersion: 5,
		},
	}
	target := executionbackend.Target{
		Kind: executionbackend.KindTAE, ID: "45000000-0000-4000-8000-000000000004", Generation: 6,
		EnvironmentID: "46000000-0000-4000-8000-000000000004",
	}
	return executorgateway.ManagedProcessEnvironmentRequest{
		Principal: principal, Target: target, ToolName: "shell", Executable: "lark-cli",
		Operation: executionbackend.OperationContext{
			WorkspaceID: principal.WorkspaceID, SessionID: principal.SessionID, RunID: principal.Run.RunID,
			RunAttemptID: principal.Run.RunAttemptID, RunAttemptGeneration: principal.Run.RunAttemptGeneration,
			ExecutionID: "47000000-0000-4000-8000-000000000004", OperationID: "48000000-0000-4000-8000-000000000004",
			MutationKey: "49000000-0000-4000-8000-000000000004",
		},
	}
}
