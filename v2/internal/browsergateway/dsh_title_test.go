package browsergateway

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func (b *permissionTestBackend) UpdateSession(ctx context.Context, token, workspace, session string, request corecontract.UpdateUserSessionRequest) (corecontract.UpdateUserSessionResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dshFakeBackend.UpdateSession(ctx, token, workspace, session, request)
}

func TestDSHTitleProjectionLiveRenameAndReplicaReplay(t *testing.T) {
	b := &permissionTestBackend{dshFakeBackend: dshFakeBackend{sessions: []corecontract.UserSessionState{{SessionID: projectorSessionID, WorkspaceID: projectorWorkspaceID, Title: "New session", TitleSource: "placeholder", TitleVersion: 1, PermissionMode: "read-only", PermissionModeVersion: 1, Version: 2}}}}
	now := time.Unix(100, 0)
	b.journal = []corecontract.UserSessionJournalEntry{
		{Seq: 1, Kind: "permission", PermissionMode: "read-only", PermissionVersion: 1, CreatedAt: now},
		{Seq: 2, Kind: "title", Title: "New session", TitleSource: "placeholder", TitleVersion: 1, CreatedAt: now},
		{Seq: 3, Kind: "prompt", Prompt: &corecontract.UserSessionTranscriptMessage{RunID: projectorRunID, MessageID: "user-1", Content: "检查权限为什么没变化", Role: "user", Complete: true, CreatedAt: now}},
	}
	makeGateway := func() *DSHGateway {
		g, err := NewDSHGateway(b, DSHGatewayConfig{WorkspaceID: projectorWorkspaceID})
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	writer, reader := makeGateway(), makeGateway()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := reader.openStream(ctx, "token", nil, "session/control")
	if err != nil {
		t.Fatal(err)
	}
	baseline := readPermissionFrame(t, stream)["value"].(map[string]any)["projections"].(map[string]any)[projectorSessionID].(map[string]any)
	var frames []map[string]any
	readTitle := func(want string) {
		deadline := time.After(4 * time.Second)
		for {
			select {
			case value := <-stream:
				frame, ok := value.(map[string]any)
				if !ok {
					t.Fatalf("control failed: %+v", value)
				}
				if frame["key"] != "title" {
					continue
				}
				if frame["value"] != want {
					t.Fatalf("title = %+v, want %s", frame, want)
				}
				frames = append(frames, frame)
				return
			case <-deadline:
				t.Fatal("title was not pushed")
			}
		}
	}
	for _, revision := range []struct{ title, source string }{{"检查权限为什么没变化", "fallback"}, {"修复权限同步", "generated"}} {
		b.mu.Lock()
		b.sessions[0].Title = revision.title
		b.sessions[0].TitleSource = revision.source
		b.sessions[0].TitleVersion++
		b.journal = append(b.journal, corecontract.UserSessionJournalEntry{Seq: int64(len(b.journal) + 1), Kind: "title", Title: revision.title, TitleSource: revision.source, TitleVersion: b.sessions[0].TitleVersion, CreatedAt: now})
		b.mu.Unlock()
		readTitle(revision.title)
	}
	rename := dshRequest(t, writer, "session/rename", `{"request":{"sessionId":"`+projectorSessionID+`","title":"我的手动标题"}}`)["result"].(map[string]any)
	if rename["ok"] != true {
		t.Fatalf("rename: %+v", rename)
	}
	readTitle("我的手动标题")
	state, err := reader.state(t.Context(), "token", projectorSessionID)
	if err != nil {
		t.Fatal(err)
	}
	events, _, tail := state.snapshot()
	restarted := makeGateway()
	replayed, err := restarted.state(t.Context(), "token", projectorSessionID)
	if err != nil {
		t.Fatal(err)
	}
	replay, _, replayTail := replayed.snapshot()
	if tail != replayTail || !reflect.DeepEqual(events, replay) {
		t.Fatal("title replay changed cursors")
	}
	row := reader.summary(b.sessions[0])
	if row["projections"].(map[string]any)["values"].(map[string]any)["title"] != "我的手动标题" {
		t.Fatal("list lost stored title")
	}
	if _, err := os.Stat("../../dsh-web/dist/plugins/@deepseek-ai/dsh-api-session-controller/client.js"); err != nil {
		if os.Getenv("AGENTSERVER_REQUIRE_DSH_ASSETS") == "1" {
			t.Fatal(err)
		}
		t.Log("frontend assets absent; official consumer not run")
		return
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"baseline": baseline, "frames": frames, "events": events, "replay": reader.sessionProjection(state.permissionMode(), tail, state.title()), "list": row})
	command := exec.CommandContext(t.Context(), node, "../../dsh-web/test/title-projection.mjs")
	command.Stdin = bytes.NewReader(raw)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("official title consumer: %v\n%s", err, output)
	}
}
