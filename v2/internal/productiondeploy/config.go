// Package productiondeploy validates and renders the closed-world Phase 5
// Kubernetes production deployment. It never accepts secret values directly;
// the input names pre-created Kubernetes Secrets, including the explicit
// credentials required by the selected S3-compatible object service.
package productiondeploy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/enrollmenttoken"
	"github.com/agentserver/agentserver/v2/internal/harnessworker"
	"github.com/agentserver/agentserver/v2/internal/objectstore"
	"github.com/agentserver/agentserver/v2/internal/objectstore/awsprovider"
	"github.com/agentserver/agentserver/v2/internal/stockruntime"
)

const (
	CurrentVersion                     = 3
	ProductionRegion                   = "sg"
	ProductionNamespace                = "agentserver"
	ProductionTrustDomain              = "agentserver.byted.bps.dev"
	ProductionPlatformLinuxAMD64       = "linux-amd64"
	ProductionPlatform                 = ProductionPlatformLinuxAMD64
	ProductionCapabilityKeyID          = "run-capability-sg-v1"
	ProductionManifestKeyID            = "run-manifest-sg-v1"
	ProductionCoreSecret               = "agentserver-core-secrets"
	ProductionPlatformSecret           = "agentserver-platform-secrets"
	ProductionBrowserSecret            = "agentserver-browser-secrets"
	ProductionExecutorSecret           = "agentserver-executor-secrets"
	ProductionHarnessPoolSecret        = "agentserver-pool-secrets"
	ProductionHarnessWorkerSecret      = "agentserver-worker-secrets"
	ProductionLLMProxySecret           = "agentserver-llmproxy-secrets"
	ProductionObjectStoreSecret        = "agentserver-object-store-secrets"
	ProductionHydraSecret              = "agentserver-hydra-secrets"
	ProductionSandboxSecret            = "agentserver-sandbox-secrets"
	ProductionEgressSecret             = "agentserver-egress-secrets"
	ProductionServiceImage             = "registry-sg.byted.cs.ac.cn/ghcr/agentserver/v2-service"
	ProductionHarnessImage             = "registry-sg.byted.cs.ac.cn/ghcr/agentserver/v2-harness"
	ProductionHydraImage               = "registry-sg.byted.cs.ac.cn/ghcr/agentserver/hydra"
	ProductionManagedSandboxImage      = "registry-sg.byted.cs.ac.cn/ghcr/agentserver/v2-managed-sandbox"
	ProductionSandboxBackendKeyID      = "sandbox-backend-sg-v1"
	ProductionSandboxFencerKeyID       = "sandbox-fencer-sg-v1"
	ProductionSandboxLifecycleKeyID    = "sandbox-lifecycle-sg-v1"
	ProductionEgressPlaceholderKeyID   = "egress-placeholder-sg-v1"
	ProductionEgressBackendCAConfigMap = "agentserver-egress-backend-ca"

	PoolUID    uint32 = 65530
	PoolGID    uint32 = 65530
	WorkerUID  uint32 = 65531
	WorkerGID  uint32 = 65531
	AppUID     uint32 = 65532
	AppGID     uint32 = 65532
	HydraUID   uint32 = 65532
	HydraGID   uint32 = 65532
	ServiceUID uint32 = 65534
	ServiceGID uint32 = 65534

	CoreInternalHost     = "core.agentserver.internal"
	ExecutorInternalHost = "executor.agentserver.internal"
	LLMProxyInternalHost = "llmproxy.agentserver.internal"
	HydraInternalHost    = "hydra.agentserver.internal"
	SandboxInternalHost  = "sandbox-gateway.agentserver.internal"
	EgressInternalHost   = "egress-authorizer.agentserver.internal"
	HarnessControlPort   = 8443
	PublicHTTPPort       = 8080
	HydraPublicPort      = 4444
	HydraAdminPort       = 4445

	ProductionGatewayNamespace         = "istio-ingress"
	ProductionGatewayName              = "istio-gateway"
	ProductionGatewaySection           = "https-byted-bps"
	ProductionFrontendHostname         = "agent.byted.bps.dev"
	ProductionBrowserFrontendHostname  = "browser.byted.bps.dev"
	ProductionBrowserHostname          = "browser-gateway.byted.bps.dev"
	ProductionExecutorHostname         = "executor-gateway.byted.bps.dev"
	ProductionHydraHostname            = "auth-sg.byted.bps.dev"
	ProductionEgressAuthorizerHostname = "egress-authorizer-sg.byted.bps.dev"
	ProductionEgressAuthorizerURL      = "https://" + ProductionEgressAuthorizerHostname + "/v1/policy"

	maximumConfigBytes = int64(256 * 1024)
	maximumTextBytes   = 4096
)

var (
	uuidPattern        = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	dnsLabelPattern    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	dnsNamePattern     = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$`)
	labelNamePattern   = regexp.MustCompile(`^[A-Za-z0-9](?:[-_.A-Za-z0-9]{0,61}[A-Za-z0-9])?$`)
	labelValuePattern  = regexp.MustCompile(`^(?:[A-Za-z0-9](?:[-_.A-Za-z0-9]{0,61}[A-Za-z0-9])?)?$`)
	versionPattern     = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,127}$`)
	toolPattern        = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)
	digestPattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	imagePattern       = regexp.MustCompile(`^[^[:space:]@]+@sha256:[0-9a-f]{64}$`)
	cpuQuantityPattern = regexp.MustCompile(`^(?:[1-9][0-9]*|[1-9][0-9]*m)$`)
	memQuantityPattern = regexp.MustCompile(`^[1-9][0-9]*(?:Ki|Mi|Gi|Ti)$`)
)

