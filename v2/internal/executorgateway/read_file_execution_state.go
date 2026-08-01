package executorgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
)

// readFileExecutionState serializes the five authoritative transitions for
// one read_file call. No retry path exists in this Phase 1 state machine:
// effect_class=read is classification, not permission to replay a request.
type readFileExecutionState struct {
	mu          sync.Mutex
	authority   ExecutionAuthority
	transitions *ExecutionTransitionAllocator
	principal   ExecutorMCPPrincipal
	plan        ReadFileV1Plan

	execution ExecutionState
	operation ExecutionOperationState
}

func newReadFileExecutionState(authority ExecutionAuthority, transitions *ExecutionTransitionAllocator, principal ExecutorMCPPrincipal, plan ReadFileV1Plan) (*readFileExecutionState, error) {
	if authority == nil || transitions == nil {
		return nil, errors.New("read-file core authority and transition allocator are required")
	}
	if err := validateExecutorMCPPrincipal(principal); err != nil {
		return nil, err
	}
	if plan.Read.OperationID == "" || plan.Read.Routing.ExecutionID == "" || plan.Read.Routing.OperationID != plan.Read.OperationID {
		return nil, errors.New("read-file plan has invalid execution or operation identities")
	}
	return &readFileExecutionState{
		authority: authority, transitions: transitions, principal: principal, plan: plan,
	}, nil
}

func (state *readFileExecutionState) Prepare(ctx context.Context) error {
	execution, err := state.PrepareExecution(ctx)
	if err != nil {
		return err
	}
	if execution.Status != "approved" {
		return fmt.Errorf("read-file execution policy status is %q, want approved", execution.Status)
	}
	return state.PrepareOperation(ctx)
}

func (state *readFileExecutionState) PrepareExecution(ctx context.Context) (ExecutionState, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	record, err := state.allocateRecordLocked("prepare execution")
	if err != nil {
		return ExecutionState{}, err
	}
	prepared, err := state.authority.PrepareExecution(ctx, PrepareExecutionRequest{
		ExecutionID:               state.plan.Read.Routing.ExecutionID,
		RunID:                     state.principal.Run.RunID,
		RunAttemptID:              state.principal.Run.RunAttemptID,
		HolderID:                  state.principal.Run.HolderID,
		RunAttemptGeneration:      state.principal.Run.RunAttemptGeneration,
		ExpectedRunVersion:        state.principal.Run.ExpectedRunVersion,
		ExpectedRunAttemptVersion: state.principal.Run.ExpectedRunAttemptVersion,
		AppServerToolCallID:       state.plan.ToolCallID,
		ExecutorID:                state.plan.Environment.ExecutorID,
		EnvironmentID:             state.plan.Environment.EnvironmentID,
		ToolName:                  mcpcontract.ToolReadFile,
		ToolVersion:               mcpcontract.Version,
		MapperVersion:             "read-file-v1",
		PolicyVersion:             state.plan.PolicyVersion,
		OperationCount:            1,
		Arguments:                 state.plan.Arguments,
		ToolSchema:                state.plan.ToolSchema,
		OperationPlan:             state.plan.OperationPlan,
		PolicyContext:             state.plan.PolicyContext,
		PolicyDecision:            state.plan.PolicyDecision,
		Record:                    record,
	})
	if err != nil {
		return ExecutionState{}, fmt.Errorf("prepare read-file execution: %w", err)
	}
	if err := state.acceptExecutionLocked(prepared.Execution); err != nil {
		return ExecutionState{}, fmt.Errorf("prepare read-file execution response: %w", err)
	}
	return state.execution, nil
}

func (state *readFileExecutionState) AcceptAuthorizedExecution(execution ExecutionState) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := state.acceptExecutionLocked(execution); err != nil {
		return fmt.Errorf("accept authorized read-file execution: %w", err)
	}
	if state.execution.Status != "approved" {
		return fmt.Errorf("authorized read-file execution status is %q, want approved", state.execution.Status)
	}
	return nil
}

