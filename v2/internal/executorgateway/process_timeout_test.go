package executorgateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestProcessTimeoutCoordinatorUsesGatewayTimerAndCorePermission(t *testing.T) {
	authority := &recordingTimeoutAuthority{result: testTimeoutBeginResult(true)}
	dispatcher := &recordingProcessDispatcher{}
	coordinator, err := NewProcessTimeoutCoordinator(authority, dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	start := testTimeoutStartExchange()
	request := testTimeoutDispatchRequest(time.Now().Add(-time.Millisecond))
	result, err := coordinator.Run(t.Context(), start, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Source != ProcessTimeoutSourceGatewayTimer || result.Terminate == nil || authority.calls() != 1 || dispatcher.calls() != 1 {
		t.Fatalf("timeout result = %+v, begin calls=%d dispatch calls=%d", result, authority.calls(), dispatcher.calls())
	}
	dispatched := dispatcher.lastRequest()
	if dispatched.ExpectedConnectionGeneration != request.ExpectedConnectionGeneration || dispatched.Context != request.Context || dispatched.Directives != nil {
		t.Fatalf("terminate dispatch = %+v", dispatched)
	}
	if string(dispatched.RPC) != `{"id":"timeout-1","method":"process/terminate","params":{"processId":"80000000-0000-4000-8000-000000000008"}}` {
		t.Fatalf("terminate RPC = %s", dispatched.RPC)
	}
}

func TestProcessTimeoutCoordinatorUsesAgentxSignalThroughSameBeginPath(t *testing.T) {
	authority := &recordingTimeoutAuthority{result: testTimeoutBeginResult(true)}
	dispatcher := &recordingProcessDispatcher{}
	coordinator, err := NewProcessTimeoutCoordinator(authority, dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	start := testTimeoutStartExchange()
	request := testTimeoutDispatchRequest(time.Now().Add(time.Hour))
	start.timeoutDue <- ProcessTimeoutDue{ProcessID: testProcessID, Context: request.Context}
	result, err := coordinator.Run(t.Context(), start, request)
	if err != nil || result.Source != ProcessTimeoutSourceAgentx || authority.calls() != 1 || dispatcher.calls() != 1 {
		t.Fatalf("agentx timeout result = %+v, %v; begin=%d dispatch=%d", result, err, authority.calls(), dispatcher.calls())
	}
}

func TestProcessTimeoutCoordinatorNeverSendsWithoutBegan(t *testing.T) {
	tests := []struct {
		name      string
		result    BeginOperationDispatchResult
		beginErr  error
		wantError bool
	}{
		{name: "already skipped", result: testTimeoutBeginResult(false)},
		{name: "core unavailable", beginErr: errors.New("core unavailable"), wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authority := &recordingTimeoutAuthority{result: test.result, err: test.beginErr}
			dispatcher := &recordingProcessDispatcher{}
			coordinator, err := NewProcessTimeoutCoordinator(authority, dispatcher)
			if err != nil {
				t.Fatal(err)
			}
			result, err := coordinator.Run(t.Context(), testTimeoutStartExchange(), testTimeoutDispatchRequest(time.Now().Add(-time.Millisecond)))
			if (err != nil) != test.wantError {
				t.Fatalf("Run() error = %v", err)
			}
			if result.Terminate != nil || dispatcher.calls() != 0 || authority.calls() != 1 {
				t.Fatalf("ungranted timeout sent terminate: result=%+v begin=%d dispatch=%d", result, authority.calls(), dispatcher.calls())
			}
		})
	}
}

func TestProcessTimeoutCoordinatorStopsForTerminalProcess(t *testing.T) {
	authority := &recordingTimeoutAuthority{result: testTimeoutBeginResult(true)}
	dispatcher := &recordingProcessDispatcher{}
	coordinator, err := NewProcessTimeoutCoordinator(authority, dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	start := testTimeoutStartExchange()
	close(start.timeoutDue)
	close(start.done)
	result, err := coordinator.Run(t.Context(), start, testTimeoutDispatchRequest(time.Now().Add(time.Hour)))
	if err != nil || !result.ProcessTerminalBeforeDeadline || authority.calls() != 0 || dispatcher.calls() != 0 {
		t.Fatalf("terminal-before-timeout result = %+v, %v; begin=%d dispatch=%d", result, err, authority.calls(), dispatcher.calls())
	}
}

func testTimeoutStartExchange() *ProcessExchange {
	return &ProcessExchange{
		holder:   ConnectionHolder{ExecutorID: testExecutorID, Generation: 7},
		response: make(chan json.RawMessage, 1), events: make(chan json.RawMessage, 1),
		timeoutDue: make(chan ProcessTimeoutDue, 1), failure: make(chan error, 1), done: make(chan struct{}),
	}
}

func testTimeoutDispatchRequest(deadline time.Time) ProcessTimeoutDispatchRequest {
	routing := testProcessTimeoutRoutingContext()
	return ProcessTimeoutDispatchRequest{
		ExecutorID: testExecutorID, ExpectedConnectionGeneration: 7, Context: routing, Deadline: deadline,
		RPCRequestID: json.RawMessage(`"timeout-1"`),
		Begin: BeginOperationDispatchRequest{
			OperationID: routing.OperationID, ExecutionID: routing.ExecutionID, RunID: routing.RunID, RunAttemptID: routing.RunAttemptID,
			RunAttemptGeneration: routing.RunAttemptGeneration, ConnectionGeneration: 7,
			Params: json.RawMessage(`{"processId":"80000000-0000-4000-8000-000000000008"}`),
		},
	}
}

func testTimeoutBeginResult(began bool) BeginOperationDispatchResult {
	routing := testProcessTimeoutRoutingContext()
	status := "skipped"
	generation := int64(0)
	if began {
		status = "dispatching"
		generation = 7
	}
	return BeginOperationDispatchResult{
		Began: began,
		Execution: ExecutionState{
			ExecutionID: routing.ExecutionID, RunID: routing.RunID, RunAttemptID: routing.RunAttemptID,
			RunAttemptGeneration: routing.RunAttemptGeneration, ExecutorID: testExecutorID, EnvironmentID: routing.EnvID,
		},
		Operation: ExecutionOperationState{
			OperationID: routing.OperationID, ExecutionID: routing.ExecutionID, MutationKey: routing.MutationKey,
			Status: status, ConnectionGeneration: generation,
		},
	}
}

type recordingTimeoutAuthority struct {
	mu     sync.Mutex
	count  int
	result BeginOperationDispatchResult
	err    error
}

func (authority *recordingTimeoutAuthority) BeginOperationDispatch(context.Context, BeginOperationDispatchRequest) (BeginOperationDispatchResult, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.count++
	return authority.result, authority.err
}

func (authority *recordingTimeoutAuthority) calls() int {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return authority.count
}

type recordingProcessDispatcher struct {
	mu       sync.Mutex
	requests []ProcessDispatchRequest
}

func (dispatcher *recordingProcessDispatcher) DispatchProcess(_ context.Context, request ProcessDispatchRequest) (*ProcessExchange, error) {
	dispatcher.mu.Lock()
	dispatcher.requests = append(dispatcher.requests, request)
	dispatcher.mu.Unlock()
	return &ProcessExchange{done: make(chan struct{}), response: make(chan json.RawMessage, 1), events: make(chan json.RawMessage)}, nil
}

func (dispatcher *recordingProcessDispatcher) calls() int {
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	return len(dispatcher.requests)
}

func (dispatcher *recordingProcessDispatcher) lastRequest() ProcessDispatchRequest {
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	return dispatcher.requests[len(dispatcher.requests)-1]
}

var _ OperationDispatchAuthority = (*recordingTimeoutAuthority)(nil)
var _ ProcessDispatcher = (*recordingProcessDispatcher)(nil)
