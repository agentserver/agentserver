package executorgateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/execprofile"
)

func TestReadFileExecutorCommitsCompactEvidenceAndReturnsUTF8(t *testing.T) {
	dispatcher := &fakeFilesystemDispatcher{respond: func(request FilesystemDispatchRequest) json.RawMessage {
		message, err := codexwire.Parse(request.RPC)
		if err != nil {
			t.Fatal(err)
		}
		return json.RawMessage(fmt.Sprintf(`{"id":%s,"result":{"chunk":"aGVsbG8=","eof":true}}`, message.ID))
	}}
	executor, authority := newReadFileExecutorFixture(t, dispatcher)
	result, err := executor.Execute(t.Context(), ReadFileExecuteRequest{
		Principal: testExecutorMCPPrincipal("capability-read-file"), ToolCallID: "call-read-file-success",
		Arguments: json.RawMessage(`{"environment_id":"60000000-0000-4000-8000-000000000006","path":"data.txt","offset":2,"limit":5}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "succeeded" || result.Path != "data.txt" || result.Offset != 2 || result.RequestedBytes != 5 ||
		result.BytesRead != 5 || !result.EOF || result.Encoding != "utf-8" || result.Content != "hello" {
		t.Fatalf("read_file result = %+v", result)
	}
	if dispatcher.contextErr != nil {
		t.Fatalf("dispatch did not use the live executor lifecycle: %v", dispatcher.contextErr)
	}
	if len(dispatcher.requests) != 1 || dispatcher.requests[0].Context.EnvID != testEnvironmentID {
		t.Fatalf("filesystem dispatches = %+v", dispatcher.requests)
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if authority.ackCalls != 1 || authority.operation.Status != "succeeded" || authority.execution.Status != "succeeded" {
		t.Fatalf("core read-file state = execution %+v operation %+v acks %d", authority.execution, authority.operation, authority.ackCalls)
	}
	for name, values := range map[string][]json.RawMessage{
		"acknowledgement":  authority.acknowledgements,
		"operation result": authority.operationResults,
		"execution result": authority.executionResults,
	} {
		for _, value := range values {
			if bytes.Contains(value, []byte("aGVsbG8=")) || bytes.Contains(value, []byte("hello")) {
				t.Fatalf("%s retained file content: %s", name, value)
			}
			if len(value) > 2048 {
				t.Fatalf("%s is not compact: %d bytes", name, len(value))
			}
		}
	}
}

func TestReadFileExecutorClosesDeterministicAndUncertainOutcomes(t *testing.T) {
	tests := []struct {
		name         string
		dispatcher   *fakeFilesystemDispatcher
		wantStatus   string
		wantAckCalls int
	}{
		{
			name: "remote error",
			dispatcher: &fakeFilesystemDispatcher{respond: responseForFilesystemRequest(func(id json.RawMessage) string {
				return fmt.Sprintf(`{"id":%s,"error":{"code":-32021,"message":"rejected"}}`, id)
			})},
			wantStatus: "failed", wantAckCalls: 1,
		},
		{
			name: "malformed result",
			dispatcher: &fakeFilesystemDispatcher{respond: responseForFilesystemRequest(func(id json.RawMessage) string {
				return fmt.Sprintf(`{"id":%s,"result":{"chunk":"YQ==","eof":true,"future":1}}`, id)
			})},
			wantStatus: "unknown", wantAckCalls: 0,
		},
		{
			name:       "ambiguous write",
			dispatcher: &fakeFilesystemDispatcher{dispatchErr: ErrDispatchAmbiguous},
			wantStatus: "unknown", wantAckCalls: 0,
		},
		{
			name:       "pre-send failure",
			dispatcher: &fakeFilesystemDispatcher{dispatchErr: ErrFilesystemReadUnavailable, nilExchange: true},
			wantStatus: "unknown", wantAckCalls: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor, authority := newReadFileExecutorFixture(t, test.dispatcher)
			result, err := executor.Execute(t.Context(), ReadFileExecuteRequest{
				Principal: testExecutorMCPPrincipal("capability-read-file"), ToolCallID: "call-read-file-outcome",
				Arguments: json.RawMessage(`{"environment_id":"60000000-0000-4000-8000-000000000006","path":"data.bin","limit":1}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != test.wantStatus || result.BytesRead != 0 || result.Content != "" {
				t.Fatalf("read_file outcome = %+v", result)
			}
			authority.mu.Lock()
			defer authority.mu.Unlock()
			if authority.ackCalls != test.wantAckCalls || authority.operation.Status != test.wantStatus || authority.execution.Status != test.wantStatus {
				t.Fatalf("core outcome = execution %q operation %q acks %d", authority.execution.Status, authority.operation.Status, authority.ackCalls)
			}
		})
	}
}

