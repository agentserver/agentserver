package runner

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// helpers for EncodeUserMessage round-trip assertions
type userMsgFrame struct {
	Type    string `json:"type"`
	Message struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

// Test 1: EncodeUserMessage produces exactly one newline-terminated line with the expected JSON shape.
func TestEncodeUserMessage_Basic(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeUserMessage(&buf, "hello world"); err != nil {
		t.Fatalf("EncodeUserMessage returned error: %v", err)
	}
	out := buf.String()

	// Exactly one trailing newline, no embedded newlines.
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("output does not end with \\n: %q", out)
	}
	trimmed := strings.TrimRight(out, "\n")
	if strings.Contains(trimmed, "\n") {
		t.Fatalf("output contains embedded newline: %q", out)
	}

	var frame userMsgFrame
	if err := json.Unmarshal([]byte(trimmed), &frame); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if frame.Type != "user" {
		t.Errorf("type: got %q, want %q", frame.Type, "user")
	}
	if frame.Message.Role != "user" {
		t.Errorf("message.role: got %q, want %q", frame.Message.Role, "user")
	}
	if len(frame.Message.Content) == 0 {
		t.Fatal("message.content is empty")
	}
	if frame.Message.Content[0].Type != "text" {
		t.Errorf("content[0].type: got %q, want %q", frame.Message.Content[0].Type, "text")
	}
	if frame.Message.Content[0].Text != "hello world" {
		t.Errorf("content[0].text: got %q, want %q", frame.Message.Content[0].Text, "hello world")
	}
}

// Test 2: EncodeUserMessage escapes special characters correctly and round-trips.
func TestEncodeUserMessage_SpecialChars(t *testing.T) {
	input := "echo \"hello\"\nworld 🌟"
	var buf bytes.Buffer
	if err := EncodeUserMessage(&buf, input); err != nil {
		t.Fatalf("EncodeUserMessage returned error: %v", err)
	}
	line := strings.TrimRight(buf.String(), "\n")

	var frame userMsgFrame
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if len(frame.Message.Content) == 0 {
		t.Fatal("message.content is empty")
	}
	got := frame.Message.Content[0].Text
	if got != input {
		t.Errorf("round-trip text mismatch:\n  got  %q\n  want %q", got, input)
	}
}

// collectDecoded drains the channels returned by Decode and returns all messages plus the final error.
func collectDecoded(t *testing.T, r interface{ Read([]byte) (int, error) }) ([]SDKMessage, error) {
	t.Helper()
	msgs, errs := Decode(r)
	var result []SDKMessage
	for m := range msgs {
		result = append(result, m)
	}
	return result, <-errs
}

// Test 3: Decode reads exactly 40 frames from sample_transcript.jsonl.
func TestDecode_FrameCount(t *testing.T) {
	f, err := os.Open("testdata/sample_transcript.jsonl")
	if err != nil {
		t.Fatalf("open testdata: %v", err)
	}
	defer f.Close()

	msgs, decErr := collectDecoded(t, f)
	if decErr != nil {
		t.Fatalf("unexpected decode error: %v", decErr)
	}
	if len(msgs) != 40 {
		t.Errorf("frame count: got %d, want 40", len(msgs))
	}
}

// Test 4: First decoded frame is system/init with a non-empty session_id.
func TestDecode_FirstFrame_SystemInit(t *testing.T) {
	f, err := os.Open("testdata/sample_transcript.jsonl")
	if err != nil {
		t.Fatalf("open testdata: %v", err)
	}
	defer f.Close()

	msgs, decErr := collectDecoded(t, f)
	if decErr != nil {
		t.Fatalf("unexpected decode error: %v", decErr)
	}
	if len(msgs) == 0 {
		t.Fatal("no messages decoded")
	}
	first := msgs[0]
	if first.Type != "system" {
		t.Errorf("first.Type: got %q, want %q", first.Type, "system")
	}
	if first.Subtype != "init" {
		t.Errorf("first.Subtype: got %q, want %q", first.Subtype, "init")
	}
	if first.SessionID == "" {
		t.Error("first.SessionID is empty")
	}
}

