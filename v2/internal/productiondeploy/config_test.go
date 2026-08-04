package productiondeploy

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/enrollmenttoken"
	"github.com/agentserver/agentserver/v2/internal/stockruntime"
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
		Version: 1, Region: ProductionRegion, Namespace: "agentserver", ClusterDomain: "cluster.local", Platform: ProductionPlatform,
		Images: ImagesDocument{
			Service: ProductionServiceImage + "@sha256:" + digest("1"),
			Harness: ProductionHarnessImage + "@sha256:" + digest("2"),
			Hydra:   ProductionHydraImage + "@sha256:" + digest("3"),
		},
		Replicas: ReplicasDocument{Core: 2, PlatformGateway: 2, BrowserGateway: 2, HarnessPool: 2, LLMProxy: 2, Hydra: 2},
		Services: ServicesDocument{
			Core:            InternalServiceDocument{ClusterIP: "10.96.10.10", Port: HarnessControlPort},
			PlatformGateway: InternalServiceDocument{ClusterIP: "10.96.10.15", Port: PublicHTTPPort},
			BrowserGateway:  InternalServiceDocument{ClusterIP: "10.96.10.11", Port: PublicHTTPPort},
			ExecutorGateway: ExecutorServiceDocument{ClusterIP: "10.96.10.12", PublicPort: PublicHTTPPort, InternalPort: HarnessControlPort},
			LLMProxy:        InternalServiceDocument{ClusterIP: "10.96.10.13", Port: HarnessControlPort},
			Hydra:           HydraServiceDocument{ClusterIP: "10.96.10.14", PublicPort: HydraPublicPort, AdminPort: HydraAdminPort},
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
			WorkspaceID:         "40000000-0000-4000-8000-000000000004",
			SessionID:           "50000000-0000-4000-8000-000000000005",
			OwnerUserID:         "10000000-0000-4000-8000-000000000001",
			ExternalOIDCSubject: "production-owner",
			ExecutorID:          "20000000-0000-4000-8000-000000000002",
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
				Issuer: "https://idp.example.test/oidc", ClientID: "agentserver-production",
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
		},
		Network: NetworkDocument{
			DNSClusterIP:          "10.96.0.10",
			DNSNamespace:          "kube-system",
			DNSPodSelector:        map[string]string{"k8s-app": "kube-dns"},
			CoreExternalEgress:    []EgressRuleDocument{{CIDR: "10.30.0.0/24", Ports: []uint16{443}}},
			BrowserExternalEgress: []EgressRuleDocument{},
			HarnessExternalEgress: []EgressRuleDocument{{CIDR: "10.32.0.0/24", Ports: []uint16{443}}},
		},
		Resources: ResourcesDocument{
			Core:            resources("500m", "512Mi", "2", "2Gi"),
			PlatformGateway: resources("250m", "256Mi", "1", "1Gi"),
			BrowserGateway:  resources("250m", "256Mi", "1", "1Gi"),
			ExecutorGateway: resources("500m", "512Mi", "2", "2Gi"),
			HarnessPool:     resources("1", "2Gi", "4", "16Gi"),
			LLMProxy:        resources("500m", "512Mi", "2", "2Gi"),
			Hydra:           resources("100m", "128Mi", "1", "512Mi"),
			RuntimeTmpfs:    "8Gi", CheckpointTmpfs: "2Gi", ScratchTmpfs: "512Mi",
		},
	}
}
