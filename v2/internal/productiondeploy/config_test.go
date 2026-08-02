package productiondeploy

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/enrollmenttoken"
	"github.com/agentserver/agentserver/v2/internal/stockruntime"
)

func TestValidateConfigAcceptsClosedLinuxARM64Deployment(t *testing.T) {
	loaded, err := ValidateConfig(validConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Document.Platform != ProductionPlatform || loaded.MaxRunDuration.String() != "30m0s" ||
		strings.Join(loaded.Document.Runtime.AllowedTools, ",") != "list_environments,read_file,shell" {
		t.Fatalf("loaded config = %+v", loaded)
	}
}

func TestValidateConfigAllowsPublicOrNodeCredentialedImages(t *testing.T) {
	document := validConfigDocument()
	document.Images.PullSecret = ""
	if _, err := ValidateConfig(document); err != nil {
		t.Fatal(err)
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
		"platform":                  func(value *ConfigDocument) { value.Platform = "linux-amd64" },
		"mutable image":             func(value *ConfigDocument) { value.Images.Service = "registry.example.test/agentserver:latest" },
		"invalid pull secret":       func(value *ConfigDocument) { value.Images.PullSecret = "Not_Canonical" },
		"pull secret collision":     func(value *ConfigDocument) { value.Images.PullSecret = value.Secrets.Core },
		"gateway replicas implied":  func(value *ConfigDocument) { value.Services.ExecutorGateway.Port = 9443 },
		"shared role":               func(value *ConfigDocument) { value.Objects.HarnessPoolRoleARN = value.Objects.CoreRoleARN },
		"invalid object prefix":     func(value *ConfigDocument) { value.Objects.Prefix = "agentserver/../production" },
		"invalid provider region":   func(value *ConfigDocument) { value.Objects.S3Region = "not a region" },
		"open egress":               func(value *ConfigDocument) { value.Network.CoreExternalEgress[0].CIDR = "0.0.0.0/0" },
		"missing DNS selector":      func(value *ConfigDocument) { value.Network.DNSPodSelector = nil },
		"DNS service collision":     func(value *ConfigDocument) { value.Network.DNSClusterIP = value.Services.Core.ClusterIP },
		"oversize DNS label":        func(value *ConfigDocument) { value.ClusterDomain = strings.Repeat("a", 64) + ".test" },
		"low core availability":     func(value *ConfigDocument) { value.Replicas.Core = 1 },
		"low browser availability":  func(value *ConfigDocument) { value.Replicas.BrowserGateway = 1 },
		"low llmproxy availability": func(value *ConfigDocument) { value.Replicas.LLMProxy = 1 },
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
		"runtime allowlist drift":     func(value *ConfigDocument) { value.Runtime.CheckpointAllowlistVersion++ },
		"shared secret":               func(value *ConfigDocument) { value.Secrets.HarnessWorker = value.Secrets.HarnessPool },
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

func validConfigDocument() ConfigDocument {
	digest := func(character string) string { return strings.Repeat(character, 64) }
	resources := func(requestCPU, requestMemory, limitCPU, limitMemory string) ContainerResourcesDocument {
		return ContainerResourcesDocument{
			Requests: ResourcePairDocument{CPU: requestCPU, Memory: requestMemory},
			Limits:   ResourcePairDocument{CPU: limitCPU, Memory: limitMemory},
		}
	}
	return ConfigDocument{
		Version: 1, Namespace: "agentserver", ClusterDomain: "cluster.local", Platform: ProductionPlatform,
		Images: ImagesDocument{
			Service:    "registry.example.test/agentserver/service@sha256:" + digest("1"),
			Harness:    "registry.example.test/agentserver/harness@sha256:" + digest("2"),
			PullSecret: "agentserver-registry-pull",
		},
		Replicas: ReplicasDocument{Core: 2, BrowserGateway: 2, HarnessPool: 2, LLMProxy: 2},
		Services: ServicesDocument{
			Core:            InternalServiceDocument{ClusterIP: "10.96.10.10", Port: HarnessControlPort},
			BrowserGateway:  PublicServiceDocument{ClusterIP: "10.96.10.11", Port: HarnessControlPort, PublicHostname: "agent.example.test"},
			ExecutorGateway: PublicServiceDocument{ClusterIP: "10.96.10.12", Port: HarnessControlPort, PublicHostname: "executor.example.test"},
			LLMProxy:        InternalServiceDocument{ClusterIP: "10.96.10.13", Port: HarnessControlPort},
		},
		Bootstrap: BootstrapDocument{
			WorkspaceID:         "40000000-0000-4000-8000-000000000004",
			SessionID:           "50000000-0000-4000-8000-000000000005",
			OwnerUserID:         "10000000-0000-4000-8000-000000000001",
			ExternalOIDCSubject: "production-owner",
			ExecutorID:          "20000000-0000-4000-8000-000000000002",
		},
		TrustDomain: "agentserver.example.test",
		OAuth: OAuthDocument{
			Hydra: HydraDocument{
				Issuer: "https://auth.example.test", AdminURL: "https://hydra-admin.example.test",
				PublicOrigin: "https://auth.example.test", IntrospectionURL: "https://hydra-admin.example.test/admin/oauth2/introspect",
				BrowserClientID: "agentserver-browser",
			},
			ExternalOIDC: ExternalOIDCDocument{
				Issuer: "https://idp.example.test/oidc", ClientID: "agentserver-production",
				RedirectURL: "https://agent.example.test/auth/oidc/callback",
			},
		},
		Runtime: RuntimeDocument{
			CapabilityIssuer:       "spiffe://agentserver.example.test/ns/agentserver/sa/agentserver-core",
			CapabilitySigningKeyID: "run-capability-2026-08", ManifestSigningKeyID: "run-manifest-2026-08",
			Model: "gpt-5", Provider: "openai", UpstreamResponsesURL: "https://api.openai.com/v1/responses",
			UpstreamAuthHeader: "Authorization", RunPolicyVersion: "run-policy-v1",
			AllowedTools: []string{"shell", "list_environments", "read_file"}, ExecutionPolicyVersion: "execution-policy-v1",
			ShellPolicyDecision: "ask", ReadFilePolicyDecision: "allow",
			MaxRunDuration: "30m", MaxApprovalTTL: "5m", CapabilityExpiryGrace: "30s", EnrollmentTokenTTL: "5m",
			MaxConcurrentAttempts: 4, RuntimeManifestSHA256: stockruntime.ManifestSHA256,
			CheckpointAllowlistVersion: stockruntime.CheckpointAllowlistVersion,
			FinalExecSHA256:            digest("4"), FinalExecSizeBytes: 1048576,
		},
		Objects: ObjectStoreDocument{
			Prefix: "agentserver/v2/production", S3Bucket: "agentserver-production", S3Region: "ap-southeast-1",
			KMSRegion: "ap-southeast-1", KMSKeyID: "arn:aws:kms:ap-southeast-1:123456789012:key/11111111-1111-4111-8111-111111111111",
			CoreRoleARN:        "arn:aws:iam::123456789012:role/agentserver-core",
			HarnessPoolRoleARN: "arn:aws:iam::123456789012:role/agentserver-harness-pool",
		},
		Secrets: SecretsDocument{
			Core: "agentserver-core-secrets", BrowserGateway: "agentserver-browser-secrets",
			ExecutorGateway: "agentserver-executor-secrets", HarnessPool: "agentserver-pool-secrets",
			HarnessWorker: "agentserver-worker-secrets", LLMProxy: "agentserver-llmproxy-secrets",
		},
		Network: NetworkDocument{
			DNSClusterIP:           "10.96.0.10",
			DNSNamespace:           "kube-system",
			DNSPodSelector:         map[string]string{"k8s-app": "kube-dns"},
			DatabaseEgress:         []EgressRuleDocument{{CIDR: "10.20.0.10/32", Ports: []uint16{5432}}},
			CoreExternalEgress:     []EgressRuleDocument{{CIDR: "10.30.0.0/24", Ports: []uint16{443}}},
			BrowserExternalEgress:  []EgressRuleDocument{{CIDR: "10.31.0.0/24", Ports: []uint16{443}}},
			HarnessExternalEgress:  []EgressRuleDocument{{CIDR: "10.32.0.0/24", Ports: []uint16{443}}},
			LLMProxyExternalEgress: []EgressRuleDocument{{CIDR: "10.33.0.0/24", Ports: []uint16{443}}},
			BrowserIngressCIDRs:    []string{"0.0.0.0/0"}, ExecutorIngressCIDRs: []string{"203.0.113.0/24"},
		},
		Resources: ResourcesDocument{
			Core:            resources("500m", "512Mi", "2", "2Gi"),
			BrowserGateway:  resources("250m", "256Mi", "1", "1Gi"),
			ExecutorGateway: resources("500m", "512Mi", "2", "2Gi"),
			HarnessPool:     resources("1", "2Gi", "4", "16Gi"),
			LLMProxy:        resources("500m", "512Mi", "2", "2Gi"),
			RuntimeTmpfs:    "8Gi", CheckpointTmpfs: "2Gi", ScratchTmpfs: "512Mi",
		},
	}
}
