package codexclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

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

// Interrupt best-effort cancels an in-flight turn. It bounds its own context so
// that a codex that never replies cannot block the caller (and leak the
// readLoop + request goroutine) indefinitely.
func (c *Client) Interrupt(ctx context.Context, threadID, turnID string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
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
