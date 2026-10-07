package productiondeploy

import (
	"errors"
	"net/netip"

	"github.com/agentserver/agentserver/v2/internal/stockruntime"
)

// KubernetesRelease replaces deployment-owned execution infrastructure only.
// It preserves OAuth, workspace credentials and DSH. UpgradeCodex explicitly
// selects the current stock bundle copied into the new harness image; final-exec
// and the independently qualified Kubernetes executor images remain unchanged.
type KubernetesRelease struct {
	ServiceImage, HarnessImage, RuntimeImage, GatewayImage string
	EnvironmentID, APICIDR                                 string
	AllWorkspaces                                          bool
	UpgradeCodex                                           bool
}

// LoadKubernetesReleaseBase is release preparation only. Ordinary LoadConfig
// still rejects stale runtime metadata. An explicit upgrade may translate just
// the previously deployed stock identity before validating the complete config.
func LoadKubernetesReleaseBase(path string, upgradeCodex bool) (LoadedConfig, error) {
	raw, err := readProductionConfigFile(path)
	if err != nil {
		return LoadedConfig{}, err
	}
	d, err := decodeConfigDocument(raw)
	if err != nil {
		return LoadedConfig{}, err
	}
	if upgradeCodex {
		if !stockruntime.CanResumeCheckpoint(d.Runtime.RuntimeManifestSHA256, stockruntime.ManifestSHA256,
			int64(d.Runtime.CheckpointAllowlistVersion), stockruntime.CheckpointAllowlistVersion) {
			return LoadedConfig{}, errors.New("unsupported Codex release upgrade source")
		}
		d.Runtime.RuntimeManifestSHA256 = stockruntime.ManifestSHA256
		compatibility := ManagedCompatibilityRuntimeDocument{CodexRelease: stockruntime.CodexRelease,
			CodexCommit: stockruntime.CodexCommit, CodexSHA256: stockruntime.LinuxAMD64CodexSHA256}
		d.Managed.Environment.Compatibility = compatibility
		for i := range d.SandboxProfiles {
			d.SandboxProfiles[i].Environment.Compatibility = compatibility
		}
	}
	return ValidateConfig(d)
}

func PrepareKubernetesRelease(base LoadedConfig, release KubernetesRelease) (LoadedConfig, error) {
	d := base.Document
	if release.UpgradeCodex && d.Images.Harness == release.HarnessImage {
		return LoadedConfig{}, errors.New("Codex upgrade requires a newly published harness image containing the current bundle")
	}
	api, err := netip.ParsePrefix(release.APICIDR)
	if err != nil || !api.Addr().IsPrivate() || !api.Addr().Is4() || api.Bits() != 32 {
		return LoadedConfig{}, errors.New("Kubernetes API must be an explicit private IPv4 /32 endpoint")
	}
	if d.Managed.Provider != "k8s" {
		for _, profile := range d.SandboxProfiles {
			if profile.Environment.EnvironmentID == release.EnvironmentID {
				return LoadedConfig{}, errors.New("Kubernetes cutover requires a new environment ID; historical TAE IDs cannot be reused")
			}
		}
	}
	d.Images.Service = release.ServiceImage
	d.Images.Harness = release.HarnessImage
	d.Images.ManagedSandbox = release.RuntimeImage
	d.Managed.Provider = "k8s"
	d.Managed.Enabled = true
	d.Managed.Stage = ManagedExecutorStageActive
	d.Managed.TAE = ManagedTAEDocument{}
	d.Managed.Environment.EnvironmentID = release.EnvironmentID
	d.Managed.Environment.Root = ManagedEnvironmentRootDocument{Path: "/workspace", DefaultCWD: ".", DisplayName: "SG · Kubernetes", Description: "Session-isolated managed CLI sandbox; ephemeral workspace"}
	d.Managed.Kubernetes = &KubernetesSandboxDocument{
		BubblewrapProfile:     true,
		APIServerEntityPolicy: true,
		RuntimeProxyURL:       kubernetesRuntimeProxyURL(d.ClusterDomain),
		AllWorkspaces:         release.AllWorkspaces,
		Namespace:             "agentserver-sandboxes", Pool: "managed-cli-v1", Scope: "sg-managed-cli",
		GatewayImage: release.GatewayImage, RuntimeTLSSecret: "agentserver-runtime-tls", RuntimeServerName: "sandbox-runtime.agentserver.internal",
		APIEgress: []EgressRuleDocument{{CIDR: api.String(), Ports: []uint16{6443}}}, RuntimeExternalEgress: []EgressRuleDocument{},
	}
	d.SandboxRegions = ManagedSandboxRegionsDocument{DefaultRegion: "sg", Regions: []string{"sg"}}
	d.ProxyProfiles = []ManagedSandboxProxyProfileDocument{}
	d.SandboxProfiles = []ManagedSandboxProfileDocument{{Region: "sg", Environment: d.Managed.Environment,
		Gateway: ManagedSandboxGatewayDocument{Component: "sandbox-gateway-k8s", ClusterIP: d.Services.SandboxGateway.ClusterIP, Port: 8443, ServerName: "sandbox-gateway-k8s.agentserver.internal", Secret: "agentserver-sandbox-k8s-secrets"}, SandboxExternalEgress: []EgressRuleDocument{}}}
	return ValidateConfig(d)
}
