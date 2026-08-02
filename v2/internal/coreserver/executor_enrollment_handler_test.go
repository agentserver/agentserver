package coreserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

func TestUserExecutorManagementHandlerCreatesAndIssuesUnderSeparateCommands(t *testing.T) {
	now := time.Date(2026, 8, 2, 13, 0, 0, 0, time.UTC)
	commands := &recordingExecutorManagementCommands{
		createResult: corecontract.CreateExecutorResourceResponse{
			Executor: corecontract.ExecutorResourceState{
				ExecutorID: enrollmentTestExecutor, WorkspaceID: enrollmentTestWorkspace,
				Status: coredb.ExecutorStatusEnrolling, Version: 1, CreatedAt: now, UpdatedAt: now,
			},
			Created: true,
		},
		issueResult: corecontract.IssueExecutorEnrollmentTokenResponse{
			ExecutorID: enrollmentTestExecutor, Token: "asv2enr1.claims.mac",
			ExpiresAt: now.Add(10 * time.Minute), Created: true,
		},
	}
	workload := &identityCapabilityAuthorizer{identity: "browser-gateway"}
	users := &recordingUserAuthorizer{actorID: enrollmentTestActor}
	handler, err := NewUserExecutorManagementHandler(workload, users, commands)
	if err != nil {
		t.Fatal(err)
	}

	create := httptest.NewRequest(
		http.MethodPost,
		corecontract.CreateExecutorResourcePath(enrollmentTestWorkspace),
		strings.NewReader(`{"executorId":"`+enrollmentTestExecutor+`"}`),
	)
	create.Header.Set("Authorization", "Bearer user-token")
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set("X-Test-Identity", "browser-gateway")
	createResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated || createResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create executor response = %d %s", createResponse.Code, createResponse.Body.String())
	}
	if commands.createActor != enrollmentTestActor || commands.createWorkspace != enrollmentTestWorkspace ||
		commands.createExecutor != enrollmentTestExecutor || workload.actions[0] != "executors.create" ||
		users.action != "executors.create" {
		t.Fatalf("create authority/command = %+v / %q / %q / %q", workload.actions, commands.createActor, commands.createWorkspace, commands.createExecutor)
	}

	issue := httptest.NewRequest(
		http.MethodPost,
		corecontract.IssueExecutorEnrollmentTokenPath(enrollmentTestWorkspace, enrollmentTestExecutor),
		nil,
	)
	issue.Header.Set("Authorization", "Bearer user-token")
	issue.Header.Set("Idempotency-Key", "enrollment-request-1")
	issue.Header.Set("X-Test-Identity", "browser-gateway")
	issueResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(issueResponse, issue)
	if issueResponse.Code != http.StatusCreated || issueResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("issue enrollment token response = %d %s", issueResponse.Code, issueResponse.Body.String())
	}
	if commands.issueActor != enrollmentTestActor || commands.issueWorkspace != enrollmentTestWorkspace ||
		commands.issueExecutor != enrollmentTestExecutor || commands.issueIdempotency != "enrollment-request-1" ||
		workload.actions[1] != "executors.enrollment-token.issue" || users.action != "executors.enrollment-token.issue" {
		t.Fatalf("issue authority/command = %+v / %q / %q / %q / %q", workload.actions, commands.issueActor, commands.issueWorkspace, commands.issueExecutor, commands.issueIdempotency)
	}
	var issued corecontract.IssueExecutorEnrollmentTokenResponse
	if err := json.Unmarshal(issueResponse.Body.Bytes(), &issued); err != nil || issued.Token != commands.issueResult.Token {
		t.Fatalf("issue response body = %+v, %v", issued, err)
	}
}

