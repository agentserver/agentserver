package coreserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

type recordingApprovalObservations struct {
	mu        sync.Mutex
	requests  []corecontract.ObserveApprovalRequest
	responses []corecontract.ObserveApprovalResponse
	err       error
}

func (commands *recordingApprovalObservations) ObserveApproval(_ context.Context, request corecontract.ObserveApprovalRequest) (corecontract.ObserveApprovalResponse, error) {
	commands.mu.Lock()
	defer commands.mu.Unlock()
	commands.requests = append(commands.requests, request)
	if commands.err != nil {
		return corecontract.ObserveApprovalResponse{}, commands.err
	}
	index := len(commands.requests) - 1
	if index >= len(commands.responses) {
		index = len(commands.responses) - 1
	}
	return commands.responses[index], nil
}

func TestApprovalObservationHandlerLongPollsCanonicalOutcome(t *testing.T) {
	pending := corecontract.ObserveApprovalResponse{
		ExecutionID: "50000000-0000-4000-8000-000000000005", ExecutionStatus: "pending_approval", ExecutionVersion: 1,
		Approval: corecontract.ApprovalState{ApprovalID: "40000000-0000-4000-8000-000000000071", Status: "pending", Version: 1},
	}
	approved := pending
	approved.Approval.Status = "approved"
	approved.Approval.Version = 2
	approved.OutcomeAvailable = true
	commands := &recordingApprovalObservations{responses: []corecontract.ObserveApprovalResponse{pending, approved}}
	authorizer := &recordingRunAttemptAuthorizer{}
	handler, err := NewApprovalObservationHandler(authorizer, commands)
	if err != nil {
		t.Fatal(err)
	}
	handler.poll = time.Millisecond

	request := httptest.NewRequest(http.MethodPost, corecontract.ObserveApprovalPath(pending.Approval.ApprovalID), bytes.NewReader(testObserveApprovalBody(t, pending.Approval.ApprovalID, 100)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || authorizer.action != "approvals.observe" {
		t.Fatalf("observe response=%d %s action=%q", response.Code, response.Body.String(), authorizer.action)
	}
	var result corecontract.ObserveApprovalResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || !result.OutcomeAvailable || result.Approval.Status != "approved" || result.Approval.Version != 2 {
		t.Fatalf("observe result=%+v error=%v", result, err)
	}
	commands.mu.Lock()
	defer commands.mu.Unlock()
	if len(commands.requests) != 2 || commands.requests[0].AfterApprovalVersion != 1 || commands.requests[0].WaitMillis != 100 {
		t.Fatalf("observe requests=%+v", commands.requests)
	}
}

func TestApprovalObservationHandlerReturnsPendingAtBoundedTimeout(t *testing.T) {
	pending := corecontract.ObserveApprovalResponse{
		ExecutionID: "50000000-0000-4000-8000-000000000005", ExecutionStatus: "pending_approval", ExecutionVersion: 1,
		Approval: corecontract.ApprovalState{ApprovalID: "40000000-0000-4000-8000-000000000071", Status: "pending", Version: 1},
	}
	commands := &recordingApprovalObservations{responses: []corecontract.ObserveApprovalResponse{pending}}
	handler, err := NewApprovalObservationHandler(&recordingRunAttemptAuthorizer{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	handler.poll = time.Millisecond
	request := httptest.NewRequest(http.MethodPost, corecontract.ObserveApprovalPath(pending.Approval.ApprovalID), bytes.NewReader(testObserveApprovalBody(t, pending.Approval.ApprovalID, 5)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("observe timeout response=%d %s", response.Code, response.Body.String())
	}
	var result corecontract.ObserveApprovalResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.OutcomeAvailable || result.Approval.Status != "pending" {
		t.Fatalf("observe timeout result=%+v error=%v", result, err)
	}
}

func TestApprovalObservationHandlerRejectsPathQueryAndUnboundedWait(t *testing.T) {
	approvalID := "40000000-0000-4000-8000-000000000071"
	commands := &recordingApprovalObservations{responses: []corecontract.ObserveApprovalResponse{{}}}
	handler, err := NewApprovalObservationHandler(&recordingRunAttemptAuthorizer{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		target string
		body   []byte
	}{
		"path":  {corecontract.ObserveApprovalPath(approvalID), testObserveApprovalBody(t, "40000000-0000-4000-8000-000000000072", 0)},
		"query": {corecontract.ObserveApprovalPath(approvalID) + "?wait=1", testObserveApprovalBody(t, approvalID, 0)},
		"wait":  {corecontract.ObserveApprovalPath(approvalID), testObserveApprovalBody(t, approvalID, maximumApprovalObserveWaitMillis+1)},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.target, bytes.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.Routes().ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid observe response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func testObserveApprovalBody(t *testing.T, approvalID string, waitMillis int64) []byte {
	t.Helper()
	raw, err := json.Marshal(corecontract.ObserveApprovalRequest{
		ApprovalID: approvalID, ExecutionID: "50000000-0000-4000-8000-000000000005",
		RunID: "41000000-0000-4000-8000-000000000004", RunAttemptID: "42000000-0000-4000-8000-000000000004",
		HolderID: "pool-holder", RunAttemptGeneration: 3,
		Nonce: "40000000-0000-4000-8000-000000000072",
		ContextDigest: corecontract.CanonicalJSONDigest{
			Domain: "approval-context", CanonicalizerVersion: "rfc8785-v1",
			SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
		AfterApprovalVersion: 1, WaitMillis: waitMillis,
		Record: corecontract.TransitionRecord{
			EventID: "40000000-0000-4000-8000-000000000081", ProducerInstanceID: "40000000-0000-4000-8000-000000000082",
			ProducerSeq: 1, OutboxID: "40000000-0000-4000-8000-000000000083",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
