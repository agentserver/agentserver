package productiondeploy

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/kubernetesresources"
	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
)

type KubernetesSandboxDocument struct {
	AllWorkspaces         bool                 `json:"allWorkspaces,omitempty"`
	Namespace             string               `json:"namespace"`
	Pool                  string               `json:"pool"`
	Scope                 string               `json:"scope"`
	GatewayImage          string               `json:"gatewayImage"`
	RuntimeTLSSecret      string               `json:"runtimeTlsSecret"`
	RuntimeServerName     string               `json:"runtimeServerName"`
	RuntimeClassName      string               `json:"runtimeClassName,omitempty"`
	BubblewrapProfile     bool                 `json:"bubblewrapProfile,omitempty"`
	RuntimeProxyURL       string               `json:"runtimeProxyUrl,omitempty"`
	APIServerEntityPolicy bool                 `json:"apiServerEntityPolicy,omitempty"`
	APIEgress             []EgressRuleDocument `json:"apiEgress"`
	RuntimeExternalEgress []EgressRuleDocument `json:"runtimeExternalEgress"`
}

func validateKubernetesManagedExecutor(m ManagedExecutorDocument, d ConfigDocument) (LoadedConfig, error) {
	if m.Kubernetes == nil || m.TAE != (ManagedTAEDocument{}) {
		return LoadedConfig{}, errors.New("k8s requires kubernetes settings and no TAE authority")
	}
	if (!m.Enabled && m.Stage != ManagedExecutorStageDisabled) || (m.Enabled && m.Stage != ManagedExecutorStageActive) {
		return LoadedConfig{}, errors.New("Kubernetes stage must be disabled or active; TAE policy-bootstrap is not applicable")
	}
	if len(m.WorkspaceAllowlist) < 1 || len(m.WorkspaceAllowlist) > 64 {
		return LoadedConfig{}, errors.New("Kubernetes workspace allowlist is required")
	}
	list := append([]string(nil), m.WorkspaceAllowlist...)
	slices.Sort(list)
	for i, id := range list {
		if !validUUID(id) || (i > 0 && id == list[i-1]) {
			return LoadedConfig{}, errors.New("invalid or duplicate workspace allowlist")
		}
	}
	if !slices.Contains(list, d.Bootstrap.WorkspaceID) {
		return LoadedConfig{}, errors.New("Kubernetes allowlist must include bootstrap workspace")
	}
	m.WorkspaceAllowlist = list
	if !validUUID(m.Environment.EnvironmentID) || m.Environment.Root.Path != "/workspace" {
		return LoadedConfig{}, errors.New("Kubernetes environment ID and /workspace root required")
	}
	for _, id := range []string{d.Bootstrap.WorkspaceID, d.Bootstrap.SessionID, d.Bootstrap.OwnerUserID, d.Bootstrap.ExecutorID} {
		if m.Environment.EnvironmentID == id {
			return LoadedConfig{}, errors.New("Kubernetes environment identity is not unique")
		}
	}
	if m.Environment.Root.DefaultCWD != "" && m.Environment.Root.DefaultCWD != "." {
		return LoadedConfig{}, errors.New("initial Kubernetes profile must start at workspace root")
	}
	if err := validateText("Kubernetes environment displayName", m.Environment.Root.DisplayName, 1, 256); err != nil {
		return LoadedConfig{}, err
	}
	if m.Enabled && (!m.Lark.Enabled || !m.Bkectl.Enabled) {
		return LoadedConfig{}, errors.New("Kubernetes CLI profile requires Lark and bkectl")
	}
	k := *m.Kubernetes
	if k.RuntimeProxyURL != "" && k.RuntimeProxyURL != kubernetesRuntimeProxyURL(d.ClusterDomain) {
		return LoadedConfig{}, errors.New("Kubernetes runtime proxy differs from the installed SG internal egress service")
	}
	if k.Namespace == d.Namespace || k.Namespace == "kube-system" || k.Scope == "" || len(k.Scope) > 63 || !dnsLabelPattern.MatchString(k.Scope) || !validDNSName(k.RuntimeServerName) {
		return LoadedConfig{}, errors.New("Kubernetes sandbox namespace/scope/runtime identity invalid")
	}
	if k.GatewayImage == "" || strings.ContainsAny(k.GatewayImage, " \t\n\r") || strings.ContainsAny(d.Images.ManagedSandbox, " \t\n\r") {
		return LoadedConfig{}, errors.New("Kubernetes runtime and gateway images must be concrete references")
	}
	if len(k.APIEgress) == 0 {
		return LoadedConfig{}, errors.New("Kubernetes API egress must explicitly identify cluster endpoints")
	}
	if err := normalizeEgressRules("kubernetes.apiEgress", &k.APIEgress); err != nil {
		return LoadedConfig{}, err
	}
	if err := normalizeEgressRules("kubernetes.runtimeExternalEgress", &k.RuntimeExternalEgress); err != nil {
		return LoadedConfig{}, err
	}
	m.Kubernetes = &k
	ttl, err := parseManagedDuration("sandboxTtl", m.Environment.SandboxTTL, 30*time.Second, 24*time.Hour)
	if err != nil {
		return LoadedConfig{}, err
	}
	activity, err := parseManagedDuration("activityTtl", m.Environment.ActivityTTL, 3*time.Second, ttl)
	if err != nil {
		return LoadedConfig{}, err
	}
	idle, err := parseManagedDuration("idleTtl", m.Environment.IdleTTL, time.Second, ttl)
	if err != nil {
		return LoadedConfig{}, err
	}
	return LoadedConfig{Document: ConfigDocument{Managed: m}, ManagedSandboxTTL: ttl, ManagedActivityTTL: activity, ManagedIdleTTL: idle}, nil
}

