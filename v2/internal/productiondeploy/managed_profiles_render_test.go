package productiondeploy

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
	"github.com/agentserver/agentserver/v2/internal/taenetworkreport"
)

func TestRenderFourManagedSandboxProfilesKeepsRoutingCatalogsAndResourcesAligned(t *testing.T) {
	loaded, err := ValidateConfig(fourRegionConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.ManagedSandboxProfiles) != 4 {
		t.Fatalf("loaded managed sandbox profiles = %d, want 4", len(loaded.ManagedSandboxProfiles))
	}
	bundle, err := Render(loaded)
	if err != nil {
		t.Fatal(err)
	}
	foundation := parseKubernetesList(t, mustBundleFile(t, bundle, foundationFile))
	runtime := parseKubernetesList(t, mustBundleFile(t, bundle, runtimeFile))
	bootstrap := parseKubernetesList(t, mustBundleFile(t, bundle, managedEnvironmentBootstrapFile))
	if len(bootstrap) != 4 || countKind(bootstrap, "Job") != 4 {
		t.Fatalf("managed environment bootstrap resources = %d items / %d Jobs, want four Jobs", len(bootstrap), countKind(bootstrap, "Job"))
	}

	wantBindings := make(map[string]managedSandboxCatalogTestBinding, len(loaded.ManagedSandboxProfiles))
	for _, profile := range loaded.ManagedSandboxProfiles {
		document := profile.Document
		component := document.Gateway.Component
		findResource(t, foundation, "ServiceAccount", component)
		findResource(t, foundation, "Service", component)
		findResource(t, foundation, "NetworkPolicy", component)
		gatewayDeployment := findResource(t, runtime, "Deployment", component)
		findResource(t, runtime, "PodDisruptionBudget", component)
		sandboxEnvironment := deploymentLiteralEnvironment(t, runtime, component)
		podSpec := objectField(t, objectField(t, objectField(t, gatewayDeployment, "spec"), "template"), "spec")
		if serviceAccount := stringField(t, podSpec, "serviceAccountName"); serviceAccount != component {
			t.Fatalf("sandbox gateway %s service account = %q", component, serviceAccount)
		}
		if identity := sandboxEnvironment("AGENTSERVER_V2_SANDBOX_GATEWAY_SPIFFE_ID"); identity != spiffeIdentity(loaded, component) {
			t.Fatalf("sandbox gateway %s SPIFFE identity = %q", component, identity)
		}
		if got := sandboxEnvironment("AGENTSERVER_V2_TAE_REGION"); got != document.Region {
			t.Fatalf("sandbox gateway %s provider region = %q", component, got)
		}
		wantBindings[document.Region] = managedSandboxCatalogTestBinding{
			Region: document.Region, ProfileID: document.ProfileID,
			BindingSHA256: document.BindingSHA256, EnvironmentID: document.Environment.EnvironmentID,
		}

		policy := findResource(t, foundation, "NetworkPolicy", component)
		spec := objectField(t, policy, "spec")
		if profile.Proxy == nil {
			if document.Region != managedsandboxprofile.RegionBOE {
				t.Fatalf("profile %s unexpectedly has direct routing", document.Region)
			}
			for _, rule := range document.SandboxExternalEgress {
				assertIPBlockPeerPresent(t, spec, rule.CIDR, rule.Ports[0])
			}
			if hasNamespacedPodPeerInNamespace(spec, "egress", "proxy-system") {
				t.Fatal("BOE sandbox NetworkPolicy unexpectedly routes through a proxy Pod")
			}
			if proxyURL := sandboxEnvironment("AGENTSERVER_V2_TAE_PROXY_URL"); proxyURL != "" {
				t.Fatalf("BOE sandbox gateway proxy URL = %q, want direct routing", proxyURL)
			}
			continue
		}
		assertNamespacedPodPeerPresent(t, spec, "egress", profile.Proxy.Namespace, profile.Proxy.PodSelector, profile.Proxy.Port)
		if proxyURL := sandboxEnvironment("AGENTSERVER_V2_TAE_PROXY_URL"); proxyURL != profile.Proxy.URL {
			t.Fatalf("sandbox gateway %s proxy URL = %q, want %q", component, proxyURL, profile.Proxy.URL)
		}
	}

	coreCatalog := decodeManagedSandboxCoreCatalog(t, deploymentLiteralEnvironment(t, runtime, coreComponent)("AGENTSERVER_V2_MANAGED_SANDBOX_PROFILE_CATALOG"))
	var gatewayIdentities []string
	if err := json.Unmarshal([]byte(deploymentLiteralEnvironment(t, runtime, coreComponent)("AGENTSERVER_V2_SANDBOX_GATEWAY_SPIFFE_IDS")), &gatewayIdentities); err != nil {
		t.Fatalf("decode Core sandbox gateway identity catalog: %v", err)
	}
	if len(gatewayIdentities) != len(loaded.ManagedSandboxProfiles) {
		t.Fatalf("Core sandbox gateway identities = %v", gatewayIdentities)
	}
	for _, profile := range loaded.ManagedSandboxProfiles {
		if !slices.Contains(gatewayIdentities, spiffeIdentity(loaded, profile.Document.Gateway.Component)) {
			t.Fatalf("Core sandbox gateway identities are missing %s", profile.Document.Gateway.Component)
		}
	}
	launchCatalog := decodeManagedSandboxProfileCatalog(t, deploymentLiteralEnvironment(t, runtime, harnessComponent)("AGENTSERVER_V2_MANAGED_SANDBOX_LAUNCH_PROFILES"))
	gatewayCatalog := decodeManagedSandboxProfileCatalog(t, deploymentLiteralEnvironment(t, runtime, executorComponent)("AGENTSERVER_V2_MANAGED_SANDBOX_GATEWAY_PROFILES"))
	if coreCatalog.DefaultRegion != managedsandboxprofile.RegionI18NTT {
		t.Fatalf("Core default managed sandbox region = %q", coreCatalog.DefaultRegion)
	}
	assertManagedSandboxCatalogMatches(t, "Core", coreCatalog.Bindings, wantBindings)
	assertManagedSandboxCatalogMatches(t, "Harness", launchCatalog.Profiles, wantBindings)
	assertManagedSandboxCatalogMatches(t, "Executor", gatewayCatalog.Profiles, wantBindings)
}

