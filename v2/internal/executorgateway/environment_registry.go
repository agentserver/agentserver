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

	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/executionbackend"
)

var registryUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const MaxListEnvironmentsResultBytes = 512 * 1024

type EnvironmentRegistry interface {
	ListEnvironments(context.Context, string, string) ([]RegisteredEnvironment, error)
}

// ScopedEnvironmentRegistry adds the session/attempt authority needed to
// project a ready managed sandbox. Legacy registries continue to implement
// EnvironmentRegistry and therefore expose only agentx environments.
type ScopedEnvironmentRegistry interface {
	ListScopedEnvironments(context.Context, EnvironmentRegistryScope) ([]RegisteredEnvironment, error)
}

type EnvironmentRegistryScope struct {
	WorkspaceID          string
	SessionID            string
	RunAttemptID         string
	RunAttemptGeneration int64
	ExecutorID           string
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
	Target      executionbackend.Target
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
	return resolver.list(ctx, EnvironmentRegistryScope{WorkspaceID: workspaceID, ExecutorID: executorID}, false)
}

// ListProduction omits insecure-development environments while preserving
// the same live Core registry lookup and deterministic ordering.
func (resolver *EnvironmentResolver) ListProduction(ctx context.Context, workspaceID, executorID string) (ListEnvironmentsResult, error) {
	return resolver.list(ctx, EnvironmentRegistryScope{WorkspaceID: workspaceID, ExecutorID: executorID}, true)
}

func (resolver *EnvironmentResolver) ListForPrincipal(ctx context.Context, principal ExecutorMCPPrincipal, executorID string, production bool) (ListEnvironmentsResult, error) {
	return resolver.list(ctx, EnvironmentRegistryScope{
		WorkspaceID: principal.WorkspaceID, SessionID: principal.SessionID,
		RunAttemptID: principal.Run.RunAttemptID, RunAttemptGeneration: principal.Run.RunAttemptGeneration,
		ExecutorID: executorID,
	}, production)
}

