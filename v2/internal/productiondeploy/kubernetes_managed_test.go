package productiondeploy

import (
	"encoding/json"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestKubernetesReleaseCutover(t *testing.T) {
	base, err := ValidateConfig(validConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	d := kubernetesConfigDocument()
	r := KubernetesRelease{ServiceImage: d.Images.Service, HarnessImage: d.Images.Harness, RuntimeImage: d.Images.ManagedSandbox, GatewayImage: d.Managed.Kubernetes.GatewayImage, EnvironmentID: d.Managed.Environment.EnvironmentID, APICIDR: "10.251.224.59/32", AllWorkspaces: true}
	got, err := PrepareKubernetesRelease(base, r)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Document.Managed.Kubernetes.AllWorkspaces || got.Document.Managed.Kubernetes.RuntimeClassName != "" {
		t.Fatal("cutover defaults changed")
	}
	if got.Document.Runtime.FinalExecSHA256 != base.Document.Runtime.FinalExecSHA256 || got.Document.OAuth != base.Document.OAuth {
		t.Fatal("cutover changed runtime/OAuth")
	}
	r.EnvironmentID = base.Document.Managed.Environment.EnvironmentID
	if _, err := PrepareKubernetesRelease(base, r); err == nil {
		t.Fatal("TAE environment ID was reused")
	}
}

func kubernetesConfigDocument() ConfigDocument {
	d := validConfigDocument()
	d.Managed.Provider = "k8s"
	d.Managed.Enabled = true
	d.Managed.Stage = ManagedExecutorStageActive
	d.Managed.TAE = ManagedTAEDocument{}
	d.Managed.Environment.EnvironmentID = "aaaaaaaa-1111-4444-8888-111111111111"
	d.Managed.Environment.Root.DisplayName = "Managed SG Kubernetes"
	d.Managed.Environment.Root.Description = "Default container runtime; managed workspace"
	d.Managed.Kubernetes = &KubernetesSandboxDocument{Namespace: "agentserver-sandboxes", Pool: "managed-cli-v1", Scope: "sg-managed-cli", GatewayImage: "registry-sg.byted.cs.ac.cn/ghcr/agentserver/v2-k8s-gateway:canary", RuntimeTLSSecret: "agentserver-runtime-tls", RuntimeServerName: "sandbox-runtime.agentserver.internal", APIEgress: []EgressRuleDocument{{CIDR: "10.251.224.152/32", Ports: []uint16{6443}}}, RuntimeExternalEgress: []EgressRuleDocument{}}
	d.Managed.Kubernetes.BubblewrapProfile = true
	d.Managed.Kubernetes.APIServerEntityPolicy = true
	d.Managed.Kubernetes.RuntimeProxyURL = kubernetesRuntimeProxyURL(d.ClusterDomain)
	d.SandboxRegions = ManagedSandboxRegionsDocument{DefaultRegion: "sg", Regions: []string{"sg"}}
	d.ProxyProfiles = []ManagedSandboxProxyProfileDocument{}
	d.SandboxProfiles = []ManagedSandboxProfileDocument{{Region: "sg", Environment: d.Managed.Environment, Gateway: ManagedSandboxGatewayDocument{Component: "sandbox-gateway-k8s", ClusterIP: d.Services.SandboxGateway.ClusterIP, Port: 8443, ServerName: "sandbox-gateway-k8s.agentserver.internal", Secret: "agentserver-sandbox-k8s-secrets"}, SandboxExternalEgress: []EgressRuleDocument{}}}
	return d
}

func TestKubernetesChartRendersExecutableGraphWithoutTAE(t *testing.T) {
	d := kubernetesConfigDocument()
	loaded, err := ValidateConfig(d)
	if err != nil {
		t.Fatal(err)
	}
	chart, err := RenderHelmChart(loaded)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "chart")
	t.Cleanup(func() {
		_ = filepath.Walk(destination, func(path string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				_ = os.Chmod(path, 0700)
			}
			return nil
		})
	})
	for range 2 {
		if err := WriteHelmChart(chart, destination); err != nil {
			t.Fatalf("Kubernetes chart publication: %v", err)
		}
	}
	if helm, err := exec.LookPath("helm"); err == nil {
		if output, err := exec.Command(helm, "template", "agentserver-v2", destination, "--namespace", d.Namespace).CombinedOutput(); err != nil {
			t.Fatalf("render Kubernetes chart with Helm: %v\n%s", err, output)
		}
	}
	bundle, err := Render(loaded)
	if err != nil {
		t.Fatal(err)
	}
	foundation := parseKubernetesList(t, mustBundleFile(t, bundle, foundationFile))
	runtime := parseKubernetesList(t, mustBundleFile(t, bundle, runtimeFile))
	for _, component := range []string{coreComponent, executorComponent} {
		env := deploymentLiteralEnvironment(t, runtime, component)
		if env("AGENTSERVER_V2_MANAGED_SANDBOX_SCOPE") != "sg-managed-cli" || env("AGENTSERVER_V2_MANAGED_WEBHOOK_REQUIRED") != "false" {
			t.Fatalf("provider scope for %s is wrong", component)
		}
	}
	env := deploymentLiteralEnvironment(t, runtime, "sandbox-gateway-k8s")
	if env("AGENTSERVER_V2_SANDBOX_SCOPE") != d.Managed.Kubernetes.Scope {
		t.Fatal("SG gateway changed immutable reservation scope")
	}
	if env("AGENTSERVER_V2_SANDBOX_PROVIDER") != "k8s" || env("AGENTSERVER_V2_SANDBOX_NAMESPACE") != "agentserver-sandboxes" {
		t.Fatal("gateway configuration missing")
	}
	var profile struct {
		Profiles []struct {
			BackendKind string `json:"backendKind"`
		}
	}
	if err := json.Unmarshal([]byte(deploymentLiteralEnvironment(t, runtime, executorComponent)("AGENTSERVER_V2_MANAGED_SANDBOX_GATEWAY_PROFILES")), &profile); err != nil || len(profile.Profiles) != 1 || profile.Profiles[0].BackendKind != "k8s" {
		t.Fatalf("dispatch kind lost: %+v %v", profile, err)
	}
	template := findResource(t, foundation, "SandboxTemplate", d.Managed.Kubernetes.Pool)
	pod := objectField(t, objectField(t, objectField(t, template, "spec"), "podTemplate"), "spec")
	if _, present := pod["runtimeClassName"]; present {
		t.Fatal("default runtime must omit runtimeClassName")
	}
	if pod["automountServiceAccountToken"] != false {
		t.Fatal("sandbox received Kubernetes token")
	}
	rawPod, _ := json.Marshal(pod)
	if !strings.Contains(string(rawPod), "AGENTSERVER_SANDBOX_HTTP_PROXY") || !strings.Contains(string(rawPod), d.Managed.Kubernetes.RuntimeProxyURL) {
		t.Fatal("runtime internal egress missing")
	}
	policy := findResource(t, foundation, "NetworkPolicy", "sandbox-cli-egress")
	rawPolicy, _ := json.Marshal(policy)
	if !strings.Contains(string(rawPolicy), "ssh-egress-merlin-i18nbd-syd2a-83092") {
		t.Fatal("runtime proxy has no narrow egress rule")
	}
	apiPolicy := findResource(t, foundation, "CiliumNetworkPolicy", "sandbox-gateway-k8s-apiserver")
	apiSpec := objectField(t, apiPolicy, "spec")
	selector := objectField(t, objectField(t, apiSpec, "endpointSelector"), "matchLabels")
	if selector["app.kubernetes.io/name"] != "sandbox-gateway-k8s" {
		t.Fatal("API egress must not cover runtime Pods")
	}
	apiRaw, _ := json.Marshal(apiPolicy)
	if !strings.Contains(string(apiRaw), `"toEntities":["kube-apiserver"]`) {
		t.Fatal("missing precise API server entity egress")
	}
	for _, name := range []string{helmRuntimeManifestFile, helmFoundationManifestFile} {
		data, _ := chart.File(name)
		for _, forbidden := range []string{"AGENTSERVER_V2_TAE_", "bytecloud-access-key-id", "bytecloud-secret-access-key", "tae-network-probe"} {
			if strings.Contains(string(data), forbidden) {
				t.Fatalf("Kubernetes manifests retain %s", forbidden)
			}
		}
	}
	probe, _ := chart.File(helmTAENetworkProbeManifestFile)
	if len(strings.TrimSpace(string(probe))) != 0 {
		t.Fatal("Kubernetes chart generated TAE probes")
	}
}

