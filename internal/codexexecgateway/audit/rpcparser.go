package audit

import (
	"encoding/json"
	"time"
)

// RPCParser parses RelayData payload as JSON-RPC and emits a CallStart
// per request/notification via the Recorder. Responses are not paired
// and not persisted — audit is request-only.
type RPCParser struct {
	rec Recorder
}

func NewRPCParser(rec Recorder) *RPCParser {
	return &RPCParser{rec: rec}
}

// skippedMethods are bridge frames that aren't meaningful tool calls
// and would just create noise in the audit timeline:
//   - initialize / initialized: MCP handshake at session start
//   - process/read: shell/exec_command output polling — one logical
//     shell command produces dozens of these. The matching process/start
//     IS recorded, which is the actual "user issued shell" event.
var skippedMethods = map[string]bool{
	"initialize":   true,
	"initialized":  true,
	"process/read": true,
}

// OnFrameToBackend processes a payload going from env-mcp to codex-exec.
// Workspace/user/exe identify the session — caller has them in context
// from the bridge handshake.
func (p *RPCParser) OnFrameToBackend(sessionID, wsID, userID, exeID string, payload []byte) {
	id, method, kind, ok := parseRPC(payload)
	if !ok {
		return
	}
	if skippedMethods[method] {
		return
	}
	p.rec.CallStart(CallStartMeta{
		SessionID:   sessionID,
		WorkspaceID: wsID,
		UserID:      userID,
		ExeID:       exeID,
		Source:      "envmcp",
		RPCID:       id,
		RPCMethod:   method,
		RPCKind:     kind,
		Request:     payload,
		StartedAt:   time.Now().UTC(),
	})
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
	// We intentionally don't bother distinguishing responses from errors
	// here: responses are dropped by the caller (request-only audit).
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
