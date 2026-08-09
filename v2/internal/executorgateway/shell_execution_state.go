package executorgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
)

// shellExecutionState serializes every core transition for one shell call and
// advances expected execution/operation versions only from authenticated core
// responses. It also implements the timeout coordinator's dispatch authority,
// placing terminal Skip and deadline Begin on the same local serialization
// boundary before core performs the authoritative CAS.
type shellExecutionState struct {
	mu          sync.Mutex
	authority   ExecutionAuthority
	transitions *ExecutionTransitionAllocator
	principal   ExecutorMCPPrincipal
	plan        ShellV1Plan

	execution  ExecutionState
	operations map[string]ExecutionOperationState
}

func newShellExecutionState(authority ExecutionAuthority, transitions *ExecutionTransitionAllocator, principal ExecutorMCPPrincipal, plan ShellV1Plan) (*shellExecutionState, error) {
	if authority == nil || transitions == nil {
		return nil, errors.New("shell core authority and transition allocator are required")
	}
	if err := validateExecutorMCPPrincipal(principal); err != nil {
		return nil, err
	}
	if plan.Start.OperationID == "" || plan.Timeout.OperationID == "" || plan.Start.OperationID == plan.Timeout.OperationID {
		return nil, errors.New("shell plan has invalid operation identities")
	}
	if _, err := executionTarget(plan.Environment); err != nil {
		return nil, err
	}
	return &shellExecutionState{
		authority: authority, transitions: transitions, principal: principal, plan: plan,
		operations: make(map[string]ExecutionOperationState, 2),
	}, nil
}

func (state *shellExecutionState) Prepare(ctx context.Context) error {
	execution, err := state.PrepareExecution(ctx)
	if err != nil {
		return err
	}
	if execution.Status != "approved" {
		return fmt.Errorf("shell execution policy status is %q, want approved", execution.Status)
	}
	return state.PrepareOperations(ctx)
}

func (state *shellExecutionState) PrepareExecution(ctx context.Context) (ExecutionState, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	record, err := state.allocateRecordLocked("prepare execution")
	if err != nil {
		return ExecutionState{}, err
	}
	prepared, err := state.authority.PrepareExecution(ctx, PrepareExecutionRequest{
		ExecutionID:               state.plan.Start.Routing.ExecutionID,
		RunID:                     state.principal.Run.RunID,
		RunAttemptID:              state.principal.Run.RunAttemptID,
		HolderID:                  state.principal.Run.HolderID,
		RunAttemptGeneration:      state.principal.Run.RunAttemptGeneration,
		ExpectedRunVersion:        state.principal.Run.ExpectedRunVersion,
		ExpectedRunAttemptVersion: state.principal.Run.ExpectedRunAttemptVersion,
		AppServerToolCallID:       state.plan.ToolCallID,
		ExecutorID:                coreExecutorID(state.plan.Environment),
		EnvironmentID:             state.plan.Environment.EnvironmentID,
		Target:                    state.plan.Environment.Target,
		ToolName:                  mcpcontract.ToolShell,
		ToolVersion:               mcpcontract.Version,
		MapperVersion:             "shell-v1",
		PolicyVersion:             state.plan.PolicyVersion,
		OperationCount:            2,
		Arguments:                 state.plan.Arguments,
		ToolSchema:                state.plan.ToolSchema,
		OperationPlan:             state.plan.OperationPlan,
		PolicyContext:             state.plan.PolicyContext,
		PolicyDecision:            state.plan.PolicyDecision,
		Record:                    record,
	})
	if err != nil {
		return ExecutionState{}, fmt.Errorf("prepare shell execution: %w", err)
	}
	if err := state.acceptExecutionLocked(prepared.Execution); err != nil {
		return ExecutionState{}, fmt.Errorf("prepare shell execution response: %w", err)
	}
	return state.execution, nil
}

func (state *shellExecutionState) AcceptAuthorizedExecution(execution ExecutionState) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := state.acceptExecutionLocked(execution); err != nil {
		return fmt.Errorf("accept authorized shell execution: %w", err)
	}
	if state.execution.Status != "approved" {
		return fmt.Errorf("authorized shell execution status is %q, want approved", state.execution.Status)
	}
	return nil
}

func (state *shellExecutionState) PrepareOperations(ctx context.Context) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.execution.Status != "approved" {
		return fmt.Errorf("shell operations require approved execution, got %q", state.execution.Status)
	}
	for _, operation := range []ShellV1Operation{state.plan.Start, state.plan.Timeout} {
		if err := state.prepareOperationLocked(ctx, operation); err != nil {
			return err
		}
	}
	return nil
}

