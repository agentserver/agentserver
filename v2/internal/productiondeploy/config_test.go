package productiondeploy

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/bkectlpolicy"
	"github.com/agentserver/agentserver/v2/internal/enrollmenttoken"
	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
	"github.com/agentserver/agentserver/v2/internal/productionimage"
	"github.com/agentserver/agentserver/v2/internal/stockruntime"
	"github.com/agentserver/agentserver/v2/internal/taeimage"
	"github.com/agentserver/agentserver/v2/internal/taenetworkreport"
	"github.com/agentserver/agentserver/v2/internal/taepolicy"
)

func TestValidateConfigAcceptsSupportedLinuxDeployment(t *testing.T) {
	loaded, err := ValidateConfig(validConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Document.Platform != ProductionPlatform || loaded.MaxRunDuration.String() != "30m0s" ||
		strings.Join(loaded.Document.Runtime.AllowedTools, ",") != "list_environments,read_file,shell" {
		t.Fatalf("loaded config = %+v", loaded)
	}
}

func TestValidateConfigRequiresFixedWorkspaceInitialSandboxRegion(t *testing.T) {
	document := fourRegionConfigDocument()
	document.SandboxRegions.DefaultRegion = managedsandboxprofile.RegionCN
	if _, err := ValidateConfig(document); err == nil || !strings.Contains(err.Error(), managedsandboxprofile.DefaultRegion) {
		t.Fatalf("noncanonical sandbox default error = %v", err)
	}

	document = fourRegionConfigDocument()
	document.SandboxRegions.Regions = document.SandboxRegions.Regions[:3]
	document.SandboxProfiles = document.SandboxProfiles[:3]
	if _, err := ValidateConfig(document); err == nil || !strings.Contains(err.Error(), "no installed profile") {
		t.Fatalf("missing initial-region profile error = %v", err)
	}
}

func TestValidateConfigRejectsNonAMD64SGDeployment(t *testing.T) {
	document := validConfigDocument()
	document.Platform = "linux-arm64"
	if _, err := ValidateConfig(document); err == nil {
		t.Fatal("SG production accepted a non-amd64 platform")
	}
}

func TestValidateConfigAcceptsCanonicalIPv6ExternalEgress(t *testing.T) {
	document := validConfigDocument()
	document.Network.CoreExternalEgress = append(document.Network.CoreExternalEgress,
		EgressRuleDocument{CIDR: "fdbd:dc51:fe:200d::1/128", Ports: []uint16{443}})
	loaded, err := ValidateConfig(document)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Document.Network.CoreExternalEgress[1].CIDR; got != "fdbd:dc51:fe:200d::1/128" {
		t.Fatalf("IPv6 egress CIDR = %q", got)
	}
}

func TestValidateConfigAcceptsHarnessWithoutExternalCIDRs(t *testing.T) {
	document := validConfigDocument()
	document.Network.HarnessExternalEgress = nil
	loaded, err := ValidateConfig(document)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Document.Network.HarnessExternalEgress) != 0 {
		t.Fatalf("harness external egress = %#v, want empty", loaded.Document.Network.HarnessExternalEgress)
	}
}

func TestParseConfigRejectsUnknownDuplicateAndSecretFields(t *testing.T) {
	document := validConfigDocument()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	withUnknown := append([]byte(nil), raw[:len(raw)-1]...)
	withUnknown = append(withUnknown, []byte(`,"awsAccessKeyId":"forbidden"}`)...)
	if _, err := ParseConfig(withUnknown); err == nil {
		t.Fatal("unknown static credential field was accepted")
	}
	withPullSecret := strings.Replace(
		string(raw), `"images":{`, `"images":{"pullSecret":"agentserver-registry-pull",`, 1,
	)
	if _, err := ParseConfig([]byte(withPullSecret)); err == nil {
		t.Fatal("public SG registry config accepted an image pull credential")
	}
	withGlobalCredentialMode := strings.Replace(
		string(raw), `"lark":{`, `"lark":{"credentialMode":"process_env",`, 1,
	)
	if _, err := ParseConfig([]byte(withGlobalCredentialMode)); err == nil {
		t.Fatal("production config accepted a deployment-wide managed Lark credential mode")
	}
	if _, err := ParseConfig([]byte(`{"version":1,"version":1}`)); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate-key error = %v", err)
	}
}

func TestValidateConfigUsesEnrollmentAuthorityMaximumTTL(t *testing.T) {
	document := validConfigDocument()
	document.Runtime.EnrollmentTokenTTL = enrollmenttoken.MaximumTTL.String()
	loaded, err := ValidateConfig(document)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.EnrollmentTokenTTL != enrollmenttoken.MaximumTTL {
		t.Fatalf("enrollment TTL = %s", loaded.EnrollmentTokenTTL)
	}
	document.Runtime.EnrollmentTokenTTL = (enrollmenttoken.MaximumTTL + time.Millisecond).String()
	if _, err := ValidateConfig(document); err == nil {
		t.Fatal("enrollment TTL above the token authority maximum was accepted")
	}
}

