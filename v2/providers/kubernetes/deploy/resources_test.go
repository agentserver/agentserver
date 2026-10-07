package deploy

import (
	"encoding/json"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestDefaultRuntimeTemplateAndLeastPrivilege(t *testing.T) {
	c := Config{Namespace: "agentserver-sandboxes", TemplateName: "managed-cli-v1", RuntimeImage: "registry.example/runtime:1", RuntimeTLSSecret: "runtime-tls", GatewayNamespace: "agentserver", GatewayServiceAccount: "sandbox-gateway-k8s", GatewayIdentity: "spiffe://agentserver.test/ns/agentserver/sa/sandbox-gateway-k8s"}
	for _, runtimeClass := range []string{"", "gvisor"} {
		c.RuntimeClassName = runtimeClass
		objects, err := Resources(c)
		if err != nil {
			t.Fatal(err)
		}
		if len(objects) != 7 {
			t.Fatalf("got %d resources", len(objects))
		}
		for _, obj := range objects {
			if obj.GetNamespace() != c.Namespace {
				t.Fatal("resource escaped sandbox namespace")
			}
			if obj.GetKind() == "SandboxTemplate" {
				spec, _, _ := unstructured.NestedMap(obj.Object, "spec", "podTemplate", "spec")
				actual, present := spec["runtimeClassName"]
				if runtimeClass == "" && present {
					t.Fatal("default runtime must OMIT runtimeClassName")
				}
				if runtimeClass != "" && actual != runtimeClass {
					t.Fatal("explicit runtime class lost")
				}
				if spec["automountServiceAccountToken"] != false || spec["enableServiceLinks"] != false {
					t.Fatal("Pod gets ambient Kubernetes credentials/configuration")
				}
				if spec["restartPolicy"] != "Never" {
					t.Fatal("container restart would revive an old generation")
				}
				b, _ := json.Marshal(spec)
				for _, forbidden := range []string{"hostPath", "hostNetwork", "hostPID", "privileged", "BYTECLOUD_AUTH", "LARKSUITE_CLI_USER_ACCESS_TOKEN"} {
					if strings.Contains(string(b), forbidden) {
						t.Fatalf("unsafe template contains %s", forbidden)
					}
				}
			}
			if obj.GetKind() == "Role" {
				b, _ := json.Marshal(obj.Object)
				for _, forbidden := range []string{"pods/exec", "pods/portforward", "secrets", "clusterroles", "\"*\""} {
					if strings.Contains(string(b), forbidden) {
						t.Fatalf("excessive gateway RBAC: %s", forbidden)
					}
				}
			}
		}
	}
}

func TestTemplateRejectsMissingImageAndNames(t *testing.T) {
	if _, err := Resources(Config{}); err == nil {
		t.Fatal("empty configuration accepted")
	}
}
