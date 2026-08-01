package executorgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executorgateway/agentxconn"
)

func TestShellExecutorCompletesSuccessfulProcessAndSkipsTimeout(t *testing.T) {
	authority := newFakeShellAuthority()
	dispatcher := &fakeShellDispatcher{start: func(request ProcessDispatchRequest) (*ProcessExchange, error) {
		exchange := testShellStartExchange(request, 8)
		exchange.response <- shellStartResponse(request.RPC, testProcessID)
		exchange.events <- json.RawMessage(`{"method":"process/output","params":{"processId":"80000000-0000-4000-8000-000000000008","seq":1,"stream":"stdout","chunk":"b2s="}}`)
		exchange.events <- json.RawMessage(`{"method":"process/exited","params":{"processId":"80000000-0000-4000-8000-000000000008","seq":2,"exitCode":0,"sandboxDenied":false}}`)
		exchange.events <- json.RawMessage(`{"method":"process/closed","params":{"processId":"80000000-0000-4000-8000-000000000008","seq":3}}`)
		closeShellStartExchange(exchange)
		return exchange, nil
	}}
	executor := newTestShellExecutor(t, authority, dispatcher)
	result, err := executor.Execute(t.Context(), testShellExecuteRequest(10_000))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "succeeded" || result.TimedOut || !result.OutputComplete || result.ExitCode == nil || *result.ExitCode != 0 ||
		len(result.Chunks) != 1 || result.Chunks[0].ChunkBase64 != "b2s=" || result.NextSequence != 4 {
		t.Fatalf("successful shell result = %+v", result)
	}
	if dispatcher.count() != 1 {
		t.Fatalf("successful shell dispatched %d RPCs, want start only", dispatcher.count())
	}
	if got := authority.operationStatuses(); got[0] != "succeeded" || got[1] != "skipped" || authority.executionStatus() != "succeeded" {
		t.Fatalf("core terminal states = operations %v execution %q", got, authority.executionStatus())
	}
	authority.assertMonotonicRecords(t)
}

func TestShellExecutorTimeoutBeginsTerminateAndWaitsForRealTerminal(t *testing.T) {
	authority := newFakeShellAuthority()
	var start *ProcessExchange
	dispatcher := &fakeShellDispatcher{}
	dispatcher.start = func(request ProcessDispatchRequest) (*ProcessExchange, error) {
		start = testShellStartExchange(request, 8)
		start.response <- shellStartResponse(request.RPC, testProcessID)
		start.timeoutDue <- ProcessTimeoutDue{ProcessID: testProcessID, Context: testProcessTimeoutRoutingFromRequest(request)}
		return start, nil
	}
	dispatcher.terminate = func(request ProcessDispatchRequest) (*ProcessExchange, error) {
		terminate := testShellCommandExchange(request)
		terminate.response <- shellTerminateResponse(request.RPC)
		close(terminate.done)
		close(terminate.events)
		start.events <- json.RawMessage(`{"method":"process/exited","params":{"processId":"80000000-0000-4000-8000-000000000008","seq":1,"exitCode":143,"sandboxDenied":false}}`)
		start.events <- json.RawMessage(`{"method":"process/closed","params":{"processId":"80000000-0000-4000-8000-000000000008","seq":2}}`)
		closeShellStartExchange(start)
		return terminate, nil
	}
	executor := newTestShellExecutor(t, authority, dispatcher)
	result, err := executor.Execute(t.Context(), testShellExecuteRequest(60_000))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" || !result.TimedOut || !result.OutputComplete || result.ExitCode == nil || *result.ExitCode != 143 {
		t.Fatalf("timed-out shell result = %+v", result)
	}
	if dispatcher.count() != 2 {
		t.Fatalf("timeout shell dispatched %d RPCs, want start+terminate", dispatcher.count())
	}
	statuses := authority.operationStatuses()
	if statuses[0] != "failed" || statuses[1] != "failed" || authority.executionStatus() != "failed" {
		t.Fatalf("timeout core terminal states = operations %v execution %q", statuses, authority.executionStatus())
	}
	if authority.skipCalls() != 0 {
		t.Fatalf("timeout path skipped timeout operation %d time(s)", authority.skipCalls())
	}
	authority.assertMonotonicRecords(t)
}

