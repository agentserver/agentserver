package harnesspool

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
	"github.com/agentserver/agentserver/v2/internal/runevent"
	"github.com/agentserver/agentserver/v2/internal/tooloutput"
)

func TestRuntimeReadableToolOutputPreservesCommandPresentation(t *testing.T) {
	mapper := newTestRuntimeEventMapperWithTools(t, mcpcontract.ToolShell)
	arguments := map[string]any{"environment_id": runtimeMapperEnvironmentID, "argv": []string{"example"}}
	if _, err := mapper.Map(dynamicToolRuntimeEvent(t, "item/started", "call-text", mcpcontract.ToolShell, "inProgress", arguments, nil, nil)); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"status": "failed", "exit_code": 7, "output_complete": false, "timed_out": true, "chunks": []any{map[string]any{"sequence": 1, "stream": "stderr", "chunk_base64": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("详细错误\n", 10000)))}}})
	text, _, err := tooloutput.Render("executor", "shell", raw, 256*1024)
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := mapper.Map(dynamicToolRuntimeEvent(t, "item/completed", "call-text", mcpcontract.ToolShell, "completed", arguments, []map[string]any{{"type": "inputText", "text": text}}, boolPointer(true)))
	if err != nil {
		t.Fatal(err)
	}
	result := decodeMappedPayload[runevent.ToolCallResultPayload](t, mapped[1])
	s, _, ok := tooloutput.Parse(result.Content)
	if !ok || !s.DisplayTruncated || !strings.Contains(result.Content, "详细错误\n") || strings.Contains(result.Content, "chunk_base64") {
		t.Fatal("unreadable tool result", result.Content)
	}
	if result.Presentation == nil || result.Presentation.Command == nil || !strings.Contains(result.Presentation.Command.Output, "详细错误\n") || !strings.Contains(result.Presentation.Command.Status, "failed (exit 7) (timed out) (output incomplete)") {
		t.Fatalf("command presentation lost: %+v", result.Presentation)
	}
}
