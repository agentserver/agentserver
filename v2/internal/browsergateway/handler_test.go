package browsergateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

type fakeReadResult struct {
	result ReadRunEventsResult
	err    error
}

type fakeRunBackend struct {
	startResult StartRunResult
	startErr    error
	reads       []fakeReadResult

	startRequests []StartRunRequest
	readRequests  []ReadRunEventsRequest
}

func (backend *fakeRunBackend) StartRun(_ context.Context, request StartRunRequest) (StartRunResult, error) {
	backend.startRequests = append(backend.startRequests, request)
	return backend.startResult, backend.startErr
}

func (backend *fakeRunBackend) ReadRunEvents(ctx context.Context, request ReadRunEventsRequest) (ReadRunEventsResult, error) {
	backend.readRequests = append(backend.readRequests, request)
	if len(backend.reads) == 0 {
		<-ctx.Done()
		return ReadRunEventsResult{}, ctx.Err()
	}
	result := backend.reads[0]
	backend.reads = backend.reads[1:]
	return result.result, result.err
}

func TestAGUIHandlerStreamsCommittedCanonicalEventsAndA2UI(t *testing.T) {
	backend := &fakeRunBackend{
		startResult: validStartRunResult(),
		reads: []fakeReadResult{{result: ReadRunEventsResult{
			NextCursor: "cursor-9",
			Events: []runevent.Event{
				projectorEvent(t, 2, runevent.KindAssistantMessageStarted, runevent.MessageStartedPayload{MessageID: "message-1", Role: "assistant"}),
				projectorEvent(t, 3, runevent.KindAssistantMessageDelta, runevent.MessageDeltaPayload{MessageID: "message-1", Delta: "hello"}),
				projectorEvent(t, 4, runevent.KindAssistantMessageCompleted, runevent.MessageCompletedPayload{MessageID: "message-1"}),
				projectorEvent(t, 5, runevent.KindToolCallStarted, runevent.ToolCallStartedPayload{ToolCallID: "call-1", ToolCallName: "executor.shell"}),
				projectorEvent(t, 6, runevent.KindToolCallArguments, runevent.ToolCallArgumentsPayload{ToolCallID: "call-1", Delta: `{"command":"pwd"}`}),
				projectorEvent(t, 7, runevent.KindToolCallCompleted, runevent.ToolCallCompletedPayload{ToolCallID: "call-1"}),
				projectorEvent(t, 8, runevent.KindToolCallResult, runevent.ToolCallResultPayload{
					MessageID: "tool-message-1", ToolCallID: "call-1", Content: "/workspace",
					Presentation: &runevent.ToolPresentation{
						Kind: "command",
						Command: &runevent.CommandPresentation{
							Command: "pwd", Output: "/workspace", Status: "succeeded",
						},
					},
				}),
				projectorEvent(t, 9, runevent.KindRunCompleted, runevent.RunTerminalPayload{}),
			},
		}}},
	}
	handler := newTestHandler(t, backend)
	request := validAGUIRequest(t, `{
        "threadId":"30000000-0000-4000-8000-000000000003",
        "runId":"client-run-1",
        "messages":[{"id":"user-1","role":"user","content":"hello backend"}],
        "tools":[],"context":[]
    }`)
	response := httptest.NewRecorder()

	handler.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q", got)
	}
	if len(backend.startRequests) != 1 {
		t.Fatalf("StartRun calls = %d", len(backend.startRequests))
	}
	started := backend.startRequests[0]
	if started.BearerToken != "user-token" || started.IdempotencyKey != "request-1" || started.Prompt != "hello backend" || started.ClientRunID != "client-run-1" {
		t.Fatalf("StartRun request = %+v", started)
	}
	if len(backend.readRequests) != 1 || backend.readRequests[0].After != "cursor-1" || backend.readRequests[0].BearerToken != "user-token" {
		t.Fatalf("ReadRunEvents requests = %+v", backend.readRequests)
	}

	frames := decodeSSEData(t, response.Body.String())
	wantTypes := []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeTextMessageStart,
		events.EventTypeTextMessageContent,
		events.EventTypeTextMessageEnd,
		events.EventTypeToolCallStart,
		events.EventTypeToolCallArgs,
		events.EventTypeToolCallEnd,
		events.EventTypeToolCallResult,
		events.EventTypeCustom,
		events.EventTypeRunFinished,
	}
	if len(frames) != len(wantTypes) {
		t.Fatalf("SSE frames = %d, want %d\n%s", len(frames), len(wantTypes), response.Body.String())
	}
	for index, eventType := range wantTypes {
		if got := frames[index]["type"]; got != string(eventType) {
			t.Fatalf("frame %d type = %v, want %s", index, got, eventType)
		}
	}
	if frames[8]["name"] != "a2ui.operations" {
		t.Fatalf("CUSTOM frame = %+v", frames[8])
	}
	operations, ok := frames[8]["value"].([]any)
	if !ok || len(operations) != 3 {
		t.Fatalf("A2UI operations = %#v", frames[8]["value"])
	}
	first := operations[0].(map[string]any)
	if first["version"] != "v0.9" || first["createSurface"] == nil {
		t.Fatalf("first A2UI operation = %#v", first)
	}
}

