package checkpoint

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/ucarion/jcs"
)

func TestCheckpointManifestCanonicalDigestAndResumeAuthority(t *testing.T) {
	manifest, _ := checkpointTestManifest()
	manifest.PackSetDigest = strings.Repeat("c", 64)
	canonical, err := CanonicalBytes(manifest)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseCanonical(canonical)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := Digest(canonical)
	if err != nil {
		t.Fatal(err)
	}
	authority := checkpointTestAuthority(manifest, digest)
	if err := VerifyResume(parsed, canonical, authority); err != nil {
		t.Fatal(err)
	}
	authority.RunAttemptGeneration++
	if err := VerifyResume(parsed, canonical, authority); err == nil || !strings.Contains(err.Error(), "resume authority") {
		t.Fatalf("generation drift error = %v", err)
	}
	authority = checkpointTestAuthority(manifest, strings.Repeat("0", 64))
	if err := VerifyResume(parsed, canonical, authority); err == nil || !strings.Contains(err.Error(), "committed manifest digest") {
		t.Fatalf("manifest digest drift error = %v", err)
	}
	authority = checkpointTestAuthority(manifest, digest)
	authority.PackSetDigest = strings.Repeat("d", 64)
	if err := VerifyResume(parsed, canonical, authority); err == nil || !strings.Contains(err.Error(), "resume authority") {
		t.Fatalf("pack-set drift error = %v", err)
	}
}

func TestCheckpointManifestRejectsNonCanonicalUnknownAndUnsafeFiles(t *testing.T) {
	manifest, _ := checkpointTestManifest()
	manifest.PackSetDigest = strings.Repeat("c", 64)
	canonical, err := CanonicalBytes(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCanonical(append(append([]byte(nil), canonical...), '\n')); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("non-canonical error = %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(canonical, &value); err != nil {
		t.Fatal(err)
	}
	value["futureAuthority"] = true
	unknown, err := jcs.Append(nil, value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCanonical(unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Manifest)
		want   string
	}{
		{name: "path escape", mutate: func(m *Manifest) { m.Files[0].Path = "../rollout.jsonl" }, want: "path"},
		{name: "absolute path", mutate: func(m *Manifest) { m.Files[0].Path = "/sessions/rollout.jsonl" }, want: "path"},
		{name: "symlink", mutate: func(m *Manifest) { m.Files[0].FileType = "symlink" }, want: "fileType"},
		{name: "extra file", mutate: func(m *Manifest) { m.Files = append(m.Files, m.Files[0]) }, want: "exactly one"},
		{name: "runtime allowlist", mutate: func(m *Manifest) { m.CheckpointAllowlistVersion = 2 }, want: "not implemented"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := manifest
			candidate.Files = append([]File(nil), manifest.Files...)
			test.mutate(&candidate)
			if _, err := CanonicalBytes(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("CanonicalBytes() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCheckpointManifestJSONSchema(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate checkpoint package")
	}
	rawSchema, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "..", "..", "api", "schema", "checkpoint-manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		t.Fatalf("checkpoint manifest schema is invalid JSON: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve checkpoint manifest schema: %v", err)
	}
	manifest, _ := checkpointTestManifest()
	raw, err := CanonicalBytes(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(value); err != nil {
		t.Fatalf("valid checkpoint manifest rejected by schema: %v", err)
	}
}

func checkpointTestManifest() (Manifest, []byte) {
	rollout := []byte("{\"type\":\"session_meta\",\"payload\":{\"id\":\"thread-checkpoint\"}}\n{\"type\":\"turn_context\"}\n")
	digest := sha256.Sum256(rollout)
	return Manifest{
		ManifestVersion: CurrentManifestVersion, CanonicalizerVersion: Canonicalizer,
		CheckpointID: "51000000-0000-4000-8000-000000000005",
		WorkspaceID:  "52000000-0000-4000-8000-000000000005",
		SessionID:    "53000000-0000-4000-8000-000000000005",
		RunID:        "54000000-0000-4000-8000-000000000005",
		RunAttemptID: "55000000-0000-4000-8000-000000000005", RunAttemptGeneration: 7,
		BrainThreadID: "thread-checkpoint", TerminalTurnID: "turn-checkpoint",
		CodexRuntimeManifestDigest: strings.Repeat("a", 64), CheckpointAllowlistVersion: 1,
		CatalogDigest: strings.Repeat("b", 64),
		Files: []File{{
			Purpose: RolloutPurpose, FileType: RegularFileType,
			Path: "sessions/2026/07/31/rollout-thread-checkpoint.jsonl", Mode: RolloutMode,
			SizeBytes: int64(len(rollout)), SHA256: hex.EncodeToString(digest[:]),
		}},
	}, rollout
}

func checkpointTestAuthority(manifest Manifest, digest string) ResumeAuthority {
	return ResumeAuthority{
		ManifestDigest: digest, CheckpointID: manifest.CheckpointID,
		WorkspaceID: manifest.WorkspaceID, SessionID: manifest.SessionID,
		RunID: manifest.RunID, RunAttemptID: manifest.RunAttemptID,
		RunAttemptGeneration: manifest.RunAttemptGeneration,
		BrainThreadID:        manifest.BrainThreadID, TerminalTurnID: manifest.TerminalTurnID,
		CodexRuntimeManifestDigest: manifest.CodexRuntimeManifestDigest,
		CheckpointAllowlistVersion: manifest.CheckpointAllowlistVersion,
		CatalogDigest:              manifest.CatalogDigest,
		PackSetDigest:              manifest.PackSetDigest,
	}
}

func mustCheckpointCanonical(t *testing.T, manifest Manifest) []byte {
	t.Helper()
	value, err := CanonicalBytes(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Clone(value)
}
