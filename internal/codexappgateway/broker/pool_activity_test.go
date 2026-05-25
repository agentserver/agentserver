package broker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// TestPoolDoesNotReapConnWithActiveTraffic exercises the reap-mid-turn bug:
// a Turn that takes longer than idleTTL but receives item/completed frames
// throughout must keep its conn alive. The reaper's idleness signal is
// "no ws activity for idleTTL", not "no Get() call for idleTTL" — frames
// flowing in either direction prove the conn is in use even if no outer
// Get/Touch was made.
func TestPoolDoesNotReapConnWithActiveTraffic(t *testing.T) {
	const (
		idleTTL    = 150 * time.Millisecond
		turnLength = 450 * time.Millisecond // 3x idleTTL — guarantees reap if conn looks idle
		itemEvery  = 30 * time.Millisecond
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		// handshake
		init := readFrame(t, ctx, c)
		writeJSON(t, ctx, c, map[string]any{"jsonrpc": "2.0", "id": init["id"], "result": map[string]any{}})
		readFrame(t, ctx, c) // initialized

		for {
			f, err := readNoFatal(ctx, c)
			if err != nil {
				return
			}
			switch f["method"] {
			case "thread/resume":
				writeJSON(t, ctx, c, map[string]any{"jsonrpc": "2.0", "id": f["id"], "result": map[string]any{}})
			case "turn/start":
				const turnID = "trn-long"
				// Ack turn/start.
				writeJSON(t, ctx, c, map[string]any{"jsonrpc": "2.0", "id": f["id"], "result": map[string]any{"turn": map[string]any{"id": turnID}}})
				// Drip items across `turnLength`. Each frame counts as ws
				// activity and must reset the reaper deadline.
				deadline := time.Now().Add(turnLength)
				tick := time.NewTicker(itemEvery)
				for time.Now().Before(deadline) {
					select {
					case <-ctx.Done():
						tick.Stop()
						return
					case <-tick.C:
					}
					writeJSON(t, ctx, c, map[string]any{
						"jsonrpc": "2.0",
						"method":  "item/completed",
						"params": map[string]any{
							"threadId": "thr-x",
							"turnId":   turnID,
							"item":     map[string]any{"type": "agentMessage", "text": "tick"},
						},
					})
				}
				tick.Stop()
				// Finish.
				writeJSON(t, ctx, c, map[string]any{
					"jsonrpc": "2.0",
					"method":  "turn/completed",
					"params": map[string]any{
						"threadId": "thr-x",
						"turn": map[string]any{
							"id":        turnID,
							"status":    "completed",
							"items":     []any{},
							"itemsView": "full",
							"error":     nil,
						},
					},
				})
			}
		}
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	resolver := func(_ context.Context, _ string) (string, *atomic.Int64, error) { return wsURL, nil, nil }
	p := NewPool(resolver, idleTTL)
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := p.Get(ctx, "ws-A")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := conn.Turn(ctx, "thr-x", json.RawMessage(`{"input":[]}`), 5*time.Second); err != nil {
		t.Fatalf("Turn errored — reaper killed conn mid-flight: %v", err)
	}
}
