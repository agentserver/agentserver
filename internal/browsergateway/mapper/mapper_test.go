package mapper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/agentserver/agentserver/internal/browsergateway/codexclient"
)

func loadFrame(t *testing.T, name string) codexclient.Frame {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var wire struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return codexclient.Frame{Method: wire.Method, Params: wire.Params}
}

func types(evs []events.Event) []events.EventType {
	out := make([]events.EventType, len(evs))
	for i, e := range evs {
		out[i] = e.Type()
	}
	return out
}

func TestMap_AgentMessageStart(t *testing.T) {
	r := Map(loadFrame(t, "agent_message_started.json"))
	got := types(r.Events)
	if len(got) != 1 || got[0] != events.EventTypeTextMessageStart {
		t.Fatalf("start frame → %v, want [TEXT_MESSAGE_START]", got)
	}
}

func TestMap_AgentMessageDelta(t *testing.T) {
	r := Map(loadFrame(t, "agent_message_delta.json"))
	got := types(r.Events)
	if len(got) != 1 || got[0] != events.EventTypeTextMessageContent {
		t.Fatalf("delta frame → %v, want [TEXT_MESSAGE_CONTENT]", got)
	}
	ce := r.Events[0].(*events.TextMessageContentEvent)
	if ce.Delta != "Hello" {
		t.Errorf("delta = %q, want Hello", ce.Delta)
	}
}

func TestMap_AgentMessageCompleted(t *testing.T) {
	r := Map(loadFrame(t, "agent_message.json"))
	got := types(r.Events)
	if len(got) != 1 || got[0] != events.EventTypeTextMessageEnd {
		t.Fatalf("completed frame → %v, want [TEXT_MESSAGE_END]", got)
	}
}

func TestMap_TurnCompleted(t *testing.T) {
	r := Map(loadFrame(t, "turn_completed.json"))
	if !r.Done {
		t.Fatal("Done = false, want true")
	}
	if len(r.Events) != 0 {
		t.Errorf("Events = %v, want none", types(r.Events))
	}
}

func TestMap_TurnFailed(t *testing.T) {
	r := Map(loadFrame(t, "turn_failed.json"))
	if r.Err == "" {
		t.Fatal("Err = empty, want non-empty for a failed turn")
	}
	if r.Done {
		t.Errorf("Done = true, want false for a failed turn")
	}
}

func TestMap_Reasoning(t *testing.T) {
	r := Map(loadFrame(t, "reasoning.json"))
	got := types(r.Events)
	if len(got) != 3 || got[0] != events.EventTypeReasoningMessageStart || got[1] != events.EventTypeReasoningMessageContent || got[2] != events.EventTypeReasoningMessageEnd {
		t.Fatalf("event types = %v, want reasoning start/content/end", got)
	}
	// The content event must carry joined summary+content, not empty.
	ce, ok := r.Events[1].(*events.ReasoningMessageContentEvent)
	if !ok {
		t.Fatalf("event[1] is %T, want *ReasoningMessageContentEvent", r.Events[1])
	}
	if ce.Delta == "" {
		t.Fatal("reasoning content delta is empty — summary/content not read")
	}
}

func TestMap_UnknownFrameIsNoop(t *testing.T) {
	r := Map(codexclient.Frame{Method: "turn/started", Params: []byte(`{}`)})
	if len(r.Events) != 0 || r.Done || r.Err != "" {
		t.Fatalf("unknown frame produced %+v, want empty Result", r)
	}
}

func TestMap_CommandExecution(t *testing.T) {
	r := Map(loadFrame(t, "command_execution.json"))
	got := types(r.Events)
	want := []events.EventType{
		events.EventTypeToolCallStart,
		events.EventTypeToolCallArgs,
		events.EventTypeToolCallEnd,
		events.EventTypeToolCallResult,
		events.EventTypeCustom,
	}
	if len(got) != len(want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event[%d] = %v, want %v (full: %v)", i, got[i], want[i], got)
		}
	}
	last, ok := r.Events[len(r.Events)-1].(*events.CustomEvent)
	if !ok || last.Name != "a2ui.operations" {
		t.Fatalf("last event not CUSTOM a2ui.operations: %+v", r.Events[len(r.Events)-1])
	}
}

func TestMap_FileChange(t *testing.T) {
	r := Map(loadFrame(t, "file_change.json"))
	got := types(r.Events)
	want := []events.EventType{
		events.EventTypeToolCallStart,
		events.EventTypeToolCallArgs,
		events.EventTypeToolCallEnd,
		events.EventTypeToolCallResult,
		events.EventTypeCustom,
	}
	if len(got) != len(want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	start, ok := r.Events[0].(*events.ToolCallStartEvent)
	if !ok || start.ToolCallName != "apply_patch" {
		t.Fatalf("tool name = %v, want apply_patch", r.Events[0])
	}
}
