package audit

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type RPCParserConfig struct {
	PairTimeout time.Duration
}

type pendingCall struct {
	callID    string
	startedAt time.Time
}

// RPCParser parses RelayData payload as JSON-RPC, pairs requests with
// responses by id within a session, and emits CallStart/CallEnd via the
// Recorder. Notifications produce CallStart only. Unpaired requests
// older than PairTimeout are swept by SweepTimeouts (caller invokes
// periodically). Session-closed flushes remaining pending calls.
type RPCParser struct {
	rec Recorder
	cfg RPCParserConfig

	mu sync.Mutex
	// pending: session_id → rpc_id → call info
	pending map[string]map[string]pendingCall
}

func NewRPCParser(rec Recorder, cfg RPCParserConfig) *RPCParser {
	if cfg.PairTimeout <= 0 {
		cfg.PairTimeout = 30 * time.Second
	}
	return &RPCParser{
		rec:     rec,
		cfg:     cfg,
		pending: map[string]map[string]pendingCall{},
	}
}

// OnFrameToBackend processes a payload going from env-mcp to codex-exec.
// Workspace/user/exe identify the session — caller has them in context
// from the bridge handshake.
func (p *RPCParser) OnFrameToBackend(sessionID, wsID, userID, exeID string, payload []byte) {
	id, method, kind, ok := parseRPC(payload)
	if !ok {
		return
	}
	now := time.Now().UTC()
	startMeta := CallStartMeta{
		SessionID:   sessionID,
		WorkspaceID: wsID,
		UserID:      userID,
		ExeID:       exeID,
		Source:      "envmcp",
		RPCID:       id,
		RPCMethod:   method,
		RPCKind:     kind,
		Request:     payload,
		StartedAt:   now,
	}
	callID, err := p.rec.CallStart(startMeta)
	if err != nil {
		// Best-effort: a session-level frame's CallStart failing means
		// the WAL is wedged. The bridge handler doesn't have a
		// per-frame error path (audit must not block frame forwarding),
		// so just drop the pair-tracking entry — the failure was logged
		// inside realRecorder.
		return
	}
	if kind != "request" {
		return // notification: no pair expected
	}
	p.mu.Lock()
	if p.pending[sessionID] == nil {
		p.pending[sessionID] = map[string]pendingCall{}
	}
	p.pending[sessionID][id] = pendingCall{callID: callID, startedAt: now}
	p.mu.Unlock()
}

// OnFrameToClient processes a payload going from codex-exec back to
// env-mcp. If it matches a pending request (same session_id + rpc_id),
// emits the matching CallEnd.
func (p *RPCParser) OnFrameToClient(sessionID string, payload []byte) {
	id, _, kind, ok := parseRPC(payload)
	if !ok {
		return
	}
	if kind != "response" && kind != "error" {
		return // shouldn't happen for server→client, but be defensive
	}
	p.mu.Lock()
	pc, found := p.pending[sessionID][id]
	if found {
		delete(p.pending[sessionID], id)
	}
	p.mu.Unlock()
	if !found {
		return
	}

	isErr := kind == "error"
	var errSum string
	if isErr {
		errSum = extractErrorMessage(payload)
	}
	p.rec.CallEnd(pc.callID, CallEndMeta{
		CompletedAt:  time.Now().UTC(),
		IsError:      isErr,
		ErrorSummary: errSum,
		Response:     payload,
	})
}

// SweepTimeouts walks the pending table and emits timeout CallEnds for
// any request older than cfg.PairTimeout. Caller (typically the real
// Recorder's background loop) invokes periodically.
func (p *RPCParser) SweepTimeouts(now time.Time) {
	p.mu.Lock()
	type timed struct {
		pc pendingCall
	}
	out := []timed{}
	for sid, m := range p.pending {
		for id, pc := range m {
			if now.Sub(pc.startedAt) >= p.cfg.PairTimeout {
				out = append(out, timed{pc: pc})
				delete(m, id)
			}
		}
		if len(m) == 0 {
			delete(p.pending, sid)
		}
	}
	p.mu.Unlock()
	for _, t := range out {
		p.rec.CallEnd(t.pc.callID, CallEndMeta{
			CompletedAt:  now,
			IsError:      true,
			ErrorSummary: fmt.Sprintf("rpc pair timeout after %s", p.cfg.PairTimeout),
		})
	}
}

// SessionClosed drops any pending calls for sid as session-closed
// errors. Called by the real Recorder when the bridge SessionClose
// fires (any still-pending request will never get its response).
func (p *RPCParser) SessionClosed(sid string, now time.Time) {
	p.mu.Lock()
	pending := p.pending[sid]
	delete(p.pending, sid)
	p.mu.Unlock()
	for _, pc := range pending {
		p.rec.CallEnd(pc.callID, CallEndMeta{
			CompletedAt:  now,
			IsError:      true,
			ErrorSummary: "session closed before response",
		})
	}
}

// parseRPC returns (id, method, kind, ok). kind is "request" |
// "notification" | "response" | "error". ok=false for non-JSON, wrong
// jsonrpc version, or shape we don't recognize.
func parseRPC(b []byte) (string, string, string, bool) {
	var m struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return "", "", "", false
	}
	if m.JSONRPC != "2.0" {
		return "", "", "", false
	}
	idStr := ""
	if len(m.ID) > 0 && string(m.ID) != "null" {
		idStr = trimQuotes(string(m.ID))
	}
	if m.Method != "" {
		if idStr == "" {
			return "", m.Method, "notification", true
		}
		return idStr, m.Method, "request", true
	}
	if len(m.Error) > 0 {
		return idStr, "", "error", true
	}
	if len(m.Result) > 0 {
		return idStr, "", "response", true
	}
	return "", "", "", false
}

func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// extractErrorMessage pulls the JSON-RPC error.message field from a
// response payload. Falls back to a truncated raw-payload string when
// the payload doesn't match the standard shape — operators no longer
// see IsError=true with an empty ErrorSummary in the audit DB (I9).
func extractErrorMessage(payload []byte) string {
	var m struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &m); err == nil && m.Error.Message != "" {
		return m.Error.Message
	}
	// Fallback: truncate the raw payload so operators get SOMETHING.
	const maxErrSummary = 256
	if len(payload) > maxErrSummary {
		return string(payload[:maxErrSummary]) + "...(truncated)"
	}
	return string(payload)
}
