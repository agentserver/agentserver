package main

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
	"github.com/agentserver/agentserver/v2/internal/taepolicy"
)

func TestLoadEgressAuthorizerDevelopmentConfig(t *testing.T) {
	environment := validEgressDevelopmentEnvironment(t)
	config, err := loadEgressAuthorizerConfig(func(name string) string { return environment[name] }, egressAuthorizerServeInsecureDevelopment)
	if err != nil {
		t.Fatal(err)
	}
	if config.production || config.listenAddress != "127.0.0.1:0" || config.allowedTAEPSM != "prod.tae.agent-gateway" ||
		config.decisionTimeout != defaultEgressDecisionTimeout || config.devCredentialLifetime != defaultDevCredentialLifetime ||
		config.devZTIToken != environment[egressDevZTITokenEnvironment] || config.devLarkAccessToken != environment[egressDevLarkAccessTokenEnvironment] {
		t.Fatalf("development config = %+v", config)
	}
}

func TestLoadEgressAuthorizerProductionConfigDoesNotConsumeDevelopmentSecrets(t *testing.T) {
	environment := validEgressProductionEnvironment(t)
	environment[egressDevZTITokenEnvironment] = "bad token with whitespace"
	environment[egressDevLarkAccessTokenEnvironment] = "short"
	environment[egressDevCredentialLifetimeEnvironment] = "not-a-duration"
	config, err := loadEgressAuthorizerConfig(func(name string) string { return environment[name] }, egressAuthorizerServeProduction)
	if err != nil {
		t.Fatal(err)
	}
	if !config.production || config.devZTIToken != "" || config.devLarkAccessToken != "" || config.devCredentialLifetime != 0 ||
		config.tlsCertificate == "" || config.tlsKey == "" || config.coreURL != "https://core.internal" ||
		config.spiffeIdentity != environment[egressSPIFFEIdentityEnvironment] ||
		config.taePolicy.BindingSHA256 != environment[egressTAEPolicyBindingEnvironment] {
		t.Fatalf("production config = %+v", config)
	}
}

func TestLoadEgressAuthorizerProductionConfigAcceptsFourRegionPolicyCatalog(t *testing.T) {
	environment := validEgressProductionEnvironment(t)
	bindings := make([]taepolicy.Binding, 0, len(managedsandboxprofile.Regions()))
	for _, region := range managedsandboxprofile.Regions() {
		binding := taepolicy.Binding{
			Version: taepolicy.BindingVersion, Region: region, SandboxPSM: environment[egressAllowedTAEPSMEnvironment],
			Revision: "lark-readonly-" + region + "-v1", PolicySHA256: larkegresspolicy.SHA256Hex(),
			PublicHost: taepolicy.PublicHost, PublicAccess: taepolicy.PublicAccessWhitelist, PublicWebhookRequired: true,
			WebhookMode: "psm", WebhookPSM: "agentserver.egress-authorizer", WebhookPath: taepolicy.WebhookPath,
			Published: true, Approved: true, EvidenceRef: "tae-change/" + region + "/2026-08-15",
		}
		binding.BindingSHA256 = binding.DigestHex()
		bindings = append(bindings, binding)
	}
	raw, err := json.Marshal(taePolicyBindingsDocument{Bindings: bindings})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		egressTAEPolicyRevisionEnvironment, egressTAEPolicySHA256Environment, egressTAEPolicyBindingEnvironment,
		egressTAEPolicyHostEnvironment, egressTAEPolicyAccessEnvironment, egressTAEWebhookRequiredEnvironment,
		egressTAEWebhookModeEnvironment, egressTAEWebhookPSMEnvironment, egressTAEWebhookURLEnvironment,
		egressTAEWebhookPathEnvironment, egressTAEPolicyPublishedEnvironment,
		egressTAEPolicyApprovedEnvironment, egressTAEPolicyEvidenceEnvironment,
	} {
		delete(environment, name)
	}
	environment[egressTAEPolicyBindingsEnvironment] = string(raw)
	config, err := loadEgressAuthorizerConfig(func(name string) string { return environment[name] }, egressAuthorizerServeProduction)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.taePolicies) != 4 || config.taePolicy != bindings[0] {
		t.Fatalf("profiled TAE policies = %+v", config.taePolicies)
	}
	environment[egressTAEPolicyRevisionEnvironment] = "legacy-must-not-mix"
	if _, err := loadEgressAuthorizerConfig(func(name string) string { return environment[name] }, egressAuthorizerServeProduction); err == nil {
		t.Fatal("profiled TAE policy catalog accepted a legacy policy setting")
	}
}

