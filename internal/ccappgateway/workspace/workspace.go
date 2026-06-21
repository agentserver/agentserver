// Package workspace manages ephemeral per-turn local directories for claude execution.
// Phase 2 adds S3 persistence: Setup downloads prior turn's claude-home tarball,
// Teardown tars + uploads + cleans up.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Workspace is an ephemeral per-turn local view of a session's state.
// The TempDir/ClaudeDir/ProjectDir layout is keyed on (workspaceID, sessionID)
// so the layout is STABLE across turns — load-bearing for claude's --resume
// to find its prior jsonl (claude's cwd sanitization makes the project
// subdir name a function of cmd.Dir; if cmd.Dir varied per turn, --resume
// would silently fail).
type Workspace struct {
	TempDir    string // <tmpRoot>/<workspaceID>/<sessionID>/
	ClaudeDir  string // <tmpRoot>/<workspaceID>/<sessionID>/claude-home/
	ProjectDir string // <tmpRoot>/<workspaceID>/<sessionID>/project/
	IsResume   bool   // true if S3 had a tarball for this session — runner uses --resume

	workspaceID string // for s3Key()
	sessionID   string // for s3Key()
}

// Setup creates a fresh tmpdir under tmpRoot at <tmpRoot>/<workspaceID>/<sessionID>/
// with claude-home + project subdirs (0700 perms). Then tries to download a
// prior tarball from store at the session's key:
//   - Hit (no error):                IsResume = true, ClaudeDir populated
//   - Miss (ErrObjectNotFound):      IsResume = false, ClaudeDir empty
//   - Other error (network, etc.):   returns the error wrapped — caller decides
//                                    whether to fail the turn or graceful-degrade
//
// Caller MUST call Teardown when done (preferably in a deferred goroutine
// per spec § Concurrency).
func Setup(ctx context.Context, tmpRoot, workspaceID, sessionID string, store ObjectStore) (*Workspace, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace.Setup: workspaceID required")
	}
	if sessionID == "" {
		return nil, fmt.Errorf("workspace.Setup: sessionID required")
	}
	// Create tmpRoot if missing (0755) — parent of per-session subtree.
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir tmpRoot: %w", err)
	}

	tempDir := filepath.Join(tmpRoot, workspaceID, sessionID)
	claudeDir := filepath.Join(tempDir, "claude-home")
	projectDir := filepath.Join(tempDir, "project")
	for _, d := range []string{claudeDir, projectDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			// Best-effort cleanup of partial state.
			_ = os.RemoveAll(tempDir)
			return nil, fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	ws := &Workspace{
		TempDir:     tempDir,
		ClaudeDir:   claudeDir,
		ProjectDir:  projectDir,
		workspaceID: workspaceID,
		sessionID:   sessionID,
	}

	// Try to download a prior tarball.
	if err := TarDownload(ctx, store, ws.s3Key(), claudeDir); err != nil {
		if errors.Is(err, ErrObjectNotFound) {
			ws.IsResume = false
			return ws, nil
		}
		// Non-NotFound: cleanup tmpdir and surface error.
		_ = os.RemoveAll(tempDir)
		return nil, fmt.Errorf("download prior tarball: %w", err)
	}
	ws.IsResume = true
	return ws, nil
}

// Teardown prunes <ClaudeDir>/backups/ (claude writes a backup per spawn that
// would otherwise grow the tarball file count linearly), tars + gzips
// ClaudeDir, uploads to store at the session's key (OVERWRITES previous
// tarball — last-write-wins by design; cross-pod race deferred to Phase 5+),
// then removes the TempDir.
//
// Idempotent: a second call after TempDir is gone returns nil.
//
// Error precedence: if any step fails, the TempDir is STILL removed
// (fail-open) and the first error is returned. Caller logs and continues
// serving — losing one turn's history is better than failing every subsequent
// turn because of stale tmpdir.
func (w *Workspace) Teardown(ctx context.Context, store ObjectStore) error {
	if w == nil || w.TempDir == "" {
		return nil
	}
	// Idempotent: if TempDir is gone, nothing to do.
	if _, err := os.Stat(w.TempDir); os.IsNotExist(err) {
		return nil
	}

	var firstErr error

	// 1. Prune backups/ before tarring (claude's per-spawn config snapshots).
	//    claude re-creates backups/ on next spawn; it's not part of resume state.
	backupsDir := filepath.Join(w.ClaudeDir, "backups")
	if err := os.RemoveAll(backupsDir); err != nil {
		firstErr = fmt.Errorf("prune backups/: %w", err)
		// continue — pruning failure doesn't block tarring
	}

	// 2. Tar + gzip + upload (root = ClaudeDir to capture .claude.json + projects/ + sessions/).
	if err := TarUpload(ctx, store, w.s3Key(), w.ClaudeDir); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("tar+upload: %w", err)
		}
	}

	// 3. Remove TempDir regardless of upload outcome (fail-open).
	if err := os.RemoveAll(w.TempDir); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("remove TempDir: %w", err)
		}
	}

	return firstErr
}

// s3Key returns the S3 object key for this workspace's session tarball.
// Format: cc-app-gateway/<workspaceID>/<sessionID>.tar.gz — pinned forever
// (changing breaks resume for existing sessions).
func (w *Workspace) s3Key() string {
	return fmt.Sprintf("cc-app-gateway/%s/%s.tar.gz", w.workspaceID, w.sessionID)
}
