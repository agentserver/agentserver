package browsergateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

type permissionTestBackend struct {
	dshFakeBackend
	mu      sync.Mutex
	revoked bool
}

func (b *permissionTestBackend) ListSessions(ctx context.Context, token, workspace string) (corecontract.ListUserSessionsResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.revoked {
		return corecontract.ListUserSessionsResponse{}, errors.New("access revoked")
	}
	return b.dshFakeBackend.ListSessions(ctx, token, workspace)
}
func (b *permissionTestBackend) GetSession(ctx context.Context, token, workspace, session string) (corecontract.UserSessionState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dshFakeBackend.GetSession(ctx, token, workspace, session)
}
func (b *permissionTestBackend) GetJournal(ctx context.Context, token, workspace, session string, cursor int64) (corecontract.UserSessionJournalPage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	page, err := b.dshFakeBackend.GetJournal(ctx, token, workspace, session, cursor)
	page.Entries = append([]corecontract.UserSessionJournalEntry(nil), page.Entries...)
	return page, err
}
func (b *permissionTestBackend) UpdatePermissionMode(ctx context.Context, token, workspace, session string, in corecontract.UpdateUserSessionPermissionModeRequest) (corecontract.UpdateUserSessionPermissionModeResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if in.ExpectedPermissionModeVersion != b.sessions[0].PermissionModeVersion {
		return corecontract.UpdateUserSessionPermissionModeResponse{}, errors.New("CAS conflict")
	}
	if in.PermissionMode == b.sessions[0].PermissionMode {
		return corecontract.UpdateUserSessionPermissionModeResponse{Session: b.sessions[0]}, nil
	}
	return b.dshFakeBackend.UpdatePermissionMode(ctx, token, workspace, session, in)
}

func readPermissionFrame(t *testing.T, stream <-chan any) map[string]any {
	t.Helper()
	deadline := time.After(4 * time.Second)
	for {
		select {
		case value, ok := <-stream:
			if !ok {
				t.Fatal("control closed unexpectedly")
			}
			frame, ok := value.(map[string]any)
			if !ok {
				t.Fatalf("control failed: %+v", value)
			}
			if frame["key"] == "title" {
				continue
			}
			return frame
		case <-deadline:
			t.Fatal("permission control did not update")
		}
	}
}

