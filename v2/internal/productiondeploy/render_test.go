package productiondeploy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/executorgateway"
)

func TestRenderProducesDeterministicStagedProductionBundle(t *testing.T) {
	loaded, err := ValidateConfig(validConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	first, err := Render(loaded)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Render(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != 5 || len(second.Files) != len(first.Files) {
		t.Fatalf("rendered file count = %d / %d", len(first.Files), len(second.Files))
	}
	for index := range first.Files {
		left, right := first.Files[index], second.Files[index]
		if left.Name != right.Name || left.SHA256 != right.SHA256 || !bytes.Equal(left.Content, right.Content) {
			t.Fatalf("rendered file %d is nondeterministic", index)
		}
		digest := sha256.Sum256(left.Content)
		if hex.EncodeToString(digest[:]) != left.SHA256 {
			t.Fatalf("rendered file %s checksum mismatch", left.Name)
		}
	}
}

func TestRenderLocksProductionTopologyAndSecurityShape(t *testing.T) {
	loaded, err := ValidateConfig(validConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Render(loaded)
	if err != nil {
		t.Fatal(err)
	}
	foundation := parseKubernetesList(t, mustBundleFile(t, bundle, foundationFile))
	runtime := parseKubernetesList(t, mustBundleFile(t, bundle, runtimeFile))
	migration := parseKubernetesList(t, mustBundleFile(t, bundle, migrationFile))
	bootstrap := parseKubernetesList(t, mustBundleFile(t, bundle, bootstrapFile))

	if len(migration) != 1 || migration[0]["kind"] != "Job" || len(bootstrap) != 1 || bootstrap[0]["kind"] != "Job" {
		t.Fatal("migration and bootstrap stages must each contain exactly one Kubernetes Job")
	}
	if countKind(runtime, "Job") != 0 || countKind(runtime, "HorizontalPodAutoscaler") != 0 {
		t.Fatal("runtime stage contains a per-run Job or HPA")
	}
	if countKind(runtime, "Deployment") != 5 || countKind(runtime, "PodDisruptionBudget") != 4 {
		t.Fatalf("runtime topology = %d deployments, %d PDBs", countKind(runtime, "Deployment"), countKind(runtime, "PodDisruptionBudget"))
	}
	gateway := findResource(t, runtime, "Deployment", executorComponent)
	gatewaySpec := objectField(t, gateway, "spec")
	if numberField(t, gatewaySpec, "replicas") != 1 || stringField(t, objectField(t, gatewaySpec, "strategy"), "type") != "Recreate" {
		t.Fatal("executor-gateway is not single-replica Recreate")
	}
	if findResourceOptional(runtime, "PodDisruptionBudget", executorComponent) != nil {
		t.Fatal("executor-gateway unexpectedly has a PDB that can obstruct Recreate")
	}

	pool := findResource(t, runtime, "Deployment", harnessComponent)
	poolTemplate := objectField(t, objectField(t, pool, "spec"), "template")
	poolAnnotations := objectField(t, poolTemplate, "metadata")
	poolConfigHash := stringField(t, objectField(t, poolAnnotations, "annotations"), "agentserver.dev/config-sha256")
	if !digestPattern.MatchString(poolConfigHash) {
		t.Fatalf("harness deployment config annotation is not a recomputed SHA-256: %q", poolConfigHash)
	}
	poolSpec := objectField(t, poolTemplate, "spec")
	initNames := containerNames(t, arrayField(t, poolSpec, "initContainers"))
	wantInit := []string{"materialize-harness-pool", "materialize-harness-worker", "prepare-harness-directories", "install-network-guard"}
	if strings.Join(initNames, ",") != strings.Join(wantInit, ",") {
		t.Fatalf("harness init order = %v, want %v", initNames, wantInit)
	}
	poolContainer := objectArrayFirst(t, poolSpec, "containers")
	capabilities := stringArray(t, objectField(t, objectField(t, poolContainer, "securityContext"), "capabilities"), "add")
	if strings.Join(capabilities, ",") != "CHOWN,SETUID,SETGID,DAC_OVERRIDE" {
		t.Fatalf("harness runtime capabilities = %v", capabilities)
	}
	if containsString(capabilities, "NET_ADMIN") || containsString(capabilities, "NET_RAW") {
		t.Fatal("harness runtime retained network administration capability")
	}

	if countKind(foundation, "NetworkPolicy") != 8 {
		t.Fatalf("foundation NetworkPolicy count = %d", countKind(foundation, "NetworkPolicy"))
	}
	if countKind(foundation, "HTTPRoute") != 3 {
		t.Fatalf("foundation HTTPRoute count = %d", countKind(foundation, "HTTPRoute"))
	}
	for _, resource := range foundation {
		if resource["kind"] == "Service" && stringField(t, objectField(t, resource, "spec"), "type") != "ClusterIP" {
			t.Fatalf("service %v is not ClusterIP", objectField(t, resource, "metadata")["name"])
		}
	}
	assertHTTPRoute(t, foundation, "agentserver-frontend", ProductionFrontendHostname, browserComponent, PublicHTTPPort,
		[]string{"/", "/auth", "/index.html", "/oauth2", "/readyz", "/reference"})
	assertHTTPRoute(t, foundation, "agentserver-browser-api", ProductionBrowserHostname, browserComponent, PublicHTTPPort,
		[]string{"/v2"})
	assertHTTPRoute(t, foundation, "agentserver-executor-agentx", ProductionExecutorHostname, executorComponent, PublicHTTPPort,
		[]string{executorgateway.AgentxChallengePath, executorgateway.AgentxConnectPath, executorgateway.AgentxEnrollmentPath})
	if bytes.Contains(mustJSONResource(t, findResource(t, foundation, "HTTPRoute", "agentserver-executor-agentx")), []byte(executorgateway.ExecutorMCPPath)) {
		t.Fatal("executor public HTTPRoute exposes /mcp")
	}
	defaultDeny := findResource(t, foundation, "NetworkPolicy", "agentserver-default-deny")
	defaultSpec := objectField(t, defaultDeny, "spec")
	defaultSelector := objectField(t, defaultSpec, "podSelector")
	defaultLabels := objectField(t, defaultSelector, "matchLabels")
	if len(defaultLabels) != 1 || defaultLabels["agentserver.dev/network"] != "managed" {
		t.Fatalf("default-deny selector = %#v, want only AgentServer managed pods", defaultLabels)
	}
	if _, hasIngress := defaultSpec["ingress"]; hasIngress {
		t.Fatal("default-deny unexpectedly contains an ingress allowance")
	}
	if _, hasEgress := defaultSpec["egress"]; hasEgress {
		t.Fatal("default-deny unexpectedly contains an egress allowance")
	}
	assertDNSPolicySupportsServiceAndPodDestinations(t, findResource(t, foundation, "NetworkPolicy", coreComponent))
	for _, name := range []string{"agentserver-migrate-egress", "agentserver-bootstrap-egress", coreComponent} {
		assertCNPGDatabaseEgress(t, findResource(t, foundation, "NetworkPolicy", name))
	}

	for _, resources := range [][]map[string]any{foundation, migration, bootstrap, runtime} {
		for _, resource := range resources {
			if resource["kind"] != "Deployment" && resource["kind"] != "Job" {
				continue
			}
			assertPinnedImages(t, resource)
			assertProductionNodeSelector(t, resource, "amd64")
			assertNoImagePullSecrets(t, resource)
		}
	}
	all := append(append(append(append([]byte(nil), mustBundleFile(t, bundle, foundationFile)...), mustBundleFile(t, bundle, migrationFile)...), mustBundleFile(t, bundle, bootstrapFile)...), mustBundleFile(t, bundle, runtimeFile)...)
	for _, forbidden := range []string{
		"AGENTSERVER_V2_KMS_", "AWS_ROLE_ARN", "AWS_WEB_IDENTITY_TOKEN_FILE",
		"eks.amazonaws.com/role-arn", "sts.amazonaws.com", "AGENTSERVER_V2_DEV_",
	} {
		if bytes.Contains(all, []byte(forbidden)) {
			t.Fatalf("rendered bundle contains static AWS credential field %q", forbidden)
		}
	}
	for _, component := range []string{coreComponent, harnessComponent} {
		deployment := findResource(t, runtime, "Deployment", component)
		container := objectArrayFirst(t, objectField(t, objectField(t, objectField(t, deployment, "spec"), "template"), "spec"), "containers")
		assertSecretEnvironment(t, container, "AGENTSERVER_V2_S3_ACCESS_KEY_ID", loaded.Document.Secrets.ObjectStore, "access-key-id")
		assertSecretEnvironment(t, container, "AGENTSERVER_V2_S3_SECRET_ACCESS_KEY", loaded.Document.Secrets.ObjectStore, "secret-access-key")
	}
}

func TestRenderPinsLinuxAMD64Nodes(t *testing.T) {
	document := validConfigDocument()
	document.Platform = ProductionPlatformLinuxAMD64
	loaded, err := ValidateConfig(document)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Render(loaded)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{migrationFile, bootstrapFile, runtimeFile} {
		for _, resource := range parseKubernetesList(t, mustBundleFile(t, bundle, file)) {
			if resource["kind"] == "Deployment" || resource["kind"] == "Job" {
				assertProductionNodeSelector(t, resource, "amd64")
			}
		}
	}
}

func TestRenderEmbedsExactBootstrapAndWorkerContracts(t *testing.T) {
	loaded, err := ValidateConfig(validConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Render(loaded)
	if err != nil {
		t.Fatal(err)
	}
	foundation := parseKubernetesList(t, mustBundleFile(t, bundle, foundationFile))
	var bootstrapConfig, workerConfig, networkConfig string
	for _, resource := range foundation {
		if resource["kind"] != "ConfigMap" {
			continue
		}
		data := objectField(t, resource, "data")
		if value, found := data["bootstrap.json"].(string); found {
			bootstrapConfig = value
		}
		if value, found := data["worker-deployment.json"].(string); found {
			workerConfig = value
		}
		if value, found := data["network-guard.json"].(string); found {
			networkConfig = value
		}
	}
	if bootstrapConfig == "" || workerConfig == "" || networkConfig == "" {
		t.Fatal("foundation is missing bootstrap, worker, or network configuration")
	}
	var bootstrapDocument productionBootstrapJSON
	if err := json.Unmarshal([]byte(bootstrapConfig), &bootstrapDocument); err != nil {
		t.Fatal(err)
	}
	if bootstrapDocument.ExecutorID != loaded.Document.Bootstrap.ExecutorID || bootstrapDocument.Identity.Issuer != loaded.Document.OAuth.ExternalOIDC.Issuer {
		t.Fatalf("bootstrap document = %+v", bootstrapDocument)
	}
	var workerDocument workerDeploymentJSON
	if err := json.Unmarshal([]byte(workerConfig), &workerDocument); err != nil {
		t.Fatal(err)
	}
	if workerDocument.WorkerUID != WorkerUID || workerDocument.AppUID != AppUID || workerDocument.CodexConfigProfile != CodexConfigProfile() ||
		workerDocument.RunManifestKeyringFile != workerMaterialPath("run-manifest-keyring.json") {
		t.Fatalf("worker document = %+v", workerDocument)
	}
	var guard networkGuardJSON
	if err := json.Unmarshal([]byte(networkConfig), &guard); err != nil {
		t.Fatal(err)
	}
	if len(guard.Policies) != 2 || guard.Policies[0].UID != WorkerUID || guard.Policies[1].UID != AppUID {
		t.Fatalf("network guard = %+v", guard)
	}
}

func parseKubernetesList(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var list struct {
		APIVersion string           `json:"apiVersion"`
		Kind       string           `json:"kind"`
		Items      []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	if list.APIVersion != "v1" || list.Kind != "List" || len(list.Items) == 0 {
		t.Fatalf("invalid Kubernetes List envelope = %+v", list)
	}
	return list.Items
}

func mustBundleFile(t *testing.T, bundle Bundle, name string) []byte {
	t.Helper()
	raw, found := bundle.File(name)
	if !found {
		t.Fatalf("bundle file %s not found", name)
	}
	return raw
}

func countKind(resources []map[string]any, kind string) int {
	count := 0
	for _, resource := range resources {
		if resource["kind"] == kind {
			count++
		}
	}
	return count
}

func findResource(t *testing.T, resources []map[string]any, kind, name string) map[string]any {
	t.Helper()
	resource := findResourceOptional(resources, kind, name)
	if resource == nil {
		t.Fatalf("resource %s/%s not found", kind, name)
	}
	return resource
}

func findResourceOptional(resources []map[string]any, kind, name string) map[string]any {
	for _, resource := range resources {
		if resource["kind"] != kind {
			continue
		}
		metadata, _ := resource["metadata"].(map[string]any)
		if metadata["name"] == name {
			return resource
		}
	}
	return nil
}

func objectField(t *testing.T, object map[string]any, name string) map[string]any {
	t.Helper()
	value, ok := object[name].(map[string]any)
	if !ok {
		t.Fatalf("field %s is not an object: %#v", name, object[name])
	}
	return value
}

func arrayField(t *testing.T, object map[string]any, name string) []any {
	t.Helper()
	value, ok := object[name].([]any)
	if !ok {
		t.Fatalf("field %s is not an array: %#v", name, object[name])
	}
	return value
}

func objectArrayFirst(t *testing.T, object map[string]any, name string) map[string]any {
	t.Helper()
	values := arrayField(t, object, name)
	if len(values) == 0 {
		t.Fatalf("field %s is empty", name)
	}
	value, ok := values[0].(map[string]any)
	if !ok {
		t.Fatalf("field %s first value is not an object", name)
	}
	return value
}

func stringField(t *testing.T, object map[string]any, name string) string {
	t.Helper()
	value, ok := object[name].(string)
	if !ok {
		t.Fatalf("field %s is not a string", name)
	}
	return value
}

func numberField(t *testing.T, object map[string]any, name string) int {
	t.Helper()
	value, ok := object[name].(float64)
	if !ok {
		t.Fatalf("field %s is not a number", name)
	}
	return int(value)
}

func containerNames(t *testing.T, values []any) []string {
	t.Helper()
	result := make([]string, len(values))
	for index, raw := range values {
		container, ok := raw.(map[string]any)
		if !ok {
			t.Fatal("container is not an object")
		}
		result[index] = stringField(t, container, "name")
	}
	return result
}

func stringArray(t *testing.T, object map[string]any, name string) []string {
	t.Helper()
	values := arrayField(t, object, name)
	result := make([]string, len(values))
	for index, raw := range values {
		value, ok := raw.(string)
		if !ok {
			t.Fatalf("field %s[%d] is not a string", name, index)
		}
		result[index] = value
	}
	return result
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertHTTPRoute(
	t *testing.T,
	resources []map[string]any,
	name, hostname, backend string,
	port uint16,
	wantPaths []string,
) {
	t.Helper()
	route := findResource(t, resources, "HTTPRoute", name)
	spec := objectField(t, route, "spec")
	hostnames := arrayField(t, spec, "hostnames")
	if len(hostnames) != 1 || hostnames[0] != hostname {
		t.Fatalf("HTTPRoute %s hostnames = %#v", name, hostnames)
	}
	parent := objectArrayFirst(t, spec, "parentRefs")
	if parent["namespace"] != ProductionGatewayNamespace || parent["name"] != ProductionGatewayName || parent["sectionName"] != ProductionGatewaySection {
		t.Fatalf("HTTPRoute %s parent = %#v", name, parent)
	}
	rule := objectArrayFirst(t, spec, "rules")
	backendRef := objectArrayFirst(t, rule, "backendRefs")
	if backendRef["name"] != backend || numberField(t, backendRef, "port") != int(port) {
		t.Fatalf("HTTPRoute %s backend = %#v", name, backendRef)
	}
	paths := make([]string, 0, len(wantPaths))
	for _, raw := range arrayField(t, rule, "matches") {
		match, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("HTTPRoute %s match = %#v", name, raw)
		}
		paths = append(paths, stringField(t, objectField(t, match, "path"), "value"))
	}
	slices.Sort(paths)
	slices.Sort(wantPaths)
	if !slices.Equal(paths, wantPaths) {
		t.Fatalf("HTTPRoute %s paths = %v, want %v", name, paths, wantPaths)
	}
}

func mustJSONResource(t *testing.T, resource map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(resource)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertSecretEnvironment(t *testing.T, container map[string]any, name, secretName, key string) {
	t.Helper()
	for _, raw := range arrayField(t, container, "env") {
		environment, ok := raw.(map[string]any)
		if !ok || environment["name"] != name {
			continue
		}
		valueFrom := objectField(t, environment, "valueFrom")
		secretKeyRef := objectField(t, valueFrom, "secretKeyRef")
		if secretKeyRef["name"] != secretName || secretKeyRef["key"] != key {
			t.Fatalf("environment %s secretKeyRef = %#v", name, secretKeyRef)
		}
		return
	}
	t.Fatalf("environment %s not found", name)
}

func assertPinnedImages(t *testing.T, resource map[string]any) {
	t.Helper()
	spec := objectField(t, resource, "spec")
	var podSpec map[string]any
	if resource["kind"] == "Deployment" {
		podSpec = objectField(t, objectField(t, spec, "template"), "spec")
	} else {
		podSpec = objectField(t, objectField(t, spec, "template"), "spec")
	}
	for _, field := range []string{"initContainers", "containers"} {
		values, found := podSpec[field].([]any)
		if !found {
			continue
		}
		for _, raw := range values {
			container := raw.(map[string]any)
			image := stringField(t, container, "image")
			if !imagePattern.MatchString(image) {
				t.Fatalf("unpinned image %q", image)
			}
		}
	}
}

func assertProductionNodeSelector(t *testing.T, resource map[string]any, architecture string) {
	t.Helper()
	spec := objectField(t, resource, "spec")
	podSpec := objectField(t, objectField(t, spec, "template"), "spec")
	selector := objectField(t, podSpec, "nodeSelector")
	if stringField(t, selector, "kubernetes.io/os") != "linux" || stringField(t, selector, "kubernetes.io/arch") != architecture {
		t.Fatalf("%s is not pinned to linux/%s: %#v", resource["kind"], architecture, selector)
	}
}

func assertNoImagePullSecrets(t *testing.T, resource map[string]any) {
	t.Helper()
	spec := objectField(t, resource, "spec")
	podSpec := objectField(t, objectField(t, spec, "template"), "spec")
	if secrets, exists := podSpec["imagePullSecrets"]; exists {
		t.Fatalf("%s imagePullSecrets = %#v, want field omitted for the public registry", resource["kind"], secrets)
	}
}

func assertDNSPolicySupportsServiceAndPodDestinations(t *testing.T, resource map[string]any) {
	t.Helper()
	egress := arrayField(t, objectField(t, resource, "spec"), "egress")
	for _, rawRule := range egress {
		rule, ok := rawRule.(map[string]any)
		if !ok {
			continue
		}
		ports, _ := rule["ports"].([]any)
		isDNS := false
		for _, rawPort := range ports {
			port, _ := rawPort.(map[string]any)
			if port["port"] == float64(53) {
				isDNS = true
			}
		}
		if !isDNS {
			continue
		}
		peers := arrayField(t, rule, "to")
		if len(peers) != 2 {
			t.Fatalf("DNS egress peers = %d, want exact Service and Pod destinations", len(peers))
		}
		servicePeer, _ := peers[0].(map[string]any)
		podPeer, _ := peers[1].(map[string]any)
		if _, found := servicePeer["ipBlock"]; !found || podPeer["namespaceSelector"] == nil || podPeer["podSelector"] == nil {
			t.Fatalf("DNS egress peers do not cover DNAT before/after shapes: %#v", peers)
		}
		return
	}
	t.Fatal("Core NetworkPolicy has no exact DNS egress rule")
}

func assertCNPGDatabaseEgress(t *testing.T, resource map[string]any) {
	t.Helper()
	egress := arrayField(t, objectField(t, resource, "spec"), "egress")
	matches := 0
	for _, rawRule := range egress {
		rule, ok := rawRule.(map[string]any)
		if !ok {
			continue
		}
		ports, _ := rule["ports"].([]any)
		if len(ports) != 1 {
			continue
		}
		port, _ := ports[0].(map[string]any)
		if port["protocol"] != "TCP" || port["port"] != float64(productionPostgresPort) {
			continue
		}
		peers := arrayField(t, rule, "to")
		if len(peers) != 1 {
			t.Fatalf("CNPG egress peers = %#v, want one pod selector", peers)
		}
		peer, _ := peers[0].(map[string]any)
		selector := objectField(t, peer, "podSelector")
		labels := objectField(t, selector, "matchLabels")
		if len(labels) != 1 || labels["cnpg.io/cluster"] != productionPostgresClusterName {
			t.Fatalf("CNPG egress selector = %#v", labels)
		}
		if _, hasIPBlock := peer["ipBlock"]; hasIPBlock {
			t.Fatal("CNPG egress unexpectedly uses an externally supplied IP block")
		}
		matches++
	}
	if matches != 1 {
		t.Fatalf("CNPG PostgreSQL egress rules = %d, want 1", matches)
	}
}