func TestValidateConfigRejectsUnsafeProductionShapes(t *testing.T) {
	for name, mutate := range map[string]func(*ConfigDocument){
		"platform":           func(value *ConfigDocument) { value.Platform = "linux-riscv64" },
		"non-SG region":      func(value *ConfigDocument) { value.Region = "cn" },
		"wrong namespace":    func(value *ConfigDocument) { value.Namespace = "agentserver-test" },
		"wrong trust domain": func(value *ConfigDocument) { value.TrustDomain = "other.byted.bps.dev" },
		"mutable image":      func(value *ConfigDocument) { value.Images.Service = "registry.example.test/agentserver:latest" },
		"wrong service registry": func(value *ConfigDocument) {
			value.Images.Service = "registry.example.test/agentserver/service@sha256:" + strings.Repeat("1", 64)
		},
		"wrong harness registry": func(value *ConfigDocument) {
			value.Images.Harness = "registry.example.test/agentserver/harness@sha256:" + strings.Repeat("2", 64)
		},
		"wrong Hydra registry": func(value *ConfigDocument) {
			value.Images.Hydra = "registry.example.test/agentserver/hydra@sha256:" + strings.Repeat("3", 64)
		},
		"gateway replicas implied": func(value *ConfigDocument) { value.Services.ExecutorGateway.InternalPort = 9443 },
		"wrong frontend hostname":  func(value *ConfigDocument) { value.Ingress.FrontendHostname = "agent-cn.byted.bps.dev" },
		"missing gateway selector": func(value *ConfigDocument) { value.Ingress.GatewayPodSelector = nil },
		"invalid object mode":      func(value *ConfigDocument) { value.Objects.Mode = "encrypted" },
		"invalid object prefix":    func(value *ConfigDocument) { value.Objects.Prefix = "agentserver/../production" },
		"invalid provider region":  func(value *ConfigDocument) { value.Objects.S3Region = "not a region" },
		"missing S3 endpoint":      func(value *ConfigDocument) { value.Objects.S3Endpoint = "" },
		"open egress":              func(value *ConfigDocument) { value.Network.CoreExternalEgress[0].CIDR = "0.0.0.0/0" },
		"open IPv6 egress":         func(value *ConfigDocument) { value.Network.CoreExternalEgress[0].CIDR = "::/0" },
		"one-stack sandbox egress": func(value *ConfigDocument) {
			value.Network.SandboxExternalEgress = []EgressRuleDocument{{CIDR: "10.33.0.0/24", Ports: []uint16{443}}}
		},
		"one-stack authorizer egress": func(value *ConfigDocument) {
			value.Network.EgressAuthorizerExternalEgress = []EgressRuleDocument{{CIDR: "10.34.0.0/24", Ports: []uint16{443}}}
		},
		"one-stack authorizer ingress": func(value *ConfigDocument) {
			value.Network.EgressAuthorizerIngress = []string{"10.35.0.0/24"}
		},
		"noncanonical IPv6 egress": func(value *ConfigDocument) {
			value.Network.CoreExternalEgress[0].CIDR = "fdbd:dc51:fe:200d:0:0:0:1/128"
		},
		"missing DNS selector":      func(value *ConfigDocument) { value.Network.DNSPodSelector = nil },
		"DNS service collision":     func(value *ConfigDocument) { value.Network.DNSClusterIP = value.Services.Core.ClusterIP },
		"oversize DNS label":        func(value *ConfigDocument) { value.ClusterDomain = strings.Repeat("a", 64) + ".test" },
		"low core availability":     func(value *ConfigDocument) { value.Replicas.Core = 1 },
		"low platform availability": func(value *ConfigDocument) { value.Replicas.PlatformGateway = 1 },
		"low browser availability":  func(value *ConfigDocument) { value.Replicas.BrowserGateway = 1 },
		"low llmproxy availability": func(value *ConfigDocument) { value.Replicas.LLMProxy = 1 },
		"low Hydra availability":    func(value *ConfigDocument) { value.Replicas.Hydra = 1 },
		"external Hydra Admin": func(value *ConfigDocument) {
			value.OAuth.Hydra.AdminURL = "https://hydra-admin.example.test"
		},
		"Hydra issuer slash drift": func(value *ConfigDocument) {
			value.OAuth.Hydra.Issuer = "https://auth-sg.byted.bps.dev"
		},
		"request above CPU limit": func(value *ConfigDocument) {
			value.Resources.Core.Requests.CPU = "3"
		},
		"request above memory limit": func(value *ConfigDocument) {
			value.Resources.Core.Requests.Memory = "3Gi"
		},
		"harness tmpfs above limit": func(value *ConfigDocument) {
			value.Resources.HarnessPool.Limits.Memory = "10Gi"
		},
		"bad redirect": func(value *ConfigDocument) {
			value.OAuth.ExternalOIDC.RedirectURL = "https://other.example.test/callback"
		},
		"unknown tool": func(value *ConfigDocument) {
			value.Runtime.AllowedTools = []string{"list_environments", "exec_command"}
		},
		"unreviewed runtime manifest": func(value *ConfigDocument) { value.Runtime.RuntimeManifestSHA256 = strings.Repeat("9", 64) },
		"capability key mismatch":     func(value *ConfigDocument) { value.Runtime.CapabilitySigningKeyID = "other" },
		"manifest key mismatch":       func(value *ConfigDocument) { value.Runtime.ManifestSigningKeyID = "other" },
		"runtime allowlist drift":     func(value *ConfigDocument) { value.Runtime.CheckpointAllowlistVersion++ },
		"shared secret":               func(value *ConfigDocument) { value.Secrets.HarnessWorker = value.Secrets.HarnessPool },
		"missing TAE sandbox ID":      func(value *ConfigDocument) { value.Managed.TAE.SandboxID = "" },
		"uppercase TAE revision ID":   func(value *ConfigDocument) { value.Managed.TAE.RevisionID = "Revision-1" },
	} {
		t.Run(name, func(t *testing.T) {
			document := validConfigDocument()
			mutate(&document)
			if _, err := ValidateConfig(document); err == nil {
				t.Fatal("unsafe production config was accepted")
			}
		})
	}
}

func TestValidateConfigBindsManagedNetworkEvidenceToNormalizedFacts(t *testing.T) {
	document := validConfigDocument()
	document.Network.SandboxExternalEgress = []EgressRuleDocument{
		{CIDR: "10.33.0.0/24", Ports: []uint16{443}},
		{CIDR: "fdbd:dc51:fe:3300::/64", Ports: []uint16{443}},
	}
	document.Managed.TAE.NetworkEvidence.BindingSHA256 = managedTAENetworkEvidenceDigest(document)
	document.Managed.Environment.RuntimeProfileSHA256 = managedRuntimeProfileDigest(document, document.Managed)
	document.Managed.Environment.PackSetSHA256 = managedPackSetDigest(document.Managed)
	if err := refreshDefaultManagedSandboxProfile(&document); err != nil {
		t.Fatal(err)
	}
	// Equivalent ordering is normalized before the evidence digest is checked.
	slices.Reverse(document.Network.SandboxExternalEgress)
	if _, err := ValidateConfig(document); err != nil {
		t.Fatalf("equivalent network rule order changed the binding: %v", err)
	}

	for name, mutate := range map[string]func(*ConfigDocument){
		"sandbox egress": func(value *ConfigDocument) {
			value.Network.SandboxExternalEgress = []EgressRuleDocument{
				{CIDR: "10.36.0.0/24", Ports: []uint16{443}},
				{CIDR: "fdbd:dc51:fe:3600::/64", Ports: []uint16{443}},
			}
		},
		"authorizer egress": func(value *ConfigDocument) {
			value.Network.EgressAuthorizerExternalEgress = []EgressRuleDocument{
				{CIDR: "10.37.0.0/24", Ports: []uint16{443}},
				{CIDR: "fdbd:dc51:fe:3700::/64", Ports: []uint16{443}},
			}
		},
		"webhook NAT ingress": func(value *ConfigDocument) {
			value.Network.EgressAuthorizerIngress = []string{"10.38.0.0/24", "fdbd:dc51:fe:3800::/64"}
		},
		"webhook PSM": func(value *ConfigDocument) {
			value.Managed.TAE.Policy.WebhookPSM = "agentserver.egress-authorizer.changed"
			value.Managed.TAE.Policy.BindingSHA256 = managedTAEPolicyBinding(value.Managed.TAE).DigestHex()
		},
		"report digest": func(value *ConfigDocument) {
			value.Managed.TAE.NetworkEvidence.ReportSHA256 = canonicalDigest("different-report")
		},
		"terminal sandbox revision": func(value *ConfigDocument) {
			value.Managed.TAE.RevisionID = "revision-2"
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := validConfigDocument()
			mutate(&changed)
			if _, err := ValidateConfig(changed); err == nil || !strings.Contains(err.Error(), "networkEvidence.bindingSha256") {
				t.Fatalf("network evidence drift error = %v", err)
			}
		})
	}
}

