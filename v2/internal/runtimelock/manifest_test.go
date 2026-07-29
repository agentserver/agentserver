package runtimelock

import (
	"bytes"
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

	validEncoded, err := json.Marshal(validManifest())
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(validEncoded, &object); err != nil {
		t.Fatal(err)
	}
	object["futureUnsafeDefault"] = true
	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(encoded); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field Parse() error = %v", err)
	}

	legacy := bytes.Replace(validEncoded, []byte(`"externalExecutables"`), []byte(`"helpers"`), 1)
	if _, err := Parse(legacy); err == nil || !strings.Contains(err.Error(), "unknown field \"helpers\"") {
		t.Fatalf("legacy helpers Parse() error = %v", err)
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
		{name: "stock frame bound", mutate: func(m *Manifest) { m.ExecServerBounds.MaxStdioFrameBytes = 0 }, wantErr: "maxStdioFrameBytes"},
		{name: "stock JSON bound", mutate: func(m *Manifest) { m.ExecServerBounds.MaxJSONValues = 0 }, wantErr: "maxJsonValues"},
		{name: "stock argv env bound kind", mutate: func(m *Manifest) { m.ExecServerBounds.ArgvEnvLimit = "host-arg-max" }, wantErr: "argvEnvLimit"},
		{name: "stock retained output bytes", mutate: func(m *Manifest) { m.ExecServerBounds.RetainedOutputBytesPerProcess = 0 }, wantErr: "retainedOutputBytesPerProcess"},
		{name: "stock retained output chunks", mutate: func(m *Manifest) { m.ExecServerBounds.RetainedOutputChunksPerProcess = 0 }, wantErr: "retainedOutputChunksPerProcess"},
		{name: "stock retained write ids", mutate: func(m *Manifest) { m.ExecServerBounds.RetainedStdinWriteIDsPerProcess = 0 }, wantErr: "retainedStdinWriteIdsPerProcess"},
		{name: "stock exited retention", mutate: func(m *Manifest) { m.ExecServerBounds.ExitedProcessRetentionMilliseconds = 0 }, wantErr: "exitedProcessRetentionMilliseconds"},
		{name: "agentx frame above stock", mutate: func(m *Manifest) { m.AgentxLimits.MaxFrameBytes = m.ExecServerBounds.MaxStdioFrameBytes + 1 }, wantErr: "agentxLimits.maxFrameBytes"},
		{name: "agentx JSON above stock", mutate: func(m *Manifest) { m.AgentxLimits.MaxJSONValues = m.ExecServerBounds.MaxJSONValues + 1 }, wantErr: "agentxLimits.maxJsonValues"},
		{name: "agentx argv elements", mutate: func(m *Manifest) { m.AgentxLimits.MaxArgvElements = 0 }, wantErr: "agentxLimits.maxArgvElements"},
		{name: "agentx argv bytes", mutate: func(m *Manifest) { m.AgentxLimits.MaxArgvBytes = 0 }, wantErr: "agentxLimits.maxArgvBytes"},
		{name: "agentx env variables", mutate: func(m *Manifest) { m.AgentxLimits.MaxEnvVariables = 0 }, wantErr: "agentxLimits.maxEnvVariables"},
		{name: "agentx env bytes", mutate: func(m *Manifest) { m.AgentxLimits.MaxEnvBytes = 0 }, wantErr: "agentxLimits.maxEnvBytes"},
		{name: "agentx write id", mutate: func(m *Manifest) { m.AgentxLimits.MaxWriteIDBytes = 0 }, wantErr: "agentxLimits.maxWriteIdBytes"},
		{name: "agentx output buffer", mutate: func(m *Manifest) { m.AgentxLimits.MaxOutputBufferBytesPerProcess = 0 }, wantErr: "agentxLimits.maxOutputBufferBytesPerProcess"},
		{name: "checkpoint allowlist", mutate: func(m *Manifest) { m.CheckpointAllowlistVersion = 0 }, wantErr: "checkpointAllowlistVersion"},
		{name: "protocol", mutate: func(m *Manifest) { m.AgentxProtocolVersion = "v2" }, wantErr: "agentxProtocolVersion"},
		{name: "platform", mutate: func(m *Manifest) {
			artifact := m.Artifacts["linux-amd64"]
			delete(m.Artifacts, "linux-amd64")
			m.Artifacts["plan9-amd64"] = artifact
		}, wantErr: "not supported"},
		{name: "external executables missing", mutate: func(m *Manifest) {
			artifact := m.Artifacts["linux-amd64"]
			artifact.ExternalExecutables = nil
			m.Artifacts["linux-amd64"] = artifact
		}, wantErr: "externalExecutables must be present"},
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
			executable := artifact.ExternalExecutables["bwrap"]
			executable.Path = artifact.Codex.Path
			artifact.ExternalExecutables["bwrap"] = executable
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
		Definitions          map[string]json.RawMessage `json:"$defs"`
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
		"agentxLimits",
		"agentxProtocolVersion",
		"appServerSchemaDigestAlgorithm",
		"appServerSchemaSha256",
		"artifacts",
		"checkpointAllowlistVersion",
		"codexCommit",
		"codexRelease",
		"execProtocolSourceSha256",
		"execServerBounds",
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

	type objectDefinition struct {
		AdditionalProperties *bool                      `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	assertClosedDefinition := func(name string, wantFields []string) {
		t.Helper()
		var definition objectDefinition
		if err := json.Unmarshal(schema.Definitions[name], &definition); err != nil {
			t.Fatalf("decode %s schema: %v", name, err)
		}
		if definition.AdditionalProperties == nil || *definition.AdditionalProperties {
			t.Fatalf("%s schema must fail closed on unknown properties", name)
		}
		sort.Strings(definition.Required)
		sort.Strings(wantFields)
		if strings.Join(definition.Required, "\n") != strings.Join(wantFields, "\n") {
			t.Fatalf("%s required fields = %v, want %v", name, definition.Required, wantFields)
		}
		propertyNames := make([]string, 0, len(definition.Properties))
		for propertyName := range definition.Properties {
			propertyNames = append(propertyNames, propertyName)
		}
		sort.Strings(propertyNames)
		if strings.Join(propertyNames, "\n") != strings.Join(wantFields, "\n") {
			t.Fatalf("%s properties = %v, want exactly %v", name, propertyNames, wantFields)
		}
	}
	assertClosedDefinition("execServerBounds", []string{
		"argvEnvLimit",
		"exitedProcessRetentionMilliseconds",
		"maxJsonValues",
		"maxStdioFrameBytes",
		"retainedOutputBytesPerProcess",
		"retainedOutputChunksPerProcess",
		"retainedStdinWriteIdsPerProcess",
	})
	assertClosedDefinition("agentxLimits", []string{
		"maxArgvBytes",
		"maxArgvElements",
		"maxEnvBytes",
		"maxEnvVariables",
		"maxFrameBytes",
		"maxJsonValues",
		"maxOutputBufferBytesPerProcess",
		"maxWriteIdBytes",
	})

	var platformArtifacts struct {
		AdditionalProperties *bool                      `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema.Definitions["platformArtifacts"], &platformArtifacts); err != nil {
		t.Fatalf("decode platformArtifacts schema: %v", err)
	}
	if platformArtifacts.AdditionalProperties == nil || *platformArtifacts.AdditionalProperties {
		t.Fatal("platformArtifacts schema must fail closed on unknown properties")
	}
	wantPlatformFields := []string{"codex", "externalExecutables"}
	sort.Strings(platformArtifacts.Required)
	if strings.Join(platformArtifacts.Required, "\n") != strings.Join(wantPlatformFields, "\n") {
		t.Fatalf("platformArtifacts required fields = %v, want %v", platformArtifacts.Required, wantPlatformFields)
	}
	platformPropertyNames := make([]string, 0, len(platformArtifacts.Properties))
	for name := range platformArtifacts.Properties {
		platformPropertyNames = append(platformPropertyNames, name)
	}
	sort.Strings(platformPropertyNames)
	if strings.Join(platformPropertyNames, "\n") != strings.Join(wantPlatformFields, "\n") {
		t.Fatalf("platformArtifacts properties = %v, want exactly %v", platformPropertyNames, wantPlatformFields)
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
		ExecServerBounds: ExecServerBounds{
			MaxStdioFrameBytes:                 64 * 1024 * 1024,
			MaxJSONValues:                      256 * 1024,
			ArgvEnvLimit:                       ArgvEnvLimitTransportAndPlatformOnly,
			RetainedOutputBytesPerProcess:      1024 * 1024,
			RetainedOutputChunksPerProcess:     50_000,
			RetainedStdinWriteIDsPerProcess:    4096,
			ExitedProcessRetentionMilliseconds: 30_000,
		},
		AgentxLimits: AgentxLimits{
			MaxFrameBytes:                  8 * 1024 * 1024,
			MaxJSONValues:                  64 * 1024,
			MaxArgvElements:                256,
			MaxArgvBytes:                   16 * 1024,
			MaxEnvVariables:                256,
			MaxEnvBytes:                    16 * 1024,
			MaxWriteIDBytes:                128,
			MaxOutputBufferBytesPerProcess: 8 * 1024 * 1024,
		},
		CheckpointAllowlistVersion: 1,
		AgentxProtocolVersion:      "2.0",
		Artifacts: map[string]PlatformArtifacts{
			"linux-amd64": {
				Codex: FileArtifact{
					Path:      "bin/codex",
					SourceURL: "https://github.com/openai/codex/releases/download/rust-v0.145.0/codex-x86_64-unknown-linux-musl.tar.gz",
					SHA256:    strings.Repeat("d", 64),
					SizeBytes: 1024,
				},
				ExternalExecutables: map[string]FileArtifact{
					"bwrap": {
						Path:      "codex-resources/bwrap",
						SourceURL: "https://github.com/openai/codex/releases/download/rust-v0.145.0/codex-x86_64-unknown-linux-musl.tar.gz",
						SHA256:    strings.Repeat("e", 64),
						SizeBytes: 512,
					},
				},
			},
		},
	}
}
