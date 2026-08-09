package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadManagedEnvironmentProfileProjectsClosedTAEProfile(t *testing.T) {
	document := validManagedEnvironmentProfileDocument()
	profile, err := loadManagedEnvironmentProfile(writeManagedEnvironmentProfileDocument(t, document))
	if err != nil {
		t.Fatal(err)
	}
	if profile.WorkspaceID != document.WorkspaceID || profile.ExecutorID != document.ExecutorID ||
		profile.EnvironmentID != document.EnvironmentID || profile.CodexRelease != document.Runtime.CodexRelease ||
		profile.CodexCommit != document.Runtime.CodexCommit ||
		string(profile.RootDescriptor) != `{"defaultCwd":".","displayName":"Managed SG","kind":"managed","root":"/workspace"}` {
		t.Fatalf("managed environment profile = %+v", profile)
	}
	if profile.OwnerPolicySHA256 == [32]byte{} || profile.CodexSHA256 == [32]byte{} {
		t.Fatal("managed environment profile did not decode non-zero digests")
	}
}

func TestLoadManagedEnvironmentProfileRejectsOpenOrUnsafeConfig(t *testing.T) {
	valid := validManagedEnvironmentProfileDocument()
	for name, mutate := range map[string]func(*managedEnvironmentProfileDocument){
		"version":       func(document *managedEnvironmentProfileDocument) { document.Version++ },
		"workspace":     func(document *managedEnvironmentProfileDocument) { document.WorkspaceID = "not-a-uuid" },
		"relative root": func(document *managedEnvironmentProfileDocument) { document.Root.Path = "workspace" },
		"escaping cwd":  func(document *managedEnvironmentProfileDocument) { document.Root.DefaultCWD = "../escape" },
		"uppercase digest": func(document *managedEnvironmentProfileDocument) {
			document.OwnerPolicySHA256 = strings.Repeat("A", 64)
		},
		"zero digest": func(document *managedEnvironmentProfileDocument) {
			document.Runtime.CodexSHA256 = strings.Repeat("0", 64)
		},
		"codex commit": func(document *managedEnvironmentProfileDocument) {
			document.Runtime.CodexCommit = strings.Repeat("z", 40)
		},
	} {
		t.Run(name, func(t *testing.T) {
			document := valid
			mutate(&document)
			if _, err := loadManagedEnvironmentProfile(writeManagedEnvironmentProfileDocument(t, document)); err == nil {
				t.Fatal("unsafe managed environment profile was accepted")
			}
		})
	}

	unknown := filepath.Join(t.TempDir(), "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"version":1,"future":true}`), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := loadManagedEnvironmentProfile(unknown); err == nil {
		t.Fatal("unknown managed environment profile field was accepted")
	}
	duplicate := filepath.Join(t.TempDir(), "duplicate.json")
	if err := os.WriteFile(duplicate, []byte(`{"version":1,"version":1}`), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := loadManagedEnvironmentProfile(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate managed environment profile error = %v", err)
	}
}

func validManagedEnvironmentProfileDocument() managedEnvironmentProfileDocument {
	return managedEnvironmentProfileDocument{
		Version: 1, WorkspaceID: "40000000-0000-4000-8000-000000000004",
		ExecutorID:        "20000000-0000-4000-8000-000000000002",
		EnvironmentID:     "60000000-0000-4000-8000-000000000008",
		OwnerPolicySHA256: strings.Repeat("1", 64),
		Root: managedEnvironmentRootDocument{
			Path: "/workspace", DisplayName: "Managed SG", DefaultCWD: ".",
		},
		Runtime: managedEnvironmentLegacyRuntimeDocument{
			CodexRelease: "0.146.0-managed", CodexCommit: strings.Repeat("a", 40),
			CodexSHA256: strings.Repeat("2", 64),
		},
	}
}

func writeManagedEnvironmentProfileDocument(t *testing.T, document managedEnvironmentProfileDocument) string {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(t.TempDir(), "managed-environment.json")
	if err := os.WriteFile(filePath, raw, 0o400); err != nil {
		t.Fatal(err)
	}
	return filePath
}
