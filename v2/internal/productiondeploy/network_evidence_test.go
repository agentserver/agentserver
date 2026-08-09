package productiondeploy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExampleNetworkEvidenceBindingDigest(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "production", "config.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := decodeConfigDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateNetwork(&document.Network, document.Services); err != nil {
		t.Fatal(err)
	}
	want := managedTAENetworkEvidenceDigest(document)
	if document.Managed.TAE.NetworkEvidence.BindingSHA256 != want {
		t.Fatalf("example network evidence bindingSha256 = %q, want %q", document.Managed.TAE.NetworkEvidence.BindingSHA256, want)
	}
	wantRuntime := managedRuntimeProfileDigest(document, document.Managed)
	if document.Managed.Environment.RuntimeProfileSHA256 != wantRuntime {
		t.Fatalf("example runtimeProfileSha256 = %q, want %q", document.Managed.Environment.RuntimeProfileSHA256, wantRuntime)
	}
	wantPack := managedPackSetDigest(document.Managed)
	if document.Managed.Environment.PackSetSHA256 != wantPack {
		t.Fatalf("example packSetSha256 = %q, want %q", document.Managed.Environment.PackSetSHA256, wantPack)
	}
}
