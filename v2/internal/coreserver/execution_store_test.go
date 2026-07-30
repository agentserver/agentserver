package coreserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

func TestStateStoreExecutionCommandsValidatesAndHashesWireJSON(t *testing.T) {
	store := &recordingExecutionStateStore{}
	commands := StateStoreExecutionCommands{Store: store}
	request := testPrepareExecutionContractRequest()
	result, err := commands.PrepareExecution(t.Context(), request)
	if err != nil {
		t.Fatalf("PrepareExecution() error = %v", err)
	}
	if !result.Created || result.Execution.ArgumentsDigest.Domain != string(coredb.HashDomainExecutionArguments) {
		t.Fatalf("PrepareExecution() result = %+v", result)
	}
	if store.prepareExecutionCalls != 1 {
		t.Fatalf("store PrepareExecution calls = %d", store.prepareExecutionCalls)
	}
	if store.execution.ArgumentsHash.Domain() != coredb.HashDomainExecutionArguments ||
		store.execution.ToolSchemaHash.Domain() != coredb.HashDomainToolSchema ||
		store.execution.OperationPlanHash.Domain() != coredb.HashDomainOperationPlan ||
		store.execution.PolicyContextHash.Domain() != coredb.HashDomainPolicyContext {
		t.Fatalf("prepared hash domains = %q/%q/%q/%q",
			store.execution.ArgumentsHash.Domain(), store.execution.ToolSchemaHash.Domain(),
			store.execution.OperationPlanHash.Domain(), store.execution.PolicyContextHash.Domain())
	}

	operationRequest := corecontract.PrepareOperationRequest{
		OperationID: testOperationID, ExecutionID: testExecutionID, RunID: request.RunID, RunAttemptID: request.RunAttemptID,
		HolderID: request.HolderID, RunAttemptGeneration: request.RunAttemptGeneration, ExpectedExecutionVersion: result.Execution.Version,
		Ordinal: 1, Kind: "process_start", EffectClass: "mutation", MutationKey: "61000000-0000-4000-8000-000000000006",
		Params: json.RawMessage(`{"argv":["pwd"]}`), Record: request.Record,
	}
	preparedOperation, err := commands.PrepareOperation(t.Context(), operationRequest)
	if err != nil {
		t.Fatalf("PrepareOperation() error = %v", err)
	}
	if preparedOperation.Operation.ParamsDigest.Domain != string(coredb.HashDomainOperationParams) {
		t.Fatalf("prepared operation = %+v", preparedOperation.Operation)
	}

	beginRequest := corecontract.BeginOperationDispatchRequest{
		OperationID: testOperationID, ExecutionID: testExecutionID, RunID: request.RunID, RunAttemptID: request.RunAttemptID,
		HolderID: request.HolderID, RunAttemptGeneration: request.RunAttemptGeneration, ConnectionGeneration: 7,
		ExpectedExecutionVersion: preparedOperation.Execution.Version, ExpectedOperationVersion: preparedOperation.Operation.Version,
		PolicyContext: request.PolicyContext, OperationPlan: request.OperationPlan, Params: operationRequest.Params, Record: request.Record,
	}
	dispatch, err := commands.BeginOperationDispatch(t.Context(), beginRequest)
	if err != nil || !dispatch.Began || dispatch.Operation.ConnectionGeneration != 7 {
		t.Fatalf("BeginOperationDispatch() = %+v, %v", dispatch, err)
	}

	acknowledgement := corecontract.AcknowledgeOperationRequest{
		OperationID: testOperationID, ExecutionID: testExecutionID, RunID: request.RunID, RunAttemptID: request.RunAttemptID,
		RunAttemptGeneration: request.RunAttemptGeneration, ConnectionGeneration: 7,
		ExpectedExecutionVersion: dispatch.Execution.Version, ExpectedOperationVersion: dispatch.Operation.Version,
		Acknowledgement: json.RawMessage(`{"accepted":true,"mutationKey":"61000000-0000-4000-8000-000000000006"}`), Record: request.Record,
	}
	acknowledged, err := commands.AcknowledgeOperation(t.Context(), acknowledgement)
	if err != nil || !acknowledged.Changed || acknowledged.Operation.AcknowledgementDigest == nil || acknowledged.Operation.AcknowledgementDigest.Domain != string(coredb.HashDomainOperationAck) {
		t.Fatalf("AcknowledgeOperation() = %+v, %v", acknowledged, err)
	}

	completeOperation := corecontract.CompleteOperationRequest{
		OperationID: testOperationID, ExecutionID: testExecutionID, RunID: request.RunID, RunAttemptID: request.RunAttemptID,
		RunAttemptGeneration: request.RunAttemptGeneration, ConnectionGeneration: 7,
		ExpectedExecutionVersion: acknowledged.Execution.Version, ExpectedOperationVersion: acknowledged.Operation.Version,
		TerminalStatus: "succeeded", Result: json.RawMessage(`{"exitCode":0}`), Record: request.Record,
	}
	completedOperation, err := commands.CompleteOperation(t.Context(), completeOperation)
	if err != nil || !completedOperation.Changed || completedOperation.Operation.TerminalResultDigest == nil || completedOperation.Operation.TerminalResultDigest.Domain != string(coredb.HashDomainOperationResult) {
		t.Fatalf("CompleteOperation() = %+v, %v", completedOperation, err)
	}

	completeExecution := corecontract.CompleteExecutionRequest{
		ExecutionID: testExecutionID, RunID: request.RunID, RunAttemptID: request.RunAttemptID,
		RunAttemptGeneration: request.RunAttemptGeneration, ExpectedExecutionVersion: completedOperation.Execution.Version,
		TerminalStatus: "succeeded", Result: json.RawMessage(`{"status":"succeeded"}`), Record: request.Record,
	}
	completedExecution, err := commands.CompleteExecution(t.Context(), completeExecution)
	if err != nil || !completedExecution.Changed || completedExecution.Execution.TerminalResultDigest == nil || completedExecution.Execution.TerminalResultDigest.Domain != string(coredb.HashDomainExecutionResult) {
		t.Fatalf("CompleteExecution() = %+v, %v", completedExecution, err)
	}
}

