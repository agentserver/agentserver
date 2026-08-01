package main

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

func TestLoadDevelopmentBootstrapDerivesExecutorEnrollment(t *testing.T) {
	configPath, manifestBytes := writeDevelopmentBootstrapFixture(t, developmentBootstrapDocument{
		Version:     1,
		WorkspaceID: "40000000-0000-4000-8000-000000000004",
		SessionID:   "50000000-0000-4000-8000-000000000005",
		ActorID:     "10000000-0000-4000-8000-000000000001",
		Identity:    developmentBootstrapIdentityDocument{Issuer: "http://127.0.0.1:17447/idp", Subject: "agentserver-dev-user"},
		Executor: developmentBootstrapExecutorDocument{
			ExecutorID:    "20000000-0000-4000-8000-000000000002",
			EnvironmentID: "60000000-0000-4000-8000-000000000006",
			AgentxVersion: "0.1.0-dev", Platform: "darwin-arm64", WorkspaceRoot: "/workspace",
			DisplayName: "Local workspace", Description: "development executor", DefaultCWD: "src",
		},
	})
	bootstrap, err := loadDevelopmentBootstrap(configPath)
	if err != nil {
		t.Fatal(err)
	}
	wantRuntimeDigest := sha256.Sum256(manifestBytes)
	if bootstrap.WorkspaceID != "40000000-0000-4000-8000-000000000004" ||
		bootstrap.ExecutorID != "20000000-0000-4000-8000-000000000002" ||
		bootstrap.RuntimeManifestSHA256 != wantRuntimeDigest || bootstrap.AgentxVersion != "0.1.0-dev" ||
		bootstrap.ExternalOIDCIssuer != "http://127.0.0.1:17447/idp" || bootstrap.ExternalOIDCSubject != "agentserver-dev-user" {
		t.Fatalf("derived development bootstrap = %+v", bootstrap)
	}
	environment := bootstrap.Environment
	if environment.EnvironmentID != "60000000-0000-4000-8000-000000000006" ||
		environment.Platform != "darwin-arm64" || environment.CodexRelease != "0.146.0" ||
		environment.OuterProfileVersion != execprofile.FilesystemReadVersion || !environment.InsecureDev ||
		strings.Join(environment.ProcessMethods, ",") != strings.Join(execprofile.ProcessMethods(), ",") ||
		string(environment.RootDescriptor) != `{"defaultCwd":"src","description":"development executor","displayName":"Local workspace","kind":"local","root":"/workspace"}` {
		t.Fatalf("derived development environment = %+v", environment)
	}
	if environment.OwnerPolicySHA256 == [sha256.Size]byte{} || bootstrap.MachineKeySHA256 == [sha256.Size]byte{} {
		t.Fatal("development-only enrollment digests were not derived")
	}
}

