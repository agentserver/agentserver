package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProductionBootstrapAcceptsClosedProjectedConfig(t *testing.T) {
	t.Setenv(coreExternalOIDCIssuerEnvironment, "https://idp.example.test/oidc")
	t.Setenv(coreExternalOIDCSubjectEnvironment, "production-owner")
	document := validProductionBootstrapDocument()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	data := filepath.Join(root, "..2026_08_02")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(data, "bootstrap.json")
	if err := os.WriteFile(target, raw, 0o440); err != nil {
		t.Fatal(err)
	}
	projection := filepath.Join(root, "bootstrap.json")
	if err := os.Symlink(filepath.Join(filepath.Base(data), "bootstrap.json"), projection); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	bootstrap, err := loadProductionBootstrap(projection)
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap.WorkspaceID != document.WorkspaceID || bootstrap.SessionID != document.SessionID ||
		bootstrap.UserID != document.UserID || bootstrap.ExecutorID != document.ExecutorID ||
		bootstrap.ExternalOIDCIssuer != "https://idp.example.test/oidc" || bootstrap.ExternalOIDCSubject != "production-owner" {
		t.Fatalf("production bootstrap = %+v", bootstrap)
	}
}

func TestLoadProductionBootstrapRejectsOpenWorldOrInsecureConfig(t *testing.T) {
	t.Setenv(coreExternalOIDCIssuerEnvironment, "https://idp.example.test/oidc")
	t.Setenv(coreExternalOIDCSubjectEnvironment, "production-owner")
	valid := validProductionBootstrapDocument()
	for name, mutate := range map[string]func(*productionBootstrapDocument){
		"version":   func(value *productionBootstrapDocument) { value.Version = 2 },
		"workspace": func(value *productionBootstrapDocument) { value.WorkspaceID = "not-a-uuid" },
	} {
		t.Run(name, func(t *testing.T) {
			document := valid
			mutate(&document)
			if _, err := loadProductionBootstrap(writeProductionBootstrapDocument(t, document)); err == nil {
				t.Fatal("unsafe production bootstrap was accepted")
			}
		})
	}

	root := t.TempDir()
	unknown := filepath.Join(root, "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"version":1,"future":true}`), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := loadProductionBootstrap(unknown); err == nil {
		t.Fatal("unknown production bootstrap field was accepted")
	}
	duplicate := filepath.Join(root, "duplicate.json")
	if err := os.WriteFile(duplicate, []byte(`{"version":1,"version":1}`), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := loadProductionBootstrap(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate production bootstrap error = %v", err)
	}
}

func TestLoadProductionBootstrapRejectsInvalidEnvironmentIdentity(t *testing.T) {
	for name, identity := range map[string][2]string{
		"http issuer":           {"http://idp.example.test", "production-owner"},
		"issuer trailing slash": {"https://idp.example.test/", "production-owner"},
		"empty subject":         {"https://idp.example.test", ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(coreExternalOIDCIssuerEnvironment, identity[0])
			t.Setenv(coreExternalOIDCSubjectEnvironment, identity[1])
			if _, err := loadProductionBootstrap(writeProductionBootstrapDocument(t, validProductionBootstrapDocument())); err == nil {
				t.Fatal("unsafe production bootstrap environment was accepted")
			}
		})
	}
}

func validProductionBootstrapDocument() productionBootstrapDocument {
	return productionBootstrapDocument{
		Version:     1,
		WorkspaceID: "40000000-0000-4000-8000-000000000004",
		SessionID:   "50000000-0000-4000-8000-000000000005",
		UserID:      "10000000-0000-4000-8000-000000000001",
		ExecutorID:  "20000000-0000-4000-8000-000000000002",
	}
}

func writeProductionBootstrapDocument(t *testing.T, document productionBootstrapDocument) string {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bootstrap.json")
	if err := os.WriteFile(path, raw, 0o400); err != nil {
		t.Fatal(err)
	}
	return path
}