func TestDSHPermissionControlDurableAcrossReplicasAndRepeatedChanges(t *testing.T) {
	for _, withHistory := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty-session", true: "existing-history"}[withHistory], func(t *testing.T) {
			b := &permissionTestBackend{dshFakeBackend: dshFakeBackend{sessions: []corecontract.UserSessionState{{SessionID: projectorSessionID, WorkspaceID: projectorWorkspaceID, PermissionMode: "read-only", PermissionModeVersion: 1, Version: 1, CreatedAt: time.Unix(99, 0)}}}}
			makeGateway := func() *DSHGateway {
				g, err := NewDSHGateway(b, DSHGatewayConfig{WorkspaceID: projectorWorkspaceID})
				if err != nil {
					t.Fatal(err)
				}
				return g
			}
			writer, reader := makeGateway(), makeGateway()
			if _, err := writer.state(t.Context(), "token", projectorSessionID); err != nil {
				t.Fatal(err)
			}
			if withHistory {
				b.journal = append(b.journal, corecontract.UserSessionJournalEntry{Seq: 2, Kind: "prompt", RequestID: "prompt-1", Prompt: &corecontract.UserSessionTranscriptMessage{RunID: projectorRunID, MessageID: "user-1", Role: "user", Complete: true, Content: "hello", CreatedAt: time.Unix(100, 0)}})
				event := backendRunEvent(t, 1, runevent.KindRunCompleted, `{}`)
				b.journal = append(b.journal, corecontract.UserSessionJournalEntry{Seq: 3, Kind: "run_event", Event: &event})
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			stream, err := reader.openStream(ctx, "token", nil, "session/control")
			if err != nil {
				t.Fatal(err)
			}
			baseline := readPermissionFrame(t, stream)
			block := baseline["value"].(map[string]any)["projections"].(map[string]any)[projectorSessionID].(map[string]any)
			seq := block["asOfSeq"].(int64)
			frames := []map[string]any{}
			change := func(mode string) {
				result := dshRequest(t, writer, "commands/execute", `{"agentId":"`+projectorSessionID+`","line":"/permission `+mode+`"}`)["result"].(map[string]any)
				if result["ok"] != true {
					t.Fatalf("command: %+v", result)
				}
			}
			for _, mode := range []string{"full-access", "read-only", "auto", "full-access"} {
				change(mode)
				frame := readPermissionFrame(t, stream)
				next := frame["seq"].(int64)
				if next <= seq || frame["value"].(map[string]any)["currentValue"] != mode {
					t.Fatalf("stale permission: %+v previous=%d", frame, seq)
				}
				seq = next
				frames = append(frames, frame)
			}
			// Two remote changes between polls may return to the same mode. The
			// stream must track durable positions, not just value equality.
			b.mu.Lock()
			for _, mode := range []string{"read-only", "full-access"} {
				_, _ = b.dshFakeBackend.UpdatePermissionMode(t.Context(), "token", projectorWorkspaceID, projectorSessionID, corecontract.UpdateUserSessionPermissionModeRequest{PermissionMode: mode})
			}
			b.mu.Unlock()
			frame := readPermissionFrame(t, stream)
			if frame["seq"].(int64) != seq+2 || frame["value"].(map[string]any)["currentValue"] != "full-access" {
				t.Fatalf("coalesced switches lost: %+v", frame)
			}
			seq = frame["seq"].(int64)
			frames = append(frames, frame)
			// An idempotent selection must not manufacture a new journal entry.
			change("full-access")
			state, _ := writer.state(t.Context(), "token", projectorSessionID)
			if state.lastSeq() != seq {
				t.Fatal("no-op changed the sequence")
			}
			live, _, _ := state.snapshot()
			restarted := makeGateway()
			restored, err := restarted.state(t.Context(), "token", projectorSessionID)
			if err != nil {
				t.Fatal(err)
			}
			replay, _, tail := restored.snapshot()
			if tail != seq || !reflect.DeepEqual(live, replay) {
				t.Fatal("restart changed the event sequence")
			}
			runPermissionConsumer(t, block, frames, restarted.sessionProjection(restored.permissionMode(), tail), live)
			// A hot local cache must not keep serving after membership is revoked.
			b.mu.Lock()
			b.revoked = true
			b.mu.Unlock()
			for {
				select {
				case value := <-stream:
					if frame, ok := value.(map[string]any); ok && frame["key"] == "title" {
						continue
					}
					if _, ok := value.(dshFollowFailure); !ok {
						t.Fatalf("revoked stream continued: %+v", value)
					}
					return
				case <-time.After(4 * time.Second):
					t.Fatal("revoked stream was not stopped")
				}
			}
		})
	}
}

func runPermissionConsumer(t *testing.T, baseline map[string]any, frames []map[string]any, replay map[string]any, events []dshEvent) {
	t.Helper()
	if _, err := os.Stat("../../dsh-web/dist/plugins/@deepseek-ai/dsh-api-session-controller/client.js"); err != nil {
		if os.Getenv("AGENTSERVER_REQUIRE_DSH_ASSETS") == "1" {
			t.Fatal("build pinned DSH frontend before release tests")
		}
		t.Log("official consumer check omitted: frontend assets not built")
		return
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"baseline": baseline, "frames": frames, "replay": replay, "events": events})
	command := exec.CommandContext(t.Context(), node, "../../dsh-web/test/permission-projection.mjs")
	command.Stdin = bytes.NewReader(raw)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("official permission consumer: %v\n%s", err, output)
	}
}