func TestValidateConfigPinsProductionTAEPSM(t *testing.T) {
	document := validConfigDocument()
	document.Managed.TAE.PSM = "another.sandbox.psm"
	if _, err := ValidateConfig(document); err == nil || !strings.Contains(err.Error(), "managedExecutor.tae.psm must be exactly") {
		t.Fatalf("unexpected production TAE PSM validation result: %v", err)
	}
}

func TestValidateConfigAcceptsOnlyEvidenceFreePolicyBootstrap(t *testing.T) {
	document := policyBootstrapConfigDocument()
	loaded, err := ValidateConfig(document)
	if err != nil {
		t.Fatal(err)
	}
	if !managedPolicyBootstrap(loaded.Document.Managed) || loaded.ManagedOwnerPolicySHA256 != "" {
		t.Fatalf("bootstrap loaded config = %+v", loaded)
	}
	for name, mutate := range map[string]func(*ConfigDocument){
		"published": func(value *ConfigDocument) { value.Managed.TAE.Policy.Published = true },
		"approved":  func(value *ConfigDocument) { value.Managed.TAE.Policy.Approved = true },
		"policy evidence": func(value *ConfigDocument) {
			value.Managed.TAE.Policy.EvidenceRef = "tae-change/not-yet-approved"
		},
		"network evidence": func(value *ConfigDocument) {
			value.Managed.TAE.NetworkEvidence.Version = managedNetworkEvidenceVersion
		},
		"runtime lock": func(value *ConfigDocument) {
			value.Managed.Environment.RuntimeProfileSHA256 = canonicalDigest("premature-runtime")
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := policyBootstrapConfigDocument()
			mutate(&changed)
			changed.Managed.TAE.Policy.BindingSHA256 = managedTAEPolicyBinding(changed.Managed.TAE).DigestHex()
			if _, err := ValidateConfig(changed); err == nil {
				t.Fatal("policy bootstrap accepted a premature production claim")
			}
		})
	}
}

func policyBootstrapConfigDocument() ConfigDocument {
	document := validConfigDocument()
	document.Managed.Stage = ManagedExecutorStageBootstrap
	document.Managed.Environment.RuntimeProfileSHA256 = ""
	document.Managed.Environment.PackSetSHA256 = ""
	document.Managed.TAE.Policy.Published = false
	document.Managed.TAE.Policy.Approved = false
	document.Managed.TAE.Policy.EvidenceRef = ""
	document.Managed.TAE.Policy.BindingSHA256 = managedTAEPolicyBinding(document.Managed.TAE).DigestHex()
	document.Managed.TAE.NetworkEvidence = ManagedTAENetworkEvidenceDocument{}
	if err := refreshDefaultManagedSandboxProfile(&document); err != nil {
		panic(err)
	}
	return document
}

func directConfigDocument() ConfigDocument {
	document := validConfigDocument()
	policy := &document.Managed.TAE.Policy
	policy.Revision = "tae-system-default-feishu-v1"
	policy.PublicHost = taepolicy.SystemDefaultHost
	policy.PublicAccess = taepolicy.SystemDefaultAccess
	policy.PublicWebhookRequired = false
	policy.WebhookMode = ""
	policy.WebhookPSM = ""
	policy.WebhookURL = ""
	policy.WebhookPath = ""
	policy.EvidenceRef = "tae-system-policy/group:system.out.limit"
	policy.BindingSHA256 = managedTAEPolicyBinding(document.Managed.TAE).DigestHex()
	document.Managed.TAE.NetworkEvidence.BindingSHA256 = managedTAENetworkEvidenceDigest(document)
	document.Managed.Environment.RuntimeProfileSHA256 = managedRuntimeProfileDigest(document, document.Managed)
	document.Managed.Environment.PackSetSHA256 = managedPackSetDigest(document.Managed)
	if err := refreshDefaultManagedSandboxProfile(&document); err != nil {
		panic(err)
	}
	return document
}

func directPolicyBootstrapConfigDocument() ConfigDocument {
	document, err := preparePolicyBootstrapDocument(directConfigDocument())
	if err != nil {
		panic(err)
	}
	return document
}

func TestValidateConfigAcceptsDirectSystemDefaultPolicy(t *testing.T) {
	document := directConfigDocument()
	loaded, err := ValidateConfig(document)
	if err != nil {
		t.Fatal(err)
	}
	if managedEgressAuthorizerEnabled(loaded.Document.Managed) || loaded.Document.Managed.TAE.Policy.PublicWebhookRequired {
		t.Fatalf("direct managed executor enabled a webhook: %+v", loaded.Document.Managed.TAE.Policy)
	}
	for name, mutate := range map[string]func(*ManagedTAEPolicyDocument){
		"webhook mode": func(policy *ManagedTAEPolicyDocument) { policy.WebhookMode = "url" },
		"webhook psm":  func(policy *ManagedTAEPolicyDocument) { policy.WebhookPSM = "agentserver.egress-authorizer" },
		"webhook url": func(policy *ManagedTAEPolicyDocument) {
			policy.WebhookURL = ProductionEgressAuthorizerURL
		},
		"webhook path": func(policy *ManagedTAEPolicyDocument) { policy.WebhookPath = taepolicy.WebhookPath },
	} {
		t.Run(name, func(t *testing.T) {
			changed := directConfigDocument()
			mutate(&changed.Managed.TAE.Policy)
			changed.Managed.TAE.Policy.BindingSHA256 = managedTAEPolicyBinding(changed.Managed.TAE).DigestHex()
			changed.Managed.TAE.NetworkEvidence.BindingSHA256 = managedTAENetworkEvidenceDigest(changed)
			changed.Managed.Environment.RuntimeProfileSHA256 = managedRuntimeProfileDigest(changed, changed.Managed)
			changed.Managed.Environment.PackSetSHA256 = managedPackSetDigest(changed.Managed)
			if _, err := ValidateConfig(changed); err == nil {
				t.Fatal("direct policy with webhook configuration was accepted")
			}
		})
	}
}