func TestLoadEgressAuthorizerPolicyBootstrapConsumesOnlyServerIdentity(t *testing.T) {
	environment := validEgressProductionEnvironment(t)
	for _, name := range []string{
		egressCoreURLEnvironment, egressCoreCAEnvironment, egressCoreCertificateEnvironment,
		egressCoreKeyEnvironment, egressCoreServerNameEnvironment,
		egressPlaceholderKeyringEnvironment, egressAllowedTAEPSMEnvironment,
		egressTAEPolicyRevisionEnvironment, egressTAEPolicySHA256Environment,
		egressTAEPolicyBindingEnvironment, egressTAEPolicyEvidenceEnvironment,
	} {
		environment[name] = "deliberately invalid bootstrap input\n"
	}
	config, err := loadEgressAuthorizerConfig(func(name string) string { return environment[name] }, egressAuthorizerServePolicyBootstrap)
	if err != nil {
		t.Fatal(err)
	}
	if !config.production || !config.policyBootstrap || config.tlsCertificate == "" || config.tlsKey == "" ||
		config.spiffeIdentity == "" || config.coreURL != "" || config.placeholderKeyring != "" ||
		config.allowedTAEPSM != "" || config.taePolicy != (taepolicy.Binding{}) {
		t.Fatalf("policy bootstrap config = %+v", config)
	}
}

func TestLoadEgressAuthorizerConfigRejectsUnsafeValues(t *testing.T) {
	for name, mutate := range map[string]func(map[string]string){
		"wildcard development listen": func(environment map[string]string) { environment[egressListenAddressEnvironment] = ":8080" },
		"remote development listen":   func(environment map[string]string) { environment[egressListenAddressEnvironment] = "10.0.0.1:8080" },
		"relative keyring":            func(environment map[string]string) { environment[egressPlaceholderKeyringEnvironment] = "keys.json" },
		"invalid psm":                 func(environment map[string]string) { environment[egressAllowedTAEPSMEnvironment] = "bad\npsm" },
		"short zti":                   func(environment map[string]string) { environment[egressDevZTITokenEnvironment] = "short" },
		"space in Lark token": func(environment map[string]string) {
			environment[egressDevLarkAccessTokenEnvironment] = "token with forbidden spaces"
		},
		"decision over budget":         func(environment map[string]string) { environment[egressDecisionTimeoutEnvironment] = "451ms" },
		"credential lifetime too long": func(environment map[string]string) { environment[egressDevCredentialLifetimeEnvironment] = "25h" },
	} {
		t.Run(name, func(t *testing.T) {
			environment := validEgressDevelopmentEnvironment(t)
			mutate(environment)
			if _, err := loadEgressAuthorizerConfig(func(name string) string { return environment[name] }, egressAuthorizerServeInsecureDevelopment); err == nil {
				t.Fatal("unsafe egress-authorizer configuration was accepted")
			}
		})
	}
	production := validEgressProductionEnvironment(t)
	delete(production, egressTLSKeyEnvironment)
	if _, err := loadEgressAuthorizerConfig(func(name string) string { return production[name] }, egressAuthorizerServeProduction); err == nil {
		t.Fatal("production config without TLS key was accepted")
	}
	for name, mutate := range map[string]func(map[string]string){
		"cleartext Core": func(environment map[string]string) { environment[egressCoreURLEnvironment] = "http://core.internal" },
		"Core URL credentials": func(environment map[string]string) {
			environment[egressCoreURLEnvironment] = "https://user@core.internal"
		},
		"invalid SPIFFE identity": func(environment map[string]string) {
			environment[egressSPIFFEIdentityEnvironment] = "https://egress-authorizer.internal"
		},
		"relative Core key": func(environment map[string]string) { environment[egressCoreKeyEnvironment] = "client.key" },
		"unpublished TAE policy": func(environment map[string]string) {
			environment[egressTAEPolicyPublishedEnvironment] = "false"
		},
		"TAE policy digest drift": func(environment map[string]string) {
			environment[egressTAEPolicyBindingEnvironment] = strings.Repeat("f", 64)
		},
		"wrong TAE webhook path": func(environment map[string]string) {
			environment[egressTAEWebhookPathEnvironment] = "/v1/other"
		},
	} {
		t.Run(name, func(t *testing.T) {
			environment := validEgressProductionEnvironment(t)
			mutate(environment)
			if _, err := loadEgressAuthorizerConfig(func(name string) string { return environment[name] }, egressAuthorizerServeProduction); err == nil {
				t.Fatal("unsafe production egress-authorizer configuration was accepted")
			}
		})
	}
}