func TestPreparePolicyBootstrapClearsEveryInstalledManagedSandboxProfile(t *testing.T) {
	bootstrap, err := PreparePolicyBootstrapDocument(fourRegionConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	if !managedPolicyBootstrap(bootstrap.Managed) || len(bootstrap.SandboxProfiles) != 4 {
		t.Fatalf("four-region bootstrap stage = %q with %d profiles", bootstrap.Managed.Stage, len(bootstrap.SandboxProfiles))
	}
	profileIDs := make(map[string]bool, len(bootstrap.SandboxProfiles))
	for _, profile := range bootstrap.SandboxProfiles {
		if profile.Environment.RuntimeProfileSHA256 != "" || profile.Environment.PackSetSHA256 != "" ||
			profile.TAE.Policy.Published || profile.TAE.Policy.Approved || profile.TAE.Policy.EvidenceRef != "" ||
			profile.TAE.NetworkEvidence != (ManagedTAENetworkEvidenceDocument{}) {
			t.Fatalf("bootstrap profile %s retained active evidence: %+v", profile.Region, profile)
		}
		if profileIDs[profile.ProfileID] {
			t.Fatalf("bootstrap profile identity %q is repeated", profile.ProfileID)
		}
		profileIDs[profile.ProfileID] = true
	}
	loaded, err := ValidateConfig(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	chart, err := RenderHelmChart(loaded)
	if err != nil {
		t.Fatal(err)
	}
	probe := parseHelmManifest(t, mustHelmFile(t, chart, helmTAENetworkProbeManifestFile))
	if len(probe) != 9 || countKind(probe, "ServiceAccount") != 1 ||
		countKind(probe, "NetworkPolicy") != 4 || countKind(probe, "Job") != 4 {
		t.Fatalf("four-region bootstrap probe manifest = %d resources", len(probe))
	}
	values := string(mustHelmFile(t, chart, helmValuesFile))
	template := string(mustHelmFile(t, chart, helmTAENetworkProbeTemplateFile))
	for _, region := range managedsandboxprofile.Regions() {
		if !strings.Contains(values, "    "+region+": \"\"") ||
			!strings.Contains(template, "index .Values.taeNetworkProbe.policyRevisions \""+region+"\"") ||
			!strings.Contains(template, taeNetworkProbeJobPlaceholderForRegion(region)) ||
			!strings.Contains(template, taeNetworkProbeInputPlaceholderForRegion(region)) {
			t.Fatalf("four-region Helm probe is missing region %s", region)
		}
	}
}

func TestActivateManagedSandboxProfilesRequiresAndBindsEveryRegion(t *testing.T) {
	bootstrap, err := PreparePolicyBootstrapDocument(fourRegionConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	evidence := fourRegionActivationEvidence(t, bootstrap)
	active, err := ActivateManagedSandboxProfilesDocument(bootstrap, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if !managedExecutionActive(active.Managed) || len(active.SandboxProfiles) != 4 {
		t.Fatalf("activated four-region document = stage %q / %d profiles", active.Managed.Stage, len(active.SandboxProfiles))
	}
	for _, profile := range active.SandboxProfiles {
		if !profile.TAE.Policy.Published || !profile.TAE.Policy.Approved ||
			profile.TAE.Policy.EvidenceRef == "" || profile.TAE.NetworkEvidence.EvidenceRef == "" ||
			!nonzeroDigest(profile.TAE.NetworkEvidence.ReportSHA256) ||
			!nonzeroDigest(profile.TAE.NetworkEvidence.BindingSHA256) ||
			!nonzeroDigest(profile.Environment.RuntimeProfileSHA256) ||
			!nonzeroDigest(profile.Environment.PackSetSHA256) {
			t.Fatalf("activated profile %s has incomplete evidence locks: %+v", profile.Region, profile)
		}
	}
	if _, err := ValidateConfig(active); err != nil {
		t.Fatal(err)
	}
	if _, err := ActivateManagedSandboxProfilesDocument(bootstrap, evidence[:len(evidence)-1]); err == nil {
		t.Fatal("multi-region activation accepted a missing regional report")
	}
	tampered := append([]ManagedSandboxActivationEvidence(nil), evidence...)
	tampered[0].NetworkReport.Configuration.Region = managedsandboxprofile.RegionBOE
	if _, err := ActivateManagedSandboxProfilesDocument(bootstrap, tampered); err == nil {
		t.Fatal("multi-region activation accepted a report from another region")
	}
}

func fourRegionActivationEvidence(t *testing.T, bootstrap ConfigDocument) []ManagedSandboxActivationEvidence {
	t.Helper()
	evidence := make([]ManagedSandboxActivationEvidence, 0, len(bootstrap.SandboxProfiles))
	for _, profile := range bootstrap.SandboxProfiles {
		revision := "lark-readonly-" + profile.Region + "-v2"
		report := validActivationNetworkReport(bootstrap, revision)
		report.Configuration.Region = profile.Region
		report.Configuration.ByteCloudSite = profile.TAE.ByteCloudSite
		report.Configuration.JWTEndpoint = profile.TAE.ByteCloudJWTEndpoint
		report.Configuration.ProxyURL = ""
		for _, proxy := range bootstrap.ProxyProfiles {
			if proxy.Name == profile.TAE.ProxyProfile {
				report.Configuration.ProxyURL = proxy.URL
				break
			}
		}
		report.Configuration.ControlPlaneHost = strings.TrimPrefix(profile.TAE.ControlPlaneURL, "https://")
		report.Configuration.DataPlaneDomainSuffix = profile.TAE.DataPlaneSuffix
		report.Configuration.SandboxID = profile.TAE.SandboxID
		report.Configuration.SandboxRevisionID = profile.TAE.RevisionID
		raw, err := taenetworkreport.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		evidence = append(evidence, ManagedSandboxActivationEvidence{
			Region: profile.Region, PolicyRevision: revision,
			PolicyEvidenceRef: "tae-security/" + profile.Region + "/2026-08-15",
			NetworkReport:     report, NetworkReportSHA256: taenetworkreport.SHA256(raw),
			NetworkEvidenceRef: "artifact://agentserver/network/" + profile.Region + "/report.json",
		})
	}
	return evidence
}

type managedSandboxCatalogTestBinding struct {
	Region        string `json:"region"`
	ProfileID     string `json:"profileId"`
	BindingSHA256 string `json:"bindingSha256"`
	EnvironmentID string `json:"environmentId"`
}

type managedSandboxCoreCatalogTestDocument struct {
	DefaultRegion string                             `json:"defaultRegion"`
	Bindings      []managedSandboxCatalogTestBinding `json:"bindings"`
}

type managedSandboxProfileCatalogTestDocument struct {
	Profiles []managedSandboxCatalogTestBinding `json:"profiles"`
}

func decodeManagedSandboxCoreCatalog(t *testing.T, raw string) managedSandboxCoreCatalogTestDocument {
	t.Helper()
	var document managedSandboxCoreCatalogTestDocument
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatalf("decode Core managed sandbox catalog: %v", err)
	}
	return document
}

func decodeManagedSandboxProfileCatalog(t *testing.T, raw string) managedSandboxProfileCatalogTestDocument {
	t.Helper()
	var document managedSandboxProfileCatalogTestDocument
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatalf("decode managed sandbox runtime catalog: %v", err)
	}
	return document
}

func assertManagedSandboxCatalogMatches(t *testing.T, name string, got []managedSandboxCatalogTestBinding, want map[string]managedSandboxCatalogTestBinding) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s managed sandbox catalog entries = %d, want %d", name, len(got), len(want))
	}
	seen := make(map[string]bool, len(got))
	for _, binding := range got {
		if expected, ok := want[binding.Region]; !ok || binding != expected {
			t.Fatalf("%s managed sandbox binding for %q = %+v, want %+v", name, binding.Region, binding, expected)
		}
		seen[binding.Region] = true
	}
	if len(seen) != len(want) {
		t.Fatalf("%s managed sandbox catalog regions = %v", name, seen)
	}
}