func TestPinManagedTerminalRevisionIsBootstrapOnlyAndFailClosed(t *testing.T) {
	bootstrap := policyBootstrapConfigDocument()
	originalRevision := bootstrap.Managed.TAE.RevisionID
	pinned, err := PinManagedTerminalRevisionDocument(bootstrap, bootstrap.Managed.TAE.SandboxID, "revision-v8")
	if err != nil {
		t.Fatal(err)
	}
	if pinned.Managed.TAE.RevisionID != "revision-v8" ||
		pinned.Managed.Stage != ManagedExecutorStageBootstrap ||
		pinned.Managed.Environment.RuntimeProfileSHA256 != "" ||
		pinned.Managed.Environment.PackSetSHA256 != "" ||
		pinned.Managed.TAE.NetworkEvidence != (ManagedTAENetworkEvidenceDocument{}) ||
		bootstrap.Managed.TAE.RevisionID != originalRevision {
		t.Fatalf("pinned Terminal bootstrap = %+v; original revision = %q", pinned.Managed, bootstrap.Managed.TAE.RevisionID)
	}
	for name, input := range map[string]struct {
		document   ConfigDocument
		sandboxID  string
		revisionID string
	}{
		"active source":     {document: validConfigDocument(), sandboxID: bootstrap.Managed.TAE.SandboxID, revisionID: "revision-v8"},
		"sandbox mismatch":  {document: bootstrap, sandboxID: "sandbox-2", revisionID: "revision-v8"},
		"invalid revision":  {document: bootstrap, sandboxID: bootstrap.Managed.TAE.SandboxID, revisionID: "Revision_V8"},
		"sentinel revision": {document: bootstrap, sandboxID: bootstrap.Managed.TAE.SandboxID, revisionID: "replace-me"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := PinManagedTerminalRevisionDocument(input.document, input.sandboxID, input.revisionID); err == nil {
				t.Fatal("unsafe Terminal revision pin was accepted")
			}
		})
	}
}

func TestRetargetManagedTerminalIsBootstrapOnlyAtomicAndFailClosed(t *testing.T) {
	bootstrap := policyBootstrapConfigDocument()
	wantImage := ProductionManagedSandboxImage + "@sha256:" + releaseDigest("d")
	retargeted, err := RetargetManagedTerminalDocument(
		bootstrap, bootstrap.Managed.TAE.SandboxID, "sandbox-new", "revision-v9",
		"60000000-0000-4000-8000-000000000006", wantImage,
	)
	if err != nil {
		t.Fatal(err)
	}
	if retargeted.Managed.TAE.SandboxID != "sandbox-new" ||
		retargeted.Managed.TAE.RevisionID != "revision-v9" || retargeted.Images.ManagedSandbox != wantImage ||
		retargeted.Managed.Environment.EnvironmentID != "60000000-0000-4000-8000-000000000006" ||
		retargeted.Managed.Stage != ManagedExecutorStageBootstrap ||
		retargeted.Managed.Environment.RuntimeProfileSHA256 != "" ||
		retargeted.Managed.Environment.PackSetSHA256 != "" ||
		retargeted.Managed.TAE.NetworkEvidence != (ManagedTAENetworkEvidenceDocument{}) ||
		bootstrap.Managed.TAE.SandboxID == retargeted.Managed.TAE.SandboxID ||
		bootstrap.Managed.Environment.EnvironmentID == retargeted.Managed.Environment.EnvironmentID {
		t.Fatalf("retargeted Terminal bootstrap = %+v", retargeted.Managed)
	}
	for name, mutate := range map[string]func(*ConfigDocument, *string, *string, *string, *string, *string){
		"active source":       func(document *ConfigDocument, _, _, _, _, _ *string) { *document = validConfigDocument() },
		"stale current id":    func(_ *ConfigDocument, expected, _, _, _, _ *string) { *expected = "sandbox-stale" },
		"invalid new id":      func(_ *ConfigDocument, _, sandbox, _, _, _ *string) { *sandbox = "Sandbox_New" },
		"sentinel revision":   func(_ *ConfigDocument, _, _, revision, _, _ *string) { *revision = "pending-v9" },
		"invalid environment": func(_ *ConfigDocument, _, _, _, environment, _ *string) { *environment = "not-a-uuid" },
		"reused environment": func(document *ConfigDocument, _, _, _, environment, _ *string) {
			*environment = document.Managed.Environment.EnvironmentID
		},
		"wrong image repository": func(_ *ConfigDocument, _, _, _, _, image *string) {
			*image = "ghcr.io/agentserver/v2-managed-sandbox@sha256:" + releaseDigest("d")
		},
		"mutable image": func(_ *ConfigDocument, _, _, _, _, image *string) {
			*image = ProductionManagedSandboxImage + ":main"
		},
	} {
		t.Run(name, func(t *testing.T) {
			document := bootstrap
			expected, sandbox, revision := bootstrap.Managed.TAE.SandboxID, "sandbox-new", "revision-v9"
			environment, image := "60000000-0000-4000-8000-000000000006", wantImage
			mutate(&document, &expected, &sandbox, &revision, &environment, &image)
			if _, err := RetargetManagedTerminalDocument(document, expected, sandbox, revision, environment, image); err == nil {
				t.Fatal("unsafe Terminal Sandbox retarget was accepted")
			}
		})
	}
}

func TestRetargetDirectManagedTerminalIsBootstrapOnlyAtomicAndWebhookFree(t *testing.T) {
	bootstrap := policyBootstrapConfigDocument()
	wantImage := ProductionManagedSandboxImage + "@sha256:" + releaseDigest("d")
	retargeted, err := RetargetDirectManagedTerminalDocument(
		bootstrap, bootstrap.Managed.TAE.SandboxID, "sandbox-direct", "revision-direct",
		"60000000-0000-4000-8000-000000000006", wantImage,
	)
	if err != nil {
		t.Fatal(err)
	}
	policy := retargeted.Managed.TAE.Policy
	if retargeted.Managed.TAE.SandboxID != "sandbox-direct" || retargeted.Managed.TAE.RevisionID != "revision-direct" ||
		retargeted.Managed.Environment.EnvironmentID != "60000000-0000-4000-8000-000000000006" ||
		retargeted.Images.ManagedSandbox != wantImage || !managedPolicyBootstrap(retargeted.Managed) ||
		policy.PublicHost != taepolicy.SystemDefaultHost || policy.PublicAccess != taepolicy.SystemDefaultAccess ||
		policy.PublicWebhookRequired || policy.WebhookMode != "" || policy.WebhookPSM != "" ||
		policy.WebhookURL != "" || policy.WebhookPath != "" ||
		policy.BindingSHA256 != managedTAEPolicyBinding(retargeted.Managed.TAE).DigestHex() {
		t.Fatalf("direct retarget = %+v", retargeted.Managed)
	}
	if _, err := RetargetDirectManagedTerminalDocument(
		validConfigDocument(), validConfigDocument().Managed.TAE.SandboxID, "sandbox-direct", "revision-direct",
		"60000000-0000-4000-8000-000000000006", wantImage,
	); err == nil {
		t.Fatal("active config was directly retargeted")
	}
}

