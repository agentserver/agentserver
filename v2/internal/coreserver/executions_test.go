package coreserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const (
	testExecutionID = "50000000-0000-4000-8000-000000000005"
	testOperationID = "51000000-0000-4000-8000-000000000005"
)

func TestExecutionHandlerRoutesAllCommands(t *testing.T) {
	record := corecontract.TransitionRecord{
		EventID:            "71000000-0000-4000-8000-000000000001",
		ProducerInstanceID: "72000000-0000-4000-8000-000000000001",
		ProducerSeq:        1,
		OutboxID:           "73000000-0000-4000-8000-000000000001",
	}
	commonOperation := corecontract.PrepareOperationRequest{
		OperationID: testOperationID, ExecutionID: testExecutionID,
		RunID: "41000000-0000-4000-8000-000000000004", RunAttemptID: "42000000-0000-4000-8000-000000000004",
		HolderID: "holder", RunAttemptGeneration: 3, ExpectedExecutionVersion: 1,
		Ordinal: 1, Kind: "process_start", EffectClass: "mutation", MutationKey: "61000000-0000-4000-8000-000000000006",
		Params: json.RawMessage(`{"argv":["pwd"]}`), Record: record,
	}
	tests := []struct {
		name       string
		path       string
		command    any
		wantAction string
		wantCall   string
	}{
		{
			name: "prepare execution", path: corecontract.PrepareExecutionPath,
			command: corecontract.PrepareExecutionRequest{
				ExecutionID: testExecutionID, RunID: commonOperation.RunID, RunAttemptID: commonOperation.RunAttemptID,
				HolderID: "holder", RunAttemptGeneration: 3, ExpectedRunVersion: 1, ExpectedRunAttemptVersion: 1,
				AppServerToolCallID: "call-1", ExecutorID: "20000000-0000-4000-8000-000000000002", EnvironmentID: "60000000-0000-4000-8000-000000000006",
				ToolName: "shell", ToolVersion: "executor-mcp/1.0", MapperVersion: "shell-v1", PolicyVersion: "policy-v1",
				OperationCount: 1, Arguments: json.RawMessage(`{"argv":["pwd"]}`), ToolSchema: json.RawMessage(`{"type":"object"}`),
				OperationPlan: json.RawMessage(`{"operations":[]}`), PolicyContext: json.RawMessage(`{"decision":"allow"}`), PolicyDecision: "allow", Record: record,
			},
			wantAction: "executions.prepare", wantCall: "prepare-execution",
		},
		{name: "prepare operation", path: corecontract.PrepareOperationPath(testExecutionID), command: commonOperation, wantAction: "execution-operations.prepare", wantCall: "prepare-operation"},
		{
			name: "begin dispatch", path: corecontract.BeginOperationDispatchPath(testExecutionID, testOperationID),
			command: corecontract.BeginOperationDispatchRequest{
				OperationID: testOperationID, ExecutionID: testExecutionID, RunID: commonOperation.RunID, RunAttemptID: commonOperation.RunAttemptID,
				HolderID: "holder", RunAttemptGeneration: 3, ConnectionGeneration: 7, ExpectedExecutionVersion: 2, ExpectedOperationVersion: 1,
				PolicyContext: json.RawMessage(`{"decision":"allow"}`), OperationPlan: json.RawMessage(`{"operations":[]}`), Params: commonOperation.Params, Record: record,
			},
			wantAction: "execution-operations.begin-dispatch", wantCall: "begin-dispatch",
		},
		{
			name: "acknowledge", path: corecontract.AcknowledgeOperationPath(testExecutionID, testOperationID),
			command: corecontract.AcknowledgeOperationRequest{
				OperationID: testOperationID, ExecutionID: testExecutionID, RunID: commonOperation.RunID, RunAttemptID: commonOperation.RunAttemptID,
				RunAttemptGeneration: 3, ConnectionGeneration: 7, ExpectedExecutionVersion: 3, ExpectedOperationVersion: 2,
				Acknowledgement: json.RawMessage(`{"accepted":true}`), Record: record,
			},
			wantAction: "execution-operations.acknowledge", wantCall: "acknowledge",
		},
		{
			name: "complete operation", path: corecontract.CompleteOperationPath(testExecutionID, testOperationID),
			command: corecontract.CompleteOperationRequest{
				OperationID: testOperationID, ExecutionID: testExecutionID, RunID: commonOperation.RunID, RunAttemptID: commonOperation.RunAttemptID,
				RunAttemptGeneration: 3, ConnectionGeneration: 7, ExpectedExecutionVersion: 4, ExpectedOperationVersion: 3,
				TerminalStatus: "succeeded", Result: json.RawMessage(`{"exitCode":0}`), Record: record,
			},
			wantAction: "execution-operations.complete", wantCall: "complete-operation",
		},
		{
			name: "skip operation", path: corecontract.SkipOperationPath(testExecutionID, testOperationID),
			command: corecontract.SkipOperationRequest{
				OperationID: testOperationID, ExecutionID: testExecutionID, RunID: commonOperation.RunID, RunAttemptID: commonOperation.RunAttemptID,
				HolderID: "holder", RunAttemptGeneration: 3, ExpectedExecutionVersion: 4, ExpectedOperationVersion: 1,
				Result: json.RawMessage(`{"reason":"process_terminal_before_deadline"}`), Record: record,
			},
			wantAction: "execution-operations.skip", wantCall: "skip-operation",
		},
		{
			name: "complete execution", path: corecontract.CompleteExecutionPath(testExecutionID),
			command: corecontract.CompleteExecutionRequest{
				ExecutionID: testExecutionID, RunID: commonOperation.RunID, RunAttemptID: commonOperation.RunAttemptID,
				RunAttemptGeneration: 3, ExpectedExecutionVersion: 5, TerminalStatus: "succeeded",
				Result: json.RawMessage(`{"status":"succeeded"}`), Record: record,
			},
			wantAction: "executions.complete", wantCall: "complete-execution",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authorizer := &recordingExecutionAuthorizer{}
			commands := &recordingExecutionCommands{}
			handler, err := NewExecutionHandler(authorizer, commands)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(test.command)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(raw))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status/body = %d/%s", response.Code, response.Body)
			}
			if authorizer.action != test.wantAction || commands.call != test.wantCall {
				t.Fatalf("action/call = %q/%q, want %q/%q", authorizer.action, commands.call, test.wantAction, test.wantCall)
			}
		})
	}
}

