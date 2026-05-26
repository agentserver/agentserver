// internal/codexappgateway/scheduler/spawn_test.go
package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexappgateway/broker"
	"nhooyr.io/websocket"
)

// fakeCodexAppServer is the minimal ws state machine BrokerSpawner needs:
// initialize → thread/start → thread/resume → turn/start → push item/completed
// + turn/completed. Each test tunes the fakeServer fields and routes the
// real broker.Conn through it, exercising the actual broker protocol code.
type fakeServer struct {
	assistantText string // text returned in the agentMessage item; "" → no item emitted
	turnStatus    string // turn/completed status; defaults to "completed"
	turnErrorMsg  string // when set, included in turn.error.message
	turnRPCError  bool   // when true, reply turn/start with an RPC error
	url           string
}

func newFakeCodexAppServer(t *testing.T, fs *fakeServer) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()

		init, err := readFrame(ctx, c)
		if err != nil {
			return
		}
		if err := writeJSON(ctx, c, map[string]any{"jsonrpc": "2.0", "id": init["id"], "result": map[string]any{}}); err != nil {
			return
		}
		if _, err := readFrame(ctx, c); err != nil { // initialized notification
			return
		}

		for {
			f, err := readFrame(ctx, c)
			if err != nil {
				return
			}
			switch f["method"] {
			case "thread/start":
				_ = writeJSON(ctx, c, map[string]any{
					"jsonrpc": "2.0",
					"id":      f["id"],
					"result": map[string]any{
						"thread": map[string]any{"id": "thr-fake", "sessionId": "sess", "createdAt": 0, "updatedAt": 0},
					},
				})
			case "thread/resume":
				_ = writeJSON(ctx, c, map[string]any{"jsonrpc": "2.0", "id": f["id"], "result": map[string]any{}})
			case "turn/start":
				if fs.turnRPCError {
					_ = writeJSON(ctx, c, map[string]any{
						"jsonrpc": "2.0",
						"id":      f["id"],
						"error":   map[string]any{"code": -32600, "message": "synthetic rpc error"},
					})
					continue
				}
				_ = writeJSON(ctx, c, map[string]any{
					"jsonrpc": "2.0",
					"id":      f["id"],
					"result":  map[string]any{"turn": map[string]any{"id": "trn-fake"}},
				})
				if fs.assistantText != "" {
					_ = writeJSON(ctx, c, map[string]any{
						"jsonrpc": "2.0",
						"method":  "item/completed",
						"params": map[string]any{
							"threadId": "thr-fake",
							"turnId":   "trn-fake",
							"item":     map[string]any{"type": "agentMessage", "id": "m1", "text": fs.assistantText},
						},
					})
				}
				status := fs.turnStatus
				if status == "" {
					status = "completed"
				}
				turn := map[string]any{
					"id":        "trn-fake",
					"status":    status,
					"items":     []any{},
					"itemsView": "full",
					"error":     nil,
				}
				if fs.turnErrorMsg != "" {
					turn["error"] = map[string]any{"message": fs.turnErrorMsg}
				}
				_ = writeJSON(ctx, c, map[string]any{
					"jsonrpc": "2.0",
					"method":  "turn/completed",
					"params":  map[string]any{"threadId": "thr-fake", "turn": turn},
				})
			}
		}
	}))
	t.Cleanup(srv.Close)
	fs.url = "ws" + strings.TrimPrefix(srv.URL, "http")
}

func readFrame(ctx context.Context, c *websocket.Conn) (map[string]any, error) {
	_, data, err := c.Read(ctx)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func writeJSON(ctx context.Context, c *websocket.Conn, m map[string]any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return c.Write(ctx, websocket.MessageText, b)
}

func newTestPool(t *testing.T, wsURL string) *broker.Pool {
	t.Helper()
	resolver := func(_ context.Context, _ string) (string, *atomic.Int64, error) {
		return wsURL, nil, nil
	}
	p := broker.NewPool(resolver, time.Minute)
	t.Cleanup(p.Close)
	return p
}

func TestBrokerSpawner_HappyPath_ReturnsAssistantText(t *testing.T) {
	fs := &fakeServer{assistantText: "the answer is 42"}
	newFakeCodexAppServer(t, fs)
	s := NewBrokerSpawner(newTestPool(t, fs.url))

	res, err := s.Run(context.Background(), SpawnInput{
		WorkspaceID: "ws-a",
		Prompt:      "what is 6*7?",
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode=%d want 0; summary=%q", res.ExitCode, res.Summary)
	}
	if res.Summary != "the answer is 42" {
		t.Errorf("Summary=%q", res.Summary)
	}
	if res.TimedOut {
		t.Error("TimedOut=true unexpectedly")
	}
}

func TestBrokerSpawner_TurnFailed_ReportsError(t *testing.T) {
	fs := &fakeServer{turnStatus: "failed", turnErrorMsg: "model overloaded"}
	newFakeCodexAppServer(t, fs)
	s := NewBrokerSpawner(newTestPool(t, fs.url))

	res, err := s.Run(context.Background(), SpawnInput{
		WorkspaceID: "ws-a",
		Prompt:      "hi",
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode == 0 {
		t.Errorf("ExitCode=0 want non-zero")
	}
	if !strings.Contains(res.Summary, "model overloaded") {
		t.Errorf("Summary=%q expected to contain 'model overloaded'", res.Summary)
	}
}

func TestBrokerSpawner_RPCError_ReportsFailure(t *testing.T) {
	fs := &fakeServer{turnRPCError: true}
	newFakeCodexAppServer(t, fs)
	s := NewBrokerSpawner(newTestPool(t, fs.url))

	res, err := s.Run(context.Background(), SpawnInput{
		WorkspaceID: "ws-a",
		Prompt:      "hi",
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode == 0 {
		t.Errorf("ExitCode=0 want non-zero")
	}
	if !strings.Contains(res.Summary, "synthetic rpc error") {
		t.Errorf("Summary=%q expected to surface RPC error message", res.Summary)
	}
}

func TestBrokerSpawner_EmptyWorkspaceID_FastFails(t *testing.T) {
	s := NewBrokerSpawner(nil) // pool unused — empty workspace fails before pool.Get
	res, err := s.Run(context.Background(), SpawnInput{Prompt: "x"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode == 0 {
		t.Errorf("expected non-zero exit; got %+v", res)
	}
}
