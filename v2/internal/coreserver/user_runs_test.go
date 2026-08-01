package coreserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

type recordingUserAuthorizer struct {
	actorID string
	err     error
	calls   int
	action  string
}

func (authorizer *recordingUserAuthorizer) AuthorizeUser(_ *http.Request, action string) (string, error) {
	authorizer.calls++
	authorizer.action = action
	return authorizer.actorID, authorizer.err
}

type recordingUserRunCommands struct {
	createResult corecontract.CreateUserRunResponse
	createErr    error
	cancelResult corecontract.CancelUserRunResponse
	cancelErr    error
	readResult   corecontract.ReadUserRunEventsResponse
	readErr      error

	create CreateUserRunCommand
	cancel CancelUserRunCommand
	read   ReadUserRunEventsQuery
}

func (commands *recordingUserRunCommands) CreateUserRun(_ context.Context, command CreateUserRunCommand) (corecontract.CreateUserRunResponse, error) {
	commands.create = command
	return commands.createResult, commands.createErr
}

func (commands *recordingUserRunCommands) CancelUserRun(_ context.Context, command CancelUserRunCommand) (corecontract.CancelUserRunResponse, error) {
	commands.cancel = command
	return commands.cancelResult, commands.cancelErr
}

func (commands *recordingUserRunCommands) ReadUserRunEvents(_ context.Context, query ReadUserRunEventsQuery) (corecontract.ReadUserRunEventsResponse, error) {
	commands.read = query
	return commands.readResult, commands.readErr
}

func TestUserRunHandlerCreatesRunWithBothAuthorizationLayers(t *testing.T) {
	commands := &recordingUserRunCommands{createResult: corecontract.CreateUserRunResponse{
		WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, RunID: userRunID,
		CreatedAt: time.Date(2026, 7, 31, 16, 0, 0, 0, time.UTC), Cursor: "v1.cursor", LastEventSequence: 1, Created: true,
	}}
	users := &recordingUserAuthorizer{actorID: userRunActorID}
	handler, err := NewUserRunHandler(&recordingRunAttemptAuthorizer{}, users, commands)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, corecontract.CreateUserRunPath(userRunWorkspaceID, userRunSessionID), strings.NewReader(`{"clientRunId":"client-1","prompt":"hello"}`))
	request.Header.Set("Authorization", "Bearer user-token")
	request.Header.Set("Idempotency-Key", "request-1")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusCreated || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if users.calls != 1 || commands.create.ActorID != userRunActorID || commands.create.IdempotencyKey != "request-1" || commands.create.Prompt != "hello" {
		t.Fatalf("CreateUserRun command = %+v, user calls = %d", commands.create, users.calls)
	}
}

func TestUserRunHandlerForwardsOnlyExplicitEmptyCancelCommand(t *testing.T) {
	commands := &recordingUserRunCommands{cancelResult: corecontract.CancelUserRunResponse{
		WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, RunID: userRunID,
		Status: "cancelling", RunVersion: 4, Terminal: false, Changed: true,
	}}
	workload := &recordingRunAttemptAuthorizer{}
	users := &recordingUserAuthorizer{actorID: userRunActorID}
	handler, err := NewUserRunHandler(workload, users, commands)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, corecontract.CancelUserRunPath(userRunWorkspaceID, userRunID), nil)
	request.Header.Set("Authorization", "Bearer user-token")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cancel response = %d %s", response.Code, response.Body.String())
	}
	if workload.action != "runs.cancel" || users.action != "runs.cancel" || users.calls != 1 ||
		commands.cancel.ActorID != userRunActorID || commands.cancel.WorkspaceID != userRunWorkspaceID || commands.cancel.RunID != userRunID {
		t.Fatalf("cancel authority/command = %q / %q / %+v", workload.action, users.action, commands.cancel)
	}
	var result corecontract.CancelUserRunResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Status != "cancelling" || !result.Changed {
		t.Fatalf("cancel response body = %+v, %v", result, err)
	}

	for _, suffix := range []string{"?force=true", "#body"} {
		commands.cancel = CancelUserRunCommand{}
		path := corecontract.CancelUserRunPath(userRunWorkspaceID, userRunID)
		var body io.Reader
		if suffix == "#body" {
			body = strings.NewReader(`{}`)
		} else {
			path += suffix
		}
		request := httptest.NewRequest(http.MethodPost, path, body)
		request.Header.Set("Authorization", "Bearer user-token")
		response := httptest.NewRecorder()
		handler.Routes().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || commands.cancel.ActorID != "" {
			t.Fatalf("invalid cancel %q = %d %s; command=%+v", suffix, response.Code, response.Body.String(), commands.cancel)
		}
	}
}

