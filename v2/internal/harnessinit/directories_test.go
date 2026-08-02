package harnessinit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareHarnessDirectoriesPublishesExactModesAndRetries(t *testing.T) {
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
	if uid == 0 || gid == 0 {
		t.Skip("ownership assertion requires an unprivileged test identity")
	}
	root := t.TempDir()
	runtimeRoot := filepath.Join(root, "runtime")
	checkpointRoot := filepath.Join(root, "checkpoint")
	scratchRoot := filepath.Join(root, "scratch")
	for _, path := range []string{runtimeRoot, checkpointRoot, scratchRoot} {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for retry := 0; retry < 2; retry++ {
		if err := PrepareHarnessDirectories(runtimeRoot, checkpointRoot, scratchRoot, uid, gid); err != nil {
			t.Fatalf("retry %d: %v", retry, err)
		}
	}
	for path, want := range map[string]os.FileMode{
		runtimeRoot: harnessRuntimeRootMode, checkpointRoot: harnessPrivateRootMode, scratchRoot: harnessPrivateRootMode,
	} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s mode = %v, %v; want %v", path, info, err, want)
		}
	}
}

func TestPrepareHarnessDirectoriesRejectsIndirectRoot(t *testing.T) {
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
	if uid == 0 || gid == 0 {
		t.Skip("ownership assertion requires an unprivileged test identity")
	}
	root := t.TempDir()
	realRuntime := filepath.Join(root, "real-runtime")
	if err := os.Mkdir(realRuntime, 0o700); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(root, "runtime")
	if err := os.Symlink(realRuntime, runtimeRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	checkpointRoot := filepath.Join(root, "checkpoint")
	scratchRoot := filepath.Join(root, "scratch")
	if err := os.Mkdir(checkpointRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(scratchRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := PrepareHarnessDirectories(runtimeRoot, checkpointRoot, scratchRoot, uid, gid); err == nil {
		t.Fatal("symlinked runtime root was accepted")
	}
}