func validateKubernetesProfiles(d *ConfigDocument) ([]LoadedManagedSandboxProfile, error) {
	if len(d.SandboxProfiles) < 1 || len(d.SandboxProfiles) > 2 || len(d.ProxyProfiles) != 0 ||
		d.SandboxRegions.DefaultRegion != managedsandboxprofile.RegionSG ||
		len(d.SandboxRegions.Regions) != len(d.SandboxProfiles) {
		return nil, errors.New("Kubernetes deployment requires an SG profile and an optional CN profile; TAE proxies are forbidden")
	}
	loaded, err := validateKubernetesManagedExecutor(d.Managed, *d)
	if err != nil {
		return nil, err
	}
	configuredRegions := make(map[string]struct{}, len(d.SandboxProfiles))
	configuredCatalogRegions := make(map[string]struct{}, len(d.SandboxRegions.Regions))
	for _, region := range d.SandboxRegions.Regions {
		if region != managedsandboxprofile.RegionSG && region != managedsandboxprofile.RegionCN {
			return nil, fmt.Errorf("Kubernetes sandbox region %q is unsupported", region)
		}
		if _, duplicate := configuredCatalogRegions[region]; duplicate {
			return nil, fmt.Errorf("Kubernetes sandbox region %q is repeated", region)
		}
		configuredCatalogRegions[region] = struct{}{}
	}
	var local ManagedSandboxProfileDocument
	for index, candidate := range d.SandboxProfiles {
		if candidate.Region != managedsandboxprofile.RegionSG && candidate.Region != managedsandboxprofile.RegionCN {
			return nil, fmt.Errorf("sandboxProfiles[%d] has unsupported Kubernetes region %q", index, candidate.Region)
		}
		if _, duplicate := configuredRegions[candidate.Region]; duplicate {
			return nil, fmt.Errorf("Kubernetes sandbox region %q is repeated", candidate.Region)
		}
		configuredRegions[candidate.Region] = struct{}{}
		if _, present := configuredCatalogRegions[candidate.Region]; !present {
			return nil, fmt.Errorf("sandboxRegions.Regions is missing Kubernetes region %q", candidate.Region)
		}
		if candidate.TAE != (ManagedTAEDocument{}) || len(candidate.SandboxExternalEgress) != 0 {
			return nil, fmt.Errorf("sandboxProfiles[%d] contains TAE or external-egress authority", index)
		}
		if !sameKubernetesEnvironment(candidate.Environment, d.Managed.Environment) {
			return nil, fmt.Errorf("sandboxProfiles[%d].environment differs from managed executor", index)
		}
		if candidate.Region == managedsandboxprofile.RegionSG && candidate.Environment.EnvironmentID != d.Managed.Environment.EnvironmentID {
			return nil, errors.New("SG Kubernetes environment identity must equal the managed executor environment")
		}
		gateway := candidate.Gateway
		wantComponent, wantSecret, wantServer := "sandbox-gateway-k8s", "agentserver-sandbox-k8s-secrets", "sandbox-gateway-k8s.agentserver.internal"
		if candidate.Region == managedsandboxprofile.RegionCN {
			wantComponent, wantSecret, wantServer = "sandbox-gateway-cn-k8s", "agentserver-sandbox-cn-k8s-secrets", "sandbox-gateway-cn-k8s.agentserver.internal"
		}
		if gateway.Component != wantComponent || gateway.Port != HarnessControlPort || gateway.Secret != wantSecret || gateway.ServerName != wantServer {
			return nil, fmt.Errorf("sandboxProfiles[%d] gateway authority differs from deployment-owned %s gateway", index, candidate.Region)
		}
		if candidate.Region == managedsandboxprofile.RegionSG {
			if gateway.External || gateway.ExternalURL != "" {
				return nil, errors.New("SG Kubernetes gateway must be an in-cluster mTLS profile")
			}
			ip, parseErr := netip.ParseAddr(gateway.ClusterIP)
			if parseErr != nil || !ip.Is4() || !ip.IsPrivate() {
				return nil, errors.New("SG Kubernetes gateway requires a private IPv4 Service IP")
			}
			for address, owner := range configuredServiceIPs(d.Services) {
				if address == ip && owner != "sandboxGateway" {
					return nil, fmt.Errorf("Kubernetes gateway IP conflicts with %s", owner)
				}
			}
			local = candidate
		} else {
			if !gateway.External || gateway.ClusterIP != "" || !validExternalSandboxGatewayURL(gateway.ExternalURL) {
				return nil, errors.New("CN Kubernetes gateway must use an external HTTPS URL without a Service IP")
			}
		}
	}
	if len(configuredRegions) != len(configuredCatalogRegions) || len(configuredRegions) < 1 {
		return nil, errors.New("Kubernetes sandbox region catalog does not match profiles")
	}
	if _, present := configuredRegions[managedsandboxprofile.RegionSG]; !present {
		return nil, errors.New("Kubernetes deployment requires an SG sandbox profile")
	}
	if len(d.SandboxProfiles) == 2 {
		if _, present := configuredRegions[managedsandboxprofile.RegionCN]; !present {
			return nil, errors.New("two-profile Kubernetes deployment requires CN profile")
		}
	}
	for _, candidate := range d.SandboxProfiles {
		if candidate.Region == managedsandboxprofile.RegionCN && candidate.Environment.EnvironmentID == d.Managed.Environment.EnvironmentID {
			return nil, errors.New("CN Kubernetes environment identity must differ from the SG managed environment")
		}
	}
	if _, err := kubernetesresources.Resources(kubernetesTemplateConfig(*d, local)); err != nil {
		return nil, err
	}
	result := make([]LoadedManagedSandboxProfile, 0, len(d.SandboxProfiles))
	for _, candidate := range d.SandboxProfiles {
		result = append(result, LoadedManagedSandboxProfile{Document: candidate, SandboxTTL: loaded.ManagedSandboxTTL, ActivityTTL: loaded.ManagedActivityTTL, IdleTTL: loaded.ManagedIdleTTL})
	}
	return result, nil
}