func TestUserRunHandlerReadsLongPollParametersAndCursorExpiry(t *testing.T) {
	commands := &recordingUserRunCommands{readErr: &UserRunCursorExpiredError{Response: corecontract.UserRunCursorExpiredResponse{
		Code: "cursor_expired", Message: "expired",
		Snapshot: corecontract.UserRunSnapshot{
			WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, RunID: userRunID,
			Status: "running", RunVersion: 4, LastEventSequence: 9, State: []byte(`{"messages":[]}`), UpdatedAt: time.Now(),
		},
		RebaseCursor: "v1.rebase", LastEventSequence: 5,
	}}}
	handler, _ := NewUserRunHandler(&recordingRunAttemptAuthorizer{}, &recordingUserAuthorizer{actorID: userRunActorID}, commands)
	path := corecontract.ReadUserRunEventsPath(userRunWorkspaceID, userRunID) + "?after=v1.old&limit=64&waitMs=2500"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer user-token")
	response := httptest.NewRecorder()

	handler.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusGone || commands.read.After != "v1.old" || commands.read.Limit != 64 || commands.read.Wait != 2500*time.Millisecond {
		t.Fatalf("response = %d %s; query = %+v", response.Code, response.Body.String(), commands.read)
	}
	var expired corecontract.UserRunCursorExpiredResponse
	if err := json.Unmarshal(response.Body.Bytes(), &expired); err != nil || expired.RebaseCursor != "v1.rebase" || expired.LastEventSequence != 5 {
		t.Fatalf("expired response = %+v, error = %v", expired, err)
	}
}

func TestUserRunHandlerFailsClosedBeforeCommands(t *testing.T) {
	for _, test := range []struct {
		name     string
		workload *recordingRunAttemptAuthorizer
		users    *recordingUserAuthorizer
		mutate   func(*http.Request)
		status   int
	}{
		{name: "workload denied", workload: &recordingRunAttemptAuthorizer{err: errors.New("denied")}, users: &recordingUserAuthorizer{actorID: userRunActorID}, status: http.StatusForbidden},
		{name: "user invalid", workload: &recordingRunAttemptAuthorizer{}, users: &recordingUserAuthorizer{err: ErrInvalidUserAccessToken}, status: http.StatusUnauthorized},
		{name: "user auth unavailable", workload: &recordingRunAttemptAuthorizer{}, users: &recordingUserAuthorizer{err: ErrUserAuthUnavailable}, status: http.StatusServiceUnavailable},
		{name: "missing idempotency", workload: &recordingRunAttemptAuthorizer{}, users: &recordingUserAuthorizer{actorID: userRunActorID}, mutate: func(request *http.Request) { request.Header.Del("Idempotency-Key") }, status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			commands := &recordingUserRunCommands{}
			handler, _ := NewUserRunHandler(test.workload, test.users, commands)
			request := httptest.NewRequest(http.MethodPost, corecontract.CreateUserRunPath(userRunWorkspaceID, userRunSessionID), strings.NewReader(`{"prompt":"hello"}`))
			request.Header.Set("Authorization", "Bearer user-token")
			request.Header.Set("Idempotency-Key", "request-1")
			request.Header.Set("Content-Type", "application/json")
			if test.mutate != nil {
				test.mutate(request)
			}
			response := httptest.NewRecorder()
			handler.Routes().ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
			if commands.create.ActorID != "" {
				t.Fatalf("command ran after rejection: %+v", commands.create)
			}
			if test.name == "workload denied" && test.users.calls != 0 {
				t.Fatal("user token was inspected before workload authorization")
			}
		})
	}
}

func TestUserRunHandlerRejectsAmbiguousEventQuery(t *testing.T) {
	commands := &recordingUserRunCommands{}
	handler, _ := NewUserRunHandler(&recordingRunAttemptAuthorizer{}, &recordingUserAuthorizer{actorID: userRunActorID}, commands)
	request := httptest.NewRequest(http.MethodGet, corecontract.ReadUserRunEventsPath(userRunWorkspaceID, userRunID)+"?after=a&after=b", nil)
	request.Header.Set("Authorization", "Bearer user-token")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || commands.read.ActorID != "" {
		t.Fatalf("response = %d %s; command = %+v", response.Code, response.Body.String(), commands.read)
	}
}