func TestShellExecutorClosesAmbiguousStartAsUnknownWithoutRetry(t *testing.T) {
	authority := newFakeShellAuthority()
	dispatcher := &fakeShellDispatcher{start: func(request ProcessDispatchRequest) (*ProcessExchange, error) {
		exchange := testShellStartExchange(request, 4)
		exchange.response <- shellStartResponse(request.RPC, testProcessID)
		exchange.events <- json.RawMessage(`{"method":"process/exited","params":{"processId":"80000000-0000-4000-8000-000000000008","seq":1,"exitCode":0,"sandboxDenied":false}}`)
		exchange.events <- json.RawMessage(`{"method":"process/closed","params":{"processId":"80000000-0000-4000-8000-000000000008","seq":2}}`)
		closeShellStartExchange(exchange)
		return exchange, ErrDispatchAmbiguous
	}}
	executor := newTestShellExecutor(t, authority, dispatcher)
	result, err := executor.Execute(t.Context(), testShellExecuteRequest(10_000))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "unknown" || dispatcher.count() != 1 || authority.ackCallsFor(ShellV1OperationProcessStart) != 0 {
		t.Fatalf("ambiguous shell result=%+v dispatches=%d start ACKs=%d", result, dispatcher.count(), authority.ackCallsFor(ShellV1OperationProcessStart))
	}
	statuses := authority.operationStatuses()
	if statuses[0] != "unknown" || statuses[1] != "skipped" || authority.executionStatus() != "unknown" {
		t.Fatalf("ambiguous core terminal states = operations %v execution %q", statuses, authority.executionStatus())
	}
}

func TestShellExecutorClosesDefinitePreSendFailureAsUnknown(t *testing.T) {
	authority := newFakeShellAuthority()
	dispatcher := &fakeShellDispatcher{start: func(ProcessDispatchRequest) (*ProcessExchange, error) {
		return nil, ErrExecutorUnavailable
	}}
	executor := newTestShellExecutor(t, authority, dispatcher)
	result, err := executor.Execute(t.Context(), testShellExecuteRequest(10_000))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "unknown" || result.OutputComplete || dispatcher.count() != 1 {
		t.Fatalf("pre-send failure result = %+v, dispatches %d", result, dispatcher.count())
	}
	statuses := authority.operationStatuses()
	if statuses[0] != "unknown" || statuses[1] != "skipped" || authority.executionStatus() != "unknown" {
		t.Fatalf("pre-send failure core terminal states = operations %v execution %q", statuses, authority.executionStatus())
	}
}

func TestShellExecutorDoesNotTreatTerminateResponseAsProcessTerminal(t *testing.T) {
	authority := newFakeShellAuthority()
	var start *ProcessExchange
	dispatcher := &fakeShellDispatcher{}
	dispatcher.start = func(request ProcessDispatchRequest) (*ProcessExchange, error) {
		start = testShellStartExchange(request, 1)
		start.response <- shellStartResponse(request.RPC, testProcessID)
		start.timeoutDue <- ProcessTimeoutDue{ProcessID: testProcessID, Context: testProcessTimeoutRoutingFromRequest(request)}
		return start, nil
	}
	dispatcher.terminate = func(request ProcessDispatchRequest) (*ProcessExchange, error) {
		terminate := testShellCommandExchange(request)
		terminate.response <- shellTerminateResponse(request.RPC)
		close(terminate.done)
		close(terminate.events)
		// Intentionally leave the start exchange without exited/closed.
		return terminate, nil
	}
	executor := newTestShellExecutor(t, authority, dispatcher)
	result, err := executor.Execute(t.Context(), testShellExecuteRequest(1))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "unknown" || !result.TimedOut || result.OutputComplete {
		t.Fatalf("missing real terminal result = %+v", result)
	}
	statuses := authority.operationStatuses()
	if statuses[0] != "unknown" || statuses[1] != "unknown" || authority.executionStatus() != "unknown" {
		t.Fatalf("missing terminal core states = operations %v execution %q", statuses, authority.executionStatus())
	}
}

