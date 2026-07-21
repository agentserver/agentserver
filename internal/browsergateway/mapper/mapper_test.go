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

func TestMap_AgentMessage(t *testing.T) {
	r := Map(loadFrame(t, "agent_message.json"))
	got := types(r.Events)
	want := []events.EventType{events.EventTypeTextMessageStart, events.EventTypeTextMessageContent, events.EventTypeTextMessageEnd}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	if r.Done || r.Err != "" {
		t.Errorf("Done=%v Err=%q, want false/empty", r.Done, r.Err)
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
