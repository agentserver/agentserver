package executorgateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/agentxconn"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
)

var shellEnvironmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const (
	ShellV1DefaultTimeoutMillis      int64 = 60_000
	ShellV1PolicyVersion                   = "shell-policy-v1"
	ShellV1OperationProcessStart           = "process_start"
	ShellV1OperationTimeoutTerminate       = "timeout_terminate"
	ShellV1EffectMutation                  = "mutation"
)

type ShellV1Arguments struct {
	EnvironmentID string            `json:"environment_id"`
	Argv          []string          `json:"argv"`
	CWD           string            `json:"cwd,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	TimeoutMillis *int64            `json:"timeout_ms,omitempty"`
	TTY           *bool             `json:"tty,omitempty"`
}

type ShellV1Identities struct {
	ExecutionID         string
	ProcessID           string
	StartOperationID    string
	StartMutationKey    string
	TimeoutOperationID  string
	TimeoutMutationKey  string
	StartRPCRequestID   string
	TimeoutRPCRequestID string
}

type ShellV1IdentityAllocator struct {
	mu          sync.Mutex
	idGenerator IDGenerator
}

func NewShellV1IdentityAllocator(idGenerator IDGenerator) (*ShellV1IdentityAllocator, error) {
	if idGenerator == nil {
		return nil, errors.New("shell identity generator is required")
	}
	return &ShellV1IdentityAllocator{idGenerator: idGenerator}, nil
}

func NewDefaultShellV1IdentityAllocator() (*ShellV1IdentityAllocator, error) {
	return NewShellV1IdentityAllocator(newRandomUUID)
}

func (allocator *ShellV1IdentityAllocator) Allocate() (ShellV1Identities, error) {
	if allocator == nil {
		return ShellV1Identities{}, errors.New("shell identity allocator is required")
	}
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	identities := ShellV1Identities{}
	destinations := []*string{
		&identities.ExecutionID,
		&identities.ProcessID,
		&identities.StartOperationID,
		&identities.StartMutationKey,
		&identities.TimeoutOperationID,
		&identities.TimeoutMutationKey,
		&identities.StartRPCRequestID,
		&identities.TimeoutRPCRequestID,
	}
	for _, destination := range destinations {
		identity, err := allocator.idGenerator()
		if err != nil {
			return ShellV1Identities{}, err
		}
		*destination = identity
	}
	if err := validateShellV1Identities(identities); err != nil {
		return ShellV1Identities{}, err
	}
	return identities, nil
}

type ShellV1Operation struct {
	OperationID string
	Ordinal     int
	Kind        string
	EffectClass string
	MutationKey string
	Params      json.RawMessage
	RPC         json.RawMessage
	Routing     agentxconn.RoutingContext
}

// ShellV1Plan is a complete immutable projection of one shell call. The raw
// JSON values supplied to core are retained byte-for-byte and reused at every
// dispatch boundary; the timeout directive is outer agentx metadata and is
// deliberately absent from Start.Params and Start.RPC.
type ShellV1Plan struct {
	Arguments      json.RawMessage
	ToolSchema     json.RawMessage
	OperationPlan  json.RawMessage
	PolicyContext  json.RawMessage
	PolicyDecision string
	PolicyVersion  string
	ToolCallID     string
	Environment    ResolvedEnvironment
	ProcessID      string
	TimeoutMillis  int64
	RootURI        string
	CWDURI         string
	Start          ShellV1Operation
	Timeout        ShellV1Operation
	Directives     agentxconn.DispatchDirectives
}

func MapShellV1(rawArguments json.RawMessage, principal ExecutorMCPPrincipal, toolCallID string, environment ResolvedEnvironment, policy ExecutionPolicyResolution, identities ShellV1Identities) (ShellV1Plan, error) {
	if err := validateExecutorMCPPrincipal(principal); err != nil {
		return ShellV1Plan{}, fmt.Errorf("shell principal: %w", err)
	}
	if err := validateExecutionPolicyResolution(policy); err != nil {
		return ShellV1Plan{}, fmt.Errorf("shell policy: %w", err)
	}
	if toolCallID == "" || len(toolCallID) > 256 || !utf8.ValidString(toolCallID) || strings.ContainsRune(toolCallID, 0) {
		return ShellV1Plan{}, errors.New("app-server tool call ID must contain between 1 and 256 valid UTF-8 bytes without NUL")
	}
	if err := validateShellV1Identities(identities); err != nil {
		return ShellV1Plan{}, err
	}
	resolved, err := resolveRegisteredEnvironment(environment.RegisteredEnvironment)
	if err != nil {
		return ShellV1Plan{}, fmt.Errorf("shell environment: %w", err)
	}
	// Do not accept caller-constructed projections whose parsed descriptor was
	// replaced after resolution.
	if resolved.Root != environment.Root || resolved.DefaultCWD != environment.DefaultCWD ||
		resolved.DisplayName != environment.DisplayName || resolved.Description != environment.Description {
		return ShellV1Plan{}, errors.New("shell environment projection differs from its registered descriptor")
	}
	if principal.ExecutorID != "" && environment.ExecutorID != principal.ExecutorID {
		return ShellV1Plan{}, errors.New("shell environment is outside the authenticated executor scope")
	}
	if principal.Production && environment.InsecureDev {
		return ShellV1Plan{}, errors.New("production shell environment cannot be insecure-development")
	}

	var arguments ShellV1Arguments
	if err := decodeExactJSON(rawArguments, &arguments); err != nil {
		return ShellV1Plan{}, fmt.Errorf("decode shell-v1 arguments: %w", err)
	}
	if err := validateShellV1Arguments(arguments, environment); err != nil {
		return ShellV1Plan{}, err
	}
	cwd := arguments.CWD
	if cwd == "" {
		cwd = environment.DefaultCWD
	}
	timeoutMillis := ShellV1DefaultTimeoutMillis
	if arguments.TimeoutMillis != nil {
		timeoutMillis = *arguments.TimeoutMillis
	}
	tty := false
	if arguments.TTY != nil {
		tty = *arguments.TTY
	}
	explicitEnvironment := arguments.Env
	if explicitEnvironment == nil {
		explicitEnvironment = map[string]string{}
	} else {
		explicitEnvironment = cloneStringMap(explicitEnvironment)
	}

	rootURI, cwdURI, err := shellEnvironmentURIs(environment.Platform, environment.Root, cwd)
	if err != nil {
		return ShellV1Plan{}, err
	}
	startParams, err := json.Marshal(shellProcessStartParams{
		ProcessID: identities.ProcessID,
		Argv:      slices.Clone(arguments.Argv),
		CWD:       cwdURI,
		Env:       explicitEnvironment,
		EnvPolicy: shellCleanEnvironmentPolicy{
			Inherit:               "none",
			IgnoreDefaultExcludes: false,
			Exclude:               []string{},
			Set:                   map[string]string{},
			IncludeOnly:           []string{},
		},
		TTY:       tty,
		PipeStdin: false,
		Arg0:      nil,
		Sandbox: shellSandboxContext{
			Permissions: shellSandboxPermissions{
				Type: "managed",
				FileSystem: shellSandboxFileSystem{
					Type: "restricted",
					Entries: []shellSandboxEntry{
						{
							Path: shellSandboxPath{
								Type:  "special",
								Value: &shellSandboxSpecialPath{Kind: "minimal"},
							},
							Access: "read",
						},
						{Path: shellSandboxPath{Type: "path", Path: rootURI}, Access: "write"},
					},
				},
				Network: "restricted",
			},
			CWD:                          cwdURI,
			WorkspaceRoots:               []string{rootURI},
			WindowsSandboxLevel:          shellWindowsSandboxLevel(environment.Platform),
			WindowsSandboxPrivateDesktop: false,
			UseLegacyLandlock:            false,
		},
		EnforceManagedNetwork: true,
	})
	if err != nil {
		return ShellV1Plan{}, fmt.Errorf("encode shell-v1 process/start params: %w", err)
	}
	timeoutParams, err := json.Marshal(struct {
		ProcessID string `json:"processId"`
	}{ProcessID: identities.ProcessID})
	if err != nil {
		return ShellV1Plan{}, fmt.Errorf("encode shell-v1 process/terminate params: %w", err)
	}
	startRPC, err := marshalShellRPC(identities.StartRPCRequestID, execprofile.MethodProcessStart, startParams)
	if err != nil {
		return ShellV1Plan{}, err
	}
	timeoutRPC, err := marshalShellRPC(identities.TimeoutRPCRequestID, execprofile.MethodProcessTerminate, timeoutParams)
	if err != nil {
		return ShellV1Plan{}, err
	}
	startRouting := agentxconn.RoutingContext{
		WorkspaceID:          principal.WorkspaceID,
		RunID:                principal.Run.RunID,
		RunAttemptID:         principal.Run.RunAttemptID,
		RunAttemptGeneration: principal.Run.RunAttemptGeneration,
		ExecutionID:          identities.ExecutionID,
		OperationID:          identities.StartOperationID,
		EnvID:                environment.EnvironmentID,
		MutationKey:          identities.StartMutationKey,
	}
	timeoutRouting := startRouting
	timeoutRouting.OperationID = identities.TimeoutOperationID
	timeoutRouting.MutationKey = identities.TimeoutMutationKey
	operationPlan, err := json.Marshal(shellOperationPlan{
		Version:   "shell-v1",
		Lifecycle: "run",
		ProcessID: identities.ProcessID,
		Operations: []shellOperationPlanEntry{
			{
				Ordinal: 1, OperationID: identities.StartOperationID, Kind: ShellV1OperationProcessStart,
				EffectClass: ShellV1EffectMutation, MutationKey: identities.StartMutationKey,
				Method: execprofile.MethodProcessStart, RPCRequestID: identities.StartRPCRequestID, Dispatch: "required",
			},
			{
				Ordinal: 2, OperationID: identities.TimeoutOperationID, Kind: ShellV1OperationTimeoutTerminate,
				EffectClass: ShellV1EffectMutation, MutationKey: identities.TimeoutMutationKey,
				Method: execprofile.MethodProcessTerminate, RPCRequestID: identities.TimeoutRPCRequestID,
				Dispatch: "deadline_reached", AfterMillis: timeoutMillis,
			},
		},
	})
	if err != nil {
		return ShellV1Plan{}, fmt.Errorf("encode shell-v1 operation plan: %w", err)
	}
	policyContext, err := json.Marshal(shellPolicyContext{
		Version:                "shell-policy-context-v1",
		WorkspaceID:            principal.WorkspaceID,
		RunID:                  principal.Run.RunID,
		RunAttemptID:           principal.Run.RunAttemptID,
		RunAttemptGeneration:   principal.Run.RunAttemptGeneration,
		ExecutorID:             environment.ExecutorID,
		EnvironmentID:          environment.EnvironmentID,
		EnvironmentVersion:     environment.EnvironmentVersion,
		ConnectionGeneration:   environment.ConnectionGeneration,
		Platform:               environment.Platform,
		RootURI:                rootURI,
		CWDURI:                 cwdURI,
		Lifecycle:              "run",
		EnvironmentInheritance: "none",
		FilesystemProfile:      "managed-restricted-workspace-v1",
		NetworkProfile:         "managed-restricted",
		EnforceManagedNetwork:  true,
	})
	if err != nil {
		return ShellV1Plan{}, fmt.Errorf("encode shell-v1 policy context: %w", err)
	}
	tool, found := mcpcontract.Lookup(mcpcontract.ToolShell)
	if !found || tool.MapperVersion != "shell-v1" {
		return ShellV1Plan{}, errors.New("shell-v1 is missing from the executor MCP contract")
	}
	return ShellV1Plan{
		Arguments:      append(json.RawMessage(nil), rawArguments...),
		ToolSchema:     append(json.RawMessage(nil), tool.InputSchema...),
		OperationPlan:  operationPlan,
		PolicyContext:  policyContext,
		PolicyDecision: policy.Decision,
		PolicyVersion:  policy.Version,
		ToolCallID:     toolCallID,
		Environment:    environment,
		ProcessID:      identities.ProcessID,
		TimeoutMillis:  timeoutMillis,
		RootURI:        rootURI,
		CWDURI:         cwdURI,
		Start: ShellV1Operation{
			OperationID: identities.StartOperationID, Ordinal: 1, Kind: ShellV1OperationProcessStart,
			EffectClass: ShellV1EffectMutation, MutationKey: identities.StartMutationKey,
			Params: startParams, RPC: startRPC, Routing: startRouting,
		},
		Timeout: ShellV1Operation{
			OperationID: identities.TimeoutOperationID, Ordinal: 2, Kind: ShellV1OperationTimeoutTerminate,
			EffectClass: ShellV1EffectMutation, MutationKey: identities.TimeoutMutationKey,
			Params: timeoutParams, RPC: timeoutRPC, Routing: timeoutRouting,
		},
		Directives: agentxconn.DispatchDirectives{ProcessTimeout: &agentxconn.ProcessTimeoutDirective{
			AfterMillis: timeoutMillis,
			OperationID: identities.TimeoutOperationID,
			MutationKey: identities.TimeoutMutationKey,
		}},
	}, nil
}

func validateShellV1Arguments(arguments ShellV1Arguments, environment ResolvedEnvironment) error {
	if arguments.EnvironmentID != environment.EnvironmentID {
		return errors.New("shell environment_id differs from the resolved environment")
	}
	if len(arguments.Argv) < 1 || len(arguments.Argv) > 256 {
		return errors.New("shell argv must contain between 1 and 256 entries")
	}
	for index, argument := range arguments.Argv {
		if err := validateShellString(fmt.Sprintf("shell argv[%d]", index), argument, 16*1024); err != nil {
			return err
		}
	}
	if arguments.CWD != "" {
		if err := validateRelativeEnvironmentPath(arguments.CWD); err != nil {
			return fmt.Errorf("shell cwd: %w", err)
		}
	}
	if len(arguments.Env) > 256 {
		return errors.New("shell env exceeds 256 entries")
	}
	for name, value := range arguments.Env {
		if !shellEnvironmentNamePattern.MatchString(name) {
			return fmt.Errorf("shell env contains invalid name %q", name)
		}
		if err := validateShellString("shell environment value", value, 16*1024); err != nil {
			return err
		}
	}
	if arguments.TimeoutMillis != nil && (*arguments.TimeoutMillis < 1 || *arguments.TimeoutMillis > 3_600_000) {
		return errors.New("shell timeout_ms must be between 1 and 3600000")
	}
	return nil
}

func validateShellString(name, value string, maximumRunes int) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || utf8.RuneCountInString(value) > maximumRunes {
		return fmt.Errorf("%s must be valid UTF-8 without NUL and at most %d characters", name, maximumRunes)
	}
	return nil
}

func validateShellV1Identities(identities ShellV1Identities) error {
	values := []struct {
		name  string
		value string
	}{
		{"shell execution ID", identities.ExecutionID},
		{"shell process ID", identities.ProcessID},
		{"shell start operation ID", identities.StartOperationID},
		{"shell start mutation key", identities.StartMutationKey},
		{"shell timeout operation ID", identities.TimeoutOperationID},
		{"shell timeout mutation key", identities.TimeoutMutationKey},
		{"shell start RPC request ID", identities.StartRPCRequestID},
		{"shell timeout RPC request ID", identities.TimeoutRPCRequestID},
	}
	seen := make(map[string]struct{}, len(values))
	for _, identity := range values {
		if err := validateRegistryIdentity(identity.name, identity.value); err != nil {
			return err
		}
		if _, duplicate := seen[identity.value]; duplicate {
			return errors.New("shell-v1 identities must be distinct")
		}
		seen[identity.value] = struct{}{}
	}
	return nil
}

func shellEnvironmentURIs(platform, root, relativeCWD string) (string, string, error) {
	if err := validateRegisteredRoot(platform, root); err != nil {
		return "", "", err
	}
	if err := validateRelativeEnvironmentPath(relativeCWD); err != nil {
		return "", "", err
	}
	if strings.HasPrefix(platform, "windows-") {
		normalizedRoot := strings.ReplaceAll(root, `\`, "/")
		cwdPath := normalizedRoot
		if relativeCWD != "." {
			cwdPath = strings.TrimSuffix(normalizedRoot, "/") + "/" + relativeCWD
		}
		return (&url.URL{Scheme: "file", Path: "/" + normalizedRoot}).String(), (&url.URL{Scheme: "file", Path: "/" + cwdPath}).String(), nil
	}
	cwdPath := root
	if relativeCWD != "." {
		cwdPath = path.Join(root, relativeCWD)
	}
	return (&url.URL{Scheme: "file", Path: root}).String(), (&url.URL{Scheme: "file", Path: cwdPath}).String(), nil
}

func shellWindowsSandboxLevel(platform string) string {
	if strings.HasPrefix(platform, "windows-") {
		return "restricted-token"
	}
	return "disabled"
}

func marshalShellRPC(requestID, method string, params json.RawMessage) (json.RawMessage, error) {
	rpc, err := json.Marshal(struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}{ID: requestID, Method: method, Params: params})
	if err != nil {
		return nil, fmt.Errorf("encode shell-v1 %s RPC: %w", method, err)
	}
	return rpc, nil
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

type shellProcessStartParams struct {
	ProcessID             string                      `json:"processId"`
	Argv                  []string                    `json:"argv"`
	CWD                   string                      `json:"cwd"`
	Env                   map[string]string           `json:"env"`
	EnvPolicy             shellCleanEnvironmentPolicy `json:"envPolicy"`
	TTY                   bool                        `json:"tty"`
	PipeStdin             bool                        `json:"pipeStdin"`
	Arg0                  *string                     `json:"arg0"`
	Sandbox               shellSandboxContext         `json:"sandbox"`
	EnforceManagedNetwork bool                        `json:"enforceManagedNetwork"`
}

type shellCleanEnvironmentPolicy struct {
	Inherit               string            `json:"inherit"`
	IgnoreDefaultExcludes bool              `json:"ignoreDefaultExcludes"`
	Exclude               []string          `json:"exclude"`
	Set                   map[string]string `json:"set"`
	IncludeOnly           []string          `json:"includeOnly"`
}

type shellSandboxContext struct {
	Permissions                  shellSandboxPermissions `json:"permissions"`
	CWD                          string                  `json:"cwd"`
	WorkspaceRoots               []string                `json:"workspaceRoots"`
	WindowsSandboxLevel          string                  `json:"windowsSandboxLevel"`
	WindowsSandboxPrivateDesktop bool                    `json:"windowsSandboxPrivateDesktop"`
	UseLegacyLandlock            bool                    `json:"useLegacyLandlock"`
}

type shellSandboxPermissions struct {
	Type       string                 `json:"type"`
	FileSystem shellSandboxFileSystem `json:"file_system"`
	Network    string                 `json:"network"`
}

type shellSandboxFileSystem struct {
	Type    string              `json:"type"`
	Entries []shellSandboxEntry `json:"entries"`
}

type shellSandboxEntry struct {
	Path   shellSandboxPath `json:"path"`
	Access string           `json:"access"`
}

type shellSandboxPath struct {
	Type  string                   `json:"type"`
	Path  string                   `json:"path,omitempty"`
	Value *shellSandboxSpecialPath `json:"value,omitempty"`
}

type shellSandboxSpecialPath struct {
	Kind string `json:"kind"`
}

type shellOperationPlan struct {
	Version    string                    `json:"version"`
	Lifecycle  string                    `json:"lifecycle"`
	ProcessID  string                    `json:"processId"`
	Operations []shellOperationPlanEntry `json:"operations"`
}

type shellOperationPlanEntry struct {
	Ordinal      int    `json:"ordinal"`
	OperationID  string `json:"operationId"`
	Kind         string `json:"kind"`
	EffectClass  string `json:"effectClass"`
	MutationKey  string `json:"mutationKey"`
	Method       string `json:"method"`
	RPCRequestID string `json:"rpcRequestId"`
	Dispatch     string `json:"dispatch"`
	AfterMillis  int64  `json:"afterMs,omitempty"`
}

type shellPolicyContext struct {
	Version                string `json:"version"`
	WorkspaceID            string `json:"workspaceId"`
	RunID                  string `json:"runId"`
	RunAttemptID           string `json:"runAttemptId"`
	RunAttemptGeneration   int64  `json:"runAttemptGeneration"`
	ExecutorID             string `json:"executorId"`
	EnvironmentID          string `json:"environmentId"`
	EnvironmentVersion     int64  `json:"environmentVersion"`
	ConnectionGeneration   int64  `json:"connectionGeneration"`
	Platform               string `json:"platform"`
	RootURI                string `json:"rootUri"`
	CWDURI                 string `json:"cwdUri"`
	Lifecycle              string `json:"lifecycle"`
	EnvironmentInheritance string `json:"environmentInheritance"`
	FilesystemProfile      string `json:"filesystemProfile"`
	NetworkProfile         string `json:"networkProfile"`
	EnforceManagedNetwork  bool   `json:"enforceManagedNetwork"`
}