type ConfigDocument struct {
	Version       int                     `json:"version"`
	Region        string                  `json:"region"`
	Namespace     string                  `json:"namespace"`
	ClusterDomain string                  `json:"clusterDomain"`
	Platform      string                  `json:"platform"`
	Images        ImagesDocument          `json:"images"`
	Replicas      ReplicasDocument        `json:"replicas"`
	Services      ServicesDocument        `json:"services"`
	Ingress       IngressDocument         `json:"ingress"`
	Bootstrap     BootstrapDocument       `json:"bootstrap"`
	TrustDomain   string                  `json:"spiffeTrustDomain"`
	OAuth         OAuthDocument           `json:"oauth"`
	Runtime       RuntimeDocument         `json:"runtime"`
	Managed       ManagedExecutorDocument `json:"managedExecutor"`
	Objects       ObjectStoreDocument     `json:"objectStore"`
	Secrets       SecretsDocument         `json:"secrets"`
	Network       NetworkDocument         `json:"network"`
	Resources     ResourcesDocument       `json:"resources"`
}

type ImagesDocument struct {
	Service        string `json:"service"`
	Harness        string `json:"harness"`
	Hydra          string `json:"hydra"`
	ManagedSandbox string `json:"managedSandbox"`
}

type ReplicasDocument struct {
	Core             int `json:"core"`
	PlatformGateway  int `json:"platformGateway"`
	BrowserGateway   int `json:"browserGateway"`
	HarnessPool      int `json:"harnessPool"`
	LLMProxy         int `json:"llmproxy"`
	Hydra            int `json:"hydra"`
	SandboxGateway   int `json:"sandboxGateway"`
	EgressAuthorizer int `json:"egressAuthorizer"`
}

type ServicesDocument struct {
	Core             InternalServiceDocument `json:"core"`
	PlatformGateway  InternalServiceDocument `json:"platformGateway"`
	BrowserGateway   InternalServiceDocument `json:"browserGateway"`
	ExecutorGateway  ExecutorServiceDocument `json:"executorGateway"`
	LLMProxy         InternalServiceDocument `json:"llmproxy"`
	Hydra            HydraServiceDocument    `json:"hydra"`
	SandboxGateway   InternalServiceDocument `json:"sandboxGateway"`
	EgressAuthorizer InternalServiceDocument `json:"egressAuthorizer"`
}

type InternalServiceDocument struct {
	ClusterIP string `json:"clusterIp"`
	Port      uint16 `json:"port"`
}

type ExecutorServiceDocument struct {
	ClusterIP    string `json:"clusterIp"`
	PublicPort   uint16 `json:"publicPort"`
	InternalPort uint16 `json:"internalPort"`
}

type HydraServiceDocument struct {
	ClusterIP  string `json:"clusterIp"`
	PublicPort uint16 `json:"publicPort"`
	AdminPort  uint16 `json:"adminPort"`
}

type IngressDocument struct {
	GatewayNamespace        string            `json:"gatewayNamespace"`
	GatewayName             string            `json:"gatewayName"`
	GatewaySection          string            `json:"gatewaySection"`
	GatewayPodSelector      map[string]string `json:"gatewayPodSelector"`
	FrontendHostname        string            `json:"frontendHostname"`
	BrowserFrontendHostname string            `json:"browserFrontendHostname"`
	BrowserHostname         string            `json:"browserGatewayHostname"`
	ExecutorHostname        string            `json:"executorGatewayHostname"`
	HydraHostname           string            `json:"hydraHostname"`
}

type BootstrapDocument struct {
	WorkspaceID string `json:"workspaceId"`
	SessionID   string `json:"sessionId"`
	OwnerUserID string `json:"ownerUserId"`
	ExecutorID  string `json:"executorId"`
}

type OAuthDocument struct {
	Hydra        HydraDocument        `json:"hydra"`
	ExternalOIDC ExternalOIDCDocument `json:"externalOidc"`
}

type HydraDocument struct {
	Issuer           string `json:"issuer"`
	AdminURL         string `json:"adminUrl"`
	PublicOrigin     string `json:"publicOrigin"`
	IntrospectionURL string `json:"introspectionUrl"`
	PlatformClientID string `json:"platformClientId"`
	BrowserClientID  string `json:"browserClientId"`
}

type ExternalOIDCDocument struct {
	RedirectURL string `json:"redirectUrl"`
}

type RuntimeDocument struct {
	CapabilityIssuer           string   `json:"capabilityIssuer"`
	CapabilitySigningKeyID     string   `json:"capabilitySigningKeyId"`
	ManifestSigningKeyID       string   `json:"manifestSigningKeyId"`
	RunPolicyVersion           string   `json:"runPolicyVersion"`
	AllowedTools               []string `json:"allowedTools"`
	ExecutionPolicyVersion     string   `json:"executionPolicyVersion"`
	ShellPolicyDecision        string   `json:"shellPolicyDecision"`
	ReadFilePolicyDecision     string   `json:"readFilePolicyDecision"`
	MaxRunDuration             string   `json:"maxRunDuration"`
	MaxApprovalTTL             string   `json:"maxApprovalTtl"`
	CapabilityExpiryGrace      string   `json:"capabilityExpiryGrace"`
	EnrollmentTokenTTL         string   `json:"enrollmentTokenTtl"`
	MaxConcurrentAttempts      int      `json:"maxConcurrentAttempts"`
	RuntimeManifestSHA256      string   `json:"runtimeManifestSha256"`
	CheckpointAllowlistVersion int      `json:"checkpointAllowlistVersion"`
	FinalExecSHA256            string   `json:"finalExecSha256"`
	FinalExecSizeBytes         int64    `json:"finalExecSizeBytes"`
}

type ObjectStoreDocument struct {
	Mode           string `json:"mode"`
	Prefix         string `json:"prefix"`
	S3Bucket       string `json:"s3Bucket"`
	S3Region       string `json:"s3Region"`
	S3Endpoint     string `json:"s3Endpoint"`
	S3UsePathStyle bool   `json:"s3UsePathStyle"`
}

type SecretsDocument struct {
	Core             string `json:"core"`
	PlatformGateway  string `json:"platformGateway"`
	BrowserGateway   string `json:"browserGateway"`
	ExecutorGateway  string `json:"executorGateway"`
	HarnessPool      string `json:"harnessPool"`
	HarnessWorker    string `json:"harnessWorker"`
	LLMProxy         string `json:"llmproxy"`
	ObjectStore      string `json:"objectStore"`
	Hydra            string `json:"hydra"`
	SandboxGateway   string `json:"sandboxGateway"`
	EgressAuthorizer string `json:"egressAuthorizer"`
}

