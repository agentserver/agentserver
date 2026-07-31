package devruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

func TestStockExecProtocolSourceDigestMatchesPinnedRecords(t *testing.T) {
	records := append([]protocolSourceFile(nil), stockExecProtocolSources...)
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	hasher := sha256.New()
	for _, record := range records {
		if !strings.HasPrefix(record.Path, "codex-rs/exec-server-protocol/src/") ||
			len(record.SHA256) != sha256.Size*2 {
			t.Fatalf("invalid protocol source record: %+v", record)
		}
		_, _ = hasher.Write([]byte(record.SHA256 + "  " + record.Path + "\n"))
	}
	if got := hex.EncodeToString(hasher.Sum(nil)); got != stockExecProtocolSHA256 {
		t.Fatalf("protocol source digest = %s, want %s", got, stockExecProtocolSHA256)
	}
}

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
	if len(parsed.Artifacts) != 1 || parsed.CodexRelease != stockCodexRelease ||
		parsed.CodexCommit != stockCodexCommit || parsed.ExecProtocolSourceSHA256 != stockExecProtocolSHA256 {
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