func sameKubernetesEnvironment(candidate, managed ManagedEnvironmentDocument) bool {
	candidate.EnvironmentID = managed.EnvironmentID
	return reflect.DeepEqual(candidate, managed)
}

func validExternalSandboxGatewayURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host == ProductionCNSandboxGatewayHostname &&
		parsed.User == nil && parsed.Path == "" && parsed.RawPath == "" && parsed.RawQuery == "" &&
		parsed.Fragment == "" && parsed.Opaque == "" && !parsed.ForceQuery && parsed.String() == raw
}

func kubernetesTemplateConfig(d ConfigDocument, p ManagedSandboxProfileDocument) kubernetesresources.Config {
	k := d.Managed.Kubernetes
	return kubernetesresources.Config{Namespace: k.Namespace, TemplateName: k.Pool, RuntimeImage: d.Images.ManagedSandbox, RuntimeTLSSecret: k.RuntimeTLSSecret, GatewayNamespace: d.Namespace, GatewayServiceAccount: p.Gateway.Component, GatewayComponent: p.Gateway.Component, GatewayIdentity: "spiffe://" + d.TrustDomain + "/ns/" + d.Namespace + "/sa/" + p.Gateway.Component, RuntimeClassName: k.RuntimeClassName, BubblewrapProfile: k.BubblewrapProfile, RuntimeProxyURL: k.RuntimeProxyURL}
}

