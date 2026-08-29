package workspaceauthority

import (
	"crypto/sha256"
	"strings"
	"testing"
)

func TestValidateWorkingDirectoryUsesClosedRelativeGrammar(t *testing.T) {
	for _, value := range []string{".", "skills", ".agents/skills", "src/service"} {
		if err := ValidateWorkingDirectory(value); err != nil {
			t.Fatalf("ValidateWorkingDirectory(%q) error = %v", value, err)
		}
	}
	for _, value := range []string{"", "/workspace", "../rtm-aihub", "src/../secret", "src//service", `src\service`, "src\nservice"} {
		if err := ValidateWorkingDirectory(value); err == nil {
			t.Fatalf("ValidateWorkingDirectory(%q) unexpectedly succeeded", value)
		}
	}
}

func TestBindingRequiresCompleteFrozenAuthority(t *testing.T) {
	binding := Binding{
		EnvironmentID:           "10000000-0000-4000-8000-000000000001",
		EnvironmentVersion:      3,
		RootSHA256:              sha256.Sum256([]byte(`{"root":"/workspace"}`)),
		WorkingDirectory:        ".",
		WorkingDirectoryVersion: 2,
	}
	if err := binding.Validate(); err != nil {
		t.Fatal(err)
	}
	binding.RootSHA256 = [sha256.Size]byte{}
	if err := binding.Validate(); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("Binding.Validate() error = %v", err)
	}
}

func TestRootDescriptorSHA256RejectsNonObjectAndTrailingJSON(t *testing.T) {
	raw := []byte(`{"kind":"local","root":"/workspace"}`)
	digest, err := RootDescriptorSHA256(raw)
	if err != nil || digest != sha256.Sum256(raw) {
		t.Fatalf("RootDescriptorSHA256() = %x, %v", digest, err)
	}
	for _, candidate := range [][]byte{[]byte(`[]`), []byte(`{} {}`)} {
		if _, err := RootDescriptorSHA256(candidate); err == nil {
			t.Fatalf("RootDescriptorSHA256(%q) unexpectedly succeeded", candidate)
		}
	}
}
