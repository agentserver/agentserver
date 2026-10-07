package browsergateway

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

type journalTestBackend struct {
	dshFakeBackend
	mu        sync.Mutex
	events    []runevent.Event
	pageSize  int
	readError error
}

func (b *journalTestBackend) GetJournal(_ context.Context, _ string, workspaceID, sessionID, runID string, after int64) (corecontract.UserSessionJournalPage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.readError != nil {
		return corecontract.UserSessionJournalPage{}, b.readError
	}
	page := corecontract.UserSessionJournalPage{Session: b.sessions[0], RunID: projectorRunID, AfterSeq: after}
	page.Session.WorkspaceID = workspaceID
	if runID == "" {
		page.RequestID = "prompt-rpc-1"
		page.Prompt = &corecontract.UserSessionTranscriptMessage{MessageID: "user-" + projectorRunID, RunID: projectorRunID, Role: "user", Content: "列出所有执行环境", Complete: true, CreatedAt: time.Unix(100, 0)}
	}
	end := len(b.events)
	if b.pageSize > 0 && int(after)+b.pageSize < end {
		end = int(after) + b.pageSize
		page.HasMore = true
	}
	page.Events = append([]runevent.Event(nil), b.events[int(after):end]...)
	page.AfterSeq = int64(end)
	return page, nil
}

func journalGateway(t *testing.T, b *journalTestBackend) *DSHGateway {
	t.Helper()
	g, err := NewDSHGateway(b, DSHGatewayConfig{WorkspaceID: projectorWorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func journalBackend() *journalTestBackend {
	return &journalTestBackend{dshFakeBackend: dshFakeBackend{sessions: []corecontract.UserSessionState{{SessionID: projectorSessionID, WorkspaceID: projectorWorkspaceID, PermissionMode: "full-access", CreatedAt: time.Unix(99, 0)}}}, pageSize: 2}
}

func TestDSHJournalLiveReplayAndReplicaSwitchKeepIdenticalCursors(t *testing.T) {
	b := journalBackend()
	first := journalGateway(t, b)
	state, err := first.state(t.Context(), "user-token", projectorSessionID)
	if err != nil {
		t.Fatal(err)
	}
	events := []runevent.Event{
		backendRunEvent(t, 1, runevent.KindToolCallStarted, `{"toolCallId":"tool1","toolCallName":"executor.list_environments"}`),
		backendRunEvent(t, 2, runevent.KindToolCallArguments, `{"toolCallId":"tool1","delta":"{}"}`),
		backendRunEvent(t, 3, runevent.KindToolCallCompleted, `{"toolCallId":"tool1"}`),
		backendRunEvent(t, 4, runevent.KindToolCallResult, `{"toolCallId":"tool1","messageId":"tool1","content":"environments: []"}`),
		backendRunEvent(t, 5, runevent.KindRunFailed, `{"code":"worker_runtime_failed","message":"executor connection interrupted"}`),
	}
	for _, event := range events {
		b.events = append(b.events, event)
		if err := first.refreshJournal(t.Context(), "user-token", state); err != nil {
			t.Fatal(err)
		}
	}
	live, _, cursor := state.snapshot()
	other := journalGateway(t, b)
	restored, err := other.state(t.Context(), "user-token", projectorSessionID)
	if err != nil {
		t.Fatal(err)
	}
	replay, _, replayCursor := restored.snapshot()
	if cursor != replayCursor || !reflect.DeepEqual(live, replay) {
		t.Fatalf("replica replay changed journal: live=%+v replay=%+v", live, replay)
	}
	if cursor != 6 {
		t.Fatalf("lost tool/terminal events: cursor=%d", cursor)
	}
	if live[2].Data.(map[string]any)["source"].(map[string]any)["rpcId"] != "prompt-rpc-1" {
		t.Fatal("durable prompt did not acknowledge client submission")
	}
	reason := replay[len(replay)-1].Data.(map[string]any)["reason"].(map[string]any)
	if reason["kind"] != "error" {
		t.Fatalf("failed run hidden as success: %+v", reason)
	}
	if err := other.refreshJournal(t.Context(), "user-token", restored); err != nil || restored.lastSeq() != cursor {
		t.Fatalf("duplicate refresh: cursor=%d err=%v", restored.lastSeq(), err)
	}
}

func TestDSHJournalFollowerObservesRunWrittenThroughAnotherReplica(t *testing.T) {
	b := journalBackend()
	g := journalGateway(t, b)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	args := map[string]json.RawMessage{"request": json.RawMessage(`{"address":{"sessionId":"` + projectorSessionID + `"},"assistantStream":true}`)}
	stream, err := g.openStream(ctx, "user-token", args, "session/follow")
	if err != nil {
		t.Fatal(err)
	}
	<-stream
	b.mu.Lock()
	b.events = append(b.events, backendRunEvent(t, 1, runevent.KindAssistantMessageStarted, `{"messageId":"m1","role":"assistant"}`), backendRunEvent(t, 2, runevent.KindAssistantMessageDelta, `{"messageId":"m1","delta":"你好"}`))
	b.mu.Unlock()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case value := <-stream:
			raw, _ := json.Marshal(value)
			var item struct {
				Type  string `json:"type"`
				Frame struct {
					Chunk struct {
						Text string `json:"text"`
					} `json:"chunk"`
				} `json:"frame"`
			}
			json.Unmarshal(raw, &item)
			if item.Type == "assistant-stream" && item.Frame.Chunk.Text == "你好" {
				return
			}
		case <-deadline.C:
			t.Fatal("follower never observed remote write")
		}
	}
}

func TestDSHJournalReadFailureDoesNotInstallEmptyHistory(t *testing.T) {
	b := journalBackend()
	b.readError = errors.New("Core unavailable")
	g := journalGateway(t, b)
	if _, err := g.state(t.Context(), "user-token", projectorSessionID); err == nil {
		t.Fatal("read failure swallowed")
	}
	b.readError = nil
	state, err := g.state(t.Context(), "user-token", projectorSessionID)
	if err != nil || state.lastSeq() != 2 {
		t.Fatalf("retry did not restore prompt: %v", err)
	}
}

func TestDSHJournalRejectsGapsAndCrossSessionPagesBeforeMutation(t *testing.T) {
	b := journalBackend()
	page, _ := b.GetJournal(t.Context(), "user-token", projectorWorkspaceID, projectorSessionID, "", 0)
	page.Events = []runevent.Event{backendRunEvent(t, 2, runevent.KindRunCompleted, `{}`)}
	page.AfterSeq = 2
	if err := validateDSHJournalPage(page, projectorWorkspaceID, projectorSessionID, "", 0); err == nil {
		t.Fatal("accepted skipped event")
	}
	page.Events = nil
	page.AfterSeq = 0
	page.Session.SessionID = "other"
	if err := validateDSHJournalPage(page, projectorWorkspaceID, projectorSessionID, "", 0); err == nil {
		t.Fatal("accepted another session")
	}
}
