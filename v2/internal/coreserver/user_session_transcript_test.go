package coreserver

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

func TestProjectUserSessionTranscriptRunRestoresCommittedMessages(t *testing.T) {
	now := time.Date(2026, 8, 5, 2, 0, 0, 0, time.UTC)
	run := coredb.UserSessionTranscriptRun{ID: userRunID, Status: coredb.RunStatusCompleted, CreatedAt: now}
	events := []coredb.UserSessionTranscriptEvent{
		transcriptTestEvent(1, runevent.KindAssistantMessageStarted, `{"messageId":"assistant-1","role":"assistant"}`, now.Add(time.Second)),
		transcriptTestEvent(2, runevent.KindAssistantMessageDelta, `{"messageId":"assistant-1","delta":"你"}`, now.Add(2*time.Second)),
		transcriptTestEvent(3, runevent.KindAssistantMessageDelta, `{"messageId":"assistant-1","delta":"好！"}`, now.Add(3*time.Second)),
		transcriptTestEvent(4, runevent.KindAssistantMessageCompleted, `{"messageId":"assistant-1"}`, now.Add(4*time.Second)),
	}
	messages, usedBytes, usedMessages, truncated, err := projectUserSessionTranscriptRun(
		userRunWorkspaceID, userRunSessionID, run, "你好", events, 1024, 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || usedMessages != 2 || usedBytes != len("你好你好！") || len(messages) != 2 ||
		messages[0].Role != "user" || messages[0].Content != "你好" || !messages[0].Complete ||
		messages[1].Role != "assistant" || messages[1].Content != "你好！" || !messages[1].Complete {
		t.Fatalf("transcript projection = messages %+v, bytes=%d count=%d truncated=%v", messages, usedBytes, usedMessages, truncated)
	}
}

func TestProjectUserSessionTranscriptRunMarksInterruptedAndBoundedContent(t *testing.T) {
	now := time.Date(2026, 8, 5, 2, 0, 0, 0, time.UTC)
	run := coredb.UserSessionTranscriptRun{ID: userRunID, Status: coredb.RunStatusFailed, CreatedAt: now}
	events := []coredb.UserSessionTranscriptEvent{
		transcriptTestEvent(1, runevent.KindAssistantMessageStarted, `{"messageId":"assistant-1","role":"assistant"}`, now.Add(time.Second)),
		transcriptTestEvent(2, runevent.KindAssistantMessageDelta, `{"messageId":"assistant-1","delta":"partial"}`, now.Add(2*time.Second)),
	}
	messages, usedBytes, _, truncated, err := projectUserSessionTranscriptRun(
		userRunWorkspaceID, userRunSessionID, run, "你好", events, 4, 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || usedBytes != len("你") || len(messages) != 1 || messages[0].Content != "你" || messages[0].Complete {
		t.Fatalf("bounded transcript = messages %+v, bytes=%d truncated=%v", messages, usedBytes, truncated)
	}
}

func transcriptTestEvent(sequence int64, kind, payload string, createdAt time.Time) coredb.UserSessionTranscriptEvent {
	attemptID := "a1000000-0000-4000-8000-000000000001"
	generation := int64(1)
	return coredb.UserSessionTranscriptEvent{
		RunID: userRunID,
		Event: coredb.RunEvent{
			EventID: "e1000000-0000-4000-8000-" + transcriptSequenceSuffix(sequence),
			Seq:     sequence, RunAttemptID: &attemptID, RunAttemptGeneration: &generation,
			ProducerInstanceID: "b1000000-0000-4000-8000-000000000001",
			ProducerSeq:        sequence, Source: coredb.EventSourceBrain, Kind: kind,
			SchemaVersion: runevent.CurrentSchemaVersion, Payload: json.RawMessage(payload), CreatedAt: createdAt,
		},
	}
}

func transcriptSequenceSuffix(sequence int64) string {
	const digits = "000000000000"
	value := []byte(digits)
	value[len(value)-1] = byte('0' + sequence)
	return string(value)
}
