package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"

	"github.com/agentserver/agentserver/internal/codexappgateway/approvalfilter"
)

// Conn is one loopback ws to a codex app-server subprocess. Safe for
// concurrent Turn() / StartThread() calls — internally serializes
// writes and demuxes responses + turn/completed notifications.
type Conn struct {
	ws      *websocket.Conn
	writeMu sync.Mutex
	nextID  atomic.Int64

	// lastActiveAt is the unix-nano timestamp of the most recent successful
	// ws read or write. Stored as a pointer so the supervisor's per-entry
	// clock can be the same memory: every frame here also feeds the
	// IdleReaper that supervises the subprocess. Pool callers fetch the
	// pointer via supervisor.ChildHandle.LastActiveAt() and pass it to
	// DialWithClock; tests that don't care use Dial() which allocates a
	// fresh detached atomic.
	//
	// Previously this was a per-conn atomic; the pool's own reaper used it
	// correctly, but the supervisor had its own clock that didn't see ws
	// frames — so a long broker.Turn was reaped at the IdleShutdown mark
	// even while items streamed. One atomic, two readers fixes that.
	lastActiveAt *atomic.Int64

	mu              sync.Mutex
	pendingResp     map[int64]chan rpcResponse   // request id → 1-buffered chan
	pendingTurns    map[string]chan turnPayload  // turn id → 1-buffered chan
	itemsByTurn     map[string][]json.RawMessage // turn id → accumulated item/completed payloads
	completedTurns  map[string]turnPayload       // turn/completed arrived before Turn() finished registering — see deliverTurn for the race
	attachedThreads map[string]struct{}          // thread ids whose listener is wired to THIS connection — see Turn / StartThread for the lifecycle

	closeOnce sync.Once
	closed    chan struct{}
	closeErr  atomic.Value // stores *errHolder, set when reader exits
}

// errHolder wraps an error so atomic.Value always sees the same concrete type.
type errHolder struct{ err error }

// Dial opens a fresh ws with a private activity clock; equivalent to
// DialWithClock(ctx, wsURL, nil). Production code should call
// DialWithClock with the supervisor entry's clock so the IdleReaper
// observes broker frames.
func Dial(ctx context.Context, wsURL string) (*Conn, error) {
	return DialWithClock(ctx, wsURL, nil)
}

// DialWithClock opens a fresh ws, performs the codex initialize /
// initialized handshake, and starts the reader goroutine. The clock,
// if non-nil, is bumped on every successful ws read or write —
// production wiring passes supervisor.ChildHandle.LastActiveAt() so
// frame flow keeps the subprocess alive. Caller must Close().
func DialWithClock(ctx context.Context, wsURL string, clock *atomic.Int64) (*Conn, error) {
	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", wsURL, err)
	}
	ws.SetReadLimit(64 << 20)

	if clock == nil {
		clock = &atomic.Int64{}
	}
	c := &Conn{
		ws:              ws,
		lastActiveAt:    clock,
		pendingResp:     make(map[int64]chan rpcResponse),
		pendingTurns:    make(map[string]chan turnPayload),
		itemsByTurn:     make(map[string][]json.RawMessage),
		completedTurns:  make(map[string]turnPayload),
		attachedThreads: make(map[string]struct{}),
		closed:          make(chan struct{}),
	}

	// Send initialize synchronously (no reader yet, so we read inline).
	id := c.nextID.Add(1)
	if err := c.writeJSON(ctx, rpcRequest{JSONRPC: "2.0", ID: &id, Method: "initialize", Params: json.RawMessage(`{"clientInfo":{"name":"agentserver-codex-broker","version":"0.1.0"},"capabilities":{}}`)}); err != nil {
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
	// initialized (notification).
	if err := c.writeJSON(ctx, rpcRequest{JSONRPC: "2.0", Method: "initialized", Params: json.RawMessage(`{}`)}); err != nil {
		ws.Close(websocket.StatusInternalError, "")
		return nil, fmt.Errorf("initialized: %w", err)
	}

	go c.readLoop()
	return c, nil
}

// readLoop consumes every inbound frame and routes it: rpc responses
// to pendingResp[id]; turn/completed notifications to pendingTurns;
// approval requests get auto-replied; everything else is dropped.
func (c *Conn) readLoop() {
	defer c.failAllPending(errors.New("connection closed"))

	// One context + one watcher goroutine for the connection lifetime.
	// Previously a new watcher was spawned per frame; cancel() does not
	// unblock <-c.closed, so goroutines accumulated one-per-frame.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-c.closed
		cancel()
	}()

	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			c.closeErr.CompareAndSwap(nil, &errHolder{err})
			return
		}
		c.lastActiveAt.Store(time.Now().UnixNano())
		c.dispatchFrame(data)
	}
}

