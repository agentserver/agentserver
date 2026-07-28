package runtimelock

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestParseValidManifest(t *testing.T) {
	encoded, err := json.Marshal(validManifest())
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Parse(encoded)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if manifest.CodexRelease != "0.145.0" {
		t.Fatalf("codex release = %q", manifest.CodexRelease)
	}
}

func TestParseRejectsDuplicateAndUnknownFields(t *testing.T) {
	if _, err := Parse([]byte(`{"manifestVersion":1,"manifestVersion":1}`)); err == nil || !strings.Contains(err.Error(), "duplicate object key") {
		t.Fatalf("duplicate-key Parse() error = %v", err)
	}

	encoded, err := json.Marshal(validManifest())
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	object["futureUnsafeDefault"] = true
	encoded, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(encoded); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field Parse() error = %v", err)
	}
}

func TestManifestSemanticValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Manifest)
		wantErr string
	}{
		{name: "manifest version", mutate: func(m *Manifest) { m.ManifestVersion = 2 }, wantErr: "manifestVersion"},
		{name: "release", mutate: func(m *Manifest) { m.CodexRelease = "local" }, wantErr: "codexRelease"},
		{name: "commit", mutate: func(m *Manifest) { m.CodexCommit = "HEAD" }, wantErr: "codexCommit"},
		{name: "schema digest", mutate: func(m *Manifest) { m.AppServerSchemaSHA256 = strings.Repeat("A", 64) }, wantErr: "appServerSchemaSha256"},
		{name: "schema digest algorithm", mutate: func(m *Manifest) { m.AppServerSchemaDigestAlgorithm = "raw-tree-v1" }, wantErr: "appServerSchemaDigestAlgorithm"},
		{name: "checkpoint allowlist", mutate: func(m *Manifest) { m.CheckpointAllowlistVersion = 0 }, wantErr: "checkpointAllowlistVersion"},
		{name: "protocol", mutate: func(m *Manifest) { m.AgentxProtocolVersion = "v2" }, wantErr: "agentxProtocolVersion"},
		{name: "platform", mutate: func(m *Manifest) {
			artifact := m.Artifacts["linux-amd64"]
			delete(m.Artifacts, "linux-amd64")
			m.Artifacts["plan9-amd64"] = artifact
		}, wantErr: "not supported"},
		{name: "helpers missing", mutate: func(m *Manifest) {
			artifact := m.Artifacts["linux-amd64"]
			artifact.Helpers = nil
			m.Artifacts["linux-amd64"] = artifact
		}, wantErr: "helpers must be present"},
		{name: "path escape", mutate: func(m *Manifest) {
			artifact := m.Artifacts["linux-amd64"]
			artifact.Codex.Path = "../codex"
			m.Artifacts["linux-amd64"] = artifact
		}, wantErr: "normalized"},
		{name: "unstable source URL", mutate: func(m *Manifest) {
			artifact := m.Artifacts["linux-amd64"]
			artifact.Codex.SourceURL = "https://example.invalid/codex?token=temporary"
			m.Artifacts["linux-amd64"] = artifact
		}, wantErr: "stable credential-free"},
		{name: "duplicate path", mutate: func(m *Manifest) {
			artifact := m.Artifacts["linux-amd64"]
			helper := artifact.Helpers["codex-linux-sandbox"]
			helper.Path = artifact.Codex.Path
			artifact.Helpers["codex-linux-sandbox"] = helper
			m.Artifacts["linux-amd64"] = artifact
		}, wantErr: "is shared"},
		{name: "artifact size bound", mutate: func(m *Manifest) {
			artifact := m.Artifacts["linux-amd64"]
			artifact.Codex.SizeBytes = maxRuntimeArtifactBytes + 1
			m.Artifacts["linux-amd64"] = artifact
		}, wantErr: "sizeBytes exceeds"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := validManifest()
			test.mutate(&manifest)
			err := manifest.Validate()
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestRuntimeManifestJSONSchemaContract(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate runtimelock package")
	}
	schemaPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "api", "schema", "runtime-manifest.schema.json")
	encoded, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Schema               string                     `json:"$schema"`
		ID                   string                     `json:"$id"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	if schema.Schema != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("schema draft = %q", schema.Schema)
	}
	if schema.ID != "https://agentserver.dev/v2/schema/runtime-manifest.schema.json" {
		t.Fatalf("schema id = %q", schema.ID)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatal("runtime manifest schema must fail closed on unknown properties")
	}
	wantRequired := []string{
		"agentxProtocolVersion",
		"appServerSchemaDigestAlgorithm",
		"appServerSchemaSha256",
		"artifacts",
		"checkpointAllowlistVersion",
		"codexCommit",
		"codexRelease",
		"execProtocolSourceSha256",
		"manifestVersion",
	}
	sort.Strings(schema.Required)
	if strings.Join(schema.Required, "\n") != strings.Join(wantRequired, "\n") {
		t.Fatalf("required fields = %v, want %v", schema.Required, wantRequired)
	}
	propertyNames := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		propertyNames = append(propertyNames, name)
	}
	sort.Strings(propertyNames)
	if strings.Join(propertyNames, "\n") != strings.Join(wantRequired, "\n") {
		t.Fatalf("schema properties = %v, want exactly %v", propertyNames, wantRequired)
	}
}

func validManifest() Manifest {
	return Manifest{
		ManifestVersion:                CurrentManifestVersion,
		CodexRelease:                   "0.145.0",
		CodexCommit:                    strings.Repeat("a", 40),
		AppServerSchemaSHA256:          strings.Repeat("b", 64),
		AppServerSchemaDigestAlgorithm: AppServerSchemaDigestAlgorithmV1,
		ExecProtocolSourceSHA256:       strings.Repeat("c", 64),
		CheckpointAllowlistVersion:     1,
		AgentxProtocolVersion:          "2.0",
		Artifacts: map[string]PlatformArtifacts{
			"linux-amd64": {
				Codex: FileArtifact{
					Path:      "bin/codex",
					SourceURL: "https://github.com/openai/codex/releases/download/rust-v0.145.0/codex-x86_64-unknown-linux-musl.tar.gz",
					SHA256:    strings.Repeat("d", 64),
					SizeBytes: 1024,
				},
				Helpers: map[string]FileArtifact{
					"codex-linux-sandbox": {
						Path:      "bin/codex-linux-sandbox",
						SourceURL: "https://github.com/openai/codex/releases/download/rust-v0.145.0/codex-x86_64-unknown-linux-musl.tar.gz",
						SHA256:    strings.Repeat("e", 64),
						SizeBytes: 512,
					},
				},
			},
		},
	}
}
