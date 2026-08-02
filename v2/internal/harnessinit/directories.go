package harnessinit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	harnessRuntimeRootMode = 0o711
	harnessPrivateRootMode = 0o700
)

// PrepareHarnessDirectories seals the three fresh emptyDir mount roots before
// harness-pool starts. It is exactly retryable after any completed individual
// chmod/chown and never traverses or removes runtime contents.
func PrepareHarnessDirectories(runtimeRoot, checkpointRoot, scratchRoot string, uid, gid uint32) error {
	if uid == 0 || gid == 0 || uid > 1<<31-1 || gid > 1<<31-1 {
		return errors.New("harness directory UID and GID must be unprivileged signed-32-bit identities")
	}
	for _, directory := range []struct {
		label string
		path  string
		mode  os.FileMode
	}{
		{label: "runtime", path: runtimeRoot, mode: harnessRuntimeRootMode},
		{label: "checkpoint", path: checkpointRoot, mode: harnessPrivateRootMode},
		{label: "scratch", path: scratchRoot, mode: harnessPrivateRootMode},
	} {
		if err := prepareHarnessDirectory(directory.label, directory.path, directory.mode, uid, gid); err != nil {
			return err
		}
	}
	return nil
}

func prepareHarnessDirectory(label, path string, mode os.FileMode, uid, gid uint32) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("harness %s directory path must be absolute and clean", label)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect harness %s directory: %w", label, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("harness %s root must be a direct directory", label)
	}
	if info.Mode().Perm() != mode {
		if err := os.Chmod(path, mode); err != nil {
			return fmt.Errorf("set harness %s directory mode: %w", label, err)
		}
	}
	if err := ensureOwnership(path, uid, gid); err != nil {
		return fmt.Errorf("own harness %s directory: %w", label, err)
	}
	verified, err := os.Lstat(path)
	if err != nil || !verified.IsDir() || verified.Mode()&os.ModeSymlink != 0 || verified.Mode().Perm() != mode {
		return fmt.Errorf("verify harness %s directory mode", label)
	}
	if err := verifyOwnership(verified, uid, gid); err != nil {
		return fmt.Errorf("verify harness %s directory ownership: %w", label, err)
	}
	return nil
}
