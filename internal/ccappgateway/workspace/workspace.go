// Package workspace manages ephemeral per-turn local directories for claude execution.
// Phase 1 provides basic setup/teardown (mkdir + cleanup); Phase 2 will add S3 persistence.
package workspace

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
)

// Workspace is an ephemeral per-turn local view.
// Phase 1 is mkdir+cleanup only; Phase 2 adds S3 download/upload.
type Workspace struct {
	TempDir    string // /tmpRoot/<uuid>/
	ClaudeDir  string // /tmpRoot/<uuid>/claude-home/
	ProjectDir string // /tmpRoot/<uuid>/project/
}

// Setup creates a fresh tmpdir under tmpRoot with claude-home + project subdirs
// (0700 perms each, and the parent tmpRoot is created with 0755 if missing).
// Returns the populated Workspace. Caller MUST call Teardown when done.
//
// The ctx parameter is accepted for future use (e.g. S3 download in Phase 2);
// Phase 1 doesn't use it but the signature must include it.
func Setup(ctx context.Context, tmpRoot string) (*Workspace, error) {
	// Create tmpRoot if it doesn't exist (with 0755 perms)
	if err := os.MkdirAll(tmpRoot, 0755); err != nil {
		return nil, fmt.Errorf("failed to create tmpRoot: %w", err)
	}

	// Generate a UUIDv4 for the temp directory
	uuid := newUUID()

	// Create the workspace temp directory
	tempDir := filepath.Join(tmpRoot, uuid)
	if err := os.Mkdir(tempDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create TempDir: %w", err)
	}

	// Create claude-home subdirectory with 0700 perms
	claudeDir := filepath.Join(tempDir, "claude-home")
	if err := os.Mkdir(claudeDir, 0700); err != nil {
		_ = os.RemoveAll(tempDir) // cleanup on error
		return nil, fmt.Errorf("failed to create ClaudeDir: %w", err)
	}

	// Create project subdirectory with 0700 perms
	projectDir := filepath.Join(tempDir, "project")
	if err := os.Mkdir(projectDir, 0700); err != nil {
		_ = os.RemoveAll(tempDir) // cleanup on error
		return nil, fmt.Errorf("failed to create ProjectDir: %w", err)
	}

	return &Workspace{
		TempDir:    tempDir,
		ClaudeDir:  claudeDir,
		ProjectDir: projectDir,
	}, nil
}

// Teardown removes the entire TempDir. Idempotent: a second call is a no-op
// and never returns an error.
func (w *Workspace) Teardown() error {
	if w == nil || w.TempDir == "" {
		return nil
	}

	// Check if it exists before attempting removal
	_, err := os.Stat(w.TempDir)
	if err != nil {
		if os.IsNotExist(err) {
			// Already removed, so this is a no-op (no error)
			return nil
		}
		// Some other error occurred
		return err
	}

	// Remove the entire tree
	return os.RemoveAll(w.TempDir)
}

// newUUID generates a UUIDv4 using crypto/rand.
// Based on RFC 4122 v4.
func newUUID() string {
	var b [16]byte
	_, err := rand.Read(b[:])
	if err != nil {
		panic(err)
	}
	// RFC 4122 v4
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
