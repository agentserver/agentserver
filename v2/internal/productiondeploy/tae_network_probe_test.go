package productiondeploy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/taeimage"
)

func TestTAENetworkProbeResourcesAreOneShotClosedWorldAuthority(t *testing.T) {
	loaded, err := ValidateConfig(policyBootstrapConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	resources, err := taeNetworkProbeResources(loaded)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := marshalKubernetesList(resources)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 3 || list.Items[0]["kind"] != "ServiceAccount" ||
		list.Items[1]["kind"] != "NetworkPolicy" || list.Items[2]["kind"] != "Job" {
		t.Fatalf("probe resource list = %#v", list.Items)
	}
	jobMetadata := list.Items[2]["metadata"].(map[string]any)
	if jobMetadata["name"] != taeNetworkProbeJobPlaceholder {
		t.Fatalf("static probe job name = %#v", jobMetadata["name"])
	}
	policySpec := list.Items[1]["spec"].(map[string]any)
	egress := policySpec["egress"].([]any)
	if len(egress) != 2 || policySpec["ingress"] != nil {
		t.Fatalf("probe network policy = %#v", policySpec)
	}
	jobSpec := list.Items[2]["spec"].(map[string]any)
	if jobSpec["backoffLimit"].(float64) != 0 {
		t.Fatalf("probe job retries create: %#v", jobSpec)
	}
	podSpec := jobSpec["template"].(map[string]any)["spec"].(map[string]any)
	if podSpec["serviceAccountName"] != taeNetworkProbeComponent || podSpec["automountServiceAccountToken"] != false ||
		podSpec["restartPolicy"] != "Never" {
		t.Fatalf("probe pod authority = %#v", podSpec)
	}
	container := podSpec["containers"].([]any)[0].(map[string]any)
	if container["image"] != loaded.Document.Images.Service ||
		container["command"].([]any)[0] != "/usr/local/bin/sandbox-gateway" ||
		container["args"].([]any)[0] != "probe-network" {
		t.Fatalf("probe container = %#v", container)
	}
	environment := environmentByName(container["env"].([]any))
	wantTAEImage, err := taeimage.ContentTagForRepository(ProductionTAEManagedSandboxImage, loaded.Document.Images.ManagedSandbox)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"AGENTSERVER_V2_TAE_SANDBOX_IMAGE":                    wantTAEImage,
		"AGENTSERVER_V2_TAE_PROXY_URL":                        ProductionTAEProxyURL,
		"AGENTSERVER_V2_TAE_BYTECLOUD_JWT_ENDPOINT":           ProductionByteCloudJWTEndpoint,
		"AGENTSERVER_V2_TAE_PROBE_DEPLOYMENT_CONFIG_SHA256":   canonicalDigest(loaded.Document),
		"AGENTSERVER_V2_TAE_PROBE_CONNECTIVITY_ATTEMPTS":      "20",
		"AGENTSERVER_V2_TAE_PROBE_LIFECYCLE_ATTEMPTS":         "1",
		"AGENTSERVER_V2_TAE_BYTECLOUD_ACCESS_KEY_ID_FILE":     serviceMaterialPath("bytecloud-access-key-id"),
		"AGENTSERVER_V2_TAE_BYTECLOUD_SECRET_ACCESS_KEY_FILE": serviceMaterialPath("bytecloud-secret-access-key"),
	} {
		if environment[name]["value"] != want {
			t.Fatalf("probe env %s = %#v, want %q", name, environment[name], want)
		}
	}
	policyRevision := environment["AGENTSERVER_V2_TAE_PROBE_POLICY_REVISION"]
	policyReference := policyRevision["valueFrom"].(map[string]any)["configMapKeyRef"].(map[string]any)
	if policyRevision["value"] != nil || policyReference["name"] != taeNetworkProbeInputPlaceholder ||
		policyReference["key"] != "policy-revision" || policyReference["optional"] != false {
		t.Fatalf("probe policy revision authority = %#v", policyRevision)
	}
	for _, name := range []string{
		"AGENTSERVER_V2_TAE_PROBE_POD_NAMESPACE", "AGENTSERVER_V2_TAE_PROBE_POD_NAME",
		"AGENTSERVER_V2_TAE_PROBE_POD_UID", "AGENTSERVER_V2_TAE_PROBE_NODE_NAME",
		"AGENTSERVER_V2_TAE_PROBE_SERVICE_ACCOUNT",
	} {
		if environment[name]["valueFrom"] == nil || environment[name]["value"] != nil {
			t.Fatalf("probe downward env %s = %#v", name, environment[name])
		}
	}
	volumes := podSpec["volumes"].([]any)
	secret := volumes[0].(map[string]any)["secret"].(map[string]any)
	items := secret["items"].([]any)
	if secret["secretName"] != ProductionSandboxSecret || len(items) != 2 {
		t.Fatalf("probe secret projection = %#v", secret)
	}
	keys := []string{items[0].(map[string]any)["key"].(string), items[1].(map[string]any)["key"].(string)}
	if strings.Join(keys, ",") != "bytecloud-access-key-id,bytecloud-secret-access-key" {
		t.Fatalf("probe secret keys = %v", keys)
	}
	for _, forbidden := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "X-Jwt-Token"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("probe manifest contains forbidden global identity/proxy field %s", forbidden)
		}
	}
}

func environmentByName(values []any) map[string]map[string]any {
	result := make(map[string]map[string]any, len(values))
	for _, value := range values {
		entry := value.(map[string]any)
		result[entry["name"].(string)] = entry
	}
	return result
}
