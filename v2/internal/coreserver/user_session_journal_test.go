package coreserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

type recordingJournalCommands struct {
	recordingUserSessionCommands
	actor, runID string
	after        int64
}

func (c *recordingJournalCommands) GetJournal(_ context.Context, workspaceID, sessionID, actorID, runID string, after int64) (corecontract.UserSessionJournalPage, error) {
	c.actor, c.runID, c.after = actorID, runID, after
	return corecontract.UserSessionJournalPage{Session: corecontract.UserSessionState{WorkspaceID: workspaceID, SessionID: sessionID}}, nil
}
func TestSessionJournalRequiresUserAndWorkloadTranscriptAuthority(t *testing.T) {
	c := &recordingJournalCommands{}
	workload := &recordingRunAttemptAuthorizer{}
	users := &recordingUserAuthorizer{actorID: userSessionTestActor}
	handler, err := NewUserSessionHandler(workload, users, c)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, corecontract.UserSessionJournalPath(userSessionTestWorkspace, userSessionTestSession)+"?runId="+userRunID+"&after=128", nil)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || users.action != "sessions.transcript" || workload.action != "sessions.transcript" || c.actor != userSessionTestActor || c.after != 128 || c.runID != userRunID {
		t.Fatalf("journal response=%d %s authority=%s/%s command=%+v", response.Code, response.Body, users.action, workload.action, c)
	}
	for _, query := range []string{"after=-1", "after=1", "after=0&after=1", "bad=1", "after=%zz"} {
		request = httptest.NewRequest(http.MethodGet, corecontract.UserSessionJournalPath(userSessionTestWorkspace, userSessionTestSession)+"?"+query, nil)
		response = httptest.NewRecorder()
		handler.Routes().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("query %q: %d", query, response.Code)
		}
	}
}
