# browser-gateway backend (P0+P1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a working, standalone `browser-gateway` binary that exposes the codex harness as a standard AG-UI endpoint (`POST /agui`, HTTP+SSE) streaming codex text responses, fully unit- and integration-tested and runnable in a container.

**Architecture:** browser-gateway is a ws client of codex-app-gateway's `/codex-app/ws` (zero changes to CXG). Per run it dials codex, starts/resumes a thread, starts a turn from the latest user message, translates codex v2 notification frames into AG-UI events, and streams them over SSE. Bearer pass-through auth; `AG-UI threadId == codex threadId`.

**Tech Stack:** Go 1.26; AG-UI Go SDK (`github.com/ag-ui-protocol/ag-ui/sdks/community/go`) for event types + SSE framing; `nhooyr.io/websocket` (already in go.mod) for the codex ws client; stdlib `net/http` with Go 1.22 method-pattern routing.

**Scope of this plan:** P0 (codex frame fixtures) + P1 (text streaming MVP). Deferred to a follow-up plan: P2 (tool events + A2UI card synthesis), P3 (CopilotKit reference frontend + CI/Helm wiring). See `docs/superpowers/specs/2026-07-20-browser-gateway-design.md`.

## Global Constraints

- Repo module path: `github.com/agentserver/agentserver`. Go version: `go 1.26` (per `go.mod`).
- New Go dependency: `github.com/ag-ui-protocol/ag-ui/sdks/community/go` (declares `go 1.24.4`; runtime deps `github.com/google/uuid`, `github.com/sirupsen/logrus`). Import only `pkg/core/events`, `pkg/core/types`, `pkg/encoding/sse`.
- Gateways are **standalone binaries**: `cmd/<name>/main.go` (plain `flag` parsing, `serve` subcommand) + `internal/<name>/` + `Dockerfile.<name>`. Mirror `cmd/codex-app-gateway/`.
- Env config prefix: `BRG_`, loaded via `LoadServeConfigFromEnv()` (mirror `internal/codexappgateway/config.go`).
- codex v2 wire shapes (verbatim from existing CXG tests — treat as pinned):
  - `thread/start` params `{}` → result `{"thread":{"id":"..."}}`
  - `thread/resume` params `{"threadId":"..."}` (camelCase, required)
  - `turn/start` params `{"threadId":"...","input":[{"type":"text","text":"..."}]}` → result `{"turn":{"id":"..."}}`
  - `turn/interrupt` params `{"threadId":"...","turnId":"..."}`
  - notification `item/completed` params `{"item":{"type":"agentMessage","id":"msg-1","text":"..."},"threadId":"...","turnId":"..."}`
  - notification `turn/completed` params `{"threadId":"...","turn":{"id":"...","status":"completed","items":[...],"error":null,...}}`
- `TextMessageContentEvent.Validate()` rejects an empty `delta` — never emit content events with empty text.
- AG-UI event constructors return concrete `*XxxEvent` values satisfying `events.Event`; hold them as `[]events.Event`.
- TDD: write the failing test first, watch it fail, implement minimally, watch it pass, commit. Run `gofmt -w` on touched files before committing.

---

### Task 1: Add AG-UI Go SDK dependency + package skeleton

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/browsergateway/doc.go`

**Interfaces:**
- Produces: the importable module path `github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/{core/events,core/types,encoding/sse}` and an empty `browsergateway` package other tasks extend.

- [ ] **Step 1: Create the package doc file**

`internal/browsergateway/doc.go`:
```go
// Package browsergateway exposes the codex harness as a standard AG-UI
// (https://github.com/ag-ui-protocol/ag-ui) agent endpoint over HTTP+SSE.
// It is a ws client of codex-app-gateway's /codex-app/ws and translates
// codex v2 notification frames into AG-UI events.
package browsergateway
```

- [ ] **Step 2: Add the SDK dependency**

Run:
```bash
cd /root/agentserver
go get github.com/ag-ui-protocol/ag-ui/sdks/community/go@latest
```
Expected: `go.mod`/`go.sum` gain the module.

If the module proxy cannot resolve the subdirectory-module tag (common for monorepo `sdks/community/go` modules), fall back to a pinned pseudo-version from the git ref:
```bash
go get github.com/ag-ui-protocol/ag-ui/sdks/community/go@main
```
If that also fails in this environment, add a local `replace` to `go.mod` pointing at the clone and record it as a follow-up to replace with a proper version before release:
```
replace github.com/ag-ui-protocol/ag-ui/sdks/community/go => /root/ag-ui/sdks/community/go
```
then `go mod tidy`.

- [ ] **Step 3: Verify it builds and imports resolve**

Create a throwaway check, then delete it:
```bash
cat > /tmp/aguicheck.go <<'EOF'
package main
import (
	"fmt"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
)
func main() { fmt.Println(events.NewRunStartedEvent("t", "r").Type()) }
EOF
go run /tmp/aguicheck.go && rm /tmp/aguicheck.go
```
Expected: prints `RUN_STARTED` (the `EventType` value).

- [ ] **Step 4: Build the empty package**

Run: `go build ./internal/browsergateway/...`
Expected: no output, exit 0.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/browsergateway/doc.go
git add go.mod go.sum internal/browsergateway/doc.go
git commit -m "feat(browser-gateway): add AG-UI Go SDK dependency + package skeleton"
```

---

### Task 2: Config loader (`BRG_*`)

**Files:**
- Create: `internal/browsergateway/config.go`
- Test: `internal/browsergateway/config_test.go`

**Interfaces:**
- Produces:
  - `type ServeConfig struct { ListenAddr string; CodexAppGatewayWSURL string; AllowedOrigins []string; LogLevel slog.Level }`
  - `func LoadServeConfigFromEnv() (ServeConfig, error)`

- [ ] **Step 1: Write the failing test**

