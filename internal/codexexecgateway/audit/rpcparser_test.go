package audit_test

import (
	"context"
	"sync"
	"testing"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
)

// capRecorder is a test Recorder that captures CallStart events for
// assertion. SessionOpen/Close/OnFrame* are no-ops — the parser
// doesn't invoke them.
type capRecorder struct {
	mu     sync.Mutex
	starts []audit.CallStartMeta
}

func newCapRecorder() *capRecorder {
	return &capRecorder{}
}

func (r *capRecorder) SessionOpen(audit.SessionMeta) (string, error) { return "", nil }
func (r *capRecorder) SessionClose(string, string, audit.Counters)   {}
func (r *capRecorder) OnFrameToBackend(string, any, []byte)          {}
func (r *capRecorder) CallStart(m audit.CallStartMeta) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts = append(r.starts, m)
	return "call-" + m.RPCID, nil
}
func (r *capRecorder) Close(context.Context) error { return nil }

func TestRPCParser_RequestProducesCallStart(t *testing.T) {
	cap := newCapRecorder()
	p := audit.NewRPCParser(cap)

	p.OnFrameToBackend("s1", "ws_x", "user_x", "exe_x", []byte(`
		{"jsonrpc":"2.0","id":42,"method":"shell","params":{"cmd":"ls"}}
	`))

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.starts) != 1 {
		t.Fatalf("expected 1 CallStart, got %d", len(cap.starts))
	}
	s := cap.starts[0]
	if s.RPCMethod != "shell" {
		t.Errorf("method: %q", s.RPCMethod)
	}
	if s.RPCKind != "request" {
		t.Errorf("kind: %q", s.RPCKind)
	}
	if s.WorkspaceID != "ws_x" || s.UserID != "user_x" || s.ExeID != "exe_x" {
		t.Errorf("metadata mismatch: %+v", s)
	}
	if s.Source != "envmcp" {
		t.Errorf("source: %q (want envmcp)", s.Source)
	}
	if len(s.Request) == 0 {
		t.Error("expected Request bytes captured")
	}
}

func TestRPCParser_NotificationProducesCallStart(t *testing.T) {
	cap := newCapRecorder()
	p := audit.NewRPCParser(cap)
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`
		{"jsonrpc":"2.0","method":"progress","params":{"n":1}}
	`))
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.starts) != 1 {
		t.Fatalf("expected 1 CallStart for notification, got %d", len(cap.starts))
	}
	if cap.starts[0].RPCKind != "notification" {
		t.Errorf("kind: %q", cap.starts[0].RPCKind)
	}
}

func TestRPCParser_MalformedPayloadIgnored(t *testing.T) {
	cap := newCapRecorder()
	p := audit.NewRPCParser(cap)
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`not json at all`))
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`{"jsonrpc":"1.0","id":1,"method":"x"}`)) // wrong version
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.starts) != 0 {
		t.Fatalf("expected no calls for malformed payloads, got %d", len(cap.starts))
	}
}

func TestRPCParser_ProtocolNoiseSkipped(t *testing.T) {
	cap := newCapRecorder()
	p := audit.NewRPCParser(cap)
	// MCP handshake — should be skipped
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`{"jsonrpc":"2.0","id":0,"method":"initialize"}`))
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`{"jsonrpc":"2.0","method":"initialized"}`))
	// shell output poll — should be skipped
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`{"jsonrpc":"2.0","id":5,"method":"process/read","params":{"process_id":"p1"}}`))
	// real tool call — should be recorded
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`{"jsonrpc":"2.0","id":6,"method":"process/start","params":{"cmd":"ls"}}`))

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.starts) != 1 {
		t.Fatalf("expected only process/start recorded, got %d starts: %+v", len(cap.starts), cap.starts)
	}
	if cap.starts[0].RPCMethod != "process/start" {
		t.Errorf("recorded wrong method: %q", cap.starts[0].RPCMethod)
	}
}

func TestRPCParser_StringIDsHandled(t *testing.T) {
	cap := newCapRecorder()
	p := audit.NewRPCParser(cap)
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`
		{"jsonrpc":"2.0","id":"abc-123","method":"shell"}
	`))
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.starts) != 1 || cap.starts[0].RPCID != "abc-123" {
		t.Fatalf("expected string id captured; got starts=%+v", cap.starts)
	}
}

