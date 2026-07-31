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

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const (
	testRunID        = "41000000-0000-4000-8000-000000000004"
	testRunAttemptID = "42000000-0000-4000-8000-000000000004"
)

func TestRunAttemptHandlerRoutesAllCommands(t *testing.T) {
	record := corecontract.TransitionRecord{
		EventID:            "71000000-0000-4000-8000-000000000001",
		ProducerInstanceID: "72000000-0000-4000-8000-000000000001",
		ProducerSeq:        1,
		OutboxID:           "73000000-0000-4000-8000-000000000001",
	}
	tests := []struct {
		name       string
		path       string
		command    any
		wantAction string
		wantCall   string
	}{
		{
			name: "claim", path: corecontract.ClaimRunAttemptPath,
			command: corecontract.ClaimRunAttemptRequest{
				RunID: testRunID, RunAttemptID: testRunAttemptID, HolderID: "pool-holder",
				ExpectedRunVersion: 1, LeaseTTLMillis: 30_000, Record: record,
			},
			wantAction: "run-attempts.claim", wantCall: "claim",
		},
		{
			name: "renew", path: corecontract.RenewRunAttemptPath(testRunAttemptID),
			command: corecontract.RenewRunAttemptRequest{
				SessionID: "40000000-0000-4000-8000-000000000004", RunID: testRunID,
				RunAttemptID: testRunAttemptID, HolderID: "pool-holder", RunAttemptGeneration: 3, LeaseTTLMillis: 30_000,
			},
			wantAction: "run-attempts.renew", wantCall: "renew",
		},
		{
			name: "turn accepted", path: corecontract.MarkTurnAcceptedPath(testRunAttemptID),
			command: corecontract.MarkTurnAcceptedRequest{
				RunID: testRunID, RunAttemptID: testRunAttemptID, HolderID: "pool-holder", RunAttemptGeneration: 3,
				ExpectedRunVersion: 2, ExpectedRunAttemptVersion: 1, Record: record,
			},
			wantAction: "run-attempts.turn-accepted", wantCall: "turn-accepted",
		},
		{
			name: "append events", path: corecontract.AppendAttemptEventsPath(testRunAttemptID),
			command: corecontract.AppendAttemptEventsRequest{
				RunID: testRunID, RunAttemptID: testRunAttemptID, HolderID: "pool-holder", RunAttemptGeneration: 3,
				OutboxID: record.OutboxID,
				Events: []corecontract.AttemptEvent{{
					EventID: record.EventID, ProducerInstanceID: record.ProducerInstanceID, ProducerSeq: 2,
					Source: "brain", Kind: "model.delta", SchemaVersion: 1, Payload: json.RawMessage(`{"text":"ok"}`),
				}},
			},
			wantAction: "run-attempts.events.append", wantCall: "append-events",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authorizer := &recordingRunAttemptAuthorizer{}
			commands := &recordingRunAttemptCommands{}
			handler, err := NewRunAttemptHandler(authorizer, commands)
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

func TestRunAttemptHandlerRejectsMismatchedPathAndFailsClosed(t *testing.T) {
	commands := &recordingRunAttemptCommands{}
	handler, err := NewRunAttemptHandler(&recordingRunAttemptAuthorizer{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	command := corecontract.RenewRunAttemptRequest{RunAttemptID: testRunAttemptID}
	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	otherAttempt := "42000000-0000-4000-8000-000000000099"
	request := httptest.NewRequest(http.MethodPost, corecontract.RenewRunAttemptPath(otherAttempt), bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || commands.call != "" {
		t.Fatalf("mismatched path status/call/body = %d/%q/%s", response.Code, commands.call, response.Body)
	}

	denied, err := NewRunAttemptHandler(&recordingRunAttemptAuthorizer{err: errors.New("denied")}, commands)
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, corecontract.ClaimRunAttemptPath, bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	denied.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || commands.call != "" {
		t.Fatalf("denied status/call/body = %d/%q/%s", response.Code, commands.call, response.Body)
	}

	request = httptest.NewRequest(http.MethodGet, corecontract.ClaimRunAttemptPath, nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("method status/body = %d/%s", response.Code, response.Body)
	}
}

func TestParseRunAttemptActionRejectsAmbiguousPaths(t *testing.T) {
	for _, path := range []string{
		corecontract.RunAttemptPathPrefix,
		corecontract.RunAttemptPathPrefix + testRunAttemptID,
		corecontract.RunAttemptPathPrefix + testRunAttemptID + ":future",
		corecontract.RunAttemptPathPrefix + testRunAttemptID + "/events",
		corecontract.RunAttemptPathPrefix + testRunAttemptID + "/events:append/extra",
	} {
		if _, _, ok := parseRunAttemptAction(path); ok {
			t.Errorf("parseRunAttemptAction(%q) unexpectedly succeeded", path)
		}
	}
}

func TestRunAttemptHandlerBoundsEventBody(t *testing.T) {
	handler, err := NewRunAttemptHandler(&recordingRunAttemptAuthorizer{}, &recordingRunAttemptCommands{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, corecontract.AppendAttemptEventsPath(testRunAttemptID), strings.NewReader(strings.Repeat(" ", maxRunAttemptEventCommandBytes+1)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized status/body = %d/%s", response.Code, response.Body)
	}
}

type recordingRunAttemptAuthorizer struct {
	action string
	err    error
}

func (authorizer *recordingRunAttemptAuthorizer) AuthorizeWorkload(_ *http.Request, action string) error {
	authorizer.action = action
	return authorizer.err
}

type recordingRunAttemptCommands struct {
	call string
}

func (commands *recordingRunAttemptCommands) ClaimRunAttempt(context.Context, corecontract.ClaimRunAttemptRequest) (corecontract.ClaimRunAttemptResponse, error) {
	commands.call = "claim"
	return corecontract.ClaimRunAttemptResponse{}, nil
}

func (commands *recordingRunAttemptCommands) RenewRunAttempt(context.Context, corecontract.RenewRunAttemptRequest) (corecontract.RenewRunAttemptResponse, error) {
	commands.call = "renew"
	return corecontract.RenewRunAttemptResponse{}, nil
}

func (commands *recordingRunAttemptCommands) MarkTurnAccepted(context.Context, corecontract.MarkTurnAcceptedRequest) (corecontract.MarkTurnAcceptedResponse, error) {
	commands.call = "turn-accepted"
	return corecontract.MarkTurnAcceptedResponse{}, nil
}

func (commands *recordingRunAttemptCommands) AppendAttemptEvents(context.Context, corecontract.AppendAttemptEventsRequest) (corecontract.AppendAttemptEventsResponse, error) {
	commands.call = "append-events"
	return corecontract.AppendAttemptEventsResponse{}, nil
}
