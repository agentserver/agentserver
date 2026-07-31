package executorgateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/agentxconn"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
)

const (
	ReadFileV1DefaultLimit  uint64 = execprofile.MaxFilesystemReadLength
	ReadFileV1PolicyVersion        = "read-file-policy-v1"
	ReadFileV1OperationRead        = "fs_read"
	ReadFileV1EffectRead           = "read"
)

type ReadFileV1Arguments struct {
	EnvironmentID string  `json:"environment_id"`
	Path          string  `json:"path"`
	Offset        *uint64 `json:"offset,omitempty"`
	Limit         *uint64 `json:"limit,omitempty"`
}

type ReadFileV1Identities struct {
	ExecutionID  string
	OperationID  string
	MutationKey  string
	RPCRequestID string
}

type ReadFileV1IdentityAllocator struct {
	mu          sync.Mutex
	idGenerator IDGenerator
}

func NewReadFileV1IdentityAllocator(idGenerator IDGenerator) (*ReadFileV1IdentityAllocator, error) {
	if idGenerator == nil {
		return nil, errors.New("read-file identity generator is required")
	}
	return &ReadFileV1IdentityAllocator{idGenerator: idGenerator}, nil
}

func NewDefaultReadFileV1IdentityAllocator() (*ReadFileV1IdentityAllocator, error) {
	return NewReadFileV1IdentityAllocator(newRandomUUID)
}

func (allocator *ReadFileV1IdentityAllocator) Allocate() (ReadFileV1Identities, error) {
	if allocator == nil {
		return ReadFileV1Identities{}, errors.New("read-file identity allocator is required")
	}
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	identities := ReadFileV1Identities{}
	destinations := []*string{
		&identities.ExecutionID,
		&identities.OperationID,
		&identities.MutationKey,
		&identities.RPCRequestID,
	}
	for _, destination := range destinations {
		identity, err := allocator.idGenerator()
		if err != nil {
			return ReadFileV1Identities{}, err
		}
		*destination = identity
	}
	if err := validateReadFileV1Identities(identities); err != nil {
		return ReadFileV1Identities{}, err
	}
	return identities, nil
}

type ReadFileV1Operation struct {
	OperationID string
	Ordinal     int
	Kind        string
	EffectClass string
	MutationKey string
	Params      json.RawMessage
	RPC         json.RawMessage
	Routing     agentxconn.RoutingContext
}

// ReadFileV1Plan is the immutable projection of one bounded read. RelativePath
// is retained for the MCP result while PathURI is the only path sent to agentx.
// The outer request remains one core operation even though agentx composes it
// from a disposable stock fs/open, fs/readBlock, and fs/close sequence.
type ReadFileV1Plan struct {
	Arguments      json.RawMessage
	ToolSchema     json.RawMessage
	OperationPlan  json.RawMessage
	PolicyContext  json.RawMessage
	PolicyDecision string
	PolicyVersion  string
	ToolCallID     string
	Environment    ResolvedEnvironment
	RelativePath   string
	RootURI        string
	PathURI        string
	Offset         uint64
	Limit          uint64
	RPCRequestID   string
	Read           ReadFileV1Operation
}

