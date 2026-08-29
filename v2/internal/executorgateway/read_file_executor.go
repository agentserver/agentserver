package executorgateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/executionbackend"
)

const MaxReadFileV1ResultBytes = 2 * 1024 * 1024

type ReadFileV1Result struct {
	Status         string `json:"status"`
	Path           string `json:"path"`
	Offset         uint64 `json:"offset"`
	RequestedBytes uint64 `json:"requested_bytes"`
	BytesRead      uint64 `json:"bytes_read"`
	EOF            bool   `json:"eof"`
	Encoding       string `json:"encoding"`
	Content        string `json:"content"`
}

type ReadFileExecutorConfig struct {
	Lifecycle           context.Context
	PolicyResolver      ExecutionPolicyResolver
	ApprovalGate        ExecutionApprovalGate
	BackendRouter       *executionbackend.Router
	ManagedTargetFencer ManagedTargetFencer
	Logger              *slog.Logger
}

func DefaultReadFileExecutorConfig(lifecycle context.Context) ReadFileExecutorConfig {
	return ReadFileExecutorConfig{Lifecycle: lifecycle}
}

type ReadFileExecuteRequest struct {
	Principal  ExecutorMCPPrincipal
	ToolCallID string
	Arguments  json.RawMessage
	Elicitor   ApprovalElicitor
}

// ReadFileExecutor owns the complete unary read_file state machine. Once core
// grants Begin, it switches from the MCP request context to the gateway
// lifecycle so caller cancellation cannot abandon an already-journaled RPC.
type ReadFileExecutor struct {
	resolver    *EnvironmentResolver
	authority   ExecutionAuthority
	dispatcher  FilesystemDispatcher
	identities  *ReadFileV1IdentityAllocator
	transitions *ExecutionTransitionAllocator
	config      ReadFileExecutorConfig
}

func NewReadFileExecutor(resolver *EnvironmentResolver, authority ExecutionAuthority, dispatcher FilesystemDispatcher, identities *ReadFileV1IdentityAllocator, transitions *ExecutionTransitionAllocator, config ReadFileExecutorConfig) (*ReadFileExecutor, error) {
	if resolver == nil || authority == nil || dispatcher == nil || identities == nil || transitions == nil {
		return nil, errors.New("read-file resolver, core authority, dispatcher, and allocators are required")
	}
	if config.Lifecycle == nil {
		return nil, errors.New("read-file executor lifecycle context is required")
	}
	if config.PolicyResolver == nil || config.ApprovalGate == nil {
		return nil, errors.New("read-file execution policy resolver and approval gate are required")
	}
	return &ReadFileExecutor{
		resolver: resolver, authority: authority, dispatcher: dispatcher,
		identities: identities, transitions: transitions, config: config,
	}, nil
}

