package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

const (
	managedEnvironmentProfileVersion      = 1
	maximumManagedEnvironmentProfileBytes = 64 * 1024
)

type managedEnvironmentProfileDocument struct {
	Version           int                                     `json:"version"`
	WorkspaceID       string                                  `json:"workspaceId"`
	ExecutorID        string                                  `json:"executorId"`
	EnvironmentID     string                                  `json:"environmentId"`
	OwnerPolicySHA256 string                                  `json:"ownerPolicySha256"`
	Root              managedEnvironmentRootDocument          `json:"root"`
	Runtime           managedEnvironmentLegacyRuntimeDocument `json:"runtime"`
}

type managedEnvironmentRootDocument struct {
	Path        string `json:"path"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
	DefaultCWD  string `json:"defaultCwd,omitempty"`
}

// executor_environments still carries the original Codex artifact columns.
// Managed dispatch does not trust these as a provider identity; they remain an
// immutable catalog/profile projection until that legacy schema is retired.
type managedEnvironmentLegacyRuntimeDocument struct {
	CodexRelease string `json:"codexRelease"`
	CodexCommit  string `json:"codexCommit"`
	CodexSHA256  string `json:"codexSha256"`
}

type managedEnvironmentProfileCommandResult struct {
	Bootstrap     coredb.ManagedEnvironmentProfileBootstrapResult
	WorkspaceID   string
	ExecutorID    string
	EnvironmentID string
}

func bootstrapManagedEnvironmentProfile(
	ctx context.Context,
	databaseURL string,
	configPath string,
) (managedEnvironmentProfileCommandResult, error) {
	profile, err := loadManagedEnvironmentProfile(configPath)
	if err != nil {
		return managedEnvironmentProfileCommandResult{}, err
	}
	result, err := coredb.BootstrapManagedEnvironmentProfile(ctx, databaseURL, profile)
	if err != nil {
		return managedEnvironmentProfileCommandResult{}, err
	}
	return managedEnvironmentProfileCommandResult{
		Bootstrap: result, WorkspaceID: profile.WorkspaceID,
		ExecutorID: profile.ExecutorID, EnvironmentID: profile.EnvironmentID,
	}, nil
}

func loadManagedEnvironmentProfile(configPath string) (coredb.ManagedEnvironmentProfile, error) {
	raw, err := readManagedEnvironmentProfileFile(configPath)
	if err != nil {
		return coredb.ManagedEnvironmentProfile{}, err
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxJSONValues = 128
	limits.MaxJSONDepth = 6
	if _, _, err := braincatalog.DecodeCanonicalJSON(raw, maximumManagedEnvironmentProfileBytes, limits); err != nil {
		return coredb.ManagedEnvironmentProfile{}, fmt.Errorf("validate managed environment profile JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document managedEnvironmentProfileDocument
	if err := decoder.Decode(&document); err != nil {
		return coredb.ManagedEnvironmentProfile{}, fmt.Errorf("decode managed environment profile config: %w", err)
	}
	if err := finishManagedEnvironmentProfileJSON(decoder); err != nil {
		return coredb.ManagedEnvironmentProfile{}, fmt.Errorf("finish managed environment profile config: %w", err)
	}
	if err := validateManagedEnvironmentProfileDocument(document); err != nil {
		return coredb.ManagedEnvironmentProfile{}, err
	}
	ownerPolicy, err := decodeManagedEnvironmentDigest("ownerPolicySha256", document.OwnerPolicySHA256)
	if err != nil {
		return coredb.ManagedEnvironmentProfile{}, err
	}
	codexDigest, err := decodeManagedEnvironmentDigest("runtime.codexSha256", document.Runtime.CodexSHA256)
	if err != nil {
		return coredb.ManagedEnvironmentProfile{}, err
	}
	descriptor, err := managedEnvironmentRootDescriptor(document.Root)
	if err != nil {
		return coredb.ManagedEnvironmentProfile{}, err
	}
	return coredb.ManagedEnvironmentProfile{
		WorkspaceID: document.WorkspaceID, ExecutorID: document.ExecutorID,
		EnvironmentID: document.EnvironmentID, RootDescriptor: descriptor,
		OwnerPolicySHA256: ownerPolicy, CodexRelease: document.Runtime.CodexRelease,
		CodexCommit: document.Runtime.CodexCommit, CodexSHA256: codexDigest,
	}, nil
}

func validateManagedEnvironmentProfileDocument(document managedEnvironmentProfileDocument) error {
	if document.Version != managedEnvironmentProfileVersion {
		return fmt.Errorf("managed environment profile version must be %d", managedEnvironmentProfileVersion)
	}
	for name, value := range map[string]string{
		"workspaceId": document.WorkspaceID, "executorId": document.ExecutorID,
		"environmentId": document.EnvironmentID,
	} {
		if value == "00000000-0000-0000-0000-000000000000" || !productionBootstrapUUIDPattern.MatchString(value) {
			return fmt.Errorf("managed environment profile %s must be a non-zero canonical lowercase UUID", name)
		}
	}
	if err := validateManagedEnvironmentText("root.path", document.Root.Path, 1, 4096); err != nil {
		return err
	}
	if !strings.HasPrefix(document.Root.Path, "/") || path.Clean(document.Root.Path) != document.Root.Path {
		return errors.New("managed environment profile root.path must be a clean absolute Unix path")
	}
	if document.Root.DisplayName != "" {
		if err := validateManagedEnvironmentText("root.displayName", document.Root.DisplayName, 1, 256); err != nil {
			return err
		}
	}
	if err := validateManagedEnvironmentText("root.description", document.Root.Description, 0, 2048); err != nil {
		return err
	}
	if document.Root.DefaultCWD != "" {
		if err := validateManagedEnvironmentText("root.defaultCwd", document.Root.DefaultCWD, 1, 4096); err != nil {
			return err
		}
		if strings.Contains(document.Root.DefaultCWD, "\\") || strings.HasPrefix(document.Root.DefaultCWD, "/") ||
			path.Clean(document.Root.DefaultCWD) != document.Root.DefaultCWD || document.Root.DefaultCWD == ".." ||
			strings.HasPrefix(document.Root.DefaultCWD, "../") {
			return errors.New("managed environment profile root.defaultCwd must be a clean relative path without parent traversal")
		}
	}
	if err := validateManagedEnvironmentText("runtime.codexRelease", document.Runtime.CodexRelease, 1, 128); err != nil {
		return err
	}
	if len(document.Runtime.CodexCommit) != 40 || strings.Trim(document.Runtime.CodexCommit, "0123456789abcdef") != "" {
		return errors.New("managed environment profile runtime.codexCommit must be a lowercase 40-character Git SHA")
	}
	return nil
}

func managedEnvironmentRootDescriptor(root managedEnvironmentRootDocument) (json.RawMessage, error) {
	descriptor := struct {
		Kind        string `json:"kind"`
		Root        string `json:"root"`
		DisplayName string `json:"displayName,omitempty"`
		Description string `json:"description,omitempty"`
		DefaultCWD  string `json:"defaultCwd,omitempty"`
	}{
		Kind: "managed", Root: root.Path, DisplayName: root.DisplayName,
		Description: root.Description, DefaultCWD: root.DefaultCWD,
	}
	raw, err := json.Marshal(descriptor)
	if err != nil {
		return nil, fmt.Errorf("encode managed environment root descriptor: %w", err)
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxJSONValues = 32
	limits.MaxJSONDepth = 4
	_, canonical, err := braincatalog.DecodeCanonicalJSON(raw, 64*1024, limits)
	if err != nil {
		return nil, fmt.Errorf("canonicalize managed environment root descriptor: %w", err)
	}
	return append(json.RawMessage(nil), canonical...), nil
}

func readManagedEnvironmentProfileFile(filePath string) ([]byte, error) {
	if filePath == "" || !filepath.IsAbs(filePath) || filepath.Clean(filePath) != filePath {
		return nil, errors.New("managed environment profile config path must be absolute and clean")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open managed environment profile config: %w", err)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect managed environment profile config: %w", err)
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0o022 != 0 || before.Size() < 1 || before.Size() > maximumManagedEnvironmentProfileBytes {
		return nil, fmt.Errorf("managed environment profile config must resolve to an immutable regular file between 1 and %d bytes", maximumManagedEnvironmentProfileBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumManagedEnvironmentProfileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read managed environment profile config: %w", err)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || int64(len(raw)) != before.Size() {
		return nil, errors.New("managed environment profile config changed while it was being read")
	}
	return raw, nil
}

func decodeManagedEnvironmentDigest(label, value string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != len(digest) || hex.EncodeToString(decoded) != value || digestIsZero(decoded) {
		return digest, fmt.Errorf("managed environment profile %s must be a non-zero canonical lowercase SHA-256 digest", label)
	}
	copy(digest[:], decoded)
	return digest, nil
}

func digestIsZero(value []byte) bool {
	for _, octet := range value {
		if octet != 0 {
			return false
		}
	}
	return true
}

func validateManagedEnvironmentText(name, value string, minimum, maximum int) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("managed environment profile %s must be valid UTF-8 without NUL", name)
	}
	length := utf8.RuneCountInString(value)
	if length < minimum || length > maximum {
		return fmt.Errorf("managed environment profile %s must contain between %d and %d characters", name, minimum, maximum)
	}
	return nil
}

func finishManagedEnvironmentProfileJSON(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("document contains more than one JSON value")
		}
		return err
	}
	return nil
}