func TestStateStoreExecutionCommandsRejectsInvalidCanonicalEvidenceBeforeStore(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*corecontract.PrepareExecutionRequest)
	}{
		{name: "arguments violate frozen schema", mutate: func(request *corecontract.PrepareExecutionRequest) {
			request.Arguments = json.RawMessage(`{"argv":"pwd"}`)
		}},
		{name: "unknown schema keyword", mutate: func(request *corecontract.PrepareExecutionRequest) {
			request.ToolSchema = json.RawMessage(`{"type":"object","x-untrusted":true}`)
		}},
		{name: "duplicate policy key", mutate: func(request *corecontract.PrepareExecutionRequest) {
			request.PolicyContext = json.RawMessage(`{"decision":"allow","decision":"deny"}`)
		}},
		{name: "operation plan is not object", mutate: func(request *corecontract.PrepareExecutionRequest) {
			request.OperationPlan = json.RawMessage(`[]`)
		}},
		{name: "remote schema ref", mutate: func(request *corecontract.PrepareExecutionRequest) {
			request.ToolSchema = json.RawMessage(`{"type":"object","properties":{"x":{"$ref":"https://example.invalid/schema"}}}`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &recordingExecutionStateStore{}
			request := testPrepareExecutionContractRequest()
			test.mutate(&request)
			_, err := (StateStoreExecutionCommands{Store: store}).PrepareExecution(t.Context(), request)
			if !coredb.HasStateErrorCode(err, coredb.ErrorInvalidArgument) {
				t.Fatalf("PrepareExecution() error = %v, want invalid_argument", err)
			}
			if store.prepareExecutionCalls != 0 {
				t.Fatalf("invalid evidence reached store %d times", store.prepareExecutionCalls)
			}
		})
	}
}

func testPrepareExecutionContractRequest() corecontract.PrepareExecutionRequest {
	return corecontract.PrepareExecutionRequest{
		ExecutionID: testExecutionID,
		RunID:       "41000000-0000-4000-8000-000000000004", RunAttemptID: "42000000-0000-4000-8000-000000000004",
		HolderID: "holder", RunAttemptGeneration: 3, ExpectedRunVersion: 1, ExpectedRunAttemptVersion: 1,
		AppServerToolCallID: "call-1", ExecutorID: "20000000-0000-4000-8000-000000000002", EnvironmentID: "60000000-0000-4000-8000-000000000006",
		ToolName: "shell", ToolVersion: "executor-mcp/1.0", MapperVersion: "shell-v1", PolicyVersion: "policy-v1", OperationCount: 1,
		Arguments: json.RawMessage(` { "argv" : ["pwd"] } `),
		ToolSchema: json.RawMessage(`{
            "type":"object",
            "additionalProperties":false,
            "required":["argv"],
            "properties":{"argv":{"type":"array","minItems":1,"items":{"type":"string"}}}
        }`),
		OperationPlan:  json.RawMessage(`{"operations":[{"kind":"process_start","ordinal":1}]}`),
		PolicyContext:  json.RawMessage(`{"decision":"allow","policyVersion":"policy-v1"}`),
		PolicyDecision: "allow",
		Record: corecontract.TransitionRecord{
			EventID: "71000000-0000-4000-8000-000000000001", ProducerInstanceID: "72000000-0000-4000-8000-000000000001",
			ProducerSeq: 1, OutboxID: "73000000-0000-4000-8000-000000000001",
		},
	}
}