func (executor *ReadFileExecutor) Execute(ctx context.Context, request ReadFileExecuteRequest) (ReadFileV1Result, error) {
	if ctx == nil {
		return ReadFileV1Result{}, errors.New("read-file execution context is required")
	}
	if err := validateExecutorMCPPrincipal(request.Principal); err != nil {
		return ReadFileV1Result{}, err
	}
	var arguments ReadFileV1Arguments
	if err := decodeExactJSON(request.Arguments, &arguments); err != nil {
		return ReadFileV1Result{}, fmt.Errorf("decode read_file arguments: %w", err)
	}
	if err := validateRegistryIdentity("read_file environment ID", arguments.EnvironmentID); err != nil {
		return ReadFileV1Result{}, err
	}
	environment, err := executor.resolver.ResolveForPrincipal(ctx, request.Principal, arguments.EnvironmentID)
	if err != nil {
		return ReadFileV1Result{}, fmt.Errorf("resolve read_file environment: %w", err)
	}
	if request.Principal.Production && environment.InsecureDev {
		return ReadFileV1Result{}, errors.New("production read_file execution cannot target an insecure-development environment")
	}
	identities, err := executor.identities.Allocate()
	if err != nil {
		return ReadFileV1Result{}, fmt.Errorf("allocate read-file identities: %w", err)
	}
	policy, err := executor.config.PolicyResolver.ResolveExecutionPolicy(ctx, ExecutionPolicyInput{
		Principal: request.Principal, ToolName: "read_file", Arguments: append(json.RawMessage(nil), request.Arguments...), Environment: environment,
	})
	if err != nil {
		return ReadFileV1Result{}, fmt.Errorf("resolve read_file execution policy: %w", err)
	}
	plan, err := MapReadFileV1(request.Arguments, request.Principal, request.ToolCallID, environment, policy, identities)
	if err != nil {
		return ReadFileV1Result{}, err
	}
	state, err := newReadFileExecutionState(executor.authority, executor.transitions, request.Principal, plan)
	if err != nil {
		return ReadFileV1Result{}, err
	}
	prepared, err := state.PrepareExecution(ctx)
	if err != nil {
		return ReadFileV1Result{}, err
	}
	switch prepared.Status {
	case "denied":
		return ReadFileV1Result{}, fmt.Errorf("%w: read_file", ErrExecutionPolicyDenied)
	case "pending_approval":
		authorized, authorizeErr := executor.config.ApprovalGate.AuthorizeExecution(ctx, ApprovalGateRequest{
			Principal: request.Principal, Execution: prepared, ToolName: "read_file",
			ToolCallID: request.ToolCallID, Elicitor: request.Elicitor,
		})
		if authorizeErr != nil {
			return ReadFileV1Result{}, authorizeErr
		}
		if err := state.AcceptAuthorizedExecution(authorized); err != nil {
			return ReadFileV1Result{}, err
		}
	case "approved":
	default:
		return ReadFileV1Result{}, fmt.Errorf("prepared read-file execution has unsupported policy status %q", prepared.Status)
	}
	if err := state.PrepareOperation(ctx); err != nil {
		return ReadFileV1Result{}, err
	}
	begin, err := state.Begin(ctx)
	if err != nil {
		return ReadFileV1Result{}, err
	}
	if !begin.Began {
		return ReadFileV1Result{}, fmt.Errorf("core did not grant the one-shot read-file dispatch; operation status is %q", begin.Operation.Status)
	}
	if environment.Target.Kind == executionbackend.KindTAE {
		return executor.executeManaged(request, plan, state)
	}

	executionCtx := executor.config.Lifecycle
	exchange, dispatchErr := executor.dispatcher.DispatchFilesystem(executionCtx, FilesystemDispatchRequest{
		ExecutorID: environment.ExecutorID, ExpectedConnectionGeneration: environment.ConnectionGeneration,
		Context: plan.Read.Routing, RPC: plan.Read.RPC,
	})
	if exchange == nil || dispatchErr != nil {
		failureClass := "dispatch_pre_send"
		if exchange != nil {
			failureClass = "dispatch_ambiguous"
		}
		return executor.closeUnknown(executionCtx, state, plan, readFileTerminalEvidence{
			Version: "read-file-terminal-evidence-v1", Kind: ReadFileV1OperationRead,
			Status: "unknown", FailureClass: failureClass,
		}, dispatchErr)
	}

	response, responseErr := exchange.AwaitResponse(executionCtx)
	if responseErr != nil {
		return executor.closeUnknown(executionCtx, state, plan, readFileTerminalEvidence{
			Version: "read-file-terminal-evidence-v1", Kind: ReadFileV1OperationRead,
			Status: "unknown", FailureClass: "exchange_failed",
		}, responseErr)
	}
	outcome, classifyErr := classifyReadFileResponse(response, plan.RPCRequestID, plan.Limit)
	if classifyErr != nil {
		return executor.closeUnknown(executionCtx, state, plan, readFileTerminalEvidence{
			Version: "read-file-terminal-evidence-v1", Kind: ReadFileV1OperationRead,
			Status: "unknown", ResponseKind: outcome.responseKind,
			ResponseSHA256: outcome.responseSHA256, ResponseBytes: outcome.responseBytes,
			FailureClass: "malformed_response",
		}, classifyErr)
	}
	acknowledgement, err := json.Marshal(readFileAcknowledgementEvidence{
		Version: "read-file-response-evidence-v1", RequestID: plan.RPCRequestID,
		ResponseKind: outcome.responseKind, ResponseSHA256: outcome.responseSHA256, ResponseBytes: outcome.responseBytes,
	})
	if err != nil {
		return ReadFileV1Result{}, err
	}
	if _, err := state.Acknowledge(executionCtx, acknowledgement); err != nil {
		return ReadFileV1Result{}, err
	}

	result := emptyReadFileResult(plan, "failed")
	terminal := readFileTerminalEvidence{
		Version: "read-file-terminal-evidence-v1", Kind: ReadFileV1OperationRead,
		Status: "failed", Acknowledged: true, ResponseKind: outcome.responseKind,
		ResponseSHA256: outcome.responseSHA256, ResponseBytes: outcome.responseBytes,
	}
	if outcome.responseKind == "result" {
		result, err = projectReadFileResult(plan, outcome.chunk, outcome.canonicalBase64, outcome.eof)
		if err != nil {
			return ReadFileV1Result{}, err
		}
		terminal.Status = "succeeded"
		terminal.ContentSHA256 = digestHex(outcome.chunk)
		terminal.BytesRead = uint64(len(outcome.chunk))
		terminal.EOF = outcome.eof
	}
	terminalJSON, err := json.Marshal(terminal)
	if err != nil {
		return ReadFileV1Result{}, err
	}
	if _, err := state.CompleteOperation(executionCtx, terminal.Status, terminalJSON); err != nil {
		return ReadFileV1Result{}, err
	}
	if _, err := state.CompleteExecution(executionCtx, terminal.Status, terminalJSON); err != nil {
		return ReadFileV1Result{}, err
	}
	return result, nil
}

