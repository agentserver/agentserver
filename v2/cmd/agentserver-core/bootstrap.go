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
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

const (
	developmentBootstrapVersion      = 1
	maximumDevelopmentBootstrapBytes = 64 * 1024
	maximumDevelopmentManifestBytes  = 1024 * 1024
)

var developmentBootstrapUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type developmentBootstrapDocument struct {
	Version     int                                  `json:"version"`
	WorkspaceID string                               `json:"workspaceId"`
	SessionID   string                               `json:"sessionId"`
	ActorID     string                               `json:"actorId"`
	Executor    developmentBootstrapExecutorDocument `json:"executor"`
}

type developmentBootstrapExecutorDocument struct {
	ExecutorID          string `json:"executorId"`
	EnvironmentID       string `json:"environmentId"`
	AgentxVersion       string `json:"agentxVersion"`
	Platform            string `json:"platform"`
	RuntimeManifestFile string `json:"runtimeManifestFile"`
	WorkspaceRoot       string `json:"workspaceRoot"`
	DisplayName         string `json:"displayName,omitempty"`
	Description         string `json:"description,omitempty"`
	DefaultCWD          string `json:"defaultCwd,omitempty"`
}

type developmentBootstrapResult struct {
	Migration     coredb.MigrationResult
	Bootstrap     coredb.InsecureDevelopmentBootstrapResult
	WorkspaceID   string
	SessionID     string
	ActorID       string
	ExecutorID    string
	EnvironmentID string
}

func bootstrapDevelopment(ctx context.Context, databaseURL, configPath string) (developmentBootstrapResult, error) {
	bootstrap, err := loadDevelopmentBootstrap(configPath)
	if err != nil {
		return developmentBootstrapResult{}, err
	}
	migration, err := coredb.Migrate(ctx, databaseURL)
	if err != nil {
		return developmentBootstrapResult{}, fmt.Errorf("migrate before insecure development bootstrap: %w", err)
	}
	result, err := coredb.BootstrapInsecureDevelopment(ctx, databaseURL, bootstrap)
	if err != nil {
		return developmentBootstrapResult{}, err
	}
	return developmentBootstrapResult{
		Migration: migration, Bootstrap: result,
		WorkspaceID: bootstrap.WorkspaceID, SessionID: bootstrap.SessionID, ActorID: bootstrap.ActorID,
		ExecutorID: bootstrap.ExecutorID, EnvironmentID: bootstrap.Environment.EnvironmentID,
	}, nil
}

