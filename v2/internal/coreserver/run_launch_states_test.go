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

func TestRunLaunchStateHandlerAuthorizesAndRoutesExactQuery(t *testing.T) {
	authorizer := &recordingRunAttemptAuthorizer{}
	queries := &recordingRunLaunchStateQueries{}
	handler, err := NewRunLaunchStateHandler(authorizer, queries)
	if err != nil {
		t.Fatal(err)
	}
	query := corecontract.ResolveRunLaunchStateRequest{
		WorkspaceID: "40000000-0000-4000-8000-000000000004", SessionID: "43000000-0000-4000-8000-000000000004",
		RunID: testRunID, RunAttemptID: testRunAttemptID, HolderID: "pool-holder", RunAttemptGeneration: 3,
		ExpectedRunVersion: 2, ExpectedRunAttemptVersion: 1,
	}
	raw, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, corecontract.ResolveRunLaunchStatePath, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || authorizer.action != "run-launch-states.resolve" || queries.request != query {
		t.Fatalf("status/action/query/body = %d/%q/%+v/%s", response.Code, authorizer.action, queries.request, response.Body)
	}
}

func TestRunLaunchStateHandlerFailsClosed(t *testing.T) {
	queries := &recordingRunLaunchStateQueries{}
	handler, err := NewRunLaunchStateHandler(&recordingRunAttemptAuthorizer{err: errors.New("denied")}, queries)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, corecontract.ResolveRunLaunchStatePath, bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || queries.calls != 0 {
		t.Fatalf("denied status/calls/body = %d/%d/%s", response.Code, queries.calls, response.Body)
	}

	handler, err = NewRunLaunchStateHandler(&recordingRunAttemptAuthorizer{}, queries)
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, corecontract.ResolveRunLaunchStatePath, nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || queries.calls != 0 {
		t.Fatalf("wrong-method status/calls/body = %d/%d/%s", response.Code, queries.calls, response.Body)
	}
}

type recordingRunLaunchStateQueries struct {
	request corecontract.ResolveRunLaunchStateRequest
	calls   int
}

func (queries *recordingRunLaunchStateQueries) ResolveRunLaunchState(_ context.Context, request corecontract.ResolveRunLaunchStateRequest) (corecontract.ResolveRunLaunchStateResponse, error) {
	queries.request = request
	queries.calls++
	return corecontract.ResolveRunLaunchStateResponse{}, nil
}