// Test 5: Last decoded frame is result/success with a non-empty Result field.
func TestDecode_LastFrame_ResultSuccess(t *testing.T) {
	f, err := os.Open("testdata/sample_transcript.jsonl")
	if err != nil {
		t.Fatalf("open testdata: %v", err)
	}
	defer f.Close()

	msgs, decErr := collectDecoded(t, f)
	if decErr != nil {
		t.Fatalf("unexpected decode error: %v", decErr)
	}
	if len(msgs) == 0 {
		t.Fatal("no messages decoded")
	}
	last := msgs[len(msgs)-1]
	if last.Type != "result" {
		t.Errorf("last.Type: got %q, want %q", last.Type, "result")
	}
	if last.Subtype != "success" {
		t.Errorf("last.Subtype: got %q, want %q", last.Subtype, "success")
	}
	if len(last.Result) == 0 {
		t.Error("last.Result is empty")
	}
}

// Test 6: Decoder handles input without a trailing newline.
func TestDecode_NoTrailingNewline(t *testing.T) {
	input := []byte(`{"type":"system","subtype":"init","session_id":"abc"}`)
	msgs, decErr := collectDecoded(t, bytes.NewReader(input))
	if decErr != nil {
		t.Fatalf("unexpected decode error: %v", decErr)
	}
	if len(msgs) != 1 {
		t.Errorf("message count: got %d, want 1", len(msgs))
	}
}

// Test 7: Decoder skips trailing empty lines.
func TestDecode_TrailingEmptyLines(t *testing.T) {
	input := []byte("{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"abc\"}\n\n\n")
	msgs, decErr := collectDecoded(t, bytes.NewReader(input))
	if decErr != nil {
		t.Fatalf("unexpected decode error: %v", decErr)
	}
	if len(msgs) != 1 {
		t.Errorf("message count: got %d, want 1", len(msgs))
	}
}

// Test 8: Decoder emits a parse error on a malformed line.
func TestDecode_MalformedLine(t *testing.T) {
	input := []byte("{not json}\n")
	msgs, decErr := collectDecoded(t, bytes.NewReader(input))
	// We may or may not get a partial message — the important thing is decErr is non-nil.
	_ = msgs
	if decErr == nil {
		t.Fatal("expected non-nil error for malformed JSON, got nil")
	}
	errStr := strings.ToLower(decErr.Error())
	if !strings.Contains(errStr, "json") && !strings.Contains(errStr, "parse") &&
		!strings.Contains(errStr, "invalid") {
		t.Errorf("error message %q does not mention json/parse/invalid", decErr.Error())
	}
}

// Test 9: Decoded first frame's Raw field is verbatim and re-parses to the same values.
func TestDecode_RawField(t *testing.T) {
	f, err := os.Open("testdata/sample_transcript.jsonl")
	if err != nil {
		t.Fatalf("open testdata: %v", err)
	}
	defer f.Close()

	msgs, decErr := collectDecoded(t, f)
	if decErr != nil {
		t.Fatalf("unexpected decode error: %v", decErr)
	}
	if len(msgs) == 0 {
		t.Fatal("no messages decoded")
	}
	first := msgs[0]
	if len(first.Raw) == 0 {
		t.Fatal("first.Raw is nil/empty")
	}

	// Re-parse Raw into a fresh SDKMessage and check same Type/Subtype/SessionID.
	var reparsed SDKMessage
	if err := json.Unmarshal(first.Raw, &reparsed); err != nil {
		t.Fatalf("json.Unmarshal(Raw) failed: %v", err)
	}
	if reparsed.Type != first.Type {
		t.Errorf("Raw.Type: got %q, want %q", reparsed.Type, first.Type)
	}
	if reparsed.Subtype != first.Subtype {
		t.Errorf("Raw.Subtype: got %q, want %q", reparsed.Subtype, first.Subtype)
	}
	if reparsed.SessionID != first.SessionID {
		t.Errorf("Raw.SessionID: got %q, want %q", reparsed.SessionID, first.SessionID)
	}
}