func (state *shellExecutionState) prepareOperationLocked(ctx context.Context, operation ShellV1Operation) error {
	record, err := state.allocateRecordLocked("prepare " + operation.Kind)
	if err != nil {
		return err
	}
	prepared, err := state.authority.PrepareOperation(ctx, PrepareOperationRequest{
		OperationID:              operation.OperationID,
		ExecutionID:              state.execution.ExecutionID,
		RunID:                    state.principal.Run.RunID,
		RunAttemptID:             state.principal.Run.RunAttemptID,
		HolderID:                 state.principal.Run.HolderID,
		RunAttemptGeneration:     state.principal.Run.RunAttemptGeneration,
		ExpectedExecutionVersion: state.execution.Version,
		Ordinal:                  operation.Ordinal,
		Kind:                     operation.Kind,
		EffectClass:              operation.EffectClass,
		MutationKey:              operation.MutationKey,
		Params:                   operation.Params,
		Record:                   record,
	})
	if err != nil {
		return fmt.Errorf("prepare shell operation %s: %w", operation.Kind, err)
	}
	if err := state.acceptExecutionLocked(prepared.Execution); err != nil {
		return fmt.Errorf("prepare shell operation %s execution: %w", operation.Kind, err)
	}
	if err := state.acceptOperationLocked(operation, prepared.Operation); err != nil {
		return fmt.Errorf("prepare shell operation %s response: %w", operation.Kind, err)
	}
	if prepared.Operation.Status != "prepared" {
		return fmt.Errorf("prepared shell operation %s has status %q", operation.Kind, prepared.Operation.Status)
	}
	return nil
}

func (state *shellExecutionState) BeginStart(ctx context.Context) (BeginOperationDispatchResult, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.beginOperationLocked(ctx, state.plan.Start)
}

// BeginOperationDispatch implements OperationDispatchAuthority for only the
// frozen timeout operation. Expected versions and the transition record are
// assigned under the same lock as ACK, terminal, and Skip transitions.
func (state *shellExecutionState) BeginOperationDispatch(ctx context.Context, request BeginOperationDispatchRequest) (BeginOperationDispatchResult, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	operation := state.plan.Timeout
	target := state.plan.Environment.Target
	requestTarget := request.Target
	if requestTarget.Kind == "" && target.Kind == "agentx" {
		requestTarget = target
	}
	if request.OperationID != operation.OperationID || request.ExecutionID != state.plan.Start.Routing.ExecutionID ||
		request.RunID != state.principal.Run.RunID || request.RunAttemptID != state.principal.Run.RunAttemptID ||
		request.HolderID != state.principal.Run.HolderID || request.RunAttemptGeneration != state.principal.Run.RunAttemptGeneration ||
		request.ConnectionGeneration != targetConnectionGeneration(target) || requestTarget != target ||
		!bytes.Equal(request.PolicyContext, state.plan.PolicyContext) || !bytes.Equal(request.OperationPlan, state.plan.OperationPlan) ||
		!bytes.Equal(request.Params, operation.Params) {
		return BeginOperationDispatchResult{}, errors.New("timeout dispatch request differs from the frozen shell plan")
	}
	return state.beginOperationLocked(ctx, operation)
}

func (state *shellExecutionState) beginOperationLocked(ctx context.Context, operation ShellV1Operation) (BeginOperationDispatchResult, error) {
	current, ok := state.operations[operation.OperationID]
	if !ok {
		return BeginOperationDispatchResult{}, errors.New("shell operation was not prepared")
	}
	record, err := state.allocateRecordLocked("begin " + operation.Kind)
	if err != nil {
		return BeginOperationDispatchResult{}, err
	}
	result, err := state.authority.BeginOperationDispatch(ctx, BeginOperationDispatchRequest{
		OperationID:              operation.OperationID,
		ExecutionID:              state.execution.ExecutionID,
		RunID:                    state.principal.Run.RunID,
		RunAttemptID:             state.principal.Run.RunAttemptID,
		HolderID:                 state.principal.Run.HolderID,
		RunAttemptGeneration:     state.principal.Run.RunAttemptGeneration,
		ConnectionGeneration:     targetConnectionGeneration(state.plan.Environment.Target),
		Target:                   state.plan.Environment.Target,
		ExpectedExecutionVersion: state.execution.Version,
		ExpectedOperationVersion: current.Version,
		PolicyContext:            state.plan.PolicyContext,
		OperationPlan:            state.plan.OperationPlan,
		Params:                   operation.Params,
		Record:                   record,
	})
	if err != nil {
		return result, fmt.Errorf("begin shell operation %s: %w", operation.Kind, err)
	}
	if err := state.acceptExecutionLocked(result.Execution); err != nil {
		return result, fmt.Errorf("begin shell operation %s execution: %w", operation.Kind, err)
	}
	if err := state.acceptOperationLocked(operation, result.Operation); err != nil {
		return result, fmt.Errorf("begin shell operation %s response: %w", operation.Kind, err)
	}
	if result.Began && (result.Operation.Status != "dispatching" || result.Operation.Target != state.plan.Environment.Target ||
		result.Operation.ConnectionGeneration != targetConnectionGeneration(state.plan.Environment.Target)) {
		return result, errors.New("core shell dispatch permission has the wrong status or target generation")
	}
	return result, nil
}