func TestExecutionHandlerRejectsMismatchedPathAndFailsClosed(t *testing.T) {
	commands := &recordingExecutionCommands{}
	handler, err := NewExecutionHandler(&recordingExecutionAuthorizer{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	command := corecontract.CompleteOperationRequest{ExecutionID: testExecutionID, OperationID: testOperationID}
	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, corecontract.CompleteOperationPath(testExecutionID, "52000000-0000-4000-8000-000000000005"), bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || commands.call != "" {
		t.Fatalf("mismatched path status/call/body = %d/%q/%s", response.Code, commands.call, response.Body)
	}

	denied, err := NewExecutionHandler(&recordingExecutionAuthorizer{err: errors.New("denied")}, commands)
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, corecontract.PrepareExecutionPath, bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	denied.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("denied status/body = %d/%s", response.Code, response.Body)
	}

	request = httptest.NewRequest(http.MethodGet, corecontract.PrepareExecutionPath, nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("method status/body = %d/%s", response.Code, response.Body)
	}
}

func TestParseExecutionActionRejectsAmbiguousPaths(t *testing.T) {
	for _, path := range []string{
		corecontract.ExecutionPathPrefix,
		corecontract.ExecutionPathPrefix + testExecutionID,
		corecontract.ExecutionPathPrefix + testExecutionID + "/operations",
		corecontract.ExecutionPathPrefix + testExecutionID + "/operations/" + testOperationID,
		corecontract.ExecutionPathPrefix + testExecutionID + "/operations/" + testOperationID + ":future",
		corecontract.ExecutionPathPrefix + testExecutionID + "/operations/" + testOperationID + ":complete/extra",
	} {
		if _, _, _, ok := parseExecutionAction(path); ok {
			t.Errorf("parseExecutionAction(%q) unexpectedly succeeded", path)
		}
	}
}

type recordingExecutionAuthorizer struct {
	action string
	err    error
}

func (authorizer *recordingExecutionAuthorizer) AuthorizeWorkload(_ *http.Request, action string) error {
	authorizer.action = action
	return authorizer.err
}

type recordingExecutionCommands struct {
	call string
}

func (commands *recordingExecutionCommands) PrepareExecution(context.Context, corecontract.PrepareExecutionRequest) (corecontract.PrepareExecutionResponse, error) {
	commands.call = "prepare-execution"
	return corecontract.PrepareExecutionResponse{}, nil
}

func (commands *recordingExecutionCommands) PrepareOperation(context.Context, corecontract.PrepareOperationRequest) (corecontract.PrepareOperationResponse, error) {
	commands.call = "prepare-operation"
	return corecontract.PrepareOperationResponse{}, nil
}

func (commands *recordingExecutionCommands) BeginOperationDispatch(context.Context, corecontract.BeginOperationDispatchRequest) (corecontract.BeginOperationDispatchResponse, error) {
	commands.call = "begin-dispatch"
	return corecontract.BeginOperationDispatchResponse{}, nil
}

func (commands *recordingExecutionCommands) AcknowledgeOperation(context.Context, corecontract.AcknowledgeOperationRequest) (corecontract.AcknowledgeOperationResponse, error) {
	commands.call = "acknowledge"
	return corecontract.AcknowledgeOperationResponse{}, nil
}

func (commands *recordingExecutionCommands) CompleteOperation(context.Context, corecontract.CompleteOperationRequest) (corecontract.CompleteOperationResponse, error) {
	commands.call = "complete-operation"
	return corecontract.CompleteOperationResponse{}, nil
}

func (commands *recordingExecutionCommands) SkipOperation(context.Context, corecontract.SkipOperationRequest) (corecontract.SkipOperationResponse, error) {
	commands.call = "skip-operation"
	return corecontract.SkipOperationResponse{}, nil
}

func (commands *recordingExecutionCommands) CompleteExecution(context.Context, corecontract.CompleteExecutionRequest) (corecontract.CompleteExecutionResponse, error) {
	commands.call = "complete-execution"
	return corecontract.CompleteExecutionResponse{}, nil
}
