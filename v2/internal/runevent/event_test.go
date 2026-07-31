package runevent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecodeCanonicalRunEvent(t *testing.T) {
	raw := []byte(`{
        "eventId":"10000000-0000-4000-8000-000000000001",
        "schemaVersion":1,
        "seq":7,
        "workspaceId":"20000000-0000-4000-8000-000000000002",
        "sessionId":"30000000-0000-4000-8000-000000000003",
        "runId":"40000000-0000-4000-8000-000000000004",
        "runAttemptId":"50000000-0000-4000-8000-000000000005",
        "runAttemptGeneration":2,
        "producerInstanceId":"60000000-0000-4000-8000-000000000006",
        "producerSeq":9,
        "source":"brain",
        "kind":"assistant.message.delta",
        "createdAt":"2026-07-31T12:00:00Z",
        "payload":{"messageId":"message-1","delta":"hello"}
    }`)
	event, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if event.Seq != 7 || event.RunAttemptGeneration == nil || *event.RunAttemptGeneration != 2 {
		t.Fatalf("decoded event = %+v", event)
	}
	var payload MessageDeltaPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.Delta != "hello" {
		t.Fatalf("decoded payload = %+v, %v", payload, err)
	}
}

func TestDecodeCanonicalRunEventRejectsAmbiguousJSONAndScope(t *testing.T) {
	valid := string(mustTestEventJSON(t))
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{name: "duplicate", raw: strings.Replace(valid, `"seq":1`, `"seq":1,"seq":2`, 1), wantErr: "duplicate"},
		{name: "unknown", raw: strings.Replace(valid, `"seq":1`, `"future":true,"seq":1`, 1), wantErr: "unknown field"},
		{name: "half attempt scope", raw: strings.Replace(valid, `"runAttemptGeneration":1`, `"runAttemptGeneration":null`, 1), wantErr: "both"},
		{name: "two bodies", raw: strings.Replace(valid, `"payload":`, `"object":{"objectId":"70000000-0000-4000-8000-000000000007","sha256":"`+strings.Repeat("a", 64)+`","size":1,"mediaType":"text/plain"},"payload":`, 1), wantErr: "exactly one"},
		{name: "array payload", raw: strings.Replace(valid, `{"messageId":"message-1"}`, `[]`, 1), wantErr: "JSON object"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Decode([]byte(test.raw)); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Decode() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestDecodeSemanticPayloadFailsClosedForKnownKinds(t *testing.T) {
	event, err := Decode(mustTestEventJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := DecodeSemanticPayload(event)
	if err != nil {
		t.Fatalf("DecodeSemanticPayload() error = %v", err)
	}
	if got := payload.(MessageCompletedPayload).MessageID; got != "message-1" {
		t.Fatalf("messageId = %q", got)
	}

	tests := []struct {
		name    string
		mutate  func(*Event)
		wantErr string
	}{
		{name: "future schema", mutate: func(event *Event) { event.SchemaVersion = 2 }, wantErr: "unsupported"},
		{name: "unknown payload field", mutate: func(event *Event) {
			event.Payload = json.RawMessage(`{"messageId":"message-1","future":true}`)
		}, wantErr: "unknown field"},
		{name: "invalid identity", mutate: func(event *Event) {
			event.Payload = json.RawMessage(`{"messageId":"bad id"}`)
		}, wantErr: "whitespace"},
		{name: "external known payload", mutate: func(event *Event) {
			event.Payload = nil
			event.Object = &ObjectPointer{
				ObjectID: "70000000-0000-4000-8000-000000000007",
				SHA256:   strings.Repeat("a", 64), Size: 1, MediaType: "application/json",
			}
		}, wantErr: "inline"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := event
			test.mutate(&candidate)
			if _, err := DecodeSemanticPayload(candidate); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("DecodeSemanticPayload() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestDecodeToolCallProgressPayload(t *testing.T) {
	event, err := Decode(mustTestEventJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	event.Kind = KindToolCallProgress
	event.Payload = json.RawMessage(`{"toolCallId":"call-1","progress":2,"total":5,"message":"running"}`)
	payload, err := DecodeSemanticPayload(event)
	if err != nil {
		t.Fatal(err)
	}
	progress := payload.(ToolCallProgressPayload)
	if progress.ToolCallID != "call-1" || progress.Progress != 2 || progress.Total != 5 || progress.Message != "running" {
		t.Fatalf("progress payload = %+v", progress)
	}
	for _, raw := range []string{
		`{"toolCallId":"bad call","progress":1,"total":2}`,
		`{"toolCallId":"call-1","progress":3,"total":2}`,
		`{"toolCallId":"call-1","progress":-1,"total":2}`,
		`{"toolCallId":"call-1","progress":1,"total":2,"future":true}`,
	} {
		event.Payload = json.RawMessage(raw)
		if _, err := DecodeSemanticPayload(event); err == nil {
			t.Fatalf("invalid progress payload was accepted: %s", raw)
		}
	}
}

func TestUnknownFutureKindCanRemainInCanonicalLedger(t *testing.T) {
	event, err := Decode(mustTestEventJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	event.Kind = "artifact.available"
	event.SchemaVersion = 42
	if IsKnownKind(event.Kind) {
		t.Fatalf("future kind %q unexpectedly known", event.Kind)
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("unknown future event should remain envelope-valid: %v", err)
	}
}

func mustTestEventJSON(t *testing.T) []byte {
	t.Helper()
	generation := int64(1)
	attemptID := "50000000-0000-4000-8000-000000000005"
	event := Event{
		EventID: "10000000-0000-4000-8000-000000000001", SchemaVersion: 1, Seq: 1,
		WorkspaceID: "20000000-0000-4000-8000-000000000002",
		SessionID:   "30000000-0000-4000-8000-000000000003", RunID: "40000000-0000-4000-8000-000000000004",
		RunAttemptID: &attemptID, RunAttemptGeneration: &generation,
		ProducerInstanceID: "60000000-0000-4000-8000-000000000006", ProducerSeq: 1,
		Source: "brain", Kind: KindAssistantMessageCompleted, CreatedAt: time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		Payload: json.RawMessage(`{"messageId":"message-1"}`),
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