func TestUserExecutorManagementHandlerFailsClosedBeforeCommands(t *testing.T) {
	validCreate := []byte(`{"executorId":"` + enrollmentTestExecutor + `"}`)
	tests := []struct {
		name     string
		path     string
		body     []byte
		workload *identityCapabilityAuthorizer
		users    *recordingUserAuthorizer
		mutate   func(*http.Request)
		status   int
	}{
		{
			name: "workload denied", path: corecontract.CreateExecutorResourcePath(enrollmentTestWorkspace), body: validCreate,
			workload: &identityCapabilityAuthorizer{identity: "another-workload"}, users: &recordingUserAuthorizer{actorID: enrollmentTestActor},
			status: http.StatusForbidden,
		},
		{
			name: "user denied", path: corecontract.CreateExecutorResourcePath(enrollmentTestWorkspace), body: validCreate,
			workload: &identityCapabilityAuthorizer{identity: "browser-gateway"}, users: &recordingUserAuthorizer{err: ErrInvalidUserAccessToken},
			status: http.StatusUnauthorized,
		},
		{
			name: "unknown create field", path: corecontract.CreateExecutorResourcePath(enrollmentTestWorkspace), body: []byte(`{"executorId":"` + enrollmentTestExecutor + `","future":true}`),
			workload: &identityCapabilityAuthorizer{identity: "browser-gateway"}, users: &recordingUserAuthorizer{actorID: enrollmentTestActor},
			status: http.StatusBadRequest,
		},
		{
			name: "create query", path: corecontract.CreateExecutorResourcePath(enrollmentTestWorkspace) + "?force=true", body: validCreate,
			workload: &identityCapabilityAuthorizer{identity: "browser-gateway"}, users: &recordingUserAuthorizer{actorID: enrollmentTestActor},
			status: http.StatusBadRequest,
		},
		{
			name: "token body", path: corecontract.IssueExecutorEnrollmentTokenPath(enrollmentTestWorkspace, enrollmentTestExecutor), body: []byte(`{}`),
			workload: &identityCapabilityAuthorizer{identity: "browser-gateway"}, users: &recordingUserAuthorizer{actorID: enrollmentTestActor},
			mutate: func(request *http.Request) { request.Header.Set("Idempotency-Key", "request-1") }, status: http.StatusBadRequest,
		},
		{
			name: "missing token idempotency", path: corecontract.IssueExecutorEnrollmentTokenPath(enrollmentTestWorkspace, enrollmentTestExecutor),
			workload: &identityCapabilityAuthorizer{identity: "browser-gateway"}, users: &recordingUserAuthorizer{actorID: enrollmentTestActor},
			status: http.StatusBadRequest,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commands := &recordingExecutorManagementCommands{}
			handler, err := NewUserExecutorManagementHandler(test.workload, test.users, commands)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer user-token")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Test-Identity", "browser-gateway")
			if test.mutate != nil {
				test.mutate(request)
			}
			response := httptest.NewRecorder()
			handler.Routes().ServeHTTP(response, request)
			if response.Code != test.status || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("response = %d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
			if commands.calls != 0 {
				t.Fatalf("rejected request reached commands: %d", commands.calls)
			}
			if test.name == "workload denied" && test.users.calls != 0 {
				t.Fatal("user bearer was inspected before workload identity")
			}
		})
	}
}

