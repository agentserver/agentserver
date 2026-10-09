package kubernetesresources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDedicatedBubblewrapProfileStaysDefaultDeny(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "production", "security", "agentserver-bwrap-v1.seccomp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		DefaultAction string `json:"defaultAction"`
		Syscalls      []struct {
			Names  []string          `json:"names"`
			Action string            `json:"action"`
			Args   []json.RawMessage `json:"args"`
		} `json:"syscalls"`
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.DefaultAction != "SCMP_ACT_ERRNO" {
		t.Fatal("dedicated policy is not default-deny")
	}
	required := map[string]bool{"unshare": false, "mount": false, "umount2": false, "pivot_root": false}
	for _, rule := range policy.Syscalls {
		if rule.Action != "SCMP_ACT_ALLOW" {
			continue
		}
		for _, name := range rule.Names {
			switch name {
			case "bpf", "perf_event_open", "setns", "open_by_handle_at", "init_module", "finit_module", "delete_module", "reboot", "ptrace", "process_vm_readv", "process_vm_writev", "chroot", "modify_ldt":
				t.Fatalf("dedicated policy unexpectedly allows %s", name)
			case "unshare":
				if len(rule.Args) == 0 {
					t.Fatal("namespace creation lacks flags restrictions")
				}
			}
			if _, exists := required[name]; exists {
				required[name] = true
			}
		}
	}
	for name, found := range required {
		if !found {
			t.Fatalf("missing required syscall %s", name)
		}
	}
	aa, err := os.ReadFile(filepath.Join("..", "..", "deploy", "production", "security", "agentserver-bwrap-v1.apparmor"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"profile agentserver-bwrap-v1", "deny /sys/kernel/security/**", "deny @{PROC}/kcore", "  mount,", "  userns,"} {
		if !strings.Contains(string(aa), expected) {
			t.Fatalf("AppArmor protection missing: %s", expected)
		}
	}
}

func TestDedicatedProfileDoesNotChangeRuntimeClassOrGrantCapabilities(t *testing.T) {
	objects, err := Resources(Config{Namespace: "agentserver-sandboxes", TemplateName: "managed-cli-v1", RuntimeImage: "registry.example/runtime:1", RuntimeTLSSecret: "runtime-tls", GatewayNamespace: "agentserver", GatewayServiceAccount: "sandbox-gateway-k8s", GatewayIdentity: "spiffe://agentserver.test/ns/agentserver/sa/sandbox-gateway-k8s", BubblewrapProfile: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, obj := range objects {
		if obj["kind"] != "SandboxTemplate" {
			continue
		}
		spec := obj["spec"].(map[string]any)["podTemplate"].(map[string]any)["spec"].(map[string]any)
		if _, ok := spec["runtimeClassName"]; ok {
			t.Fatal("default runtime class was changed")
		}
		if spec["nodeSelector"].(map[string]any)[BubblewrapNodeLabel] != "v1" {
			t.Fatal("unqualified nodes may receive sandbox Pods")
		}
		security := spec["securityContext"].(map[string]any)
		if security["seccompProfile"].(map[string]any)["localhostProfile"] != BubblewrapSeccompProfile || security["appArmorProfile"].(map[string]any)["localhostProfile"] != BubblewrapAppArmorProfile {
			t.Fatal("wrong named security profile")
		}
		raw, _ := json.Marshal(spec)
		for _, forbidden := range []string{"Unconfined", "privileged", "SYS_ADMIN", "hostPath"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("runtime privilege expanded: %s", forbidden)
			}
		}
	}
}

func TestRepositoryStorageRBACDoesNotAllowVolumeDeletionOrPodExec(t *testing.T) {
	c := Config{Namespace: "agentserver-sandboxes", TemplateName: "managed-cli-v1", RuntimeImage: "registry.example/runtime:1", RuntimeTLSSecret: "runtime-tls", GatewayNamespace: "agentserver", GatewayServiceAccount: "sandbox-gateway-k8s", GatewayIdentity: "spiffe://agentserver.test/ns/agentserver/sa/sandbox-gateway-k8s", RepositoryStorage: true, UnrestrictedNetwork: true}
	objects, err := Resources(c)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, obj := range objects {
		if obj["kind"] == "NetworkPolicy" {
			t.Fatal("repository storage added network restrictions")
		}
		if obj["kind"] != "Role" {
			continue
		}
		for _, entry := range obj["rules"].([]any) {
			rule := entry.(map[string]any)
			for _, resource := range rule["resources"].([]any) {
				if resource == "pods/exec" || resource == "secrets" || resource == "*" {
					t.Fatal("repository storage widened runtime authority")
				}
				if resource == "persistentvolumeclaims" {
					found = true
					for _, verb := range rule["verbs"].([]any) {
						if verb != "create" && verb != "get" {
							t.Fatal("gateway can delete or rewrite session PVCs")
						}
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("repository storage RBAC missing")
	}
}