type NetworkDocument struct {
	DNSClusterIP                   string               `json:"dnsClusterIp"`
	DNSNamespace                   string               `json:"dnsNamespace"`
	DNSPodSelector                 map[string]string    `json:"dnsPodSelector"`
	CoreExternalEgress             []EgressRuleDocument `json:"coreExternalEgress"`
	BrowserExternalEgress          []EgressRuleDocument `json:"browserExternalEgress"`
	HarnessExternalEgress          []EgressRuleDocument `json:"harnessExternalEgress"`
	SandboxExternalEgress          []EgressRuleDocument `json:"sandboxExternalEgress"`
	EgressAuthorizerExternalEgress []EgressRuleDocument `json:"egressAuthorizerExternalEgress"`
	EgressAuthorizerIngress        []string             `json:"egressAuthorizerIngress"`
}

type EgressRuleDocument struct {
	CIDR  string   `json:"cidr"`
	Ports []uint16 `json:"ports"`
}

type ResourcesDocument struct {
	Core             ContainerResourcesDocument `json:"core"`
	PlatformGateway  ContainerResourcesDocument `json:"platformGateway"`
	BrowserGateway   ContainerResourcesDocument `json:"browserGateway"`
	ExecutorGateway  ContainerResourcesDocument `json:"executorGateway"`
	HarnessPool      ContainerResourcesDocument `json:"harnessPool"`
	LLMProxy         ContainerResourcesDocument `json:"llmproxy"`
	Hydra            ContainerResourcesDocument `json:"hydra"`
	SandboxGateway   ContainerResourcesDocument `json:"sandboxGateway"`
	EgressAuthorizer ContainerResourcesDocument `json:"egressAuthorizer"`
	RuntimeTmpfs     string                     `json:"runtimeTmpfs"`
	CheckpointTmpfs  string                     `json:"checkpointTmpfs"`
	ScratchTmpfs     string                     `json:"scratchTmpfs"`
}

type ContainerResourcesDocument struct {
	Requests ResourcePairDocument `json:"requests"`
	Limits   ResourcePairDocument `json:"limits"`
}

type ResourcePairDocument struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

type LoadedConfig struct {
	Document                 ConfigDocument
	MaxRunDuration           time.Duration
	MaxApprovalTTL           time.Duration
	CapabilityExpiryGrace    time.Duration
	EnrollmentTokenTTL       time.Duration
	ManagedSandboxTTL        time.Duration
	ManagedActivityTTL       time.Duration
	ManagedIdleTTL           time.Duration
	ManagedOwnerPolicySHA256 string
}

func LoadConfig(path string) (LoadedConfig, error) {
	raw, err := readProductionConfigFile(path)
	if err != nil {
		return LoadedConfig{}, err
	}
	return ParseConfig(raw)
}