func (resolver *EnvironmentResolver) list(ctx context.Context, scope EnvironmentRegistryScope, production bool) (ListEnvironmentsResult, error) {
	if err := validateRegistryIdentity("workspace ID", scope.WorkspaceID); err != nil {
		return ListEnvironmentsResult{}, err
	}
	if scope.ExecutorID != "" {
		if err := validateRegistryIdentity("executor ID", scope.ExecutorID); err != nil {
			return ListEnvironmentsResult{}, err
		}
	}
	if scope.SessionID != "" || scope.RunAttemptID != "" || scope.RunAttemptGeneration != 0 {
		if err := validateRegistryIdentity("session ID", scope.SessionID); err != nil {
			return ListEnvironmentsResult{}, err
		}
		if err := validateRegistryIdentity("run attempt ID", scope.RunAttemptID); err != nil {
			return ListEnvironmentsResult{}, err
		}
		if scope.RunAttemptGeneration < 1 {
			return ListEnvironmentsResult{}, errors.New("run attempt generation must be positive for a scoped environment lookup")
		}
	}
	registered, err := resolver.listRegistered(ctx, scope)
	if err != nil {
		return ListEnvironmentsResult{}, err
	}
	if len(registered) > 256 {
		return ListEnvironmentsResult{}, errors.New("environment registry exceeded the Phase 1 result bound")
	}
	result := ListEnvironmentsResult{Environments: make([]EnvironmentSummary, 0, len(registered))}
	seen := make(map[string]struct{}, len(registered))
	for index, environment := range registered {
		resolved, err := resolveRegisteredEnvironment(environment)
		if err != nil {
			return ListEnvironmentsResult{}, fmt.Errorf("environment %d: %w", index, err)
		}
		if scope.ExecutorID != "" && resolved.ExecutorID != scope.ExecutorID {
			return ListEnvironmentsResult{}, fmt.Errorf("environment registry returned executor %s outside requested executor %s", resolved.ExecutorID, scope.ExecutorID)
		}
		if production && resolved.InsecureDev {
			continue
		}
		if _, duplicate := seen[resolved.EnvironmentID]; duplicate {
			return ListEnvironmentsResult{}, fmt.Errorf("environment registry duplicated environment %s", resolved.EnvironmentID)
		}
		seen[resolved.EnvironmentID] = struct{}{}
		result.Environments = append(result.Environments, EnvironmentSummary{
			EnvironmentID: resolved.EnvironmentID,
			ExecutorID:    resolved.ExecutorID,
			DisplayName:   resolved.DisplayName,
			Description:   resolved.Description,
			Platform:      resolved.Platform,
			DefaultCWD:    resolved.DefaultCWD,
		})
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
	return resolver.resolve(ctx, EnvironmentRegistryScope{WorkspaceID: workspaceID}, environmentID)
}

func (resolver *EnvironmentResolver) ResolveForPrincipal(ctx context.Context, principal ExecutorMCPPrincipal, environmentID string) (ResolvedEnvironment, error) {
	return resolver.resolve(ctx, EnvironmentRegistryScope{
		WorkspaceID: principal.WorkspaceID, SessionID: principal.SessionID,
		RunAttemptID: principal.Run.RunAttemptID, RunAttemptGeneration: principal.Run.RunAttemptGeneration,
		ExecutorID: principal.ExecutorID,
	}, environmentID)
}

func (resolver *EnvironmentResolver) resolve(ctx context.Context, scope EnvironmentRegistryScope, environmentID string) (ResolvedEnvironment, error) {
	workspaceID := scope.WorkspaceID
	if err := validateRegistryIdentity("workspace ID", workspaceID); err != nil {
		return ResolvedEnvironment{}, err
	}
	if err := validateRegistryIdentity("environment ID", environmentID); err != nil {
		return ResolvedEnvironment{}, err
	}
	registered, err := resolver.listRegistered(ctx, scope)
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

func (resolver *EnvironmentResolver) listRegistered(ctx context.Context, scope EnvironmentRegistryScope) ([]RegisteredEnvironment, error) {
	if scoped, ok := resolver.registry.(ScopedEnvironmentRegistry); ok && scope.SessionID != "" {
		return scoped.ListScopedEnvironments(ctx, scope)
	}
	return resolver.registry.ListEnvironments(ctx, scope.WorkspaceID, scope.ExecutorID)
}

type environmentRootDescriptor struct {
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
	if environment.EnvironmentVersion < 1 {
		return ResolvedEnvironment{}, errors.New("environment registry version must be positive")
	}
	if !supportedExecutorPlatform(environment.Platform) {
		return ResolvedEnvironment{}, fmt.Errorf("unsupported executor platform %q", environment.Platform)
	}
	if !execprofile.AllowsEnvironmentProfile(environment.OuterProfileVersion) {
		return ResolvedEnvironment{}, fmt.Errorf("unsupported executor outer profile %q", environment.OuterProfileVersion)
	}
	target, err := registeredEnvironmentTarget(environment)
	if err != nil {
		return ResolvedEnvironment{}, err
	}
	var descriptor environmentRootDescriptor
	if err := decodeRootDescriptor(environment.RootDescriptor, &descriptor); err != nil {
		return ResolvedEnvironment{}, err
	}
	wantRootKind := "local"
	if target.Kind == executionbackend.KindTAE {
		wantRootKind = "managed"
		if environment.Platform != "linux-amd64" {
			return ResolvedEnvironment{}, errors.New("managed environments require the linux-amd64 platform")
		}
	}
	if descriptor.Kind != wantRootKind {
		return ResolvedEnvironment{}, fmt.Errorf("root descriptor kind %q does not match %s backend", descriptor.Kind, target.Kind)
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
		Target:                target,
	}, nil
}

func registeredEnvironmentTarget(environment RegisteredEnvironment) (executionbackend.Target, error) {
	kind := environment.BackendKind
	targetID := environment.TargetID
	generation := environment.TargetGeneration
	if kind == "" {
		kind = executionbackend.KindAgentX
		targetID = environment.ExecutorID
		generation = environment.ConnectionGeneration
	}
	target := executionbackend.Target{
		Kind: kind, ID: targetID, Generation: generation,
		EnvironmentID: environment.EnvironmentID,
	}
	if err := target.Validate(); err != nil {
		return executionbackend.Target{}, fmt.Errorf("environment dispatch target: %w", err)
	}
	switch kind {
	case executionbackend.KindAgentX:
		if targetID != environment.ExecutorID || generation != environment.ConnectionGeneration {
			return executionbackend.Target{}, errors.New("agentx environment target differs from its legacy executor projection")
		}
	case executionbackend.KindTAE:
		if environment.ConnectionGeneration != 0 {
			return executionbackend.Target{}, errors.New("managed environment carries an agentx connection generation")
		}
	default:
		return executionbackend.Target{}, fmt.Errorf("unsupported environment backend kind %q", kind)
	}
	return target, nil
}

func decodeRootDescriptor(raw json.RawMessage, destination *environmentRootDescriptor) error {
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
