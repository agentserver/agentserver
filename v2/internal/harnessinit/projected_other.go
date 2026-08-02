//go:build !linux

package harnessinit

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func readProjectedFile(root, name string, maximum int64) ([]byte, error) {
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve projected root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, name))
	if err != nil {
		return nil, fmt.Errorf("resolve projected file: %w", err)
	}
	relative, err := filepath.Rel(canonicalRoot, resolved)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("projected file resolves outside its root")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || info.Size() < 1 || info.Size() > maximum {
		_ = file.Close()
		return nil, errors.New("projected source must be a bounded regular file not writable by group or other")
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, maximum+1))
	after, statErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || closeErr != nil {
		clear(raw)
		return nil, errors.Join(readErr, statErr, closeErr)
	}
	if int64(len(raw)) != info.Size() || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		clear(raw)
		return nil, errors.New("projected source changed while it was being read")
	}
	return raw, nil
}