func readProductionConfigFile(path string) ([]byte, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("production deployment config path must be absolute and clean")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open production deployment config: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect production deployment config: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || info.Size() < 1 || info.Size() > maximumConfigBytes {
		return nil, fmt.Errorf("production deployment config must resolve to a regular file between 1 and %d bytes not writable by group or other", maximumConfigBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read production deployment config: %w", err)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) || int64(len(raw)) != info.Size() {
		return nil, errors.New("production deployment config changed while it was being read")
	}
	return raw, nil
}

func ParseConfig(raw []byte) (LoadedConfig, error) {
	document, err := decodeConfigDocument(raw)
	if err != nil {
		return LoadedConfig{}, err
	}
	return ValidateConfig(document)
}

func decodeConfigDocument(raw []byte) (ConfigDocument, error) {
	limits := braincatalog.DefaultLimits()
	limits.MaxJSONValues = 4096
	limits.MaxJSONDepth = 20
	if _, _, err := braincatalog.DecodeCanonicalJSON(raw, int(maximumConfigBytes), limits); err != nil {
		return ConfigDocument{}, fmt.Errorf("validate production deployment JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document ConfigDocument
	if err := decoder.Decode(&document); err != nil {
		return ConfigDocument{}, fmt.Errorf("decode production deployment config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return ConfigDocument{}, errors.New("production deployment config contains more than one JSON value")
		}
		return ConfigDocument{}, fmt.Errorf("finish production deployment config: %w", err)
	}
	return document, nil
}

func ValidateConfig(document ConfigDocument) (LoadedConfig, error) {
	if document.Version != CurrentVersion {
		return LoadedConfig{}, fmt.Errorf("production deployment version must be %d", CurrentVersion)
	}
	if document.Region != ProductionRegion {
		return LoadedConfig{}, fmt.Errorf("region must be exactly %s for the current production deployment", ProductionRegion)
	}
	if document.Namespace != ProductionNamespace {
		return LoadedConfig{}, fmt.Errorf("namespace must be exactly %s for the SG production deployment", ProductionNamespace)
	}
	if !validDNSName(document.ClusterDomain) || strings.Contains(document.ClusterDomain, "..") {
		return LoadedConfig{}, errors.New("clusterDomain must be a canonical lowercase DNS name")
	}
	if document.Platform != ProductionPlatformLinuxAMD64 {
		return LoadedConfig{}, fmt.Errorf("platform must be %s for the SG production cluster", ProductionPlatformLinuxAMD64)
	}
	if !imagePattern.MatchString(document.Images.Service) || !imagePattern.MatchString(document.Images.Harness) ||
		!imagePattern.MatchString(document.Images.Hydra) || !imagePattern.MatchString(document.Images.ManagedSandbox) {
		return LoadedConfig{}, errors.New("service, harness, Hydra, and managed sandbox images must be immutable OCI references ending in @sha256:<64 lowercase hex>")
	}
	if !strings.HasPrefix(document.Images.Service, ProductionServiceImage+"@sha256:") {
		return LoadedConfig{}, fmt.Errorf("images.service must use the SG production repository %s", ProductionServiceImage)
	}
	if !strings.HasPrefix(document.Images.Harness, ProductionHarnessImage+"@sha256:") {
		return LoadedConfig{}, fmt.Errorf("images.harness must use the SG production repository %s", ProductionHarnessImage)
	}
	if !strings.HasPrefix(document.Images.Hydra, ProductionHydraImage+"@sha256:") {
		return LoadedConfig{}, fmt.Errorf("images.hydra must use the SG production repository %s", ProductionHydraImage)
	}
	if !strings.HasPrefix(document.Images.ManagedSandbox, ProductionManagedSandboxImage+"@sha256:") {
		return LoadedConfig{}, fmt.Errorf("images.managedSandbox must use the SG production repository %s", ProductionManagedSandboxImage)
	}
	imageSet := []string{document.Images.Service, document.Images.Harness, document.Images.Hydra, document.Images.ManagedSandbox}
	slices.Sort(imageSet)
	if len(slices.Compact(imageSet)) != 4 {
		return LoadedConfig{}, errors.New("service, harness, Hydra, and managed sandbox images must be independently pinned artifacts")
	}
	if err := validateReplicas(document.Replicas); err != nil {
		return LoadedConfig{}, err
	}
	if err := validateServices(document.Services); err != nil {
		return LoadedConfig{}, err
	}
	if err := validateIngress(document.Ingress); err != nil {
		return LoadedConfig{}, err
	}
	if err := validateBootstrap(document.Bootstrap); err != nil {
		return LoadedConfig{}, err
	}
	if document.TrustDomain != ProductionTrustDomain {
		return LoadedConfig{}, fmt.Errorf("spiffeTrustDomain must be exactly %s for the SG production deployment", ProductionTrustDomain)
	}
	if err := validateOAuth(document.OAuth, document.Ingress); err != nil {
		return LoadedConfig{}, err
	}
	loaded, err := validateRuntime(document.Runtime)
	if err != nil {
		return LoadedConfig{}, err
	}
	wantCapabilityIssuer := fmt.Sprintf(
		"spiffe://%s/ns/%s/sa/%s", document.TrustDomain, document.Namespace, "agentserver-core",
	)
	if document.Runtime.CapabilityIssuer != wantCapabilityIssuer {
		return LoadedConfig{}, fmt.Errorf("runtime.capabilityIssuer must be exactly %s", wantCapabilityIssuer)
	}
	if document.Runtime.CapabilitySigningKeyID != ProductionCapabilityKeyID {
		return LoadedConfig{}, fmt.Errorf("runtime.capabilitySigningKeyId must be exactly %s", ProductionCapabilityKeyID)
	}
	if document.Runtime.ManifestSigningKeyID != ProductionManifestKeyID {
		return LoadedConfig{}, fmt.Errorf("runtime.manifestSigningKeyId must be exactly %s", ProductionManifestKeyID)
	}
	loaded.Document = document
	loaded.Document.Runtime.AllowedTools = append([]string(nil), document.Runtime.AllowedTools...)
	slices.Sort(loaded.Document.Runtime.AllowedTools)
	if err := validateObjects(document.Objects); err != nil {
		return LoadedConfig{}, err
	}
	if err := validateSecrets(document.Secrets); err != nil {
		return LoadedConfig{}, err
	}
	if err := validateNetwork(&loaded.Document.Network, loaded.Document.Services); err != nil {
		return LoadedConfig{}, err
	}
	managedLoaded, err := validateManagedExecutor(loaded.Document.Managed, loaded.Document)
	if err != nil {
		return LoadedConfig{}, err
	}
	loaded.Document.Managed = managedLoaded.Document.Managed
	loaded.ManagedSandboxTTL = managedLoaded.ManagedSandboxTTL
	loaded.ManagedActivityTTL = managedLoaded.ManagedActivityTTL
	loaded.ManagedIdleTTL = managedLoaded.ManagedIdleTTL
	loaded.ManagedOwnerPolicySHA256 = managedLoaded.ManagedOwnerPolicySHA256
	if err := validateResources(document.Resources); err != nil {
		return LoadedConfig{}, err
	}
	return loaded, nil
}

func validateReplicas(document ReplicasDocument) error {
	for _, component := range []struct {
		name    string
		value   int
		minimum int
	}{
		{name: "core", value: document.Core, minimum: 2},
		{name: "platformGateway", value: document.PlatformGateway, minimum: 2},
		{name: "browserGateway", value: document.BrowserGateway, minimum: 2},
		{name: "harnessPool", value: document.HarnessPool, minimum: 1},
		{name: "llmproxy", value: document.LLMProxy, minimum: 2},
		{name: "hydra", value: document.Hydra, minimum: 2},
		{name: "sandboxGateway", value: document.SandboxGateway, minimum: 2},
		{name: "egressAuthorizer", value: document.EgressAuthorizer, minimum: 2},
	} {
		if component.value < component.minimum || component.value > 32 {
			return fmt.Errorf("replicas.%s must be between %d and 32", component.name, component.minimum)
		}
	}
	return nil
}

func validateServices(document ServicesDocument) error {
	addresses := []struct {
		name string
		ip   string
	}{
		{"core", document.Core.ClusterIP},
		{"platformGateway", document.PlatformGateway.ClusterIP},
		{"browserGateway", document.BrowserGateway.ClusterIP},
		{"executorGateway", document.ExecutorGateway.ClusterIP},
		{"llmproxy", document.LLMProxy.ClusterIP},
		{"hydra", document.Hydra.ClusterIP},
		{"sandboxGateway", document.SandboxGateway.ClusterIP},
		{"egressAuthorizer", document.EgressAuthorizer.ClusterIP},
	}
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, service := range addresses {
		address, err := netip.ParseAddr(service.ip)
		if err != nil || !address.Is4() || address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() {
			return fmt.Errorf("services.%s.clusterIp must be a usable canonical IPv4 address", service.name)
		}
		if address.String() != service.ip {
			return fmt.Errorf("services.%s.clusterIp must be canonical", service.name)
		}
		if _, duplicate := seen[address]; duplicate {
			return errors.New("service ClusterIPs must be distinct")
		}
		seen[address] = struct{}{}
	}
	for name, actual := range map[string]uint16{
		"core.port":                    document.Core.Port,
		"llmproxy.port":                document.LLMProxy.Port,
		"executorGateway.internalPort": document.ExecutorGateway.InternalPort,
		"sandboxGateway.port":          document.SandboxGateway.Port,
		"egressAuthorizer.port":        document.EgressAuthorizer.Port,
	} {
		if actual != HarnessControlPort {
			return fmt.Errorf("services.%s must be exactly %d", name, HarnessControlPort)
		}
	}
	for name, actual := range map[string]uint16{
		"platformGateway.port":       document.PlatformGateway.Port,
		"browserGateway.port":        document.BrowserGateway.Port,
		"executorGateway.publicPort": document.ExecutorGateway.PublicPort,
	} {
		if actual != PublicHTTPPort {
			return fmt.Errorf("services.%s must be exactly %d", name, PublicHTTPPort)
		}
	}
	if document.Hydra.PublicPort != HydraPublicPort {
		return fmt.Errorf("services.hydra.publicPort must be exactly %d", HydraPublicPort)
	}
	if document.Hydra.AdminPort != HydraAdminPort {
		return fmt.Errorf("services.hydra.adminPort must be exactly %d", HydraAdminPort)
	}
	return nil
}

func validateIngress(document IngressDocument) error {
	for _, field := range []struct{ name, actual, expected string }{
		{name: "gatewayNamespace", actual: document.GatewayNamespace, expected: ProductionGatewayNamespace},
		{name: "gatewayName", actual: document.GatewayName, expected: ProductionGatewayName},
		{name: "gatewaySection", actual: document.GatewaySection, expected: ProductionGatewaySection},
		{name: "frontendHostname", actual: document.FrontendHostname, expected: ProductionFrontendHostname},
		{name: "browserFrontendHostname", actual: document.BrowserFrontendHostname, expected: ProductionBrowserFrontendHostname},
		{name: "browserGatewayHostname", actual: document.BrowserHostname, expected: ProductionBrowserHostname},
		{name: "executorGatewayHostname", actual: document.ExecutorHostname, expected: ProductionExecutorHostname},
		{name: "hydraHostname", actual: document.HydraHostname, expected: ProductionHydraHostname},
	} {
		if field.actual != field.expected {
			return fmt.Errorf("ingress.%s must be exactly %s for the SG production deployment", field.name, field.expected)
		}
	}
	if len(document.GatewayPodSelector) < 1 || len(document.GatewayPodSelector) > 8 {
		return errors.New("ingress.gatewayPodSelector must contain between 1 and 8 exact pod labels")
	}
	for key, value := range document.GatewayPodSelector {
		if !validLabelKey(key) || !labelValuePattern.MatchString(value) {
			return fmt.Errorf("ingress.gatewayPodSelector contains invalid Kubernetes label %q=%q", key, value)
		}
	}
	return nil
}

func validateBootstrap(document BootstrapDocument) error {
	for name, value := range map[string]string{
		"workspaceId": document.WorkspaceID, "sessionId": document.SessionID,
		"ownerUserId": document.OwnerUserID, "executorId": document.ExecutorID,
	} {
		if !validUUID(value) {
			return fmt.Errorf("bootstrap.%s must be a non-zero canonical lowercase UUID", name)
		}
	}
	return nil
}

func validateOAuth(document OAuthDocument, ingress IngressDocument) error {
	if err := validateHTTPSURL("oauth.hydra.issuer", document.Hydra.Issuer, false); err != nil {
		return err
	}
	wantIssuer := "https://" + ingress.HydraHostname + "/"
	if document.Hydra.Issuer != wantIssuer {
		return fmt.Errorf("oauth.hydra.issuer must be exactly %s", wantIssuer)
	}
	if err := validateHTTPSOrigin("oauth.hydra.adminUrl", document.Hydra.AdminURL); err != nil {
		return err
	}
	wantAdminURL := internalOrigin(HydraInternalHost, HydraAdminPort)
	if document.Hydra.AdminURL != wantAdminURL {
		return fmt.Errorf("oauth.hydra.adminUrl must be exactly %s", wantAdminURL)
	}
	if err := validateHTTPSOrigin("oauth.hydra.publicOrigin", document.Hydra.PublicOrigin); err != nil {
		return err
	}
	if document.Hydra.PublicOrigin != "https://"+ingress.HydraHostname {
		return fmt.Errorf("oauth.hydra.publicOrigin must be exactly https://%s", ingress.HydraHostname)
	}
	if err := validateHTTPSURL("oauth.hydra.introspectionUrl", document.Hydra.IntrospectionURL, true); err != nil {
		return err
	}
	wantIntrospectionURL := wantAdminURL + "/admin/oauth2/introspect"
	if document.Hydra.IntrospectionURL != wantIntrospectionURL {
		return fmt.Errorf("oauth.hydra.introspectionUrl must be exactly %s", wantIntrospectionURL)
	}
	if err := validateText("oauth.hydra.platformClientId", document.Hydra.PlatformClientID, 1, 256); err != nil {
		return err
	}
	if document.Hydra.PlatformClientID != corecontract.PlatformOAuthClientID {
		return fmt.Errorf("oauth.hydra.platformClientId must be exactly %s", corecontract.PlatformOAuthClientID)
	}
	if err := validateText("oauth.hydra.browserClientId", document.Hydra.BrowserClientID, 1, 256); err != nil {
		return err
	}
	if document.Hydra.BrowserClientID != corecontract.BrowserOAuthClientID {
		return fmt.Errorf("oauth.hydra.browserClientId must be exactly %s", corecontract.BrowserOAuthClientID)
	}
	wantRedirect := "https://" + ingress.HydraHostname + "/auth/oidc/callback"
	if document.ExternalOIDC.RedirectURL != wantRedirect {
		return fmt.Errorf("oauth.externalOidc.redirectUrl must be exactly %s", wantRedirect)
	}
	return nil
}

func validateRuntime(document RuntimeDocument) (LoadedConfig, error) {
	for name, value := range map[string]string{
		"capabilityIssuer":       document.CapabilityIssuer,
		"capabilitySigningKeyId": document.CapabilitySigningKeyID,
		"manifestSigningKeyId":   document.ManifestSigningKeyID,
	} {
		if err := validateText("runtime."+name, value, 1, 256); err != nil {
			return LoadedConfig{}, err
		}
	}
	if document.CapabilitySigningKeyID == document.ManifestSigningKeyID {
		return LoadedConfig{}, errors.New("capability and run-manifest signing key IDs must be distinct")
	}
	for name, value := range map[string]string{
		"runPolicyVersion":       document.RunPolicyVersion,
		"executionPolicyVersion": document.ExecutionPolicyVersion,
	} {
		if !versionPattern.MatchString(value) {
			return LoadedConfig{}, fmt.Errorf("runtime.%s must be canonical version text", name)
		}
	}
	if len(document.AllowedTools) < 1 || len(document.AllowedTools) > 3 {
		return LoadedConfig{}, errors.New("runtime.allowedTools must contain between one and three implemented executor tools")
	}
	known := map[string]struct{}{"list_environments": {}, "read_file": {}, "shell": {}}
	seen := make(map[string]struct{}, len(document.AllowedTools))
	for _, tool := range document.AllowedTools {
		if !toolPattern.MatchString(tool) {
			return LoadedConfig{}, fmt.Errorf("runtime.allowedTools contains invalid tool %q", tool)
		}
		if _, found := known[tool]; !found {
			return LoadedConfig{}, fmt.Errorf("runtime.allowedTools contains unsupported tool %q", tool)
		}
		if _, duplicate := seen[tool]; duplicate {
			return LoadedConfig{}, fmt.Errorf("runtime.allowedTools repeats %q", tool)
		}
		seen[tool] = struct{}{}
	}
	if _, found := seen["list_environments"]; !found {
		return LoadedConfig{}, errors.New("runtime.allowedTools must include list_environments")
	}
	for name, decision := range map[string]string{
		"shellPolicyDecision":    document.ShellPolicyDecision,
		"readFilePolicyDecision": document.ReadFilePolicyDecision,
	} {
		if decision != "allow" && decision != "ask" && decision != "deny" {
			return LoadedConfig{}, fmt.Errorf("runtime.%s must be allow, ask, or deny", name)
		}
	}
	maxRun, err := parseDuration("runtime.maxRunDuration", document.MaxRunDuration, time.Second, 24*time.Hour)
	if err != nil {
		return LoadedConfig{}, err
	}
	maxApproval, err := parseDuration("runtime.maxApprovalTtl", document.MaxApprovalTTL, time.Second, 24*time.Hour)
	if err != nil {
		return LoadedConfig{}, err
	}
	if maxApproval > maxRun {
		return LoadedConfig{}, errors.New("runtime.maxApprovalTtl must not exceed maxRunDuration")
	}
	grace, err := parseDuration("runtime.capabilityExpiryGrace", document.CapabilityExpiryGrace, time.Second, 10*time.Minute)
	if err != nil {
		return LoadedConfig{}, err
	}
	enrollment, err := parseDuration("runtime.enrollmentTokenTtl", document.EnrollmentTokenTTL, time.Second, enrollmenttoken.MaximumTTL)
	if err != nil || enrollment%time.Millisecond != 0 {
		return LoadedConfig{}, fmt.Errorf(
			"runtime.enrollmentTokenTtl must be a whole-millisecond duration between 1s and %s",
			enrollmenttoken.MaximumTTL,
		)
	}
	if document.MaxConcurrentAttempts < 1 || document.MaxConcurrentAttempts > 64 {
		return LoadedConfig{}, errors.New("runtime.maxConcurrentAttempts must be between 1 and 64")
	}
	if document.RuntimeManifestSHA256 != stockruntime.ManifestSHA256 {
		return LoadedConfig{}, fmt.Errorf("runtime.runtimeManifestSha256 must select the reviewed stock runtime %s", stockruntime.ManifestSHA256)
	}
	if !digestPattern.MatchString(document.FinalExecSHA256) {
		return LoadedConfig{}, errors.New("runtime.finalExecSha256 must be 64 lowercase hexadecimal characters")
	}
	if document.CheckpointAllowlistVersion != stockruntime.CheckpointAllowlistVersion {
		return LoadedConfig{}, fmt.Errorf("runtime.checkpointAllowlistVersion must be %d for the reviewed stock runtime", stockruntime.CheckpointAllowlistVersion)
	}
	if document.FinalExecSizeBytes < 1 || document.FinalExecSizeBytes > 1<<30 {
		return LoadedConfig{}, errors.New("runtime.finalExecSizeBytes must be between 1 and 1073741824")
	}
	return LoadedConfig{
		MaxRunDuration: maxRun, MaxApprovalTTL: maxApproval,
		CapabilityExpiryGrace: grace, EnrollmentTokenTTL: enrollment,
	}, nil
}

func validateObjects(document ObjectStoreDocument) error {
	if document.Mode != "s3-plaintext-v1" {
		return errors.New("objectStore.mode must be exactly s3-plaintext-v1")
	}
	for name, value := range map[string]string{
		"prefix": document.Prefix, "s3Bucket": document.S3Bucket, "s3Region": document.S3Region,
	} {
		if err := validateText("objectStore."+name, value, 1, maximumTextBytes); err != nil {
			return err
		}
	}
	if err := objectstore.ValidatePrefix(document.Prefix); err != nil {
		return fmt.Errorf("objectStore.prefix: %w", err)
	}
	if err := validateHTTPSOrigin("objectStore.s3Endpoint", document.S3Endpoint); err != nil {
		return err
	}
	if err := awsprovider.ValidateS3Config(awsprovider.S3Config{
		Bucket: document.S3Bucket, Region: document.S3Region,
		Endpoint: document.S3Endpoint, UsePathStyle: document.S3UsePathStyle,
	}); err != nil {
		return fmt.Errorf("objectStore provider routing: %w", err)
	}
	return nil
}

func validateSecrets(document SecretsDocument) error {
	for name, pair := range map[string][2]string{
		"core":             {document.Core, ProductionCoreSecret},
		"platformGateway":  {document.PlatformGateway, ProductionPlatformSecret},
		"browserGateway":   {document.BrowserGateway, ProductionBrowserSecret},
		"executorGateway":  {document.ExecutorGateway, ProductionExecutorSecret},
		"harnessPool":      {document.HarnessPool, ProductionHarnessPoolSecret},
		"harnessWorker":    {document.HarnessWorker, ProductionHarnessWorkerSecret},
		"llmproxy":         {document.LLMProxy, ProductionLLMProxySecret},
		"objectStore":      {document.ObjectStore, ProductionObjectStoreSecret},
		"hydra":            {document.Hydra, ProductionHydraSecret},
		"sandboxGateway":   {document.SandboxGateway, ProductionSandboxSecret},
		"egressAuthorizer": {document.EgressAuthorizer, ProductionEgressSecret},
	} {
		if pair[0] != pair[1] {
			return fmt.Errorf("secrets.%s must be exactly %s for the SG production deployment", name, pair[1])
		}
	}
	return nil
}

func validateNetwork(document *NetworkDocument, services ServicesDocument) error {
	if document == nil {
		return errors.New("network configuration is required")
	}
	dns, err := netip.ParseAddr(document.DNSClusterIP)
	if err != nil || !dns.Is4() || dns.IsUnspecified() || dns.IsLoopback() || dns.IsMulticast() || dns.String() != document.DNSClusterIP {
		return errors.New("network.dnsClusterIp must be a usable canonical IPv4 address")
	}
	for name, clusterIP := range map[string]string{
		"core": services.Core.ClusterIP, "platformGateway": services.PlatformGateway.ClusterIP,
		"browserGateway":  services.BrowserGateway.ClusterIP,
		"executorGateway": services.ExecutorGateway.ClusterIP, "llmproxy": services.LLMProxy.ClusterIP,
		"hydra":            services.Hydra.ClusterIP,
		"sandboxGateway":   services.SandboxGateway.ClusterIP,
		"egressAuthorizer": services.EgressAuthorizer.ClusterIP,
	} {
		if clusterIP == document.DNSClusterIP {
			return fmt.Errorf("network.dnsClusterIp must differ from services.%s.clusterIp", name)
		}
	}
	if !dnsLabelPattern.MatchString(document.DNSNamespace) {
		return errors.New("network.dnsNamespace must be a canonical Kubernetes namespace DNS label")
	}
	if len(document.DNSPodSelector) < 1 || len(document.DNSPodSelector) > 8 {
		return errors.New("network.dnsPodSelector must contain between 1 and 8 exact pod labels")
	}
	for key, value := range document.DNSPodSelector {
		if !validLabelKey(key) || !labelValuePattern.MatchString(value) {
			return fmt.Errorf("network.dnsPodSelector contains invalid Kubernetes label %q=%q", key, value)
		}
	}
	groups := []struct {
		name     string
		rules    *[]EgressRuleDocument
		required bool
	}{
		{"coreExternalEgress", &document.CoreExternalEgress, true},
		{"browserExternalEgress", &document.BrowserExternalEgress, false},
		{"harnessExternalEgress", &document.HarnessExternalEgress, true},
		{"sandboxExternalEgress", &document.SandboxExternalEgress, false},
		{"egressAuthorizerExternalEgress", &document.EgressAuthorizerExternalEgress, false},
	}
	for _, group := range groups {
		if group.required && len(*group.rules) == 0 {
			return fmt.Errorf("network.%s must contain at least one explicit rule", group.name)
		}
		if err := normalizeEgressRules("network."+group.name, group.rules); err != nil {
			return err
		}
	}
	for name, rules := range map[string][]EgressRuleDocument{
		"network.sandboxExternalEgress":          document.SandboxExternalEgress,
		"network.egressAuthorizerExternalEgress": document.EgressAuthorizerExternalEgress,
	} {
		if len(rules) > 0 {
			if err := requireDualStackEgress(name, rules); err != nil {
				return err
			}
		}
	}
	if err := validateIngressPrefixes("network.egressAuthorizerIngress", &document.EgressAuthorizerIngress); err != nil {
		return err
	}
	return nil
}

func normalizeEgressRules(name string, rules *[]EgressRuleDocument) error {
	if rules == nil || len(*rules) > 128 {
		return fmt.Errorf("%s exceeds 128 rules", name)
	}
	seen := make(map[string]struct{}, len(*rules))
	for index := range *rules {
		rule := &(*rules)[index]
		prefix, err := netip.ParsePrefix(rule.CIDR)
		if err != nil || prefix.Masked().String() != rule.CIDR ||
			(prefix.Addr().Is4() && prefix.Bits() < 8) ||
			(prefix.Addr().Is6() && (prefix.Addr().Is4In6() || prefix.Bits() < 32)) ||
			(!prefix.Addr().Is4() && !prefix.Addr().Is6()) {
			return fmt.Errorf("%s[%d].cidr must be a canonical IP CIDR no broader than IPv4 /8 or IPv6 /32", name, index)
		}
		if len(rule.Ports) == 0 || len(rule.Ports) > 32 {
			return fmt.Errorf("%s[%d].ports must contain between 1 and 32 ports", name, index)
		}
		slices.Sort(rule.Ports)
		rule.Ports = slices.Compact(rule.Ports)
		key := rule.CIDR + ":"
		for _, port := range rule.Ports {
			if port == 0 {
				return fmt.Errorf("%s[%d].ports contains zero", name, index)
			}
			key += fmt.Sprintf("%d,", port)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%s repeats rule %s", name, key)
		}
		seen[key] = struct{}{}
	}
	slices.SortFunc(*rules, func(left, right EgressRuleDocument) int {
		if left.CIDR < right.CIDR {
			return -1
		}
		if left.CIDR > right.CIDR {
			return 1
		}
		return slices.Compare(left.Ports, right.Ports)
	})
	return nil
}

func validateResources(document ResourcesDocument) error {
	components := []struct {
		name     string
		resource ContainerResourcesDocument
	}{
		{name: "core", resource: document.Core},
		{name: "platformGateway", resource: document.PlatformGateway},
		{name: "browserGateway", resource: document.BrowserGateway},
		{name: "executorGateway", resource: document.ExecutorGateway},
		{name: "harnessPool", resource: document.HarnessPool},
		{name: "llmproxy", resource: document.LLMProxy},
		{name: "hydra", resource: document.Hydra},
		{name: "sandboxGateway", resource: document.SandboxGateway},
		{name: "egressAuthorizer", resource: document.EgressAuthorizer},
	}
	for _, component := range components {
		name, resource := component.name, component.resource
		if err := validateResourcePair("resources."+name+".requests", resource.Requests); err != nil {
			return err
		}
		if err := validateResourcePair("resources."+name+".limits", resource.Limits); err != nil {
			return err
		}
		requestCPU, _ := parseCPUQuantity(resource.Requests.CPU)
		limitCPU, _ := parseCPUQuantity(resource.Limits.CPU)
		requestMemory, _ := parseMemoryQuantity(resource.Requests.Memory)
		limitMemory, _ := parseMemoryQuantity(resource.Limits.Memory)
		if requestCPU > limitCPU {
			return fmt.Errorf("resources.%s.requests.cpu must not exceed limits.cpu", name)
		}
		if requestMemory > limitMemory {
			return fmt.Errorf("resources.%s.requests.memory must not exceed limits.memory", name)
		}
	}
	for name, value := range map[string]string{
		"runtimeTmpfs":    document.RuntimeTmpfs,
		"checkpointTmpfs": document.CheckpointTmpfs,
		"scratchTmpfs":    document.ScratchTmpfs,
	} {
		if !memQuantityPattern.MatchString(value) {
			return fmt.Errorf("resources.%s must be a positive binary-memory Kubernetes quantity", name)
		}
		if _, err := parseMemoryQuantity(value); err != nil {
			return fmt.Errorf("resources.%s must fit in an unsigned 64-bit byte count", name)
		}
	}
	runtimeTmpfs, _ := parseMemoryQuantity(document.RuntimeTmpfs)
	checkpointTmpfs, _ := parseMemoryQuantity(document.CheckpointTmpfs)
	scratchTmpfs, _ := parseMemoryQuantity(document.ScratchTmpfs)
	harnessLimit, _ := parseMemoryQuantity(document.HarnessPool.Limits.Memory)
	if runtimeTmpfs > ^uint64(0)-checkpointTmpfs || runtimeTmpfs+checkpointTmpfs > ^uint64(0)-scratchTmpfs ||
		runtimeTmpfs+checkpointTmpfs+scratchTmpfs > harnessLimit {
		return errors.New("resources runtimeTmpfs + checkpointTmpfs + scratchTmpfs must not exceed harnessPool.limits.memory")
	}
	for _, component := range components {
		limitMemory, _ := parseMemoryQuantity(component.resource.Limits.Memory)
		if component.name != "harnessPool" && scratchTmpfs > limitMemory {
			return fmt.Errorf("resources.scratchTmpfs must not exceed %s.limits.memory", component.name)
		}
	}
	return nil
}

func validateResourcePair(name string, pair ResourcePairDocument) error {
	if !cpuQuantityPattern.MatchString(pair.CPU) {
		return fmt.Errorf("%s.cpu must be a positive whole-core or millicore quantity", name)
	}
	if _, err := parseCPUQuantity(pair.CPU); err != nil {
		return fmt.Errorf("%s.cpu is too large", name)
	}
	if !memQuantityPattern.MatchString(pair.Memory) {
		return fmt.Errorf("%s.memory must be a positive binary-memory quantity", name)
	}
	if _, err := parseMemoryQuantity(pair.Memory); err != nil {
		return fmt.Errorf("%s.memory is too large", name)
	}
	return nil
}

func parseCPUQuantity(raw string) (uint64, error) {
	if strings.HasSuffix(raw, "m") {
		return strconv.ParseUint(strings.TrimSuffix(raw, "m"), 10, 64)
	}
	whole, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || whole > ^uint64(0)/1000 {
		return 0, errors.New("CPU quantity overflows millicores")
	}
	return whole * 1000, nil
}

func parseMemoryQuantity(raw string) (uint64, error) {
	if len(raw) < 3 {
		return 0, errors.New("memory quantity is malformed")
	}
	factors := map[string]uint64{"Ki": 1 << 10, "Mi": 1 << 20, "Gi": 1 << 30, "Ti": 1 << 40}
	factor, found := factors[raw[len(raw)-2:]]
	if !found {
		return 0, errors.New("memory quantity has an unsupported suffix")
	}
	amount, err := strconv.ParseUint(raw[:len(raw)-2], 10, 64)
	if err != nil || amount > ^uint64(0)/factor {
		return 0, errors.New("memory quantity overflows bytes")
	}
	return amount * factor, nil
}

func validateHTTPSOrigin(name, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" || parsed.Opaque != "" || parsed.ForceQuery ||
		(parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("%s must be an HTTPS origin without credentials, path, query, or fragment", name)
	}
	return nil
}

func validateHTTPSURL(name, raw string, requirePath bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" || parsed.Opaque != "" || parsed.ForceQuery ||
		(requirePath && (parsed.Path == "" || parsed.Path == "/")) {
		return fmt.Errorf("%s must be a credential-free absolute HTTPS URL", name)
	}
	return nil
}

func parseDuration(name, raw string, minimum, maximum time.Duration) (time.Duration, error) {
	value, err := time.ParseDuration(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be a Go duration between %s and %s", name, minimum, maximum)
	}
	return value, nil
}

func validateText(name, value string, minimum, maximum int) error {
	if len(value) < minimum || len(value) > maximum || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must contain between %d and %d bytes of canonical UTF-8 text", name, minimum, maximum)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s must not contain control characters", name)
		}
	}
	return nil
}

func validUUID(value string) bool {
	return value != "00000000-0000-0000-0000-000000000000" && uuidPattern.MatchString(value)
}

func validDNSName(value string) bool {
	if len(value) > 253 || !dnsNamePattern.MatchString(value) || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if !dnsLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func validLabelKey(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) == 1 {
		return labelNamePattern.MatchString(parts[0])
	}
	return len(parts) == 2 && validDNSName(parts[0]) && labelNamePattern.MatchString(parts[1])
}

func BrowserOAuthAudience() string { return corecontract.BrowserOAuthAudience }

func BrowserOAuthScopes() []string { return corecontract.BrowserOAuthScopes() }

func PlatformOAuthAudience() string { return corecontract.PlatformOAuthAudience }

func PlatformOAuthScopes() []string { return corecontract.PlatformOAuthScopes() }

func CodexConfigProfile() string { return harnessworker.CodexConfigProfileStable0146 }