func (state *shellExecutionState) Acknowledge(ctx context.Context, operation ShellV1Operation, acknowledgement json.RawMessage) (AcknowledgeOperationResult, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	current, ok := state.operations[operation.OperationID]
	if !ok {
		return AcknowledgeOperationResult{}, errors.New("shell operation was not prepared")
	}
	record, err := state.allocateRecordLocked("acknowledge " + operation.Kind)
	if err != nil {
		return AcknowledgeOperationResult{}, err
	}
	result, err := state.authority.AcknowledgeOperation(ctx, AcknowledgeOperationRequest{
		OperationID:              operation.OperationID,
		ExecutionID:              state.execution.ExecutionID,
		RunID:                    state.principal.Run.RunID,
		RunAttemptID:             state.principal.Run.RunAttemptID,
		RunAttemptGeneration:     state.principal.Run.RunAttemptGeneration,
		ConnectionGeneration:     targetConnectionGeneration(state.plan.Environment.Target),
		Target:                   state.plan.Environment.Target,
		ExpectedExecutionVersion: state.execution.Version,
		ExpectedOperationVersion: current.Version,
		Acknowledgement:          acknowledgement,
		Record:                   record,
	})
	if err != nil {
		return result, fmt.Errorf("acknowledge shell operation %s: %w", operation.Kind, err)
	}
	if err := state.acceptExecutionLocked(result.Execution); err != nil {
		return result, fmt.Errorf("acknowledge shell operation %s execution: %w", operation.Kind, err)
	}
	if err := state.acceptOperationLocked(operation, result.Operation); err != nil {
		return result, fmt.Errorf("acknowledge shell operation %s response: %w", operation.Kind, err)
	}
	if result.Operation.Status != "acknowledged" && !terminalOperationStatus(result.Operation.Status) {
		return result, fmt.Errorf("acknowledged shell operation %s has status %q", operation.Kind, result.Operation.Status)
	}
	return result, nil
}

func (state *shellExecutionState) CompleteOperation(ctx context.Context, operation ShellV1Operation, terminalStatus string, resultJSON json.RawMessage) (CompleteOperationResult, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	current, ok := state.operations[operation.OperationID]
	if !ok {
		return CompleteOperationResult{}, errors.New("shell operation was not prepared")
	}
	record, err := state.allocateRecordLocked("complete " + operation.Kind)
	if err != nil {
		return CompleteOperationResult{}, err
	}
	result, err := state.authority.CompleteOperation(ctx, CompleteOperationRequest{
		OperationID:              operation.OperationID,
		ExecutionID:              state.execution.ExecutionID,
		RunID:                    state.principal.Run.RunID,
		RunAttemptID:             state.principal.Run.RunAttemptID,
		RunAttemptGeneration:     state.principal.Run.RunAttemptGeneration,
		ConnectionGeneration:     targetConnectionGeneration(state.plan.Environment.Target),
		Target:                   state.plan.Environment.Target,
		ExpectedExecutionVersion: state.execution.Version,
		ExpectedOperationVersion: current.Version,
		TerminalStatus:           terminalStatus,
		Result:                   resultJSON,
		Record:                   record,
	})
	if err != nil {
		return result, fmt.Errorf("complete shell operation %s: %w", operation.Kind, err)
	}
	if err := state.acceptExecutionLocked(result.Execution); err != nil {
		return result, fmt.Errorf("complete shell operation %s execution: %w", operation.Kind, err)
	}
	if err := state.acceptOperationLocked(operation, result.Operation); err != nil {
		return result, fmt.Errorf("complete shell operation %s response: %w", operation.Kind, err)
	}
	if result.Operation.Status != terminalStatus {
		return result, fmt.Errorf("completed shell operation %s has status %q, want %q", operation.Kind, result.Operation.Status, terminalStatus)
	}
	return result, nil
}

