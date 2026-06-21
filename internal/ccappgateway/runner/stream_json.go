package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// SDKMessage is the wire shape claude --print --output-format stream-json emits.
// We keep raw json.RawMessage for the body so we don't have to mirror every
// content-block shape; runner/events.go (Task 5b) does the keep/drop classification.
type SDKMessage struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	UUID      string          `json:"uuid,omitempty"`
	Message   json.RawMessage `json:"message,omitempty"` // assistant/user frames
	Result    json.RawMessage `json:"result,omitempty"`  // result/* frames
	Raw       json.RawMessage `json:"-"`                  // verbatim line for logging
}

// Decode wraps a reader and returns a channel of SDKMessages. The error
// channel emits at most one error (EOF or parse failure), then closes.
// Caller should range over messages; on close, check the error channel
// for a non-nil error (EOF returns nil on the error channel).
//
// Implementation note: spawn one goroutine; close messages when stdin
// closes; then send the final error (or nil for EOF) and close errors.
func Decode(r io.Reader) (<-chan SDKMessage, <-chan error) {
	messages := make(chan SDKMessage)
	errors := make(chan error, 1)

	go func() {
		defer close(messages)
		defer close(errors)

		scanner := bufio.NewScanner(r)
		// 1 MiB buffer — claude frames are usually <10 KB but stream_event
		// with a full message_start can be larger than the default 64 KB.
		scanner.Buffer(make([]byte, 1<<20), 1<<20)

		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" {
				continue // skip blank / whitespace-only lines
			}

			var msg SDKMessage
			raw := scanner.Bytes()
			// Copy bytes — Scanner reuses its internal buffer.
			rawCopy := make([]byte, len(raw))
			copy(rawCopy, raw)

			if err := json.Unmarshal(rawCopy, &msg); err != nil {
				errors <- fmt.Errorf("json parse error: %w", err)
				return
			}
			msg.Raw = json.RawMessage(rawCopy)
			messages <- msg
		}

		if err := scanner.Err(); err != nil {
			errors <- err
			return
		}
		// Clean EOF — send nil so callers know it was not an error.
		errors <- nil
	}()

	return messages, errors
}

// sdkUserContent is the inner content block for EncodeUserMessage.
type sdkUserContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// sdkUserMessageBody is the message body for EncodeUserMessage.
type sdkUserMessageBody struct {
	Role    string           `json:"role"`
	Content []sdkUserContent `json:"content"`
}

// sdkUserFrame is the top-level frame for EncodeUserMessage.
type sdkUserFrame struct {
	Type    string             `json:"type"`
	Message sdkUserMessageBody `json:"message"`
}

// EncodeUserMessage writes a single SDKUserMessage line to w (terminated
// with \n) for the user message text. Format from Phase 0 PoC:
//
//	{"type":"user","message":{"role":"user","content":[{"type":"text","text":"..."}]}}
//
// The text is JSON-escaped automatically by encoding/json (handles
// quotes, newlines, unicode).
func EncodeUserMessage(w io.Writer, text string) error {
	frame := sdkUserFrame{
		Type: "user",
		Message: sdkUserMessageBody{
			Role: "user",
			Content: []sdkUserContent{
				{Type: "text", Text: text},
			},
		},
	}

	b, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("EncodeUserMessage marshal: %w", err)
	}

	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}
