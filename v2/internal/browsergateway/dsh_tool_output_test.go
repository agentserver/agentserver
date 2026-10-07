package browsergateway

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/runevent"
	"github.com/agentserver/agentserver/v2/internal/tooloutput"
)

func TestDSHDecodesHistoricalToolContentWithoutChangingSequence(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"status": "succeeded", "exit_code": 0, "output_complete": true, "chunks": []any{map[string]any{"sequence": 1, "stream": "stdout", "chunk_base64": base64.StdEncoding.EncodeToString([]byte("飞书文档内容\n"))}}})
	current, _, err := tooloutput.Render("executor", "shell", raw, 32*1024)
	if err != nil {
		t.Fatal(err)
	}
	var cursors []int64
	var contents []string
	for _, content := range []string{string(raw), current} {
		state := newDSHTestGateway(t, &dshFakeBackend{}).installSession(corecontract.UserSessionState{SessionID: projectorSessionID})
		state.appendJournalPrompt(corecontract.UserSessionTranscriptMessage{MessageID: "user-1", Content: "read", CreatedAt: time.Unix(100, 0)}, "request-1")
		state.mapCanonical(projectorEvent(t, 1, runevent.KindToolCallStarted, runevent.ToolCallStartedPayload{ToolCallID: "call-1", ToolCallName: "executor.shell"}))
		state.mapCanonical(projectorEvent(t, 2, runevent.KindToolCallResult, runevent.ToolCallResultPayload{MessageID: "result-1", ToolCallID: "call-1", Content: content}))
		events, _, cursor := state.snapshot()
		cursors = append(cursors, cursor)
		for _, event := range events {
			if event.Type == "tool/result" {
				message := event.Data.(map[string]any)["message"].(map[string]any)
				text := message["content"].([]any)[0].(map[string]any)["text"].(string)
				if strings.Contains(text, "chunk_base64") || !strings.Contains(text, "飞书文档内容\n") {
					t.Fatal("encoded DSH output", text)
				}
				contents = append(contents, text)
			}
		}
	}
	if cursors[0] != cursors[1] || len(contents) != 2 || contents[0] != contents[1] {
		t.Fatal("old/new projection or cursors differ", cursors)
	}
}
