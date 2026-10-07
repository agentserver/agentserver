package harnessworker

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/tooloutput"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPOutputReplacesEncodedMirrorButPreservesDiagnosticsAndStatus(t *testing.T) {
	doc := map[string]any{"process_id": "p1", "status": "failed", "exit_code": 7, "output_complete": false, "timed_out": true, "sandbox_denied": false, "next_sequence": 2, "chunks": []any{map[string]any{"sequence": 1, "stream": "stderr", "chunk_base64": base64.StdEncoding.EncodeToString([]byte("查询超时\n"))}}}
	mirror, _ := json.MarshalIndent(doc, "", "  ")
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "executor diagnostic"}, &mcp.TextContent{Text: string(mirror)}}, StructuredContent: mcpJSONValue(t, doc), IsError: true}
	before, _ := json.Marshal(result)
	converted, err := convertToolResult(result, DefaultLimits(), "executor", "shell")
	if err != nil {
		t.Fatal(err)
	}
	if converted.Success || len(converted.ContentItems) != 2 || converted.ContentItems[0].Text != "executor diagnostic" {
		t.Fatalf("conversion: %+v", converted)
	}
	text := converted.ContentItems[1].Text
	if strings.Contains(text, "chunk_base64") || !strings.Contains(text, "查询超时\n") {
		t.Fatal(text)
	}
	s, _, ok := tooloutput.Parse(text)
	if !ok || s.Status != "failed" || s.ExitCode == nil || *s.ExitCode != 7 || !*s.TimedOut || *s.OutputComplete {
		t.Fatal(s)
	}
	after, _ := json.Marshal(result)
	if string(before) != string(after) {
		t.Fatal("mutated MCP transport result")
	}
	unknown, err := convertToolResult(result, DefaultLimits(), "custom", "shell")
	if err != nil || len(unknown.ContentItems) != 3 {
		t.Fatal("changed unknown tool", err)
	}
}

func TestMCPReadableOutputFitsRetainedNotificationBudget(t *testing.T) {
	content := strings.Repeat("\"\\\n\x1b", 200000)
	doc := map[string]any{"status": "succeeded", "path": "escaped.txt", "offset": 0, "requested_bytes": len(content), "bytes_read": len(content), "eof": true, "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content))}
	converted, err := convertToolResult(&mcp.CallToolResult{Content: []mcp.Content{}, StructuredContent: mcpJSONValue(t, doc)}, DefaultLimits(), "executor", "read_file")
	if err != nil {
		t.Fatal(err)
	}
	s, _, ok := tooloutput.Parse(converted.ContentItems[0].Text)
	encoded, _ := json.Marshal(converted)
	if !ok || !s.DisplayTruncated || s.TextBytesShown == nil || *s.TextBytesShown >= uint64(len(content)) || len(encoded) > workerMaxEventBytes/4+128 {
		t.Fatalf("oversized or unmarked converted result: bytes=%d summary=%+v", len(encoded), s)
	}
}

func mcpJSONValue(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}