func (executor *ReadFileExecutor) executeManaged(
	request ReadFileExecuteRequest,
	plan ReadFileV1Plan,
	state *readFileExecutionState,
) (ReadFileV1Result, error) {
	executionCtx := executor.config.Lifecycle
	if executor.config.BackendRouter == nil {
		result, err := executor.closeUnknown(executionCtx, state, plan, readFileTerminalEvidence{
			Version: "read-file-terminal-evidence-v1", Kind: ReadFileV1OperationRead,
			Status: "unknown", FailureClass: "backend_router_unavailable",
		}, errors.New("managed execution backend router is not configured"))
		return result, err
	}
	exchange, dispatchErr := executor.config.BackendRouter.ReadFile(executionCtx, executionbackend.ReadFileRequest{
		Target:    plan.Environment.Target,
		Operation: backendOperationContext(request.Principal, plan.Read.Routing),
		RequestID: plan.RPCRequestID, Path: plan.AbsolutePath,
		Offset: plan.Offset, Limit: plan.Limit,
	})
	if exchange == nil {
		executor.logManagedReadDispatchFailure(request.Principal, plan.Environment.Target,
			backendOperationContext(request.Principal, plan.Read.Routing), "read_dispatch", dispatchErr)
		result, closeErr := executor.closeUnknown(executionCtx, state, plan, readFileTerminalEvidence{
			Version: "read-file-terminal-evidence-v1", Kind: ReadFileV1OperationRead,
			Status: "unknown", FailureClass: "dispatch_unknown",
		}, dispatchErr)
		executor.fenceManagedReadUnknown(executionCtx, request.Principal, plan.Environment.Target, "read_file_dispatch_unknown")
		return result, closeErr
	}
	acknowledgement, err := exchange.AwaitAcknowledgement(executionCtx)
	if err != nil || dispatchErr != nil {
		executor.logManagedReadDispatchFailure(request.Principal, plan.Environment.Target,
			backendOperationContext(request.Principal, plan.Read.Routing), "read_acknowledgement", errors.Join(dispatchErr, err))
		result, closeErr := executor.closeUnknown(executionCtx, state, plan, readFileTerminalEvidence{
			Version: "read-file-terminal-evidence-v1", Kind: ReadFileV1OperationRead,
			Status: "unknown", FailureClass: "acknowledgement_unknown",
		}, errors.Join(dispatchErr, err))
		executor.fenceManagedReadUnknown(executionCtx, request.Principal, plan.Environment.Target, "read_file_acknowledgement_unknown")
		return result, closeErr
	}
	acknowledgementJSON, err := marshalBackendAcknowledgement(plan.RPCRequestID, acknowledgement)
	if err != nil {
		return ReadFileV1Result{}, err
	}
	if _, err := state.Acknowledge(executionCtx, acknowledgementJSON); err != nil {
		return ReadFileV1Result{}, err
	}

	content := make([]byte, 0, min(int(plan.Limit), 64*1024))
	nextSequence := uint64(1)
	for {
		event, eventErr := exchange.NextEvent(executionCtx)
		if errors.Is(eventErr, io.EOF) {
			break
		}
		if eventErr != nil || event.Sequence != nextSequence || event.Kind != executionbackend.EventFileBytes || uint64(len(content))+uint64(len(event.Data)) > plan.Limit {
			if eventErr == nil {
				eventErr = errors.New("managed read-file event stream is invalid or exceeds the frozen limit")
			}
			result, closeErr := executor.closeUnknown(executionCtx, state, plan, readFileTerminalEvidence{
				Version: "read-file-terminal-evidence-v1", Kind: ReadFileV1OperationRead,
				Status: "unknown", Acknowledged: true, FailureClass: "exchange_failed",
			}, eventErr)
			executor.fenceManagedReadUnknown(executionCtx, request.Principal, plan.Environment.Target, "read_file_stream_unknown")
			return result, closeErr
		}
		nextSequence++
		content = append(content, event.Data...)
	}
	terminal, err := exchange.AwaitTerminal(executionCtx)
	if err != nil {
		result, closeErr := executor.closeUnknown(executionCtx, state, plan, readFileTerminalEvidence{
			Version: "read-file-terminal-evidence-v1", Kind: ReadFileV1OperationRead,
			Status: "unknown", Acknowledged: true, FailureClass: "terminal_unknown",
		}, err)
		executor.fenceManagedReadUnknown(executionCtx, request.Principal, plan.Environment.Target, "read_file_terminal_unknown")
		return result, closeErr
	}
	status := "failed"
	if terminal.Status == executionbackend.TerminalUnknown || !terminal.OutputComplete {
		status = "unknown"
	} else if terminal.Status == executionbackend.TerminalSucceeded {
		status = "succeeded"
	}
	terminalJSON, err := json.Marshal(readFileTerminalEvidence{
		Version: "read-file-terminal-evidence-v1", Kind: ReadFileV1OperationRead,
		Status: status, Acknowledged: true, ResponseKind: "backend_terminal",
		ContentSHA256: digestHex(content), BytesRead: uint64(len(content)),
		EOF: uint64(len(content)) < plan.Limit,
	})
	if err != nil {
		return ReadFileV1Result{}, err
	}
	if _, err := state.CompleteOperation(executionCtx, status, terminalJSON); err != nil {
		return ReadFileV1Result{}, err
	}
	if _, err := state.CompleteExecution(executionCtx, status, terminalJSON); err != nil {
		return ReadFileV1Result{}, err
	}
	if status == "unknown" {
		executor.fenceManagedReadUnknown(executionCtx, request.Principal, plan.Environment.Target, "read_file_outcome_unknown")
		return emptyReadFileResult(plan, "unknown"), nil
	}
	if status == "failed" {
		return emptyReadFileResult(plan, "failed"), nil
	}
	canonicalBase64 := base64.StdEncoding.EncodeToString(content)
	return projectReadFileResult(plan, content, canonicalBase64, uint64(len(content)) < plan.Limit)
}