type recordingExecutionStateStore struct {
	prepareExecutionCalls int
	execution             coredb.Execution
	operation             coredb.ExecutionOperation
}

func (store *recordingExecutionStateStore) PrepareExecution(_ context.Context, command coredb.PrepareExecutionCommand) (coredb.PrepareExecutionResult, error) {
	store.prepareExecutionCalls++
	now := time.Now().UTC()
	store.execution = coredb.Execution{
		ID: command.ExecutionID, RunID: command.RunID, RunAttemptID: command.AttemptID, RunAttemptGeneration: command.Generation,
		AppServerToolCallID: command.AppServerToolCallID, ExecutorID: command.ExecutorID, EnvID: command.EnvID,
		ToolName: command.ToolName, ToolVersion: command.ToolVersion, MapperVersion: command.MapperVersion,
		PolicyVersion: command.PolicyVersion, PolicyDecision: command.PolicyDecision, OperationCount: command.OperationCount,
		ArgumentsHash: command.ArgumentsHash, ToolSchemaHash: command.ToolSchemaHash, OperationPlanHash: command.OperationPlanHash,
		PolicyContextHash: command.PolicyContextHash, Status: "approved", Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	return coredb.PrepareExecutionResult{Execution: store.execution, Created: true}, nil
}

func (store *recordingExecutionStateStore) PrepareOperation(_ context.Context, command coredb.PrepareOperationCommand) (coredb.PrepareOperationResult, error) {
	store.execution.Version++
	store.execution.UpdatedAt = time.Now().UTC()
	store.operation = coredb.ExecutionOperation{
		ID: command.OperationID, ExecutionID: command.ExecutionID, Ordinal: command.Ordinal, Kind: command.Kind,
		EffectClass: command.EffectClass, MutationKey: command.MutationKey, ParamsHash: command.ParamsHash,
		Status: "prepared", Version: 1, CreatedAt: store.execution.UpdatedAt, UpdatedAt: store.execution.UpdatedAt,
	}
	return coredb.PrepareOperationResult{Execution: store.execution, Operation: store.operation, Created: true}, nil
}

func (store *recordingExecutionStateStore) BeginOperationDispatch(_ context.Context, command coredb.BeginOperationDispatchCommand) (coredb.BeginOperationDispatchResult, error) {
	store.execution.Version++
	store.execution.Status = "dispatching"
	store.operation.Version++
	store.operation.Status = "dispatching"
	store.operation.ConnectionGeneration = command.ConnectionGeneration
	now := time.Now().UTC()
	store.execution.DispatchedAt = &now
	store.operation.DispatchedAt = &now
	return coredb.BeginOperationDispatchResult{Execution: store.execution, Operation: store.operation, Began: true}, nil
}

func (store *recordingExecutionStateStore) AcknowledgeOperation(_ context.Context, command coredb.AcknowledgeOperationCommand) (coredb.AcknowledgeOperationResult, error) {
	store.execution.Version++
	store.execution.Status = "running"
	store.operation.Version++
	store.operation.Status = "acknowledged"
	store.operation.AcknowledgementHash = &command.AcknowledgementHash
	now := time.Now().UTC()
	store.operation.AcknowledgedAt = &now
	return coredb.AcknowledgeOperationResult{Execution: store.execution, Operation: store.operation, Changed: true}, nil
}

func (store *recordingExecutionStateStore) CompleteOperation(_ context.Context, command coredb.CompleteOperationCommand) (coredb.CompleteOperationResult, error) {
	store.execution.Version++
	store.operation.Version++
	store.operation.Status = command.TerminalStatus
	store.operation.TerminalResultHash = &command.ResultHash
	now := time.Now().UTC()
	store.operation.TerminalAt = &now
	return coredb.CompleteOperationResult{Execution: store.execution, Operation: store.operation, Changed: true}, nil
}

func (store *recordingExecutionStateStore) CompleteExecution(_ context.Context, command coredb.CompleteExecutionCommand) (coredb.CompleteExecutionResult, error) {
	store.execution.Version++
	store.execution.Status = command.TerminalStatus
	store.execution.TerminalResultHash = &command.ResultHash
	now := time.Now().UTC()
	store.execution.TerminalAt = &now
	return coredb.CompleteExecutionResult{Execution: store.execution, Changed: true}, nil
}

var _ ExecutionStateStore = (*recordingExecutionStateStore)(nil)
