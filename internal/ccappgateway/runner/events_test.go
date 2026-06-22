package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"strings"
	"testing"
)

// TestKeepFrameFromTranscript verifies that each frame type from the sample
// transcript classifies as expected (keep vs drop).
func TestKeepFrameFromTranscript(t *testing.T) {
	f, err := os.Open("testdata/sample_transcript.jsonl")
	if err != nil {
		t.Fatalf("open testdata: %v", err)
	}
	defer f.Close()

	messages, errs := Decode(f)
	var frames []SDKMessage
	for msg := range messages {
		frames = append(frames, msg)
	}
	if err := <-errs; err != nil {
		t.Fatalf("decode error: %v", err)
	}

	// Count by type/subtype
	systemInit := 0
	assistant := 0
	user := 0
	result := 0
	streamEvent := 0
	systemStatus := 0
	systemThinking := 0
	unknown := 0

	for _, f := range frames {
		switch {
		case f.Type == "system" && f.Subtype == "init":
			systemInit++
		case f.Type == "assistant":
			assistant++
		case f.Type == "user":
			user++
		case f.Type == "result":
			result++
		case f.Type == "stream_event":
			streamEvent++
		case f.Type == "system" && f.Subtype == "status":
			systemStatus++
		case f.Type == "system" && f.Subtype == "thinking_tokens":
			systemThinking++
		default:
			unknown++
		}
	}

	// Compute expected keeps and drops.
	expectedKeep := systemInit + assistant + user + result
	expectedDrop := streamEvent + systemStatus + systemThinking

	// Verify the count.
	actualKeep := 0
	actualDrop := 0
	for _, f := range frames {
		if KeepFrame(f) {
			actualKeep++
		} else {
			actualDrop++
		}
	}

	if actualKeep != expectedKeep {
		t.Errorf("KeepFrame: got %d keeps, want %d (system/init=%d, assistant=%d, user=%d, result=%d)",
			actualKeep, expectedKeep, systemInit, assistant, user, result)
	}
	if actualDrop != expectedDrop {
		t.Errorf("KeepFrame: got %d drops, want %d (stream_event=%d, system/status=%d, system/thinking_tokens=%d)",
			actualDrop, expectedDrop, streamEvent, systemStatus, systemThinking)
	}

	// Spot-check individual types.
	if systemInit > 0 && !KeepFrame(SDKMessage{Type: "system", Subtype: "init"}) {
		t.Error("KeepFrame(system/init) should be true")
	}
	if assistant > 0 && !KeepFrame(SDKMessage{Type: "assistant"}) {
		t.Error("KeepFrame(assistant) should be true")
	}
	if result > 0 && !KeepFrame(SDKMessage{Type: "result"}) {
		t.Error("KeepFrame(result) should be true")
	}
	if streamEvent > 0 && KeepFrame(SDKMessage{Type: "stream_event"}) {
		t.Error("KeepFrame(stream_event) should be false")
	}
	if systemStatus > 0 && KeepFrame(SDKMessage{Type: "system", Subtype: "status"}) {
		t.Error("KeepFrame(system/status) should be false")
	}
	if systemThinking > 0 && KeepFrame(SDKMessage{Type: "system", Subtype: "thinking_tokens"}) {
		t.Error("KeepFrame(system/thinking_tokens) should be false")
	}
}

// TestKeepFrameUnknownType verifies that unknown types default to keep and log a warning.
func TestKeepFrameUnknownType(t *testing.T) {
	// Save original logger output and redirect to buffer for capture.
	origOut := log.Writer()
	defer log.SetOutput(origOut)

	var buf bytes.Buffer
	log.SetOutput(&buf)

	// Call KeepFrame with unknown type.
	if !KeepFrame(SDKMessage{Type: "newtype", Subtype: "newsubtype"}) {
		t.Error("KeepFrame(unknown type) should be true (keep unknown frames)")
	}

	// Verify that a warning was logged.
	logOutput := buf.String()
	if !strings.Contains(logOutput, "unknown SDKMessage type") {
		t.Errorf("expected log message containing 'unknown SDKMessage type', got: %q", logOutput)
	}
	if !strings.Contains(logOutput, "newtype") {
		t.Errorf("expected log message containing 'newtype', got: %q", logOutput)
	}
}

// TestKeepFrameUserFrame verifies user frames are kept.
func TestKeepFrameUserFrame(t *testing.T) {
	if !KeepFrame(SDKMessage{Type: "user"}) {
		t.Error("KeepFrame(user) should be true")
	}
}

// TestKeepFrameSystemInit verifies system/init is kept.
func TestKeepFrameSystemInit(t *testing.T) {
	if !KeepFrame(SDKMessage{Type: "system", Subtype: "init"}) {
		t.Error("KeepFrame(system/init) should be true")
	}
}

// TestKeepFrameStreamEvent verifies stream_event is dropped.
func TestKeepFrameStreamEvent(t *testing.T) {
	if KeepFrame(SDKMessage{Type: "stream_event"}) {
		t.Error("KeepFrame(stream_event) should be false")
	}
}

