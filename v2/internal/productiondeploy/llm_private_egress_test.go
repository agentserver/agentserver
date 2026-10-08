package productiondeploy

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPrivateLLMGatewayEgressIsLimitedToAxonHubCNHTTPS(t *testing.T) {
	loaded, err := ValidateConfig(kubernetesConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Render(loaded)
	if err != nil {
		t.Fatal(err)
	}
	foundation := parseKubernetesList(t, mustBundleFile(t, bundle, foundationFile))
	policy := findResource(t, foundation, "CiliumNetworkPolicy", "llmproxy-cn-axonhub-egress")
	spec := objectField(t, policy, "spec")
	selector := objectField(t, objectField(t, spec, "endpointSelector"), "matchLabels")
	if selector["app.kubernetes.io/name"] != llmproxyComponent || selector["app.kubernetes.io/part-of"] != "agentserver-v2" {
		t.Fatal("policy escaped llmproxy selector")
	}
	rules := arrayField(t, spec, "egress")
	if len(rules) != 2 {
		t.Fatal("unexpected private egress rules")
	}
	dns := rules[0].(map[string]any)
	dnsPort := objectArrayFirst(t, dns, "toPorts")
	dnsRule := objectArrayFirst(t, objectField(t, dnsPort, "rules"), "dns")
	if dnsRule["matchPattern"] != "*" {
		t.Fatal("DNS interception would break other public model gateways")
	}
	peer := objectArrayFirst(t, dns, "toEndpoints")
	if objectField(t, peer, "matchLabels")["k8s:io.kubernetes.pod.namespace"] != loaded.Document.Network.DNSNamespace {
		t.Fatal("DNS scope escaped cluster resolver")
	}
	https := rules[1].(map[string]any)
	if !reflect.DeepEqual(objectArrayFirst(t, https, "toFQDNs"), map[string]any{"matchName": "axonhub-cn.byted.bps.dev"}) {
		t.Fatal("private HTTPS destination widened")
	}
	port := objectArrayFirst(t, https, "toPorts")
	if !reflect.DeepEqual(objectArrayFirst(t, port, "ports"), map[string]any{"port": "443", "protocol": "TCP"}) || !reflect.DeepEqual(port["serverNames"], []any{"axonhub-cn.byted.bps.dev"}) {
		t.Fatal("port or TLS SNI scope widened")
	}
	raw, _ := json.Marshal(https)
	if strings.Contains(string(raw), "matchPattern") || strings.Contains(string(raw), "toEntities") || strings.Contains(string(raw), "toCIDR") {
		t.Fatal("private HTTPS rule became a broad network exception")
	}
}