func MapReadFileV1(rawArguments json.RawMessage, principal ExecutorMCPPrincipal, toolCallID string, environment ResolvedEnvironment, identities ReadFileV1Identities) (ReadFileV1Plan, error) {
	if err := validateExecutorMCPPrincipal(principal); err != nil {
		return ReadFileV1Plan{}, fmt.Errorf("read-file principal: %w", err)
	}
	if toolCallID == "" || len(toolCallID) > 256 || !utf8.ValidString(toolCallID) || strings.ContainsRune(toolCallID, 0) {
		return ReadFileV1Plan{}, errors.New("app-server tool call ID must contain between 1 and 256 valid UTF-8 bytes without NUL")
	}
	if err := validateReadFileV1Identities(identities); err != nil {
		return ReadFileV1Plan{}, err
	}
	resolved, err := resolveRegisteredEnvironment(environment.RegisteredEnvironment)
	if err != nil {
		return ReadFileV1Plan{}, fmt.Errorf("read-file environment: %w", err)
	}
	if resolved.Root != environment.Root || resolved.DefaultCWD != environment.DefaultCWD ||
		resolved.DisplayName != environment.DisplayName || resolved.Description != environment.Description {
		return ReadFileV1Plan{}, errors.New("read-file environment projection differs from its registered descriptor")
	}
	if principal.ExecutorID != "" && environment.ExecutorID != principal.ExecutorID {
		return ReadFileV1Plan{}, errors.New("read-file environment is outside the authenticated executor scope")
	}
	if !execprofile.SupportsFilesystemRead(environment.OuterProfileVersion) {
		return ReadFileV1Plan{}, errors.New("read-file environment does not advertise the bounded filesystem-read profile")
	}

	var arguments ReadFileV1Arguments
	if err := decodeExactJSON(rawArguments, &arguments); err != nil {
		return ReadFileV1Plan{}, fmt.Errorf("decode read-file-v1 arguments: %w", err)
	}
	if arguments.EnvironmentID != environment.EnvironmentID {
		return ReadFileV1Plan{}, errors.New("read_file environment_id differs from the resolved environment")
	}
	if err := validateReadFileRelativePath(arguments.Path); err != nil {
		return ReadFileV1Plan{}, fmt.Errorf("read_file path: %w", err)
	}
	offset := uint64(0)
	if arguments.Offset != nil {
		offset = *arguments.Offset
	}
	if offset > execprofile.MaxFilesystemReadOffset {
		return ReadFileV1Plan{}, fmt.Errorf("read_file offset exceeds %d", execprofile.MaxFilesystemReadOffset)
	}
	limit := ReadFileV1DefaultLimit
	if arguments.Limit != nil {
		limit = *arguments.Limit
	}
	if limit < 1 || limit > execprofile.MaxFilesystemReadLength {
		return ReadFileV1Plan{}, fmt.Errorf("read_file limit must be between 1 and %d", execprofile.MaxFilesystemReadLength)
	}

	rootURI, pathURI, err := readFileEnvironmentURIs(environment.Platform, environment.Root, arguments.Path)
	if err != nil {
		return ReadFileV1Plan{}, err
	}
	params, err := json.Marshal(readFileBlockParams{Path: pathURI, Offset: offset, Length: limit})
	if err != nil {
		return ReadFileV1Plan{}, fmt.Errorf("encode read-file-v1 params: %w", err)
	}
	rpc, err := marshalReadFileRPC(identities.RPCRequestID, params)
	if err != nil {
		return ReadFileV1Plan{}, err
	}
	routing := agentxconn.RoutingContext{
		WorkspaceID:          principal.WorkspaceID,
		RunID:                principal.Run.RunID,
		RunAttemptID:         principal.Run.RunAttemptID,
		RunAttemptGeneration: principal.Run.RunAttemptGeneration,
		ExecutionID:          identities.ExecutionID,
		OperationID:          identities.OperationID,
		EnvID:                environment.EnvironmentID,
		MutationKey:          identities.MutationKey,
	}
	operationPlan, err := json.Marshal(readFileOperationPlan{
		Version:   "read-file-v1",
		Lifecycle: "request",
		Operations: []readFileOperationPlanEntry{{
			Ordinal: 1, OperationID: identities.OperationID, Kind: ReadFileV1OperationRead,
			EffectClass: ReadFileV1EffectRead, MutationKey: identities.MutationKey,
			Method: execprofile.MethodFilesystemReadFileBlock, RPCRequestID: identities.RPCRequestID,
			Dispatch: "required", Retry: "none-phase1",
		}},
	})
	if err != nil {
		return ReadFileV1Plan{}, fmt.Errorf("encode read-file-v1 operation plan: %w", err)
	}
	policyContext, err := json.Marshal(readFilePolicyContext{
		Version:              "read-file-policy-context-v1",
		WorkspaceID:          principal.WorkspaceID,
		RunID:                principal.Run.RunID,
		RunAttemptID:         principal.Run.RunAttemptID,
		RunAttemptGeneration: principal.Run.RunAttemptGeneration,
		ExecutorID:           environment.ExecutorID,
		EnvironmentID:        environment.EnvironmentID,
		EnvironmentVersion:   environment.EnvironmentVersion,
		ConnectionGeneration: environment.ConnectionGeneration,
		Platform:             environment.Platform,
		OuterProfileVersion:  environment.OuterProfileVersion,
		RootURI:              rootURI,
		RelativePath:         arguments.Path,
		PathURI:              pathURI,
		Offset:               offset,
		Limit:                limit,
		FilesystemProfile:    "bounded-registered-root-read-v1",
	})
	if err != nil {
		return ReadFileV1Plan{}, fmt.Errorf("encode read-file-v1 policy context: %w", err)
	}
	tool, found := mcpcontract.Lookup(mcpcontract.ToolReadFile)
	if !found || tool.MapperVersion != "read-file-v1" {
		return ReadFileV1Plan{}, errors.New("read-file-v1 is missing from the executor MCP contract")
	}
	return ReadFileV1Plan{
		Arguments:      append(json.RawMessage(nil), rawArguments...),
		ToolSchema:     append(json.RawMessage(nil), tool.InputSchema...),
		OperationPlan:  operationPlan,
		PolicyContext:  policyContext,
		PolicyDecision: "allow",
		PolicyVersion:  ReadFileV1PolicyVersion,
		ToolCallID:     toolCallID,
		Environment:    environment,
		RelativePath:   arguments.Path,
		RootURI:        rootURI,
		PathURI:        pathURI,
		Offset:         offset,
		Limit:          limit,
		RPCRequestID:   identities.RPCRequestID,
		Read: ReadFileV1Operation{
			OperationID: identities.OperationID, Ordinal: 1, Kind: ReadFileV1OperationRead,
			EffectClass: ReadFileV1EffectRead, MutationKey: identities.MutationKey,
			Params: params, RPC: rpc, Routing: routing,
		},
	}, nil
}