func (c *Conn) dispatchFrame(data []byte) {
	var f rpcResponse // shape covers both response and notification
	if err := json.Unmarshal(data, &f); err != nil {
		return
	}
	if f.ID != nil && f.Method == "" {
		c.deliverResponse(*f.ID, f)
		return
	}
	// Notification or server request.
	if f.ID != nil && approvalfilter.IsApproval(f.Method) {
		_ = c.writeJSON(context.Background(), rpcResponse{
			JSONRPC: "2.0", ID: f.ID, Result: approvalfilter.Reply(f.Method),
		})
		return
	}
	if f.Method == "item/completed" {
		// Codex emits items incrementally via item/completed; turn/completed's
		// Turn.items is empty (items_view: NotLoaded). Accumulate so we can
		// inject the items into the final Turn payload at delivery time.
		var p itemCompletedParams
		if err := json.Unmarshal(f.Params, &p); err != nil {
			return
		}
		if p.TurnID != "" && len(p.Item) > 0 {
			c.mu.Lock()
			c.itemsByTurn[p.TurnID] = append(c.itemsByTurn[p.TurnID], p.Item)
			c.mu.Unlock()
		}
		return
	}
	if f.Method == "turn/completed" {
		var p turnCompletedParams
		if err := json.Unmarshal(f.Params, &p); err != nil {
			return
		}
		c.deliverTurn(p.Turn.ID, p.Turn)
		return
	}
	// Unknown server-side request (id-bearing, method not in our
	// approval allowlist): reply with a JSON-RPC method-not-found error
	// so codex doesn't block waiting for a response it'll never get.
	// Silent drop would cause every subsequent Turn on this conn to
	// time out — the prod symptom that led to commit 322c2db.
	if f.ID != nil && f.Method != "" {
		log.Printf("broker: unhandled server request method=%q id=%d — replying method-not-found", f.Method, *f.ID)
		_ = c.writeJSON(context.Background(), rpcResponse{
			JSONRPC: "2.0", ID: f.ID,
			Error: &rpcError{Code: -32601, Message: "method not implemented by agentserver broker: " + f.Method},
		})
		return
	}
	// Drop genuine notifications (no id) silently — codex won't block on them.
}

func (c *Conn) deliverResponse(id int64, resp rpcResponse) {
	c.mu.Lock()
	ch, ok := c.pendingResp[id]
	delete(c.pendingResp, id)
	c.mu.Unlock()
	if ok {
		ch <- resp
	}
}

func (c *Conn) deliverTurn(turnID string, payload turnPayload) {
	c.mu.Lock()
	ch, ok := c.pendingTurns[turnID]
	items := c.itemsByTurn[turnID]
	delete(c.pendingTurns, turnID)
	delete(c.itemsByTurn, turnID)
	// Inject the accumulated item/completed payloads into Turn.items.
	// turn/completed's Turn.items is empty in codex's v2 protocol
	// (TurnItemsView::NotLoaded); the items arrived as separate
	// item/completed notifications. Without this merge, REST callers
	// see an empty items list and can't pull the agentMessage text.
	if len(items) > 0 {
		if merged, err := mergeItemsIntoTurnRaw(payload.Raw, items); err == nil {
			payload.Raw = merged
		}
	}
	if !ok {
		// Race: turn/completed arrived before Turn() finished registering
		// pendingTurns. Turn() registers AFTER waitResp on turn/start
		// returns, so a server that streams turn/start ack + turn/completed
		// back-to-back (and codex does this for cached/empty turns, plus
		// the in-process fake server in tests) can have its completion
		// dispatched by readLoop before Turn's goroutine wakes up to
		// register. Buffer the payload; Turn() will drain on registration.
		c.completedTurns[turnID] = payload
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	ch <- payload
}

// mergeItemsIntoTurnRaw replaces the "items" field of a codex Turn JSON
// payload with the supplied items slice and returns the re-serialized
// bytes. The original payload is parsed into an ordered map so unknown
// fields and field order are preserved across codex protocol updates.
func mergeItemsIntoTurnRaw(raw json.RawMessage, items []json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw, err
	}
	itemsRaw, err := json.Marshal(items)
	if err != nil {
		return raw, err
	}
	m["items"] = itemsRaw
	// Caller's lastAgentMessageText scans by item type, so this is
	// sufficient — itemsView still says "notLoaded" but we don't
	// promise to update it.
	return json.Marshal(m)
}

