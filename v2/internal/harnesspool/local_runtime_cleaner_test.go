package harnesspool

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalAttemptRuntimeCleanerRemovesOnlyCanonicalDirectAttempt(t *testing.T) {
	runtimeRoot := t.TempDir()
	cleaner := &localFilesystemAttemptRuntimeCleaner{runtimeRoot: runtimeRoot}
	attempt := filepath.Join(runtimeRoot, "attempt-0123456789abcdef0123456789abcdef")
	if err := os.MkdirAll(filepath.Join(attempt, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "must-survive")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(attempt, "nested", "outside-link")); err != nil {
		t.Fatal(err)
	}
	if err := cleaner.CleanLocalAttemptRuntime(attempt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(attempt); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleaned attempt stat error = %v", err)
	}
	if contents, err := os.ReadFile(outside); err != nil || string(contents) != "outside" {
		t.Fatalf("outside symlink target = %q, %v", contents, err)
	}

	for _, target := range []string{
		runtimeRoot,
		filepath.Join(runtimeRoot, "not-an-attempt"),
		filepath.Join(filepath.Dir(runtimeRoot), "attempt-0123456789abcdef0123456789abcdef"),
	} {
		if err := cleaner.CleanLocalAttemptRuntime(target); err == nil || !strings.Contains(err.Error(), "direct canonical attempt child") {
			t.Errorf("cleanup target %q error = %v", target, err)
		}
	}
}

func TestDACOverrideRuntimeCleanerFailsClosedOffLinuxOrWithoutCapability(t *testing.T) {
	runtimeRoot := t.TempDir()
	cleaner, err := NewDACOverrideLocalAttemptRuntimeCleaner(runtimeRoot)
	if err == nil {
		if cleaner == nil || !cleaner.productionSafeLocalAttemptCleaner() {
			t.Fatal("successful DAC_OVERRIDE cleaner is not marked production-safe")
		}
		return
	}
	if !strings.Contains(err.Error(), "Linux") && !strings.Contains(err.Error(), "CAP_DAC_OVERRIDE") &&
		!strings.Contains(err.Error(), "capabilities") {
		t.Fatalf("unexpected DAC_OVERRIDE cleaner error = %v", err)
	}
}