func legacyMalivaPolicyBootstrapConfigDocument() ConfigDocument {
	document := directPolicyBootstrapConfigDocument()
	document.ProxyProfiles[0] = ManagedSandboxProxyProfileDocument{
		Name:      managedSandboxProxyI18NTTLegacy,
		URL:       managedSandboxProxyLegacyURL,
		Namespace: ProductionTAEProxyNamespace, PodSelector: map[string]string{"app": managedSandboxProxyLegacyApp},
		Port: ProductionTAEProxyPort,
	}
	managed := document.Managed
	managed.TAE.ProxyProfile = managedSandboxProxyI18NTTLegacy
	managed.TAE.Policy.BindingSHA256 = managedTAEPolicyBinding(managed.TAE).DigestHex()
	if err := refreshManagedSandboxProfileFromManaged(&document, 0, managed); err != nil {
		panic(err)
	}
	if _, err := ValidateConfig(document); err != nil {
		panic(err)
	}
	return document
}

func syd2aProxyRetarget(document ConfigDocument) ManagedSandboxProxyRetarget {
	return ManagedSandboxProxyRetarget{
		Region:       managedsandboxprofile.RegionI18NTT,
		ExpectedName: document.Managed.TAE.ProxyProfile,
		ExpectedURL:  document.ProxyProfiles[0].URL,
		Proxy: ManagedSandboxProxyProfileDocument{
			Name: ManagedSandboxProxyI18NTT, URL: ProductionTAEProxyURL,
			Namespace:   ProductionTAEProxyNamespace,
			PodSelector: map[string]string{"app": ProductionTAEProxyPodApp}, Port: ProductionTAEProxyPort,
		},
	}
}

func TestRetargetManagedSandboxProxyIsBootstrapOnlyAtomicAndFailClosed(t *testing.T) {
	bootstrap := legacyMalivaPolicyBootstrapConfigDocument()
	originalProfileID := bootstrap.SandboxProfiles[0].ProfileID
	originalBinding := bootstrap.SandboxProfiles[0].BindingSHA256
	retargeted, err := RetargetManagedSandboxProxyDocument(bootstrap, syd2aProxyRetarget(bootstrap))
	if err != nil {
		t.Fatal(err)
	}
	proxy := retargeted.ProxyProfiles[0]
	profile := retargeted.SandboxProfiles[0]
	if proxy.Name != ManagedSandboxProxyI18NTT || proxy.URL != ProductionTAEProxyURL ||
		proxy.Namespace != ProductionTAEProxyNamespace || proxy.PodSelector["app"] != ProductionTAEProxyPodApp ||
		profile.TAE.ProxyProfile != ManagedSandboxProxyI18NTT || retargeted.Managed.TAE.ProxyProfile != ManagedSandboxProxyI18NTT ||
		profile.ProfileID == originalProfileID || profile.BindingSHA256 == originalBinding ||
		!managedPolicyBootstrap(retargeted.Managed) || profile.TAE.NetworkEvidence != (ManagedTAENetworkEvidenceDocument{}) ||
		bootstrap.ProxyProfiles[0].Name != managedSandboxProxyI18NTTLegacy {
		t.Fatalf("retargeted proxy = %+v, profile = %+v, original profile/binding = %s/%s", proxy, profile, originalProfileID, originalBinding)
	}
	if _, err := ValidateConfig(retargeted); err != nil {
		t.Fatalf("retargeted config is invalid: %v", err)
	}

	for name, mutate := range map[string]func(*ConfigDocument, *ManagedSandboxProxyRetarget){
		"active source": func(document *ConfigDocument, _ *ManagedSandboxProxyRetarget) {
			*document = directConfigDocument()
		},
		"direct region": func(_ *ConfigDocument, retarget *ManagedSandboxProxyRetarget) {
			retarget.Region = managedsandboxprofile.RegionBOE
		},
		"stale name": func(_ *ConfigDocument, retarget *ManagedSandboxProxyRetarget) {
			retarget.ExpectedName = "merlin-stale"
		},
		"stale URL": func(_ *ConfigDocument, retarget *ManagedSandboxProxyRetarget) {
			retarget.ExpectedURL = "socks5h://stale.example.internal:1080"
		},
		"invalid replacement": func(_ *ConfigDocument, retarget *ManagedSandboxProxyRetarget) {
			retarget.Proxy.Name = "merlin-unreviewed"
		},
		"unchanged authority": func(document *ConfigDocument, retarget *ManagedSandboxProxyRetarget) {
			retarget.Proxy = document.ProxyProfiles[0]
		},
	} {
		t.Run(name, func(t *testing.T) {
			document := bootstrap
			retarget := syd2aProxyRetarget(bootstrap)
			mutate(&document, &retarget)
			if _, err := RetargetManagedSandboxProxyDocument(document, retarget); err == nil {
				t.Fatal("unsafe managed sandbox proxy retarget was accepted")
			}
		})
	}
}