func deploymentLiteralEnvironment(t *testing.T, resources []map[string]any, component string) func(string) string {
	t.Helper()
	deployment := findResource(t, resources, "Deployment", component)
	pod := objectField(t, objectField(t, objectField(t, deployment, "spec"), "template"), "spec")
	container := objectArrayFirst(t, pod, "containers")
	return func(name string) string { return literalEnvironment(t, container, name) }
}

func assertIPBlockPeerPresent(t *testing.T, spec map[string]any, cidr string, port uint16) {
	t.Helper()
	for _, rawRule := range arrayField(t, spec, "egress") {
		rule := rawRule.(map[string]any)
		ports, ok := rule["ports"].([]any)
		if !ok || len(ports) != 1 || numberField(t, ports[0].(map[string]any), "port") != int(port) {
			continue
		}
		for _, rawPeer := range arrayField(t, rule, "to") {
			peer := rawPeer.(map[string]any)
			block, ok := peer["ipBlock"].(map[string]any)
			if ok && stringField(t, block, "cidr") == cidr {
				return
			}
		}
	}
	t.Fatalf("NetworkPolicy egress is missing IP block %s on TCP port %d", cidr, port)
}

func hasNamespacedPodPeerInNamespace(spec map[string]any, field, namespace string) bool {
	rules, _ := spec[field].([]any)
	for _, rawRule := range rules {
		rule, _ := rawRule.(map[string]any)
		peers, _ := rule["to"].([]any)
		for _, rawPeer := range peers {
			peer, _ := rawPeer.(map[string]any)
			namespaceSelector, namespaceOK := peer["namespaceSelector"].(map[string]any)
			if !namespaceOK || peer["podSelector"] == nil {
				continue
			}
			labels, _ := namespaceSelector["matchLabels"].(map[string]any)
			if labels["kubernetes.io/metadata.name"] == namespace {
				return true
			}
		}
	}
	return false
}