func kubernetesRuntimeProxyURL(clusterDomain string) string {
	return "socks5h://ssh-egress-merlin-i18nbd-syd2a-83092-headless.ssh-egress.svc." + clusterDomain + ":1080"
}

func managedCredentialScopeEnvironment(m ManagedExecutorDocument) []any {
	if m.Provider == "k8s" {
		return []any{valueEnvironment("AGENTSERVER_V2_MANAGED_SANDBOX_SCOPE", m.Kubernetes.Scope), valueEnvironment("AGENTSERVER_V2_MANAGED_WEBHOOK_REQUIRED", "false")}
	}
	return []any{valueEnvironment("AGENTSERVER_V2_MANAGED_TAE_PSM", m.TAE.PSM), valueEnvironment("AGENTSERVER_V2_TAE_POLICY_WEBHOOK_REQUIRED", strconv.FormatBool(m.TAE.Policy.PublicWebhookRequired))}
}

func renderKubernetesGateway(c renderContext, p LoadedManagedSandboxProfile) (kubeObject, error) {
	d := c.config.Document
	k := d.Managed.Kubernetes
	g := p.Document.Gateway
	workspaceAllowlist := strings.Join(d.Managed.WorkspaceAllowlist, ",")
	if k.AllWorkspaces {
		workspaceAllowlist = "*"
	}
	material, err := secretMaterialVolume("material", g.Secret, "sandbox-gateway-k8s", groupReadableSecretMode)
	if err != nil {
		return nil, err
	}
	mounts, err := secretMaterialMounts("material", "/var/run/agentserver/material", "sandbox-gateway-k8s")
	if err != nil {
		return nil, err
	}
	env := []any{
		valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_LISTEN_ADDR", listenAddress(g.Port)),
		valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_TLS_CERT_FILE", serviceMaterialPath("tls.crt")), valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_TLS_KEY_FILE", serviceMaterialPath("tls.key")), valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_CLIENT_CA_FILE", serviceMaterialPath("ca.crt")),
		valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_SPIFFE_ID", spiffeIdentity(c.config, g.Component)), valueEnvironment("AGENTSERVER_V2_EXECUTOR_GATEWAY_SPIFFE_ID", spiffeIdentity(c.config, executorComponent)), valueEnvironment("AGENTSERVER_V2_HARNESS_POOL_SPIFFE_ID", spiffeIdentity(c.config, harnessComponent)),
		valueEnvironment("AGENTSERVER_V2_CORE_URL", internalOrigin(CoreInternalHost, d.Services.Core.Port)), valueEnvironment("AGENTSERVER_V2_CORE_CA_FILE", serviceMaterialPath("ca.crt")), valueEnvironment("AGENTSERVER_V2_CORE_CLIENT_CERT_FILE", serviceMaterialPath("tls.crt")), valueEnvironment("AGENTSERVER_V2_CORE_CLIENT_KEY_FILE", serviceMaterialPath("tls.key")), valueEnvironment("AGENTSERVER_V2_CORE_SERVER_NAME", CoreInternalHost),
		valueEnvironment("AGENTSERVER_V2_SANDBOX_CAPABILITY_KEYRING_FILE", serviceMaterialPath("sandbox-capability-keyring.json")),
		valueEnvironment("AGENTSERVER_V2_RUNTIME_CA_FILE", serviceMaterialPath("runtime-ca.crt")), valueEnvironment("AGENTSERVER_V2_RUNTIME_CLIENT_CERT_FILE", serviceMaterialPath("runtime-client.crt")), valueEnvironment("AGENTSERVER_V2_RUNTIME_CLIENT_KEY_FILE", serviceMaterialPath("runtime-client.key")), valueEnvironment("AGENTSERVER_V2_RUNTIME_SERVER_NAME", k.RuntimeServerName),
		valueEnvironment("AGENTSERVER_V2_SANDBOX_PROVIDER", "k8s"), valueEnvironment("AGENTSERVER_V2_SANDBOX_REGION", p.Document.Region), valueEnvironment("AGENTSERVER_V2_SANDBOX_SCOPE", k.Scope+"-"+p.Document.Region), valueEnvironment("AGENTSERVER_V2_SANDBOX_NAMESPACE", k.Namespace), valueEnvironment("AGENTSERVER_V2_SANDBOX_POOL", k.Pool), valueEnvironment("AGENTSERVER_V2_CLUSTER_DOMAIN", d.ClusterDomain),
		valueEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_EXTERNAL_TLS", strconv.FormatBool(g.External)),
		valueEnvironment("AGENTSERVER_V2_MANAGED_IDLE_TTL", p.Document.Environment.IdleTTL), valueEnvironment("AGENTSERVER_V2_MANAGED_WORKSPACE_ALLOWLIST", workspaceAllowlist), valueEnvironment("AGENTSERVER_V2_SANDBOX_ENSURE_TIMEOUT", "3m"), valueEnvironment("AGENTSERVER_V2_SANDBOX_ENSURE_POLL_INTERVAL", "1s"),
	}
	resource := deployment(deploymentInput{namespace: d.Namespace, platform: d.Platform, component: g.Component, replicas: d.Replicas.SandboxGateway, image: k.GatewayImage, serviceAccount: g.Component, command: []any{"/usr/local/bin/sandbox-gateway-k8s"}, environment: env, volumes: []any{material, emptyDirVolume("scratch", "Memory", d.Resources.ScratchTmpfs)}, volumeMounts: append(mounts, kubeObject{"name": "scratch", "mountPath": "/tmp"}), hostAliases: map[string]string{CoreInternalHost: d.Services.Core.ClusterIP}, resources: d.Resources.SandboxGateway, uid: ServiceUID, gid: ServiceGID, fsGroup: ServiceGID, strategy: "RollingUpdate", configHash: c.documentHash, termination: 45})
	resource["spec"].(kubeObject)["template"].(kubeObject)["spec"].(kubeObject)["automountServiceAccountToken"] = true
	return resource, nil
}

