package browsergateway

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

func TestDSHProjectionWithShippedTrajectoryConsumer(t *testing.T) {
	if _, err := os.Stat("../../dsh-web/dist/plugins/@deepseek-ai/dsh-client-ui-trajectory/client.js"); err != nil {
		if os.Getenv("AGENTSERVER_REQUIRE_DSH_ASSETS") == "1" {
			t.Fatal("build the pinned DSH frontend before release tests")
		}
		t.Skip("DSH frontend not built; run bash v2/dsh-web/build.sh")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the shipped DSH consumer regression")
	}
	state := newDSHTestGateway(t, &dshFakeBackend{}).installSession(corecontract.UserSessionState{SessionID: projectorSessionID})
	now := time.Unix(1700000000, 0)
	sequence := int64(0)
	project := func(kind string, payload any) {
		sequence++
		event := projectorEvent(t, sequence, kind, payload)
		event.CreatedAt = now.Add(time.Duration(sequence) * time.Second)
		state.mapCanonical(event)
	}
	message := func(id, text string, reasoning bool) {
		start, delta, done := runevent.KindAssistantMessageStarted, runevent.KindAssistantMessageDelta, runevent.KindAssistantMessageCompleted
		if reasoning {
			start, delta, done = runevent.KindAssistantReasoningStarted, runevent.KindAssistantReasoningDelta, runevent.KindAssistantReasoningDone
		}
		project(start, runevent.MessageStartedPayload{MessageID: id, Role: "assistant"})
		project(delta, runevent.MessageDeltaPayload{MessageID: id, Delta: text})
		project(done, runevent.MessageCompletedPayload{MessageID: id})
	}
	state.appendJournalPrompt(corecontract.UserSessionTranscriptMessage{MessageID: "user-1", Content: "hello", CreatedAt: now}, "request-1")
	message("greeting", "hello back", false)
	project(runevent.KindRunCompleted, runevent.RunTerminalPayload{})
	state.appendJournalPrompt(corecontract.UserSessionTranscriptMessage{MessageID: "user-2", Content: "query", CreatedAt: now.Add(5 * time.Second)}, "request-2")
	message("reasoning", "inspect first", true)
	for _, id := range []string{"call-a", "call-b"} {
		project(runevent.KindToolCallStarted, runevent.ToolCallStartedPayload{ToolCallID: id, ToolCallName: "executor.shell"})
		project(runevent.KindToolCallArguments, runevent.ToolCallArgumentsPayload{ToolCallID: id, Delta: `{"argv":["bkectl","--help"]}`})
	}
	// Parallel completion must retain each call's original step.
	for _, id := range []string{"call-b", "call-a"} {
		project(runevent.KindToolCallCompleted, runevent.ToolCallCompletedPayload{ToolCallID: id})
		project(runevent.KindToolCallResult, runevent.ToolCallResultPayload{ToolCallID: id, MessageID: id, Content: "ok"})
	}
	message("answer", "done", false)
	project(runevent.KindRunCompleted, runevent.RunTerminalPayload{})
	events, _, _ := state.snapshot()
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), node, "../../dsh-web/test/trajectory-replay.mjs")
	command.Stdin = bytes.NewReader(raw)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("shipped Trajectory consumer: %v\n%s", err, output)
	}
	t.Log(string(output))
}