func (state *readFileExecutionState) PrepareOperation(ctx context.Context) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.execution.Status != "approved" {
		return fmt.Errorf("read-file operation requires approved execution, got %q", state.execution.Status)
	}
	record, err := state.allocateRecordLocked("prepare fs_read")
	if err != nil {
		return err
	}
	operation, err := state.authority.PrepareOperation(ctx, PrepareOperationRequest{
		OperationID:              state.plan.Read.OperationID,
		ExecutionID:              state.execution.ExecutionID,
		RunID:                    state.principal.Run.RunID,
		RunAttemptID:             state.principal.Run.RunAttemptID,
		HolderID:                 state.principal.Run.HolderID,
		RunAttemptGeneration:     state.principal.Run.RunAttemptGeneration,
		ExpectedExecutionVersion: state.execution.Version,
		Ordinal:                  state.plan.Read.Ordinal,
		Kind:                     state.plan.Read.Kind,
		EffectClass:              state.plan.Read.EffectClass,
		MutationKey:              state.plan.Read.MutationKey,
		Params:                   state.plan.Read.Params,
		Record:                   record,
	})
	if err != nil {
		return fmt.Errorf("prepare read-file operation: %w", err)
	}
	if err := state.acceptExecutionLocked(operation.Execution); err != nil {
		return fmt.Errorf("prepare read-file operation execution: %w", err)
	}
	if err := state.acceptOperationLocked(operation.Operation); err != nil {
		return fmt.Errorf("prepare read-file operation response: %w", err)
	}
	if state.operation.Status != "prepared" {
		return fmt.Errorf("prepared read-file operation has status %q", state.operation.Status)
	}
	return nil
}

func (state *readFileExecutionState) Begin(ctx context.Context) (BeginOperationDispatchResult, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.operation.OperationID == "" {
		return BeginOperationDispatchResult{}, errors.New("read-file operation was not prepared")
	}
	record, err := state.allocateRecordLocked("begin fs_read")
	if err != nil {
		return BeginOperationDispatchResult{}, err
	}
	result, err := state.authority.BeginOperationDispatch(ctx, BeginOperationDispatchRequest{
		OperationID:              state.plan.Read.OperationID,
		ExecutionID:              state.execution.ExecutionID,
		RunID:                    state.principal.Run.RunID,
		RunAttemptID:             state.principal.Run.RunAttemptID,
		HolderID:                 state.principal.Run.HolderID,
		RunAttemptGeneration:     state.principal.Run.RunAttemptGeneration,
		ConnectionGeneration:     state.plan.Environment.ConnectionGeneration,
		ExpectedExecutionVersion: state.execution.Version,
		ExpectedOperationVersion: state.operation.Version,
		PolicyContext:            state.plan.PolicyContext,
		OperationPlan:            state.plan.OperationPlan,
		Params:                   state.plan.Read.Params,
		Record:                   record,
	})
	if err != nil {
		return result, fmt.Errorf("begin read-file operation: %w", err)
	}
	if err := state.acceptExecutionLocked(result.Execution); err != nil {
		return result, fmt.Errorf("begin read-file operation execution: %w", err)
	}
	if err := state.acceptOperationLocked(result.Operation); err != nil {
		return result, fmt.Errorf("begin read-file operation response: %w", err)
	}
	if result.Began && (state.operation.Status != "dispatching" || state.operation.ConnectionGeneration != state.plan.Environment.ConnectionGeneration) {
		return result, errors.New("core read-file dispatch permission has the wrong status or connection generation")
	}
	return result, nil
}

func (state *readFileExecutionState) Acknowledge(ctx context.Context, acknowledgement json.RawMessage) (AcknowledgeOperationResult, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.operation.OperationID == "" {
		return AcknowledgeOperationResult{}, errors.New("read-file operation was not prepared")
	}
	record, err := state.allocateRecordLocked("acknowledge fs_read")
	if err != nil {
		return AcknowledgeOperationResult{}, err
	}
	result, err := state.authority.AcknowledgeOperation(ctx, AcknowledgeOperationRequest{
		OperationID:              state.plan.Read.OperationID,
		ExecutionID:              state.execution.ExecutionID,
		RunID:                    state.principal.Run.RunID,
		RunAttemptID:             state.principal.Run.RunAttemptID,
		RunAttemptGeneration:     state.principal.Run.RunAttemptGeneration,
		ConnectionGeneration:     state.plan.Environment.ConnectionGeneration,
		ExpectedExecutionVersion: state.execution.Version,
		ExpectedOperationVersion: state.operation.Version,
		Acknowledgement:          acknowledgement,
		Record:                   record,
	})
	if err != nil {
		return result, fmt.Errorf("acknowledge read-file operation: %w", err)
	}
	if err := state.acceptExecutionLocked(result.Execution); err != nil {
		return result, fmt.Errorf("acknowledge read-file operation execution: %w", err)
	}
	if err := state.acceptOperationLocked(result.Operation); err != nil {
		return result, fmt.Errorf("acknowledge read-file operation response: %w", err)
	}
	if state.operation.Status != "acknowledged" && !terminalOperationStatus(state.operation.Status) {
		return result, fmt.Errorf("acknowledged read-file operation has status %q", state.operation.Status)
	}
	return result, nil
}

