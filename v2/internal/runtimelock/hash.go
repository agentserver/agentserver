package runtimelock

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxCanonicalJSONValues = 2 * 1024 * 1024

type TreeLimits struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

func DefaultTreeLimits() TreeLimits {
	return TreeLimits{
		MaxFiles:      4096,
		MaxFileBytes:  64 * 1024 * 1024,
		MaxTotalBytes: 256 * 1024 * 1024,
	}
}

type TreeEntry struct {
	Path      string
	SHA256    string
	SizeBytes int64
}

type TreeDigest struct {
	SHA256 string
	Files  []TreeEntry
}

func HashFile(path string) (digest string, sizeBytes int64, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open file for hashing: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("stat file for hashing: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", 0, errors.New("hash target must be a regular file")
	}

	hasher := sha256.New()
	written, err := io.Copy(hasher, file)
	if err != nil {
		return "", 0, fmt.Errorf("hash file: %w", err)
	}
	if written != info.Size() {
		return "", 0, fmt.Errorf("file size changed while hashing: read %d bytes, stat reported %d", written, info.Size())
	}
	return hex.EncodeToString(hasher.Sum(nil)), written, nil
}

// HashTree hashes a directory using the canonical record format documented in
// packaging/agentx/README.md. Symlinks and non-regular filesystem objects are
// rejected instead of followed.
func HashTree(root string, limits TreeLimits) (TreeDigest, error) {
	return hashTree(root, limits, func(path, _ string, maxBytes int64) (string, int64, int64, error) {
		digest, size, err := hashFileBounded(path, maxBytes)
		return digest, size, size, err
	})
}

// HashCanonicalJSONTree hashes a generated JSON bundle after deterministic
// compact re-encoding. Object keys are sorted by encoding/json, arrays retain
// source order, numbers retain their source lexical representation, and
// duplicate keys are rejected. This is the versioned canonical-json-tree-v1
// algorithm; it is intentionally narrower than general-purpose RFC 8785 JCS.
func HashCanonicalJSONTree(root string, limits TreeLimits) (TreeDigest, error) {
	return hashTree(root, limits, hashCanonicalJSONFile)
}

type treeFileHasher func(path, relative string, maxBytes int64) (digest string, outputBytes, inputBytes int64, err error)

func hashTree(root string, limits TreeLimits, hashFile treeFileHasher) (TreeDigest, error) {
	if !filepath.IsAbs(root) {
		return TreeDigest{}, errors.New("tree root must be absolute")
	}
	if err := limits.validate(); err != nil {
		return TreeDigest{}, err
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return TreeDigest{}, fmt.Errorf("stat tree root: %w", err)
	}
	if !rootInfo.IsDir() {
		return TreeDigest{}, errors.New("tree root must be a directory")
	}

	entries := make([]TreeEntry, 0)
	var totalInputBytes int64
	var totalOutputBytes int64
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("tree contains symlink %q", path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat tree entry %q: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("tree entry %q is not a regular file", path)
		}
		if len(entries) >= limits.MaxFiles {
			return fmt.Errorf("tree exceeds %d files", limits.MaxFiles)
		}
		if info.Size() > limits.MaxFileBytes {
			return fmt.Errorf("tree file %q exceeds %d bytes", path, limits.MaxFileBytes)
		}
		if info.Size() > limits.MaxTotalBytes-totalInputBytes {
			return fmt.Errorf("tree exceeds %d total bytes", limits.MaxTotalBytes)
		}

		relative, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("make tree path relative: %w", err)
		}
		relative = filepath.ToSlash(relative)
		if !fs.ValidPath(relative) || strings.ContainsAny(relative, "\r\n") {
			return fmt.Errorf("tree path %q is not canonical", relative)
		}
		digest, size, inputSize, err := hashFile(path, relative, limits.MaxFileBytes)
		if err != nil {
			return fmt.Errorf("hash tree file %q: %w", relative, err)
		}
		if inputSize != info.Size() {
			return fmt.Errorf("tree file %q changed size while hashing", relative)
		}
		if size > limits.MaxFileBytes {
			return fmt.Errorf("hashed tree file %q exceeds %d bytes", relative, limits.MaxFileBytes)
		}
		if size > limits.MaxTotalBytes-totalOutputBytes {
			return fmt.Errorf("tree exceeds %d total bytes", limits.MaxTotalBytes)
		}
		totalInputBytes += info.Size()
		totalOutputBytes += size
		entries = append(entries, TreeEntry{Path: relative, SHA256: digest, SizeBytes: size})
		return nil
	})
	if err != nil {
		return TreeDigest{}, err
	}
	if len(entries) == 0 {
		return TreeDigest{}, errors.New("tree must contain at least one file")
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	treeHasher := sha256.New()
	writer := bufio.NewWriter(treeHasher)
	for _, entry := range entries {
		if _, err := fmt.Fprintf(writer, "%s  %s\n", entry.SHA256, entry.Path); err != nil {
			return TreeDigest{}, fmt.Errorf("hash tree record: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return TreeDigest{}, fmt.Errorf("flush tree hash: %w", err)
	}
	return TreeDigest{
		SHA256: hex.EncodeToString(treeHasher.Sum(nil)),
		Files:  entries,
	}, nil
}

func hashCanonicalJSONFile(path, relative string, maxBytes int64) (string, int64, int64, error) {
	if filepath.Ext(relative) != ".json" {
		return "", 0, 0, errors.New("canonical JSON tree contains a non-JSON file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, 0, err
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	closeErr := file.Close()
	if err != nil {
		return "", 0, 0, err
	}
	if closeErr != nil {
		return "", 0, 0, closeErr
	}
	if int64(len(raw)) > maxBytes {
		return "", 0, 0, fmt.Errorf("file exceeds %d bytes", maxBytes)
	}
	if err := validateJSONDocument(raw, maxCanonicalJSONValues); err != nil {
		return "", 0, 0, fmt.Errorf("validate JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", 0, 0, fmt.Errorf("decode JSON: %w", err)
	}
	if _, ok := value.(map[string]any); !ok {
		return "", 0, 0, errors.New("schema JSON root must be an object")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", 0, 0, fmt.Errorf("encode canonical JSON: %w", err)
	}
	if int64(len(canonical)) > maxBytes {
		return "", 0, 0, fmt.Errorf("canonical JSON exceeds %d bytes", maxBytes)
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), int64(len(canonical)), int64(len(raw)), nil
}

func (l TreeLimits) validate() error {
	if l.MaxFiles < 1 || l.MaxFileBytes < 1 || l.MaxTotalBytes < 1 {
		return errors.New("tree hash limits must be positive")
	}
	if l.MaxFileBytes == int64(^uint64(0)>>1) {
		return errors.New("max file bytes is too large")
	}
	return nil
}

func hashFileBounded(path string, maxBytes int64) (digest string, sizeBytes int64, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hasher := sha256.New()
	read, err := io.Copy(hasher, io.LimitReader(file, maxBytes+1))
	if err != nil {
		return "", 0, err
	}
	if read > maxBytes {
		return "", 0, fmt.Errorf("file exceeds %d bytes", maxBytes)
	}
	return hex.EncodeToString(hasher.Sum(nil)), read, nil
}