func validateReadFileRelativePath(value string) error {
	if err := validateRegistryText("environment-relative file path", value, 1, 4096); err != nil {
		return err
	}
	if strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || path.Clean(value) != value || value == "." {
		return errors.New("path must be a clean slash-separated relative file path without dot segments")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("path must be a clean slash-separated relative file path without dot segments")
		}
	}
	return nil
}

func validateReadFileV1Identities(identities ReadFileV1Identities) error {
	values := []struct {
		name  string
		value string
	}{
		{"read-file execution ID", identities.ExecutionID},
		{"read-file operation ID", identities.OperationID},
		{"read-file mutation key", identities.MutationKey},
		{"read-file RPC request ID", identities.RPCRequestID},
	}
	seen := make(map[string]struct{}, len(values))
	for _, identity := range values {
		if err := validateRegistryIdentity(identity.name, identity.value); err != nil {
			return err
		}
		if _, duplicate := seen[identity.value]; duplicate {
			return errors.New("read-file-v1 identities must be distinct")
		}
		seen[identity.value] = struct{}{}
	}
	return nil
}

func readFileEnvironmentURIs(platform, root, relativePath string) (string, string, error) {
	if err := validateRegisteredRoot(platform, root); err != nil {
		return "", "", err
	}
	if err := validateReadFileRelativePath(relativePath); err != nil {
		return "", "", err
	}
	rootPath := root
	if strings.HasPrefix(platform, "windows-") {
		rootPath = "/" + strings.ReplaceAll(root, `\`, "/")
	}
	rootURI := (&url.URL{Scheme: "file", Path: rootPath}).String()
	targetURI := (&url.URL{Scheme: "file", Path: strings.TrimSuffix(rootPath, "/") + "/" + relativePath}).String()
	if len(rootURI) > 4096 || len(targetURI) > 4096 {
		return "", "", errors.New("read-file URI exceeds the agentx 4096-byte path bound")
	}
	return rootURI, targetURI, nil
}

func marshalReadFileRPC(requestID string, params json.RawMessage) (json.RawMessage, error) {
	rpc, err := json.Marshal(struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}{ID: requestID, Method: execprofile.MethodFilesystemReadFileBlock, Params: params})
	if err != nil {
		return nil, fmt.Errorf("encode read-file-v1 RPC: %w", err)
	}
	return rpc, nil
}

type readFileBlockParams struct {
	Path   string `json:"path"`
	Offset uint64 `json:"offset"`
	Length uint64 `json:"len"`
}

type readFileOperationPlan struct {
	Version    string                       `json:"version"`
	Lifecycle  string                       `json:"lifecycle"`
	Operations []readFileOperationPlanEntry `json:"operations"`
}

type readFileOperationPlanEntry struct {
	Ordinal      int    `json:"ordinal"`
	OperationID  string `json:"operationId"`
	Kind         string `json:"kind"`
	EffectClass  string `json:"effectClass"`
	MutationKey  string `json:"mutationKey"`
	Method       string `json:"method"`
	RPCRequestID string `json:"rpcRequestId"`
	Dispatch     string `json:"dispatch"`
	Retry        string `json:"retry"`
}

type readFilePolicyContext struct {
	Version              string `json:"version"`
	WorkspaceID          string `json:"workspaceId"`
	RunID                string `json:"runId"`
	RunAttemptID         string `json:"runAttemptId"`
	RunAttemptGeneration int64  `json:"runAttemptGeneration"`
	ExecutorID           string `json:"executorId"`
	EnvironmentID        string `json:"environmentId"`
	EnvironmentVersion   int64  `json:"environmentVersion"`
	ConnectionGeneration int64  `json:"connectionGeneration"`
	Platform             string `json:"platform"`
	OuterProfileVersion  string `json:"outerProfileVersion"`
	RootURI              string `json:"rootUri"`
	RelativePath         string `json:"relativePath"`
	PathURI              string `json:"pathUri"`
	Offset               uint64 `json:"offset"`
	Limit                uint64 `json:"limit"`
	FilesystemProfile    string `json:"filesystemProfile"`
}