func (executor *ReadFileExecutor) logManagedReadDispatchFailure(
	principal ExecutorMCPPrincipal,
	target executionbackend.Target,
	operation executionbackend.OperationContext,
	stage string,
	err error,
) {
	if executor == nil || executor.config.Logger == nil {
		return
	}
	var dispatchError *executionbackend.DispatchError
	if !errors.As(err, &dispatchError) || dispatchError == nil {
		return
	}
	executor.config.Logger.Error("managed read-file dispatch failed",
		"workspace_id", principal.WorkspaceID,
		"run_id", operation.RunID,
		"run_attempt_id", operation.RunAttemptID,
		"execution_id", operation.ExecutionID,
		"operation_id", operation.OperationID,
		"target_id", target.ID,
		"target_generation", target.Generation,
		"dispatch_stage", stage,
		"dispatch_outcome", dispatchError.Outcome,
		"reason_code", dispatchError.Code,
		"provider_http_status", dispatchError.HTTPStatus,
		"provider_code", dispatchError.ProviderCode,
		"provider_request_id", dispatchError.ProviderRequestID,
		"request_written", dispatchError.RequestWritten,
	)
}

func (executor *ReadFileExecutor) fenceManagedReadUnknown(ctx context.Context, principal ExecutorMCPPrincipal, target executionbackend.Target, reason string) {
	if executor.config.ManagedTargetFencer != nil {
		_ = executor.config.ManagedTargetFencer.FenceManagedTarget(ctx, principal, target, reason)
	}
}

