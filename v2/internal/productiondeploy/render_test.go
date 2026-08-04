package productiondeploy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
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
	if len(first.Files) != 7 || len(second.Files) != len(first.Files) {
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
	hydraMigration := parseKubernetesList(t, mustBundleFile(t, bundle, hydraMigrationFile))
	hydraSetup := parseKubernetesList(t, mustBundleFile(t, bundle, hydraSetupFile))
	bootstrap := parseKubernetesList(t, mustBundleFile(t, bundle, bootstrapFile))

	if len(migration) != 1 || migration[0]["kind"] != "Job" || len(hydraMigration) != 1 || hydraMigration[0]["kind"] != "Job" ||
		len(hydraSetup) != 1 || hydraSetup[0]["kind"] != "Job" || len(bootstrap) != 1 || bootstrap[0]["kind"] != "Job" {
		t.Fatal("Hydra migration/setup and AgentServer migration/bootstrap stages must each contain exactly one Kubernetes Job")
	}
	if countKind(runtime, "Job") != 0 || countKind(runtime, "HorizontalPodAutoscaler") != 0 {
		t.Fatal("runtime stage contains a per-run Job or HPA")
	}
	if countKind(runtime, "Deployment") != 7 || countKind(runtime, "PodDisruptionBudget") != 6 {
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
	hydra := findResource(t, runtime, "Deployment", hydraComponent)
	hydraContainer := objectArrayFirst(t, objectField(t, objectField(t, objectField(t, hydra, "spec"), "template"), "spec"), "containers")
	for name, want := range map[string]string{
		"URLS_SELF_ISSUER":                                 "https://auth-sg.byted.bps.dev/",
		"URLS_SELF_PUBLIC":                                 "https://auth-sg.byted.bps.dev",
		"URLS_LOGIN":                                       "https://auth-sg.byted.bps.dev/auth/hydra/login",
		"URLS_CONSENT":                                     "https://auth-sg.byted.bps.dev/auth/hydra/consent",
		"SERVE_PUBLIC_TLS_ENABLED":                         "false",
		"SERVE_ADMIN_TLS_ENABLED":                          "true",
		"SERVE_PUBLIC_CORS_ENABLED":                        "true",
		"SERVE_PUBLIC_CORS_ALLOWED_ORIGINS":                "https://agent.byted.bps.dev,https://browser.byted.bps.dev",
		"SERVE_PUBLIC_CORS_ALLOW_CREDENTIALS":              "false",
		"SERVE_COOKIES_SAME_SITE_MODE":                     "Lax",
		"OAUTH2_PKCE_ENFORCED_FOR_PUBLIC_CLIENTS":          "true",
		"OAUTH2_GRANT_REFRESH_TOKEN_ROTATION_GRACE_PERIOD": "0s",
		"OIDC_DYNAMIC_CLIENT_REGISTRATION_ENABLED":         "false",
		"STRATEGIES_ACCESS_TOKEN":                          "opaque",
	} {
		if got := literalEnvironment(t, hydraContainer, name); got != want {
			t.Fatalf("Hydra environment %s = %q, want %q", name, got, want)
		}
	}
	if literalEnvironmentOptional(t, hydraContainer, "OAUTH2_REFRESH_TOKEN_ROTATION_GRACE_PERIOD") != "" {
		t.Fatal("Hydra deployment uses the obsolete refresh-token grace-period environment path")
	}
	setupContainer := objectArrayFirst(t, objectField(t, objectField(t, objectField(t, hydraSetup[0], "spec"), "template"), "spec"), "containers")
	setupArgs := arrayField(t, setupContainer, "args")
	if len(setupArgs) != 2 {
		t.Fatalf("Hydra client setup args = %#v", setupArgs)
	}
	setupScript, _ := setupArgs[1].(string)
	for _, required := range []string{
		"--grant-type authorization_code", "--token-endpoint-auth-method none",
		"reconcile_client 'agentserver-platform'", "reconcile_client 'agentserver-browser'",
		"--redirect-uri 'https://agent.byted.bps.dev/'", "--redirect-uri 'https://browser.byted.bps.dev/'",
		"--audience " + corecontract.PlatformOAuthAudience, "--audience " + corecontract.BrowserOAuthAudience,
		"--access-token-strategy opaque",
	} {
		if !strings.Contains(setupScript, required) {
			t.Fatalf("Hydra browser client setup is missing %q", required)
		}
	}
	for _, scope := range append(corecontract.PlatformOAuthScopes(), corecontract.BrowserOAuthScopes()...) {
		if !strings.Contains(setupScript, "--scope "+scope) {
			t.Fatalf("Hydra user client setup is missing scope %q", scope)
		}
	}
	if strings.Contains(setupScript, "client-secret") || strings.Contains(setupScript, "refresh_token") {
		t.Fatal("Hydra browser client setup contains a client secret or refresh-token grant")
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
	wantInit := []string{"prepare-harness-directories", "install-network-guard"}
	if strings.Join(initNames, ",") != strings.Join(wantInit, ",") {
		t.Fatalf("harness init order = %v, want %v", initNames, wantInit)
	}
	runtimeJSON := mustBundleFile(t, bundle, runtimeFile)
	for _, forbidden := range []string{"materialize-", "material-source", "pool-source", "worker-source"} {
		if bytes.Contains(runtimeJSON, []byte(forbidden)) {
			t.Fatalf("runtime still contains removed materialization path %q", forbidden)
		}
	}
	assertSecretMaterialMounts(t, findResource(t, runtime, "Deployment", coreComponent), "material", loaded.Document.Secrets.Core,
		"/var/run/agentserver/material", groupReadableSecretMode,
		[]string{"ca.crt", "tls.crt", "tls.key", "run-capability.key", "run-capability-keyring.json", "executor-enrollment.key", "llm-gateway-sealing-keyring.json"})
	assertSecretMaterialMounts(t, findResource(t, runtime, "Deployment", platformComponent), "material", loaded.Document.Secrets.PlatformGateway,
		"/var/run/agentserver/material", groupReadableSecretMode, []string{"ca.crt", "tls.crt", "tls.key"})
	assertSecretMaterialMounts(t, findResource(t, runtime, "Deployment", browserComponent), "material", loaded.Document.Secrets.BrowserGateway,
		"/var/run/agentserver/material", groupReadableSecretMode, []string{"ca.crt", "tls.crt", "tls.key"})
	assertSecretMaterialMounts(t, findResource(t, runtime, "Deployment", executorComponent), "material", loaded.Document.Secrets.ExecutorGateway,
		"/var/run/agentserver/material", groupReadableSecretMode, []string{"ca.crt", "tls.crt", "tls.key", "run-capability-keyring.json"})
	assertSecretMaterialMounts(t, pool, "pool-material", loaded.Document.Secrets.HarnessPool,
		"/var/run/agentserver/pool", groupReadableSecretMode, []string{"ca.crt", "tls.crt", "tls.key", "run-manifest.key"})
	assertSecretMaterialMounts(t, pool, "worker-material", loaded.Document.Secrets.HarnessWorker,
		"/var/run/agentserver/worker", workerReadableSecretMode, []string{"ca.crt", "tls.crt", "tls.key", "run-manifest-keyring.json"})
	assertSecretMaterialMounts(t, findResource(t, runtime, "Deployment", llmproxyComponent), "material", loaded.Document.Secrets.LLMProxy,
		"/var/run/agentserver/material", groupReadableSecretMode, []string{"ca.crt", "tls.crt", "tls.key", "run-capability-keyring.json"})
	poolContainer := objectArrayFirst(t, poolSpec, "containers")
	poolSecurity := objectField(t, poolContainer, "securityContext")
	if numberField(t, poolSecurity, "runAsUser") != 0 || boolField(t, poolSecurity, "runAsNonRoot") {
		t.Fatalf("harness pool entrypoint must run as root with bounded capabilities: %v", poolSecurity)
	}
	capabilities := stringArray(t, objectField(t, poolSecurity, "capabilities"), "add")
	if strings.Join(capabilities, ",") != "CHOWN,SETUID,SETGID,DAC_OVERRIDE" {
		t.Fatalf("harness runtime capabilities = %v", capabilities)
	}
	if containsString(capabilities, "NET_ADMIN") || containsString(capabilities, "NET_RAW") {
		t.Fatal("harness runtime retained network administration capability")
	}

	if countKind(foundation, "NetworkPolicy") != 12 {
		t.Fatalf("foundation NetworkPolicy count = %d", countKind(foundation, "NetworkPolicy"))
	}
	if countKind(foundation, "HTTPRoute") != 6 {
		t.Fatalf("foundation HTTPRoute count = %d", countKind(foundation, "HTTPRoute"))
	}
	for _, resource := range foundation {
		if resource["kind"] == "Service" && stringField(t, objectField(t, resource, "spec"), "type") != "ClusterIP" {
			t.Fatalf("service %v is not ClusterIP", objectField(t, resource, "metadata")["name"])
		}
	}
	assertHTTPRoute(t, foundation, "agentserver-platform", ProductionFrontendHostname, platformComponent, PublicHTTPPort,
		[]string{"/", "/auth/config", "/auth/llm-gateway/callback", "/index.html", "/platform", "/readyz", "/v2"})
	assertHTTPRoute(t, foundation, "agentserver-browser", ProductionBrowserFrontendHostname, browserComponent, PublicHTTPPort,
		[]string{"/", "/auth/config", "/index.html", "/readyz", "/reference"})
	assertHTTPRoute(t, foundation, "agentserver-browser-api", ProductionBrowserHostname, browserComponent, PublicHTTPPort,
		[]string{"/v2"})
	assertHTTPRoute(t, foundation, "agentserver-executor-agentx", ProductionExecutorHostname, executorComponent, PublicHTTPPort,
		[]string{executorgateway.AgentxChallengePath, executorgateway.AgentxConnectPath, executorgateway.AgentxEnrollmentPath})
	assertHTTPRoute(t, foundation, "agentserver-auth-ui", ProductionHydraHostname, platformComponent, PublicHTTPPort,
		[]string{"/auth/hydra/consent", "/auth/hydra/login", "/auth/oidc/callback"})
	assertHTTPRoute(t, foundation, "agentserver-hydra-public", ProductionHydraHostname, hydraComponent, HydraPublicPort,
		[]string{"/"})
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
	for _, name := range []string{"hydra-migrate-egress", "agentserver-migrate-egress", "agentserver-bootstrap-egress", coreComponent, hydraComponent} {
		assertCNPGDatabaseEgress(t, findResource(t, foundation, "NetworkPolicy", name))
	}

	for _, resources := range [][]map[string]any{foundation, hydraMigration, migration, hydraSetup, bootstrap, runtime} {
		for _, resource := range resources {
			if resource["kind"] != "Deployment" && resource["kind"] != "Job" {
				continue
			}
			assertPinnedImages(t, resource)
			assertProductionNodeSelector(t, resource, "amd64")
			assertNoImagePullSecrets(t, resource)
		}
	}
	all := append([]byte(nil), mustBundleFile(t, bundle, foundationFile)...)
	for _, name := range []string{hydraMigrationFile, migrationFile, hydraSetupFile, bootstrapFile, runtimeFile} {
		all = append(all, mustBundleFile(t, bundle, name)...)
	}
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
	for _, file := range []string{hydraMigrationFile, migrationFile, hydraSetupFile, bootstrapFile, runtimeFile} {
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

func assertSecretMaterialMounts(
	t *testing.T,
	deployment map[string]any,
	volumeName, secretName, destination string,
	mode int,
	files []string,
) {
	t.Helper()
	podSpec := objectField(t, objectField(t, objectField(t, deployment, "spec"), "template"), "spec")
	var volume map[string]any
	for _, raw := range arrayField(t, podSpec, "volumes") {
		candidate, ok := raw.(map[string]any)
		if ok && candidate["name"] == volumeName {
			volume = candidate
			break
		}
	}
	if volume == nil {
		t.Fatalf("Secret material volume %s not found", volumeName)
	}
	secret := objectField(t, volume, "secret")
	if stringField(t, secret, "secretName") != secretName || int(numberField(t, secret, "defaultMode")) != mode {
		t.Fatalf("Secret material volume %s = %#v", volumeName, secret)
	}
	items := arrayField(t, secret, "items")
	if len(items) != len(files) {
		t.Fatalf("Secret material volume %s item count = %d, want %d", volumeName, len(items), len(files))
	}
	for index, file := range files {
		item, ok := items[index].(map[string]any)
		if !ok || item["key"] != file || item["path"] != file || int(numberField(t, item, "mode")) != mode {
			t.Fatalf("Secret material volume %s item %d = %#v", volumeName, index, items[index])
		}
	}
	container := objectArrayFirst(t, podSpec, "containers")
	wanted := make(map[string]struct{}, len(files))
	for _, file := range files {
		wanted[file] = struct{}{}
	}
	seen := make(map[string]struct{}, len(files))
	for _, raw := range arrayField(t, container, "volumeMounts") {
		mount, ok := raw.(map[string]any)
		if !ok || mount["name"] != volumeName {
			continue
		}
		subPath, _ := mount["subPath"].(string)
		if _, found := wanted[subPath]; !found || mount["mountPath"] != destination+"/"+subPath || mount["readOnly"] != true {
			t.Fatalf("Secret material mount %s/%s = %#v", volumeName, subPath, mount)
		}
		seen[subPath] = struct{}{}
	}
	if len(seen) != len(wanted) {
		t.Fatalf("Secret material mounts %s = %v, want %v", volumeName, seen, wanted)
	}
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

func boolField(t *testing.T, object map[string]any, name string) bool {
	t.Helper()
	value, ok := object[name].(bool)
	if !ok {
		t.Fatalf("field %s is not a boolean", name)
	}
	return value
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

func literalEnvironment(t *testing.T, container map[string]any, name string) string {
	t.Helper()
	for _, raw := range arrayField(t, container, "env") {
		environment, ok := raw.(map[string]any)
		if ok && environment["name"] == name {
			value, _ := environment["value"].(string)
			return value
		}
	}
	t.Fatalf("environment %s not found", name)
	return ""
}

func literalEnvironmentOptional(t *testing.T, container map[string]any, name string) string {
	t.Helper()
	for _, raw := range arrayField(t, container, "env") {
		environment, ok := raw.(map[string]any)
		if ok && environment["name"] == name {
			value, _ := environment["value"].(string)
			return value
		}
	}
	return ""
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