func (c *Conn) failAllPending(err error) {
	c.mu.Lock()
	for id, ch := range c.pendingResp {
		close(ch)
		delete(c.pendingResp, id)
	}
	for tid, ch := range c.pendingTurns {
		close(ch)
		delete(c.pendingTurns, tid)
	}
	for tid := range c.itemsByTurn {
		delete(c.itemsByTurn, tid)
	}
	for tid := range c.completedTurns {
		delete(c.completedTurns, tid)
	}
	for tid := range c.attachedThreads {
		delete(c.attachedThreads, tid)
	}
	c.mu.Unlock()
	c.closeErr.CompareAndSwap(nil, &errHolder{err})
}

// Turn sends turn/start and blocks until the matching turn/completed
// notification arrives or timeout elapses. Returns the raw codex Turn
// JSON for verbatim REST passthrough.
func (c *Conn) Turn(ctx context.Context, threadID string, callerParams json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	mergedParams, err := mergeTurnParams(threadID, callerParams)
	if err != nil {
		return nil, fmt.Errorf("merge params: %w", err)
	}

	// turn/start does NOT auto-attach the per-thread event listener (see
	// codex turn_processor.rs turn_start_inner — only thread/start,
	// thread/resume, thread/fork, and thread/realtime/start trigger
	// ensure_conversation_listener). After a broker reconnect (new
	// connection_id) the listener tied to the old connection_id is gone,
	// so the first turn we issue for a known thread on a fresh conn must
	// thread/resume to (re-)attach. Codex's TUI follows the same pattern
	// (see codex tui app_server_session.rs:resume_thread — explicit
	// thread/resume on bootstrap, never on subsequent turns).
	//
	// Skip when codex's thread_created broadcast already attached the
	// listener for us: that fires on thread/start AND thread/resume (see
	// codex core/agent/control.rs notify_thread_created at :311 and :609,
	// dispatched by lib.rs:1022 main loop to every initialized connection).
	// Tracked via attachedThreads, populated by StartThread and by every
	// successful ensureListener below. Resets implicitly when the Conn is
	// discarded — Pool will dial fresh on the next Get.
	c.mu.Lock()
	_, alreadyAttached := c.attachedThreads[threadID]
	c.mu.Unlock()
	if !alreadyAttached {
		if err := c.ensureListener(ctx, threadID); err != nil {
			return nil, fmt.Errorf("ensure listener: %w", err)
		}
		c.mu.Lock()
		c.attachedThreads[threadID] = struct{}{}
		c.mu.Unlock()
	}

	id := c.nextID.Add(1)
	respCh := make(chan rpcResponse, 1)
	c.mu.Lock()
	c.pendingResp[id] = respCh
	c.mu.Unlock()

	startedAt := time.Now()
	log.Printf("broker.Turn: turn/start sent thread=%s rpcID=%d paramsBytes=%d", threadID, id, len(mergedParams))
	if err := c.writeJSON(ctx, rpcRequest{JSONRPC: "2.0", ID: &id, Method: "turn/start", Params: mergedParams}); err != nil {
		c.mu.Lock()
		delete(c.pendingResp, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("write turn/start: %w", err)
	}

	resp, ok := waitResp(ctx, respCh)
	if !ok {
		// Either readLoop died (channel closed) or caller's ctx cancelled.
		// Remove our registration so the reader doesn't deliver into an
		// abandoned channel and the map entry doesn't persist until Close().
		// If deliverResponse already deleted it, this delete is a no-op.
		c.mu.Lock()
		delete(c.pendingResp, id)
		c.mu.Unlock()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, c.closeErrOr(errors.New("connection closed before turn/start response"))
	}
	if resp.Error != nil {
		return nil, &TurnRPCError{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}
	}
	var startResp turnStartResponse
	if err := json.Unmarshal(resp.Result, &startResp); err != nil {
		return nil, fmt.Errorf("decode turn/start result: %w", err)
	}
	if startResp.Turn.ID == "" {
		return nil, fmt.Errorf("turn/start result missing turn.id")
	}
	log.Printf("broker.Turn: turn/start ack thread=%s turn=%s ackMs=%d", threadID, startResp.Turn.ID, time.Since(startedAt).Milliseconds())

	turnCh := make(chan turnPayload, 1)
	c.mu.Lock()
	// Race-fix companion to deliverTurn: if turn/completed already arrived
	// (and was buffered there because no pending receiver existed yet),
	// consume it now instead of registering and blocking forever.
	if buffered, ok := c.completedTurns[startResp.Turn.ID]; ok {
		delete(c.completedTurns, startResp.Turn.ID)
		c.mu.Unlock()
		log.Printf("broker.Turn: turn/completed (preregistered) thread=%s turn=%s totalMs=%d items=0", threadID, startResp.Turn.ID, time.Since(startedAt).Milliseconds())
		return buffered.Raw, nil
	}
	c.pendingTurns[startResp.Turn.ID] = turnCh
	c.mu.Unlock()

	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Heartbeat so we can see whether codex is still producing items during
	// a long-running turn. items=0 across multiple heartbeats means codex
	// is fully silent (likely LLM stream hang or internal deadlock); items
	// climbing means it's just a long turn.
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go func() {
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-tick.C:
				c.mu.Lock()
				items := len(c.itemsByTurn[startResp.Turn.ID])
				c.mu.Unlock()
				log.Printf("broker.Turn: still waiting thread=%s turn=%s elapsedMs=%d items=%d", threadID, startResp.Turn.ID, time.Since(startedAt).Milliseconds(), items)
			}
		}
	}()

	select {
	case payload, open := <-turnCh:
		if !open {
			return nil, c.closeErrOr(errors.New("connection closed before turn/completed"))
		}
		c.mu.Lock()
		items := len(c.itemsByTurn[startResp.Turn.ID])
		c.mu.Unlock()
		log.Printf("broker.Turn: turn/completed thread=%s turn=%s totalMs=%d items=%d", threadID, startResp.Turn.ID, time.Since(startedAt).Milliseconds(), items)
		return payload.Raw, nil
	case <-tctx.Done():
		c.mu.Lock()
		items := len(c.itemsByTurn[startResp.Turn.ID])
		delete(c.pendingTurns, startResp.Turn.ID)
		delete(c.itemsByTurn, startResp.Turn.ID)
		c.mu.Unlock()
		log.Printf("broker.Turn: TIMEOUT thread=%s turn=%s elapsedMs=%d items=%d — codex never sent turn/completed", threadID, startResp.Turn.ID, time.Since(startedAt).Milliseconds(), items)
		// Best-effort interrupt so codex doesn't keep working.
		bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		ipB, _ := json.Marshal(turnInterruptParams{ThreadID: threadID, TurnID: startResp.Turn.ID})
		interruptID := c.nextID.Add(1)
		_ = c.writeJSON(bgCtx, rpcRequest{
			JSONRPC: "2.0", ID: &interruptID, Method: "turn/interrupt", Params: ipB,
		})
		cancel()
		// Treat the conn as poisoned: a timeout means either codex is
		// hung or our readLoop missed the response, and either way
		// subsequent Turns on this conn would inherit the bad state.
		// Close it so the Pool dials a fresh one on the next Get(). This
		// self-heals the "broker gets stuck for the whole workspace
		// after one timeout" failure mode observed in prod, where each
		// new Turn would brokerTimeout forever until CXG was kubectl-
		// restarted by hand.
		c.Close()
		return nil, &TimeoutError{ThreadID: threadID, TurnID: startResp.Turn.ID}
	}
}

