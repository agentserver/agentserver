package productiondeploy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteBundlePublishesReadOnlyExactRetry(t *testing.T) {
	loaded, err := ValidateConfig(validConfigDocument())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Render(loaded)
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	destination := filepath.Join(parent, "bundle")
	for retry := 0; retry < 2; retry++ {
		if err := WriteBundle(bundle, destination); err != nil {
			t.Fatalf("retry %d: %v", retry, err)
		}
	}
	t.Cleanup(func() { _ = os.Chmod(destination, 0o700) })
	info, err := os.Lstat(destination)
	if err != nil || info.Mode().Perm() != bundleDirectoryMode {
		t.Fatalf("bundle mode = %v, %v", info, err)
	}
	if err := os.Chmod(filepath.Join(destination, runtimeFile), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteBundle(bundle, destination); err == nil {
		t.Fatal("tampered production bundle was accepted on retry")
	}
}
