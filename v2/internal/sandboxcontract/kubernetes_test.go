package sandboxcontract

import (
	"encoding/json"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
)

func TestExplicitKubernetesReference(t *testing.T) {
	ref := SandboxRef{SandboxID: "sandbox-1", TargetGeneration: 1, BackendKind: executionbackend.KindKubernetes}
	if err := ref.Validate(); err != nil {
		t.Fatal(err)
	}
	if ref.Target("env-1").Kind != executionbackend.KindKubernetes {
		t.Fatal("Kubernetes ref became TAE")
	}
	encoded, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SandboxRef
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != ref {
		t.Fatalf("reference round trip: %s %v", encoded, err)
	}
	ref.BackendKind = ""
	if ref.Target("env-1").Kind != executionbackend.KindTAE {
		t.Fatal("legacy ref compatibility changed")
	}
	ref.BackendKind = executionbackend.KindAgentX
	if err := ref.Validate(); err == nil {
		t.Fatal("AgentX accepted as managed sandbox reference")
	}
}