func newTestShellExecutor(t *testing.T, authority *fakeShellAuthority, dispatcher *fakeShellDispatcher) *ShellExecutor {
	t.Helper()
	registry := &fakeEnvironmentRegistry{environments: []RegisteredEnvironment{
		testRegisteredEnvironment(testEnvironmentID, `{"kind":"local","root":"/workspace","defaultCwd":"."}`),
	}}
	resolver, err := NewEnvironmentResolver(registry)
	if err != nil {
		t.Fatal(err)
	}
	identitySequence := 0
	identityAllocator, err := NewShellV1IdentityAllocator(func() (string, error) {
		identitySequence++
		// The second identity is the process ID expected by the fixture.
		if identitySequence == 2 {
			return testProcessID, nil
		}
		return fmt.Sprintf("%08x-0000-4000-8000-%012x", 0x90+identitySequence, 0x90+identitySequence), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	transitionSequence := 0
	transitionAllocator, err := NewExecutionTransitionAllocator("71000000-0000-4000-8000-000000000001", func() (string, error) {
		transitionSequence++
		return fmt.Sprintf("%08x-0000-4000-8000-%012x", 0xa0+transitionSequence, 0xa0+transitionSequence), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultShellExecutorConfig(t.Context())
	config.TerminalGrace = 50 * time.Millisecond
	configureTestShellPolicy(t, &config)
	executor, err := NewShellExecutor(resolver, authority, dispatcher, identityAllocator, transitionAllocator, config)
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func testShellExecuteRequest(timeoutMillis int64) ShellExecuteRequest {
	return ShellExecuteRequest{
		Principal: testExecutorMCPPrincipal("capability-shell-executor"), ToolCallID: "call-shell-executor",
		Arguments: json.RawMessage(fmt.Sprintf(`{"environment_id":"%s","argv":["/bin/echo","ok"],"timeout_ms":%d}`, testEnvironmentID, timeoutMillis)),
	}
}

func testShellStartExchange(request ProcessDispatchRequest, eventCapacity int) *ProcessExchange {
	return &ProcessExchange{
		holder:   ConnectionHolder{ExecutorID: request.ExecutorID, Generation: request.ExpectedConnectionGeneration},
		response: make(chan json.RawMessage, 1), events: make(chan json.RawMessage, eventCapacity),
		timeoutDue: make(chan ProcessTimeoutDue, 1), failure: make(chan error, 1), done: make(chan struct{}),
	}
}

func testShellCommandExchange(request ProcessDispatchRequest) *ProcessExchange {
	return &ProcessExchange{
		holder:   ConnectionHolder{ExecutorID: request.ExecutorID, Generation: request.ExpectedConnectionGeneration},
		response: make(chan json.RawMessage, 1), events: make(chan json.RawMessage, 1),
		failure: make(chan error, 1), done: make(chan struct{}),
	}
}

func closeShellStartExchange(exchange *ProcessExchange) {
	close(exchange.events)
	close(exchange.timeoutDue)
	close(exchange.done)
}

func shellStartResponse(requestRPC json.RawMessage, processID string) json.RawMessage {
	var request struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(requestRPC, &request)
	response, _ := json.Marshal(struct {
		ID     json.RawMessage `json:"id"`
		Result struct {
			ProcessID string `json:"processId"`
		} `json:"result"`
	}{ID: request.ID, Result: struct {
		ProcessID string `json:"processId"`
	}{ProcessID: processID}})
	return response
}

func shellTerminateResponse(requestRPC json.RawMessage) json.RawMessage {
	var request struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(requestRPC, &request)
	response, _ := json.Marshal(struct {
		ID     json.RawMessage `json:"id"`
		Result struct{}        `json:"result"`
	}{ID: request.ID, Result: struct{}{}})
	return response
}

func testProcessTimeoutRoutingFromRequest(request ProcessDispatchRequest) agentxconn.RoutingContext {
	routing := request.Context
	routing.OperationID = request.Directives.ProcessTimeout.OperationID
	routing.MutationKey = request.Directives.ProcessTimeout.MutationKey
	return routing
}

type fakeShellDispatcher struct {
	mu        sync.Mutex
	requests  []ProcessDispatchRequest
	start     func(ProcessDispatchRequest) (*ProcessExchange, error)
	terminate func(ProcessDispatchRequest) (*ProcessExchange, error)
}

func (dispatcher *fakeShellDispatcher) DispatchProcess(_ context.Context, request ProcessDispatchRequest) (*ProcessExchange, error) {
	dispatcher.mu.Lock()
	dispatcher.requests = append(dispatcher.requests, request)
	start := dispatcher.start
	terminate := dispatcher.terminate
	dispatcher.mu.Unlock()
	var envelope struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(request.RPC, &envelope); err != nil {
		return nil, err
	}
	if envelope.Method == "process/start" {
		return start(request)
	}
	if terminate == nil {
		return nil, errors.New("unexpected process/terminate")
	}
	return terminate(request)
}

func (dispatcher *fakeShellDispatcher) count() int {
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	return len(dispatcher.requests)
}

type fakeShellAuthority struct {
	mu         sync.Mutex
	execution  ExecutionState
	operations map[int]ExecutionOperationState
	records    []ExecutionTransitionRecord
	acks       map[string]int
	skips      int
}

func newFakeShellAuthority() *fakeShellAuthority {
	return &fakeShellAuthority{operations: make(map[int]ExecutionOperationState, 2), acks: make(map[string]int)}
}

func (authority *fakeShellAuthority) PrepareExecution(_ context.Context, request PrepareExecutionRequest) (PrepareExecutionResult, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.record(request.Record)
	status := "approved"
	if request.PolicyDecision == PolicyDecisionAsk {
		status = "pending_approval"
	} else if request.PolicyDecision == PolicyDecisionDeny {
		status = "denied"
	}
	authority.execution = ExecutionState{
		ExecutionID: request.ExecutionID, RunID: request.RunID, RunAttemptID: request.RunAttemptID,
		RunAttemptGeneration: request.RunAttemptGeneration, AppServerToolCallID: request.AppServerToolCallID,
		ExecutorID: request.ExecutorID, EnvironmentID: request.EnvironmentID,
		ToolName: request.ToolName, ToolVersion: request.ToolVersion, MapperVersion: request.MapperVersion,
		PolicyVersion: request.PolicyVersion, PolicyDecision: request.PolicyDecision, OperationCount: request.OperationCount,
		Status: status, Version: 1,
	}
	return PrepareExecutionResult{Execution: authority.execution, Created: true}, nil
}

func (authority *fakeShellAuthority) PrepareOperation(_ context.Context, request PrepareOperationRequest) (PrepareOperationResult, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if request.ExpectedExecutionVersion != authority.execution.Version {
		return PrepareOperationResult{}, fmt.Errorf("prepare expected execution version %d, current %d", request.ExpectedExecutionVersion, authority.execution.Version)
	}
	authority.record(request.Record)
	authority.execution.Version++
	operation := ExecutionOperationState{
		OperationID: request.OperationID, ExecutionID: request.ExecutionID, Ordinal: request.Ordinal,
		Kind: request.Kind, EffectClass: request.EffectClass, MutationKey: request.MutationKey,
		Status: "prepared", Version: 1,
	}
	authority.operations[request.Ordinal] = operation
	return PrepareOperationResult{Execution: authority.execution, Operation: operation, Created: true}, nil
}

func (authority *fakeShellAuthority) BeginOperationDispatch(_ context.Context, request BeginOperationDispatchRequest) (BeginOperationDispatchResult, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	operation := authority.operationByID(request.OperationID)
	if operation.Status != "prepared" {
		return BeginOperationDispatchResult{Execution: authority.execution, Operation: operation, Began: false}, nil
	}
	if request.ExpectedExecutionVersion != authority.execution.Version || request.ExpectedOperationVersion != operation.Version {
		return BeginOperationDispatchResult{}, errors.New("begin version mismatch")
	}
	authority.record(request.Record)
	operation.Status = "dispatching"
	operation.ConnectionGeneration = request.ConnectionGeneration
	operation.Version++
	authority.operations[operation.Ordinal] = operation
	authority.execution.Version++
	if authority.execution.Status == "approved" {
		authority.execution.Status = "dispatching"
	}
	return BeginOperationDispatchResult{Execution: authority.execution, Operation: operation, Began: true}, nil
}

func (authority *fakeShellAuthority) AcknowledgeOperation(_ context.Context, request AcknowledgeOperationRequest) (AcknowledgeOperationResult, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	operation := authority.operationByID(request.OperationID)
	if request.ExpectedExecutionVersion != authority.execution.Version || request.ExpectedOperationVersion != operation.Version || operation.Status != "dispatching" {
		return AcknowledgeOperationResult{}, errors.New("ack version or state mismatch")
	}
	authority.record(request.Record)
	operation.Status = "acknowledged"
	operation.Version++
	authority.operations[operation.Ordinal] = operation
	authority.execution.Version++
	if authority.execution.Status == "dispatching" {
		authority.execution.Status = "running"
	}
	authority.acks[operation.Kind]++
	return AcknowledgeOperationResult{Execution: authority.execution, Operation: operation, Changed: true}, nil
}

func (authority *fakeShellAuthority) CompleteOperation(_ context.Context, request CompleteOperationRequest) (CompleteOperationResult, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	operation := authority.operationByID(request.OperationID)
	if request.ExpectedExecutionVersion != authority.execution.Version || request.ExpectedOperationVersion != operation.Version {
		return CompleteOperationResult{}, errors.New("complete operation version mismatch")
	}
	if operation.Status == "dispatching" && request.TerminalStatus != "unknown" {
		return CompleteOperationResult{}, errors.New("unacknowledged operation completed as known")
	}
	authority.record(request.Record)
	operation.Status = request.TerminalStatus
	operation.Version++
	authority.operations[operation.Ordinal] = operation
	authority.execution.Version++
	return CompleteOperationResult{Execution: authority.execution, Operation: operation, Changed: true}, nil
}

func (authority *fakeShellAuthority) SkipOperation(_ context.Context, request SkipOperationRequest) (SkipOperationResult, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	operation := authority.operationByID(request.OperationID)
	if request.ExpectedExecutionVersion != authority.execution.Version || request.ExpectedOperationVersion != operation.Version || operation.Status != "prepared" {
		return SkipOperationResult{}, errors.New("skip version or state mismatch")
	}
	if authority.operations[1].Status == "prepared" || authority.operations[1].Status == "dispatching" || authority.operations[1].Status == "acknowledged" {
		return SkipOperationResult{}, errors.New("skip before process terminal")
	}
	authority.record(request.Record)
	operation.Status = "skipped"
	operation.Version++
	authority.operations[operation.Ordinal] = operation
	authority.execution.Version++
	authority.skips++
	return SkipOperationResult{Execution: authority.execution, Operation: operation, Changed: true}, nil
}

func (authority *fakeShellAuthority) CompleteExecution(_ context.Context, request CompleteExecutionRequest) (CompleteExecutionResult, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if request.ExpectedExecutionVersion != authority.execution.Version {
		return CompleteExecutionResult{}, errors.New("complete execution version mismatch")
	}
	want := aggregateShellResultStatus(authority.operations[1].Status, authority.operations[2].Status)
	if request.TerminalStatus != want {
		return CompleteExecutionResult{}, fmt.Errorf("execution terminal status %q, want %q", request.TerminalStatus, want)
	}
	authority.record(request.Record)
	authority.execution.Status = request.TerminalStatus
	authority.execution.Version++
	return CompleteExecutionResult{Execution: authority.execution, Changed: true}, nil
}

func (authority *fakeShellAuthority) operationByID(operationID string) ExecutionOperationState {
	for _, operation := range authority.operations {
		if operation.OperationID == operationID {
			return operation
		}
	}
	return ExecutionOperationState{}
}

func (authority *fakeShellAuthority) record(record ExecutionTransitionRecord) {
	authority.records = append(authority.records, record)
}

func (authority *fakeShellAuthority) operationStatuses() [2]string {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return [2]string{authority.operations[1].Status, authority.operations[2].Status}
}

func (authority *fakeShellAuthority) executionStatus() string {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return authority.execution.Status
}

func (authority *fakeShellAuthority) skipCalls() int {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return authority.skips
}

func (authority *fakeShellAuthority) ackCallsFor(kind string) int {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return authority.acks[kind]
}

func (authority *fakeShellAuthority) assertMonotonicRecords(t *testing.T) {
	t.Helper()
	authority.mu.Lock()
	defer authority.mu.Unlock()
	previous := int64(0)
	for _, record := range authority.records {
		if record.ProducerSeq <= previous {
			t.Fatalf("committed producer sequence regressed: %d after %d", record.ProducerSeq, previous)
		}
		previous = record.ProducerSeq
	}
}

var _ ExecutionAuthority = (*fakeShellAuthority)(nil)
var _ ProcessDispatcher = (*fakeShellDispatcher)(nil)