func (executor *ReadFileExecutor) closeUnknown(ctx context.Context, state *readFileExecutionState, plan ReadFileV1Plan, evidence readFileTerminalEvidence, cause error) (ReadFileV1Result, error) {
	result := emptyReadFileResult(plan, "unknown")
	terminalJSON, err := json.Marshal(evidence)
	if err != nil {
		return ReadFileV1Result{}, err
	}
	if _, err := state.CompleteOperation(ctx, "unknown", terminalJSON); err != nil {
		return ReadFileV1Result{}, errors.Join(cause, err)
	}
	if _, err := state.CompleteExecution(ctx, "unknown", terminalJSON); err != nil {
		return ReadFileV1Result{}, errors.Join(cause, err)
	}
	return result, nil
}

type readFileResponseOutcome struct {
	responseKind    string
	responseSHA256  string
	responseBytes   int
	chunk           []byte
	canonicalBase64 string
	eof             bool
}

func classifyReadFileResponse(raw json.RawMessage, expectedRequestID string, maximum uint64) (readFileResponseOutcome, error) {
	outcome := readFileResponseOutcome{responseSHA256: digestHex(raw), responseBytes: len(raw)}
	message, err := codexwire.Parse(raw)
	if err != nil {
		return outcome, fmt.Errorf("parse read-file response: %w", err)
	}
	wantID, err := json.Marshal(expectedRequestID)
	if err != nil {
		return outcome, err
	}
	wantCanonical, err := canonicalRPCID(wantID)
	if err != nil {
		return outcome, err
	}
	gotCanonical, err := canonicalRPCID(message.ID)
	if err != nil || gotCanonical != wantCanonical {
		return outcome, errors.New("read-file response request ID differs from the frozen request")
	}
	switch message.Kind {
	case codexwire.KindError:
		outcome.responseKind = "error"
		return outcome, nil
	case codexwire.KindResponse:
		outcome.responseKind = "result"
	default:
		return outcome, errors.New("read-file exchange did not return a response or error")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(message.Result, &fields); err != nil || fields == nil || len(fields) != 2 {
		return outcome, errors.New("read-file result must contain exactly chunk and eof")
	}
	if _, ok := fields["chunk"]; !ok {
		return outcome, errors.New("read-file result omitted chunk")
	}
	if _, ok := fields["eof"]; !ok {
		return outcome, errors.New("read-file result omitted eof")
	}
	var block struct {
		Chunk *string `json:"chunk"`
		EOF   *bool   `json:"eof"`
	}
	if err := decodeExactJSON(message.Result, &block); err != nil || block.Chunk == nil || block.EOF == nil {
		return outcome, errors.New("decode read-file result")
	}
	if uint64(len(*block.Chunk)) > uint64(base64.StdEncoding.EncodedLen(int(maximum))) {
		return outcome, errors.New("read-file result exceeds the requested byte bound")
	}
	decoded, err := base64.StdEncoding.DecodeString(*block.Chunk)
	if err != nil || base64.StdEncoding.EncodeToString(decoded) != *block.Chunk || uint64(len(decoded)) > maximum {
		return outcome, errors.New("read-file result contains invalid or oversized canonical base64")
	}
	outcome.chunk = decoded
	outcome.canonicalBase64 = *block.Chunk
	outcome.eof = *block.EOF
	return outcome, nil
}

func projectReadFileResult(plan ReadFileV1Plan, content []byte, canonicalBase64 string, eof bool) (ReadFileV1Result, error) {
	resultPath := plan.RequestedPath
	if resultPath == "" {
		// Keep direct unit/legacy callers that construct a plan by hand
		// compatible while all mapped requests carry RequestedPath explicitly.
		resultPath = plan.RelativePath
	}
	result := ReadFileV1Result{
		Status: "succeeded", Path: resultPath, Offset: plan.Offset, RequestedBytes: plan.Limit,
		BytesRead: uint64(len(content)), EOF: eof,
	}
	if utf8.Valid(content) {
		result.Encoding = "utf-8"
		result.Content = string(content)
		if encoded, err := json.Marshal(result); err == nil && len(encoded) <= MaxReadFileV1ResultBytes {
			return result, nil
		}
	}
	result.Encoding = "base64"
	result.Content = canonicalBase64
	encoded, err := json.Marshal(result)
	if err != nil {
		return ReadFileV1Result{}, err
	}
	if len(encoded) > MaxReadFileV1ResultBytes {
		return ReadFileV1Result{}, fmt.Errorf("read_file MCP result is %d bytes, limit is %d", len(encoded), MaxReadFileV1ResultBytes)
	}
	return result, nil
}

func emptyReadFileResult(plan ReadFileV1Plan, status string) ReadFileV1Result {
	resultPath := plan.RequestedPath
	if resultPath == "" {
		resultPath = plan.RelativePath
	}
	return ReadFileV1Result{
		Status: status, Path: resultPath, Offset: plan.Offset, RequestedBytes: plan.Limit,
		Encoding: "utf-8", Content: "",
	}
}

func digestHex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

type readFileAcknowledgementEvidence struct {
	Version        string `json:"version"`
	RequestID      string `json:"requestId"`
	ResponseKind   string `json:"responseKind"`
	ResponseSHA256 string `json:"responseSha256"`
	ResponseBytes  int    `json:"responseBytes"`
}

type readFileTerminalEvidence struct {
	Version        string `json:"version"`
	Kind           string `json:"kind"`
	Status         string `json:"status"`
	Acknowledged   bool   `json:"acknowledged"`
	ResponseKind   string `json:"responseKind,omitempty"`
	ResponseSHA256 string `json:"responseSha256,omitempty"`
	ResponseBytes  int    `json:"responseBytes,omitempty"`
	ContentSHA256  string `json:"contentSha256,omitempty"`
	BytesRead      uint64 `json:"bytesRead"`
	EOF            bool   `json:"eof"`
	FailureClass   string `json:"failureClass,omitempty"`
}
