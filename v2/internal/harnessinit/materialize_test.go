package harnessinit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializeWorkerFilesPublishesExactDirectProfileAndRetries(t *testing.T) {
	source := projectedMaterialFixture(t, ProfileHarnessWorker)
	parent := t.TempDir()
	destination := filepath.Join(parent, "worker")
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
	if uid == 0 || gid == 0 {
		t.Skip("ownership assertion requires an unprivileged test identity")
	}

	if err := MaterializeWorkerFiles(source, destination, uid, gid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(destination, 0o700) })
	if err := MaterializeWorkerFiles(source, destination, uid, gid); err != nil {
		t.Fatalf("exact materialization retry: %v", err)
	}
	entries, err := os.ReadDir(destination)
	profile := materialProfiles[ProfileHarnessWorker]
	if err != nil || len(entries) != len(profile) {
		t.Fatalf("materialized entries = %v, %v", entries, err)
	}
	for _, fileProfile := range profile {
		info, err := os.Lstat(filepath.Join(destination, fileProfile.name))
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != workerDirectFileMode {
			t.Fatalf("%s mode = %s", fileProfile.name, info.Mode())
		}
	}

	if err := os.Chmod(filepath.Join(destination, "tls.key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MaterializeWorkerFiles(source, destination, uid, gid); err == nil {
		t.Fatal("tampered direct worker material was accepted on retry")
	}
}

func TestMaterializeFilesPublishesEveryClosedProfile(t *testing.T) {
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
	if uid == 0 || gid == 0 {
		t.Skip("ownership assertion requires an unprivileged test identity")
	}
	for _, profileName := range []string{
		ProfileCore,
		ProfileBrowserGateway,
		ProfileExecutorGateway,
		ProfileHarnessWorker,
		ProfileLLMProxy,
	} {
		t.Run(profileName, func(t *testing.T) {
			source := projectedMaterialFixture(t, profileName)
			destination := filepath.Join(t.TempDir(), "direct")
			if err := MaterializeFiles(profileName, source, destination, uid, gid); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(destination, 0o700) })
			if err := MaterializeFiles(profileName, source, destination, uid, gid); err != nil {
				t.Fatalf("exact retry: %v", err)
			}
			entries, err := os.ReadDir(destination)
			if err != nil || len(entries) != len(materialProfiles[profileName]) {
				t.Fatalf("materialized entries = %v, %v", entries, err)
			}
			for index, fileProfile := range materialProfiles[profileName] {
				raw, err := os.ReadFile(filepath.Join(destination, fileProfile.name))
				if err != nil {
					t.Fatal(err)
				}
				want := []byte{byte(index + 1), byte(index + 11)}
				if string(raw) != string(want) {
					t.Fatalf("%s content = %v, want %v", fileProfile.name, raw, want)
				}
			}
		})
	}
	if err := MaterializeFiles("future-profile", t.TempDir(), filepath.Join(t.TempDir(), "direct"), uid, gid); err == nil {
		t.Fatal("unknown materialization profile was accepted")
	}
}

func TestMaterializeWorkerFilesRejectsProjectedEscape(t *testing.T) {
	source := projectedMaterialFixture(t, ProfileHarnessWorker)
	outside := filepath.Join(t.TempDir(), "outside.key")
	if err := os.WriteFile(outside, []byte("outside"), 0o400); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "tls.key")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
	if uid == 0 || gid == 0 {
		t.Skip("ownership assertion requires an unprivileged test identity")
	}
	if err := MaterializeWorkerFiles(source, filepath.Join(t.TempDir(), "worker"), uid, gid); err == nil {
		t.Fatal("projected path escape was accepted")
	}
}

func projectedMaterialFixture(t *testing.T, profileName string) string {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "..2026_08_02")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	for index, fileProfile := range materialProfiles[profileName] {
		target := filepath.Join(data, fileProfile.name)
		if err := os.WriteFile(target, []byte{byte(index + 1), byte(index + 11)}, 0o400); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(filepath.Base(data), fileProfile.name), filepath.Join(root, fileProfile.name)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
	}
	return root
}
