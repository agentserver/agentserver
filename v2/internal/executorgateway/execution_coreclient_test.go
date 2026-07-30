package executorgateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/coreserver"
)

const (
	testCoreExecutionID = "50000000-0000-4000-8000-000000000005"
	testCoreOperationID = "51000000-0000-4000-8000-000000000005"
	testCoreRunID       = "41000000-0000-4000-8000-000000000004"
	testCoreAttemptID   = "42000000-0000-4000-8000-000000000004"
	testCoreMutationKey = "61000000-0000-4000-8000-000000000006"
)

func TestCoreExecutionClientRoundTrip(t *testing.T) {
	commands := &recordingExecutionContractCommands{}
	handler, err := coreserver.NewExecutionHandler(allowCoreWorkload{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewCoreConnectionClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	prepare := testGatewayPrepareExecutionRequest()
	prepared, err := client.PrepareExecution(t.Context(), prepare)
	if err != nil || !prepared.Created || prepared.Execution.Status != "approved" {
		t.Fatalf("PrepareExecution() = %+v, %v", prepared, err)
	}
	if string(commands.prepareExecution.Arguments) != string(prepare.Arguments) || commands.prepareExecution.ToolVersion != prepare.ToolVersion {
		t.Fatalf("PrepareExecution wire request = %+v", commands.prepareExecution)
	}

	prepareOperation := PrepareOperationRequest{
		OperationID: testCoreOperationID, ExecutionID: prepared.Execution.ExecutionID,
		RunID: testCoreRunID, RunAttemptID: testCoreAttemptID, HolderID: "holder", RunAttemptGeneration: 3,
		ExpectedExecutionVersion: prepared.Execution.Version, Ordinal: 1, Kind: "process_start", EffectClass: "mutation",
		MutationKey: testCoreMutationKey, Params: json.RawMessage(`{"argv":["pwd"]}`), Record: testGatewayTransitionRecord(2),
	}
	operation, err := client.PrepareOperation(t.Context(), prepareOperation)
	if err != nil || !operation.Created || operation.Operation.Status != "prepared" || operation.Execution.Version != 2 {
		t.Fatalf("PrepareOperation() = %+v, %v", operation, err)
	}

	begin := BeginOperationDispatchRequest{
		OperationID: testCoreOperationID, ExecutionID: testCoreExecutionID, RunID: testCoreRunID, RunAttemptID: testCoreAttemptID,
		HolderID: "holder", RunAttemptGeneration: 3, ConnectionGeneration: 7,
		ExpectedExecutionVersion: operation.Execution.Version, ExpectedOperationVersion: operation.Operation.Version,
		PolicyContext: prepare.PolicyContext, OperationPlan: prepare.OperationPlan, Params: prepareOperation.Params, Record: testGatewayTransitionRecord(3),
	}
	dispatch, err := client.BeginOperationDispatch(t.Context(), begin)
	if err != nil || !dispatch.Began || dispatch.Operation.Status != "dispatching" || dispatch.Operation.ConnectionGeneration != 7 {
		t.Fatalf("BeginOperationDispatch() = %+v, %v", dispatch, err)
	}

	ack := AcknowledgeOperationRequest{
		OperationID: testCoreOperationID, ExecutionID: testCoreExecutionID, RunID: testCoreRunID, RunAttemptID: testCoreAttemptID,
		RunAttemptGeneration: 3, ConnectionGeneration: 7,
		ExpectedExecutionVersion: dispatch.Execution.Version, ExpectedOperationVersion: dispatch.Operation.Version,
		Acknowledgement: json.RawMessage(`{"accepted":true,"mutationKey":"61000000-0000-4000-8000-000000000006"}`), Record: testGatewayTransitionRecord(4),
	}
	acknowledged, err := client.AcknowledgeOperation(t.Context(), ack)
	if err != nil || !acknowledged.Changed || acknowledged.Operation.AcknowledgementDigest == nil || acknowledged.Operation.Status != "acknowledged" {
		t.Fatalf("AcknowledgeOperation() = %+v, %v", acknowledged, err)
	}

	completeOperation := CompleteOperationRequest{
		OperationID: testCoreOperationID, ExecutionID: testCoreExecutionID, RunID: testCoreRunID, RunAttemptID: testCoreAttemptID,
		RunAttemptGeneration: 3, ConnectionGeneration: 7,
		ExpectedExecutionVersion: acknowledged.Execution.Version, ExpectedOperationVersion: acknowledged.Operation.Version,
		TerminalStatus: "succeeded", Result: json.RawMessage(`{"exitCode":0}`), Record: testGatewayTransitionRecord(5),
	}
	completedOperation, err := client.CompleteOperation(t.Context(), completeOperation)
	if err != nil || !completedOperation.Changed || completedOperation.Operation.Status != "succeeded" || completedOperation.Operation.TerminalResultDigest == nil {
		t.Fatalf("CompleteOperation() = %+v, %v", completedOperation, err)
	}

	completeExecution := CompleteExecutionRequest{
		ExecutionID: testCoreExecutionID, RunID: testCoreRunID, RunAttemptID: testCoreAttemptID, RunAttemptGeneration: 3,
		ExpectedExecutionVersion: completedOperation.Execution.Version, TerminalStatus: "succeeded",
		Result: json.RawMessage(`{"status":"succeeded"}`), Record: testGatewayTransitionRecord(6),
	}
	completedExecution, err := client.CompleteExecution(t.Context(), completeExecution)
	if err != nil || !completedExecution.Changed || completedExecution.Execution.Status != "succeeded" || completedExecution.Execution.TerminalResultDigest == nil {
		t.Fatalf("CompleteExecution() = %+v, %v", completedExecution, err)
	}
}

func TestCoreExecutionClientPreservesConflictDetails(t *testing.T) {
	commands := &recordingExecutionContractCommands{beginError: &coredb.StateError{
		Code: coredb.ErrorVersionConflict, Message: "operation version does not match", CurrentVersion: 9,
	}}
	handler, err := coreserver.NewExecutionHandler(allowCoreWorkload{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewCoreConnectionClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.BeginOperationDispatch(t.Context(), testGatewayBeginRequest())
	var commandError *CoreCommandError
	if !errors.As(err, &commandError) || commandError.Code != "version_conflict" || commandError.CurrentVersion != 9 || commandError.HTTPStatus != 409 {
		t.Fatalf("BeginOperationDispatch() error = %#v", err)
	}
}

func TestCoreExecutionClientRejectsInvalidOneShotPermission(t *testing.T) {
	commands := &recordingExecutionContractCommands{badBeginGeneration: true}
	handler, err := coreserver.NewExecutionHandler(allowCoreWorkload{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewCoreConnectionClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.BeginOperationDispatch(t.Context(), testGatewayBeginRequest())
	if err == nil || !strings.Contains(err.Error(), "matching dispatching operation generation") {
		t.Fatalf("invalid began response error = %v", err)
	}
}

func TestCoreExecutionClientAcceptsNonDispatchedSkip(t *testing.T) {
	commands := &recordingExecutionContractCommands{}
	handler, err := coreserver.NewExecutionHandler(allowCoreWorkload{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewCoreConnectionClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	skipped, err := client.SkipOperation(t.Context(), SkipOperationRequest{
		OperationID: testCoreOperationID, ExecutionID: testCoreExecutionID, RunID: testCoreRunID, RunAttemptID: testCoreAttemptID,
		HolderID: "holder", RunAttemptGeneration: 3, ExpectedExecutionVersion: 5, ExpectedOperationVersion: 1,
		Result: json.RawMessage(`{"reason":"process_terminal_before_deadline"}`), Record: testGatewayTransitionRecord(7),
	})
	if err != nil || !skipped.Changed || skipped.Operation.Status != "skipped" || skipped.Operation.TerminalResultDigest == nil || skipped.Operation.ConnectionGeneration != 0 {
		t.Fatalf("SkipOperation() = %+v, %v", skipped, err)
	}
}

func TestGatewayOperationStateRejectsDispatchedSkip(t *testing.T) {
	terminal := testContractDigest("operation-result", "skip-result")
	state := testContractOperationState("skipped", 2, 7, nil, &terminal)
	if _, err := gatewayExecutionOperationState(state); err == nil || !strings.Contains(err.Error(), "skipped operation crossed") {
		t.Fatalf("gatewayExecutionOperationState() error = %v", err)
	}
}

func testGatewayPrepareExecutionRequest() PrepareExecutionRequest {
	return PrepareExecutionRequest{
		ExecutionID: testCoreExecutionID, RunID: testCoreRunID, RunAttemptID: testCoreAttemptID,
		HolderID: "holder", RunAttemptGeneration: 3, ExpectedRunVersion: 1, ExpectedRunAttemptVersion: 1,
		AppServerToolCallID: "call-1", ExecutorID: testExecutorID, EnvironmentID: testEnvironmentID,
		ToolName: "shell", ToolVersion: "executor-mcp/1.0", MapperVersion: "shell-v1", PolicyVersion: "policy-v1",
		OperationCount: 1, Arguments: json.RawMessage(`{"argv":["pwd"]}`),
		ToolSchema:    json.RawMessage(`{"type":"object","required":["argv"],"properties":{"argv":{"type":"array","items":{"type":"string"}}}}`),
		OperationPlan: json.RawMessage(`{"operations":[{"kind":"process_start","ordinal":1}]}`),
		PolicyContext: json.RawMessage(`{"decision":"allow"}`), PolicyDecision: "allow", Record: testGatewayTransitionRecord(1),
	}
}

func testGatewayBeginRequest() BeginOperationDispatchRequest {
	prepare := testGatewayPrepareExecutionRequest()
	return BeginOperationDispatchRequest{
		OperationID: testCoreOperationID, ExecutionID: testCoreExecutionID, RunID: testCoreRunID, RunAttemptID: testCoreAttemptID,
		HolderID: "holder", RunAttemptGeneration: 3, ConnectionGeneration: 7,
		ExpectedExecutionVersion: 2, ExpectedOperationVersion: 1,
		PolicyContext: prepare.PolicyContext, OperationPlan: prepare.OperationPlan,
		Params: json.RawMessage(`{"argv":["pwd"]}`), Record: testGatewayTransitionRecord(3),
	}
}

func testGatewayTransitionRecord(seed int) ExecutionTransitionRecord {
	return ExecutionTransitionRecord{
		EventID:            testCoreUUID(0x70 + seed),
		ProducerInstanceID: "72000000-0000-4000-8000-000000000001",
		ProducerSeq:        int64(seed),
		OutboxID:           testCoreUUID(0x80 + seed),
	}
}

func testCoreUUID(seed int) string {
	return fmt.Sprintf("%08x-0000-4000-8000-%012x", seed, seed)
}

type recordingExecutionContractCommands struct {
	prepareExecution   corecontract.PrepareExecutionRequest
	execution          corecontract.ExecutionState
	operation          corecontract.ExecutionOperationState
	beginError         error
	badBeginGeneration bool
}

func (commands *recordingExecutionContractCommands) PrepareExecution(_ context.Context, request corecontract.PrepareExecutionRequest) (corecontract.PrepareExecutionResponse, error) {
	commands.prepareExecution = request
	commands.execution = testContractExecutionState("approved", 1, nil)
	return corecontract.PrepareExecutionResponse{Execution: commands.execution, Created: true}, nil
}

func (commands *recordingExecutionContractCommands) PrepareOperation(_ context.Context, request corecontract.PrepareOperationRequest) (corecontract.PrepareOperationResponse, error) {
	commands.execution = testContractExecutionState("approved", 2, nil)
	commands.operation = testContractOperationState("prepared", 1, 0, nil, nil)
	return corecontract.PrepareOperationResponse{Execution: commands.execution, Operation: commands.operation, Created: true}, nil
}

func (commands *recordingExecutionContractCommands) BeginOperationDispatch(_ context.Context, request corecontract.BeginOperationDispatchRequest) (corecontract.BeginOperationDispatchResponse, error) {
	if commands.beginError != nil {
		return corecontract.BeginOperationDispatchResponse{}, commands.beginError
	}
	commands.execution = testContractExecutionState("dispatching", 3, nil)
	generation := request.ConnectionGeneration
	if commands.badBeginGeneration {
		generation++
	}
	commands.operation = testContractOperationState("dispatching", 2, generation, nil, nil)
	return corecontract.BeginOperationDispatchResponse{Execution: commands.execution, Operation: commands.operation, Began: true}, nil
}

func (commands *recordingExecutionContractCommands) AcknowledgeOperation(_ context.Context, request corecontract.AcknowledgeOperationRequest) (corecontract.AcknowledgeOperationResponse, error) {
	ack := testContractDigest("operation-ack", "ack")
	commands.execution = testContractExecutionState("running", 4, nil)
	commands.operation = testContractOperationState("acknowledged", 3, request.ConnectionGeneration, &ack, nil)
	return corecontract.AcknowledgeOperationResponse{Execution: commands.execution, Operation: commands.operation, Changed: true}, nil
}

func (commands *recordingExecutionContractCommands) CompleteOperation(_ context.Context, request corecontract.CompleteOperationRequest) (corecontract.CompleteOperationResponse, error) {
	ack := testContractDigest("operation-ack", "ack")
	terminal := testContractDigest("operation-result", "operation-result")
	commands.execution = testContractExecutionState("running", 5, nil)
	commands.operation = testContractOperationState(request.TerminalStatus, 4, request.ConnectionGeneration, &ack, &terminal)
	return corecontract.CompleteOperationResponse{Execution: commands.execution, Operation: commands.operation, Changed: true}, nil
}

func (commands *recordingExecutionContractCommands) SkipOperation(_ context.Context, _ corecontract.SkipOperationRequest) (corecontract.SkipOperationResponse, error) {
	terminal := testContractDigest("operation-result", "skip-result")
	commands.execution = testContractExecutionState("running", 6, nil)
	commands.execution.OperationCount = 2
	commands.operation = testContractOperationState("skipped", 2, 0, nil, &terminal)
	commands.operation.Ordinal = 2
	commands.operation.Kind = coredb.OperationKindTimeoutTerminate
	return corecontract.SkipOperationResponse{Execution: commands.execution, Operation: commands.operation, Changed: true}, nil
}

func (commands *recordingExecutionContractCommands) CompleteExecution(_ context.Context, request corecontract.CompleteExecutionRequest) (corecontract.CompleteExecutionResponse, error) {
	terminal := testContractDigest("execution-result", "execution-result")
	commands.execution = testContractExecutionState(request.TerminalStatus, 6, &terminal)
	return corecontract.CompleteExecutionResponse{Execution: commands.execution, Changed: true}, nil
}

func testContractExecutionState(status string, version int64, terminal *corecontract.CanonicalJSONDigest) corecontract.ExecutionState {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	state := corecontract.ExecutionState{
		ExecutionID: testCoreExecutionID, RunID: testCoreRunID, RunAttemptID: testCoreAttemptID, RunAttemptGeneration: 3,
		AppServerToolCallID: "call-1", ExecutorID: testExecutorID, EnvironmentID: testEnvironmentID,
		ToolName: "shell", ToolVersion: "executor-mcp/1.0", MapperVersion: "shell-v1", PolicyVersion: "policy-v1",
		PolicyDecision: "allow", OperationCount: 1,
		ArgumentsDigest: testContractDigest("execution-arguments", "arguments"), ToolSchemaDigest: testContractDigest("tool-schema", "schema"),
		OperationPlanDigest: testContractDigest("operation-plan", "plan"), PolicyContextDigest: testContractDigest("policy-context", "policy"),
		Status: status, TerminalResultDigest: terminal, Version: version, CreatedAt: now, UpdatedAt: now,
	}
	if status == "dispatching" || status == "running" || terminal != nil {
		state.DispatchedAt = &now
	}
	if terminal != nil {
		state.TerminalAt = &now
	}
	return state
}

func testContractOperationState(status string, version, generation int64, acknowledgement, terminal *corecontract.CanonicalJSONDigest) corecontract.ExecutionOperationState {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	state := corecontract.ExecutionOperationState{
		OperationID: testCoreOperationID, ExecutionID: testCoreExecutionID, Ordinal: 1, Kind: "process_start",
		EffectClass: "mutation", MutationKey: testCoreMutationKey, ParamsDigest: testContractDigest("operation-params", "params"),
		Status: status, ConnectionGeneration: generation, AcknowledgementDigest: acknowledgement, TerminalResultDigest: terminal,
		Version: version, CreatedAt: now, UpdatedAt: now,
	}
	if generation > 0 {
		state.DispatchedAt = &now
	}
	if acknowledgement != nil {
		state.AcknowledgedAt = &now
	}
	if terminal != nil {
		state.TerminalAt = &now
	}
	return state
}

func testContractDigest(domain, value string) corecontract.CanonicalJSONDigest {
	digest := sha256.Sum256([]byte(value))
	return corecontract.CanonicalJSONDigest{Domain: domain, CanonicalizerVersion: coreCanonicalizerRFC8785V1, SHA256: hex.EncodeToString(digest[:])}
}

var _ coreserver.ExecutionCommands = (*recordingExecutionContractCommands)(nil)