func (state *readFileExecutionState) CompleteOperation(ctx context.Context, terminalStatus string, resultJSON json.RawMessage) (CompleteOperationResult, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.operation.OperationID == "" {
		return CompleteOperationResult{}, errors.New("read-file operation was not prepared")
	}
	record, err := state.allocateRecordLocked("complete fs_read")
	if err != nil {
		return CompleteOperationResult{}, err
	}
	result, err := state.authority.CompleteOperation(ctx, CompleteOperationRequest{
		OperationID:              state.plan.Read.OperationID,
		ExecutionID:              state.execution.ExecutionID,
		RunID:                    state.principal.Run.RunID,
		RunAttemptID:             state.principal.Run.RunAttemptID,
		RunAttemptGeneration:     state.principal.Run.RunAttemptGeneration,
		ConnectionGeneration:     state.plan.Environment.ConnectionGeneration,
		ExpectedExecutionVersion: state.execution.Version,
		ExpectedOperationVersion: state.operation.Version,
		TerminalStatus:           terminalStatus,
		Result:                   resultJSON,
		Record:                   record,
	})
	if err != nil {
		return result, fmt.Errorf("complete read-file operation: %w", err)
	}
	if err := state.acceptExecutionLocked(result.Execution); err != nil {
		return result, fmt.Errorf("complete read-file operation execution: %w", err)
	}
	if err := state.acceptOperationLocked(result.Operation); err != nil {
		return result, fmt.Errorf("complete read-file operation response: %w", err)
	}
	if state.operation.Status != terminalStatus {
		return result, fmt.Errorf("completed read-file operation has status %q, want %q", state.operation.Status, terminalStatus)
	}
	return result, nil
}

func (state *readFileExecutionState) CompleteExecution(ctx context.Context, terminalStatus string, resultJSON json.RawMessage) (CompleteExecutionResult, error) {
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
		return result, fmt.Errorf("complete read-file execution: %w", err)
	}
	if err := state.acceptExecutionLocked(result.Execution); err != nil {
		return result, fmt.Errorf("complete read-file execution response: %w", err)
	}
	if state.execution.Status != terminalStatus {
		return result, fmt.Errorf("completed read-file execution has status %q, want %q", state.execution.Status, terminalStatus)
	}
	return result, nil
}

func (state *readFileExecutionState) OperationStatus() string {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.operation.Status
}

func (state *readFileExecutionState) allocateRecordLocked(action string) (ExecutionTransitionRecord, error) {
	record, err := state.transitions.Allocate()
	if err != nil {
		return ExecutionTransitionRecord{}, fmt.Errorf("allocate transition for %s: %w", action, err)
	}
	return record, nil
}

func (state *readFileExecutionState) acceptExecutionLocked(execution ExecutionState) error {
	if execution.ExecutionID != state.plan.Read.Routing.ExecutionID || execution.RunID != state.principal.Run.RunID ||
		execution.RunAttemptID != state.principal.Run.RunAttemptID || execution.RunAttemptGeneration != state.principal.Run.RunAttemptGeneration ||
		execution.AppServerToolCallID != state.plan.ToolCallID || execution.ExecutorID != state.plan.Environment.ExecutorID ||
		execution.EnvironmentID != state.plan.Environment.EnvironmentID || execution.ToolName != mcpcontract.ToolReadFile ||
		execution.ToolVersion != mcpcontract.Version || execution.MapperVersion != "read-file-v1" ||
		execution.PolicyVersion != state.plan.PolicyVersion || execution.PolicyDecision != state.plan.PolicyDecision || execution.OperationCount != 1 {
		return errors.New("core execution identity or frozen read-file contract differs from the request")
	}
	state.execution = execution
	return nil
}

func (state *readFileExecutionState) acceptOperationLocked(operation ExecutionOperationState) error {
	expected := state.plan.Read
	if operation.OperationID != expected.OperationID || operation.ExecutionID != state.plan.Read.Routing.ExecutionID ||
		operation.Ordinal != expected.Ordinal || operation.Kind != expected.Kind || operation.EffectClass != expected.EffectClass ||
		operation.MutationKey != expected.MutationKey {
		return errors.New("core operation identity or frozen read-file contract differs from the request")
	}
	state.operation = operation
	return nil
}