func fourRegionConfigDocument() ConfigDocument {
	document := validConfigDocument()
	document.ProxyProfiles = []ManagedSandboxProxyProfileDocument{
		{Name: ManagedSandboxProxyCN, URL: "socks5h://merlin-hl-1.proxy-system.svc.cluster.local:1081", Namespace: "proxy-system", PodSelector: map[string]string{"app": "merlin-hl-1"}, Port: 1081},
		{Name: ManagedSandboxProxyI18NBD, URL: "socks5h://merlin-useast14a-1.proxy-system.svc.cluster.local:1082", Namespace: "proxy-system", PodSelector: map[string]string{"app": "merlin-useast14a-1"}, Port: 1082},
		{Name: ManagedSandboxProxyI18NTT, URL: "socks5h://merlin-maliva-1.proxy-system.svc.cluster.local:1083", Namespace: "proxy-system", PodSelector: map[string]string{"app": "merlin-maliva-1"}, Port: 1083},
	}
	document.SandboxRegions = ManagedSandboxRegionsDocument{
		DefaultRegion: managedsandboxprofile.RegionI18NTT,
		Regions:       managedsandboxprofile.Regions(),
	}
	type profileInput struct {
		region, proxy, component, clusterIP, serverName, secret, environmentID, byteCloudSite, jwtEndpoint string
		directEgress                                                                                       []EgressRuleDocument
	}
	inputs := []profileInput{
		{region: managedsandboxprofile.RegionCN, proxy: ManagedSandboxProxyCN, component: "sandbox-gateway-cn", clusterIP: "10.96.10.18", serverName: "sandbox-gateway-cn.agentserver.internal", secret: "agentserver-sandbox-cn-secrets", environmentID: "31000000-0000-4000-8000-000000000001", byteCloudSite: "cn", jwtEndpoint: "https://jwt-cn.example.internal"},
		{region: managedsandboxprofile.RegionBOE, component: "sandbox-gateway-boe", clusterIP: "10.96.10.19", serverName: "sandbox-gateway-boe.agentserver.internal", secret: "agentserver-sandbox-boe-secrets", environmentID: "31000000-0000-4000-8000-000000000002", byteCloudSite: "cn", jwtEndpoint: "https://jwt-boe.example.internal", directEgress: []EgressRuleDocument{{CIDR: "10.70.0.0/24", Ports: []uint16{443}}, {CIDR: "fd00:70::/64", Ports: []uint16{443}}}},
		{region: managedsandboxprofile.RegionI18NBD, proxy: ManagedSandboxProxyI18NBD, component: "sandbox-gateway-i18n-bd", clusterIP: "10.96.10.20", serverName: "sandbox-gateway-i18n-bd.agentserver.internal", secret: "agentserver-sandbox-i18n-bd-secrets", environmentID: "31000000-0000-4000-8000-000000000003", byteCloudSite: "i18n-bd", jwtEndpoint: "https://jwt-i18n-bd.example.internal"},
		{region: managedsandboxprofile.RegionI18NTT, proxy: ManagedSandboxProxyI18NTT, component: sandboxComponent, clusterIP: document.Services.SandboxGateway.ClusterIP, serverName: SandboxInternalHost, secret: document.Secrets.SandboxGateway, environmentID: "31000000-0000-4000-8000-000000000004", byteCloudSite: "i18n-tt", jwtEndpoint: "https://jwt-i18n-tt.example.internal"},
	}

	document.SandboxProfiles = nil
	for index, input := range inputs {
		managed := document.Managed
		managed.Environment.EnvironmentID = input.environmentID
		managed.Environment.Root.DisplayName = "Managed " + input.region
		managed.Environment.Root.Description = "TAE managed sandbox test profile for " + input.region
		managed.TAE.Region = input.region
		managed.TAE.SandboxID = fmt.Sprintf("sandbox-%d", index+1)
		managed.TAE.RevisionID = fmt.Sprintf("revision-%d", index+1)
		managed.TAE.ByteCloudSite = input.byteCloudSite
		managed.TAE.ByteCloudJWTEndpoint = input.jwtEndpoint
		managed.TAE.ProxyProfile = input.proxy
		managed.TAE.ControlPlaneURL, managed.TAE.DataPlaneSuffix, _ = managedSandboxTAEAuthority(input.region)
		managed.TAE.Policy.BindingSHA256 = managedTAEPolicyBinding(managed.TAE).DigestHex()
		managed.TAE.NetworkEvidence.ReportSHA256 = canonicalDigest("network-report-" + input.region)
		managed.TAE.NetworkEvidence.EvidenceRef = "artifact://network/" + input.region + "/report.json"
		managed.TAE.NetworkEvidence.BindingSHA256 = ""

		gateway := ManagedSandboxGatewayDocument{
			Component: input.component, ClusterIP: input.clusterIP, Port: HarnessControlPort,
			ServerName: input.serverName, Secret: input.secret,
		}
		synthetic := document
		synthetic.Managed = managed
		synthetic.Services.SandboxGateway = InternalServiceDocument{ClusterIP: gateway.ClusterIP, Port: gateway.Port}
		synthetic.Secrets.SandboxGateway = gateway.Secret
		synthetic.Network.SandboxExternalEgress = append([]EgressRuleDocument{}, input.directEgress...)
		managed.TAE.NetworkEvidence.BindingSHA256 = managedTAENetworkEvidenceDigest(synthetic)
		managed.Environment.RuntimeProfileSHA256 = managedRuntimeProfileDigest(synthetic, managed)
		managed.Environment.PackSetSHA256 = managedPackSetDigest(managed)
		profile := ManagedSandboxProfileDocument{
			Region: input.region, ProfileID: "tae-" + input.region + "-test-v1", Gateway: gateway,
			Environment: managed.Environment, TAE: managed.TAE,
			SandboxExternalEgress: append([]EgressRuleDocument{}, input.directEgress...),
		}
		var proxy *ManagedSandboxProxyProfileDocument
		for proxyIndex := range document.ProxyProfiles {
			if document.ProxyProfiles[proxyIndex].Name == input.proxy {
				proxy = &document.ProxyProfiles[proxyIndex]
				break
			}
		}
		profile.BindingSHA256 = managedSandboxProfileBindingSHA256(profile, proxy)
		document.SandboxProfiles = append(document.SandboxProfiles, profile)
		if input.region == document.SandboxRegions.DefaultRegion {
			document.Managed = managed
			document.Network.SandboxExternalEgress = append([]EgressRuleDocument{}, input.directEgress...)
		}
	}
	if !slices.Equal(document.SandboxRegions.Regions, managedsandboxprofile.Regions()) {
		panic("managed sandbox test region catalog drift")
	}
	return document
}
