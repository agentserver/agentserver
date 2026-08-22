package browsergateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

type browserRoundTripFunc func(*http.Request) (*http.Response, error)

func (function browserRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestCoreRunBackendCreatesRunAndReadsScopedCommittedEvents(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Header.Get("Authorization") != "Bearer user-token" {
			t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
		}
		switch calls {
		case 1:
			if request.Method != http.MethodPost || request.URL.Path != corecontract.CreateUserRunPath(projectorWorkspaceID, projectorSessionID) || request.Header.Get("Idempotency-Key") != "request-1" {
				t.Fatalf("CreateRun request = %s %s headers=%v", request.Method, request.URL, request.Header)
			}
			var body corecontract.CreateUserRunRequest
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Prompt != "hello" || body.ClientRunID != "client-1" || body.ExpectedPermissionModeVersion != 3 {
				t.Fatalf("CreateRun body = %+v, %v", body, err)
			}
			return browserJSONResponse(request, http.StatusCreated, corecontract.CreateUserRunResponse{
				WorkspaceID: projectorWorkspaceID, SessionID: projectorSessionID, RunID: projectorRunID,
				CreatedAt: time.Date(2026, 7, 31, 17, 0, 0, 0, time.UTC), Cursor: "v1.initial", LastEventSequence: 1, Created: true,
			}), nil
		case 2:
			if request.Method != http.MethodGet || request.URL.Path != corecontract.ReadUserRunEventsPath(projectorWorkspaceID, projectorRunID) || request.URL.Query().Get("after") != "v1.initial" || request.URL.Query().Get("limit") != "32" || request.URL.Query().Get("waitMs") != "2500" {
				t.Fatalf("event request = %s %s", request.Method, request.URL)
			}
			event := backendRunEvent(t, 2, runevent.KindRunCompleted, `{}`)
			return browserJSONResponse(request, http.StatusOK, corecontract.ReadUserRunEventsResponse{
				Events: []runevent.Event{event}, EventCursors: []string{"v1.next"}, NextCursor: "v1.next", LastEventSequence: 2,
			}), nil
		default:
			t.Fatalf("unexpected core request %d", calls)
			return nil, nil
		}
	})}
	backend, err := NewCoreRunBackend("https://core.agentserver.local", client)
	if err != nil {
		t.Fatal(err)
	}
	started, err := backend.StartRun(t.Context(), StartRunRequest{
		BearerToken: "user-token", WorkspaceID: projectorWorkspaceID, SessionID: projectorSessionID,
		IdempotencyKey: "request-1", ClientRunID: "client-1", Prompt: "hello", ExpectedPermissionModeVersion: 3,
	})
	if err != nil || started.RunID != projectorRunID || started.Cursor != "v1.initial" {
		t.Fatalf("StartRun() = %+v, %v", started, err)
	}
	page, err := backend.ReadRunEvents(t.Context(), ReadRunEventsRequest{
		BearerToken: "user-token", WorkspaceID: projectorWorkspaceID, SessionID: projectorSessionID,
		RunID: projectorRunID, After: started.Cursor, Limit: 32, Wait: 2500 * time.Millisecond,
	})
	if err != nil || len(page.Events) != 1 || page.Events[0].Kind != runevent.KindRunCompleted || page.NextCursor != "v1.next" {
		t.Fatalf("ReadRunEvents() = %+v, %v", page, err)
	}
}

func TestCoreRunBackendForwardsExplicitCancelWithoutIdempotencyOrBody(t *testing.T) {
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != corecontract.CancelUserRunPath(projectorWorkspaceID, projectorRunID) ||
			request.Header.Get("Authorization") != "Bearer user-token" || request.Header.Get("Idempotency-Key") != "" || request.Body != nil {
			t.Fatalf("cancel request = %s %s headers=%v body=%v", request.Method, request.URL, request.Header, request.Body)
		}
		return browserJSONResponse(request, http.StatusOK, corecontract.CancelUserRunResponse{
			WorkspaceID: projectorWorkspaceID, SessionID: projectorSessionID, RunID: projectorRunID,
			Status: "cancelling", RunVersion: 4, Terminal: false, Changed: true,
		}), nil
	})}
	backend, _ := NewCoreRunBackend("https://core.agentserver.local", client)
	result, err := backend.CancelRun(t.Context(), CancelRunRequest{
		BearerToken: "user-token", WorkspaceID: projectorWorkspaceID, RunID: projectorRunID,
	})
	if err != nil || result.Status != "cancelling" || result.Terminal || !result.Changed {
		t.Fatalf("CancelRun() = %+v, %v", result, err)
	}
}