func loadDevelopmentBootstrap(configPath string) (coredb.InsecureDevelopmentBootstrap, error) {
	raw, err := readDirectDevelopmentFile("insecure development bootstrap config", configPath, maximumDevelopmentBootstrapBytes)
	if err != nil {
		return coredb.InsecureDevelopmentBootstrap{}, err
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxJSONValues = 256
	limits.MaxJSONDepth = 8
	if _, _, err := braincatalog.DecodeCanonicalJSON(raw, maximumDevelopmentBootstrapBytes, limits); err != nil {
		return coredb.InsecureDevelopmentBootstrap{}, fmt.Errorf("validate insecure development bootstrap JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document developmentBootstrapDocument
	if err := decoder.Decode(&document); err != nil {
		return coredb.InsecureDevelopmentBootstrap{}, fmt.Errorf("decode insecure development bootstrap config: %w", err)
	}
	if err := finishDevelopmentJSON(decoder); err != nil {
		return coredb.InsecureDevelopmentBootstrap{}, fmt.Errorf("finish insecure development bootstrap config: %w", err)
	}
	if err := validateDevelopmentBootstrapDocument(document); err != nil {
		return coredb.InsecureDevelopmentBootstrap{}, err
	}

	manifestBytes, err := readDirectDevelopmentFile(
		"insecure development runtime manifest", document.Executor.RuntimeManifestFile, maximumDevelopmentManifestBytes,
	)
	if err != nil {
		return coredb.InsecureDevelopmentBootstrap{}, err
	}
	manifest, err := runtimelock.Parse(manifestBytes)
	if err != nil {
		return coredb.InsecureDevelopmentBootstrap{}, fmt.Errorf("parse insecure development runtime manifest: %w", err)
	}
	artifacts, found := manifest.Artifacts[document.Executor.Platform]
	if !found {
		return coredb.InsecureDevelopmentBootstrap{}, fmt.Errorf("runtime manifest does not contain development executor platform %q", document.Executor.Platform)
	}
	runtimeManifestDigest := sha256.Sum256(manifestBytes)
	execProtocolDigest, err := decodeDevelopmentDigest("runtime manifest execProtocolSourceSha256", manifest.ExecProtocolSourceSHA256)
	if err != nil {
		return coredb.InsecureDevelopmentBootstrap{}, err
	}
	codexDigest, err := decodeDevelopmentDigest("runtime manifest Codex artifact SHA-256", artifacts.Codex.SHA256)
	if err != nil {
		return coredb.InsecureDevelopmentBootstrap{}, err
	}

	descriptor, err := developmentRootDescriptor(document.Executor)
	if err != nil {
		return coredb.InsecureDevelopmentBootstrap{}, err
	}
	machineKeyDigest := sha256.Sum256([]byte("agentserver-v2/insecure-dev-placeholder-machine-key/v1\x00" + document.Executor.ExecutorID))
	ownerPolicyHasher := sha256.New()
	_, _ = ownerPolicyHasher.Write([]byte("agentserver-v2/insecure-dev-owner-policy/v1\x00"))
	_, _ = ownerPolicyHasher.Write(descriptor)
	var ownerPolicyDigest [sha256.Size]byte
	copy(ownerPolicyDigest[:], ownerPolicyHasher.Sum(nil))

	return coredb.InsecureDevelopmentBootstrap{
		WorkspaceID: document.WorkspaceID, SessionID: document.SessionID, ActorID: document.ActorID,
		ExecutorID: document.Executor.ExecutorID, MachineKeySHA256: machineKeyDigest,
		AgentxVersion: document.Executor.AgentxVersion, RuntimeManifestSHA256: runtimeManifestDigest,
		ExecProtocolSourceSHA256: execProtocolDigest,
		Environment: coredb.InsecureDevelopmentEnvironment{
			EnvironmentID: document.Executor.EnvironmentID, RootDescriptor: descriptor,
			OwnerPolicySHA256: ownerPolicyDigest, Platform: document.Executor.Platform,
			CodexRelease: manifest.CodexRelease, CodexCommit: manifest.CodexCommit, CodexSHA256: codexDigest,
			OuterProfileVersion: execprofile.FilesystemReadVersion,
			ProcessMethods:      append([]string(nil), execprofile.ProcessMethods()...), InsecureDev: true,
		},
	}, nil
}

func validateDevelopmentBootstrapDocument(document developmentBootstrapDocument) error {
	if document.Version != developmentBootstrapVersion {
		return fmt.Errorf("insecure development bootstrap version must be %d", developmentBootstrapVersion)
	}
	for name, value := range map[string]string{
		"workspaceId": document.WorkspaceID, "sessionId": document.SessionID, "actorId": document.ActorID,
		"executor.executorId": document.Executor.ExecutorID, "executor.environmentId": document.Executor.EnvironmentID,
	} {
		if value == "00000000-0000-0000-0000-000000000000" || !developmentBootstrapUUIDPattern.MatchString(value) {
			return fmt.Errorf("insecure development bootstrap %s must be a non-zero canonical lowercase UUID", name)
		}
	}
	if err := validateDevelopmentText("executor.agentxVersion", document.Executor.AgentxVersion, 1, 256); err != nil {
		return err
	}
	if document.Executor.RuntimeManifestFile == "" || !filepath.IsAbs(document.Executor.RuntimeManifestFile) ||
		filepath.Clean(document.Executor.RuntimeManifestFile) != document.Executor.RuntimeManifestFile {
		return errors.New("insecure development bootstrap executor.runtimeManifestFile must be an absolute clean path")
	}
	if err := validateDevelopmentExecutorRoot(document.Executor.Platform, document.Executor.WorkspaceRoot); err != nil {
		return fmt.Errorf("insecure development bootstrap executor.workspaceRoot: %w", err)
	}
	if document.Executor.DisplayName != "" {
		if err := validateDevelopmentText("executor.displayName", document.Executor.DisplayName, 1, 256); err != nil {
			return err
		}
	}
	if err := validateDevelopmentText("executor.description", document.Executor.Description, 0, 2048); err != nil {
		return err
	}
	if document.Executor.DefaultCWD != "" {
		if err := validateDevelopmentRelativePath(document.Executor.DefaultCWD); err != nil {
			return fmt.Errorf("insecure development bootstrap executor.defaultCwd: %w", err)
		}
	}
	return nil
}

func developmentRootDescriptor(executor developmentBootstrapExecutorDocument) (json.RawMessage, error) {
	type descriptorDocument struct {
		Kind        string `json:"kind"`
		Root        string `json:"root"`
		DisplayName string `json:"displayName,omitempty"`
		Description string `json:"description,omitempty"`
		DefaultCWD  string `json:"defaultCwd,omitempty"`
	}
	raw, err := json.Marshal(descriptorDocument{
		Kind: "local", Root: executor.WorkspaceRoot, DisplayName: executor.DisplayName,
		Description: executor.Description, DefaultCWD: executor.DefaultCWD,
	})
	if err != nil {
		return nil, fmt.Errorf("encode insecure development root descriptor: %w", err)
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxJSONValues = 32
	limits.MaxJSONDepth = 4
	_, canonical, err := braincatalog.DecodeCanonicalJSON(raw, 64*1024, limits)
	if err != nil {
		return nil, fmt.Errorf("canonicalize insecure development root descriptor: %w", err)
	}
	return append(json.RawMessage(nil), canonical...), nil
}

func validateDevelopmentExecutorRoot(platform, root string) error {
	switch platform {
	case "linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64":
		if err := validateDevelopmentText("executor.workspaceRoot", root, 1, 4096); err != nil {
			return err
		}
		if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
			return errors.New("Unix root must be a clean absolute path")
		}
		return nil
	case "windows-amd64", "windows-arm64":
		return validateDevelopmentWindowsRoot(root)
	default:
		return fmt.Errorf("executor.platform %q is unsupported", platform)
	}
}

func validateDevelopmentWindowsRoot(root string) error {
	if err := validateDevelopmentText("executor.workspaceRoot", root, 3, 4096); err != nil {
		return err
	}
	if ((root[0] < 'A' || root[0] > 'Z') && (root[0] < 'a' || root[0] > 'z')) || root[1] != ':' ||
		(root[2] != '\\' && root[2] != '/') {
		return errors.New("Windows root must be an absolute drive path")
	}
	separator := root[2]
	if len(root) == 3 {
		return nil
	}
	if root[len(root)-1] == separator {
		return errors.New("Windows root must not have a trailing separator")
	}
	segmentStart := 3
	for index := 3; index <= len(root); index++ {
		if index < len(root) && root[index] != separator {
			if root[index] == '/' || root[index] == '\\' || root[index] < 0x20 || strings.ContainsRune(`<>:"|?*`, rune(root[index])) {
				return errors.New("Windows root contains an invalid or mixed separator path byte")
			}
			continue
		}
		segment := root[segmentStart:index]
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("Windows root must be clean and contain no dot segments")
		}
		segmentStart = index + 1
	}
	return nil
}

func validateDevelopmentRelativePath(value string) error {
	if err := validateDevelopmentText("executor.defaultCwd", value, 1, 4096); err != nil {
		return err
	}
	if strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || path.Clean(value) != value ||
		value == ".." || strings.HasPrefix(value, "../") {
		return errors.New("path must be clean, slash-separated, relative, and contain no parent traversal")
	}
	return nil
}

func validateDevelopmentText(name, value string, minimum, maximum int) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("insecure development bootstrap %s must be valid UTF-8 without NUL", name)
	}
	length := utf8.RuneCountInString(value)
	if length < minimum || length > maximum {
		return fmt.Errorf("insecure development bootstrap %s must contain between %d and %d characters", name, minimum, maximum)
	}
	return nil
}

