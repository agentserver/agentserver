//go:build linux

package harnesspool

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/harnesslayout"
)

func TestLocalCheckpointSourceOpensOnlyExactAppOwnedRegularRollout(t *testing.T) {
	attemptRoot := t.TempDir()
	locator := "sessions/2026/07/31/rollout-safe.jsonl"
	rolloutPath := filepath.Join(
		attemptRoot,
		harnesslayout.AppRuntimeDirectory,
		harnesslayout.CodexHomeDirectory,
		filepath.FromSlash(locator),
	)
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o700); err != nil {
		t.Fatal(err)
	}
	contents := []byte("{\"type\":\"session_meta\"}\n")
	if err := os.WriteFile(rolloutPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	anchor, err := openLocalAttemptRuntimeAnchor(attemptRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer anchor.Close()
	expected := LocalProcessCredential{UID: uint32(os.Getuid()), GID: uint32(os.Getgid())}
	rollout, err := openLocalCheckpointRollout(anchor, locator, expected)
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(rollout.Reader)
	closeErr := rollout.Reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(got, contents) || rollout.SizeBytes != int64(len(contents)) {
		t.Fatalf("opened rollout = %q size=%d, errors=%v", got, rollout.SizeBytes, errors.Join(readErr, closeErr))
	}

	wrongOwner := expected
	wrongOwner.UID++
	if _, err := openLocalCheckpointRollout(anchor, locator, wrongOwner); err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("wrong-owner rollout error = %v", err)
	}

	if err := os.Chmod(rolloutPath, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := openLocalCheckpointRollout(anchor, locator, expected); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("wrong-mode rollout error = %v", err)
	}
	if err := os.Chmod(rolloutPath, 0o600); err != nil {
		t.Fatal(err)
	}

	symlinkLocator := "sessions/2026/07/31/rollout-symlink.jsonl"
	symlinkPath := filepath.Join(
		attemptRoot,
		harnesslayout.AppRuntimeDirectory,
		harnesslayout.CodexHomeDirectory,
		filepath.FromSlash(symlinkLocator),
	)
	if err := os.Symlink(filepath.Base(rolloutPath), symlinkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := openLocalCheckpointRollout(anchor, symlinkLocator, expected); err == nil {
		t.Fatal("symlink rollout was accepted")
	}

	hardlinkLocator := "sessions/2026/07/31/rollout-hardlink.jsonl"
	hardlinkPath := filepath.Join(
		attemptRoot,
		harnesslayout.AppRuntimeDirectory,
		harnesslayout.CodexHomeDirectory,
		filepath.FromSlash(hardlinkLocator),
	)
	if err := os.Link(rolloutPath, hardlinkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := openLocalCheckpointRollout(anchor, locator, expected); err == nil || !strings.Contains(err.Error(), "one filesystem link") {
		t.Fatalf("hard-linked rollout error = %v", err)
	}
}