func (state *shellExecutionState) SkipTimeoutIfPrepared(ctx context.Context, reason string) (bool, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	operation := state.plan.Timeout
	current, ok := state.operations[operation.OperationID]
	if !ok {
		return false, errors.New("shell timeout operation was not prepared")
	}
	if current.Status == "skipped" {
		return true, nil
	}
	if current.Status != "prepared" {
		return false, nil
	}
	resultJSON, err := json.Marshal(struct {
		Reason    string `json:"reason"`
		ProcessID string `json:"processId"`
	}{Reason: reason, ProcessID: state.plan.ProcessID})
	if err != nil {
		return false, err
	}
	record, err := state.allocateRecordLocked("skip timeout_terminate")
	if err != nil {
		return false, err
	}
	result, err := state.authority.SkipOperation(ctx, SkipOperationRequest{
		OperationID:              operation.OperationID,
		ExecutionID:              state.execution.ExecutionID,
		RunID:                    state.principal.Run.RunID,
		RunAttemptID:             state.principal.Run.RunAttemptID,
		HolderID:                 state.principal.Run.HolderID,
		RunAttemptGeneration:     state.principal.Run.RunAttemptGeneration,
		ExpectedExecutionVersion: state.execution.Version,
		ExpectedOperationVersion: current.Version,
		Result:                   resultJSON,
		Record:                   record,
	})
	if err != nil {
		return false, fmt.Errorf("skip shell timeout operation: %w", err)
	}
	if err := state.acceptExecutionLocked(result.Execution); err != nil {
		return false, fmt.Errorf("skip shell timeout execution: %w", err)
	}
	if err := state.acceptOperationLocked(operation, result.Operation); err != nil {
		return false, fmt.Errorf("skip shell timeout response: %w", err)
	}
	if result.Operation.Status != "skipped" {
		return false, fmt.Errorf("skipped shell timeout has status %q", result.Operation.Status)
	}
	return true, nil
}

func (state *shellExecutionState) CompleteExecution(ctx context.Context, terminalStatus string, resultJSON json.RawMessage) (CompleteExecutionResult, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	record, err := state.allocateRecordLocked("complete execution")
	if err != nil {
		return CompleteExecutionResult{}, err
	}
	result, err := state.authority.CompleteExecution(ctx, CompleteExecutionRequest{
		ExecutionID:              state.execution.ExecutionID,
		RunID:                    state.principal.Run.RunID,
		RunAttemptID:             state.principal.Run.RunAttemptID,
		RunAttemptGeneration:     state.principal.Run.RunAttemptGeneration,
		ExpectedExecutionVersion: state.execution.Version,
		TerminalStatus:           terminalStatus,
		Result:                   resultJSON,
		Record:                   record,
	})
	if err != nil {
		return result, fmt.Errorf("complete shell execution: %w", err)
	}
	if err := state.acceptExecutionLocked(result.Execution); err != nil {
		return result, fmt.Errorf("complete shell execution response: %w", err)
	}
	if result.Execution.Status != terminalStatus {
		return result, fmt.Errorf("completed shell execution has status %q, want %q", result.Execution.Status, terminalStatus)
	}
	return result, nil
}

func (state *shellExecutionState) OperationStatus(operationID string) string {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.operations[operationID].Status
}

func (state *shellExecutionState) allocateRecordLocked(action string) (ExecutionTransitionRecord, error) {
	record, err := state.transitions.Allocate()
	if err != nil {
		return ExecutionTransitionRecord{}, fmt.Errorf("allocate transition for %s: %w", action, err)
	}
	return record, nil
}

func (state *shellExecutionState) acceptExecutionLocked(execution ExecutionState) error {
	if execution.ExecutionID != state.plan.Start.Routing.ExecutionID || execution.RunID != state.principal.Run.RunID ||
		execution.RunAttemptID != state.principal.Run.RunAttemptID || execution.RunAttemptGeneration != state.principal.Run.RunAttemptGeneration ||
		execution.AppServerToolCallID != state.plan.ToolCallID || execution.ExecutorID != coreExecutorID(state.plan.Environment) ||
		execution.EnvironmentID != state.plan.Environment.EnvironmentID || execution.ToolName != mcpcontract.ToolShell ||
		execution.ToolVersion != mcpcontract.Version || execution.MapperVersion != "shell-v1" ||
		execution.PolicyVersion != state.plan.PolicyVersion || execution.PolicyDecision != state.plan.PolicyDecision || execution.OperationCount != 2 ||
		execution.Target != state.plan.Environment.Target {
		return errors.New("core execution identity or frozen shell contract differs from the request")
	}
	state.execution = execution
	return nil
}

func (state *shellExecutionState) acceptOperationLocked(expected ShellV1Operation, operation ExecutionOperationState) error {
	if operation.OperationID != expected.OperationID || operation.ExecutionID != state.plan.Start.Routing.ExecutionID ||
		operation.Ordinal != expected.Ordinal || operation.Kind != expected.Kind || operation.EffectClass != expected.EffectClass ||
		operation.MutationKey != expected.MutationKey || operation.Target != state.plan.Environment.Target {
		return errors.New("core operation identity or frozen shell contract differs from the request")
	}
	state.operations[operation.OperationID] = operation
	return nil
}

func terminalOperationStatus(status string) bool {
	switch status {
	case "succeeded", "failed", "cancelled", "unknown", "skipped":
		return true
	default:
		return false
	}
}

var _ OperationDispatchAuthority = (*shellExecutionState)(nil)
