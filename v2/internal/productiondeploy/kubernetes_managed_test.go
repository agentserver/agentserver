package productiondeploy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
