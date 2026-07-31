package harnesspool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var localAttemptRuntimeNamePattern = regexp.MustCompile(`^attempt-[0-9a-f]{32}$`)

// LocalAttemptRuntimeCleaner removes one pool-created attempt tree only after
// the complete process group has stopped. Production uses a cleaner whose
// process retains DAC_OVERRIDE because the fixed app UID legitimately owns
// private 0700 descendants. The unexported marker prevents deployment code
// from accidentally substituting an arbitrary callback and calling it safe.
type LocalAttemptRuntimeCleaner interface {
	CleanLocalAttemptRuntime(string) error
	productionSafeLocalAttemptCleaner() bool
}

type localFilesystemAttemptRuntimeCleaner struct {
	runtimeRoot string
	privileged  bool
}

// NewDACOverrideLocalAttemptRuntimeCleaner constructs the production cleaner.
// On Linux it succeeds only when the current pool process has CAP_DAC_OVERRIDE
// effective. Other platforms reject this production backend explicitly.
func NewDACOverrideLocalAttemptRuntimeCleaner(runtimeRoot string) (LocalAttemptRuntimeCleaner, error) {
	if err := validateLocalAttemptRuntimeRoot(runtimeRoot); err != nil {
		return nil, err
	}
	if err := requireLocalCleanupDACOverride(); err != nil {
		return nil, err
	}
	return &localFilesystemAttemptRuntimeCleaner{runtimeRoot: runtimeRoot, privileged: true}, nil
}

func (cleaner *localFilesystemAttemptRuntimeCleaner) CleanLocalAttemptRuntime(path string) error {
	if cleaner == nil {
		return errors.New("local attempt runtime cleaner is required")
	}
	if err := validateLocalAttemptRuntimeRoot(cleaner.runtimeRoot); err != nil {
		return err
	}
	cleanPath := filepath.Clean(path)
	if path == "" || !filepath.IsAbs(path) || cleanPath != path || filepath.Dir(cleanPath) != cleaner.runtimeRoot ||
		!localAttemptRuntimeNamePattern.MatchString(filepath.Base(cleanPath)) {
		return errors.New("local attempt runtime cleanup target must be one direct canonical attempt child")
	}
	if err := os.RemoveAll(cleanPath); err != nil {
		return fmt.Errorf("remove local attempt runtime: %w", err)
	}
	return nil
}

func (cleaner *localFilesystemAttemptRuntimeCleaner) productionSafeLocalAttemptCleaner() bool {
	return cleaner != nil && cleaner.privileged
}

func validateLocalAttemptRuntimeRoot(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("local attempt runtime cleaner root must be an absolute clean path")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect local attempt runtime cleaner root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("local attempt runtime cleaner root is not a real directory: mode=%s", info.Mode())
	}
	return nil
}
