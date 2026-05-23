package audit_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
)

// capRecorder is a test Recorder that captures CallStart/CallEnd events
// for assertion. SessionOpen/Close/OnFrame* are no-ops — the parser
// doesn't invoke them.
type capRecorder struct {
	mu     sync.Mutex
	starts []audit.CallStartMeta
	ends   map[string]audit.CallEndMeta
}

func newCapRecorder() *capRecorder {
	return &capRecorder{ends: map[string]audit.CallEndMeta{}}
}

func (r *capRecorder) SessionOpen(audit.SessionMeta) string        { return "" }
func (r *capRecorder) SessionClose(string, string, audit.Counters) {}
func (r *capRecorder) OnFrameToBackend(string, any, []byte)        {}
func (r *capRecorder) OnFrameToClient(string, any, []byte)         {}
func (r *capRecorder) CallStart(m audit.CallStartMeta) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := "call-" + m.RPCID
	r.starts = append(r.starts, m)
	return id
}
func (r *capRecorder) CallEnd(id string, m audit.CallEndMeta) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ends[id] = m
}
func (r *capRecorder) Close(context.Context) error { return nil }

func TestRPCParser_RequestResponsePair(t *testing.T) {
	cap := newCapRecorder()
	p := audit.NewRPCParser(cap, audit.RPCParserConfig{PairTimeout: time.Minute})

	p.OnFrameToBackend("s1", "ws_x", "user_x", "exe_x", []byte(`
		{"jsonrpc":"2.0","id":42,"method":"shell","params":{"cmd":"ls"}}
	`))
	p.OnFrameToClient("s1", []byte(`
		{"jsonrpc":"2.0","id":42,"result":{"stdout":"foo"}}
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
	end, ok := cap.ends["call-42"]
	if !ok {
		t.Fatal("expected CallEnd for id=42")
	}
	if end.IsError {
		t.Error("expected IsError=false")
	}
	if len(end.Response) == 0 {
		t.Error("expected Response bytes captured")
	}
}

func TestRPCParser_NotificationProducesCallStartOnly(t *testing.T) {
	cap := newCapRecorder()
	p := audit.NewRPCParser(cap, audit.RPCParserConfig{PairTimeout: time.Minute})
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
	if len(cap.ends) != 0 {
		t.Fatalf("notifications shouldn't produce CallEnd, got %d", len(cap.ends))
	}
}

func TestRPCParser_ErrorResponsePairs(t *testing.T) {
	cap := newCapRecorder()
	p := audit.NewRPCParser(cap, audit.RPCParserConfig{PairTimeout: time.Minute})
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`
		{"jsonrpc":"2.0","id":7,"method":"die","params":{}}
	`))
	p.OnFrameToClient("s1", []byte(`
		{"jsonrpc":"2.0","id":7,"error":{"code":-32603,"message":"boom"}}
	`))
	cap.mu.Lock()
	defer cap.mu.Unlock()
	end, ok := cap.ends["call-7"]
	if !ok {
		t.Fatal("expected CallEnd for id=7")
	}
	if !end.IsError {
		t.Error("expected IsError=true on JSON-RPC error response")
	}
	if end.ErrorSummary != "boom" {
		t.Errorf("expected ErrorSummary='boom', got %q", end.ErrorSummary)
	}
}

func TestRPCParser_MalformedPayloadIgnored(t *testing.T) {
	cap := newCapRecorder()
	p := audit.NewRPCParser(cap, audit.RPCParserConfig{PairTimeout: time.Minute})
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`not json at all`))
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`{"jsonrpc":"1.0","id":1,"method":"x"}`)) // wrong version
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.starts) != 0 {
		t.Fatalf("expected no calls for malformed payloads, got %d", len(cap.starts))
	}
}

func TestRPCParser_TimeoutEmitsErrorCallEnd(t *testing.T) {
	cap := newCapRecorder()
	p := audit.NewRPCParser(cap, audit.RPCParserConfig{PairTimeout: 50 * time.Millisecond})
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`
		{"jsonrpc":"2.0","id":99,"method":"slow","params":{}}
	`))
	time.Sleep(200 * time.Millisecond)
	p.SweepTimeouts(time.Now())
	cap.mu.Lock()
	defer cap.mu.Unlock()
	end, ok := cap.ends["call-99"]
	if !ok {
		t.Fatal("expected timeout CallEnd for id=99")
	}
	if !end.IsError || end.ErrorSummary == "" {
		t.Fatalf("expected is_error + summary, got %+v", end)
	}
}

func TestRPCParser_SessionClosedFlushesPending(t *testing.T) {
	cap := newCapRecorder()
	p := audit.NewRPCParser(cap, audit.RPCParserConfig{PairTimeout: time.Minute})
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`
		{"jsonrpc":"2.0","id":11,"method":"x","params":{}}
	`))
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`
		{"jsonrpc":"2.0","id":12,"method":"y","params":{}}
	`))
	p.SessionClosed("s1", time.Now())
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.ends) != 2 {
		t.Fatalf("expected 2 flushed CallEnds (for ids 11 and 12), got %d", len(cap.ends))
	}
	for id, end := range cap.ends {
		if !end.IsError {
			t.Errorf("%s: expected IsError=true on session-closed flush", id)
		}
	}
}

func TestRPCParser_StringIDsHandled(t *testing.T) {
	cap := newCapRecorder()
	p := audit.NewRPCParser(cap, audit.RPCParserConfig{PairTimeout: time.Minute})
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`
		{"jsonrpc":"2.0","id":"abc-123","method":"shell"}
	`))
	p.OnFrameToClient("s1", []byte(`
		{"jsonrpc":"2.0","id":"abc-123","result":{"stdout":""}}
	`))
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if _, ok := cap.ends["call-abc-123"]; !ok {
		t.Fatalf("expected pairing with string id; got ends=%v", cap.ends)
	}
}
