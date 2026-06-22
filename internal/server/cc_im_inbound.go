package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/agentserver/agentserver/internal/db"
)

// ccInboundHandler routes inbound WeChat messages destined for the
// cc-app-gateway routing path. POST /api/internal/imbridge/cc/turn body is:
//
//	{
//	  "channel_id": "ch-xxx",
//	  "workspace_id": "ws-xxx",
//	  "wechat_user_id": "wxid_xxx",
//	  "text": "..."
//	}
//
// Returns 202 immediately and processes the turn in a goroutine.
// A per-(channel,user) FIFO dispatcher serializes concurrent requests
// from the same user so cc-app-gateway always sees a single in-flight
// turn per conversation. Media and quoted fields are accepted in the
// request body but dropped with a log warning (Phase 5+ adds vision).
type ccInboundHandler struct {
	cc              ccCaller
	sessions        ccSessionStore
	imbridgeSendURL string
	internalSecret  string
	dispatcher      *ccDispatcher
}

// ccCaller is the interface the cc handler needs from CcClient.
// Defined as a local interface so tests can inject fakes without
// depending on the concrete *CcClient type.
type ccCaller interface {
	RunTurn(ctx context.Context, req CcTurnRequest) (*CcTurnResponse, error)
}

// ccSessionStore is what the cc handler needs from the DB. It is a
// separate interface from codex's sessionStore so the two handlers
// remain independent per spec Open Risk #7.
type ccSessionStore interface {
	GetSessionByExternalID(ctx context.Context, workspaceID, externalID string) (sessionView, error)
	CreateSession(ctx context.Context, workspaceID, externalID, title, imChannelID string) (sessionView, error)
	// SetClaudeSessionID persists a newly-minted claude_session_id for
	// an existing session (migration from codex/nanoclaw → managed_cc).
	SetClaudeSessionID(ctx context.Context, sessionID, claudeSessionID string) error
}

// ccInboundRequest is the JSON body POSTed by imbridge to the cc turn endpoint.
// Media and quoted fields mirror codex's request shape so imbridge can use the
// same payload constructor — but for Phase 4 only the text field is forwarded
// to cc-app-gateway. The rest is dropped with a log warning.
type ccInboundRequest struct {
	ChannelID    string `json:"channel_id"`
	WorkspaceID  string `json:"workspace_id"`
	WechatUserID string `json:"wechat_user_id"`
	WechatSender string `json:"wechat_sender_name,omitempty"`
	Text         string `json:"text"`
	QuotedText   string `json:"quoted_text,omitempty"`
	QuotedSender string `json:"quoted_sender,omitempty"`
	// Media fields — accepted but dropped in Phase 4 (Phase 5+ adds vision).
	MediaType       string `json:"media_type,omitempty"`
	MediaData       string `json:"media_data,omitempty"` // base64
	QuotedMediaType string `json:"quoted_media_type,omitempty"`
	QuotedMediaData string `json:"quoted_media_data,omitempty"` // base64
}

