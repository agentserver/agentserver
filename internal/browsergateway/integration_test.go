package browsergateway

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// fakeCXG is a stand-in for codex-app-gateway /codex-app/ws: it speaks the
// codex v2 protocol and emits one agentMessage + turn/completed per turn.
func fakeCXG(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-xyz" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		type req struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		reply := func(id int64, result string) {
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": json.RawMessage(result)})
			_ = c.Write(ctx, websocket.MessageText, b)
		}
		notify := func(method, params string) {
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": json.RawMessage(params)})
			_ = c.Write(ctx, websocket.MessageText, b)
		}
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var m req
			_ = json.Unmarshal(data, &m)
			switch m.Method {
			case "initialize":
				reply(*m.ID, `{}`)
			case "thread/start":
				reply(*m.ID, `{"thread":{"id":"thr-1"}}`)
			case "turn/start":
				reply(*m.ID, `{"turn":{"id":"trn-1"}}`)
				notify("item/started", `{"item":{"type":"agentMessage","id":"msg-1","text":"","phase":null},"threadId":"thr-1","turnId":"trn-1","startedAtMs":1}`)
				notify("item/agentMessage/delta", `{"threadId":"thr-1","turnId":"trn-1","itemId":"msg-1","delta":"Hello!"}`)
				notify("item/completed", `{"item":{"type":"agentMessage","id":"msg-1","text":"Hello!","phase":"final_answer"},"threadId":"thr-1","turnId":"trn-1","completedAtMs":2}`)
				notify("turn/completed", `{"threadId":"thr-1","turn":{"id":"trn-1","status":"completed","items":[],"error":null}}`)
			}
		}
	}))
}

func TestIntegration_TextRun(t *testing.T) {
	cxg := fakeCXG(t)
	defer cxg.Close()
	wsBase := "ws" + strings.TrimPrefix(cxg.URL, "http")

	srv := NewServer(ServeConfig{CodexAppGatewayWSURL: wsBase, AllowedOrigins: []string{"*"}}, slog.Default())
	bg := httptest.NewServer(srv.Handler())
	defer bg.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	body := `{"threadId":"","runId":"run-1","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, bg.URL+"/agui", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok-xyz")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /agui: %v", err)
	}
	defer resp.Body.Close()

	var eventTypes []string
	var sawHello bool
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			continue
		}
		eventTypes = append(eventTypes, ev.Type)
		if ev.Delta == "Hello!" {
			sawHello = true
		}
		if ev.Type == "RUN_FINISHED" {
			break
		}
	}

	joined := strings.Join(eventTypes, ",")
	for _, want := range []string{"RUN_STARTED", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END", "RUN_FINISHED"} {
		if !strings.Contains(joined, want) {
			t.Errorf("event stream missing %q; got [%s]", want, joined)
		}
	}
	if !sawHello {
		t.Errorf("did not see the assistant text delta 'Hello!'")
	}
}