func TestCoreRunBackendForwardsApprovalDecisionWithExactAuthority(t *testing.T) {
	digest := corecontract.CanonicalJSONDigest{
		Domain: "approval-context", CanonicalizerVersion: "rfc8785-v1", SHA256: strings.Repeat("a", 64),
	}
	approvalID := "80000000-0000-4000-8000-000000000008"
	nonce := "90000000-0000-4000-8000-000000000009"
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != corecontract.DecideUserApprovalPath(projectorWorkspaceID, approvalID) ||
			request.Header.Get("Authorization") != "Bearer user-token" || request.Header.Get("Content-Type") != "application/json" ||
			request.Header.Get("Idempotency-Key") != "" {
			t.Fatalf("approval request = %s %s headers=%v", request.Method, request.URL, request.Header)
		}
		var input corecontract.DecideUserApprovalRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.Decision != "approve" ||
			input.Nonce != nonce || input.ContextDigest != digest || input.ExpectedApprovalVersion != 1 {
			t.Fatalf("approval body = %+v, %v", input, err)
		}
		now := time.Date(2026, 7, 31, 17, 0, 0, 0, time.UTC)
		return browserJSONResponse(request, http.StatusOK, corecontract.DecideUserApprovalResponse{
			WorkspaceID: projectorWorkspaceID, ExecutionID: "70000000-0000-4000-8000-000000000007",
			ExecutionStatus: "pending_approval", ExecutionVersion: 2, Changed: true,
			Approval: corecontract.ApprovalState{
				ApprovalID: approvalID, ExecutionID: "70000000-0000-4000-8000-000000000007",
				RunID: projectorRunID, RunAttemptID: "50000000-0000-4000-8000-000000000005",
				RunAttemptGeneration: 1, Nonce: nonce, RequesterID: "gateway-1",
				ApproverID: "10000000-0000-4000-8000-000000000010", Decision: "approve",
				ContextDigest: digest, Status: "approved", ExpiresAt: now.Add(time.Minute),
				Version: 2, CreatedAt: now, UpdatedAt: now,
			},
		}), nil
	})}
	backend, _ := NewCoreRunBackend("https://core.agentserver.local", client)
	result, err := backend.DecideApproval(t.Context(), DecideApprovalRequest{
		BearerToken: "user-token", WorkspaceID: projectorWorkspaceID, ApprovalID: approvalID,
		Decision: "approve", Nonce: nonce, ContextDigest: digest, ExpectedApprovalVersion: 1,
	})
	if err != nil || result.Approval.Status != "approved" || result.ExecutionStatus != "pending_approval" || !result.Changed {
		t.Fatalf("DecideApproval() = %+v, %v", result, err)
	}
}

func TestCoreRunBackendRejectsNonPostCancelState(t *testing.T) {
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return browserJSONResponse(request, http.StatusOK, corecontract.CancelUserRunResponse{
			WorkspaceID: projectorWorkspaceID, SessionID: projectorSessionID, RunID: projectorRunID,
			Status: "running", RunVersion: 4, Terminal: false, Changed: true,
		}), nil
	})}
	backend, _ := NewCoreRunBackend("https://core.agentserver.local", client)
	if _, err := backend.CancelRun(t.Context(), CancelRunRequest{
		BearerToken: "user-token", WorkspaceID: projectorWorkspaceID, RunID: projectorRunID,
	}); err == nil || !strings.Contains(err.Error(), "escaped or contradicted") {
		t.Fatalf("non-post-cancel state error = %v", err)
	}
}

func TestCoreRunBackendResolvesExplicitReconnectCursorBeforeSSE(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return browserJSONResponse(request, http.StatusOK, corecontract.CreateUserRunResponse{
				WorkspaceID: projectorWorkspaceID, SessionID: projectorSessionID, RunID: projectorRunID,
				CreatedAt: time.Now(), Cursor: "v1.initial", LastEventSequence: 1, Created: false,
			}), nil
		}
		if request.URL.Query().Get("after") != "v1.resume" || request.URL.Query().Get("limit") != "0" || request.URL.Query().Get("waitMs") != "0" {
			t.Fatalf("cursor resolution request = %s", request.URL)
		}
		return browserJSONResponse(request, http.StatusOK, corecontract.ReadUserRunEventsResponse{
			Events: []runevent.Event{}, EventCursors: []string{}, NextCursor: "v1.resume", LastEventSequence: 7,
		}), nil
	})}
	backend, _ := NewCoreRunBackend("https://core.agentserver.local", client)
	started, err := backend.StartRun(t.Context(), StartRunRequest{
		BearerToken: "user-token", WorkspaceID: projectorWorkspaceID, SessionID: projectorSessionID,
		IdempotencyKey: "request-1", Prompt: "hello", ResumeCursor: "v1.resume",
	})
	if err != nil || calls != 2 || started.Cursor != "v1.resume" || started.LastEventSequence != 7 {
		t.Fatalf("StartRun() = %+v, %v; calls = %d", started, err, calls)
	}
}