func readDirectDevelopmentFile(label, filePath string, maximum int64) ([]byte, error) {
	if filePath == "" || !filepath.IsAbs(filePath) || filepath.Clean(filePath) != filePath {
		return nil, fmt.Errorf("%s path must be absolute and clean", label)
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 ||
		info.Size() < 1 || info.Size() > maximum {
		return nil, fmt.Errorf("%s must be a direct immutable regular file between 1 and %d bytes: mode=%s size=%d", label, maximum, info.Mode(), info.Size())
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, maximum+1))
	openedInfo, statErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || closeErr != nil {
		return nil, errors.Join(
			wrapDevelopmentFileError("read "+label, readErr),
			wrapDevelopmentFileError("verify "+label, statErr),
			wrapDevelopmentFileError("close "+label, closeErr),
		)
	}
	if int64(len(raw)) != info.Size() || int64(len(raw)) > maximum || !os.SameFile(info, openedInfo) || openedInfo.Size() != info.Size() {
		return nil, fmt.Errorf("%s identity or size changed while reading", label)
	}
	return raw, nil
}

func decodeDevelopmentDigest(label, value string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != len(digest) || hex.EncodeToString(decoded) != value {
		return digest, fmt.Errorf("%s must be a canonical lowercase SHA-256 digest", label)
	}
	copy(digest[:], decoded)
	return digest, nil
}

func finishDevelopmentJSON(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("document contains more than one JSON value")
		}
		return err
	}
	return nil
}

func wrapDevelopmentFileError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}
