package browsergateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/agentserver/agentserver/v2/internal/browsergateway/a2ui"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

const (
	projectorWorkspaceID = "20000000-0000-4000-8000-000000000002"
	projectorSessionID   = "30000000-0000-4000-8000-000000000003"
	projectorRunID       = "40000000-0000-4000-8000-000000000004"
)

func TestProjectorMapsCanonicalMessageToolA2UIAndCompletion(t *testing.T) {
	projector := newTestProjector(t, 1)
	canonical := []runevent.Event{
		projectorEvent(t, 2, runevent.KindAssistantMessageStarted, runevent.MessageStartedPayload{MessageID: "message-1", Role: "assistant"}),
		projectorEvent(t, 3, runevent.KindAssistantMessageDelta, runevent.MessageDeltaPayload{MessageID: "message-1", Delta: "hello"}),
		projectorEvent(t, 4, runevent.KindAssistantMessageCompleted, runevent.MessageCompletedPayload{MessageID: "message-1"}),
		projectorEvent(t, 5, runevent.KindToolCallStarted, runevent.ToolCallStartedPayload{ToolCallID: "call-1", ToolCallName: "executor.shell", ParentMessageID: "message-1"}),
		projectorEvent(t, 6, runevent.KindToolCallArguments, runevent.ToolCallArgumentsPayload{ToolCallID: "call-1", Delta: `{"command":"pwd"}`}),
		projectorEvent(t, 7, runevent.KindToolCallProgress, runevent.ToolCallProgressPayload{
			ToolCallID: "call-1", Progress: 1, Total: 2, Message: "running",
		}),
		projectorEvent(t, 8, runevent.KindToolCallCompleted, runevent.ToolCallCompletedPayload{ToolCallID: "call-1"}),
		projectorEvent(t, 9, runevent.KindToolCallResult, runevent.ToolCallResultPayload{
			MessageID: "tool-message-1", ToolCallID: "call-1", Content: "/workspace",
			Presentation: &runevent.ToolPresentation{
				Kind: "command",
				Command: &runevent.CommandPresentation{
					Command: "pwd", Output: "/workspace", Status: "succeeded",
				},
			},
		}),
		projectorEvent(t, 10, runevent.KindRunCompleted, runevent.RunTerminalPayload{Result: json.RawMessage(`{"answer":42}`)}),
	}

	projected := []events.Event{events.NewRunStartedEvent(projectorSessionID, projectorRunID)}
	for _, event := range canonical {
		result, err := projector.Project(event)
		if err != nil {
			t.Fatalf("Project(seq=%d, kind=%s) error = %v", event.Seq, event.Kind, err)
		}
		projected = append(projected, result.Events...)
		if event.Kind == runevent.KindRunCompleted && !result.Terminal {
			t.Fatal("run.completed did not terminate projection")
		}
	}
	wantTypes := []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeTextMessageStart,
		events.EventTypeTextMessageContent,
		events.EventTypeTextMessageEnd,
		events.EventTypeToolCallStart,
		events.EventTypeToolCallArgs,
		events.EventTypeCustom,
		events.EventTypeToolCallEnd,
		events.EventTypeToolCallResult,
		events.EventTypeCustom,
		events.EventTypeRunFinished,
	}
	if len(projected) != len(wantTypes) {
		t.Fatalf("projected event count = %d, want %d", len(projected), len(wantTypes))
	}
	for index, want := range wantTypes {
		if got := projected[index].Type(); got != want {
			t.Fatalf("projected[%d] type = %s, want %s", index, got, want)
		}
	}
	if err := events.ValidateSequence(projected); err != nil {
		t.Fatalf("AG-UI sequence is invalid: %v", err)
	}
	progress := projected[6].(*events.CustomEvent)
	if progress.Name != "agentserver.tool_progress" {
		t.Fatalf("progress CUSTOM name = %q", progress.Name)
	}
	progressValue, ok := progress.Value.(map[string]any)
	if !ok || progressValue["toolCallId"] != "call-1" || progressValue["progress"] != float64(1) ||
		progressValue["total"] != float64(2) || progressValue["message"] != "running" {
		t.Fatalf("progress CUSTOM value = %#v", progress.Value)
	}
	custom := projected[9].(*events.CustomEvent)
	if custom.Name != "a2ui.operations" {
		t.Fatalf("CUSTOM name = %q", custom.Name)
	}
	operations, ok := custom.Value.([]a2ui.Message)
	if !ok {
		t.Fatalf("CUSTOM value type = %T", custom.Value)
	}
	if err := a2ui.ValidateOperations(operations); err != nil {
		t.Fatalf("projected A2UI operations invalid: %v", err)
	}
	wantTimestamp := canonical[7].CreatedAt.UnixMilli()
	if projected[8].Timestamp() == nil || *projected[8].Timestamp() != wantTimestamp || projected[9].Timestamp() == nil || *projected[9].Timestamp() != wantTimestamp {
		t.Fatalf("tool result/A2UI timestamps did not preserve canonical createdAt")
	}
	finished := projected[len(projected)-1].(*events.RunFinishedEvent)
	if finished.Outcome == nil || finished.Outcome.Type != events.RunFinishedOutcomeTypeSuccess {
		t.Fatalf("RUN_FINISHED outcome = %+v", finished.Outcome)
	}
}

