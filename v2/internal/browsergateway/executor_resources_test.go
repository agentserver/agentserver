package browsergateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const (
	executorResourceWorkspace = "71000000-0000-4000-8000-000000000002"
	executorResourceExecutor  = "71000000-0000-4000-8000-000000000003"
)

func TestExecutorResourceHandlerCreatesAndIssuesWithoutExposingBearerToOtherRoutes(t *testing.T) {
	now := time.Date(2026, 8, 2, 15, 0, 0, 0, time.UTC)
	backend := &recordingExecutorResourceBackend{
		createResponse: corecontract.CreateExecutorResourceResponse{
			Executor: executorResourceState(now, "enrolling"), Created: true,
		},
		issueResponse: corecontract.IssueExecutorEnrollmentTokenResponse{
			ExecutorID: executorResourceExecutor, Token: validExecutorEnrollmentBearer(),
			ExpiresAt: now.Add(10 * time.Minute), Created: true,
		},
	}
	handler, err := NewExecutorResourceHandler(backend, ExecutorResourceHandlerConfig{})
	if err != nil {
		t.Fatal(err)
	}

	create := httptest.NewRequest(
		http.MethodPost,
		corecontract.CreateExecutorResourcePath(executorResourceWorkspace),
		strings.NewReader(`{"executorId":"`+executorResourceExecutor+`"}`),
	)
	create.Header.Set("Authorization", "Bearer browser-user-token")
	create.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated || createResponse.Header().Get("Cache-Control") != "no-store" ||
		backend.createBearer != "browser-user-token" || backend.createWorkspace != executorResourceWorkspace ||
		backend.createInput.ExecutorID != executorResourceExecutor {
		t.Fatalf("create response/backend = %d %s / %+v", createResponse.Code, createResponse.Body.String(), backend)
	}

	issue := httptest.NewRequest(
		http.MethodPost,
		corecontract.IssueExecutorEnrollmentTokenPath(executorResourceWorkspace, executorResourceExecutor), nil,
	)
	issue.Header.Set("Authorization", "Bearer browser-user-token")
	issue.Header.Set("Idempotency-Key", "enroll-1")
	issueResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(issueResponse, issue)
	if issueResponse.Code != http.StatusCreated || issueResponse.Header().Get("Cache-Control") != "no-store" ||
		backend.issueBearer != "browser-user-token" || backend.issueWorkspace != executorResourceWorkspace ||
		backend.issueExecutor != executorResourceExecutor || backend.issueIdempotency != "enroll-1" {
		t.Fatalf("issue response/backend = %d %s / %+v", issueResponse.Code, issueResponse.Body.String(), backend)
	}
	if !strings.Contains(issueResponse.Body.String(), backend.issueResponse.Token) {
		t.Fatal("browser response omitted the one-time enrollment bearer")
	}
}

