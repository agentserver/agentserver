package workspaceauthority

import (
	"encoding/json"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/workspacerepository"
)

func TestRepositoryAuthorityEqualityAfterIndependentDecode(t *testing.T) {
	a := Binding{RepositoryID: "repo", Repository: &workspacerepository.Binding{CheckoutID: "repo", Source: workspacerepository.Source{URL: "https://code.byted.org/tce/rtm-aihub.git"}}}
	raw, _ := json.Marshal(a)
	var b Binding
	if json.Unmarshal(raw, &b) != nil {
		t.Fatal("decode")
	}
	if a.Repository == b.Repository || !Equal(&a, &b) {
		t.Fatal("authority compared allocation identity")
	}
	b.Repository.Source.Ref = "different"
	if Equal(&a, &b) {
		t.Fatal("different ref compared equal")
	}
}

func TestRepositoryRootSurvivesJSONHTTPProjection(t *testing.T) {
	raw := []byte(`{"kind": "managed", "root": "/workspace", "defaultCwd": "."}`)
	var wire struct {
		Root json.RawMessage `json:"root"`
	}
	encoded, _ := json.Marshal(struct {
		Root json.RawMessage `json:"root"`
	}{raw})
	if json.Unmarshal(encoded, &wire) != nil {
		t.Fatal("decode")
	}
	before, err := RepositoryRootDescriptorSHA256(raw)
	if err != nil {
		t.Fatal(err)
	}
	after, err := RepositoryRootDescriptorSHA256(wire.Root)
	if err != nil || before != after {
		t.Fatal("database whitespace invalidated repository authority")
	}
	other, _ := RepositoryRootDescriptorSHA256([]byte(`{"kind":"managed","root":"/other","defaultCwd":"."}`))
	if other == before {
		t.Fatal("different root accepted")
	}
}