func TestOptionalEgressDurationBounds(t *testing.T) {
	if value, err := optionalEgressDuration("275ms", time.Second, time.Millisecond, time.Second, "TEST"); err != nil || value != 275*time.Millisecond {
		t.Fatalf("duration = %s, %v", value, err)
	}
	if _, err := optionalEgressDuration("1.5us", time.Second, time.Millisecond, time.Second, "TEST"); err == nil {
		t.Fatal("fractional-millisecond duration was accepted")
	}
}

func validEgressDevelopmentEnvironment(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		egressListenAddressEnvironment:      "127.0.0.1:0",
		egressPlaceholderKeyringEnvironment: filepath.Join(t.TempDir(), "egress-keyring.json"),
		egressAllowedTAEPSMEnvironment:      "prod.tae.agent-gateway",
		egressDevZTITokenEnvironment:        "development-zti-token-0001",
		egressDevLarkAccessTokenEnvironment: "development-lark-token-0001",
	}
}

func validEgressProductionEnvironment(t *testing.T) map[string]string {
	t.Helper()
	root := t.TempDir()
	environment := map[string]string{
		egressListenAddressEnvironment:      ":8443",
		egressTLSCertificateEnvironment:     filepath.Join(root, "server.crt"),
		egressTLSKeyEnvironment:             filepath.Join(root, "server.key"),
		egressSPIFFEIdentityEnvironment:     "spiffe://agentserver.internal/ns/agentserver/sa/egress-authorizer",
		egressCoreURLEnvironment:            "https://core.internal",
		egressCoreCAEnvironment:             filepath.Join(root, "core-ca.crt"),
		egressCoreCertificateEnvironment:    filepath.Join(root, "core-client.crt"),
		egressCoreKeyEnvironment:            filepath.Join(root, "core-client.key"),
		egressCoreServerNameEnvironment:     "core.internal",
		egressPlaceholderKeyringEnvironment: filepath.Join(root, "egress-keyring.json"),
		egressAllowedTAEPSMEnvironment:      "prod.tae.agent-gateway",
		egressDecisionTimeoutEnvironment:    "300ms",
	}
	policy := taepolicy.Binding{
		Version: taepolicy.BindingVersion, Region: "sg", SandboxPSM: environment[egressAllowedTAEPSMEnvironment],
		Revision: "lark-readonly-v1", PolicySHA256: larkegresspolicy.SHA256Hex(),
		PublicHost: taepolicy.PublicHost, PublicAccess: taepolicy.PublicAccessWhitelist, PublicWebhookRequired: true,
		WebhookMode: "psm", WebhookPSM: "agentserver.egress-authorizer", WebhookPath: taepolicy.WebhookPath,
		Published: true, Approved: true, EvidenceRef: "tae-change/sg-2026-08-06",
	}
	policy.BindingSHA256 = policy.DigestHex()
	environment[egressTAEPolicyRevisionEnvironment] = policy.Revision
	environment[egressTAEPolicySHA256Environment] = policy.PolicySHA256
	environment[egressTAEPolicyBindingEnvironment] = policy.BindingSHA256
	environment[egressTAEPolicyHostEnvironment] = policy.PublicHost
	environment[egressTAEPolicyAccessEnvironment] = policy.PublicAccess
	environment[egressTAEWebhookRequiredEnvironment] = strconv.FormatBool(policy.PublicWebhookRequired)
	environment[egressTAEWebhookModeEnvironment] = policy.WebhookMode
	environment[egressTAEWebhookPSMEnvironment] = policy.WebhookPSM
	environment[egressTAEWebhookURLEnvironment] = policy.WebhookURL
	environment[egressTAEWebhookPathEnvironment] = policy.WebhookPath
	environment[egressTAEPolicyPublishedEnvironment] = strconv.FormatBool(policy.Published)
	environment[egressTAEPolicyApprovedEnvironment] = strconv.FormatBool(policy.Approved)
	environment[egressTAEPolicyEvidenceEnvironment] = policy.EvidenceRef
	return environment
}

func TestDevelopmentSecretsRejectPaddingAndControls(t *testing.T) {
	for _, value := range []string{" " + strings.Repeat("a", 16), strings.Repeat("a", 16) + "\n", strings.Repeat("a", maximumDevelopmentSecretBytes+1)} {
		if validDevelopmentSecret(value) {
			t.Fatalf("validDevelopmentSecret(%q) = true", value[:min(len(value), 32)])
		}
	}
}
