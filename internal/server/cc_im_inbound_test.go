package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeCcSessionStore implements ccSessionStore for tests.
type fakeCcSessionStore struct {
	mu       sync.Mutex
	sessions map[string]sessionView // key: workspaceID+":"+externalID
	created  []sessionView

	// onSetClaudeSessionID is called when SetClaudeSessionID is invoked (optional).
	onSetClaudeSessionID func(sessionID, claudeSessionID string) error
}

func newFakeCcSessionStore() *fakeCcSessionStore {
	return &fakeCcSessionStore{
		sessions: make(map[string]sessionView),
	}
}

func (f *fakeCcSessionStore) GetSessionByExternalID(_ context.Context, workspaceID, externalID string) (sessionView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := workspaceID + ":" + externalID
	sess, ok := f.sessions[key]
	if !ok {
		return sessionView{}, nil
	}
	return sess, nil
}

func (f *fakeCcSessionStore) CreateSession(_ context.Context, workspaceID, externalID, title, imChannelID string) (sessionView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess := sessionView{
		ID:              "cse_created-" + externalID,
		ClaudeSessionID: "claude-created-" + externalID,
	}
	key := workspaceID + ":" + externalID
	f.sessions[key] = sess
	f.created = append(f.created, sess)
	_ = title
	_ = imChannelID
	return sess, nil
}

func (f *fakeCcSessionStore) SetClaudeSessionID(_ context.Context, sessionID, claudeSessionID string) error {
	if f.onSetClaudeSessionID != nil {
		return f.onSetClaudeSessionID(sessionID, claudeSessionID)
	}
	return nil
}

// fakeCcClient records RunTurn calls and returns canned responses.
type fakeCcClient struct {
	mu     sync.Mutex
	calls  []CcTurnRequest
	resp   *CcTurnResponse
	err    error
	turnFn func(req CcTurnRequest) (*CcTurnResponse, error)
}

func (f *fakeCcClient) RunTurn(_ context.Context, req CcTurnRequest) (*CcTurnResponse, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	fn := f.turnFn
	f.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return f.resp, f.err
}

func (f *fakeCcClient) lastCall() (CcTurnRequest, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return CcTurnRequest{}, false
	}
	return f.calls[len(f.calls)-1], true
}

