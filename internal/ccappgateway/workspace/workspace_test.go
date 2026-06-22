package workspace_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/ccappgateway/workspace"
)

func TestSetupCreatesPerSessionSubdirs(t *testing.T) {
	tmpRoot := t.TempDir()
	store := newFakeStore()
	ws, err := workspace.Setup(context.Background(), tmpRoot, "ws_abc", "sid_123", store)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	wantTemp := filepath.Join(tmpRoot, "ws_abc", "sid_123")
	if ws.TempDir != wantTemp {
		t.Errorf("TempDir: got %q, want %q", ws.TempDir, wantTemp)
	}
	if ws.ClaudeDir != filepath.Join(wantTemp, "claude-home") {
		t.Errorf("ClaudeDir wrong: %q", ws.ClaudeDir)
	}
	if ws.ProjectDir != filepath.Join(wantTemp, "project") {
		t.Errorf("ProjectDir wrong: %q", ws.ProjectDir)
	}
	if ws.IsResume {
		t.Error("IsResume should be false on first Setup (empty store)")
	}
	// Confirm directories actually exist.
	for _, d := range []string{ws.ClaudeDir, ws.ProjectDir} {
		info, err := os.Stat(d)
		if err != nil {
			t.Errorf("dir %q missing: %v", d, err)
		} else if !info.IsDir() {
			t.Errorf("%q is not a dir", d)
		} else if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("%q perm: got %#o, want 0700", d, perm)
		}
	}
}

func TestSetupHitDownloadsTarball(t *testing.T) {
	tmpRoot := t.TempDir()
	store := newFakeStore()
	// Seed store with a tarball containing one file under projects/.
	// Use TarUpload from Task 1 to create the seed naturally.
	seedDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(seedDir, "projects", "-tmp-x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "projects", "-tmp-x", "sid_123.jsonl"), []byte("seeded-jsonl-row\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := "cc-app-gateway/ws_abc/sid_123.tar.gz"
	if err := workspace.TarUpload(context.Background(), store, key, seedDir); err != nil {
		t.Fatalf("seed TarUpload: %v", err)
	}

	ws, err := workspace.Setup(context.Background(), tmpRoot, "ws_abc", "sid_123", store)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if !ws.IsResume {
		t.Error("IsResume should be true on Setup hit")
	}
	// Confirm seeded file was untarred into ClaudeDir.
	got, err := os.ReadFile(filepath.Join(ws.ClaudeDir, "projects", "-tmp-x", "sid_123.jsonl"))
	if err != nil {
		t.Fatalf("seeded jsonl missing: %v", err)
	}
	if string(got) != "seeded-jsonl-row\n" {
		t.Errorf("seeded content lost: %q", got)
	}
}

func TestSetupNonNotFoundErrorReturned(t *testing.T) {
	// Use an ObjectStore that returns a non-NotFound error on Get.
	errStore := &errorStore{err: errors.New("network unreachable")}
	_, err := workspace.Setup(context.Background(), t.TempDir(), "ws", "sid", errStore)
	if err == nil {
		t.Fatal("Setup should return error on non-NotFound S3 failure")
	}
	if !strings.Contains(err.Error(), "network unreachable") {
		t.Errorf("error should wrap S3 err, got: %v", err)
	}
}

func TestTeardownPrunesBackupsBeforeUpload(t *testing.T) {
	tmpRoot := t.TempDir()
	store := newFakeStore()
	ws, err := workspace.Setup(context.Background(), tmpRoot, "ws_abc", "sid_123", store)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate claude writing a backup file.
	backupDir := filepath.Join(ws.ClaudeDir, "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, ".claude.json.backup.123"), []byte("backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Also put a real file in projects/ so the tarball has something.
	pdir := filepath.Join(ws.ClaudeDir, "projects", "-tmp-x")
	if err := os.MkdirAll(pdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "sid_123.jsonl"), []byte("line\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ws.Teardown(context.Background(), store); err != nil {
		t.Fatalf("Teardown: %v", err)
	}

	// Untar the stored tarball into a verify dir, assert no backups/ entry.
	verifyDir := t.TempDir()
	key := "cc-app-gateway/ws_abc/sid_123.tar.gz"
	if err := workspace.TarDownload(context.Background(), store, key, verifyDir); err != nil {
		t.Fatalf("verify TarDownload: %v", err)
	}
	if _, err := os.Stat(filepath.Join(verifyDir, "backups")); !os.IsNotExist(err) {
		t.Errorf("backups/ should be absent from tarball, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(verifyDir, "projects", "-tmp-x", "sid_123.jsonl")); err != nil {
		t.Errorf("projects/ content lost: %v", err)
	}
	// Confirm TempDir was removed.
	if _, err := os.Stat(ws.TempDir); !os.IsNotExist(err) {
		t.Errorf("TempDir should be removed, stat err=%v", err)
	}
}

func TestTeardownIdempotent(t *testing.T) {
	tmpRoot := t.TempDir()
	store := newFakeStore()
	ws, err := workspace.Setup(context.Background(), tmpRoot, "ws_abc", "sid_123", store)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Teardown(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	// Second Teardown should not error.
	if err := ws.Teardown(context.Background(), store); err != nil {
		t.Errorf("second Teardown should be no-op, got: %v", err)
	}
}

// errorStore is an ObjectStore whose Get always fails with the wrapped error.
type errorStore struct{ err error }

func (e *errorStore) Get(_ context.Context, _ string) ([]byte, error) { return nil, e.err }
func (e *errorStore) Put(_ context.Context, _ string, _ []byte) error { return e.err }
func (e *errorStore) Delete(_ context.Context, _ string) error        { return e.err }

func TestSetupRejectsPathTraversalDefenseInDepth(t *testing.T) {
	// Even if the caller bypassed turn_api.go validation, Setup itself
	// refuses to mkdir a path that escapes tmpRoot.
	store := newFakeStore()
	_, err := workspace.Setup(context.Background(), t.TempDir(), "../escape", "00000000-0000-4000-8000-000000000001", store)
	if err == nil {
		t.Fatal("Setup should reject workspaceID with path traversal")
	}
	if !strings.Contains(err.Error(), "escapes tmpRoot") {
		t.Errorf("error should mention path traversal; got: %v", err)
	}
}
