package coreserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const (
	userSessionTestWorkspace = "91000000-0000-4000-8000-000000000001"
	userSessionTestSession   = "92000000-0000-4000-8000-000000000002"
	userSessionTestActor     = "93000000-0000-4000-8000-000000000003"
)

func TestUserSessionHandlerCreatesAndListsWithDistinctActions(t *testing.T) {
	now := time.Date(2026, 8, 4, 4, 0, 0, 0, time.UTC)
	state := corecontract.UserSessionState{
		SessionID: userSessionTestSession, WorkspaceID: userSessionTestWorkspace,
		Title: "Inspect SG", Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	commands := &recordingUserSessionCommands{
		listResult:   corecontract.ListUserSessionsResponse{Sessions: []corecontract.UserSessionState{state}},
		createResult: corecontract.CreateUserSessionResponse{Session: state, Created: true},
	}
	workload := &recordingRunAttemptAuthorizer{}
	users := &recordingUserAuthorizer{actorID: userSessionTestActor}
	handler, err := NewUserSessionHandler(workload, users, commands)
	if err != nil {
		t.Fatal(err)
	}

	create := httptest.NewRequest(http.MethodPost, corecontract.UserSessionsPath(userSessionTestWorkspace), strings.NewReader(
		`{"sessionId":"`+userSessionTestSession+`","title":"Inspect SG"}`,
	))
	create.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated || commands.createActor != userSessionTestActor ||
		commands.createWorkspace != userSessionTestWorkspace || commands.createInput.SessionID != userSessionTestSession ||
		users.action != "sessions.create" || workload.action != "sessions.create" {
		t.Fatalf("create session response/authority = %d %s / %+v / %q / %q", createResponse.Code, createResponse.Body.String(), commands.createInput, users.action, workload.action)
	}

	list := httptest.NewRequest(http.MethodGet, corecontract.UserSessionsPath(userSessionTestWorkspace), nil)
	listResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK || commands.listActor != userSessionTestActor || users.action != "sessions.list" || workload.action != "sessions.list" {
		t.Fatalf("list session response/authority = %d %s / %q / %q", listResponse.Code, listResponse.Body.String(), users.action, workload.action)
	}
}

func TestUserSessionHandlerRejectsUnknownMutationJSON(t *testing.T) {
	commands := &recordingUserSessionCommands{}
	handler, err := NewUserSessionHandler(&recordingRunAttemptAuthorizer{}, &recordingUserAuthorizer{actorID: userSessionTestActor}, commands)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, corecontract.UserSessionsPath(userSessionTestWorkspace), strings.NewReader(
		`{"sessionId":"`+userSessionTestSession+`","title":"Inspect SG","future":true}`,
	))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || commands.createCalls != 0 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unknown session JSON response = %d %s calls=%d", response.Code, response.Body.String(), commands.createCalls)
	}
}

func TestUserSessionHandlerReadsTranscriptWithCombinedAuthorityAction(t *testing.T) {
	now := time.Date(2026, 8, 5, 1, 0, 0, 0, time.UTC)
	commands := &recordingUserSessionCommands{transcriptResult: corecontract.GetUserSessionTranscriptResponse{
		WorkspaceID: userSessionTestWorkspace,
		SessionID:   userSessionTestSession,
		Messages: []corecontract.UserSessionTranscriptMessage{{
			MessageID: "user-" + userSessionTestSession, RunID: userSessionTestSession,
			Role: "user", Content: "hello", Complete: true, CreatedAt: now,
		}},
	}}
	workload := &recordingRunAttemptAuthorizer{}
	users := &recordingUserAuthorizer{actorID: userSessionTestActor}
	handler, err := NewUserSessionHandler(workload, users, commands)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		corecontract.UserSessionTranscriptPath(userSessionTestWorkspace, userSessionTestSession),
		nil,
	)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || commands.transcriptActor != userSessionTestActor ||
		commands.transcriptSession != userSessionTestSession || users.action != "sessions.transcript" ||
		workload.action != "sessions.transcript" || !strings.Contains(response.Body.String(), `"content":"hello"`) {
		t.Fatalf("transcript response/authority = %d %s / actor=%q session=%q actions=%q/%q",
			response.Code, response.Body.String(), commands.transcriptActor, commands.transcriptSession,
			users.action, workload.action,
		)
	}
}

type recordingUserSessionCommands struct {
	listResult        corecontract.ListUserSessionsResponse
	createResult      corecontract.CreateUserSessionResponse
	listActor         string
	createActor       string
	createWorkspace   string
	createInput       corecontract.CreateUserSessionRequest
	createCalls       int
	transcriptResult  corecontract.GetUserSessionTranscriptResponse
	transcriptActor   string
	transcriptSession string
}

func (commands *recordingUserSessionCommands) ListSessions(_ context.Context, _ string, actorID string) (corecontract.ListUserSessionsResponse, error) {
	commands.listActor = actorID
	return commands.listResult, nil
}

func (*recordingUserSessionCommands) GetSession(context.Context, string, string, string) (corecontract.UserSessionState, error) {
	return corecontract.UserSessionState{}, nil
}

func (commands *recordingUserSessionCommands) GetTranscript(_ context.Context, _ string, sessionID, actorID string) (corecontract.GetUserSessionTranscriptResponse, error) {
	commands.transcriptActor, commands.transcriptSession = actorID, sessionID
	return commands.transcriptResult, nil
}

func (commands *recordingUserSessionCommands) CreateSession(_ context.Context, workspaceID, actorID string, input corecontract.CreateUserSessionRequest) (corecontract.CreateUserSessionResponse, error) {
	commands.createCalls++
	commands.createWorkspace, commands.createActor, commands.createInput = workspaceID, actorID, input
	return commands.createResult, nil
}

func (*recordingUserSessionCommands) UpdateSession(context.Context, string, string, string, corecontract.UpdateUserSessionRequest) (corecontract.UpdateUserSessionResponse, error) {
	return corecontract.UpdateUserSessionResponse{}, nil
}

func (*recordingUserSessionCommands) ArchiveSession(context.Context, string, string, string, corecontract.ArchiveUserSessionRequest) (corecontract.ArchiveUserSessionResponse, error) {
	return corecontract.ArchiveUserSessionResponse{}, nil
}
