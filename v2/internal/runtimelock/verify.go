package runtimelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type VerifiedFile struct {
	Path      string
	SHA256    string
	SizeBytes int64
}

type VerifiedRuntime struct {
	Platform string
	Codex    VerifiedFile
	Helpers  map[string]VerifiedFile
}

func CurrentPlatform() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

func (m Manifest) VerifyCurrentPlatform(root string) (VerifiedRuntime, error) {
	return m.VerifyPlatform(root, CurrentPlatform())
}

// VerifyPlatform checks a signed manifest's files beneath an immutable bundle
// root. It rejects symlinks in every path component. Agentx still needs a
// platform-specific atomic safe-open/execute boundary after this check.
func (m Manifest) VerifyPlatform(root, platform string) (VerifiedRuntime, error) {
	if err := m.Validate(); err != nil {
		return VerifiedRuntime{}, err
	}
	if !filepath.IsAbs(root) {
		return VerifiedRuntime{}, errors.New("runtime bundle root must be absolute")
	}
	artifacts, exists := m.Artifacts[platform]
	if !exists {
		return VerifiedRuntime{}, fmt.Errorf("runtime manifest has no artifacts for %q", platform)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return VerifiedRuntime{}, fmt.Errorf("resolve runtime bundle root: %w", err)
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return VerifiedRuntime{}, fmt.Errorf("stat runtime bundle root: %w", err)
	}
	if !rootInfo.IsDir() {
		return VerifiedRuntime{}, errors.New("runtime bundle root must be a directory")
	}

	codex, err := verifyArtifact(root, artifacts.Codex)
	if err != nil {
		return VerifiedRuntime{}, fmt.Errorf("verify codex artifact: %w", err)
	}
	verified := VerifiedRuntime{
		Platform: platform,
		Codex:    codex,
		Helpers:  make(map[string]VerifiedFile, len(artifacts.Helpers)),
	}
	helperNames := make([]string, 0, len(artifacts.Helpers))
	for name := range artifacts.Helpers {
		helperNames = append(helperNames, name)
	}
	sort.Strings(helperNames)
	for _, name := range helperNames {
		helper, err := verifyArtifact(root, artifacts.Helpers[name])
		if err != nil {
			return VerifiedRuntime{}, fmt.Errorf("verify helper %q: %w", name, err)
		}
		verified.Helpers[name] = helper
	}
	return verified, nil
}

func verifyArtifact(root string, artifact FileArtifact) (VerifiedFile, error) {
	path, err := resolveWithoutSymlinks(root, artifact.Path)
	if err != nil {
		return VerifiedFile{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return VerifiedFile{}, err
	}
	if !info.Mode().IsRegular() {
		return VerifiedFile{}, errors.New("artifact is not a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return VerifiedFile{}, errors.New("artifact is not executable")
	}
	if info.Size() != artifact.SizeBytes {
		return VerifiedFile{}, fmt.Errorf("size = %d, want %d", info.Size(), artifact.SizeBytes)
	}
	digest, size, err := HashFile(path)
	if err != nil {
		return VerifiedFile{}, err
	}
	if size != artifact.SizeBytes {
		return VerifiedFile{}, fmt.Errorf("size changed while hashing: read %d, want %d", size, artifact.SizeBytes)
	}
	if digest != artifact.SHA256 {
		return VerifiedFile{}, fmt.Errorf("SHA-256 = %s, want %s", digest, artifact.SHA256)
	}
	return VerifiedFile{Path: path, SHA256: digest, SizeBytes: size}, nil
}

func resolveWithoutSymlinks(root, manifestPath string) (string, error) {
	current := root
	parts := strings.Split(manifestPath, "/")
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", fmt.Errorf("inspect artifact path component %q: %w", part, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("artifact path component %q is a symlink", part)
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", fmt.Errorf("artifact path component %q is not a directory", part)
		}
	}
	relative, err := filepath.Rel(root, current)
	if err != nil {
		return "", fmt.Errorf("check artifact containment: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("artifact path escapes runtime bundle root")
	}
	return current, nil
}