func TestInternalExecutorIdentityHandlerSeparatesEnrollmentAndOAuthBearers(t *testing.T) {
	now := time.Date(2026, 8, 2, 14, 0, 0, 0, time.UTC)
	enrollment := &recordingInternalEnrollmentCommands{response: corecontract.CompleteExecutorEnrollmentResponse{
		Executor: corecontract.ExecutorResourceState{
			ExecutorID: enrollmentTestExecutor, WorkspaceID: enrollmentTestWorkspace,
			Status: coredb.ExecutorStatusOffline, Version: 3, CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
		},
		OAuthClientID: "agentserver-executor-" + enrollmentTestExecutor,
		Audience:      ExecutorOAuthAudience, Scope: ExecutorOAuthScope,
	}}
	connections := &recordingInternalExecutorConnectionAuthorizer{response: corecontract.AuthorizeExecutorConnectionResponse{
		ExecutorID: enrollmentTestExecutor, WorkspaceID: enrollmentTestWorkspace,
		OAuthClientID:           "agentserver-executor-" + enrollmentTestExecutor,
		MachinePublicKeyEd25519: strings.Repeat("a", 43), MachineKeySHA256: strings.Repeat("b", 64),
		ExecutorVersion: 3, TokenExpiresAt: now.Add(5 * time.Minute), AuthorizedAt: now,
	}}
	workload := &identityCapabilityAuthorizer{identity: "executor-gateway"}
	handler, err := NewInternalExecutorIdentityHandler(workload, enrollment, connections)
	if err != nil {
		t.Fatal(err)
	}

	complete := httptest.NewRequest(http.MethodPost, corecontract.CompleteExecutorEnrollmentPath, strings.NewReader(`{
		"machinePublicKeyEd25519":"key","machineProofEd25519":"proof","agentxVersion":"1",
		"runtimeManifestSha256":"runtime","execProtocolSourceSha256":"protocol","environments":[]
	}`))
	complete.Header.Set("Authorization", "Bearer enrollment-bearer")
	complete.Header.Set("Content-Type", "application/json")
	complete.Header.Set("X-Test-Identity", "executor-gateway")
	completeResponse := httptest.NewRecorder()
	handler.ServeHTTP(completeResponse, complete)
	if completeResponse.Code != http.StatusOK || len(enrollment.calls) != 1 ||
		enrollment.bearers[0] != "enrollment-bearer" || len(connections.bearers) != 0 ||
		workload.actions[0] != "executor-enrollments.complete" {
		t.Fatalf("complete response/calls = %d / %q / %q / %+v", completeResponse.Code, enrollment.bearers, connections.bearers, workload.actions)
	}

	authorize := httptest.NewRequest(http.MethodPost, corecontract.AuthorizeExecutorConnectionPath, nil)
	authorize.Header.Set("Authorization", "Bearer oauth-bearer")
	authorize.Header.Set("X-Test-Identity", "executor-gateway")
	authorizeResponse := httptest.NewRecorder()
	handler.ServeHTTP(authorizeResponse, authorize)
	if authorizeResponse.Code != http.StatusOK || len(connections.bearers) != 1 || connections.bearers[0] != "oauth-bearer" ||
		len(enrollment.calls) != 1 || workload.actions[1] != "executor-connections.authorize" {
		t.Fatalf("authorize response/calls = %d / %q / %d / %+v", authorizeResponse.Code, connections.bearers, len(enrollment.calls), workload.actions)
	}
	for _, response := range []*httptest.ResponseRecorder{completeResponse, authorizeResponse} {
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("executor identity response omitted Cache-Control: no-store")
		}
	}
}

func TestInternalExecutorIdentityHandlerRejectsAmbiguousAuthorityBeforeServices(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		body   []byte
		mutate func(*http.Request)
		status int
	}{
		{name: "wrong workload", path: corecontract.AuthorizeExecutorConnectionPath, mutate: func(request *http.Request) {
			request.Header.Set("X-Test-Identity", "pool")
		}, status: http.StatusForbidden},
		{name: "missing enrollment bearer", path: corecontract.CompleteExecutorEnrollmentPath, body: []byte(`{}`), mutate: func(request *http.Request) {
			request.Header.Del("Authorization")
		}, status: http.StatusUnauthorized},
		{name: "duplicate OAuth bearer", path: corecontract.AuthorizeExecutorConnectionPath, mutate: func(request *http.Request) {
			request.Header.Add("Authorization", "Bearer second")
		}, status: http.StatusForbidden},
		{name: "OAuth request body", path: corecontract.AuthorizeExecutorConnectionPath, body: []byte(`{}`), status: http.StatusBadRequest},
		{name: "enrollment unknown field", path: corecontract.CompleteExecutorEnrollmentPath, body: []byte(`{"future":true}`), status: http.StatusBadRequest},
		{name: "enrollment duplicate field", path: corecontract.CompleteExecutorEnrollmentPath, body: []byte(`{"agentxVersion":"one","agentxVersion":"two"}`), status: http.StatusBadRequest},
		{name: "query", path: corecontract.AuthorizeExecutorConnectionPath + "?cached=true", status: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			enrollment := &recordingInternalEnrollmentCommands{}
			connections := &recordingInternalExecutorConnectionAuthorizer{}
			workload := &identityCapabilityAuthorizer{identity: "executor-gateway"}
			handler, err := NewInternalExecutorIdentityHandler(workload, enrollment, connections)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer authority")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Test-Identity", "executor-gateway")
			if test.mutate != nil {
				test.mutate(request)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("response = %d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
			if len(enrollment.calls) != 0 || len(connections.bearers) != 0 {
				t.Fatalf("rejected request reached services: %d/%d", len(enrollment.calls), len(connections.bearers))
			}
		})
	}
}

