package productionimage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/runtimelock"
	"github.com/agentserver/agentserver/v2/internal/stockruntime"
)

type PrepareConfig struct {
	Kind             string
	SourceRevision   string
	BinaryDirectory  string
	CodexExecutable  string
	BwrapExecutable  string
	RequirementsFile string
	OutputDirectory  string
}

type PrepareResult struct {
	OutputDirectory string
	RootFS          string
	ManifestFile    string
	Manifest        Manifest
}

// Prepare creates a new build context payload without downloading anything.
// Every executable name and non-Go runtime byte belongs to a fixed profile.
func Prepare(config PrepareConfig) (_ PrepareResult, returnErr error) {
	if err := validatePrepareConfig(config); err != nil {
		return PrepareResult{}, err
	}
	if err := os.Mkdir(config.OutputDirectory, 0o755); err != nil {
		return PrepareResult{}, fmt.Errorf("create production image output: %w", err)
	}
	created := true
	defer func() {
		if returnErr != nil && created {
			returnErr = errors.Join(returnErr, removeIncompleteOutput(config.OutputDirectory))
		}
	}()

	rootfs := filepath.Join(config.OutputDirectory, "rootfs")
	if err := os.Mkdir(rootfs, 0o755); err != nil {
		return PrepareResult{}, fmt.Errorf("create production image rootfs: %w", err)
	}
	for _, directory := range expectedDirectories(config.Kind) {
		if err := os.Mkdir(filepath.Join(rootfs, filepath.FromSlash(directory.Path)), 0o755); err != nil {
			return PrepareResult{}, fmt.Errorf("create production image directory %s: %w", directory.Path, err)
		}
	}

	files := []FileEntry{{
		Path: CABundlePath, SHA256: CABundleSHA256, SizeBytes: CABundleSizeBytes,
		Mode: 0o444,
	}}
	for _, binary := range ExpectedBinaries(config.Kind) {
		source := filepath.Join(config.BinaryDirectory, binary)
		if err := validateLinuxARM64GoExecutable(source, binary); err != nil {
			return PrepareResult{}, err
		}
		entry, err := copyArtifact(source, rootfs, "usr/local/bin/"+binary, 0o555, "production Go executable "+binary)
		if err != nil {
			return PrepareResult{}, err
		}
		files = append(files, entry)
	}
	if config.Kind == KindHarness {
		requirements, err := copyPinnedArtifact(
			config.RequirementsFile, rootfs, RequirementsPath, 0o444,
			RequirementsSHA256, RequirementsSizeBytes, "Codex system requirements",
		)
		if err != nil {
			return PrepareResult{}, err
		}
		files = append(files, requirements)

		runtimeBytes, err := stockruntime.ManifestBytes()
		if err != nil {
			return PrepareResult{}, err
		}
		runtimeEntry, err := writePayload(rootfs, RuntimeManifestPath, runtimeBytes, 0o444)
		if err != nil {
			return PrepareResult{}, fmt.Errorf("write stock runtime manifest: %w", err)
		}
		files = append(files, runtimeEntry)

		codex, err := copyPinnedArtifact(
			config.CodexExecutable, rootfs, RuntimeBundleRoot+"/bin/codex", 0o555,
			stockruntime.LinuxARM64CodexSHA256, stockruntime.LinuxARM64CodexSize, "stock Codex",
		)
		if err != nil {
			return PrepareResult{}, err
		}
		files = append(files, codex)
		bwrap, err := copyPinnedArtifact(
			config.BwrapExecutable, rootfs, RuntimeBundleRoot+"/codex-resources/bwrap", 0o555,
			stockruntime.LinuxARM64BwrapSHA256, stockruntime.LinuxARM64BwrapSize, "stock bwrap",
		)
		if err != nil {
			return PrepareResult{}, err
		}
		files = append(files, bwrap)
	}
	slices.SortFunc(files, func(left, right FileEntry) int { return strings.Compare(left.Path, right.Path) })
	manifest := Manifest{
		Version: ManifestVersion, Kind: config.Kind, Platform: Platform,
		SourceRevision: config.SourceRevision, GoToolchain: GoToolchain,
		CABundleSource: CABundleSourceImage,
		Directories:    expectedDirectories(config.Kind), Files: files,
	}
	manifestBytes, err := MarshalManifest(manifest)
	if err != nil {
		return PrepareResult{}, err
	}
	if _, err := writePayload(rootfs, ManifestPath, manifestBytes, 0o444); err != nil {
		return PrepareResult{}, fmt.Errorf("write in-image production manifest: %w", err)
	}
	manifestFile := filepath.Join(config.OutputDirectory, "image-manifest.json")
	if err := writeExclusiveFile(manifestFile, manifestBytes, 0o444); err != nil {
		return PrepareResult{}, fmt.Errorf("write external production image manifest: %w", err)
	}
	for _, directory := range manifest.Directories {
		if err := os.Chmod(filepath.Join(rootfs, filepath.FromSlash(directory.Path)), os.FileMode(directory.Mode)); err != nil {
			return PrepareResult{}, fmt.Errorf("seal production image directory %s: %w", directory.Path, err)
		}
	}
	if err := os.Chmod(rootfs, 0o555); err != nil {
		return PrepareResult{}, fmt.Errorf("seal production image rootfs: %w", err)
	}
	if err := verifyPreparedPayload(rootfs, manifestBytes, manifest); err != nil {
		return PrepareResult{}, fmt.Errorf("verify prepared production image: %w", err)
	}
	created = false
	return PrepareResult{
		OutputDirectory: config.OutputDirectory, RootFS: rootfs,
		ManifestFile: manifestFile, Manifest: manifest,
	}, nil
}

