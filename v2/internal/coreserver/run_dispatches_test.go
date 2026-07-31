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

func TestRunDispatchHandlerRoutesAllCommands(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		command    any
		wantAction string
		wantCall   string
	}{
		{
			name: "claim", path: corecontract.ClaimRunDispatchesPath,
			command:    corecontract.ClaimRunDispatchesRequest{OwnerID: "pool-holder", Limit: 1, LockTTLMillis: 30_000, WaitTimeoutMillis: 0},
			wantAction: "run-dispatches.claim", wantCall: "claim",
		},
		{
			name: "complete", path: corecontract.CompleteRunDispatchPath(testRunDispatchID),
			command:    corecontract.CompleteRunDispatchRequest{RunID: testRunID, OwnerID: "pool-holder", ClaimGeneration: 2},
			wantAction: "run-dispatches.complete", wantCall: "complete",
		},
		{
			name: "release", path: corecontract.ReleaseRunDispatchPath(testRunDispatchID),
			command:    corecontract.ReleaseRunDispatchRequest{RunID: testRunID, OwnerID: "pool-holder", ClaimGeneration: 2, RetryAfterMillis: 1_000},
			wantAction: "run-dispatches.release", wantCall: "release",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authorizer := &recordingRunAttemptAuthorizer{}
			commands := &recordingRunDispatchCommands{}
			handler, err := NewRunDispatchHandler(authorizer, commands)
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
			if response.Code != http.StatusOK || authorizer.action != test.wantAction || commands.call != test.wantCall {
				t.Fatalf("status/action/call/body = %d/%q/%q/%s", response.Code, authorizer.action, commands.call, response.Body)
			}
		})
	}
}

func TestRunDispatchHandlerFailsClosedOnAuthorizationMethodAndAmbiguousPath(t *testing.T) {
	commands := &recordingRunDispatchCommands{}
	denied, err := NewRunDispatchHandler(&recordingRunAttemptAuthorizer{err: errors.New("denied")}, commands)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, corecontract.ClaimRunDispatchesPath, bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	denied.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || commands.call != "" {
		t.Fatalf("denied status/call/body = %d/%q/%s", response.Code, commands.call, response.Body)
	}

	allowed, err := NewRunDispatchHandler(&recordingRunAttemptAuthorizer{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, corecontract.ClaimRunDispatchesPath},
		{http.MethodPost, corecontract.RunDispatchPathPrefix},
		{http.MethodPost, corecontract.RunDispatchPathPrefix + testRunDispatchID},
		{http.MethodPost, corecontract.RunDispatchPathPrefix + testRunDispatchID + ":complete/extra"},
		{http.MethodPost, corecontract.RunDispatchPathPrefix + testRunDispatchID + ":unknown"},
	} {
		request = httptest.NewRequest(test.method, test.path, nil)
		response = httptest.NewRecorder()
		allowed.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Errorf("%s %s status/body = %d/%s", test.method, test.path, response.Code, response.Body)
		}
	}
}

type recordingRunDispatchCommands struct {
	call string
}

func (commands *recordingRunDispatchCommands) ClaimRunDispatches(context.Context, corecontract.ClaimRunDispatchesRequest) (corecontract.ClaimRunDispatchesResponse, error) {
	commands.call = "claim"
	return corecontract.ClaimRunDispatchesResponse{RunDispatches: []corecontract.RunDispatch{}}, nil
}

func (commands *recordingRunDispatchCommands) CompleteRunDispatch(context.Context, string, corecontract.CompleteRunDispatchRequest) (corecontract.CompleteRunDispatchResponse, error) {
	commands.call = "complete"
	return corecontract.CompleteRunDispatchResponse{}, nil
}

func (commands *recordingRunDispatchCommands) ReleaseRunDispatch(context.Context, string, corecontract.ReleaseRunDispatchRequest) (corecontract.ReleaseRunDispatchResponse, error) {
	commands.call = "release"
	return corecontract.ReleaseRunDispatchResponse{}, nil
}