func TestProjectorRejectsToolProgressOutsideToolLifecycle(t *testing.T) {
	projector := newTestProjector(t, 0)
	progress := projectorEvent(t, 1, runevent.KindToolCallProgress, runevent.ToolCallProgressPayload{
		ToolCallID: "call-1", Progress: 1, Total: 2,
	})
	if _, err := projector.Project(progress); err == nil || !strings.Contains(err.Error(), "before start") {
		t.Fatalf("progress-before-start error = %v", err)
	}
}

func TestProjectorPublishesCanonicalCancellingStateWithoutEndingStream(t *testing.T) {
	projector := newTestProjector(t, 0)
	result, err := projector.Project(projectorEvent(t, 1, runevent.KindRunCancelling, runevent.RunTerminalPayload{
		Code: "user_cancelled", Message: "the run was cancelled by a workspace member",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Terminal || len(result.Events) != 1 || result.Events[0].Type() != events.EventTypeCustom {
		t.Fatalf("cancelling projection = %+v", result)
	}
	custom := result.Events[0].(*events.CustomEvent)
	value, ok := custom.Value.(map[string]any)
	if custom.Name != "agentserver.run_status" || !ok || value["runId"] != projectorRunID ||
		value["status"] != "cancelling" || value["code"] != "user_cancelled" {
		t.Fatalf("cancelling custom event = %#v", custom)
	}
}

func TestProjectorMapsApprovalAuthorityToCustomAndDisplayOnlyA2UI(t *testing.T) {
	projector := newTestProjector(t, 0)
	event := projectorEvent(t, 1, runevent.KindApprovalRequested, runevent.ApprovalPayload{
		RunID: projectorRunID, RunAttemptID: "50000000-0000-4000-8000-000000000005",
		RunAttemptGeneration: 1, ExecutionID: "70000000-0000-4000-8000-000000000007",
		ApprovalID: "80000000-0000-4000-8000-000000000008", ToolName: "shell", Status: "pending",
		Nonce: "90000000-0000-4000-8000-000000000009", ContextSHA256: strings.Repeat("a", 64),
		ExpiresAt: time.Date(2026, 7, 31, 12, 10, 0, 0, time.UTC),
		Version:   1,
	})
	event.Source = "approval"
	result, err := projector.Project(event)
	if err != nil {
		t.Fatal(err)
	}
	if result.Terminal || len(result.Events) != 2 {
		t.Fatalf("approval projection = %+v", result)
	}
	authority := result.Events[0].(*events.CustomEvent)
	value, ok := authority.Value.(map[string]any)
	digest, digestOK := value["contextDigest"].(map[string]any)
	if authority.Name != "agentserver.approval" || !ok || value["approvalId"] != "80000000-0000-4000-8000-000000000008" ||
		value["status"] != "pending" || value["version"] != int64(1) || !digestOK || digest["domain"] != "approval-context" ||
		digest["canonicalizerVersion"] != "rfc8785-v1" || digest["sha256"] != strings.Repeat("a", 64) {
		t.Fatalf("approval authority event = %#v", authority)
	}
	display := result.Events[1].(*events.CustomEvent)
	operations, ok := display.Value.([]a2ui.Message)
	if display.Name != "a2ui.operations" || !ok {
		t.Fatalf("approval display event = %#v", display)
	}
	if err := a2ui.ValidateOperations(operations); err != nil {
		t.Fatal(err)
	}
}

func TestProjectorMapsReasoningAndNonSuccessTerminalToRunError(t *testing.T) {
	projector := newTestProjector(t, 0)
	input := []runevent.Event{
		projectorEvent(t, 1, runevent.KindAssistantReasoningStarted, runevent.MessageStartedPayload{MessageID: "reasoning-1", Role: "assistant"}),
		projectorEvent(t, 2, runevent.KindAssistantReasoningDelta, runevent.MessageDeltaPayload{MessageID: "reasoning-1", Delta: "checking"}),
		projectorEvent(t, 3, runevent.KindAssistantReasoningDone, runevent.MessageCompletedPayload{MessageID: "reasoning-1"}),
		projectorEvent(t, 4, runevent.KindRunInterrupted, runevent.RunTerminalPayload{Code: "lease_lost", Message: "attempt lease was lost"}),
	}
	var projected []events.Event
	for _, event := range input {
		result, err := projector.Project(event)
		if err != nil {
			t.Fatal(err)
		}
		projected = append(projected, result.Events...)
	}
	want := []events.EventType{
		events.EventTypeReasoningMessageStart,
		events.EventTypeReasoningMessageContent,
		events.EventTypeReasoningMessageEnd,
		events.EventTypeRunError,
	}
	for index, eventType := range want {
		if projected[index].Type() != eventType {
			t.Fatalf("event %d = %s, want %s", index, projected[index].Type(), eventType)
		}
	}
	if projected[len(projected)-1].Type() == events.EventTypeRunFinished {
		t.Fatal("interrupted run must not project as RUN_FINISHED")
	}
}

func TestProjectorSkipsUnknownKindsButFailsOnGapScopeAndKnownFutureSchema(t *testing.T) {
	t.Run("unknown kind", func(t *testing.T) {
		projector := newTestProjector(t, 0)
		future := projectorEvent(t, 1, "artifact.available", map[string]any{"object": "summary"})
		future.SchemaVersion = 42
		result, err := projector.Project(future)
		if err != nil || len(result.Events) != 0 {
			t.Fatalf("future projection = %+v, %v", result, err)
		}
		next := projectorEvent(t, 2, runevent.KindRunCompleted, runevent.RunTerminalPayload{})
		if _, err := projector.Project(next); err != nil {
			t.Fatalf("event after skipped future kind failed: %v", err)
		}
	})

	tests := []struct {
		name    string
		mutate  func(*runevent.Event)
		wantErr string
	}{
		{name: "gap", mutate: func(event *runevent.Event) { event.Seq = 2 }, wantErr: "sequence gap"},
		{name: "scope", mutate: func(event *runevent.Event) { event.SessionID = "90000000-0000-4000-8000-000000000009" }, wantErr: "scope"},
		{name: "known future schema", mutate: func(event *runevent.Event) { event.SchemaVersion = 2 }, wantErr: "unsupported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projector := newTestProjector(t, 0)
			event := projectorEvent(t, 1, runevent.KindRunCompleted, runevent.RunTerminalPayload{})
			test.mutate(&event)
			if _, err := projector.Project(event); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Project() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func newTestProjector(t *testing.T, afterSequence int64) *Projector {
	t.Helper()
	projector, err := NewProjector(ProjectionScope{
		WorkspaceID: projectorWorkspaceID,
		SessionID:   projectorSessionID,
		RunID:       projectorRunID,
	}, afterSequence)
	if err != nil {
		t.Fatal(err)
	}
	return projector
}

func projectorEvent(t *testing.T, sequence int64, kind string, payload any) runevent.Event {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	attemptID := "50000000-0000-4000-8000-000000000005"
	generation := int64(1)
	return runevent.Event{
		EventID:              fmt.Sprintf("10000000-0000-4000-8000-%012d", sequence),
		SchemaVersion:        runevent.CurrentSchemaVersion,
		Seq:                  sequence,
		WorkspaceID:          projectorWorkspaceID,
		SessionID:            projectorSessionID,
		RunID:                projectorRunID,
		RunAttemptID:         &attemptID,
		RunAttemptGeneration: &generation,
		ProducerInstanceID:   "60000000-0000-4000-8000-000000000006",
		ProducerSeq:          sequence,
		Source:               "brain",
		Kind:                 kind,
		CreatedAt:            time.Date(2026, 7, 31, 12, 0, int(sequence), 0, time.UTC),
		Payload:              raw,
	}
}
