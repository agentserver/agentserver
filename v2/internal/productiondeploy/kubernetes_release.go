package productiondeploy

import (
	"errors"
	"net/netip"
)

// KubernetesRelease replaces deployment-owned execution infrastructure only.
// It preserves OAuth, workspace credentials, DSH and the stock Codex runtime.
// Runtime metadata must describe the unchanged bundle/final-exec in the chosen
// harness base. The release builder replaces pool/worker/init, not that bundle.
type KubernetesRelease struct {
	ServiceImage, HarnessImage, RuntimeImage, GatewayImage string
	EnvironmentID, APICIDR                                 string
	AllWorkspaces                                          bool
}

func PrepareKubernetesRelease(base LoadedConfig, release KubernetesRelease) (LoadedConfig, error) {
	d := base.Document
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
		BubblewrapProfile: true,
		RuntimeProxyURL:   kubernetesRuntimeProxyURL(d.ClusterDomain),
		AllWorkspaces:     release.AllWorkspaces,
		Namespace:         "agentserver-sandboxes", Pool: "managed-cli-v1", Scope: "sg-managed-cli",
		GatewayImage: release.GatewayImage, RuntimeTLSSecret: "agentserver-runtime-tls", RuntimeServerName: "sandbox-runtime.agentserver.internal",
		APIEgress: []EgressRuleDocument{{CIDR: api.String(), Ports: []uint16{6443}}}, RuntimeExternalEgress: []EgressRuleDocument{},
	}
	d.SandboxRegions = ManagedSandboxRegionsDocument{DefaultRegion: "sg", Regions: []string{"sg"}}
	d.ProxyProfiles = []ManagedSandboxProxyProfileDocument{}
	d.SandboxProfiles = []ManagedSandboxProfileDocument{{Region: "sg", Environment: d.Managed.Environment,
		Gateway: ManagedSandboxGatewayDocument{Component: "sandbox-gateway-k8s", ClusterIP: d.Services.SandboxGateway.ClusterIP, Port: 8443, ServerName: "sandbox-gateway-k8s.agentserver.internal", Secret: "agentserver-sandbox-k8s-secrets"}, SandboxExternalEgress: []EgressRuleDocument{}}}
	return ValidateConfig(d)
}