func TestActivateManagedExecutorBindsAllExternalEvidence(t *testing.T) {
	bootstrap := policyBootstrapConfigDocument()
	revision := "lark-readonly-v2"
	report := validActivationNetworkReport(bootstrap, revision)
	raw, err := taenetworkreport.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	reportSHA256 := taenetworkreport.SHA256(raw)
	active, err := ActivateManagedExecutorDocument(
		bootstrap,
		revision, "tae-security-ticket/sg-2026-08-09", report,
		reportSHA256, "artifact://agentserver/sg-network/2026-08-09/report.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !managedExecutionActive(active.Managed) || !active.Managed.TAE.Policy.Published ||
		!active.Managed.TAE.Policy.Approved || active.Managed.TAE.Policy.EvidenceRef == "" ||
		active.Managed.TAE.NetworkEvidence.ReportSHA256 != reportSHA256 ||
		active.Managed.TAE.NetworkEvidence.BindingSHA256 != managedTAENetworkEvidenceDigest(active) ||
		active.Managed.Environment.RuntimeProfileSHA256 != managedRuntimeProfileDigest(active, active.Managed) ||
		active.Managed.Environment.PackSetSHA256 != managedPackSetDigest(active.Managed) {
		t.Fatalf("activated managed executor = %+v", active.Managed)
	}
	for name, mutate := range map[string]func(*ConfigDocument, *string, *string, *taenetworkreport.Report, *string, *string){
		"active source": func(document *ConfigDocument, _, _ *string, _ *taenetworkreport.Report, _, _ *string) {
			document.Managed.Stage = ManagedExecutorStageActive
			document.Managed.TAE.Policy.Published = true
			document.Managed.TAE.Policy.Approved = true
			document.Managed.TAE.Policy.EvidenceRef = "ticket/already-active"
			document.Managed.TAE.Policy.BindingSHA256 = managedTAEPolicyBinding(document.Managed.TAE).DigestHex()
			document.Managed.TAE.NetworkEvidence = validConfigDocument().Managed.TAE.NetworkEvidence
			document.Managed.TAE.NetworkEvidence.BindingSHA256 = managedTAENetworkEvidenceDigest(*document)
			document.Managed.Environment.RuntimeProfileSHA256 = managedRuntimeProfileDigest(*document, document.Managed)
			document.Managed.Environment.PackSetSHA256 = managedPackSetDigest(document.Managed)
		},
		"policy sentinel": func(_ *ConfigDocument, _ *string, policyRef *string, _ *taenetworkreport.Report, _, _ *string) {
			*policyRef = "REPLACE_TICKET"
		},
		"network sentinel": func(_ *ConfigDocument, _, _ *string, _ *taenetworkreport.Report, _, networkRef *string) {
			*networkRef = "TODO/report"
		},
		"synthetic digest": func(_ *ConfigDocument, _, _ *string, _ *taenetworkreport.Report, digest, _ *string) {
			*digest = strings.Repeat("9", 64)
		},
		"report/config mismatch": func(_ *ConfigDocument, _, _ *string, report *taenetworkreport.Report, _, _ *string) {
			report.Configuration.DeploymentConfigSHA256 = canonicalDigest("another-bootstrap")
		},
	} {
		t.Run(name, func(t *testing.T) {
			document := bootstrap
			revision := revision
			policyRef := "tae-security-ticket/sg-2026-08-09"
			report := report
			digest := reportSHA256
			networkRef := "artifact://agentserver/sg-network/2026-08-09/report.json"
			mutate(&document, &revision, &policyRef, &report, &digest, &networkRef)
			if _, err := ActivateManagedExecutorDocument(document, revision, policyRef, report, digest, networkRef); err == nil {
				t.Fatal("unsafe managed activation was accepted")
			}
		})
	}
}

func validActivationNetworkReport(document ConfigDocument, revision string) taenetworkreport.Report {
	loaded, err := ValidateConfig(document)
	if err != nil {
		panic(err)
	}
	document = loaded.Document
	const connectivityAttempts = 20
	const lifecycleAttempts = 1
	checks := make([]taenetworkreport.Check, 0, 28)
	for _, name := range []string{
		"jwt_force_refresh", "control_search_missing", "control_create", "control_search_created",
		"control_wait_ready", "control_update_ttl", "data_exec_terminal", "data_exec_lark_version", "data_exec_bkectl_version",
		"data_stat_lark_cli", "data_read_lark_cli", "data_stat_lark_skill", "data_read_lark_skill",
		"data_stat_managed_skill", "data_read_managed_skill", "data_stat_bkectl_cli", "data_read_bkectl_cli",
		"data_stat_bkectl_skill", "data_read_bkectl_skill", "data_stat_bkectl_command_surface", "data_read_bkectl_command_surface",
		"data_stat_bkectl_domain_guides", "data_read_bkectl_domain_guides", "data_stat_bkectl_invocation", "data_read_bkectl_invocation",
		"control_delete",
		"control_confirm_deleted", "control_cleanup",
	} {
		attempts := lifecycleAttempts
		if name == "jwt_force_refresh" || name == "control_search_missing" {
			attempts = connectivityAttempts
		}
		check := taenetworkreport.Check{
			Name: name, Attempts: attempts, Succeeded: attempts, DurationsMillis: make([]int64, attempts),
		}
		if name == "data_read_lark_cli" {
			check.BytesRead = productionimage.ManagedLarkCLISizeBytes * lifecycleAttempts
		}
		if name == "data_read_lark_skill" {
			check.BytesRead = 1024 * lifecycleAttempts
		}
		for checkName, size := range map[string]int64{
			"data_read_managed_skill":          productionimage.ManagedSkillSizeBytes,
			"data_read_bkectl_cli":             productionimage.ManagedBkectlCLISizeBytes,
			"data_read_bkectl_skill":           productionimage.ManagedBkectlSkillSizeBytes,
			"data_read_bkectl_command_surface": productionimage.ManagedBkectlCommandSurfaceSizeBytes,
			"data_read_bkectl_domain_guides":   productionimage.ManagedBkectlDomainGuidesSizeBytes,
			"data_read_bkectl_invocation":      productionimage.ManagedBkectlInvocationSizeBytes,
		} {
			if name == checkName {
				check.BytesRead = size * lifecycleAttempts
			}
		}
		checks = append(checks, check)
	}
	started := time.Date(2026, 8, 9, 1, 2, 3, 0, time.UTC)
	taeSandboxImage, err := taeimage.ContentTagForRepository(ProductionTAEManagedSandboxImage, document.Images.ManagedSandbox)
	if err != nil {
		panic(err)
	}
	return taenetworkreport.Report{
		SchemaVersion: taenetworkreport.CurrentVersion, Kind: taenetworkreport.Kind,
		StartedAt: started, FinishedAt: started.Add(time.Minute), Passed: true, CleanupConfirmed: true,
		Source: taenetworkreport.Source{
			Namespace: document.Namespace, PodName: "agentserver-tae-network-probe-abcde",
			PodUID: "12345678-1234-4234-8234-123456789abc", NodeName: "sg-node-1", ServiceAccount: taeNetworkProbeComponent,
		},
		Configuration: taenetworkreport.Configuration{
			DeploymentConfigSHA256: canonicalDigest(document), Region: document.Managed.TAE.Region, PSM: ProductionTAEPSM,
			PolicyRevision: revision, ByteCloudSite: "i18n-tt", JWTEndpoint: ProductionByteCloudJWTEndpoint,
			ProxyURL: ProductionTAEProxyURL, ControlPlaneHost: ProductionTAEControlPlaneHost,
			DataPlaneDomainSuffix: ProductionTAEDataPlaneSuffix, SandboxImage: taeSandboxImage,
			SandboxID: document.Managed.TAE.SandboxID, SandboxRevisionID: document.Managed.TAE.RevisionID,
			LarkCLIVersion: productionimage.ManagedLarkCLIVersion, LarkCLISHA256: document.Managed.Lark.CLISHA256,
			LarkSkillSHA256:       document.Managed.Lark.SkillSHA256,
			ManagedSkillSHA256:    document.Managed.BaseInstructionsSHA256,
			BkectlSourceRevision:  document.Managed.Bkectl.SourceRevision,
			BkectlCLISHA256:       document.Managed.Bkectl.CLISHA256,
			BkectlSkillPackSHA256: document.Managed.Bkectl.SkillPackSHA256,
			ConnectivityAttempts:  connectivityAttempts, LifecycleAttempts: lifecycleAttempts,
		},
		Checks: checks,
	}
}

func validConfigDocument() ConfigDocument {
	digest := func(character string) string { return strings.Repeat(character, 64) }
	resources := func(requestCPU, requestMemory, limitCPU, limitMemory string) ContainerResourcesDocument {
		return ContainerResourcesDocument{
			Requests: ResourcePairDocument{CPU: requestCPU, Memory: requestMemory},
			Limits:   ResourcePairDocument{CPU: limitCPU, Memory: limitMemory},
		}
	}
	document := ConfigDocument{
		Version: CurrentVersion, Region: ProductionRegion, Namespace: "agentserver", ClusterDomain: "cluster.local", Platform: ProductionPlatform,
		Images: ImagesDocument{
			Service:        ProductionServiceImage + "@sha256:" + digest("1"),
			Harness:        ProductionHarnessImage + "@sha256:" + digest("2"),
			Hydra:          ProductionHydraImage + "@sha256:" + digest("3"),
			ManagedSandbox: ProductionManagedSandboxImage + "@sha256:" + digest("5"),
		},
		Replicas: ReplicasDocument{
			Core: 2, PlatformGateway: 2, BrowserGateway: 2, HarnessPool: 2, LLMProxy: 2, Hydra: 2,
			SandboxGateway: 2, EgressAuthorizer: 2,
		},
		Services: ServicesDocument{
			Core:             InternalServiceDocument{ClusterIP: "10.96.10.10", Port: HarnessControlPort},
			PlatformGateway:  InternalServiceDocument{ClusterIP: "10.96.10.15", Port: PublicHTTPPort},
			BrowserGateway:   InternalServiceDocument{ClusterIP: "10.96.10.11", Port: PublicHTTPPort},
			ExecutorGateway:  ExecutorServiceDocument{ClusterIP: "10.96.10.12", PublicPort: PublicHTTPPort, InternalPort: HarnessControlPort},
			LLMProxy:         InternalServiceDocument{ClusterIP: "10.96.10.13", Port: HarnessControlPort},
			Hydra:            HydraServiceDocument{ClusterIP: "10.96.10.14", PublicPort: HydraPublicPort, AdminPort: HydraAdminPort},
			SandboxGateway:   InternalServiceDocument{ClusterIP: "10.96.10.16", Port: HarnessControlPort},
			EgressAuthorizer: InternalServiceDocument{ClusterIP: "10.96.10.17", Port: HarnessControlPort},
		},
		Ingress: IngressDocument{
			GatewayNamespace: ProductionGatewayNamespace, GatewayName: ProductionGatewayName,
			GatewaySection:     ProductionGatewaySection,
			GatewayPodSelector: map[string]string{"gateway.networking.k8s.io/gateway-name": ProductionGatewayName},
			FrontendHostname:   ProductionFrontendHostname, BrowserFrontendHostname: ProductionBrowserFrontendHostname,
			BrowserHostname:  ProductionBrowserHostname,
			ExecutorHostname: ProductionExecutorHostname,
			HydraHostname:    ProductionHydraHostname,
		},
		Bootstrap: BootstrapDocument{
			WorkspaceID: "40000000-0000-4000-8000-000000000004",
			SessionID:   "50000000-0000-4000-8000-000000000005",
			OwnerUserID: "10000000-0000-4000-8000-000000000001",
			ExecutorID:  "20000000-0000-4000-8000-000000000002",
		},
		TrustDomain: ProductionTrustDomain,
		OAuth: OAuthDocument{
			Hydra: HydraDocument{
				Issuer: "https://auth-sg.byted.bps.dev/", AdminURL: "https://hydra.agentserver.internal:4445",
				PublicOrigin:     "https://auth-sg.byted.bps.dev",
				IntrospectionURL: "https://hydra.agentserver.internal:4445/admin/oauth2/introspect",
				PlatformClientID: "agentserver-platform", BrowserClientID: "agentserver-browser",
			},
			ExternalOIDC: ExternalOIDCDocument{
				RedirectURL: "https://auth-sg.byted.bps.dev/auth/oidc/callback",
			},
		},
		Runtime: RuntimeDocument{
			CapabilityIssuer:       "spiffe://" + ProductionTrustDomain + "/ns/" + ProductionNamespace + "/sa/agentserver-core",
			CapabilitySigningKeyID: ProductionCapabilityKeyID, ManifestSigningKeyID: ProductionManifestKeyID,
			RunPolicyVersion: "run-policy-v1",
			AllowedTools:     []string{"shell", "list_environments", "read_file"}, ExecutionPolicyVersion: "execution-policy-v1",
			ShellPolicyDecision: "ask", ReadFilePolicyDecision: "allow",
			MaxRunDuration: "30m", MaxApprovalTTL: "5m", CapabilityExpiryGrace: "30s", EnrollmentTokenTTL: "5m",
			MaxConcurrentAttempts: 4, RuntimeManifestSHA256: stockruntime.ManifestSHA256,
			CheckpointAllowlistVersion: stockruntime.CheckpointAllowlistVersion,
			FinalExecSHA256:            digest("4"), FinalExecSizeBytes: 1048576,
		},
		Managed: ManagedExecutorDocument{
			Enabled: true, Stage: ManagedExecutorStageActive,
			WorkspaceAllowlist:     []string{"40000000-0000-4000-8000-000000000004"},
			BaseInstructionsSHA256: productionimage.ManagedSkillSHA256,
			Environment: ManagedEnvironmentDocument{
				EnvironmentID: "30000000-0000-4000-8000-000000000003",
				Root: ManagedEnvironmentRootDocument{
					Path: "/workspace", DisplayName: "Managed SG", Description: "TAE managed Lark executor", DefaultCWD: "",
				},
				Compatibility: ManagedCompatibilityRuntimeDocument{
					CodexRelease: stockruntime.CodexRelease, CodexCommit: stockruntime.CodexCommit,
					CodexSHA256: stockruntime.LinuxAMD64CodexSHA256,
				},
				SandboxTTL: "30m", ActivityTTL: "5m", IdleTTL: "5m",
			},
			TAE: ManagedTAEDocument{
				Region: managedsandboxprofile.RegionI18NTT, PSM: ProductionTAEPSM,
				SandboxID: "sandbox-1", RevisionID: "revision-1",
				ControlPlaneURL:      "https://controlplane.sg.ai-sandbox-i18n.byted.org",
				DataPlaneSuffix:      "sg.ai-sandbox-i18n.byted.org",
				ByteCloudSite:        managedsandboxprofile.RegionI18NTT,
				ByteCloudJWTEndpoint: ProductionByteCloudJWTEndpoint,
				ProxyProfile:         ManagedSandboxProxyI18NTT,
				Policy: ManagedTAEPolicyDocument{
					Version: 1, Revision: "lark-readonly-v1",
					PolicySHA256: larkegresspolicy.SHA256Hex(),
					PublicHost:   "open.feishu.cn", PublicAccess: "whitelist", PublicWebhookRequired: true,
					WebhookMode: "psm", WebhookPSM: "agentserver.egress-authorizer", WebhookPath: "/v1/policy",
					Published: true, Approved: true, EvidenceRef: "tae-change/sg-2026-08-06",
				},
				NetworkEvidence: ManagedTAENetworkEvidenceDocument{
					Version: managedNetworkEvidenceVersion, ReportSHA256: canonicalDigest("sg-network-test-report"),
					EvidenceRef: "artifact://agentserver/sg-network-gates/2026-08-06/report.json",
				},
			},
			Lark: ManagedLarkDocument{
				Enabled:     true,
				CLISHA256:   productionimage.ManagedLarkCLISHA256,
				SkillSHA256: digest("8"), PolicySHA256: larkegresspolicy.SHA256Hex(),
			},
			Bkectl: ManagedBkectlDocument{
				Enabled: true, SourceRevision: bkectlpolicy.SourceRevision,
				CLISHA256: bkectlpolicy.CLISHA256, SkillPackSHA256: bkectlpolicy.SkillPackSHA256,
				PolicySHA256: bkectlpolicy.SHA256Hex(),
			},
		},
		SandboxRegions: ManagedSandboxRegionsDocument{
			DefaultRegion: managedsandboxprofile.RegionI18NTT,
			Regions:       []string{managedsandboxprofile.RegionI18NTT},
		},
		ProxyProfiles: []ManagedSandboxProxyProfileDocument{{
			Name: ManagedSandboxProxyI18NTT, URL: ProductionTAEProxyURL,
			Namespace: ProductionTAEProxyNamespace, PodSelector: map[string]string{"app": ProductionTAEProxyPodApp},
			Port: ProductionTAEProxyPort,
		}},
		Objects: ObjectStoreDocument{
			Mode: "s3-plaintext-v1", Prefix: "agentserver/v2/production",
			S3Bucket: "agentserver-production", S3Region: "sg-central",
			S3Endpoint: "https://tos-s3-sg.byted.org",
		},
		Secrets: SecretsDocument{
			Core: "agentserver-core-secrets", PlatformGateway: "agentserver-platform-secrets",
			BrowserGateway:  "agentserver-browser-secrets",
			ExecutorGateway: "agentserver-executor-secrets", HarnessPool: "agentserver-pool-secrets",
			HarnessWorker: "agentserver-worker-secrets", LLMProxy: "agentserver-llmproxy-secrets",
			ObjectStore: "agentserver-object-store-secrets", Hydra: "agentserver-hydra-secrets",
			SandboxGateway: ProductionSandboxSecret, EgressAuthorizer: ProductionEgressSecret,
		},
		Network: NetworkDocument{
			DNSClusterIP:                   "10.96.0.10",
			DNSNamespace:                   "kube-system",
			DNSPodSelector:                 map[string]string{"k8s-app": "kube-dns"},
			CoreExternalEgress:             []EgressRuleDocument{{CIDR: "10.30.0.0/24", Ports: []uint16{443}}},
			BrowserExternalEgress:          []EgressRuleDocument{},
			HarnessExternalEgress:          []EgressRuleDocument{{CIDR: "10.32.0.0/24", Ports: []uint16{443}}},
			SandboxExternalEgress:          []EgressRuleDocument{},
			EgressAuthorizerExternalEgress: []EgressRuleDocument{},
			EgressAuthorizerIngress:        []string{},
		},
		Resources: ResourcesDocument{
			Core:             resources("500m", "512Mi", "2", "2Gi"),
			PlatformGateway:  resources("250m", "256Mi", "1", "1Gi"),
			BrowserGateway:   resources("250m", "256Mi", "1", "1Gi"),
			ExecutorGateway:  resources("500m", "512Mi", "2", "2Gi"),
			HarnessPool:      resources("1", "2Gi", "4", "16Gi"),
			LLMProxy:         resources("500m", "512Mi", "2", "2Gi"),
			Hydra:            resources("100m", "128Mi", "1", "512Mi"),
			SandboxGateway:   resources("500m", "512Mi", "2", "2Gi"),
			EgressAuthorizer: resources("500m", "512Mi", "2", "2Gi"),
			RuntimeTmpfs:     "8Gi", CheckpointTmpfs: "2Gi", ScratchTmpfs: "512Mi",
		},
	}
	document.Managed.TAE.Policy.BindingSHA256 = managedTAEPolicyBinding(document.Managed.TAE).DigestHex()
	document.Managed.TAE.NetworkEvidence.BindingSHA256 = managedTAENetworkEvidenceDigest(document)
	document.Managed.Environment.RuntimeProfileSHA256 = managedRuntimeProfileDigest(document, document.Managed)
	document.Managed.Environment.PackSetSHA256 = managedPackSetDigest(document.Managed)
	profile := ManagedSandboxProfileDocument{
		Region: managedsandboxprofile.RegionI18NTT, ProfileID: "tae-i18n-tt-test-v1",
		Gateway: ManagedSandboxGatewayDocument{
			Component: sandboxComponent, ClusterIP: document.Services.SandboxGateway.ClusterIP,
			Port: document.Services.SandboxGateway.Port, ServerName: SandboxInternalHost, Secret: document.Secrets.SandboxGateway,
		},
		Environment: document.Managed.Environment, TAE: document.Managed.TAE,
		SandboxExternalEgress: append([]EgressRuleDocument{}, document.Network.SandboxExternalEgress...),
	}
	profile.BindingSHA256 = managedSandboxProfileBindingSHA256(profile, &document.ProxyProfiles[0])
	document.SandboxProfiles = []ManagedSandboxProfileDocument{profile}
	return document
}