func TestAGUIHandlerRebasesExpiredCursorWithStateSnapshot(t *testing.T) {
	backend := &fakeRunBackend{
		startResult: validStartRunResult(),
		reads: []fakeReadResult{
			{err: &CursorExpiredError{
				Snapshot:     map[string]any{"run": map[string]any{"status": "running"}},
				RebaseCursor: "cursor-5", LastEventSequence: 5,
			}},
			{result: ReadRunEventsResult{
				NextCursor: "cursor-6",
				Events:     []runevent.Event{projectorEvent(t, 6, runevent.KindRunCompleted, runevent.RunTerminalPayload{})},
			}},
		},
	}
	handler := newTestHandler(t, backend)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, validAGUIRequest(t, validAGUIBody()))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	frames := decodeSSEData(t, response.Body.String())
	want := []events.EventType{events.EventTypeRunStarted, events.EventTypeStateSnapshot, events.EventTypeRunFinished}
	if len(frames) != len(want) {
		t.Fatalf("frames = %#v", frames)
	}
	for index, eventType := range want {
		if frames[index]["type"] != string(eventType) {
			t.Fatalf("frame %d = %#v, want %s", index, frames[index], eventType)
		}
	}
	if len(backend.readRequests) != 2 || backend.readRequests[0].After != "cursor-1" || backend.readRequests[1].After != "cursor-5" {
		t.Fatalf("read cursors = %+v", backend.readRequests)
	}
}

func TestAGUIHandlerTurnsSequenceGapIntoRunError(t *testing.T) {
	backend := &fakeRunBackend{
		startResult: validStartRunResult(),
		reads: []fakeReadResult{{result: ReadRunEventsResult{
			NextCursor: "cursor-3",
			Events:     []runevent.Event{projectorEvent(t, 3, runevent.KindRunCompleted, runevent.RunTerminalPayload{})},
		}}},
	}
	handler := newTestHandler(t, backend)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, validAGUIRequest(t, validAGUIBody()))
	frames := decodeSSEData(t, response.Body.String())
	if len(frames) != 2 || frames[0]["type"] != string(events.EventTypeRunStarted) || frames[1]["type"] != string(events.EventTypeRunError) {
		t.Fatalf("gap frames = %#v", frames)
	}
	if frames[1]["code"] != "invalid_run_event_stream" {
		t.Fatalf("gap error = %#v", frames[1])
	}
}