func TestKubernetesConfigRejectsMixedAuthority(t *testing.T) {
	for _, field := range []string{"tae", "region", "stage", "environment", "scope"} {
		t.Run(field, func(t *testing.T) {
			d := kubernetesConfigDocument()
			switch field {
			case "tae":
				d.Managed.TAE.PSM = ProductionTAEPSM
			case "region":
				d.SandboxProfiles[0].Region = "cn"
			case "stage":
				d.Managed.Stage = ManagedExecutorStageBootstrap
			case "environment":
				d.SandboxProfiles[0].Environment.EnvironmentID = "bbbbbbbb-1111-4444-8888-111111111111"
			case "scope":
				d.Managed.Kubernetes.Scope = ""
			}
			if _, err := ValidateConfig(d); err == nil {
				t.Fatalf("accepted %s", field)
			}
		})
	}
}

func TestKubernetesProductionSchema(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(productionRepositoryRoot(t), "api/schema/production-deployment.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	d := kubernetesConfigDocument()
	raw, err = json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	assertProductionSchemaAccepts(t, resolved, raw)
}

func TestCNRenderedControlPlaneGraph(t *testing.T) {
	d := kubernetesConfigDocument()
	cn := d.SandboxProfiles[0]
	cn.Region = "cn"
	cn.Environment.EnvironmentID = "bbbbbbbb-1111-4444-8888-111111111111"
	cn.Environment.Root.DisplayName = "CN · Kubernetes"
	cn.Gateway = ManagedSandboxGatewayDocument{Component: "sandbox-gateway-cn-k8s", Port: 8443, ServerName: ProductionCNSandboxGatewayBackendHost, Secret: "agentserver-sandbox-cn-k8s-secrets", External: true, ExternalURL: "https://" + ProductionCNSandboxGatewayHostname}
	// Exercise CN-first ordering, which previously selected CN for SG runtime
	// resources and created an empty local host alias.
	d.SandboxProfiles = append([]ManagedSandboxProfileDocument{cn}, d.SandboxProfiles...)
	d.SandboxRegions.Regions = []string{"cn", "sg"}
	loaded, err := ValidateConfig(d)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Render(loaded)
	if err != nil {
		t.Fatal(err)
	}
	foundation := parseKubernetesList(t, mustBundleFile(t, bundle, foundationFile))
	runtime := parseKubernetesList(t, mustBundleFile(t, bundle, runtimeFile))
	for _, component := range []string{coreComponent, executorComponent} {
		raw := deploymentLiteralEnvironment(t, runtime, component)(managedcredential.ScopeBindingsEnvironment)
		scopes, err := managedcredential.ParseScopeBindings(raw)
		if err != nil {
			t.Fatal(err)
		}
		for env, want := range map[string]string{cn.Environment.EnvironmentID: "cn-managed-cli", d.Managed.Environment.EnvironmentID: "sg-managed-cli"} {
			if got, ok := scopes.Scope(env); !ok || got != want {
				t.Fatalf("%s routes %s to %q, want %s", component, env, got, want)
			}
		}
	}
	if findResourceOptional(runtime, "Deployment", cn.Gateway.Component) != nil || findResourceOptional(foundation, "Service", cn.Gateway.Component) != nil {
		t.Fatal("external CN gateway was rendered locally in SG")
	}
	pod := objectField(t, objectField(t, objectField(t, findResource(t, runtime, "Deployment", executorComponent), "spec"), "template"), "spec")
	for _, entry := range pod["hostAliases"].([]any) {
		if _, err := netip.ParseAddr(entry.(map[string]any)["ip"].(string)); err != nil {
			t.Fatal("executor has invalid host alias", entry)
		}
	}
	findResource(t, foundation, "CiliumNetworkPolicy", "executor-cn-gateway-egress")
	route := objectField(t, findResource(t, foundation, "HTTPRoute", "agentserver-core-external"), "spec")
	rule := objectArrayFirst(t, route, "rules")
	paths := map[string]string{}
	for _, entry := range rule["matches"].([]any) {
		p := objectField(t, entry.(map[string]any), "path")
		paths[p["value"].(string)] = p["type"].(string)
	}
	for _, p := range []string{corecontract.ReserveManagedSandboxPath, corecontract.ListManagedSandboxesForReconcilePath, corecontract.AuthorizeManagedSandboxOperationPath} {
		if paths[p] != "Exact" {
			t.Fatalf("Core external route missing %s", p)
		}
	}
	if paths[corecontract.ManagedSandboxPathPrefix] != "PathPrefix" {
		t.Fatal("sandbox lifecycle prefix missing")
	}
	for _, p := range loaded.ManagedSandboxProfiles {
		raw, err := renderManagedEnvironmentBootstrapJSON(loaded, p)
		if err != nil {
			t.Fatal(err)
		}
		var bootstrap managedEnvironmentBootstrapJSON
		if err := json.Unmarshal(raw, &bootstrap); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(bootstrap.RetainedWorkspaceRegions, []string{"cn", "sg"}) {
			t.Fatal("upgrade would erase CN selection")
		}
	}
}

func TestKubernetesCNExternalProfileSchema(t *testing.T) {
	base, err := ValidateConfig(validConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	d := base.Document
	cn := KubernetesRelease{
		ServiceImage: d.Images.Service, HarnessImage: d.Images.Harness, RuntimeImage: d.Images.ManagedSandbox,
		GatewayImage:  "registry-sg.byted.cs.ac.cn/ghcr/agentserver/v2-k8s-gateway@sha256:" + strings.Repeat("a", 64),
		EnvironmentID: "aaaaaaaa-1111-4444-8888-111111111111", APICIDR: "10.251.224.59/32",
		CNGatewayURL: "https://sandbox-gateway-cn.byted.bps.dev", CNGatewayServerName: "sandbox-gateway-cn-k8s.agentserver.internal", CNEnvironmentID: "bbbbbbbb-1111-4444-8888-111111111111",
	}
	got, err := PrepareKubernetesRelease(base, cn)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Document.SandboxProfiles) != 2 || got.Document.SandboxProfiles[1].Gateway.ExternalURL == "" {
		t.Fatalf("CN external profile was not emitted: %+v", got.Document.SandboxProfiles)
	}
	raw, err := json.Marshal(got.Document)
	if err != nil {
		t.Fatal(err)
	}
	schemaRaw, err := os.ReadFile(filepath.Join(productionRepositoryRoot(t), "api/schema/production-deployment.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	assertProductionSchemaAccepts(t, resolved, raw)
}
