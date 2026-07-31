package devruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

const (
	manifestRelativePath = "runtime-manifest.json"
	bundleRelativePath   = "bundle"
)

type PrepareConfig struct {
	Platform        string
	CodexExecutable string
	BwrapExecutable string
	OutputDirectory string
}

type Result struct {
	OutputDirectory string
	ManifestFile    string
	BundleRoot      string
	Platform        string
}

// Prepare creates a new immutable-by-permissions development runtime package.
// It accepts only the exact official stable 0.146.0 Linux arm64 artifacts
// already characterized by the repository's native image gates.
func Prepare(config PrepareConfig) (_ Result, returnErr error) {
	if config.Platform != PlatformLinuxARM64 {
		return Result{}, fmt.Errorf("insecure development runtime platform must be %q", PlatformLinuxARM64)
	}
	if err := validateNewOutputDirectory(config.OutputDirectory); err != nil {
		return Result{}, err
	}
	for label, path := range map[string]string{
		"stock Codex executable": config.CodexExecutable,
		"stock bwrap executable": config.BwrapExecutable,
	} {
		if err := validateInputPath(label, path); err != nil {
			return Result{}, err
		}
	}

	manifest := stockLinuxARM64Manifest()
	if err := manifest.Validate(); err != nil {
		return Result{}, fmt.Errorf("validate built-in development runtime profile: %w", err)
	}
	manifestBytes, err := marshalManifest(manifest)
	if err != nil {
		return Result{}, err
	}

	if err := os.Mkdir(config.OutputDirectory, 0o755); err != nil {
		return Result{}, fmt.Errorf("create development runtime output: %w", err)
	}
	created := true
	defer func() {
		if returnErr != nil && created {
			returnErr = errors.Join(returnErr, removeIncompleteOutput(config.OutputDirectory))
		}
	}()
	bundleRoot := filepath.Join(config.OutputDirectory, bundleRelativePath)
	for _, directory := range []string{
		bundleRoot,
		filepath.Join(bundleRoot, "bin"),
		filepath.Join(bundleRoot, "codex-resources"),
	} {
		if err := os.Mkdir(directory, 0o755); err != nil {
			return Result{}, fmt.Errorf("create development runtime directory: %w", err)
		}
		if err := os.Chmod(directory, 0o755); err != nil {
			return Result{}, fmt.Errorf("set development runtime directory mode: %w", err)
		}
	}
	artifacts := manifest.Artifacts[PlatformLinuxARM64]
	if err := copyPinnedArtifact(
		"stock Codex", config.CodexExecutable,
		filepath.Join(bundleRoot, filepath.FromSlash(artifacts.Codex.Path)), artifacts.Codex,
	); err != nil {
		return Result{}, err
	}
	bwrap := artifacts.ExternalExecutables["bwrap"]
	if err := copyPinnedArtifact(
		"stock bwrap", config.BwrapExecutable,
		filepath.Join(bundleRoot, filepath.FromSlash(bwrap.Path)), bwrap,
	); err != nil {
		return Result{}, err
	}
	manifestPath := filepath.Join(config.OutputDirectory, manifestRelativePath)
	if err := writeExclusiveFile(manifestPath, manifestBytes, 0o444); err != nil {
		return Result{}, fmt.Errorf("write development runtime manifest: %w", err)
	}
	if _, err := runtimelock.Parse(manifestBytes); err != nil {
		return Result{}, fmt.Errorf("reparse development runtime manifest: %w", err)
	}
	if _, err := manifest.VerifyPlatform(bundleRoot, PlatformLinuxARM64); err != nil {
		return Result{}, fmt.Errorf("verify assembled development runtime: %w", err)
	}
	created = false
	return Result{
		OutputDirectory: config.OutputDirectory,
		ManifestFile:    manifestPath, BundleRoot: bundleRoot, Platform: PlatformLinuxARM64,
	}, nil
}

func validateNewOutputDirectory(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return errors.New("development runtime output directory must be an absolute clean path")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("development runtime output directory already exists; runtime preparation never overwrites or merges")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect development runtime output directory: %w", err)
	}
	parent := filepath.Dir(path)
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("resolve development runtime output parent: %w", err)
	}
	if resolved != parent {
		return errors.New("development runtime output parent must be canonical and contain no symlink component")
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("inspect development runtime output parent: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("development runtime output parent must be a direct directory not writable by group or other: mode=%s", info.Mode())
	}
	return nil
}

func validateInputPath(label, path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return fmt.Errorf("%s path must be absolute and clean", label)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s must be a direct executable regular file: mode=%s", label, info.Mode())
	}
	return nil
}

func copyPinnedArtifact(label, source, destination string, artifact runtimelock.FileArtifact) error {
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect %s source: %w", label, err)
	}
	if sourceInfo.Size() != artifact.SizeBytes {
		return fmt.Errorf("%s source size = %d, want %d", label, sourceInfo.Size(), artifact.SizeBytes)
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open %s source: %w", label, err)
	}
	inputInfo, statErr := input.Stat()
	if statErr != nil || !os.SameFile(sourceInfo, inputInfo) {
		_ = input.Close()
		return fmt.Errorf("%s source identity changed while opening: %w", label, statErr)
	}
	hasher := sha256.New()
	read, hashErr := io.Copy(hasher, input)
	if hashErr != nil || read != artifact.SizeBytes || hex.EncodeToString(hasher.Sum(nil)) != artifact.SHA256 {
		_ = input.Close()
		return fmt.Errorf("%s source does not match the pinned SHA-256 and size", label)
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		_ = input.Close()
		return fmt.Errorf("rewind %s source: %w", label, err)
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o555)
	if err != nil {
		_ = input.Close()
		return fmt.Errorf("create %s destination: %w", label, err)
	}
	written, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	chmodErr := output.Chmod(0o555)
	outputCloseErr := output.Close()
	inputCloseErr := input.Close()
	if err := errors.Join(copyErr, syncErr, chmodErr, outputCloseErr, inputCloseErr); err != nil || written != artifact.SizeBytes {
		return errors.Join(fmt.Errorf("copy %s artifact: wrote %d of %d bytes", label, written, artifact.SizeBytes), err)
	}
	digest, size, err := runtimelock.HashFile(destination)
	if err != nil || digest != artifact.SHA256 || size != artifact.SizeBytes {
		return errors.Join(fmt.Errorf("verify copied %s artifact", label), err)
	}
	return nil
}

func marshalManifest(manifest runtimelock.Manifest) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		return nil, fmt.Errorf("encode development runtime manifest: %w", err)
	}
	return output.Bytes(), nil
}

func writeExclusiveFile(path string, contents []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	written, writeErr := file.Write(contents)
	if writeErr == nil && written != len(contents) {
		writeErr = io.ErrShortWrite
	}
	syncErr := file.Sync()
	chmodErr := file.Chmod(mode)
	closeErr := file.Close()
	return errors.Join(writeErr, syncErr, chmodErr, closeErr)
}

func removeIncompleteOutput(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("refuse to clean invalid development runtime output path")
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("clean incomplete development runtime output: %w", err)
	}
	return nil
}