func TestAGUIHandlerRejectsAuthenticationIdempotencyAndClientAuthority(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		mutate     func(*http.Request)
		wantStatus int
		wantCode   string
	}{
		{
			name: "missing bearer", body: validAGUIBody(),
			mutate:     func(request *http.Request) { request.Header.Del("Authorization") },
			wantStatus: http.StatusUnauthorized, wantCode: "unauthorized",
		},
		{
			name: "missing idempotency key", body: validAGUIBody(),
			mutate:     func(request *http.Request) { request.Header.Del("Idempotency-Key") },
			wantStatus: http.StatusBadRequest, wantCode: "invalid_idempotency_key",
		},
		{
			name: "wrong thread", body: strings.Replace(validAGUIBody(), projectorSessionID, "90000000-0000-4000-8000-000000000009", 1),
			mutate: func(*http.Request) {}, wantStatus: http.StatusBadRequest, wantCode: "invalid_agui_input",
		},
		{
			name: "client tools", body: strings.Replace(validAGUIBody(), `"tools":[]`, `"tools":[{"name":"shell","description":"bad","parameters":{}}]`, 1),
			mutate: func(*http.Request) {}, wantStatus: http.StatusBadRequest, wantCode: "invalid_agui_input",
		},
		{
			name: "duplicate field", body: strings.Replace(validAGUIBody(), `"messages":`, `"threadId":"`+projectorSessionID+`","messages":`, 1),
			mutate: func(*http.Request) {}, wantStatus: http.StatusBadRequest, wantCode: "invalid_agui_input",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &fakeRunBackend{startResult: validStartRunResult()}
			handler := newTestHandler(t, backend)
			request := validAGUIRequest(t, test.body)
			test.mutate(request)
			response := httptest.NewRecorder()
			handler.Routes().ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			var problem struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || problem.Code != test.wantCode {
				t.Fatalf("problem = %+v, %v, body = %s", problem, err, response.Body.String())
			}
			if len(backend.startRequests) != 0 {
				t.Fatalf("invalid request reached StartRun: %+v", backend.startRequests)
			}
		})
	}
}

func TestAGUIHandlerMapsPublicBackendConflictBeforeSSE(t *testing.T) {
	backend := &fakeRunBackend{startErr: &BackendHTTPError{
		Status: http.StatusConflict, Code: "active_run", Message: "session already has an active run", Err: errors.New("internal detail"),
	}}
	handler := newTestHandler(t, backend)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, validAGUIRequest(t, validAGUIBody()))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"active_run"`) || strings.Contains(response.Body.String(), "internal detail") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func newTestHandler(t *testing.T, backend RunBackend) *AGUIHandler {
	t.Helper()
	config := DefaultHandlerConfig()
	config.LongPollWait = time.Second
	config.Now = func() time.Time { return time.Date(2026, 7, 31, 12, 30, 0, 0, time.UTC) }
	config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := NewAGUIHandler(backend, config)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func validStartRunResult() StartRunResult {
	return StartRunResult{
		WorkspaceID: projectorWorkspaceID,
		SessionID:   projectorSessionID,
		RunID:       projectorRunID,
		CreatedAt:   time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		Cursor:      "cursor-1", LastEventSequence: 1,
	}
}

func validAGUIRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	path := "/v2/workspaces/" + projectorWorkspaceID + "/sessions/" + projectorSessionID + "/agui"
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer user-token")
	request.Header.Set("Idempotency-Key", "request-1")
	request.Header.Set("Content-Type", "application/json")
	return request
}

func validAGUIBody() string {
	return `{
        "threadId":"` + projectorSessionID + `",
        "runId":"client-run-1",
        "messages":[{"id":"user-1","role":"user","content":"hello"}],
        "tools":[],"context":[]
    }`
}

func decodeSSEData(t *testing.T, stream string) []map[string]any {
	t.Helper()
	var frames []map[string]any
	for _, line := range strings.Split(stream, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
			t.Fatalf("decode SSE data %q: %v", line, err)
		}
		frames = append(frames, frame)
	}
	return frames
}