`internal/browsergateway/config_test.go`:
```go
package browsergateway

import (
	"log/slog"
	"testing"
)

func TestLoadServeConfigFromEnv_Defaults(t *testing.T) {
	t.Setenv("BRG_CODEX_APP_GATEWAY_WS_URL", "ws://cxg:8086")
	cfg, err := LoadServeConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if cfg.ListenAddr != ":8088" {
		t.Errorf("ListenAddr = %q, want :8088", cfg.ListenAddr)
	}
	if cfg.CodexAppGatewayWSURL != "ws://cxg:8086" {
		t.Errorf("CodexAppGatewayWSURL = %q", cfg.CodexAppGatewayWSURL)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want Info", cfg.LogLevel)
	}
}

func TestLoadServeConfigFromEnv_RequiresWSURL(t *testing.T) {
	t.Setenv("BRG_CODEX_APP_GATEWAY_WS_URL", "")
	if _, err := LoadServeConfigFromEnv(); err == nil {
		t.Fatal("expected error when BRG_CODEX_APP_GATEWAY_WS_URL is unset")
	}
}

func TestLoadServeConfigFromEnv_OriginsAndLevel(t *testing.T) {
	t.Setenv("BRG_CODEX_APP_GATEWAY_WS_URL", "ws://cxg:8086")
	t.Setenv("BRG_ALLOWED_ORIGINS", "https://a.example, https://b.example")
	t.Setenv("BRG_LOG_LEVEL", "debug")
	cfg, err := LoadServeConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(cfg.AllowedOrigins) != 2 || cfg.AllowedOrigins[0] != "https://a.example" || cfg.AllowedOrigins[1] != "https://b.example" {
		t.Errorf("AllowedOrigins = %#v", cfg.AllowedOrigins)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want Debug", cfg.LogLevel)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/browsergateway/ -run TestLoadServeConfig -v`
Expected: FAIL (compile error: `LoadServeConfigFromEnv` / `ServeConfig` undefined).

- [ ] **Step 3: Write minimal implementation**

`internal/browsergateway/config.go`:
```go
package browsergateway

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// ServeConfig is browser-gateway's runtime configuration, sourced from BRG_* env.
type ServeConfig struct {
	ListenAddr           string
	CodexAppGatewayWSURL string // base URL of codex-app-gateway, e.g. ws://codex-app-gateway:8086
	AllowedOrigins       []string
	LogLevel             slog.Level
}

// LoadServeConfigFromEnv reads BRG_* env vars. BRG_CODEX_APP_GATEWAY_WS_URL is required.
func LoadServeConfigFromEnv() (ServeConfig, error) {
	cfg := ServeConfig{
		ListenAddr:           envOr("BRG_LISTEN_ADDR", ":8088"),
		CodexAppGatewayWSURL: os.Getenv("BRG_CODEX_APP_GATEWAY_WS_URL"),
		LogLevel:             slog.LevelInfo,
	}
	if v := os.Getenv("BRG_ALLOWED_ORIGINS"); v != "" {
		for _, o := range strings.Split(v, ",") {
			if o = strings.TrimSpace(o); o != "" {
				cfg.AllowedOrigins = append(cfg.AllowedOrigins, o)
			}
		}
	}
	switch strings.ToLower(os.Getenv("BRG_LOG_LEVEL")) {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	}
	if cfg.CodexAppGatewayWSURL == "" {
		return cfg, fmt.Errorf("BRG_CODEX_APP_GATEWAY_WS_URL is required")
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/browsergateway/ -run TestLoadServeConfig -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/browsergateway/config.go internal/browsergateway/config_test.go
git add internal/browsergateway/config.go internal/browsergateway/config_test.go
git commit -m "feat(browser-gateway): BRG_* config loader"
```

---

### Task 3: codex frame fixtures (P0)

**Files:**
- Create: `internal/browsergateway/mapper/testdata/agent_message.json`
- Create: `internal/browsergateway/mapper/testdata/turn_completed.json`
- Create: `internal/browsergateway/mapper/testdata/reasoning.json`
- Create: `internal/browsergateway/mapper/testdata/PROBE.md`

**Interfaces:**
- Produces: canonical codex notification-frame fixtures consumed by Task 5's mapper tests. Each file is the JSON of one frame's `{ "method": ..., "params": ... }`.

These shapes are transcribed verbatim from the codex v2 frames exercised in `internal/codexappgateway/broker/*_test.go` (see Global Constraints). `PROBE.md` documents how to validate them against a live codex tag; the values below are correct for the shapes CXG already round-trips, so P1 can proceed without live access.

- [ ] **Step 1: Create the agent-message frame fixture**

`internal/browsergateway/mapper/testdata/agent_message.json`:
```json
{"method":"item/completed","params":{"item":{"type":"agentMessage","id":"msg-1","text":"Hello from codex"},"threadId":"thr-1","turnId":"trn-1"}}
```

- [ ] **Step 2: Create the turn-completed frame fixture**

`internal/browsergateway/mapper/testdata/turn_completed.json`:
```json
{"method":"turn/completed","params":{"threadId":"thr-1","turn":{"id":"trn-1","status":"completed","items":[{"type":"agentMessage","id":"msg-1","text":"Hello from codex"}],"itemsView":"full","error":null,"startedAt":1,"completedAt":2,"durationMs":1000}}}
```

- [ ] **Step 3: Create the reasoning frame fixture**

`internal/browsergateway/mapper/testdata/reasoning.json`:
```json
{"method":"item/completed","params":{"item":{"type":"reasoning","id":"rsn-1","text":"thinking about it"},"threadId":"thr-1","turnId":"trn-1"}}
```

- [ ] **Step 4: Document the live-probe procedure**