func TestInternalExecutorIdentityHandlerDoesNotExposeAuthorityFailures(t *testing.T) {
	workload := &identityCapabilityAuthorizer{identity: "executor-gateway"}
	enrollment := &recordingInternalEnrollmentCommands{}
	connections := &recordingInternalExecutorConnectionAuthorizer{}
	handler, err := NewInternalExecutorIdentityHandler(workload, enrollment, connections)
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []error{
		&coredb.StateError{Code: coredb.ErrorForbidden, Message: "revoked machine detail"},
		errors.New("hydra endpoint secret detail"),
	} {
		connections.err = failure
		request := httptest.NewRequest(http.MethodPost, corecontract.AuthorizeExecutorConnectionPath, nil)
		request.Header.Set("Authorization", "Bearer oauth-bearer")
		request.Header.Set("X-Test-Identity", "executor-gateway")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if strings.Contains(response.Body.String(), "detail") {
			t.Fatalf("authority failure was reflected: %s", response.Body.String())
		}
		want := http.StatusServiceUnavailable
		if _, ok := failure.(*coredb.StateError); ok {
			want = http.StatusForbidden
		}
		if response.Code != want {
			t.Fatalf("failure %T response = %d %s", failure, response.Code, response.Body.String())
		}
	}
}

type recordingExecutorManagementCommands struct {
	calls            int
	createActor      string
	createWorkspace  string
	createExecutor   string
	createResult     corecontract.CreateExecutorResourceResponse
	createErr        error
	issueActor       string
	issueWorkspace   string
	issueExecutor    string
	issueIdempotency string
	issueResult      corecontract.IssueExecutorEnrollmentTokenResponse
	issueErr         error
}

func (commands *recordingExecutorManagementCommands) CreateExecutor(_ context.Context, actorID, workspaceID, executorID string) (corecontract.CreateExecutorResourceResponse, error) {
	commands.calls++
	commands.createActor, commands.createWorkspace, commands.createExecutor = actorID, workspaceID, executorID
	return commands.createResult, commands.createErr
}

func (commands *recordingExecutorManagementCommands) IssueEnrollmentToken(_ context.Context, actorID, workspaceID, executorID, idempotencyKey string) (corecontract.IssueExecutorEnrollmentTokenResponse, error) {
	commands.calls++
	commands.issueActor, commands.issueWorkspace = actorID, workspaceID
	commands.issueExecutor, commands.issueIdempotency = executorID, idempotencyKey
	return commands.issueResult, commands.issueErr
}

type recordingInternalEnrollmentCommands struct {
	bearers  []string
	calls    []corecontract.CompleteExecutorEnrollmentRequest
	response corecontract.CompleteExecutorEnrollmentResponse
	err      error
}

func (commands *recordingInternalEnrollmentCommands) CompleteEnrollment(_ context.Context, bearer string, request corecontract.CompleteExecutorEnrollmentRequest) (corecontract.CompleteExecutorEnrollmentResponse, error) {
	commands.bearers = append(commands.bearers, bearer)
	commands.calls = append(commands.calls, request)
	return commands.response, commands.err
}

type recordingInternalExecutorConnectionAuthorizer struct {
	bearers  []string
	response corecontract.AuthorizeExecutorConnectionResponse
	err      error
}

func (authorizer *recordingInternalExecutorConnectionAuthorizer) Authorize(_ context.Context, bearer string) (corecontract.AuthorizeExecutorConnectionResponse, error) {
	authorizer.bearers = append(authorizer.bearers, bearer)
	return authorizer.response, authorizer.err
}