func TestLoadDevelopmentBootstrapRejectsAmbiguousOrUnsafeConfig(t *testing.T) {
	valid := developmentBootstrapDocument{
		Version:     1,
		WorkspaceID: "40000000-0000-4000-8000-000000000004",
		SessionID:   "50000000-0000-4000-8000-000000000005",
		ActorID:     "10000000-0000-4000-8000-000000000001",
		Identity:    developmentBootstrapIdentityDocument{Issuer: "http://127.0.0.1:17447/idp", Subject: "agentserver-dev-user"},
		Executor: developmentBootstrapExecutorDocument{
			ExecutorID:    "20000000-0000-4000-8000-000000000002",
			EnvironmentID: "60000000-0000-4000-8000-000000000006",
			AgentxVersion: "0.1.0-dev", Platform: "darwin-arm64", WorkspaceRoot: "/workspace",
		},
	}
	for name, mutate := range map[string]func(*developmentBootstrapDocument){
		"version":          func(value *developmentBootstrapDocument) { value.Version = 2 },
		"workspace":        func(value *developmentBootstrapDocument) { value.WorkspaceID = "not-a-uuid" },
		"identity issuer":  func(value *developmentBootstrapDocument) { value.Identity.Issuer = "https://idp.example" },
		"identity subject": func(value *developmentBootstrapDocument) { value.Identity.Subject = "" },
		"platform":         func(value *developmentBootstrapDocument) { value.Executor.Platform = "plan9-amd64" },
		"relative root":    func(value *developmentBootstrapDocument) { value.Executor.WorkspaceRoot = "workspace" },
		"default cwd":      func(value *developmentBootstrapDocument) { value.Executor.DefaultCWD = "../escape" },
	} {
		t.Run(name, func(t *testing.T) {
			document := valid
			mutate(&document)
			configPath, _ := writeDevelopmentBootstrapFixture(t, document)
			if _, err := loadDevelopmentBootstrap(configPath); err == nil {
				t.Fatal("unsafe development bootstrap config was accepted")
			}
		})
	}

	root := t.TempDir()
	unknownPath := filepath.Join(root, "unknown.json")
	if err := os.WriteFile(unknownPath, []byte(`{"version":1,"future":true}`), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDevelopmentBootstrap(unknownPath); err == nil {
		t.Fatal("unknown development bootstrap field was accepted")
	}
	duplicatePath := filepath.Join(root, "duplicate.json")
	if err := os.WriteFile(duplicatePath, []byte(`{"version":1,"version":1}`), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDevelopmentBootstrap(duplicatePath); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate development bootstrap error = %v", err)
	}
}

func TestDevelopmentBootstrapValidatesWindowsRoots(t *testing.T) {
	for _, root := range []string{`C:\workspace`, `D:/workspace/repository`} {
		if err := validateDevelopmentExecutorRoot("windows-amd64", root); err != nil {
			t.Fatalf("valid Windows root %q: %v", root, err)
		}
	}
	for _, root := range []string{`workspace`, `C:\work\..\escape`, `C:/work\mixed`, `C:/work/`} {
		if err := validateDevelopmentExecutorRoot("windows-amd64", root); err == nil {
			t.Fatalf("unsafe Windows root %q was accepted", root)
		}
	}
}

func writeDevelopmentBootstrapFixture(t *testing.T, document developmentBootstrapDocument) (string, []byte) {
	t.Helper()
	root := t.TempDir()
	manifest := developmentBootstrapTestManifest()
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "runtime-manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o400); err != nil {
		t.Fatal(err)
	}
	document.Executor.RuntimeManifestFile = manifestPath
	configBytes, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "bootstrap.json")
	if err := os.WriteFile(configPath, configBytes, 0o400); err != nil {
		t.Fatal(err)
	}
	return configPath, manifestBytes
}

func developmentBootstrapTestManifest() runtimelock.Manifest {
	artifact := runtimelock.PlatformArtifacts{
		Codex: runtimelock.FileArtifact{
			Path: "bin/codex", SourceURL: "https://example.test/codex/0.146.0/codex",
			SHA256: strings.Repeat("d", 64), SizeBytes: 1024,
		},
		ExternalExecutables: map[string]runtimelock.FileArtifact{},
	}
	return runtimelock.Manifest{
		ManifestVersion: runtimelock.CurrentManifestVersion, CodexRelease: "0.146.0",
		CodexCommit: strings.Repeat("a", 40), AppServerSchemaSHA256: strings.Repeat("b", 64),
		AppServerSchemaDigestAlgorithm: runtimelock.AppServerSchemaDigestAlgorithmV1,
		ExecProtocolSourceSHA256:       strings.Repeat("c", 64),
		ExecServerBounds: runtimelock.ExecServerBounds{
			MaxStdioFrameBytes: 64 * 1024 * 1024, MaxJSONValues: 256 * 1024,
			ArgvEnvLimit:                  runtimelock.ArgvEnvLimitTransportAndPlatformOnly,
			RetainedOutputBytesPerProcess: 1024 * 1024, RetainedOutputChunksPerProcess: 50_000,
			RetainedStdinWriteIDsPerProcess: 4096, ExitedProcessRetentionMilliseconds: 30_000,
		},
		AgentxLimits: runtimelock.AgentxLimits{
			MaxFrameBytes: 8 * 1024 * 1024, MaxJSONValues: 64 * 1024,
			MaxArgvElements: 256, MaxArgvBytes: 16 * 1024, MaxEnvVariables: 256, MaxEnvBytes: 16 * 1024,
			MaxWriteIDBytes: 128, MaxOutputBufferBytesPerProcess: 8 * 1024 * 1024,
		},
		CheckpointAllowlistVersion: 1, AgentxProtocolVersion: "2.0",
		Artifacts: map[string]runtimelock.PlatformArtifacts{"darwin-arm64": artifact},
	}
}
