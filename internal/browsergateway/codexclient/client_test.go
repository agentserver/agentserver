package codexclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// fakeCodex is a minimal codex app-server: it answers initialize/thread/turn
// RPCs and emits one item/completed + turn/completed notification.
func fakeCodex(t *testing.T) (url, gotBearer string, srv *httptest.Server) {
	t.Helper()
	var bearer string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer = r.Header.Get("Authorization")
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var req rpcRequest
			_ = json.Unmarshal(data, &req)
			switch req.Method {
			case "initialize":
				writeResult(ctx, c, *req.ID, `{}`)
			case "initialized":
				// notification, no reply
			case "thread/start":
				writeResult(ctx, c, *req.ID, `{"thread":{"id":"thr-1"}}`)
			case "turn/start":
				writeResult(ctx, c, *req.ID, `{"turn":{"id":"trn-1"}}`)
				writeNotif(ctx, c, "item/completed", `{"item":{"type":"agentMessage","id":"msg-1","text":"hi"},"threadId":"thr-1","turnId":"trn-1"}`)
				writeNotif(ctx, c, "turn/completed", `{"threadId":"thr-1","turn":{"id":"trn-1","status":"completed","items":[],"error":null}}`)
			}
		}
	}))
	return "ws" + strings.TrimPrefix(srv.URL, "http"), bearer, srv
}

func writeResult(ctx context.Context, c *websocket.Conn, id int64, result string) {
	b, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: &id, Result: json.RawMessage(result)})
	_ = c.Write(ctx, websocket.MessageText, b)
}

func writeNotif(ctx context.Context, c *websocket.Conn, method, params string) {
	b, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: json.RawMessage(params)})
	_ = c.Write(ctx, websocket.MessageText, b)
}

func TestClient_TurnStreamsFrames(t *testing.T) {
	url, _, srv := fakeCodex(t)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := Dial(ctx, url, "tok-123")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	tid, err := c.StartThread(ctx)
	if err != nil || tid != "thr-1" {
		t.Fatalf("StartThread = %q, %v", tid, err)
	}
	turnID, err := c.StartTurn(ctx, tid, "hi")
	if err != nil || turnID != "trn-1" {
		t.Fatalf("StartTurn = %q, %v", turnID, err)
	}

	var methods []string
	for f := range c.Frames() {
		methods = append(methods, f.Method)
		if f.Method == "turn/completed" {
			break
		}
	}
	if len(methods) != 2 || methods[0] != "item/completed" || methods[1] != "turn/completed" {
		t.Fatalf("frames = %v, want [item/completed turn/completed]", methods)
	}
}