func (f *fakeCcClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// newCcInboundTestRequest creates an HTTP request for the cc inbound handler.
func newCcInboundTestRequest(body map[string]any) *http.Request {
	b, _ := json.Marshal(body)
	return httptest.NewRequest("POST", "/api/internal/imbridge/cc/turn", bytes.NewReader(b))
}

// happyCcResponse is a CcTurnResponse for the happy path.
func happyCcResponse(text string) *CcTurnResponse {
	return &CcTurnResponse{AssistantText: text, IsError: false}
}

// waitForCc blocks until cond returns true or 2s elapses.
func waitForCc(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("waitForCc: condition never satisfied")
}

// --- Test 1: NewSession path ---

func TestCcInbound_NewSession(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	store := newFakeCcSessionStore()
	client := &fakeCcClient{resp: happyCcResponse("session created response")}

	h := newCcInboundHandler(client, store, sendURL, "")
	defer h.Close()

	r := newCcInboundTestRequest(map[string]any{
		"channel_id":     "ch-1",
		"workspace_id":   "ws-1",
		"wechat_user_id": "wxid_new",
		"text":           "hello",
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d want 202", w.Code)
	}

	waitForCc(t, func() bool { return len(sends.Load().([]*capturedSend)) == 1 })

	// CreateSession should have been called.
	store.mu.Lock()
	createdCount := len(store.created)
	store.mu.Unlock()
	if createdCount != 1 {
		t.Errorf("CreateSession called %d times, want 1", createdCount)
	}

	// RunTurn should have been called with the new claude_session_id.
	req, ok := client.lastCall()
	if !ok {
		t.Fatal("RunTurn not called")
	}
	if req.SessionID == "" {
		t.Error("RunTurn called with empty SessionID — want the newly minted claude_session_id")
	}
	if req.WorkspaceID != "ws-1" {
		t.Errorf("RunTurn WorkspaceID=%q want ws-1", req.WorkspaceID)
	}

	got := sends.Load().([]*capturedSend)[0]
	if got.text != "session created response" {
		t.Errorf("reply text=%q want 'session created response'", got.text)
	}
}

// --- Test 2: ExistingCcSession reused ---

func TestCcInbound_ExistingCcSessionReused(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	store := newFakeCcSessionStore()
	store.sessions["ws-1:wxid_existing"] = sessionView{
		ID:              "cse_existing",
		ClaudeSessionID: "existing-claude-id",
	}

	var setCalled int32
	store.onSetClaudeSessionID = func(_, _ string) error {
		atomic.AddInt32(&setCalled, 1)
		return nil
	}

	client := &fakeCcClient{resp: happyCcResponse("existing session reply")}
	h := newCcInboundHandler(client, store, sendURL, "")
	defer h.Close()

	r := newCcInboundTestRequest(map[string]any{
		"channel_id":     "ch-1",
		"workspace_id":   "ws-1",
		"wechat_user_id": "wxid_existing",
		"text":           "second message",
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	waitForCc(t, func() bool { return len(sends.Load().([]*capturedSend)) == 1 })

	// No CreateSession — session already existed.
	store.mu.Lock()
	createdCount := len(store.created)
	store.mu.Unlock()
	if createdCount != 0 {
		t.Errorf("CreateSession called %d times, want 0 (session already exists)", createdCount)
	}

	// SetClaudeSessionID must NOT be called — session already had a claude_session_id.
	if atomic.LoadInt32(&setCalled) != 0 {
		t.Error("SetClaudeSessionID called unexpectedly — session already had ClaudeSessionID")
	}

	// RunTurn called with the existing claude session id.
	req, ok := client.lastCall()
	if !ok {
		t.Fatal("RunTurn not called")
	}
	if req.SessionID != "existing-claude-id" {
		t.Errorf("RunTurn SessionID=%q want 'existing-claude-id'", req.SessionID)
	}
}

// --- Test 3: Migration from codex/nanoclaw (has ID but no ClaudeSessionID) ---

func TestCcInbound_MigrationFromCodex(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	store := newFakeCcSessionStore()
	codexThreadID := "thr-legacy"
	store.sessions["ws-1:wxid_legacy"] = sessionView{
		ID:            "cse_legacy",
		CodexThreadID: &codexThreadID,
		// ClaudeSessionID is empty — simulates a codex/nanoclaw session
	}

	var setCalledWith struct {
		sessionID       string
		claudeSessionID string
	}
	var setCalled int32
	store.onSetClaudeSessionID = func(sessionID, claudeSessionID string) error {
		atomic.AddInt32(&setCalled, 1)
		setCalledWith.sessionID = sessionID
		setCalledWith.claudeSessionID = claudeSessionID
		// Also update the in-memory session so subsequent gets work.
		store.mu.Lock()
		sess := store.sessions["ws-1:wxid_legacy"]
		sess.ClaudeSessionID = claudeSessionID
		store.sessions["ws-1:wxid_legacy"] = sess
		store.mu.Unlock()
		return nil
	}

	client := &fakeCcClient{resp: happyCcResponse("migrated reply")}
	h := newCcInboundHandler(client, store, sendURL, "")
	defer h.Close()

	r := newCcInboundTestRequest(map[string]any{
		"channel_id":     "ch-1",
		"workspace_id":   "ws-1",
		"wechat_user_id": "wxid_legacy",
		"text":           "first cc message",
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	waitForCc(t, func() bool { return len(sends.Load().([]*capturedSend)) == 1 })

	if atomic.LoadInt32(&setCalled) != 1 {
		t.Fatalf("SetClaudeSessionID called %d times, want 1", atomic.LoadInt32(&setCalled))
	}
	if setCalledWith.sessionID != "cse_legacy" {
		t.Errorf("SetClaudeSessionID sessionID=%q want 'cse_legacy'", setCalledWith.sessionID)
	}
	if setCalledWith.claudeSessionID == "" {
		t.Error("SetClaudeSessionID claudeSessionID is empty — should be a new UUID")
	}

	// RunTurn should use the newly minted UUID, not the old codex thread ID.
	req, ok := client.lastCall()
	if !ok {
		t.Fatal("RunTurn not called")
	}
	if req.SessionID != setCalledWith.claudeSessionID {
		t.Errorf("RunTurn SessionID=%q want %q (newly minted)", req.SessionID, setCalledWith.claudeSessionID)
	}

	got := sends.Load().([]*capturedSend)[0]
	if got.text != "migrated reply" {
		t.Errorf("reply=%q want 'migrated reply'", got.text)
	}
}

// --- Test 4: Transport error sends Chinese error message ---

func TestCcInbound_TransportError_SendsErrorMessage(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	store := newFakeCcSessionStore()
	store.sessions["ws-1:wxid_err"] = sessionView{
		ID:              "cse_err",
		ClaudeSessionID: "claude-err",
	}

	client := &fakeCcClient{
		err: &testTransportError{msg: "connection refused"},
	}
	h := newCcInboundHandler(client, store, sendURL, "")
	defer h.Close()

	r := newCcInboundTestRequest(map[string]any{
		"channel_id":     "ch-1",
		"workspace_id":   "ws-1",
		"wechat_user_id": "wxid_err",
		"text":           "test",
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	waitForCc(t, func() bool { return len(sends.Load().([]*capturedSend)) == 1 })

	got := sends.Load().([]*capturedSend)[0]
	if !strings.Contains(got.text, "cc-app-gateway") || !strings.Contains(got.text, "暂时无法访问") {
		t.Errorf("error text=%q — want message containing 'cc-app-gateway' and '暂时无法访问'", got.text)
	}
}

// testTransportError is a simple error type for testing transport failures.
type testTransportError struct{ msg string }

func (e *testTransportError) Error() string { return e.msg }

// --- Test 5: Context window error ---

func TestCcInbound_IsErrorContextWindow_SendsSpecificMessage(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	store := newFakeCcSessionStore()
	store.sessions["ws-1:wxid_ctx"] = sessionView{
		ID:              "cse_ctx",
		ClaudeSessionID: "claude-ctx",
	}

	client := &fakeCcClient{resp: &CcTurnResponse{
		IsError:      true,
		ErrorMessage: "context length exceeded — too many tokens in context window",
	}}
	h := newCcInboundHandler(client, store, sendURL, "")
	defer h.Close()

	r := newCcInboundTestRequest(map[string]any{
		"channel_id":     "ch-1",
		"workspace_id":   "ws-1",
		"wechat_user_id": "wxid_ctx",
		"text":           "test",
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	waitForCc(t, func() bool { return len(sends.Load().([]*capturedSend)) == 1 })

	got := sends.Load().([]*capturedSend)[0]
	if !strings.Contains(got.text, "上下文已满") {
		t.Errorf("error text=%q — want message containing '上下文已满'", got.text)
	}
}

// --- Test 6: Empty assistant text treated as error ---

func TestCcInbound_EmptyAssistantText_SendsErrorMessage(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	store := newFakeCcSessionStore()
	store.sessions["ws-1:wxid_empty"] = sessionView{
		ID:              "cse_empty",
		ClaudeSessionID: "claude-empty",
	}

	client := &fakeCcClient{resp: &CcTurnResponse{
		IsError:       false,
		AssistantText: "", // empty — should be treated as error
	}}
	h := newCcInboundHandler(client, store, sendURL, "")
	defer h.Close()

	r := newCcInboundTestRequest(map[string]any{
		"channel_id":     "ch-1",
		"workspace_id":   "ws-1",
		"wechat_user_id": "wxid_empty",
		"text":           "test",
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	waitForCc(t, func() bool { return len(sends.Load().([]*capturedSend)) == 1 })

	got := sends.Load().([]*capturedSend)[0]
	if !strings.Contains(got.text, "返回为空") {
		t.Errorf("error text=%q — want message containing '返回为空'", got.text)
	}
}

// --- Test 7: Media fields dropped ---

func TestCcInbound_MediaFieldsDropped(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	store := newFakeCcSessionStore()
	store.sessions["ws-1:wxid_media"] = sessionView{
		ID:              "cse_media",
		ClaudeSessionID: "claude-media",
	}

	client := &fakeCcClient{resp: happyCcResponse("text only reply")}
	h := newCcInboundHandler(client, store, sendURL, "")
	defer h.Close()

	r := newCcInboundTestRequest(map[string]any{
		"channel_id":     "ch-1",
		"workspace_id":   "ws-1",
		"wechat_user_id": "wxid_media",
		"text":           "caption only",
		"media_type":     "image",
		"media_data":     "aGVsbG8=", // base64("hello") — won't be read
		"quoted_text":    "earlier quote",
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	waitForCc(t, func() bool { return len(sends.Load().([]*capturedSend)) == 1 })

	// RunTurn must receive only the text, not media.
	req, ok := client.lastCall()
	if !ok {
		t.Fatal("RunTurn not called")
	}
	if req.UserMessage != "caption only" {
		t.Errorf("RunTurn UserMessage=%q want 'caption only'", req.UserMessage)
	}
	// Response should be the happy path text (media was silently dropped).
	got := sends.Load().([]*capturedSend)[0]
	if got.text != "text only reply" {
		t.Errorf("reply=%q want 'text only reply'", got.text)
	}
}

// --- Test 8: Dispatcher serializes same user ---

func TestCcInbound_DispatcherSerializesSameUser(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	store := newFakeCcSessionStore()
	store.sessions["ws-1:wxid_serial"] = sessionView{
		ID:              "cse_serial",
		ClaudeSessionID: "claude-serial",
	}

	// Slow client: first call blocks until signaled, second call returns immediately.
	firstStarted := make(chan struct{})
	firstUnblock := make(chan struct{})
	var callOrder []int
	var mu sync.Mutex

	client := &fakeCcClient{
		turnFn: func(req CcTurnRequest) (*CcTurnResponse, error) {
			mu.Lock()
			n := len(callOrder) + 1
			callOrder = append(callOrder, n)
			mu.Unlock()
			if n == 1 {
				close(firstStarted)
				<-firstUnblock // block until test unblocks
			}
			return happyCcResponse("reply " + req.UserMessage), nil
		},
	}

	h := newCcInboundHandler(client, store, sendURL, "")
	defer h.Close()

	// Send first request.
	r1 := newCcInboundTestRequest(map[string]any{
		"channel_id":     "ch-1",
		"workspace_id":   "ws-1",
		"wechat_user_id": "wxid_serial",
		"text":           "first",
	})
	h.ServeHTTP(httptest.NewRecorder(), r1)

	// Wait until first call has started.
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first call never started")
	}

	// Now enqueue second request — same (channel, user).
	r2 := newCcInboundTestRequest(map[string]any{
		"channel_id":     "ch-1",
		"workspace_id":   "ws-1",
		"wechat_user_id": "wxid_serial",
		"text":           "second",
	})
	h.ServeHTTP(httptest.NewRecorder(), r2)

	// Verify second hasn't started yet (first is still blocked).
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	countSoFar := len(callOrder)
	mu.Unlock()
	if countSoFar != 1 {
		t.Errorf("second call started before first finished: callOrder=%v", callOrder)
	}

	// Unblock first, both should complete.
	close(firstUnblock)

	waitForCc(t, func() bool { return len(sends.Load().([]*capturedSend)) == 2 })

	mu.Lock()
	order := make([]int, len(callOrder))
	copy(order, callOrder)
	mu.Unlock()
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Errorf("call order=%v want [1 2]", order)
	}
}

// --- Test 9: Dispatcher runs different users in parallel ---

func TestCcInbound_DispatcherParallelDifferentUsers(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	store := newFakeCcSessionStore()
	store.sessions["ws-1:wxid_para1"] = sessionView{ID: "cse_p1", ClaudeSessionID: "claude-p1"}
	store.sessions["ws-1:wxid_para2"] = sessionView{ID: "cse_p2", ClaudeSessionID: "claude-p2"}

	// Both calls block until both have started — proves parallelism.
	bothStarted := make(chan struct{})
	var startCount int32
	unblock := make(chan struct{})

	client := &fakeCcClient{
		turnFn: func(req CcTurnRequest) (*CcTurnResponse, error) {
			n := atomic.AddInt32(&startCount, 1)
			if n == 2 {
				close(bothStarted)
			}
			<-unblock
			return happyCcResponse("parallel reply"), nil
		},
	}

	h := newCcInboundHandler(client, store, sendURL, "")
	defer h.Close()

	r1 := newCcInboundTestRequest(map[string]any{
		"channel_id":     "ch-1",
		"workspace_id":   "ws-1",
		"wechat_user_id": "wxid_para1",
		"text":           "user1 msg",
	})
	r2 := newCcInboundTestRequest(map[string]any{
		"channel_id":     "ch-1",
		"workspace_id":   "ws-1",
		"wechat_user_id": "wxid_para2",
		"text":           "user2 msg",
	})

	h.ServeHTTP(httptest.NewRecorder(), r1)
	h.ServeHTTP(httptest.NewRecorder(), r2)

	// Both calls should start concurrently within 2s.
	select {
	case <-bothStarted:
		// success — both ran in parallel
	case <-time.After(2 * time.Second):
		t.Fatalf("parallel calls did not both start; startCount=%d", atomic.LoadInt32(&startCount))
	}

	close(unblock)
	waitForCc(t, func() bool { return len(sends.Load().([]*capturedSend)) == 2 })
}

// --- Test: 202 response shape ---

func TestCcInbound_Returns202WithQueuedTrue(t *testing.T) {
	sendURL, _, stop := newCapturingImbridge(t)
	defer stop()

	store := newFakeCcSessionStore()
	store.sessions["ws-1:wxid_q"] = sessionView{ID: "cse_q", ClaudeSessionID: "claude-q"}
	client := &fakeCcClient{resp: happyCcResponse("ok")}

	h := newCcInboundHandler(client, store, sendURL, "")
	defer h.Close()

	r := newCcInboundTestRequest(map[string]any{
		"channel_id":     "ch-1",
		"workspace_id":   "ws-1",
		"wechat_user_id": "wxid_q",
		"text":           "hi",
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 202 {
		t.Fatalf("status=%d want 202", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body["queued"] != true {
		t.Errorf("body=%v — want {queued:true}", body)
	}
}

// --- Test: Generic IsError ---

func TestCcInbound_GenericIsError_SendsErrorMessage(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	store := newFakeCcSessionStore()
	store.sessions["ws-1:wxid_gerr"] = sessionView{ID: "cse_gerr", ClaudeSessionID: "claude-gerr"}
	client := &fakeCcClient{resp: &CcTurnResponse{
		IsError:      true,
		ErrorMessage: "some backend failure detail",
	}}

	h := newCcInboundHandler(client, store, sendURL, "")
	defer h.Close()

	r := newCcInboundTestRequest(map[string]any{
		"channel_id":     "ch-1",
		"workspace_id":   "ws-1",
		"wechat_user_id": "wxid_gerr",
		"text":           "hi",
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	waitForCc(t, func() bool { return len(sends.Load().([]*capturedSend)) == 1 })

	got := sends.Load().([]*capturedSend)[0]
	if !strings.Contains(got.text, "Claude 返回错误") {
		t.Errorf("error text=%q — want message containing 'Claude 返回错误'", got.text)
	}
	if !strings.Contains(got.text, "some backend failure detail") {
		t.Errorf("error text=%q — want ErrorMessage included", got.text)
	}
}
