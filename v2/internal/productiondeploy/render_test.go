package productiondeploy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
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
	defaultDeny := findResource(t, foundation, "NetworkPolicy", "agentserver-default-deny")
	defaultSpec := objectField(t, defaultDeny, "spec")
	if _, hasIngress := defaultSpec["ingress"]; hasIngress {
		t.Fatal("default-deny unexpectedly contains an ingress allowance")
	}
	if _, hasEgress := defaultSpec["egress"]; hasEgress {
		t.Fatal("default-deny unexpectedly contains an egress allowance")
	}
	assertDNSPolicySupportsServiceAndPodDestinations(t, findResource(t, foundation, "NetworkPolicy", coreComponent))

	for _, resources := range [][]map[string]any{foundation, migration, bootstrap, runtime} {
		for _, resource := range resources {
			if resource["kind"] != "Deployment" && resource["kind"] != "Job" {
				continue
			}
			assertPinnedImages(t, resource)
			assertProductionNodeSelector(t, resource)
		}
	}
	all := append(append(append(append([]byte(nil), mustBundleFile(t, bundle, foundationFile)...), mustBundleFile(t, bundle, migrationFile)...), mustBundleFile(t, bundle, bootstrapFile)...), mustBundleFile(t, bundle, runtimeFile)...)
	for _, forbidden := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "aws_access_key_id", "secretAccessKey",
		"AGENTSERVER_V2_DEV_",
	} {
		if bytes.Contains(all, []byte(forbidden)) {
			t.Fatalf("rendered bundle contains static AWS credential field %q", forbidden)
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

func assertProductionNodeSelector(t *testing.T, resource map[string]any) {
	t.Helper()
	spec := objectField(t, resource, "spec")
	podSpec := objectField(t, objectField(t, spec, "template"), "spec")
	selector := objectField(t, podSpec, "nodeSelector")
	if stringField(t, selector, "kubernetes.io/os") != "linux" || stringField(t, selector, "kubernetes.io/arch") != "arm64" {
		t.Fatalf("%s is not pinned to the gated linux-arm64 platform: %#v", resource["kind"], selector)
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