// TestKeepFrameSystemStatus verifies system/status is dropped.
func TestKeepFrameSystemStatus(t *testing.T) {
	if KeepFrame(SDKMessage{Type: "system", Subtype: "status"}) {
		t.Error("KeepFrame(system/status) should be false")
	}
}

// TestKeepFrameSystemThinkingTokens verifies system/thinking_tokens is dropped.
func TestKeepFrameSystemThinkingTokens(t *testing.T) {
	if KeepFrame(SDKMessage{Type: "system", Subtype: "thinking_tokens"}) {
		t.Error("KeepFrame(system/thinking_tokens) should be false")
	}
}

// TestExtractAssistantTextFromTranscript verifies that the sample transcript
// yields the correct assistant text and result metadata.
func TestExtractAssistantTextFromTranscript(t *testing.T) {
	f, err := os.Open("testdata/sample_transcript.jsonl")
	if err != nil {
		t.Fatalf("open testdata: %v", err)
	}
	defer f.Close()

	messages, errs := Decode(f)
	text, meta, err := ExtractAssistantText(messages)
	if err != nil {
		t.Fatalf("ExtractAssistantText: %v", err)
	}
	if err := <-errs; err != nil {
		t.Fatalf("decode error: %v", err)
	}

	// The assistant text should be the final non-empty text from the last
	// assistant frame. From the sample transcript, that's:
	// "You asked me to echo \"phase0-works\"."
	expectedText := "You asked me to echo \"phase0-works\"."
	if text != expectedText {
		t.Errorf("assistant text mismatch: got %q, want %q", text, expectedText)
	}

	// Verify metadata.
	if meta == nil {
		t.Fatal("ResultMeta is nil")
	}
	if meta.Subtype != "success" {
		t.Errorf("subtype: got %q, want \"success\"", meta.Subtype)
	}
	if meta.IsError {
		t.Errorf("IsError: got true, want false")
	}
	if meta.DurationMs <= 0 {
		t.Errorf("DurationMs: got %d, want > 0", meta.DurationMs)
	}
	if len(meta.ModelUsage) == 0 {
		t.Error("ModelUsage: expected non-empty map")
	}
	if meta.ErrorMessage != "" {
		t.Errorf("ErrorMessage: got %q, want empty", meta.ErrorMessage)
	}

	// Spot-check a model usage entry.
	modelUsage, ok := meta.ModelUsage["claude-haiku-4-5-20251001"]
	if !ok {
		t.Error("ModelUsage missing claude-haiku-4-5-20251001")
	} else {
		if modelUsage.InputTokens <= 0 {
			t.Errorf("InputTokens: got %d, want > 0", modelUsage.InputTokens)
		}
		if modelUsage.OutputTokens <= 0 {
			t.Errorf("OutputTokens: got %d, want > 0", modelUsage.OutputTokens)
		}
	}
}

// TestExtractAssistantTextErrorResult verifies that an error result frame
// is properly extracted.
func TestExtractAssistantTextErrorResult(t *testing.T) {
	// Synthesize a channel with only an error result frame.
	messages := make(chan SDKMessage)
	go func() {
		messages <- SDKMessage{
			Type:    "result",
			Subtype: "error",
			Raw: json.RawMessage(`{
				"type": "result",
				"subtype": "error",
				"is_error": true,
				"error": "oops",
				"duration_ms": 500,
				"total_cost_usd": 0
			}`),
		}
		close(messages)
	}()

	text, meta, err := ExtractAssistantText(messages)
	if err != nil {
		t.Fatalf("ExtractAssistantText: %v", err)
	}
	if text != "" {
		t.Errorf("assistant text: got %q, want empty string", text)
	}
	if meta == nil {
		t.Fatal("ResultMeta is nil")
	}
	if meta.Subtype != "error" {
		t.Errorf("subtype: got %q, want \"error\"", meta.Subtype)
	}
	if !meta.IsError {
		t.Errorf("IsError: got false, want true")
	}
	if meta.ErrorMessage != "oops" {
		t.Errorf("ErrorMessage: got %q, want \"oops\"", meta.ErrorMessage)
	}
}

// TestExtractAssistantTextNoResultFrame verifies that a channel without
// a result frame returns an error.
func TestExtractAssistantTextNoResultFrame(t *testing.T) {
	// Synthesize a channel with only an assistant frame, then close.
	messages := make(chan SDKMessage)
	go func() {
		messages <- SDKMessage{
			Type: "assistant",
			Message: json.RawMessage(`{
				"content": [{"type": "text", "text": "hello"}]
			}`),
		}
		close(messages)
	}()

	_, _, err := ExtractAssistantText(messages)
	if err == nil {
		t.Error("ExtractAssistantText: expected error, got nil")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("error: got %v, want to wrap io.ErrUnexpectedEOF", err)
	}
}

// TestExtractAssistantTextEmptyChannel verifies that an empty channel
// (just close, no frames) returns an error.
func TestExtractAssistantTextEmptyChannel(t *testing.T) {
	messages := make(chan SDKMessage)
	close(messages)

	_, _, err := ExtractAssistantText(messages)
	if err == nil {
		t.Error("ExtractAssistantText: expected error, got nil")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("error: got %v, want to wrap io.ErrUnexpectedEOF", err)
	}
}