// ensureListener sends thread/resume so codex (re-)attaches the
// per-thread event listener to this ws connection. Idempotent on the
// codex side — listener_matches short-circuits when the listener is
// already wired to the same conversation. Result body is discarded.
func (c *Conn) ensureListener(ctx context.Context, threadID string) error {
	id := c.nextID.Add(1)
	respCh := make(chan rpcResponse, 1)
	c.mu.Lock()
	c.pendingResp[id] = respCh
	c.mu.Unlock()

	// ThreadResumeParams has #[serde(rename_all = "camelCase")] in
	// codex-rs/app-server-protocol/src/protocol/v2/thread.rs — wire
	// field is threadId, NOT thread_id. Wrong casing would surface as
	// "missing field threadId" on every turn (verified by reading the
	// struct serde attrs, not by trust).
	params, _ := json.Marshal(map[string]string{"threadId": threadID})
	if err := c.writeJSON(ctx, rpcRequest{JSONRPC: "2.0", ID: &id, Method: "thread/resume", Params: params}); err != nil {
		c.mu.Lock()
		delete(c.pendingResp, id)
		c.mu.Unlock()
		return fmt.Errorf("write thread/resume: %w", err)
	}
	resp, ok := waitResp(ctx, respCh)
	if !ok {
		c.mu.Lock()
		delete(c.pendingResp, id)
		c.mu.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return c.closeErrOr(errors.New("connection closed before thread/resume response"))
	}
	if resp.Error != nil {
		return &TurnRPCError{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}
	}
	return nil
}