func (h *ccInboundHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req ccInboundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.ChannelID == "" || req.WorkspaceID == "" || req.WechatUserID == "" {
		http.Error(w, "channel_id, workspace_id, wechat_user_id required", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"queued":true}`))
	h.dispatcher.Enqueue(req)
}

func (h *ccInboundHandler) processTurn(ctx context.Context, req ccInboundRequest) {
	externalID := req.WechatUserID

	// Step 1: look up existing session.
	sess, err := h.sessions.GetSessionByExternalID(ctx, req.WorkspaceID, externalID)
	if err != nil {
		log.Printf("cc_im: resolve session channel=%s user=%s: %v", req.ChannelID, externalID, err)
		h.sendError(ctx, req, "⚠️ 内部错误，请重试")
		return
	}

	// Step 2: create session on first contact.
	if sess.ID == "" {
		title := "IM: " + req.WechatSender
		if title == "IM: " {
			title = "IM: " + req.WechatUserID
		}
		sess, err = h.sessions.CreateSession(ctx, req.WorkspaceID, externalID, title, req.ChannelID)
		if err != nil {
			log.Printf("cc_im: create session channel=%s user=%s: %v", req.ChannelID, externalID, err)
			h.sendError(ctx, req, "⚠️ 内部错误，请重试")
			return
		}
	}

	// Step 3: migration — existing codex/nanoclaw session has no ClaudeSessionID.
	// Mint a fresh UUID and persist it. Codex history is NOT carried over.
	if sess.ClaudeSessionID == "" {
		newUUID := uuid.NewString()
		log.Printf("cc_im: session migrating from codex/nanoclaw to managed_cc channel=%s user=%s session=%s new_claude_session_id=%s",
			req.ChannelID, externalID, sess.ID, newUUID)
		if err := h.sessions.SetClaudeSessionID(ctx, sess.ID, newUUID); err != nil {
			log.Printf("cc_im: set claude_session_id channel=%s user=%s: %v", req.ChannelID, externalID, err)
			h.sendError(ctx, req, "⚠️ 内部错误，请重试")
			return
		}
		sess.ClaudeSessionID = newUUID
	}

	// Step 4: drop media + quoted fields with a log warning (Phase 5+ adds vision).
	if req.MediaType != "" || req.QuotedText != "" || req.MediaData != "" || req.QuotedMediaType != "" || req.QuotedMediaData != "" {
		log.Printf("cc_im: Phase 4 drops media/quoted fields channel=%s user=%s media_type=%q quoted_text_len=%d",
			req.ChannelID, externalID, req.MediaType, len(req.QuotedText))
	}

	// Step 5: call cc-app-gateway.
	resp, err := h.cc.RunTurn(ctx, CcTurnRequest{
		WorkspaceID: req.WorkspaceID,
		SessionID:   sess.ClaudeSessionID,
		UserMessage: req.Text,
	})

	// Step 6: error matrix dispatch.
	if err != nil {
		log.Printf("cc_im: RunTurn channel=%s user=%s: %v", req.ChannelID, externalID, err)
		h.sendError(ctx, req, "cc-app-gateway 暂时无法访问，请稍后再试")
		return
	}

	if resp.IsError {
		if strings.Contains(resp.ErrorMessage, "context") {
			h.sendError(ctx, req, "上下文已满，请新开对话（管理员请清理 session）")
			return
		}
		h.sendError(ctx, req, "Claude 返回错误："+resp.ErrorMessage)
		return
	}

	if resp.AssistantText == "" {
		h.sendError(ctx, req, "Claude 返回为空，请重新发送")
		return
	}

	// Step 7: happy path — send the assistant's reply.
	h.sendText(ctx, req, resp.AssistantText)
}

// sendText / sendError both POST /api/internal/imbridge/send.

func (h *ccInboundHandler) sendText(ctx context.Context, req ccInboundRequest, text string) {
	h.postSend(ctx, map[string]any{
		"channel_id": req.ChannelID,
		"to_user_id": req.WechatUserID,
		"text":       text,
	})
}

func (h *ccInboundHandler) sendError(ctx context.Context, req ccInboundRequest, text string) {
	h.postSend(ctx, map[string]any{
		"channel_id": req.ChannelID,
		"to_user_id": req.WechatUserID,
		"text":       text,
	})
}

func (h *ccInboundHandler) postSend(ctx context.Context, body map[string]any) {
	b, _ := json.Marshal(body)
	r, err := http.NewRequestWithContext(ctx, "POST", h.imbridgeSendURL+"/api/internal/imbridge/send", bytes.NewReader(b))
	if err != nil {
		log.Printf("cc_im: build send req: %v", err)
		return
	}
	r.Header.Set("Content-Type", "application/json")
	if h.internalSecret != "" {
		r.Header.Set("X-Internal-Secret", h.internalSecret)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		log.Printf("cc_im: send POST: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		log.Printf("cc_im: send status=%d body=%s", resp.StatusCode, b)
	}
}

// newCcInboundHandler wires up the handler with its dispatcher already running.
// Cap is the per-(channel,user) queue depth — past cap, drop-oldest applies.
func newCcInboundHandler(cc ccCaller, sessions ccSessionStore, imbridgeSendURL, internalSecret string) *ccInboundHandler {
	h := &ccInboundHandler{
		cc:              cc,
		sessions:        sessions,
		imbridgeSendURL: imbridgeSendURL,
		internalSecret:  internalSecret,
	}
	h.dispatcher = newCcDispatcher(func(req ccInboundRequest) {
		h.processTurn(context.Background(), req)
	}, 5)
	return h
}

// Close stops the FIFO dispatcher. Safe to call multiple times.
// In-flight worker goroutines complete their current task then exit.
func (h *ccInboundHandler) Close() {
	h.dispatcher.Stop()
}

// --- per-(channel,user) FIFO dispatcher ---
// Verbatim copy of codexDispatcher with ccInboundRequest substituted for
// codexInboundRequest. NOT extracted to a generic package per spec Open Risk #7.

type ccDispatcher struct {
	processFn func(ccInboundRequest)
	cap       int

	mu      sync.Mutex
	workers map[string]*ccDispatcherSlot
	stopped bool
}

type ccDispatcherSlot struct {
	ch    chan ccInboundRequest
	ready chan struct{}
}

func newCcDispatcher(processFn func(ccInboundRequest), cap int) *ccDispatcher {
	return &ccDispatcher{
		processFn: processFn,
		cap:       cap,
		workers:   make(map[string]*ccDispatcherSlot),
	}
}

func ccDispatcherKey(req ccInboundRequest) string {
	return req.ChannelID + ":" + req.WechatUserID
}

// Enqueue adds req to the per-key channel. If the channel is full,
// drains the oldest queued item to make room (drop-oldest policy).
// Starts a worker for this key if none is running.
func (d *ccDispatcher) Enqueue(req ccInboundRequest) {
	key := ccDispatcherKey(req)
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	slot, ok := d.workers[key]
	if !ok {
		slot = &ccDispatcherSlot{
			ch:    make(chan ccInboundRequest, d.cap),
			ready: make(chan struct{}),
		}
		d.workers[key] = slot
		slot.ch <- req // buffered, never blocks (fresh channel, cap >= 1)
		go d.runWorker(key, slot)
		d.mu.Unlock()
		<-slot.ready
		return
	}
	d.mu.Unlock()

	for {
		select {
		case slot.ch <- req:
			return
		default:
			// Full — drop oldest then retry.
			select {
			case <-slot.ch:
			default:
			}
		}
	}
}

func (d *ccDispatcher) runWorker(key string, slot *ccDispatcherSlot) {
	first, ok := <-slot.ch
	close(slot.ready) // unblock the spawning Enqueue
	if !ok {
		return
	}
	d.processFn(first)
	for req := range slot.ch {
		d.processFn(req)
	}
	_ = key
}

func (d *ccDispatcher) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	d.stopped = true
	for _, slot := range d.workers {
		close(slot.ch)
	}
	d.workers = nil
}

// --- ccDbSessionStore: production ccSessionStore ---

// ccDbSessionStore is the production ccSessionStore that reads/writes
// the real agent_sessions table. Separate from codex's dbSessionStore —
// the two handlers remain independent.
type ccDbSessionStore struct {
	db *db.DB
}

func (s *ccDbSessionStore) GetSessionByExternalID(ctx context.Context, workspaceID, externalID string) (sessionView, error) {
	sess, err := s.db.GetSessionByExternalID(ctx, workspaceID, externalID)
	if err != nil {
		return sessionView{}, err
	}
	if sess == nil {
		return sessionView{}, nil
	}
	return sessionView{
		ID:              sess.ID,
		CodexThreadID:   sess.CodexThreadID,
		ClaudeSessionID: sess.ClaudeSessionID.String,
	}, nil
}

func (s *ccDbSessionStore) CreateSession(ctx context.Context, workspaceID, externalID, title, imChannelID string) (sessionView, error) {
	sessionID := "cse_" + uuid.NewString()
	claudeSessionID := uuid.NewString() // pure UUID, no prefix — cc-app-gateway's session ID
	if err := s.db.CreateAgentSession(sessionID, nil, workspaceID, title, nil); err != nil {
		return sessionView{}, fmt.Errorf("create session: %w", err)
	}
	if err := s.db.SetSessionExternalID(ctx, sessionID, externalID); err != nil {
		return sessionView{}, fmt.Errorf("set external_id: %w", err)
	}
	if err := s.db.SetSessionClaudeSessionID(ctx, sessionID, claudeSessionID); err != nil {
		return sessionView{}, fmt.Errorf("set claude_session_id: %w", err)
	}
	if imChannelID != "" {
		if err := s.db.SetSessionIMChannel(ctx, sessionID, imChannelID); err != nil {
			log.Printf("cc_im: failed to set im_channel_id for session %s: %v", sessionID, err)
		}
	}
	return sessionView{
		ID:              sessionID,
		ClaudeSessionID: claudeSessionID,
	}, nil
}

func (s *ccDbSessionStore) SetClaudeSessionID(ctx context.Context, sessionID, claudeSessionID string) error {
	return s.db.SetSessionClaudeSessionID(ctx, sessionID, claudeSessionID)
}