func TestExecutorResourceHandlerPreservesExpiredExactIdempotentReplay(t *testing.T) {
	backend := &recordingExecutorResourceBackend{issueResponse: corecontract.IssueExecutorEnrollmentTokenResponse{
		ExecutorID: executorResourceExecutor, Token: validExecutorEnrollmentBearer(),
		ExpiresAt: time.Date(2026, 8, 2, 1, 0, 0, 0, time.UTC), Created: false,
	}}
	handler, err := NewExecutorResourceHandler(backend, ExecutorResourceHandlerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		corecontract.IssueExecutorEnrollmentTokenPath(executorResourceWorkspace, executorResourceExecutor), nil,
	)
	request.Header.Set("Authorization", "Bearer browser-user-token")
	request.Header.Set("Idempotency-Key", "expired-exact-replay")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"created":false`) {
		t.Fatalf("expired exact replay response = %d %s", response.Code, response.Body.String())
	}
}

func TestExecutorResourceHandlerRejectsAmbiguousRequestsBeforeBackend(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		body   []byte
		mutate func(*http.Request)
		status int
	}{
		{name: "invalid workspace", path: corecontract.CreateExecutorResourcePath("not-a-workspace"), body: []byte(`{}`), status: http.StatusBadRequest},
		{name: "missing bearer", path: corecontract.CreateExecutorResourcePath(executorResourceWorkspace), body: []byte(`{"executorId":"` + executorResourceExecutor + `"}`), mutate: func(request *http.Request) {
			request.Header.Del("Authorization")
		}, status: http.StatusUnauthorized},
		{name: "duplicate bearer", path: corecontract.CreateExecutorResourcePath(executorResourceWorkspace), body: []byte(`{"executorId":"` + executorResourceExecutor + `"}`), mutate: func(request *http.Request) {
			request.Header.Add("Authorization", "Bearer second")
		}, status: http.StatusUnauthorized},
		{name: "unknown JSON", path: corecontract.CreateExecutorResourcePath(executorResourceWorkspace), body: []byte(`{"executorId":"` + executorResourceExecutor + `","future":true}`), status: http.StatusBadRequest},
		{name: "invalid executor", path: corecontract.CreateExecutorResourcePath(executorResourceWorkspace), body: []byte(`{"executorId":"invalid"}`), status: http.StatusBadRequest},
		{name: "token body", path: corecontract.IssueExecutorEnrollmentTokenPath(executorResourceWorkspace, executorResourceExecutor), body: []byte(`{}`), mutate: func(request *http.Request) {
			request.Header.Set("Idempotency-Key", "enroll-1")
		}, status: http.StatusBadRequest},
		{name: "token query", path: corecontract.IssueExecutorEnrollmentTokenPath(executorResourceWorkspace, executorResourceExecutor) + "?rotate=true", mutate: func(request *http.Request) {
			request.Header.Set("Idempotency-Key", "enroll-1")
		}, status: http.StatusBadRequest},
		{name: "missing idempotency", path: corecontract.IssueExecutorEnrollmentTokenPath(executorResourceWorkspace, executorResourceExecutor), status: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &recordingExecutorResourceBackend{}
			handler, err := NewExecutorResourceHandler(backend, ExecutorResourceHandlerConfig{})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer browser-user-token")
			request.Header.Set("Content-Type", "application/json")
			if test.mutate != nil {
				test.mutate(request)
			}
			response := httptest.NewRecorder()
			handler.Routes().ServeHTTP(response, request)
			if response.Code != test.status || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("response = %d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
			if backend.calls != 0 {
				t.Fatalf("rejected request reached backend %d times", backend.calls)
			}
		})
	}
}

func TestExecutorResourceHandlerRejectsCoreScopeDriftAndHidesTransportFailures(t *testing.T) {
	now := time.Date(2026, 8, 2, 15, 30, 0, 0, time.UTC)
	backend := &recordingExecutorResourceBackend{createResponse: corecontract.CreateExecutorResourceResponse{
		Executor: executorResourceState(now, "enrolling"), Created: true,
	}}
	backend.createResponse.Executor.WorkspaceID = "72000000-0000-4000-8000-000000000002"
	handler, _ := NewExecutorResourceHandler(backend, ExecutorResourceHandlerConfig{})
	request := httptest.NewRequest(http.MethodPost, corecontract.CreateExecutorResourcePath(executorResourceWorkspace), strings.NewReader(`{"executorId":"`+executorResourceExecutor+`"}`))
	request.Header.Set("Authorization", "Bearer browser-user-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "backend_contract_error") {
		t.Fatalf("scope drift response = %d %s", response.Code, response.Body.String())
	}

	backend.createErr = errors.New("internal mTLS key path detail")
	request = httptest.NewRequest(http.MethodPost, corecontract.CreateExecutorResourcePath(executorResourceWorkspace), strings.NewReader(`{"executorId":"`+executorResourceExecutor+`"}`))
	request.Header.Set("Authorization", "Bearer browser-user-token")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "detail") {
		t.Fatalf("transport failure response = %d %s", response.Code, response.Body.String())
	}
}

func executorResourceState(now time.Time, status string) corecontract.ExecutorResourceState {
	return corecontract.ExecutorResourceState{
		ExecutorID: executorResourceExecutor, WorkspaceID: executorResourceWorkspace,
		Status: status, Version: 1, CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}
}

func validExecutorEnrollmentBearer() string {
	return "asv2enr1." + base64.RawURLEncoding.EncodeToString([]byte(`{"v":1}`)) + "." +
		base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
}

type recordingExecutorResourceBackend struct {
	calls            int
	createBearer     string
	createWorkspace  string
	createInput      corecontract.CreateExecutorResourceRequest
	createResponse   corecontract.CreateExecutorResourceResponse
	createErr        error
	issueBearer      string
	issueWorkspace   string
	issueExecutor    string
	issueIdempotency string
	issueResponse    corecontract.IssueExecutorEnrollmentTokenResponse
	issueErr         error
}

func (backend *recordingExecutorResourceBackend) CreateExecutorResource(_ context.Context, bearer, workspaceID string, input corecontract.CreateExecutorResourceRequest) (corecontract.CreateExecutorResourceResponse, error) {
	backend.calls++
	backend.createBearer, backend.createWorkspace, backend.createInput = bearer, workspaceID, input
	return backend.createResponse, backend.createErr
}

func (backend *recordingExecutorResourceBackend) ListExecutorResources(_ context.Context, _, _ string) (corecontract.ListExecutorResourcesResponse, error) {
	return corecontract.ListExecutorResourcesResponse{}, nil
}

func (backend *recordingExecutorResourceBackend) ArchiveExecutorResource(_ context.Context, _, _, _ string) (corecontract.ArchiveExecutorResourceResponse, error) {
	return corecontract.ArchiveExecutorResourceResponse{}, nil
}

func (backend *recordingExecutorResourceBackend) IssueExecutorEnrollmentToken(_ context.Context, bearer, workspaceID, executorID, idempotencyKey string) (corecontract.IssueExecutorEnrollmentTokenResponse, error) {
	backend.calls++
	backend.issueBearer, backend.issueWorkspace = bearer, workspaceID
	backend.issueExecutor, backend.issueIdempotency = executorID, idempotencyKey
	return backend.issueResponse, backend.issueErr
}
