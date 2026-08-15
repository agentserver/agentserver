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
	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/executorgateway"
	"github.com/agentserver/agentserver/v2/internal/sandboxgatewayapp"
	"github.com/agentserver/agentserver/v2/internal/taeimage"
	"github.com/agentserver/agentserver/v2/internal/taepolicy"
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
	if len(first.Files) != 8 || len(second.Files) != len(first.Files) {
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

func TestRenderContainsNoDeploymentWideManagedCredentialMode(t *testing.T) {
	loaded, err := ValidateConfig(validConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Render(loaded)
	if err != nil {
		t.Fatal(err)
	}
	chart, err := RenderHelmChart(loaded)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range append(append([]RenderedFile(nil), bundle.Files...), chart.Files...) {
		for _, forbidden := range [][]byte{
			[]byte("AGENTSERVER_V2_MANAGED_LARK_CREDENTIAL_MODE"),
			[]byte("managedLarkCredentialMode"),
			[]byte(`"credentialMode"`),
		} {
			if bytes.Contains(file.Content, forbidden) {
				t.Fatalf("rendered deployment file %s contains deployment-wide mode %q", file.Name, forbidden)
			}
		}
	}
}

func TestRenderManagedExecutorKillSwitchOmitsManagedRuntimeAndAuthorities(t *testing.T) {
	document := validConfigDocument()
	document.Managed.Enabled = false
	document.Managed.Stage = ManagedExecutorStageDisabled
	loaded, err := ValidateConfig(document)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Render(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Files) != 7 {
		t.Fatalf("disabled managed executor bundle files = %d, want 7", len(bundle.Files))
	}
	for _, name := range []string{managedEnvironmentBootstrapFile} {
		if _, found := bundle.File(name); found {
			t.Fatalf("disabled managed executor rendered %s", name)
		}
	}
	foundation := parseKubernetesList(t, mustBundleFile(t, bundle, foundationFile))
	runtime := parseKubernetesList(t, mustBundleFile(t, bundle, runtimeFile))
	for _, component := range []string{sandboxComponent, egressComponent} {
		if findResourceOptional(foundation, "Service", component) != nil ||
			findResourceOptional(runtime, "Deployment", component) != nil ||
			findResourceOptional(foundation, "NetworkPolicy", component) != nil {
			t.Fatalf("disabled managed executor rendered component %s", component)
		}
	}
	var workerConfig string
	for _, resource := range foundation {
		if resource["kind"] != "ConfigMap" {
			continue
		}
		if value, found := objectField(t, resource, "data")["worker-deployment.json"].(string); found {
			workerConfig = value
		}
	}
	var workerDocument workerDeploymentJSON
	if workerConfig == "" {
		t.Fatal("disabled managed executor omitted the base worker deployment document")
	}
	if err := json.Unmarshal([]byte(workerConfig), &workerDocument); err != nil {
		t.Fatal(err)
	}
	if workerDocument.Version != 2 || workerDocument.ManagedSkill != nil {
		t.Fatalf("disabled managed executor retained worker skill authority: %+v", workerDocument.ManagedSkill)
	}
	core := findResource(t, runtime, "Deployment", coreComponent)
	coreContainer := objectArrayFirst(t, objectField(t, objectField(t, objectField(t, core, "spec"), "template"), "spec"), "containers")
	if got := literalEnvironment(t, coreContainer, "AGENTSERVER_V2_MANAGED_EXECUTOR_ENABLED"); got != "false" {
		t.Fatalf("Core managed kill switch = %q", got)
	}
	for _, forbidden := range []string{
		"AGENTSERVER_V2_SANDBOX_GATEWAY_SPIFFE_ID", "AGENTSERVER_V2_SANDBOX_GATEWAY_SPIFFE_IDS",
		"AGENTSERVER_V2_EGRESS_AUTHORIZER_SPIFFE_ID",
		"AGENTSERVER_V2_LARK_CLIENT_ID", "AGENTSERVER_V2_LARK_CLIENT_SECRET_FILE", "AGENTSERVER_V2_LARK_REFRESH_WORKER_ID",
	} {
		if literalEnvironmentOptional(t, coreContainer, forbidden) != "" {
			t.Fatalf("disabled managed executor retained Core environment %s", forbidden)
		}
	}
	harness := findResource(t, runtime, "Deployment", harnessComponent)
	harnessContainer := objectArrayFirst(t, objectField(t, objectField(t, objectField(t, harness, "spec"), "template"), "spec"), "containers")
	for _, forbidden := range []string{
		"AGENTSERVER_V2_MANAGED_ENVIRONMENT_ID", "AGENTSERVER_V2_MANAGED_RUNTIME_PROFILE_SHA256",
		"AGENTSERVER_V2_MANAGED_PACK_SET_SHA256", "AGENTSERVER_V2_MANAGED_SKILL_SHA256",
		"AGENTSERVER_V2_SANDBOX_GATEWAY_URL", "AGENTSERVER_V2_SANDBOX_LIFECYCLE_CAPABILITY_SIGNING_KEY_FILE",
	} {
		if literalEnvironmentOptional(t, harnessContainer, forbidden) != "" {
			t.Fatalf("disabled managed executor retained harness environment %s", forbidden)
		}
	}
	executor := findResource(t, runtime, "Deployment", executorComponent)
	executorContainer := objectArrayFirst(t, objectField(t, objectField(t, objectField(t, executor, "spec"), "template"), "spec"), "containers")
	if literalEnvironmentOptional(t, executorContainer, "AGENTSERVER_V2_MANAGED_LARK_CLIENT_ID") != "" {
		t.Fatal("disabled managed executor retained the Lark client ID projection")
	}
	chart, err := RenderHelmChart(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(chart.Files) != len(requiredHelmBaseChartFiles) {
		t.Fatalf("disabled managed executor Helm file count = %d, want %d", len(chart.Files), len(requiredHelmBaseChartFiles))
	}
}

func TestRenderPolicyBootstrapExposesOnlyDenyWebhook(t *testing.T) {
	loaded, err := ValidateConfig(policyBootstrapConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Render(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Files) != 7 {
		t.Fatalf("policy bootstrap bundle files = %d, want 7", len(bundle.Files))
	}
	if _, found := bundle.File(managedEnvironmentBootstrapFile); found {
		t.Fatal("policy bootstrap rendered the managed environment authority")
	}
	foundation := parseKubernetesList(t, mustBundleFile(t, bundle, foundationFile))
	runtime := parseKubernetesList(t, mustBundleFile(t, bundle, runtimeFile))
	if findResourceOptional(foundation, "Service", sandboxComponent) != nil ||
		findResourceOptional(runtime, "Deployment", sandboxComponent) != nil {
		t.Fatal("policy bootstrap rendered a sandbox provider")
	}
	findResource(t, foundation, "Service", egressComponent)
	findResource(t, foundation, "HTTPRoute", "agentserver-egress-authorizer-webhook")
	egress := findResource(t, runtime, "Deployment", egressComponent)
	podSpec := objectField(t, objectField(t, objectField(t, egress, "spec"), "template"), "spec")
	container := objectArrayFirst(t, podSpec, "containers")
	args := stringArray(t, container, "args")
	if !slices.Equal(args, []string{"serve", "--policy-bootstrap"}) {
		t.Fatalf("policy bootstrap args = %v", args)
	}
	for _, forbidden := range []string{
		"AGENTSERVER_V2_CORE_URL", "AGENTSERVER_V2_EGRESS_PLACEHOLDER_KEYRING_FILE",
		"AGENTSERVER_V2_EGRESS_ALLOWED_TAE_PSM", "AGENTSERVER_V2_TAE_POLICY_BINDING_SHA256",
	} {
		if literalEnvironmentOptional(t, container, forbidden) != "" {
			t.Fatalf("policy bootstrap received forbidden authority %s", forbidden)
		}
	}
	assertSecretMaterialMounts(t, egress, "material", ProductionEgressSecret,
		"/var/run/agentserver/material", groupReadableSecretMode, []string{"tls.crt", "tls.key"})
	policy := findResource(t, foundation, "NetworkPolicy", egressComponent)
	policySpec := objectField(t, policy, "spec")
	if _, found := policySpec["egress"]; found {
		t.Fatal("policy bootstrap has outbound network authority")
	}
	core := findResource(t, runtime, "Deployment", coreComponent)
	coreContainer := objectArrayFirst(t, objectField(t, objectField(t, objectField(t, core, "spec"), "template"), "spec"), "containers")
	if literalEnvironment(t, coreContainer, "AGENTSERVER_V2_MANAGED_EXECUTOR_ENABLED") != "false" {
		t.Fatal("policy bootstrap activated managed execution in Core")
	}
	if got := literalEnvironment(t, coreContainer, "AGENTSERVER_V2_CREDENTIAL_SEALING_KEYRING_FILE"); got != serviceMaterialPath("credential-sealing-keyring.json") {
		t.Fatalf("policy bootstrap credential sealing keyring = %q", got)
	}
	if got := literalEnvironment(t, coreContainer, "AGENTSERVER_V2_LARK_DEVICE_SCOPES"); got != corecredentials.DefaultManagedLarkScopes {
		t.Fatalf("policy bootstrap Lark device scopes = %q", got)
	}
	if got := literalEnvironment(t, coreContainer, "AGENTSERVER_V2_BYTECLOUD_DEVICE_API_BASE_URL"); got != corecredentials.DefaultByteCloudDeviceAPIBaseURL {
		t.Fatalf("policy bootstrap ByteCloud device API = %q", got)
	}
	assertSecretEnvironment(t, coreContainer, "AGENTSERVER_V2_LARK_DEVICE_APP_ID", loaded.Document.Secrets.Core, "lark-device-app-id")
	assertSecretEnvironment(t, coreContainer, "AGENTSERVER_V2_LARK_DEVICE_APP_SECRET", loaded.Document.Secrets.Core, "lark-device-app-secret")
	assertSecretMaterialMounts(t, core, "material", loaded.Document.Secrets.Core,
		"/var/run/agentserver/material", groupReadableSecretMode,
		[]string{"ca.crt", "tls.crt", "tls.key", "run-capability.key", "run-capability-keyring.json", "executor-enrollment.key", "llm-gateway-sealing-keyring.json", "credential-sealing-keyring.json"})
}

func TestRenderDirectManagedExecutorOmitsWebhookWorkloadAndAuthority(t *testing.T) {
	loaded, err := ValidateConfig(directConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Render(loaded)
	if err != nil {
		t.Fatal(err)
	}
	foundation := parseKubernetesList(t, mustBundleFile(t, bundle, foundationFile))
	runtime := parseKubernetesList(t, mustBundleFile(t, bundle, runtimeFile))
	if findResourceOptional(foundation, "Service", egressComponent) != nil ||
		findResourceOptional(foundation, "HTTPRoute", "agentserver-egress-authorizer-webhook") != nil ||
		findResourceOptional(foundation, "BackendTLSPolicy", "agentserver-egress-authorizer-backend-tls") != nil ||
		findResourceOptional(foundation, "NetworkPolicy", egressComponent) != nil ||
		findResourceOptional(runtime, "Deployment", egressComponent) != nil {
		t.Fatal("direct managed executor rendered egress-authorizer authority")
	}
	sandbox := findResource(t, runtime, "Deployment", sandboxComponent)
	container := objectArrayFirst(t, objectField(t, objectField(t, objectField(t, sandbox, "spec"), "template"), "spec"), "containers")
	environment := literalEnvironmentLookup(t, container)
	if environment("AGENTSERVER_V2_TAE_POLICY_HOST") != taepolicy.SystemDefaultHost ||
		environment("AGENTSERVER_V2_TAE_POLICY_ACCESS") != taepolicy.SystemDefaultAccess ||
		environment("AGENTSERVER_V2_TAE_POLICY_WEBHOOK_REQUIRED") != "false" {
		t.Fatalf("direct sandbox policy = host=%q access=%q webhook=%q",
			environment("AGENTSERVER_V2_TAE_POLICY_HOST"), environment("AGENTSERVER_V2_TAE_POLICY_ACCESS"),
			environment("AGENTSERVER_V2_TAE_POLICY_WEBHOOK_REQUIRED"))
	}
	for _, name := range []string{
		"AGENTSERVER_V2_TAE_WEBHOOK_MODE", "AGENTSERVER_V2_TAE_WEBHOOK_PSM",
		"AGENTSERVER_V2_TAE_WEBHOOK_URL", "AGENTSERVER_V2_TAE_WEBHOOK_PATH",
	} {
		if literalEnvironmentOptional(t, container, name) != "" {
			t.Fatalf("direct sandbox rendered webhook environment %s", name)
		}
	}
	if _, err := sandboxgatewayapp.LoadProductionConfig(environment); err != nil {
		t.Fatalf("sandbox-gateway rejected rendered direct policy: %v", err)
	}
	core := findResource(t, runtime, "Deployment", coreComponent)
	coreContainer := objectArrayFirst(t, objectField(t, objectField(t, objectField(t, core, "spec"), "template"), "spec"), "containers")
	if literalEnvironmentOptional(t, coreContainer, "AGENTSERVER_V2_EGRESS_AUTHORIZER_SPIFFE_ID") != "" {
		t.Fatal("direct Core retained the egress-authorizer workload identity")
	}
	if literalEnvironmentOptional(t, coreContainer, "AGENTSERVER_V2_EGRESS_PLACEHOLDER_KEYRING_FILE") != "" {
		t.Fatal("direct Core retained the webhook placeholder verifier")
	}
	executor := findResource(t, runtime, "Deployment", executorComponent)
	executorContainer := objectArrayFirst(t, objectField(t, objectField(t, objectField(t, executor, "spec"), "template"), "spec"), "containers")
	if literalEnvironment(t, executorContainer, "AGENTSERVER_V2_TAE_POLICY_WEBHOOK_REQUIRED") != "false" {
		t.Fatal("direct executor did not receive the direct profile lock")
	}
	for _, name := range []string{
		"AGENTSERVER_V2_EGRESS_PLACEHOLDER_ISSUER", "AGENTSERVER_V2_EGRESS_PLACEHOLDER_KEY_ID",
		"AGENTSERVER_V2_EGRESS_PLACEHOLDER_SIGNING_KEY_FILE",
	} {
		if literalEnvironmentOptional(t, executorContainer, name) != "" {
			t.Fatalf("direct executor retained placeholder authority %s", name)
		}
	}
}

func TestRenderDirectPolicyBootstrapOmitsWebhookWorkloadAndAuthority(t *testing.T) {
	loaded, err := ValidateConfig(directPolicyBootstrapConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Render(loaded)
	if err != nil {
		t.Fatal(err)
	}
	foundation := parseKubernetesList(t, mustBundleFile(t, bundle, foundationFile))
	runtime := parseKubernetesList(t, mustBundleFile(t, bundle, runtimeFile))
	if findResourceOptional(foundation, "Service", egressComponent) != nil ||
		findResourceOptional(foundation, "HTTPRoute", "agentserver-egress-authorizer-webhook") != nil ||
		findResourceOptional(foundation, "BackendTLSPolicy", "agentserver-egress-authorizer-backend-tls") != nil ||
		findResourceOptional(foundation, "NetworkPolicy", egressComponent) != nil ||
		findResourceOptional(runtime, "Deployment", egressComponent) != nil {
		t.Fatal("direct policy bootstrap rendered egress-authorizer authority")
	}
	if findResourceOptional(foundation, "Service", sandboxComponent) != nil ||
		findResourceOptional(runtime, "Deployment", sandboxComponent) != nil {
		t.Fatal("direct policy bootstrap activated the sandbox provider")
	}
	core := findResource(t, runtime, "Deployment", coreComponent)
	coreContainer := objectArrayFirst(t, objectField(t, objectField(t, objectField(t, core, "spec"), "template"), "spec"), "containers")
	for _, name := range []string{
		"AGENTSERVER_V2_EGRESS_AUTHORIZER_SPIFFE_ID",
		"AGENTSERVER_V2_EGRESS_PLACEHOLDER_KEYRING_FILE",
	} {
		if literalEnvironmentOptional(t, coreContainer, name) != "" {
			t.Fatalf("direct policy bootstrap retained webhook authority %s", name)
		}
	}
}

func TestRenderRejectsPartialManagedToolPack(t *testing.T) {
	document := validConfigDocument()
	document.Managed.Lark.Enabled = false
	document.Managed.Environment.RuntimeProfileSHA256 = managedRuntimeProfileDigest(document, document.Managed)
	document.Managed.Environment.PackSetSHA256 = managedPackSetDigest(document.Managed)
	if _, err := ValidateConfig(document); err == nil || !strings.Contains(err.Error(), "requires both the pinned lark and bkectl") {
		t.Fatalf("partial managed tool pack error = %v", err)
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
	managedEnvironmentBootstrap := parseKubernetesList(t, mustBundleFile(t, bundle, managedEnvironmentBootstrapFile))

	if len(migration) != 1 || migration[0]["kind"] != "Job" || len(hydraMigration) != 1 || hydraMigration[0]["kind"] != "Job" ||
		len(hydraSetup) != 1 || hydraSetup[0]["kind"] != "Job" || len(bootstrap) != 1 || bootstrap[0]["kind"] != "Job" ||
		len(managedEnvironmentBootstrap) != 1 || managedEnvironmentBootstrap[0]["kind"] != "Job" {
		t.Fatal("Hydra, base bootstrap, and managed environment stages must each contain exactly one Kubernetes Job")
	}
	if countKind(runtime, "Job") != 0 || countKind(runtime, "HorizontalPodAutoscaler") != 0 {
		t.Fatal("runtime stage contains a per-run Job or HPA")
	}
	if countKind(runtime, "Deployment") != 9 || countKind(runtime, "PodDisruptionBudget") != 8 {
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
		[]string{"ca.crt", "tls.crt", "tls.key", "run-capability.key", "run-capability-keyring.json", "executor-enrollment.key", "llm-gateway-sealing-keyring.json", "credential-sealing-keyring.json", "egress-placeholder-keyring.json"})
	assertSecretMaterialMounts(t, findResource(t, runtime, "Deployment", platformComponent), "material", loaded.Document.Secrets.PlatformGateway,
		"/var/run/agentserver/material", groupReadableSecretMode, []string{"ca.crt", "tls.crt", "tls.key"})
	assertSecretMaterialMounts(t, findResource(t, runtime, "Deployment", browserComponent), "material", loaded.Document.Secrets.BrowserGateway,
		"/var/run/agentserver/material", groupReadableSecretMode, []string{"ca.crt", "tls.crt", "tls.key"})
	assertSecretMaterialMounts(t, findResource(t, runtime, "Deployment", executorComponent), "material", loaded.Document.Secrets.ExecutorGateway,
		"/var/run/agentserver/material", groupReadableSecretMode, []string{
			"ca.crt", "tls.crt", "tls.key", "run-capability-keyring.json", "sandbox-backend-capability.key",
			"sandbox-fencer-capability.key", "egress-placeholder.key",
		})
	assertSecretMaterialMounts(t, pool, "pool-material", loaded.Document.Secrets.HarnessPool,
		"/var/run/agentserver/pool", groupReadableSecretMode, []string{"ca.crt", "tls.crt", "tls.key", "run-manifest.key"})
	assertSecretMaterialMounts(t, pool, "worker-material", loaded.Document.Secrets.HarnessWorker,
		"/var/run/agentserver/worker", workerReadableSecretMode, []string{"ca.crt", "tls.crt", "tls.key", "run-manifest-keyring.json"})
	assertSecretMaterialMounts(t, findResource(t, runtime, "Deployment", llmproxyComponent), "material", loaded.Document.Secrets.LLMProxy,
		"/var/run/agentserver/material", groupReadableSecretMode, []string{"ca.crt", "tls.crt", "tls.key", "run-capability-keyring.json"})
	assertSecretMaterialMounts(t, findResource(t, runtime, "Deployment", sandboxComponent), "material", loaded.Document.Secrets.SandboxGateway,
		"/var/run/agentserver/material", groupReadableSecretMode, []string{
			"ca.crt", "tls.crt", "tls.key", "sandbox-capability-keyring.json",
			"bytecloud-access-key-id", "bytecloud-secret-access-key",
		})
	assertSecretMaterialMounts(t, findResource(t, runtime, "Deployment", egressComponent), "material", loaded.Document.Secrets.EgressAuthorizer,
		"/var/run/agentserver/material", groupReadableSecretMode, []string{"ca.crt", "tls.crt", "tls.key", "egress-placeholder-keyring.json"})
	sandboxDeployment := findResource(t, runtime, "Deployment", sandboxComponent)
	sandboxContainer := objectArrayFirst(t, objectField(t, objectField(t, objectField(t, sandboxDeployment, "spec"), "template"), "spec"), "containers")
	sandboxEnvironment := literalEnvironmentLookup(t, sandboxContainer)
	sandboxConfig, err := sandboxgatewayapp.LoadProductionConfig(sandboxEnvironment)
	if err != nil {
		t.Fatalf("provider-linked sandbox-gateway rejected rendered production environment: %v", err)
	}
	wantTAEImage, err := taeimage.ContentTagForRepository(ProductionTAEManagedSandboxImage, loaded.Document.Images.ManagedSandbox)
	if err != nil {
		t.Fatal(err)
	}
	if sandboxConfig.ProviderRegion != loaded.Document.Managed.TAE.Region || sandboxConfig.ProviderPSM != loaded.Document.Managed.TAE.PSM ||
		len(sandboxConfig.WorkspaceAllowlist) != len(loaded.Document.Managed.WorkspaceAllowlist) ||
		sandboxEnvironment("AGENTSERVER_V2_TAE_SANDBOX_IMAGE") != wantTAEImage ||
		sandboxEnvironment("AGENTSERVER_V2_TAE_SANDBOX_ID") != loaded.Document.Managed.TAE.SandboxID ||
		sandboxEnvironment("AGENTSERVER_V2_TAE_SANDBOX_REVISION_ID") != loaded.Document.Managed.TAE.RevisionID {
		t.Fatalf("rendered sandbox-gateway authority = %+v", sandboxConfig)
	}
	for name, want := range map[string]string{
		"AGENTSERVER_V2_TAE_AUTH_MODE":                        "bytecloud-app-aksk-v1",
		"AGENTSERVER_V2_TAE_BYTECLOUD_SITE":                   "i18n-tt",
		"AGENTSERVER_V2_TAE_BYTECLOUD_JWT_ENDPOINT":           ProductionByteCloudJWTEndpoint,
		"AGENTSERVER_V2_TAE_PROXY_URL":                        ProductionTAEProxyURL,
		"AGENTSERVER_V2_TAE_CONTROL_PLANE_URL":                loaded.Document.Managed.TAE.ControlPlaneURL,
		"AGENTSERVER_V2_TAE_DATA_PLANE_SUFFIX":                loaded.Document.Managed.TAE.DataPlaneSuffix,
		"AGENTSERVER_V2_TAE_BYTECLOUD_ACCESS_KEY_ID_FILE":     "/var/run/agentserver/material/bytecloud-access-key-id",
		"AGENTSERVER_V2_TAE_BYTECLOUD_SECRET_ACCESS_KEY_FILE": "/var/run/agentserver/material/bytecloud-secret-access-key",
		"AGENTSERVER_V2_TAE_BYTECLOUD_JWT_TIMEOUT":            "5s",
	} {
		if got := sandboxEnvironment(name); got != want {
			t.Fatalf("sandbox-gateway %s = %q, want %q", name, got, want)
		}
	}
	for _, component := range []string{coreComponent, executorComponent, harnessComponent, egressComponent} {
		deployment := findResource(t, runtime, "Deployment", component)
		container := objectArrayFirst(t, objectField(t, objectField(t, objectField(t, deployment, "spec"), "template"), "spec"), "containers")
		if literalEnvironmentOptional(t, container, "AGENTSERVER_V2_TAE_BYTECLOUD_SECRET_ACCESS_KEY_FILE") != "" {
			t.Fatalf("ByteCloud secret key projected into non-sandbox component %s", component)
		}
	}
	egressDeployment := findResource(t, runtime, "Deployment", egressComponent)
	egressContainer := objectArrayFirst(t, objectField(t, objectField(t, objectField(t, egressDeployment, "spec"), "template"), "spec"), "containers")
	egressEnvironment := literalEnvironmentLookup(t, egressContainer)
	assertRenderedTAEPolicyCatalog(t, sandboxEnvironment,
		egressEnvironment("AGENTSERVER_V2_TAE_POLICY_BINDINGS"), loaded)
	wantWebhookPSM := loaded.Document.Managed.TAE.Policy.WebhookPSM
	wantWebhookURL := loaded.Document.Managed.TAE.Policy.WebhookURL
	if sandboxEnvironment("AGENTSERVER_V2_TAE_WEBHOOK_PSM") != wantWebhookPSM ||
		sandboxEnvironment("AGENTSERVER_V2_TAE_WEBHOOK_URL") != wantWebhookURL {
		t.Fatalf("webhook authority was not rendered as an exclusive value: psm=%q/%q url=%q/%q",
			sandboxEnvironment("AGENTSERVER_V2_TAE_WEBHOOK_PSM"), wantWebhookPSM,
			sandboxEnvironment("AGENTSERVER_V2_TAE_WEBHOOK_URL"), wantWebhookURL)
	}
	assertManagedNetworkPolicyShape(t, foundation, loaded.Document)
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

	if countKind(foundation, "NetworkPolicy") != 15 {
		t.Fatalf("foundation NetworkPolicy count = %d", countKind(foundation, "NetworkPolicy"))
	}
	if countKind(foundation, "HTTPRoute") != 7 {
		t.Fatalf("foundation HTTPRoute count = %d", countKind(foundation, "HTTPRoute"))
	}
	for _, resource := range foundation {
		if resource["kind"] == "Service" && stringField(t, objectField(t, resource, "spec"), "type") != "ClusterIP" {
			t.Fatalf("service %v is not ClusterIP", objectField(t, resource, "metadata")["name"])
		}
	}
	assertHTTPRoute(t, foundation, "agentserver-platform", ProductionFrontendHostname, platformComponent, PublicHTTPPort,
		[]string{"/", "/assets", "/auth/config", "/auth/llm-gateway/callback", "/index.html", "/readyz", "/v2", "/workspaces"})
	assertHTTPRoute(t, foundation, "agentserver-browser", ProductionBrowserFrontendHostname, browserComponent, PublicHTTPPort,
		[]string{"/", "/assets", "/auth/config", "/index.html", "/readyz", "/workspaces"})
	assertHTTPRoute(t, foundation, "agentserver-browser-api", ProductionBrowserHostname, browserComponent, PublicHTTPPort,
		[]string{"/v2"})
	assertHTTPRoute(t, foundation, "agentserver-executor-agentx", ProductionExecutorHostname, executorComponent, PublicHTTPPort,
		[]string{executorgateway.AgentxChallengePath, executorgateway.AgentxConnectPath, executorgateway.AgentxEnrollmentPath})
	assertHTTPRoute(t, foundation, "agentserver-auth-ui", ProductionHydraHostname, platformComponent, PublicHTTPPort,
		[]string{"/auth/hydra/consent", "/auth/hydra/login", "/auth/oidc/callback"})
	assertHTTPRoute(t, foundation, "agentserver-hydra-public", ProductionHydraHostname, hydraComponent, HydraPublicPort,
		[]string{"/"})
	assertHTTPRoute(t, foundation, "agentserver-egress-authorizer-webhook", ProductionEgressAuthorizerHostname, egressComponent,
		loaded.Document.Services.EgressAuthorizer.Port, []string{"/v1/policy"})
	backendTLS := findResource(t, foundation, "BackendTLSPolicy", "agentserver-egress-authorizer-backend-tls")
	backendTLSSpec := objectField(t, backendTLS, "spec")
	targetRef := objectArrayFirst(t, backendTLSSpec, "targetRefs")
	if targetRef["kind"] != "Service" || targetRef["name"] != egressComponent || targetRef["sectionName"] != "https" {
		t.Fatalf("egress BackendTLSPolicy target = %#v", targetRef)
	}
	validation := objectField(t, backendTLSSpec, "validation")
	if stringField(t, validation, "hostname") != EgressInternalHost {
		t.Fatalf("egress BackendTLSPolicy hostname = %#v", validation)
	}
	caRef := objectArrayFirst(t, validation, "caCertificateRefs")
	if caRef["kind"] != "ConfigMap" || caRef["name"] != ProductionEgressBackendCAConfigMap {
		t.Fatalf("egress BackendTLSPolicy CA ref = %#v", caRef)
	}
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
	coreNetworkPolicy := findResource(t, foundation, "NetworkPolicy", coreComponent)
	assertNamespacedPodPeerPresent(t, objectField(t, coreNetworkPolicy, "spec"), "egress",
		loaded.Document.Ingress.GatewayNamespace, loaded.Document.Ingress.GatewayPodSelector, 443)
	harnessNetworkPolicy := findResource(t, foundation, "NetworkPolicy", harnessComponent)
	assertNamespacedPodPeerPresent(t, objectField(t, harnessNetworkPolicy, "spec"), "egress",
		loaded.Document.Ingress.GatewayNamespace, loaded.Document.Ingress.GatewayPodSelector, 443)
	for _, name := range []string{
		"hydra-migrate-egress", "agentserver-migrate-egress", "agentserver-bootstrap-egress",
		"agentserver-managed-environment-bootstrap-egress",
		coreComponent, hydraComponent,
	} {
		assertCNPGDatabaseEgress(t, findResource(t, foundation, "NetworkPolicy", name))
	}

	for _, resources := range [][]map[string]any{
		foundation, hydraMigration, migration, hydraSetup, bootstrap, managedEnvironmentBootstrap, runtime,
	} {
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
	for _, name := range []string{
		hydraMigrationFile, migrationFile, hydraSetupFile, bootstrapFile,
		managedEnvironmentBootstrapFile, runtimeFile,
	} {
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
	for _, file := range []string{
		hydraMigrationFile, migrationFile, hydraSetupFile, bootstrapFile,
		managedEnvironmentBootstrapFile, runtimeFile,
	} {
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
	if bootstrapDocument.ExecutorID != loaded.Document.Bootstrap.ExecutorID ||
		bootstrapDocument.WorkspaceID != loaded.Document.Bootstrap.WorkspaceID ||
		bootstrapDocument.SessionID != loaded.Document.Bootstrap.SessionID ||
		bootstrapDocument.UserID != loaded.Document.Bootstrap.OwnerUserID {
		t.Fatalf("bootstrap document = %+v", bootstrapDocument)
	}
	var workerDocument workerDeploymentJSON
	if err := json.Unmarshal([]byte(workerConfig), &workerDocument); err != nil {
		t.Fatal(err)
	}
	if workerDocument.WorkerUID != WorkerUID || workerDocument.AppUID != AppUID || workerDocument.CodexConfigProfile != CodexConfigProfile() ||
		workerDocument.RunManifestKeyringFile != workerMaterialPath("run-manifest-keyring.json") ||
		workerDocument.Version != 2 || workerDocument.ManagedSkill == nil ||
		workerDocument.ManagedSkill.Path != managedBaseInstructionsPath ||
		workerDocument.ManagedSkill.SHA256 != loaded.Document.Managed.BaseInstructionsSHA256 {
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

func literalEnvironmentLookup(t *testing.T, container map[string]any) func(string) string {
	t.Helper()
	values := make(map[string]string)
	for _, raw := range arrayField(t, container, "env") {
		environment, ok := raw.(map[string]any)
		if !ok {
			t.Fatal("container environment entry is not an object")
		}
		name, _ := environment["name"].(string)
		value, literal := environment["value"].(string)
		if name == "" || !literal {
			t.Fatalf("environment %q is not a literal value", name)
		}
		if _, duplicate := values[name]; duplicate {
			t.Fatalf("environment %s is duplicated", name)
		}
		values[name] = value
	}
	return func(name string) string { return values[name] }
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

func assertRenderedTAEPolicyCatalog(t *testing.T, sandbox func(string) string, raw string, config LoadedConfig) {
	t.Helper()
	var catalog struct {
		Bindings []taepolicy.Binding `json:"bindings"`
	}
	if err := json.Unmarshal([]byte(raw), &catalog); err != nil {
		t.Fatalf("decode egress-authorizer TAE policy catalog: %v", err)
	}
	if len(catalog.Bindings) != len(config.ManagedSandboxProfiles) {
		t.Fatalf("egress-authorizer TAE policy bindings = %d, want %d", len(catalog.Bindings), len(config.ManagedSandboxProfiles))
	}
	for index, profile := range config.ManagedSandboxProfiles {
		want := managedTAEPolicyBinding(profile.Document.TAE)
		if catalog.Bindings[index] != want {
			t.Fatalf("egress-authorizer TAE policy binding %d = %+v, want %+v", index, catalog.Bindings[index], want)
		}
	}
	defaultBinding := managedTAEPolicyBinding(config.Document.Managed.TAE)
	for name, want := range map[string]string{
		"AGENTSERVER_V2_TAE_POLICY_REVISION":       defaultBinding.Revision,
		"AGENTSERVER_V2_TAE_POLICY_SHA256":         defaultBinding.PolicySHA256,
		"AGENTSERVER_V2_TAE_POLICY_BINDING_SHA256": defaultBinding.BindingSHA256,
		"AGENTSERVER_V2_TAE_POLICY_HOST":           defaultBinding.PublicHost,
		"AGENTSERVER_V2_TAE_POLICY_ACCESS":         defaultBinding.PublicAccess,
		"AGENTSERVER_V2_TAE_WEBHOOK_MODE":          defaultBinding.WebhookMode,
		"AGENTSERVER_V2_TAE_WEBHOOK_PSM":           defaultBinding.WebhookPSM,
		"AGENTSERVER_V2_TAE_WEBHOOK_URL":           defaultBinding.WebhookURL,
		"AGENTSERVER_V2_TAE_WEBHOOK_PATH":          defaultBinding.WebhookPath,
		"AGENTSERVER_V2_TAE_POLICY_EVIDENCE_REF":   defaultBinding.EvidenceRef,
	} {
		if sandbox(name) != want {
			t.Fatalf("sandbox TAE policy environment %s = %q, want %q", name, sandbox(name), want)
		}
	}
	if sandbox("AGENTSERVER_V2_TAE_POLICY_HOST") != taepolicy.PublicHost ||
		sandbox("AGENTSERVER_V2_TAE_POLICY_ACCESS") != taepolicy.PublicAccessWhitelist ||
		sandbox("AGENTSERVER_V2_TAE_WEBHOOK_PATH") != taepolicy.WebhookPath {
		t.Fatalf("rendered TAE policy has a non-canonical public authority: host=%q access=%q path=%q",
			sandbox("AGENTSERVER_V2_TAE_POLICY_HOST"), sandbox("AGENTSERVER_V2_TAE_POLICY_ACCESS"), sandbox("AGENTSERVER_V2_TAE_WEBHOOK_PATH"))
	}
	switch sandbox("AGENTSERVER_V2_TAE_WEBHOOK_MODE") {
	case "psm":
		if sandbox("AGENTSERVER_V2_TAE_WEBHOOK_PSM") == "" || sandbox("AGENTSERVER_V2_TAE_WEBHOOK_URL") != "" {
			t.Fatalf("PSM webhook rendered with an ambiguous authority: psm=%q url=%q",
				sandbox("AGENTSERVER_V2_TAE_WEBHOOK_PSM"), sandbox("AGENTSERVER_V2_TAE_WEBHOOK_URL"))
		}
	case "url":
		if sandbox("AGENTSERVER_V2_TAE_WEBHOOK_URL") == "" || sandbox("AGENTSERVER_V2_TAE_WEBHOOK_PSM") != "" {
			t.Fatalf("URL webhook rendered with an ambiguous authority: psm=%q url=%q",
				sandbox("AGENTSERVER_V2_TAE_WEBHOOK_PSM"), sandbox("AGENTSERVER_V2_TAE_WEBHOOK_URL"))
		}
	default:
		t.Fatalf("rendered unsupported TAE webhook mode %q", sandbox("AGENTSERVER_V2_TAE_WEBHOOK_MODE"))
	}
}

func assertManagedNetworkPolicyShape(t *testing.T, foundation []map[string]any, document ConfigDocument) {
	t.Helper()
	sandbox := findResource(t, foundation, "NetworkPolicy", sandboxComponent)
	sandboxSpec := objectField(t, sandbox, "spec")
	sandboxIngress := arrayField(t, sandboxSpec, "ingress")
	if len(sandboxIngress) != 1 {
		t.Fatalf("sandbox-gateway ingress rule count = %d, want 1", len(sandboxIngress))
	}
	ingressRule := sandboxIngress[0].(map[string]any)
	if numberField(t, objectArrayFirst(t, ingressRule, "ports"), "port") != int(document.Services.SandboxGateway.Port) {
		t.Fatalf("sandbox-gateway ingress port is not the service port")
	}
	from := arrayField(t, ingressRule, "from")
	if len(from) != 1 {
		t.Fatalf("sandbox-gateway ingress peer count = %d, want executor-gateway only", len(from))
	}
	peer := from[0].(map[string]any)
	labels := objectField(t, objectField(t, peer, "podSelector"), "matchLabels")
	if labels["app.kubernetes.io/name"] != executorComponent {
		t.Fatalf("sandbox-gateway ingress peer = %#v, want executor-gateway only", from)
	}
	assertPodPeerPresent(t, sandboxSpec, "egress", coreComponent, document.Services.Core.Port)
	assertNamespacedPodPeerPresent(t, sandboxSpec, "egress", ProductionTAEProxyNamespace,
		map[string]string{"app": ProductionTAEProxyPodApp}, ProductionTAEProxyPort)
	if got := len(arrayField(t, sandboxSpec, "egress")); got != 3 {
		t.Fatalf("sandbox-gateway egress rule count = %d, want only Core, DNS, and maliva", got)
	}
	if podPeerPresent(t, sandboxSpec, "egress", egressComponent) {
		t.Fatal("sandbox-gateway egress unexpectedly reaches egress-authorizer directly")
	}

	egress := findResource(t, foundation, "NetworkPolicy", egressComponent)
	egressSpec := objectField(t, egress, "spec")
	egressIngress := arrayField(t, egressSpec, "ingress")
	wantIngressRules := 1
	if len(document.Network.EgressAuthorizerIngress) > 0 {
		wantIngressRules++
	}
	if len(egressIngress) != wantIngressRules {
		t.Fatalf("egress-authorizer ingress rule count = %d, want %d", len(egressIngress), wantIngressRules)
	}
	wantCIDRs := make(map[string]bool, len(document.Network.EgressAuthorizerIngress))
	for _, cidr := range document.Network.EgressAuthorizerIngress {
		wantCIDRs[cidr] = true
	}
	seenCIDRs := make(map[string]bool, len(wantCIDRs))
	seenGateway := false
	for _, rawRule := range egressIngress {
		rule := rawRule.(map[string]any)
		if numberField(t, objectArrayFirst(t, rule, "ports"), "port") != int(document.Services.EgressAuthorizer.Port) {
			t.Fatalf("egress-authorizer ingress port is not the service port")
		}
		for _, raw := range arrayField(t, rule, "from") {
			peer := raw.(map[string]any)
			if block, ok := peer["ipBlock"].(map[string]any); ok {
				cidr := stringField(t, block, "cidr")
				if !wantCIDRs[cidr] {
					t.Fatalf("egress-authorizer ingress contains an unexpected CIDR %q", cidr)
				}
				seenCIDRs[cidr] = true
				continue
			}
			namespace, namespaceOK := peer["namespaceSelector"].(map[string]any)
			pod, podOK := peer["podSelector"].(map[string]any)
			if !namespaceOK || !podOK || stringField(t, objectField(t, namespace, "matchLabels"), "kubernetes.io/metadata.name") != document.Ingress.GatewayNamespace {
				t.Fatalf("egress-authorizer ingress contains an unexpected non-CIDR peer %#v", peer)
			}
			gatewayLabels := objectField(t, pod, "matchLabels")
			for key, value := range document.Ingress.GatewayPodSelector {
				if gatewayLabels[key] != value {
					t.Fatalf("egress-authorizer ingress Gateway selector drift: got=%#v want=%#v", gatewayLabels, document.Ingress.GatewayPodSelector)
				}
			}
			seenGateway = true
		}
	}
	if len(seenCIDRs) != len(wantCIDRs) || !seenGateway {
		t.Fatalf("egress-authorizer ingress peers missing CIDRs or Gateway: cidrs=%v gateway=%v", seenCIDRs, seenGateway)
	}
	assertPodPeerPresent(t, egressSpec, "egress", coreComponent, document.Services.Core.Port)
	if got := len(arrayField(t, egressSpec, "egress")); got != 2 {
		t.Fatalf("egress-authorizer egress rule count = %d, want only Core and DNS", got)
	}
}

func assertPodPeerPresent(t *testing.T, spec map[string]any, field, component string, port uint16) {
	t.Helper()
	if !podPeerPresent(t, spec, field, component) {
		t.Fatalf("NetworkPolicy %s is missing pod peer %s", field, component)
	}
	for _, rawRule := range arrayField(t, spec, field) {
		rule := rawRule.(map[string]any)
		if numberField(t, objectArrayFirst(t, rule, "ports"), "port") != int(port) {
			continue
		}
		for _, rawPeer := range arrayField(t, rule, "to") {
			peer := rawPeer.(map[string]any)
			selector, ok := peer["podSelector"].(map[string]any)
			if !ok {
				continue
			}
			labels, _ := selector["matchLabels"].(map[string]any)
			if labels["app.kubernetes.io/name"] == component {
				return
			}
		}
	}
	t.Fatalf("NetworkPolicy %s pod peer %s is not bound to TCP port %d", field, component, port)
}

func podPeerPresent(t *testing.T, spec map[string]any, field, component string) bool {
	t.Helper()
	rules, ok := spec[field].([]any)
	if !ok {
		return false
	}
	for _, rawRule := range rules {
		rule, ok := rawRule.(map[string]any)
		if !ok {
			continue
		}
		peers, _ := rule["to"].([]any)
		for _, rawPeer := range peers {
			peer, ok := rawPeer.(map[string]any)
			if !ok {
				continue
			}
			selector, ok := peer["podSelector"].(map[string]any)
			if !ok {
				continue
			}
			labels, _ := selector["matchLabels"].(map[string]any)
			if labels["app.kubernetes.io/name"] == component {
				return true
			}
		}
	}
	return false
}

func assertNamespacedPodPeerPresent(t *testing.T, spec map[string]any, field, namespace string, labels map[string]string, port uint16) {
	t.Helper()
	for _, rawRule := range arrayField(t, spec, field) {
		rule := rawRule.(map[string]any)
		if numberField(t, objectArrayFirst(t, rule, "ports"), "port") != int(port) {
			continue
		}
		for _, rawPeer := range arrayField(t, rule, "to") {
			peer := rawPeer.(map[string]any)
			namespaceSelector, namespaceOK := peer["namespaceSelector"].(map[string]any)
			podSelector, podOK := peer["podSelector"].(map[string]any)
			if !namespaceOK || !podOK {
				continue
			}
			namespaceLabels, _ := namespaceSelector["matchLabels"].(map[string]any)
			podLabels, _ := podSelector["matchLabels"].(map[string]any)
			if namespaceLabels["kubernetes.io/metadata.name"] != namespace {
				continue
			}
			matches := true
			for key, value := range labels {
				if podLabels[key] != value {
					matches = false
					break
				}
			}
			if matches {
				return
			}
		}
	}
	t.Fatalf("NetworkPolicy %s is missing namespaced pod peer %s/%v on TCP port %d", field, namespace, labels, port)
}