// StartThread issues thread/start with empty params and returns the new
// thread id. Other ThreadStartResponse fields are discarded — CXG only
// owns the loopback, agentserver tracks per-conversation state.
func (c *Conn) StartThread(ctx context.Context) (string, error) {
	id := c.nextID.Add(1)
	respCh := make(chan rpcResponse, 1)
	c.mu.Lock()
	c.pendingResp[id] = respCh
	c.mu.Unlock()

	if err := c.writeJSON(ctx, rpcRequest{JSONRPC: "2.0", ID: &id, Method: "thread/start", Params: json.RawMessage(`{}`)}); err != nil {
		c.mu.Lock()
		delete(c.pendingResp, id)
		c.mu.Unlock()
		return "", fmt.Errorf("write thread/start: %w", err)
	}
	resp, ok := waitResp(ctx, respCh)
	if !ok {
		// Same cleanup as Turn() — see fix in commit fc24e81.
		c.mu.Lock()
		delete(c.pendingResp, id)
		c.mu.Unlock()
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", c.closeErrOr(errors.New("connection closed before thread/start response"))
	}
	if resp.Error != nil {
		return "", &TurnRPCError{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}
	}
	var tsResp threadStartResponse
	if err := json.Unmarshal(resp.Result, &tsResp); err != nil {
		return "", fmt.Errorf("decode thread/start: %w", err)
	}
	if tsResp.Thread.ID == "" {
		return "", errors.New("thread/start result missing thread.id")
	}
	// Codex's thread_created broadcast (control.rs:311 → lib.rs:1022 main
	// loop) attached the per-thread listener for THIS connection
	// already, so the immediate next Turn() on this thread does not need
	// thread/resume. Record so it's skipped — calling thread/resume
	// before the rollout is flushed to disk would 500 with "no rollout
	// found for thread id ..." (thread_processor.rs:3589), wedging
	// every first-message-per-thread.
	c.mu.Lock()
	c.attachedThreads[tsResp.Thread.ID] = struct{}{}
	c.mu.Unlock()
	return tsResp.Thread.ID, nil
}

func waitResp(ctx context.Context, ch chan rpcResponse) (rpcResponse, bool) {
	select {
	case resp, open := <-ch:
		return resp, open
	case <-ctx.Done():
		return rpcResponse{}, false
	}
}

func (c *Conn) closeErrOr(fallback error) error {
	if v := c.closeErr.Load(); v != nil {
		if h, ok := v.(*errHolder); ok && h.err != nil {
			return h.err
		}
	}
	return fallback
}

// mergeTurnParams takes the caller-supplied params blob (which must be
// a JSON object) and merges {"threadId": threadID} into it without
// overwriting other caller fields. The caller MUST NOT include
// threadId — broker owns thread routing.
func mergeTurnParams(threadID string, caller json.RawMessage) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if len(caller) == 0 {
		m = map[string]json.RawMessage{}
	} else if err := json.Unmarshal(caller, &m); err != nil {
		return nil, fmt.Errorf("caller params is not a JSON object: %w", err)
	}
	if _, exists := m["threadId"]; exists {
		return nil, errors.New("caller params must not include threadId")
	}
	tid, _ := json.Marshal(threadID)
	m["threadId"] = tid
	return json.Marshal(m)
}

// TurnRPCError is returned by Turn when codex returns a JSON-RPC error
// in response to turn/start (rare; usually means malformed request).
type TurnRPCError struct {
	Code    int
	Message string
	Data    json.RawMessage
}

func (e *TurnRPCError) Error() string {
	return fmt.Sprintf("codex rpc error %d: %s", e.Code, e.Message)
}

// TimeoutError is returned when timeoutMs elapses without turn/completed.
type TimeoutError struct {
	ThreadID, TurnID string
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("turn timed out (thread=%s turn=%s)", e.ThreadID, e.TurnID)
}

// Close shuts down the ws. Safe to call multiple times.
func (c *Conn) Close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.ws.Close(websocket.StatusNormalClosure, "")
	})
}

func (c *Conn) writeJSON(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.ws.Write(ctx, websocket.MessageText, b); err != nil {
		return err
	}
	c.lastActiveAt.Store(time.Now().UnixNano())
	return nil
}
