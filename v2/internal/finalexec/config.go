// Package finalexec implements the last process boundary before a pinned
// runtime binary. It is deliberately small: callers prepare stdio, identity,
// filesystem, and the explicit environment; Execute then seals the process and
// atomically replaces it with the target.
package finalexec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Program         string
	Arguments       []string
	Directory       string
	Environment     []string
	ExpectedUID     uint32
	ExpectedGID     uint32
	RequiredOpenFDs []int
}

func validate(config Config) error {
	if config.Program == "" || !filepath.IsAbs(config.Program) {
		return errors.New("final exec program must be an absolute path")
	}
	info, err := os.Lstat(config.Program)
	if err != nil {
		return fmt.Errorf("inspect final exec program: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("final exec program is not a directly executable regular file: mode=%s", info.Mode())
	}
	if config.Directory == "" || !filepath.IsAbs(config.Directory) {
		return errors.New("final exec directory must be an absolute path")
	}
	directory, err := os.Lstat(config.Directory)
	if err != nil {
		return fmt.Errorf("inspect final exec directory: %w", err)
	}
	if !directory.IsDir() || directory.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("final exec directory is not a real directory: mode=%s", directory.Mode())
	}
	if config.Environment == nil {
		return errors.New("final exec environment must be explicit")
	}
	seenEnvironment := make(map[string]struct{}, len(config.Environment))
	for index, entry := range config.Environment {
		name, value, found := strings.Cut(entry, "=")
		if !found || name == "" || strings.ContainsAny(name, "=\x00") || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("invalid final exec environment entry at index %d", index)
		}
		if _, duplicate := seenEnvironment[name]; duplicate {
			return fmt.Errorf("duplicate final exec environment variable %q", name)
		}
		seenEnvironment[name] = struct{}{}
	}
	if config.ExpectedUID == 0 || config.ExpectedGID == 0 || config.ExpectedUID == ^uint32(0) || config.ExpectedGID == ^uint32(0) {
		return fmt.Errorf("final exec identity must be valid and unprivileged: uid=%d gid=%d", config.ExpectedUID, config.ExpectedGID)
	}
	seenFDs := make(map[int]struct{}, len(config.RequiredOpenFDs))
	for _, descriptor := range config.RequiredOpenFDs {
		if descriptor < 3 {
			return fmt.Errorf("required inherited descriptor %d overlaps stdio", descriptor)
		}
		if _, duplicate := seenFDs[descriptor]; duplicate {
			return fmt.Errorf("required inherited descriptor %d is duplicated", descriptor)
		}
		seenFDs[descriptor] = struct{}{}
	}
	for index, argument := range config.Arguments {
		if strings.ContainsRune(argument, '\x00') {
			return fmt.Errorf("final exec argument %d contains NUL", index)
		}
	}
	return nil
}
