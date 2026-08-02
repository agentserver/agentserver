package devruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/runtimelock"
	"github.com/agentserver/agentserver/v2/internal/stockruntime"
)

func TestPrepareRejectsUnpinnedArtifactsWithoutLeavingOutput(t *testing.T) {
	root := canonicalTemporaryDirectory(t)
	codex := writeExecutable(t, root, "codex", []byte("not stock Codex"))
	bwrap := writeExecutable(t, root, "bwrap", []byte("not stock bwrap"))
	output := filepath.Join(root, "runtime")
	_, err := Prepare(PrepareConfig{
		Platform: PlatformLinuxARM64, CodexExecutable: codex,
		BwrapExecutable: bwrap, OutputDirectory: output,
	})
	if err == nil || !strings.Contains(err.Error(), "source size") {
		t.Fatalf("Prepare() error = %v", err)
	}
	if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
		t.Fatalf("failed output remains: %v", statErr)
	}
}

func TestBuiltInLinuxARM64ManifestIsClosedWorld(t *testing.T) {
	manifest := stockLinuxARM64Manifest()
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := marshalManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := runtimelock.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Artifacts) != 1 || parsed.CodexRelease != stockruntime.CodexRelease ||
		parsed.CodexCommit != stockruntime.CodexCommit || parsed.ExecProtocolSourceSHA256 != stockruntime.ExecProtocolSHA256 {
		t.Fatalf("development manifest = %+v", parsed)
	}
	artifacts := parsed.Artifacts[PlatformLinuxARM64]
	if len(artifacts.ExternalExecutables) != 1 || artifacts.Codex.Path != "bin/codex" ||
		artifacts.ExternalExecutables["bwrap"].Path != "codex-resources/bwrap" {
		t.Fatalf("development platform artifacts = %+v", artifacts)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if len(document) != 11 {
		t.Fatalf("manifest top-level fields = %v", document)
	}
}

func canonicalTemporaryDirectory(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func writeExecutable(t *testing.T, root, name string, contents []byte) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, contents, 0o500); err != nil {
		t.Fatal(err)
	}
	return path
}