func TestReadFileProjectionUsesBase64WhenTextWouldExceedBound(t *testing.T) {
	content := bytes.Repeat([]byte{0}, int(execprofile.MaxFilesystemReadLength))
	canonical := base64.StdEncoding.EncodeToString(content)
	result, err := projectReadFileResult(ReadFileV1Plan{RelativePath: "data.bin", Limit: execprofile.MaxFilesystemReadLength}, content, canonical, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Encoding != "base64" || result.Content != canonical || result.BytesRead != execprofile.MaxFilesystemReadLength {
		t.Fatalf("maximum control-byte projection = encoding %q content bytes %d read %d", result.Encoding, len(result.Content), result.BytesRead)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > MaxReadFileV1ResultBytes || len(result.Content) != 1_398_104 {
		t.Fatalf("bounded base64 result = JSON %d content %d", len(encoded), len(result.Content))
	}

	binary := []byte{0xff, 0x00, 0xfe}
	result, err = projectReadFileResult(ReadFileV1Plan{RelativePath: "binary", Limit: 3}, binary, base64.StdEncoding.EncodeToString(binary), false)
	if err != nil || result.Encoding != "base64" {
		t.Fatalf("binary projection = %+v, %v", result, err)
	}
}

func TestClassifyReadFileResponseRejectsNonExactOrOversizedBlocks(t *testing.T) {
	invalid := []json.RawMessage{
		json.RawMessage(`{"id":"request-1","result":{"chunk":null,"eof":true}}`),
		json.RawMessage(`{"id":"request-1","result":{"chunk":"YR==","eof":true}}`),
		json.RawMessage(`{"id":"request-1","result":{"chunk":"YWI=","eof":true}}`),
		json.RawMessage(`{"id":"request-1","result":{"chunk":"YQ==","eof":true,"future":1}}`),
		json.RawMessage(`{"id":"other","result":{"chunk":"YQ==","eof":true}}`),
	}
	for _, raw := range invalid {
		if _, err := classifyReadFileResponse(raw, "request-1", 1); err == nil {
			t.Errorf("invalid read-file response was accepted: %s", raw)
		}
	}
	outcome, err := classifyReadFileResponse(json.RawMessage(`{"id":"request-1","error":{"code":-32000,"message":"failed"}}`), "request-1", 1)
	if err != nil || outcome.responseKind != "error" || outcome.responseSHA256 == "" {
		t.Fatalf("deterministic error classification = %+v, %v", outcome, err)
	}
}

func responseForFilesystemRequest(build func(json.RawMessage) string) func(FilesystemDispatchRequest) json.RawMessage {
	return func(request FilesystemDispatchRequest) json.RawMessage {
		message, err := codexwire.Parse(request.RPC)
		if err != nil {
			panic(err)
		}
		return json.RawMessage(build(message.ID))
	}
}

func newReadFileExecutorFixture(t *testing.T, dispatcher *fakeFilesystemDispatcher) (*ReadFileExecutor, *fakeReadFileAuthority) {
	t.Helper()
	registered := testRegisteredEnvironment(testEnvironmentID, `{"kind":"local","root":"/workspace"}`)
	registered.OuterProfileVersion = execprofile.FilesystemReadVersion
	resolver, err := NewEnvironmentResolver(&fakeEnvironmentRegistry{environments: []RegisteredEnvironment{registered}})
	if err != nil {
		t.Fatal(err)
	}
	identities, err := NewReadFileV1IdentityAllocator(deterministicIDGenerator())
	if err != nil {
		t.Fatal(err)
	}
	transitions, err := NewExecutionTransitionAllocator("90000000-0000-4000-8000-000000000009", deterministicIDGenerator())
	if err != nil {
		t.Fatal(err)
	}
	authority := newFakeReadFileAuthority()
	executor, err := NewReadFileExecutor(
		resolver, authority, dispatcher, identities, transitions,
		DefaultReadFileExecutorConfig(t.Context()),
	)
	if err != nil {
		t.Fatal(err)
	}
	return executor, authority
}

type fakeFilesystemDispatcher struct {
	mu          sync.Mutex
	respond     func(FilesystemDispatchRequest) json.RawMessage
	dispatchErr error
	nilExchange bool
	requests    []FilesystemDispatchRequest
	contextErr  error
}

func (dispatcher *fakeFilesystemDispatcher) DispatchFilesystem(ctx context.Context, request FilesystemDispatchRequest) (*FilesystemExchange, error) {
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	dispatcher.contextErr = ctx.Err()
	dispatcher.requests = append(dispatcher.requests, request)
	if dispatcher.nilExchange {
		return nil, dispatcher.dispatchErr
	}
	exchange := &FilesystemExchange{
		holder: testProcessHolder(), response: make(chan json.RawMessage, 1), failure: make(chan error, 1), done: make(chan struct{}),
	}
	if dispatcher.respond != nil {
		exchange.response <- dispatcher.respond(request)
		close(exchange.done)
	}
	return exchange, dispatcher.dispatchErr
}

type fakeReadFileAuthority struct {
	mu sync.Mutex

	execution ExecutionState
	operation ExecutionOperationState
	records   []ExecutionTransitionRecord

	ackCalls         int
	acknowledgements []json.RawMessage
	operationResults []json.RawMessage
	executionResults []json.RawMessage
}

func newFakeReadFileAuthority() *fakeReadFileAuthority { return &fakeReadFileAuthority{} }

func (authority *fakeReadFileAuthority) PrepareExecution(_ context.Context, request PrepareExecutionRequest) (PrepareExecutionResult, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.record(request.Record)
	authority.execution = ExecutionState{
		ExecutionID: request.ExecutionID, RunID: request.RunID, RunAttemptID: request.RunAttemptID,
		RunAttemptGeneration: request.RunAttemptGeneration, AppServerToolCallID: request.AppServerToolCallID,
		ExecutorID: request.ExecutorID, EnvironmentID: request.EnvironmentID,
		ToolName: request.ToolName, ToolVersion: request.ToolVersion, MapperVersion: request.MapperVersion,
		PolicyVersion: request.PolicyVersion, PolicyDecision: request.PolicyDecision, OperationCount: request.OperationCount,
		Status: "approved", Version: 1,
	}
	return PrepareExecutionResult{Execution: authority.execution, Created: true}, nil
}

func (authority *fakeReadFileAuthority) PrepareOperation(_ context.Context, request PrepareOperationRequest) (PrepareOperationResult, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if request.ExpectedExecutionVersion != authority.execution.Version || authority.operation.OperationID != "" {
		return PrepareOperationResult{}, errors.New("prepare read-file operation version mismatch")
	}
	authority.record(request.Record)
	authority.execution.Version++
	authority.operation = ExecutionOperationState{
		OperationID: request.OperationID, ExecutionID: request.ExecutionID, Ordinal: request.Ordinal,
		Kind: request.Kind, EffectClass: request.EffectClass, MutationKey: request.MutationKey,
		Status: "prepared", Version: 1,
	}
	return PrepareOperationResult{Execution: authority.execution, Operation: authority.operation, Created: true}, nil
}

func (authority *fakeReadFileAuthority) BeginOperationDispatch(_ context.Context, request BeginOperationDispatchRequest) (BeginOperationDispatchResult, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if authority.operation.Status != "prepared" || request.ExpectedExecutionVersion != authority.execution.Version || request.ExpectedOperationVersion != authority.operation.Version {
		return BeginOperationDispatchResult{}, errors.New("begin read-file operation version mismatch")
	}
	authority.record(request.Record)
	authority.operation.Status = "dispatching"
	authority.operation.ConnectionGeneration = request.ConnectionGeneration
	authority.operation.Version++
	authority.execution.Status = "dispatching"
	authority.execution.Version++
	return BeginOperationDispatchResult{Execution: authority.execution, Operation: authority.operation, Began: true}, nil
}

func (authority *fakeReadFileAuthority) AcknowledgeOperation(_ context.Context, request AcknowledgeOperationRequest) (AcknowledgeOperationResult, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if authority.operation.Status != "dispatching" || request.ExpectedExecutionVersion != authority.execution.Version || request.ExpectedOperationVersion != authority.operation.Version {
		return AcknowledgeOperationResult{}, errors.New("acknowledge read-file operation version mismatch")
	}
	authority.record(request.Record)
	authority.ackCalls++
	authority.acknowledgements = append(authority.acknowledgements, append(json.RawMessage(nil), request.Acknowledgement...))
	authority.operation.Status = "acknowledged"
	authority.operation.Version++
	authority.execution.Status = "running"
	authority.execution.Version++
	return AcknowledgeOperationResult{Execution: authority.execution, Operation: authority.operation, Changed: true}, nil
}

func (authority *fakeReadFileAuthority) CompleteOperation(_ context.Context, request CompleteOperationRequest) (CompleteOperationResult, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if request.ExpectedExecutionVersion != authority.execution.Version || request.ExpectedOperationVersion != authority.operation.Version {
		return CompleteOperationResult{}, errors.New("complete read-file operation version mismatch")
	}
	if authority.operation.Status == "dispatching" && request.TerminalStatus != "unknown" {
		return CompleteOperationResult{}, errors.New("unacknowledged read-file operation completed as known")
	}
	authority.record(request.Record)
	authority.operationResults = append(authority.operationResults, append(json.RawMessage(nil), request.Result...))
	authority.operation.Status = request.TerminalStatus
	authority.operation.Version++
	authority.execution.Version++
	return CompleteOperationResult{Execution: authority.execution, Operation: authority.operation, Changed: true}, nil
}

func (authority *fakeReadFileAuthority) SkipOperation(context.Context, SkipOperationRequest) (SkipOperationResult, error) {
	return SkipOperationResult{}, errors.New("read_file has no skippable operation")
}

func (authority *fakeReadFileAuthority) CompleteExecution(_ context.Context, request CompleteExecutionRequest) (CompleteExecutionResult, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if request.ExpectedExecutionVersion != authority.execution.Version || request.TerminalStatus != authority.operation.Status {
		return CompleteExecutionResult{}, errors.New("complete read-file execution version or status mismatch")
	}
	authority.record(request.Record)
	authority.executionResults = append(authority.executionResults, append(json.RawMessage(nil), request.Result...))
	authority.execution.Status = request.TerminalStatus
	authority.execution.Version++
	return CompleteExecutionResult{Execution: authority.execution, Changed: true}, nil
}

func (authority *fakeReadFileAuthority) record(record ExecutionTransitionRecord) {
	if len(authority.records) != 0 && record.ProducerSeq <= authority.records[len(authority.records)-1].ProducerSeq {
		panic("read-file transition sequence regressed")
	}
	authority.records = append(authority.records, record)
}

var _ FilesystemDispatcher = (*fakeFilesystemDispatcher)(nil)
var _ ExecutionAuthority = (*fakeReadFileAuthority)(nil)
