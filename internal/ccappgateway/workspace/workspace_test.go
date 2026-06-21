package workspace

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
)

// TestSetupCreatesSubdirs tests that Setup creates both ClaudeDir and ProjectDir with 0700 perms.
func TestSetupCreatesSubdirs(t *testing.T) {
	tmpRoot := t.TempDir()
	ctx := context.Background()

	ws, err := Setup(ctx, tmpRoot)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Check TempDir exists
	if _, err := os.Stat(ws.TempDir); err != nil {
		t.Errorf("TempDir does not exist: %v", err)
	}

	// Check ClaudeDir exists and is a directory
	clauseDirInfo, err := os.Stat(ws.ClaudeDir)
	if err != nil {
		t.Errorf("ClaudeDir does not exist: %v", err)
	}
	if !clauseDirInfo.IsDir() {
		t.Errorf("ClaudeDir is not a directory")
	}
	if clauseDirInfo.Mode().Perm() != 0700 {
		t.Errorf("ClaudeDir perms: want 0700, got %o", clauseDirInfo.Mode().Perm())
	}

	// Check ProjectDir exists and is a directory
	projDirInfo, err := os.Stat(ws.ProjectDir)
	if err != nil {
		t.Errorf("ProjectDir does not exist: %v", err)
	}
	if !projDirInfo.IsDir() {
		t.Errorf("ProjectDir is not a directory")
	}
	if projDirInfo.Mode().Perm() != 0700 {
		t.Errorf("ProjectDir perms: want 0700, got %o", projDirInfo.Mode().Perm())
	}

	// Cleanup
	ws.Teardown()
}

// TestSetupCreatesTmpRootIfMissing tests that Setup creates tmpRoot if it doesn't exist.
func TestSetupCreatesTmpRootIfMissing(t *testing.T) {
	tmpRoot := filepath.Join(t.TempDir(), "subroot")
	// Verify subroot doesn't exist yet
	if _, err := os.Stat(tmpRoot); err == nil {
		t.Fatalf("subroot should not exist before test")
	}

	ctx := context.Background()
	ws, err := Setup(ctx, tmpRoot)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Check tmpRoot now exists
	if _, err := os.Stat(tmpRoot); err != nil {
		t.Errorf("tmpRoot was not created: %v", err)
	}

	// Check both subdirs exist
	if _, err := os.Stat(ws.ClaudeDir); err != nil {
		t.Errorf("ClaudeDir does not exist: %v", err)
	}
	if _, err := os.Stat(ws.ProjectDir); err != nil {
		t.Errorf("ProjectDir does not exist: %v", err)
	}

	// Cleanup
	ws.Teardown()
}

// TestTeardownRemovesDir tests that Teardown removes the TempDir.
func TestTeardownRemovesDir(t *testing.T) {
	tmpRoot := t.TempDir()
	ctx := context.Background()

	ws, err := Setup(ctx, tmpRoot)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	tempDir := ws.TempDir
	if _, err := os.Stat(tempDir); err != nil {
		t.Fatalf("TempDir should exist before Teardown: %v", err)
	}

	if err := ws.Teardown(); err != nil {
		t.Fatalf("Teardown failed: %v", err)
	}

	if _, err := os.Stat(tempDir); err == nil {
		t.Errorf("TempDir was not removed")
	} else if !os.IsNotExist(err) {
		t.Errorf("Stat returned unexpected error: %v", err)
	}
}

// TestDoubleTeardownIsNoop tests that calling Teardown twice is safe.
func TestDoubleTeardownIsNoop(t *testing.T) {
	tmpRoot := t.TempDir()
	ctx := context.Background()

	ws, err := Setup(ctx, tmpRoot)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// First teardown should succeed
	if err := ws.Teardown(); err != nil {
		t.Fatalf("First Teardown failed: %v", err)
	}

	// Second teardown should also succeed (no-op)
	if err := ws.Teardown(); err != nil {
		t.Fatalf("Second Teardown should be a no-op and not error: %v", err)
	}
}

// TestConcurrentSetupNoDuplicates tests that two concurrent Setup calls don't collide.
func TestConcurrentSetupNoDuplicates(t *testing.T) {
	tmpRoot := t.TempDir()
	ctx := context.Background()

	var wg sync.WaitGroup
	var ws1, ws2 *Workspace
	var err1, err2 error

	wg.Add(2)

	go func() {
		defer wg.Done()
		ws1, err1 = Setup(ctx, tmpRoot)
	}()

	go func() {
		defer wg.Done()
		ws2, err2 = Setup(ctx, tmpRoot)
	}()

	wg.Wait()

	if err1 != nil {
		t.Fatalf("Setup 1 failed: %v", err1)
	}
	if err2 != nil {
		t.Fatalf("Setup 2 failed: %v", err2)
	}

	// TempDirs should be distinct
	if ws1.TempDir == ws2.TempDir {
		t.Errorf("TempDirs are identical: %s == %s", ws1.TempDir, ws2.TempDir)
	}

	// Both should exist
	if _, err := os.Stat(ws1.TempDir); err != nil {
		t.Errorf("Workspace 1 TempDir does not exist: %v", err)
	}
	if _, err := os.Stat(ws2.TempDir); err != nil {
		t.Errorf("Workspace 2 TempDir does not exist: %v", err)
	}

	// Cleanup
	ws1.Teardown()
	ws2.Teardown()
}

// TestSetupUUIDIsCanonical tests that the UUID portion of TempDir matches RFC 4122 v4 format.
func TestSetupUUIDIsCanonical(t *testing.T) {
	tmpRoot := t.TempDir()
	ctx := context.Background()

	ws, err := Setup(ctx, tmpRoot)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Extract the UUID from TempDir (it's the final path segment)
	uuid := filepath.Base(ws.TempDir)

	// RFC 4122 v4 format: 8 hex digits, dash, 4 hex, dash, 4 hex (version 4),
	// dash, 4 hex (variant 10), dash, 12 hex
	canonicalRe := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !canonicalRe.MatchString(uuid) {
		t.Errorf("UUID format is not canonical RFC 4122 v4: got %q", uuid)
	}

	// Cleanup
	ws.Teardown()
}
