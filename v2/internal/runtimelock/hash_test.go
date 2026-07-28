package runtimelock

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHashTreeUsesCanonicalSortedRecords(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "z.json", "last")
	writeFile(t, root, "nested/a.json", "first")

	digest, err := HashTree(root, DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(digest.Files) != 2 || digest.Files[0].Path != "nested/a.json" || digest.Files[1].Path != "z.json" {
		t.Fatalf("tree files are not sorted: %+v", digest.Files)
	}
	records := ""
	for _, entry := range digest.Files {
		records += fmt.Sprintf("%s  %s\n", entry.SHA256, entry.Path)
	}
	wantBytes := sha256.Sum256([]byte(records))
	want := hex.EncodeToString(wantBytes[:])
	if digest.SHA256 != want {
		t.Fatalf("tree SHA-256 = %s, want %s", digest.SHA256, want)
	}
}

func TestHashCanonicalJSONTreeNormalizesObjectOrder(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	writeFile(t, firstRoot, "schema.json", `{
  "z": {"second": 2, "first": 1},
  "array": [3, 2, 1],
  "number": 1.0,
  "html": "<tag>&",
  "a": true
}`)
	writeFile(t, secondRoot, "schema.json", `{"a":true,"html":"<tag>&","number":1.0,"array":[3,2,1],"z":{"first":1,"second":2}}`)

	firstRaw, err := HashTree(firstRoot, DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	secondRaw, err := HashTree(secondRoot, DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	if firstRaw.SHA256 == secondRaw.SHA256 {
		t.Fatal("raw tree digest unexpectedly ignored JSON representation")
	}
	firstCanonical, err := HashCanonicalJSONTree(firstRoot, DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	secondCanonical, err := HashCanonicalJSONTree(secondRoot, DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	if firstCanonical.SHA256 != secondCanonical.SHA256 {
		t.Fatalf("canonical tree digests differ: %s != %s", firstCanonical.SHA256, secondCanonical.SHA256)
	}
	canonicalJSON := `{"a":true,"array":[3,2,1],"html":"\u003ctag\u003e\u0026","number":1.0,"z":{"first":1,"second":2}}`
	fileHash := sha256.Sum256([]byte(canonicalJSON))
	if got, want := firstCanonical.Files[0].SHA256, hex.EncodeToString(fileHash[:]); got != want {
		t.Fatalf("canonical file digest = %s, want %s", got, want)
	}
}

func TestHashCanonicalJSONTreeRejectsDuplicateKeysAndNonJSON(t *testing.T) {
	duplicateRoot := t.TempDir()
	writeFile(t, duplicateRoot, "schema.json", `{"type":"object","type":"array"}`)
	if _, err := HashCanonicalJSONTree(duplicateRoot, DefaultTreeLimits()); err == nil || !strings.Contains(err.Error(), "duplicate object key") {
		t.Fatalf("duplicate-key error = %v", err)
	}

	nonJSONRoot := t.TempDir()
	writeFile(t, nonJSONRoot, "README.txt", "not schema JSON")
	if _, err := HashCanonicalJSONTree(nonJSONRoot, DefaultTreeLimits()); err == nil || !strings.Contains(err.Error(), "non-JSON") {
		t.Fatalf("non-JSON error = %v", err)
	}
}

func TestHashTreeEnforcesBoundsAndRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "large.json", "12345")
	_, err := HashTree(root, TreeLimits{MaxFiles: 1, MaxFileBytes: 4, MaxTotalBytes: 10})
	if err == nil || !strings.Contains(err.Error(), "exceeds 4 bytes") {
		t.Fatalf("HashTree() error = %v, want per-file bound", err)
	}

	if runtime.GOOS == "windows" {
		return
	}
	linkRoot := t.TempDir()
	target := filepath.Join(linkRoot, "target")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(linkRoot, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := HashTree(linkRoot, DefaultTreeLimits()); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("HashTree() symlink error = %v", err)
	}
}

func TestVerifyPlatformChecksDigestSizeAndSymlink(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "bin/codex", "stock-codex")
	writeFile(t, root, "bin/codex-linux-sandbox", "sandbox-helper")
	for _, executable := range []string{"codex", "codex-linux-sandbox"} {
		if err := os.Chmod(filepath.Join(root, "bin", executable), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	codexDigest, codexSize, err := HashFile(filepath.Join(root, "bin", "codex"))
	if err != nil {
		t.Fatal(err)
	}
	helperDigest, helperSize, err := HashFile(filepath.Join(root, "bin", "codex-linux-sandbox"))
	if err != nil {
		t.Fatal(err)
	}

	manifest := validManifest()
	artifacts := manifest.Artifacts["linux-amd64"]
	artifacts.Codex.SHA256 = codexDigest
	artifacts.Codex.SizeBytes = codexSize
	helper := artifacts.Helpers["codex-linux-sandbox"]
	helper.SHA256 = helperDigest
	helper.SizeBytes = helperSize
	artifacts.Helpers["codex-linux-sandbox"] = helper
	manifest.Artifacts["linux-amd64"] = artifacts

	verified, err := manifest.VerifyPlatform(root, "linux-amd64")
	if err != nil {
		t.Fatalf("VerifyPlatform() error = %v", err)
	}
	if verified.Codex.SHA256 != codexDigest || verified.Helpers["codex-linux-sandbox"].SHA256 != helperDigest {
		t.Fatalf("verified runtime = %+v", verified)
	}

	badDigest := manifest
	badArtifacts := badDigest.Artifacts["linux-amd64"]
	badArtifacts.Codex.SHA256 = strings.Repeat("0", 64)
	badDigest.Artifacts = map[string]PlatformArtifacts{"linux-amd64": badArtifacts}
	if _, err := badDigest.VerifyPlatform(root, "linux-amd64"); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("VerifyPlatform() digest error = %v", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(root, "bin", "codex"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := manifest.VerifyPlatform(root, "linux-amd64"); err == nil || !strings.Contains(err.Error(), "not executable") {
			t.Fatalf("VerifyPlatform() executable-mode error = %v", err)
		}
		if err := os.Chmod(filepath.Join(root, "bin", "codex"), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	if runtime.GOOS == "windows" {
		return
	}
	linkPath := filepath.Join(root, "bin", "codex-link")
	if err := os.Symlink(filepath.Join(root, "bin", "codex"), linkPath); err != nil {
		t.Fatal(err)
	}
	linked := manifest
	linkedArtifacts := linked.Artifacts["linux-amd64"]
	linkedArtifacts.Codex.Path = "bin/codex-link"
	linked.Artifacts = map[string]PlatformArtifacts{"linux-amd64": linkedArtifacts}
	if _, err := linked.VerifyPlatform(root, "linux-amd64"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("VerifyPlatform() symlink error = %v", err)
	}
}

func TestCurrentPlatformUsesGoRuntimeIdentity(t *testing.T) {
	if got, want := CurrentPlatform(), runtime.GOOS+"-"+runtime.GOARCH; got != want {
		t.Fatalf("CurrentPlatform() = %q, want %q", got, want)
	}
}

func writeFile(t *testing.T, root, relative, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