func validatePrepareConfig(config PrepareConfig) error {
	if config.Kind != KindService && config.Kind != KindHarness {
		return errors.New("production image kind must be service or harness")
	}
	if !revisionPattern.MatchString(config.SourceRevision) {
		return errors.New("production image source revision must be a lowercase 40-character Git SHA")
	}
	if err := validateCanonicalDirectory("production binary directory", config.BinaryDirectory, true); err != nil {
		return err
	}
	entries, err := os.ReadDir(config.BinaryDirectory)
	if err != nil {
		return fmt.Errorf("list production binary directory: %w", err)
	}
	wanted := ExpectedBinaries(config.Kind)
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("production binary directory contains a non-file entry")
		}
		actual = append(actual, entry.Name())
	}
	slices.Sort(actual)
	if !slices.Equal(actual, wanted) {
		return fmt.Errorf("production binary directory must contain exactly %v", wanted)
	}
	if config.Kind == KindHarness {
		for label, path := range map[string]string{
			"stock Codex":               config.CodexExecutable,
			"stock bwrap":               config.BwrapExecutable,
			"Codex system requirements": config.RequirementsFile,
		} {
			if err := validateCanonicalFile(label, path); err != nil {
				return err
			}
		}
	} else if config.CodexExecutable != "" || config.BwrapExecutable != "" || config.RequirementsFile != "" {
		return errors.New("service image preparation must not receive harness runtime inputs")
	}
	return validateNewOutputDirectory(config.OutputDirectory)
}

func validateNewOutputDirectory(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return errors.New("production image output directory must be an absolute clean path")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("production image output already exists; preparation never overwrites or merges")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect production image output: %w", err)
	}
	return validateCanonicalDirectory("production image output parent", filepath.Dir(path), false)
}

func validateCanonicalDirectory(label, path string, permitReadOnly bool) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return fmt.Errorf("%s must be an absolute clean path", label)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", label, err)
	}
	if resolved != path {
		return fmt.Errorf("%s must be canonical and contain no symlink component", label)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || (!permitReadOnly && info.Mode().Perm()&0o022 != 0) {
		return fmt.Errorf("%s must be a direct directory not writable by group or other: mode=%s", label, info.Mode())
	}
	return nil
}

func validateCanonicalFile(label, path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return fmt.Errorf("%s must be an absolute clean path", label)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", label, err)
	}
	if resolved != path {
		return fmt.Errorf("%s must be canonical and contain no symlink component", label)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s must be a direct regular file not writable by group or other: mode=%s", label, info.Mode())
	}
	return nil
}

func copyPinnedArtifact(source, rootfs, relative string, mode os.FileMode, digest string, size int64, label string) (FileEntry, error) {
	entry, err := copyArtifact(source, rootfs, relative, mode, label)
	if err != nil {
		return FileEntry{}, err
	}
	if entry.SHA256 != digest || entry.SizeBytes != size {
		return FileEntry{}, fmt.Errorf("%s does not match pinned SHA-256 and size", label)
	}
	return entry, nil
}

func copyArtifact(source, rootfs, relative string, mode os.FileMode, label string) (FileEntry, error) {
	if err := validateCanonicalFile(label, source); err != nil {
		return FileEntry{}, err
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return FileEntry{}, fmt.Errorf("inspect %s: %w", label, err)
	}
	input, err := os.Open(source)
	if err != nil {
		return FileEntry{}, fmt.Errorf("open %s: %w", label, err)
	}
	openedInfo, statErr := input.Stat()
	if statErr != nil || !os.SameFile(sourceInfo, openedInfo) || openedInfo.Size() != sourceInfo.Size() {
		_ = input.Close()
		return FileEntry{}, errors.Join(fmt.Errorf("%s identity changed while opening", label), statErr)
	}
	destination := filepath.Join(rootfs, filepath.FromSlash(relative))
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		_ = input.Close()
		return FileEntry{}, fmt.Errorf("create production image payload %s: %w", relative, err)
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(output, hasher), input)
	syncErr := output.Sync()
	chmodErr := output.Chmod(mode)
	outputCloseErr := output.Close()
	inputCloseErr := input.Close()
	if err := errors.Join(copyErr, syncErr, chmodErr, outputCloseErr, inputCloseErr); err != nil || written != sourceInfo.Size() {
		return FileEntry{}, errors.Join(fmt.Errorf("copy production image payload %s: wrote %d of %d bytes", relative, written, sourceInfo.Size()), err)
	}
	entry := FileEntry{
		Path: relative, SHA256: hex.EncodeToString(hasher.Sum(nil)), SizeBytes: written,
		Mode: uint32(mode.Perm()),
	}
	actualDigest, actualSize, err := runtimelock.HashFile(destination)
	if err != nil || actualDigest != entry.SHA256 || actualSize != entry.SizeBytes {
		return FileEntry{}, errors.Join(fmt.Errorf("verify copied production image payload %s", relative), err)
	}
	return entry, nil
}

func writePayload(rootfs, relative string, contents []byte, mode os.FileMode) (FileEntry, error) {
	destination := filepath.Join(rootfs, filepath.FromSlash(relative))
	if err := writeExclusiveFile(destination, contents, mode); err != nil {
		return FileEntry{}, err
	}
	return FileEntry{
		Path: relative, SHA256: sha256Hex(contents), SizeBytes: int64(len(contents)),
		Mode: uint32(mode.Perm()),
	}, nil
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
		return errors.New("refuse to clean invalid production image output path")
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("clean incomplete production image output: %w", err)
	}
	return nil
}
