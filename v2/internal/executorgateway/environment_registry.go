package executorgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var registryUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const MaxListEnvironmentsResultBytes = 512 * 1024

type EnvironmentRegistry interface {
	ListEnvironments(context.Context, string, string) ([]RegisteredEnvironment, error)
}

type EnvironmentSummary struct {
	EnvironmentID string `json:"environment_id"`
	ExecutorID    string `json:"executor_id"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description,omitempty"`
	Platform      string `json:"platform"`
	DefaultCWD    string `json:"default_cwd"`
}

type ListEnvironmentsResult struct {
	Environments []EnvironmentSummary `json:"environments"`
}

type ResolvedEnvironment struct {
	RegisteredEnvironment
	Root        string
	DefaultCWD  string
	DisplayName string
	Description string
}

type EnvironmentResolver struct {
	registry EnvironmentRegistry
}

func NewEnvironmentResolver(registry EnvironmentRegistry) (*EnvironmentResolver, error) {
	if registry == nil {
		return nil, errors.New("environment registry is required")
	}
	return &EnvironmentResolver{registry: registry}, nil
}

func (resolver *EnvironmentResolver) List(ctx context.Context, workspaceID, executorID string) (ListEnvironmentsResult, error) {
	if err := validateRegistryIdentity("workspace ID", workspaceID); err != nil {
		return ListEnvironmentsResult{}, err
	}
	if executorID != "" {
		if err := validateRegistryIdentity("executor ID", executorID); err != nil {
			return ListEnvironmentsResult{}, err
		}
	}
	registered, err := resolver.registry.ListEnvironments(ctx, workspaceID, executorID)
	if err != nil {
		return ListEnvironmentsResult{}, err
	}
	if len(registered) > 256 {
		return ListEnvironmentsResult{}, errors.New("environment registry exceeded the Phase 1 result bound")
	}
	result := ListEnvironmentsResult{Environments: make([]EnvironmentSummary, len(registered))}
	seen := make(map[string]struct{}, len(registered))
	for index, environment := range registered {
		resolved, err := resolveRegisteredEnvironment(environment)
		if err != nil {
			return ListEnvironmentsResult{}, fmt.Errorf("environment %d: %w", index, err)
		}
		if executorID != "" && resolved.ExecutorID != executorID {
			return ListEnvironmentsResult{}, fmt.Errorf("environment registry returned executor %s outside requested executor %s", resolved.ExecutorID, executorID)
		}
		if _, duplicate := seen[resolved.EnvironmentID]; duplicate {
			return ListEnvironmentsResult{}, fmt.Errorf("environment registry duplicated environment %s", resolved.EnvironmentID)
		}
		seen[resolved.EnvironmentID] = struct{}{}
		result.Environments[index] = EnvironmentSummary{
			EnvironmentID: resolved.EnvironmentID,
			ExecutorID:    resolved.ExecutorID,
			DisplayName:   resolved.DisplayName,
			Description:   resolved.Description,
			Platform:      resolved.Platform,
			DefaultCWD:    resolved.DefaultCWD,
		}
	}
	sort.Slice(result.Environments, func(left, right int) bool {
		if result.Environments[left].ExecutorID != result.Environments[right].ExecutorID {
			return result.Environments[left].ExecutorID < result.Environments[right].ExecutorID
		}
		return result.Environments[left].EnvironmentID < result.Environments[right].EnvironmentID
	})
	encoded, err := json.Marshal(result)
	if err != nil {
		return ListEnvironmentsResult{}, fmt.Errorf("encode environment registry projection: %w", err)
	}
	if len(encoded) > MaxListEnvironmentsResultBytes {
		return ListEnvironmentsResult{}, fmt.Errorf("environment registry projection is %d bytes, limit is %d", len(encoded), MaxListEnvironmentsResultBytes)
	}
	return result, nil
}

func (resolver *EnvironmentResolver) Resolve(ctx context.Context, workspaceID, environmentID string) (ResolvedEnvironment, error) {
	if err := validateRegistryIdentity("workspace ID", workspaceID); err != nil {
		return ResolvedEnvironment{}, err
	}
	if err := validateRegistryIdentity("environment ID", environmentID); err != nil {
		return ResolvedEnvironment{}, err
	}
	registered, err := resolver.registry.ListEnvironments(ctx, workspaceID, "")
	if err != nil {
		return ResolvedEnvironment{}, err
	}
	var matched *RegisteredEnvironment
	for index := range registered {
		if registered[index].EnvironmentID != environmentID {
			continue
		}
		if matched != nil {
			return ResolvedEnvironment{}, errors.New("environment registry returned duplicate environment identity")
		}
		copy := registered[index]
		matched = &copy
	}
	if matched == nil {
		return ResolvedEnvironment{}, ErrExecutorUnavailable
	}
	return resolveRegisteredEnvironment(*matched)
}