func TestCoreRunBackendMapsAuthorizedCursorRebase(t *testing.T) {
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return browserJSONResponse(request, http.StatusGone, corecontract.UserRunCursorExpiredResponse{
			Code: "cursor_expired", Message: "expired",
			Snapshot: corecontract.UserRunSnapshot{
				WorkspaceID: projectorWorkspaceID, SessionID: projectorSessionID, RunID: projectorRunID,
				Status: "running", RunVersion: 4, LastEventSequence: 5,
				State: []byte(`{"messages":[{"id":"message-1"}]}`), UpdatedAt: time.Now(),
			},
			RebaseCursor: "v1.rebase", LastEventSequence: 5,
		}), nil
	})}
	backend, _ := NewCoreRunBackend("https://core.agentserver.local", client)
	_, err := backend.ReadRunEvents(t.Context(), ReadRunEventsRequest{
		BearerToken: "user-token", WorkspaceID: projectorWorkspaceID, SessionID: projectorSessionID,
		RunID: projectorRunID, After: "v1.old", Limit: 10, Wait: time.Second,
	})
	var expired *CursorExpiredError
	if !errors.As(err, &expired) || expired.RebaseCursor != "v1.rebase" || expired.LastEventSequence != 5 {
		t.Fatalf("cursor error = %#v", err)
	}
	snapshot, ok := expired.Snapshot.(map[string]any)
	if !ok || snapshot["messages"] == nil {
		t.Fatalf("snapshot = %#v", expired.Snapshot)
	}
}

func TestCoreRunBackendRejectsCrossScopeCursorSnapshot(t *testing.T) {
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return browserJSONResponse(request, http.StatusGone, corecontract.UserRunCursorExpiredResponse{
			Code: "cursor_expired", Message: "expired",
			Snapshot: corecontract.UserRunSnapshot{
				WorkspaceID: "90000000-0000-4000-8000-000000000009",
				SessionID:   projectorSessionID, RunID: projectorRunID,
				Status: "running", RunVersion: 4, LastEventSequence: 5,
				State: []byte(`{"messages":[]}`), UpdatedAt: time.Now(),
			},
			RebaseCursor: "v1.rebase", LastEventSequence: 5,
		}), nil
	})}
	backend, _ := NewCoreRunBackend("https://core.agentserver.local", client)
	_, err := backend.ReadRunEvents(t.Context(), ReadRunEventsRequest{
		BearerToken: "user-token", WorkspaceID: projectorWorkspaceID, SessionID: projectorSessionID,
		RunID: projectorRunID, After: "v1.old", Limit: 10, Wait: time.Second,
	})
	var expired *CursorExpiredError
	if err == nil || errors.As(err, &expired) {
		t.Fatalf("cross-scope snapshot error = %#v", err)
	}
}

func TestCoreRunBackendRejectsNullEventArrays(t *testing.T) {
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return browserJSONResponse(request, http.StatusOK, corecontract.ReadUserRunEventsResponse{
			NextCursor: "v1.same", LastEventSequence: 1,
		}), nil
	})}
	backend, _ := NewCoreRunBackend("https://core.agentserver.local", client)
	if _, err := backend.ReadRunEvents(t.Context(), ReadRunEventsRequest{
		BearerToken: "user-token", WorkspaceID: projectorWorkspaceID, SessionID: projectorSessionID,
		RunID: projectorRunID, After: "v1.same", Limit: 10, Wait: time.Second,
	}); err == nil {
		t.Fatal("null event arrays were accepted")
	}
}

func TestCoreRunBackendDoesNotFollowRedirectOrExposeInternalErrors(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusFound, Header: http.Header{"Content-Type": []string{"application/json"}, "Location": []string{"https://token-sink.invalid/"}},
			Body: io.NopCloser(strings.NewReader(`{"code":"redirect","message":"not followed"}`)), Request: request,
		}, nil
	})}
	backend, _ := NewCoreRunBackend("https://core.agentserver.local", client)
	_, err := backend.StartRun(context.Background(), StartRunRequest{
		BearerToken: "secret-token", WorkspaceID: projectorWorkspaceID, SessionID: projectorSessionID,
		IdempotencyKey: "request-1", Prompt: "hello",
	})
	var public *BackendHTTPError
	if !errors.As(err, &public) || public.Status != http.StatusFound || requests != 1 {
		t.Fatalf("StartRun error = %#v, requests = %d", err, requests)
	}
}

func browserJSONResponse(request *http.Request, status int, value any) *http.Response {
	raw, _ := json.Marshal(value)
	return &http.Response{
		StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(string(raw))), Request: request,
	}
}

func backendRunEvent(t *testing.T, sequence int64, kind, payload string) runevent.Event {
	t.Helper()
	attemptID := "50000000-0000-4000-8000-000000000005"
	generation := int64(1)
	event := runevent.Event{
		EventID: "60000000-0000-4000-8000-000000000006", SchemaVersion: 1, Seq: sequence,
		WorkspaceID: projectorWorkspaceID, SessionID: projectorSessionID, RunID: projectorRunID,
		RunAttemptID: &attemptID, RunAttemptGeneration: &generation,
		ProducerInstanceID: "70000000-0000-4000-8000-000000000007", ProducerSeq: 1,
		Source: "system", Kind: kind, CreatedAt: time.Now(), Payload: []byte(payload),
	}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	return event
}
