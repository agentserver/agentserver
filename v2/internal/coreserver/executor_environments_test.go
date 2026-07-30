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

func TestExecutorEnvironmentHandlerReturnsBoundedProjection(t *testing.T) {
	authorizer := &recordingEnvironmentAuthorizer{}
	queries := &recordingEnvironmentQueries{environments: []corecontract.ExecutorEnvironment{{
		EnvironmentID:        "60000000-0000-4000-8000-000000000006",
		ExecutorID:           "20000000-0000-4000-8000-000000000002",
		RootDescriptor:       json.RawMessage(`{"kind":"local","root":"/workspace"}`),
		Platform:             "linux-arm64",
		InsecureDev:          true,
		EnvironmentVersion:   3,
		ConnectionGeneration: 7,
	}}}
	handler, err := NewExecutorEnvironmentHandler(authorizer, queries)
	if err != nil {
		t.Fatal(err)
	}
	requestBody := []byte(`{"workspaceId":"40000000-0000-4000-8000-000000000004","executorId":"20000000-0000-4000-8000-000000000002"}`)
	request := httptest.NewRequest(http.MethodPost, corecontract.ListExecutorEnvironmentsPath, bytes.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("environment list status/body = %d/%s", response.Code, response.Body)
	}
	if authorizer.action != "executor-environments.list" {
		t.Fatalf("authorized action = %q", authorizer.action)
	}
	if queries.last.WorkspaceID != "40000000-0000-4000-8000-000000000004" || queries.last.ExecutorID != "20000000-0000-4000-8000-000000000002" {
		t.Fatalf("environment query = %+v", queries.last)
	}
	var result corecontract.ListExecutorEnvironmentsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Environments) != 1 || result.Environments[0].ConnectionGeneration != 7 || string(result.Environments[0].RootDescriptor) != `{"kind":"local","root":"/workspace"}` {
		t.Fatalf("environment response = %+v", result)
	}
}

func TestExecutorEnvironmentHandlerFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		authorizer *recordingEnvironmentAuthorizer
		method     string
		body       string
		wantStatus int
	}{
		{name: "authorization", authorizer: &recordingEnvironmentAuthorizer{err: errors.New("denied")}, method: http.MethodPost, body: `{}`, wantStatus: http.StatusForbidden},
		{name: "method", authorizer: &recordingEnvironmentAuthorizer{}, method: http.MethodGet, body: `{}`, wantStatus: http.StatusNotFound},
		{name: "unknown field", authorizer: &recordingEnvironmentAuthorizer{}, method: http.MethodPost, body: `{"workspaceId":"40000000-0000-4000-8000-000000000004","future":true}`, wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, err := NewExecutorEnvironmentHandler(test.authorizer, &recordingEnvironmentQueries{})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(test.method, corecontract.ListExecutorEnvironmentsPath, bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status/body = %d/%s, want %d", response.Code, response.Body, test.wantStatus)
			}
		})
	}
}

type recordingEnvironmentAuthorizer struct {
	action string
	err    error
}

func (authorizer *recordingEnvironmentAuthorizer) AuthorizeWorkload(_ *http.Request, action string) error {
	authorizer.action = action
	return authorizer.err
}

type recordingEnvironmentQueries struct {
	last         corecontract.ListExecutorEnvironmentsRequest
	environments []corecontract.ExecutorEnvironment
}

func (queries *recordingEnvironmentQueries) ListExecutorEnvironments(_ context.Context, request corecontract.ListExecutorEnvironmentsRequest) ([]corecontract.ExecutorEnvironment, error) {
	queries.last = request
	result := make([]corecontract.ExecutorEnvironment, len(queries.environments))
	copy(result, queries.environments)
	for index := range result {
		result[index].RootDescriptor = append(json.RawMessage(nil), result[index].RootDescriptor...)
	}
	return result, nil
}