type localRootDescriptor struct {
	Kind        string `json:"kind"`
	Root        string `json:"root"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
	DefaultCWD  string `json:"defaultCwd,omitempty"`
}

func resolveRegisteredEnvironment(environment RegisteredEnvironment) (ResolvedEnvironment, error) {
	if err := validateRegistryIdentity("environment ID", environment.EnvironmentID); err != nil {
		return ResolvedEnvironment{}, err
	}
	if err := validateRegistryIdentity("executor ID", environment.ExecutorID); err != nil {
		return ResolvedEnvironment{}, err
	}
	if environment.EnvironmentVersion < 1 || environment.ConnectionGeneration < 1 {
		return ResolvedEnvironment{}, errors.New("environment registry versions must be positive")
	}
	if !supportedExecutorPlatform(environment.Platform) {
		return ResolvedEnvironment{}, fmt.Errorf("unsupported executor platform %q", environment.Platform)
	}
	var descriptor localRootDescriptor
	if err := decodeRootDescriptor(environment.RootDescriptor, &descriptor); err != nil {
		return ResolvedEnvironment{}, err
	}
	if descriptor.Kind != "local" {
		return ResolvedEnvironment{}, fmt.Errorf("unsupported root descriptor kind %q", descriptor.Kind)
	}
	if err := validateRegisteredRoot(environment.Platform, descriptor.Root); err != nil {
		return ResolvedEnvironment{}, err
	}
	if descriptor.DisplayName == "" {
		descriptor.DisplayName = "environment " + environment.EnvironmentID
	}
	if err := validateRegistryText("environment display name", descriptor.DisplayName, 1, 256); err != nil {
		return ResolvedEnvironment{}, err
	}
	if err := validateRegistryText("environment description", descriptor.Description, 0, 2048); err != nil {
		return ResolvedEnvironment{}, err
	}
	if descriptor.DefaultCWD == "" {
		descriptor.DefaultCWD = "."
	}
	if err := validateRelativeEnvironmentPath(descriptor.DefaultCWD); err != nil {
		return ResolvedEnvironment{}, fmt.Errorf("default cwd: %w", err)
	}
	return ResolvedEnvironment{
		RegisteredEnvironment: environment,
		Root:                  descriptor.Root,
		DefaultCWD:            descriptor.DefaultCWD,
		DisplayName:           descriptor.DisplayName,
		Description:           descriptor.Description,
	}, nil
}

func decodeRootDescriptor(raw json.RawMessage, destination *localRootDescriptor) error {
	if len(raw) == 0 || len(raw) > 64*1024 {
		return errors.New("root descriptor is empty or exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode root descriptor: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("root descriptor contains additional JSON")
		}
		return err
	}
	return nil
}

func validateRegisteredRoot(platform, root string) error {
	if err := validateRegistryText("environment root", root, 1, 4096); err != nil {
		return err
	}
	if strings.HasPrefix(platform, "windows-") {
		return validateWindowsEnvironmentRoot(root)
	}
	if !strings.HasPrefix(root, "/") || path.Clean(root) != root {
		return errors.New("Unix environment root must be a clean absolute path")
	}
	return nil
}

func validateWindowsEnvironmentRoot(root string) error {
	if len(root) < 3 || ((root[0] < 'A' || root[0] > 'Z') && (root[0] < 'a' || root[0] > 'z')) || root[1] != ':' || (root[2] != '\\' && root[2] != '/') {
		return errors.New("Windows environment root must be an absolute drive path")
	}
	separator := root[2]
	if len(root) == 3 {
		return nil
	}
	if len(root) > 3 && root[len(root)-1] == separator {
		return errors.New("Windows environment root must not have a trailing separator")
	}
	segmentStart := 3
	for index := 3; index <= len(root); index++ {
		if index < len(root) && root[index] != separator {
			if root[index] == '/' || root[index] == '\\' || root[index] < 0x20 || strings.ContainsRune(`<>:"|?*`, rune(root[index])) {
				return errors.New("Windows environment root contains an invalid or mixed separator path byte")
			}
			continue
		}
		segment := root[segmentStart:index]
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("Windows environment root must be clean and contain no dot segments")
		}
		segmentStart = index + 1
	}
	return nil
}

func validateRelativeEnvironmentPath(value string) error {
	if err := validateRegistryText("environment-relative path", value, 1, 4096); err != nil {
		return err
	}
	if strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || path.Clean(value) != value || value == ".." || strings.HasPrefix(value, "../") {
		return errors.New("path must be a clean slash-separated relative path without parent traversal")
	}
	return nil
}

func supportedExecutorPlatform(platform string) bool {
	switch platform {
	case "linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64", "windows-arm64":
		return true
	default:
		return false
	}
}

func validateRegistryIdentity(name, value string) error {
	if value == "00000000-0000-0000-0000-000000000000" || !registryUUIDPattern.MatchString(value) {
		return fmt.Errorf("%s must be a non-zero canonical lowercase UUID", name)
	}
	return nil
}

func validateRegistryText(name, value string, minimum, maximum int) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%s must be valid UTF-8 without NUL", name)
	}
	length := utf8.RuneCountInString(value)
	if length < minimum || length > maximum {
		return fmt.Errorf("%s must contain between %d and %d characters", name, minimum, maximum)
	}
	return nil
}