`internal/browsergateway/mapper/testdata/PROBE.md`:
```markdown
# codex frame fixtures — provenance & validation

These fixtures are transcribed from the codex v2 frames exercised in
`internal/codexappgateway/broker/*_test.go`. They are correct for the shapes
codex-app-gateway already round-trips against codex 0.137.0.

## Validate / refresh against a live codex tag

1. Get a workspace codex token (console: `POST /api/codex/tokens`).
2. Connect a ws client to `<codex-app-gateway>/codex-app/ws` with
   `Authorization: Bearer <token>`, do the initialize/initialized handshake,
   send `thread/start`, then `turn/start` with
   `{"threadId":"<id>","input":[{"type":"text","text":"say hi then run ls"}]}`.
3. Log every server->client frame. Confirm the `item/completed` /
   `turn/completed` shapes match these files; if codex changed field names,
   update the fixtures AND the struct json tags in `mapper/mapper.go`.

## Not yet pinned (deferred to P1.5 / P2)
- `item/agentMessage/delta` — incremental text deltas (finer-grained streaming
  than per-`item/completed`). Record its exact params to add
  `TEXT_MESSAGE_CONTENT` streaming.
- `commandExecution` / `fileChange` item schemas — needed for P2 tool events.
```

- [ ] **Step 5: Commit**

```bash
git add internal/browsergateway/mapper/testdata/
git commit -m "test(browser-gateway): codex frame fixtures (P0)"
```

---

### Task 4: codex ws client (`codexclient`)

**Files:**
- Create: `internal/browsergateway/codexclient/protocol.go`
- Create: `internal/browsergateway/codexclient/client.go`
- Test: `internal/browsergateway/codexclient/client_test.go`

**Interfaces:**
- Consumes: `nhooyr.io/websocket`.
- Produces:
  - `type Frame struct { Method string; Params json.RawMessage }`
  - `func Dial(ctx context.Context, wsURL, bearer string) (*Client, error)`
  - `func (c *Client) StartThread(ctx context.Context) (string, error)`
  - `func (c *Client) ResumeThread(ctx context.Context, threadID string) error`
  - `func (c *Client) StartTurn(ctx context.Context, threadID, userText string) (string, error)`
  - `func (c *Client) Frames() <-chan Frame`
  - `func (c *Client) Interrupt(ctx context.Context, threadID, turnID string)`
  - `func (c *Client) Close() error`

- [ ] **Step 1: Write the protocol structs**

`internal/browsergateway/codexclient/protocol.go`:
```go
package codexclient

import "encoding/json"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Frame is a server->client notification (no id), forwarded to consumers.
type Frame struct {
	Method string
	Params json.RawMessage
}

type threadStartResult struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

type turnStartResult struct {
	Turn struct {
		ID string `json:"id"`
	} `json:"turn"`
}

type turnInputItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type turnStartParams struct {
	ThreadID string          `json:"threadId"`
	Input    []turnInputItem `json:"input"`
}
```

- [ ] **Step 2: Write the failing client test**

`internal/browsergateway/codexclient/client_test.go`:
```go
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
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/browsergateway/codexclient/ -run TestClient -v`
Expected: FAIL (compile error: `Dial`/`Client` undefined).

- [ ] **Step 4: Write the client implementation**

`internal/browsergateway/codexclient/client.go`:
```go
package codexclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"nhooyr.io/websocket"
)

// Client is one ws connection to codex-app-gateway's /codex-app/ws, speaking
// the codex v2 JSON-RPC protocol. Server->client notifications are surfaced on
// Frames(); rpc responses are routed to the matching caller.
type Client struct {
	ws      *websocket.Conn
	writeMu sync.Mutex
	nextID  atomic.Int64

	mu          sync.Mutex
	pendingResp map[int64]chan rpcResponse

	frames    chan Frame
	closeOnce sync.Once
	closed    chan struct{}
}

// Dial connects to wsURL with an optional Bearer token and completes the codex
// initialize/initialized handshake. Caller must Close().
func Dial(ctx context.Context, wsURL, bearer string) (*Client, error) {
	hdr := http.Header{}
	if bearer != "" {
		hdr.Set("Authorization", "Bearer "+bearer)
	}
	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		CompressionMode: websocket.CompressionDisabled,
		HTTPHeader:      hdr,
	})
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", wsURL, err)
	}
	ws.SetReadLimit(64 << 20)
	c := &Client{
		ws:          ws,
		pendingResp: make(map[int64]chan rpcResponse),
		frames:      make(chan Frame, 64),
		closed:      make(chan struct{}),
	}
	id := c.nextID.Add(1)
	if err := c.writeJSON(ctx, rpcRequest{JSONRPC: "2.0", ID: &id, Method: "initialize",
		Params: json.RawMessage(`{"clientInfo":{"name":"agentserver-browser-gateway","version":"0.1.0"},"capabilities":{}}`)}); err != nil {
		ws.Close(websocket.StatusInternalError, "")
		return nil, fmt.Errorf("initialize: %w", err)
	}
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			ws.Close(websocket.StatusInternalError, "")
			return nil, fmt.Errorf("initialize read: %w", err)
		}
		var resp rpcResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			ws.Close(websocket.StatusInternalError, "")
			return nil, fmt.Errorf("initialize decode: %w", err)
		}
		if resp.ID != nil && *resp.ID == id {
			if resp.Error != nil {
				ws.Close(websocket.StatusInternalError, "")
				return nil, fmt.Errorf("initialize rpc error: %s", resp.Error.Message)
			}
			break
		}
	}
	if err := c.writeJSON(ctx, rpcRequest{JSONRPC: "2.0", Method: "initialized", Params: json.RawMessage(`{}`)}); err != nil {
		ws.Close(websocket.StatusInternalError, "")
		return nil, fmt.Errorf("initialized: %w", err)
	}
	go c.readLoop()
	return c, nil
}

func (c *Client) writeJSON(ctx context.Context, req rpcRequest) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.Write(ctx, websocket.MessageText, b)
}

func (c *Client) readLoop() {
	defer close(c.frames)
	defer c.failAllPending()
	for {
		_, data, err := c.ws.Read(context.Background())
		if err != nil {
			return
		}
		var resp rpcResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}
		if resp.Method != "" {
			// Pure notification (no id) → surface as a frame. An id-bearing
			// server request (e.g. an approval) should never reach us: CXG's
			// /codex-app/ws auto-accepts and drops those. Ignore if it does.
			if resp.ID == nil {
				select {
				case c.frames <- Frame{Method: resp.Method, Params: resp.Params}:
				case <-c.closed:
					return
				}
			}
			continue
		}
		if resp.ID != nil {
			c.mu.Lock()
			ch := c.pendingResp[*resp.ID]
			delete(c.pendingResp, *resp.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- resp
			}
		}
	}
}

func (c *Client) failAllPending() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pendingResp {
		close(ch)
		delete(c.pendingResp, id)
	}
}

func (c *Client) call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan rpcResponse, 1)
	c.mu.Lock()
	c.pendingResp[id] = ch
	c.mu.Unlock()
	if err := c.writeJSON(ctx, rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params}); err != nil {
		c.mu.Lock()
		delete(c.pendingResp, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("write %s: %w", method, err)
	}
	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("connection closed before %s response", method)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("%s rpc error %d: %s", method, resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pendingResp, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// StartThread sends thread/start (which also attaches the per-thread event
// listener) and returns the new codex thread id.
func (c *Client) StartThread(ctx context.Context) (string, error) {
	res, err := c.call(ctx, "thread/start", json.RawMessage(`{}`))
	if err != nil {
		return "", err
	}
	var r threadStartResult
	if err := json.Unmarshal(res, &r); err != nil {
		return "", fmt.Errorf("decode thread/start: %w", err)
	}
	if r.Thread.ID == "" {
		return "", errors.New("thread/start: empty thread id")
	}
	return r.Thread.ID, nil
}

// ResumeThread sends thread/resume for an existing thread id, re-attaching the
// per-thread event listener to this connection.
func (c *Client) ResumeThread(ctx context.Context, threadID string) error {
	p, _ := json.Marshal(map[string]string{"threadId": threadID})
	_, err := c.call(ctx, "thread/resume", p)
	return err
}

// StartTurn sends turn/start with userText as a single text input item and
// returns the new turn id.
func (c *Client) StartTurn(ctx context.Context, threadID, userText string) (string, error) {
	p, _ := json.Marshal(turnStartParams{ThreadID: threadID, Input: []turnInputItem{{Type: "text", Text: userText}}})
	res, err := c.call(ctx, "turn/start", p)
	if err != nil {
		return "", err
	}
	var r turnStartResult
	if err := json.Unmarshal(res, &r); err != nil {
		return "", fmt.Errorf("decode turn/start: %w", err)
	}
	if r.Turn.ID == "" {
		return "", errors.New("turn/start: empty turn id")
	}
	return r.Turn.ID, nil
}

// Interrupt best-effort cancels an in-flight turn.
func (c *Client) Interrupt(ctx context.Context, threadID, turnID string) {
	p, _ := json.Marshal(map[string]string{"threadId": threadID, "turnId": turnID})
	_, _ = c.call(ctx, "turn/interrupt", p)
}

// Frames returns the channel of server->client notifications. Closed when the
// connection ends.
func (c *Client) Frames() <-chan Frame { return c.frames }

// Close closes the ws connection.
func (c *Client) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.ws.Close(websocket.StatusNormalClosure, "")
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/browsergateway/codexclient/ -run TestClient -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/browsergateway/codexclient/
git add internal/browsergateway/codexclient/
git commit -m "feat(browser-gateway): streaming codex v2 ws client"
```

---

### Task 5: codex → AG-UI mapper (text + lifecycle)

**Files:**
- Create: `internal/browsergateway/mapper/mapper.go`
- Test: `internal/browsergateway/mapper/mapper_test.go`

**Interfaces:**
- Consumes: `codexclient.Frame`; AG-UI `events` package.
- Produces:
  - `type Result struct { Events []events.Event; Done bool; Err string }`
  - `func Map(f codexclient.Frame) Result`

- [ ] **Step 1: Write the failing test**

`internal/browsergateway/mapper/mapper_test.go`:
```go
package mapper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/agentserver/agentserver/internal/browsergateway/codexclient"
)

func loadFrame(t *testing.T, name string) codexclient.Frame {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var wire struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return codexclient.Frame{Method: wire.Method, Params: wire.Params}
}

func types(evs []events.Event) []events.EventType {
	out := make([]events.EventType, len(evs))
	for i, e := range evs {
		out[i] = e.Type()
	}
	return out
}

func TestMap_AgentMessage(t *testing.T) {
	r := Map(loadFrame(t, "agent_message.json"))
	got := types(r.Events)
	want := []events.EventType{events.EventTypeTextMessageStart, events.EventTypeTextMessageContent, events.EventTypeTextMessageEnd}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	if r.Done || r.Err != "" {
		t.Errorf("Done=%v Err=%q, want false/empty", r.Done, r.Err)
	}
}

func TestMap_TurnCompleted(t *testing.T) {
	r := Map(loadFrame(t, "turn_completed.json"))
	if !r.Done {
		t.Fatal("Done = false, want true")
	}
	if len(r.Events) != 0 {
		t.Errorf("Events = %v, want none", types(r.Events))
	}
}

func TestMap_Reasoning(t *testing.T) {
	r := Map(loadFrame(t, "reasoning.json"))
	got := types(r.Events)
	if len(got) != 3 || got[0] != events.EventTypeReasoningMessageStart {
		t.Fatalf("event types = %v, want reasoning start/content/end", got)
	}
}

func TestMap_UnknownFrameIsNoop(t *testing.T) {
	r := Map(codexclient.Frame{Method: "turn/started", Params: []byte(`{}`)})
	if len(r.Events) != 0 || r.Done || r.Err != "" {
		t.Fatalf("unknown frame produced %+v, want empty Result", r)
	}
}
```

Note: confirm the exact `EventType` constant names (e.g. `events.EventTypeTextMessageStart`, `events.EventTypeReasoningMessageStart`) exist in `pkg/core/events/events.go`; adjust if the SDK spells them differently. If a reasoning constant is absent, drop `TestMap_Reasoning` and the reasoning branch — text is the P1 must-have.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/browsergateway/mapper/ -v`
Expected: FAIL (compile error: `Map`/`Result` undefined).

- [ ] **Step 3: Write the implementation**

`internal/browsergateway/mapper/mapper.go`:
```go
// Package mapper translates codex v2 server notification frames into AG-UI
// events. It is pure (no I/O) so it is trivially unit-testable with recorded
// frames. Unknown frames are logged and skipped (prefer over-logging to
// silently dropping new data).
package mapper

import (
	"encoding/json"
	"log/slog"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/agentserver/agentserver/internal/browsergateway/codexclient"
)

// Result is the outcome of mapping one codex notification frame.
type Result struct {
	Events []events.Event // AG-UI content events to write
	Done   bool           // turn/completed observed → loop emits RUN_FINISHED
	Err    string         // non-empty → codex reported an error → loop emits RUN_ERROR
}

type itemParams struct {
	Item codexItem `json:"item"`
}

type codexItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Map translates one codex server notification into AG-UI content events.
func Map(f codexclient.Frame) Result {
	switch f.Method {
	case "item/completed":
		var p itemParams
		if err := json.Unmarshal(f.Params, &p); err != nil {
			slog.Warn("browser-gateway/mapper: bad item/completed params", "err", err)
			return Result{}
		}
		return mapItem(p.Item)
	case "turn/completed":
		return Result{Done: true}
	case "error":
		return Result{Err: string(f.Params)}
	default:
		// turn/started, item/started, thread/*, item/agentMessage/delta, ...
		// Not surfaced in P1. Deltas become TEXT_MESSAGE_CONTENT once Phase 0
		// pins their frame shape (see mapper/testdata/PROBE.md).
		return Result{}
	}
}

func mapItem(it codexItem) Result {
	switch it.Type {
	case "agentMessage":
		if it.Text == "" {
			return Result{}
		}
		return Result{Events: []events.Event{
			events.NewTextMessageStartEvent(it.ID, events.WithRole("assistant")),
			events.NewTextMessageContentEvent(it.ID, it.Text),
			events.NewTextMessageEndEvent(it.ID),
		}}
	case "reasoning":
		if it.Text == "" {
			return Result{}
		}
		return Result{Events: []events.Event{
			events.NewReasoningMessageStartEvent(it.ID, "assistant"),
			events.NewReasoningMessageContentEvent(it.ID, it.Text),
			events.NewReasoningMessageEndEvent(it.ID),
		}}
	case "userMessage":
		return Result{} // client already has the user's own message
	default:
		slog.Warn("browser-gateway/mapper: unmapped item type", "type", it.Type)
		return Result{}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/browsergateway/mapper/ -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/browsergateway/mapper/
git add internal/browsergateway/mapper/mapper.go internal/browsergateway/mapper/mapper_test.go
git commit -m "feat(browser-gateway): codex→AG-UI mapper (text + lifecycle)"
```

---

### Task 6: Run loop (`runAGUI`)

**Files:**
- Create: `internal/browsergateway/run.go`
- Test: `internal/browsergateway/run_test.go`

**Interfaces:**
- Consumes: `codexclient.Frame`, `mapper.Map`, AG-UI `events`/`types`/`sse`.
- Produces:
  - `type codexConn interface { StartThread(ctx) (string,error); ResumeThread(ctx, string) error; StartTurn(ctx, string, string) (string,error); Frames() <-chan codexclient.Frame; Interrupt(ctx, string, string); Close() error }`
  - `type dialFunc func(ctx context.Context, bearer string) (codexConn, error)`
  - `func latestUserText(in *types.RunAgentInput) string`
  - `func runAGUI(ctx context.Context, w http.ResponseWriter, sw *sse.SSEWriter, in *types.RunAgentInput, bearer string, dial dialFunc)`

- [ ] **Step 1: Write the failing test**

`internal/browsergateway/run_test.go`:
```go
package browsergateway

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"

	"github.com/agentserver/agentserver/internal/browsergateway/codexclient"
)

// fakeConn implements codexConn with a scripted frame stream.
type fakeConn struct {
	frames    chan codexclient.Frame
	startedTurn string
}

func (f *fakeConn) StartThread(context.Context) (string, error)           { return "thr-1", nil }
func (f *fakeConn) ResumeThread(context.Context, string) error            { return nil }
func (f *fakeConn) StartTurn(_ context.Context, _, text string) (string, error) {
	f.startedTurn = text
	return "trn-1", nil
}
func (f *fakeConn) Frames() <-chan codexclient.Frame                      { return f.frames }
func (f *fakeConn) Interrupt(context.Context, string, string)             {}
func (f *fakeConn) Close() error                                          { return nil }

func TestRunAGUI_TextRun(t *testing.T) {
	fc := &fakeConn{frames: make(chan codexclient.Frame, 4)}
	fc.frames <- codexclient.Frame{Method: "item/completed", Params: []byte(`{"item":{"type":"agentMessage","id":"msg-1","text":"hi there"}}`)}
	fc.frames <- codexclient.Frame{Method: "turn/completed", Params: []byte(`{"turn":{"id":"trn-1"}}`)}

	in := &types.RunAgentInput{
		RunID:    "run-1",
		Messages: []types.Message{{Role: types.RoleUser, Content: "say hi"}},
	}
	rec := httptest.NewRecorder()
	dial := func(context.Context, string) (codexConn, error) { return fc, nil }

	runAGUI(context.Background(), rec, sse.NewSSEWriter(), in, "tok", dial)

	body := rec.Body.String()
	for _, want := range []string{"RUN_STARTED", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END", "RUN_FINISHED"} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE body missing %q\n---\n%s", want, body)
		}
	}
	if fc.startedTurn != "say hi" {
		t.Errorf("turn input = %q, want %q", fc.startedTurn, "say hi")
	}
}

func TestRunAGUI_NoUserMessage(t *testing.T) {
	in := &types.RunAgentInput{RunID: "run-1"}
	rec := httptest.NewRecorder()
	dial := func(context.Context, string) (codexConn, error) { t.Fatal("dial should not be called"); return nil, nil }
	runAGUI(context.Background(), rec, sse.NewSSEWriter(), in, "tok", dial)
	if !strings.Contains(rec.Body.String(), "RUN_ERROR") {
		t.Errorf("expected RUN_ERROR, got:\n%s", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/browsergateway/ -run TestRunAGUI -v`
Expected: FAIL (compile error: `runAGUI`/`codexConn` undefined).

- [ ] **Step 3: Write the implementation**

`internal/browsergateway/run.go`:
```go
package browsergateway

import (
	"context"
	"net/http"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"

	"github.com/agentserver/agentserver/internal/browsergateway/codexclient"
	"github.com/agentserver/agentserver/internal/browsergateway/mapper"
)

// codexConn is the codex-side surface runAGUI needs; *codexclient.Client
// satisfies it. Injected so tests can supply a scripted connection.
type codexConn interface {
	StartThread(ctx context.Context) (string, error)
	ResumeThread(ctx context.Context, threadID string) error
	StartTurn(ctx context.Context, threadID, userText string) (string, error)
	Frames() <-chan codexclient.Frame
	Interrupt(ctx context.Context, threadID, turnID string)
	Close() error
}

type dialFunc func(ctx context.Context, bearer string) (codexConn, error)

// latestUserText returns the text of the last user message, or "" if none.
func latestUserText(in *types.RunAgentInput) string {
	for i := len(in.Messages) - 1; i >= 0; i-- {
		if in.Messages[i].Role == types.RoleUser {
			if t, ok := in.Messages[i].ContentString(); ok {
				return t
			}
		}
	}
	return ""
}

// runAGUI drives one AG-UI run to completion, writing SSE events to w. It never
// returns an error: all failures are surfaced as a RUN_ERROR event so the
// client's stream is always well-formed.
func runAGUI(ctx context.Context, w http.ResponseWriter, sw *sse.SSEWriter, in *types.RunAgentInput, bearer string, dial dialFunc) {
	threadID := in.ThreadID
	runID := in.RunID
	if runID == "" {
		runID = events.GenerateRunID()
	}

	emitError := func(msg string) {
		tid := threadID
		if tid == "" {
			tid = events.GenerateThreadID()
		}
		_ = sw.WriteEvent(ctx, w, events.NewRunStartedEvent(tid, runID))
		_ = sw.WriteEvent(ctx, w, events.NewRunErrorEvent(msg, events.WithRunID(runID)))
	}

	userText := latestUserText(in)
	if userText == "" {
		emitError("no user message in RunAgentInput")
		return
	}

	conn, err := dial(ctx, bearer)
	if err != nil {
		emitError("codex dial failed: " + err.Error())
		return
	}
	defer conn.Close()

	if threadID == "" {
		threadID, err = conn.StartThread(ctx)
	} else {
		err = conn.ResumeThread(ctx, threadID)
	}
	if err != nil {
		emitError("codex thread setup failed: " + err.Error())
		return
	}

	if err := sw.WriteEvent(ctx, w, events.NewRunStartedEvent(threadID, runID)); err != nil {
		return // client gone
	}

	turnID, err := conn.StartTurn(ctx, threadID, userText)
	if err != nil {
		_ = sw.WriteEvent(ctx, w, events.NewRunErrorEvent("codex turn/start failed: "+err.Error(), events.WithRunID(runID)))
		return
	}

	frames := conn.Frames()
	for {
		select {
		case <-ctx.Done():
			conn.Interrupt(context.Background(), threadID, turnID)
			return
		case f, ok := <-frames:
			if !ok {
				_ = sw.WriteEvent(ctx, w, events.NewRunErrorEvent("codex connection closed before turn/completed", events.WithRunID(runID)))
				return
			}
			res := mapper.Map(f)
			for _, ev := range res.Events {
				if err := sw.WriteEvent(ctx, w, ev); err != nil {
					conn.Interrupt(context.Background(), threadID, turnID)
					return
				}
			}
			if res.Err != "" {
				_ = sw.WriteEvent(ctx, w, events.NewRunErrorEvent("codex error: "+res.Err, events.WithRunID(runID)))
				return
			}
			if res.Done {
				_ = sw.WriteEvent(ctx, w, events.NewRunFinishedEventWithOptions(threadID, runID, events.WithSuccessOutcome()))
				return
			}
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/browsergateway/ -run TestRunAGUI -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/browsergateway/run.go internal/browsergateway/run_test.go
git add internal/browsergateway/run.go internal/browsergateway/run_test.go
git commit -m "feat(browser-gateway): AG-UI run loop"
```

---

### Task 7: HTTP server (routes, CORS, Bearer)

**Files:**
- Create: `internal/browsergateway/server.go`
- Test: `internal/browsergateway/server_test.go`

**Interfaces:**
- Consumes: `ServeConfig`, `runAGUI`, `dialFunc`, `codexclient.Dial`.
- Produces:
  - `func NewServer(cfg ServeConfig, logger *slog.Logger) *Server`
  - `func (s *Server) Handler() http.Handler`
  - `func (s *Server) Run(ctx context.Context, addr string) error`
  - unexported field `dial dialFunc` (overridable in tests).

- [ ] **Step 1: Write the failing test**

`internal/browsergateway/server_test.go`:
```go
package browsergateway

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/browsergateway/codexclient"
)

func newTestServer(t *testing.T, fc *fakeConn) *Server {
	t.Helper()
	s := NewServer(ServeConfig{CodexAppGatewayWSURL: "ws://unused", AllowedOrigins: []string{"*"}}, slog.Default())
	s.dial = func(context.Context, string) (codexConn, error) { return fc, nil }
	return s
}

func TestServer_HealthZ(t *testing.T) {
	s := newTestServer(t, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestServer_AGUI_RequiresBearer(t *testing.T) {
	s := newTestServer(t, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/agui", strings.NewReader(`{"messages":[]}`))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestServer_AGUI_StreamsRun(t *testing.T) {
	fc := &fakeConn{frames: make(chan codexclient.Frame, 4)}
	fc.frames <- codexclient.Frame{Method: "item/completed", Params: []byte(`{"item":{"type":"agentMessage","id":"m1","text":"hi"}}`)}
	fc.frames <- codexclient.Frame{Method: "turn/completed", Params: []byte(`{"turn":{"id":"t1"}}`)}
	s := newTestServer(t, fc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/agui", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer tok-1")
	s.Handler().ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if !strings.Contains(rec.Body.String(), "RUN_FINISHED") {
		t.Errorf("body missing RUN_FINISHED:\n%s", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/browsergateway/ -run TestServer -v`
Expected: FAIL (compile error: `NewServer`/`Server` undefined).

- [ ] **Step 3: Write the implementation**

`internal/browsergateway/server.go`:
```go
package browsergateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"

	"github.com/agentserver/agentserver/internal/browsergateway/codexclient"
)

// Server hosts the AG-UI endpoint. It is a ws client of codex-app-gateway.
type Server struct {
	cfg    ServeConfig
	logger *slog.Logger
	sse    *sse.SSEWriter
	dial   dialFunc
}

// NewServer builds a Server. The default dialer connects to
// cfg.CodexAppGatewayWSURL + "/codex-app/ws" forwarding the request's Bearer.
func NewServer(cfg ServeConfig, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	wsURL := strings.TrimRight(cfg.CodexAppGatewayWSURL, "/") + "/codex-app/ws"
	s := &Server{
		cfg:    cfg,
		logger: logger,
		sse:    sse.NewSSEWriter().WithLogger(logger),
	}
	s.dial = func(ctx context.Context, bearer string) (codexConn, error) {
		return codexclient.Dial(ctx, wsURL, bearer)
	}
	return s
}

// Handler returns the HTTP handler (routes + CORS).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /agui", s.handleAGUI)
	return s.withCORS(mux)
}

func (s *Server) handleAGUI(w http.ResponseWriter, r *http.Request) {
	bearer := extractBearer(r)
	if bearer == "" {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return
	}
	var in types.RunAgentInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid RunAgentInput: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	defer func() {
		if rec := recover(); rec != nil {
			s.logger.Error("browser-gateway: run panicked", "err", rec)
		}
	}()
	runAGUI(r.Context(), w, s.sse, &in, bearer, s.dial)
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	origin := "*"
	if len(s.cfg.AllowedOrigins) > 0 {
		origin = strings.Join(s.cfg.AllowedOrigins, ", ")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, Cache-Control")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func extractBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[len("Bearer "):])
	}
	return ""
}

// Run starts the HTTP server and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.Handler()}
	go func() {
		<-ctx.Done()
		sc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sc)
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/browsergateway/ -run TestServer -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/browsergateway/server.go internal/browsergateway/server_test.go
git add internal/browsergateway/server.go internal/browsergateway/server_test.go
git commit -m "feat(browser-gateway): HTTP server (routes, CORS, bearer)"
```

---

### Task 8: `cmd/browser-gateway` binary

**Files:**
- Create: `cmd/browser-gateway/main.go`
- Create: `cmd/browser-gateway/serve_args.go`
- Create: `cmd/browser-gateway/serve_args_test.go`

**Interfaces:**
- Consumes: `browsergateway.LoadServeConfigFromEnv`, `browsergateway.NewServer`, `(*Server).Run`.
- Produces: a runnable binary with a `serve` subcommand.

- [ ] **Step 1: Write the failing args test**

`cmd/browser-gateway/serve_args_test.go`:
```go
package main

import "testing"

func TestParseServeArgs_Default(t *testing.T) {
	a, err := parseServeArgs(nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if a.ListenAddr != ":8088" {
		t.Errorf("ListenAddr = %q, want :8088", a.ListenAddr)
	}
}

func TestParseServeArgs_Flag(t *testing.T) {
	a, err := parseServeArgs([]string{"--listen-addr", ":9999"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if a.ListenAddr != ":9999" {
		t.Errorf("ListenAddr = %q, want :9999", a.ListenAddr)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/browser-gateway/ -v`
Expected: FAIL (compile error: `parseServeArgs` undefined).

- [ ] **Step 3: Write serve_args.go**

`cmd/browser-gateway/serve_args.go`:
```go
package main

import (
	"flag"
	"io"
	"os"
)

type serveArgs struct {
	ListenAddr string
}

func parseServeArgs(rawArgs []string) (serveArgs, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	listen := fs.String("listen-addr", ":8088", "HTTP listen address (env BRG_LISTEN_ADDR)")
	if err := fs.Parse(rawArgs); err != nil {
		return serveArgs{}, err
	}
	if envListen := os.Getenv("BRG_LISTEN_ADDR"); envListen != "" && *listen == ":8088" {
		*listen = envListen
	}
	return serveArgs{ListenAddr: *listen}, nil
}
```

- [ ] **Step 4: Write main.go**

`cmd/browser-gateway/main.go`:
```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/agentserver/agentserver/internal/browsergateway"
)

const usage = `browser-gateway — AG-UI endpoint for the codex harness

Subcommands:
  serve   Run the AG-UI HTTP/SSE server
`

const serveHelp = `Usage: browser-gateway serve [flags]

Run the browser-gateway HTTP/SSE server: exposes POST /agui (AG-UI over SSE),
translating each run into a codex turn via codex-app-gateway /codex-app/ws.

Flags:
  --listen-addr <addr>   HTTP listen address (default :8088, env BRG_LISTEN_ADDR)

Required env:
  BRG_CODEX_APP_GATEWAY_WS_URL   base ws URL of codex-app-gateway (e.g. ws://codex-app-gateway:8086)
Optional env:
  BRG_ALLOWED_ORIGINS            CORS allowlist, comma-separated (default *)
  BRG_LOG_LEVEL                  debug|info|warn|error (default info)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		runServe(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Fprint(os.Stderr, usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

func runServe(rawArgs []string) {
	args, err := parseServeArgs(rawArgs)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(os.Stderr, serveHelp)
		os.Exit(0)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "browser-gateway serve:", err)
		os.Exit(2)
	}
	cfg, err := browsergateway.LoadServeConfigFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "browser-gateway serve: config:", err)
		os.Exit(2)
	}
	if args.ListenAddr != "" {
		cfg.ListenAddr = args.ListenAddr
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	srv := browsergateway.NewServer(cfg, logger)
	logger.Info("browser-gateway starting", "addr", cfg.ListenAddr, "cxg", cfg.CodexAppGatewayWSURL)
	if err := srv.Run(ctx, cfg.ListenAddr); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
	logger.Info("server clean exit")
}
```

- [ ] **Step 5: Run test + build**

Run: `go test ./cmd/browser-gateway/ -v && go build ./cmd/browser-gateway/`
Expected: PASS (2 tests); build exit 0.

- [ ] **Step 6: Commit**

```bash
gofmt -w cmd/browser-gateway/
git add cmd/browser-gateway/
git commit -m "feat(browser-gateway): cmd binary with serve subcommand"
```

---

### Task 9: End-to-end integration test

**Files:**
- Create: `internal/browsergateway/integration_test.go`

**Interfaces:**
- Consumes: everything above. Uses the real `codexclient.Dial` (not the injected fake) against an in-process fake codex ws server, and decodes the SSE body with the SDK.

- [ ] **Step 1: Write the integration test**

`internal/browsergateway/integration_test.go`:
```go
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
				notify("item/completed", `{"item":{"type":"agentMessage","id":"msg-1","text":"Hello!"},"threadId":"thr-1","turnId":"trn-1"}`)
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
```

- [ ] **Step 2: Run the integration test**

Run: `go test ./internal/browsergateway/ -run TestIntegration -v`
Expected: PASS. If it fails on the SSE `data:` parsing, print `resp.Body` to confirm the SDK's frame format is `data: <json>\n\n` (it is per `sse/writer.go`).

- [ ] **Step 3: Run the whole package + vet**

Run: `go test ./internal/browsergateway/... ./cmd/browser-gateway/... && go vet ./internal/browsergateway/... ./cmd/browser-gateway/...`
Expected: PASS, no vet warnings.

- [ ] **Step 4: Commit**

```bash
gofmt -w internal/browsergateway/integration_test.go
git add internal/browsergateway/integration_test.go
git commit -m "test(browser-gateway): end-to-end text-run integration"
```

---

### Task 10: Dockerfile + Makefile target

**Files:**
- Create: `Dockerfile.browser-gateway`
- Modify: `Makefile` (add a `browser-gateway` build target + wire into `.PHONY`)

**Interfaces:**
- Produces: a container image running `browser-gateway serve`; a `make browser-gateway` local build.

- [ ] **Step 1: Write the Dockerfile**

`Dockerfile.browser-gateway`:
```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.26-trixie AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' \
    -o /out/browser-gateway ./cmd/browser-gateway

FROM debian:trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/browser-gateway /usr/local/bin/browser-gateway
ENTRYPOINT ["/usr/local/bin/browser-gateway"]
CMD ["serve"]
EXPOSE 8088
```

- [ ] **Step 2: Add the Makefile target**

In `Makefile`, add `browser-gateway` to the `.PHONY` line and add this target near the other gateway build targets (after `astool`):
```makefile
browser-gateway:
	CGO_ENABLED=0 go build -o bin/browser-gateway ./cmd/browser-gateway
```

- [ ] **Step 3: Verify the local build**

Run: `make browser-gateway && ./bin/browser-gateway --help`
Expected: builds; prints the usage banner.

- [ ] **Step 4: Verify the image builds**

Run: `docker build -f Dockerfile.browser-gateway -t browser-gateway:dev .`
Expected: image builds successfully.
(If Docker is unavailable in the environment, skip the build and note it; the Go build in Step 3 is the load-bearing check.)

- [ ] **Step 5: Smoke-run the binary against config validation**

Run: `BRG_CODEX_APP_GATEWAY_WS_URL= ./bin/browser-gateway serve`
Expected: exits non-zero printing `BRG_CODEX_APP_GATEWAY_WS_URL is required`.

- [ ] **Step 6: Commit**

```bash
git add Dockerfile.browser-gateway Makefile
git commit -m "feat(browser-gateway): Dockerfile + make target"
```

---

## Self-Review

**1. Spec coverage (P0+P1 scope):**
- AG-UI endpoint `POST /agui` + SSE → Tasks 7, 9. ✓
- codex streaming via `/codex-app/ws` ws client → Task 4. ✓
- Bearer pass-through auth → Tasks 4 (dial header), 6/7 (extract + forward). ✓
- `threadId == codex threadId`, start/resume, latest-user-message input → Task 6. ✓
- codex→AG-UI mapping (text/lifecycle) → Task 5. ✓
- Error handling (dial/thread/turn failures, closed conn, no-user-message, panic recover) → Tasks 6, 7. ✓
- Config `BRG_*` → Task 2. ✓
- Standalone binary + Dockerfile → Tasks 8, 10. ✓
- Phase 0 fixtures + probe doc → Task 3. ✓
- Deferred (documented, not in this plan): tool events + A2UI (P2), CopilotKit frontend + CI/Helm (P3), `item/agentMessage/delta` streaming. ✓ (called out in Task 3 PROBE.md and mapper default branch)

**2. Placeholder scan:** No TBD/TODO. Every code step shows complete code; every run step shows the command + expected result. The only "confirm against the SDK" note is the `EventType` constant-name check in Task 5 Step 1, which includes the concrete fallback. ✓

**3. Type consistency:**
- `codexclient.Frame{Method, Params}` — defined Task 4, consumed identically in Tasks 5, 6, 7, 9. ✓
- `mapper.Result{Events, Done, Err}` + `mapper.Map(codexclient.Frame) Result` — defined Task 5, consumed in Task 6. ✓
- `codexConn` interface methods (`StartThread/ResumeThread/StartTurn/Frames/Interrupt/Close`) — defined Task 6, satisfied by `*codexclient.Client` (Task 4 method set matches exactly) and by `fakeConn` (Tasks 6, 7). ✓
- `dialFunc func(ctx, bearer) (codexConn, error)` — defined Task 6, set in Task 7 `NewServer` and overridden in tests. ✓
- AG-UI SDK calls match the extracted signatures: `NewRunStartedEvent(threadID, runID)`, `NewRunFinishedEventWithOptions(threadID, runID, WithSuccessOutcome())`, `NewRunErrorEvent(msg, WithRunID(runID))`, `NewTextMessageStartEvent(id, WithRole("assistant"))`, `NewTextMessageContentEvent(id, delta)`, `NewTextMessageEndEvent(id)`, `sw.WriteEvent(ctx, w, ev)`, `msg.ContentString()`, `GenerateRunID/ThreadID`. ✓

No issues found.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-20-browser-gateway-backend-p1.md`. Two execution options:

**1. Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — execute tasks in this session using executing-plans, batch execution with checkpoints.

Follow-up plan (P2 + P3) to be written next: tool events + gateway-side A2UI card synthesis, then the CopilotKit reference frontend and CI/Helm deployment wiring.
