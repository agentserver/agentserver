package stockruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

func TestExecProtocolSourceDigestMatchesReviewedRecords(t *testing.T) {
	records := ProtocolSources()
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	hasher := sha256.New()
	for _, record := range records {
		if !strings.HasPrefix(record.Path, "codex-rs/exec-server-protocol/src/") ||
			len(record.SHA256) != sha256.Size*2 {
			t.Fatalf("invalid protocol source record: %+v", record)
		}
		_, _ = hasher.Write([]byte(record.SHA256 + "  " + record.Path + "\n"))
	}
	if got := hex.EncodeToString(hasher.Sum(nil)); got != ExecProtocolSHA256 {
		t.Fatalf("protocol source digest = %s, want %s", got, ExecProtocolSHA256)
	}
}

func TestManifestBytesMatchCheckedInProductionArtifact(t *testing.T) {
	raw, err := ManifestBytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtimelock.Parse(raw); err != nil {
		t.Fatalf("parse generated production manifest: %v", err)
	}
	digest := sha256.Sum256(raw)
	if got := hex.EncodeToString(digest[:]); got != ManifestSHA256 {
		t.Fatalf("production runtime manifest SHA-256 = %s, want %s", got, ManifestSHA256)
	}
	if int64(len(raw)) != ManifestSizeBytes {
		t.Fatalf("production runtime manifest size = %d, want %d", len(raw), ManifestSizeBytes)
	}
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate stockruntime package")
	}
	path := filepath.Join(filepath.Dir(current), "..", "..", "packaging", "stockruntime", "runtime-manifest.json")
	checkedIn, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(checkedIn, raw) {
		t.Fatalf("checked-in production runtime manifest differs from stockruntime.ManifestBytes(); regenerate and review %s", path)
	}
}

func TestManifestReturnsDefensiveArtifactMaps(t *testing.T) {
	first := LinuxARM64Manifest()
	artifacts := first.Artifacts[PlatformLinuxARM64]
	delete(artifacts.ExternalExecutables, "bwrap")
	first.Artifacts[PlatformLinuxARM64] = artifacts
	second := LinuxARM64Manifest()
	if len(second.Artifacts) != 1 || len(second.Artifacts[PlatformLinuxARM64].ExternalExecutables) != 1 {
		t.Fatal("LinuxARM64Manifest exposed mutable package state")
	}
}