func renderKubernetesWorkloadResources(c renderContext) ([]kubeObject, error) {
	d := c.config.Document
	k := d.Managed.Kubernetes
	var local ManagedSandboxProfileDocument
	for _, profile := range d.SandboxProfiles {
		if !profile.Gateway.External {
			local = profile
			break
		}
	}
	if local.Region == "" {
		return nil, errors.New("Kubernetes workload resources require a local SG profile")
	}
	objects, err := kubernetesresources.Resources(kubernetesTemplateConfig(d, local))
	if err != nil {
		return nil, err
	}
	items := make([]kubeObject, 0, len(objects)+1)
	for _, obj := range objects {
		items = append(items, kubeObject(obj))
	}
	if k.APIServerEntityPolicy {
		// On SG's Cilium, node/API-server identities do not match ordinary
		// ipBlock rules. This admits only the gateway to the API-server entity,
		// never all cluster/private addresses or runtime Pods.
		items = append(items, kubeObject{"apiVersion": "cilium.io/v2", "kind": "CiliumNetworkPolicy", "metadata": kubeObject{"name": "sandbox-gateway-k8s-apiserver", "namespace": d.Namespace}, "spec": kubeObject{"endpointSelector": kubeObject{"matchLabels": kubeObject{"app.kubernetes.io/name": "sandbox-gateway-k8s", "app.kubernetes.io/part-of": "agentserver-v2"}}, "egress": []any{kubeObject{"toEntities": []any{"kube-apiserver"}, "toPorts": []any{kubeObject{"ports": []any{kubeObject{"port": "443", "protocol": "TCP"}, kubeObject{"port": "6443", "protocol": "TCP"}}}}}}}})
	}
	egress := append(publicHTTPSEgress(), externalEgress(k.RuntimeExternalEgress)...)
	if k.RuntimeProxyURL != "" {
		egress = append(egress, kubeObject{"to": []any{kubeObject{"namespaceSelector": kubeObject{"matchLabels": kubeObject{"kubernetes.io/metadata.name": "ssh-egress"}}, "podSelector": kubeObject{"matchLabels": kubeObject{"app": "ssh-egress-merlin-i18nbd-syd2a-83092"}}}}, "ports": []any{kubeObject{"protocol": "TCP", "port": 1080}}})
	}
	items = append(items, kubeObject{"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy", "metadata": kubeObject{"name": "sandbox-cli-egress", "namespace": k.Namespace}, "spec": kubeObject{"podSelector": kubeObject{"matchLabels": kubeObject{"app.kubernetes.io/name": "agentserver-sandbox-runtime"}}, "policyTypes": []any{"Egress"}, "egress": egress}})
	return items, nil
}
