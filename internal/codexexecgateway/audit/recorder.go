package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// SessionMeta is the input to Recorder.SessionOpen — describes a new
// envmcp ws bridge session about to forward frames between env-mcp
// (or the SDK REST bridge.Pool) and codex-exec.
type SessionMeta struct {
	WorkspaceID string
	UserID      string
	ExeID       string
	TurnID      string
	StreamID    string
	ClientIP    string
	CapIAT      time.Time
	CapEXP      time.Time
	OpenedAt    time.Time
}

// Counters is the running per-direction frame/byte totals tracked by the
// bridge pumps over the life of one session, snapshotted at close time.
type Counters struct {
	FramesToBackend int
	FramesToClient  int
	BytesToBackend  int64
	BytesToClient   int64
}

// CallStartMeta is the input to Recorder.CallStart. SessionID is the
// audit session id (only set for envmcp source). For rest/relay sources
// it's empty — those calls aren't tied to a long-lived bridge session.
type CallStartMeta struct {
	SessionID   string
	WorkspaceID string
	UserID      string
	ExeID       string
	Source      string // "envmcp" | "rest" | "relay"
	RPCID       string
	RPCMethod   string
	RPCKind     string // "request" | "notification" | "frame" | "" for non-RPC
	Request     []byte // raw bytes; recorder owns truncation/hashing
	StartedAt   time.Time
}

// CallEndMeta is the input to Recorder.CallEnd, paired with the callID
// returned by the matching CallStart.
type CallEndMeta struct {
	CompletedAt  time.Time
	IsError      bool
	ErrorSummary string
	Response     []byte
}

// Recorder is the audit interface used by the gateway pumps and
// handlers. Production wires this to a real Recorder backed by WAL +
// Uploader (Plan 2b later tasks). Tests and audit-disabled deployments
// use NewNoopRecorder.
type Recorder interface {
	SessionOpen(SessionMeta) (sessionID string)
	SessionClose(sessionID, reason string, c Counters)
	OnFrameToBackend(sessionID string, frame any, rawBytes []byte)
	OnFrameToClient(sessionID string, frame any, rawBytes []byte)
	CallStart(CallStartMeta) (callID string)
	CallEnd(callID string, m CallEndMeta)
	Close(ctx context.Context) error
}

type noopRecorder struct{}

// NewNoopRecorder returns a Recorder that mints fresh UUIDs but does
// not persist anything. Use when CXG_AUDIT_ENABLED=false.
func NewNoopRecorder() Recorder { return noopRecorder{} }

func (noopRecorder) SessionOpen(SessionMeta) string        { return uuid.NewString() }
func (noopRecorder) SessionClose(string, string, Counters) {}
func (noopRecorder) OnFrameToBackend(string, any, []byte)  {}
func (noopRecorder) OnFrameToClient(string, any, []byte)   {}
func (noopRecorder) CallStart(CallStartMeta) string        { return uuid.NewString() }
func (noopRecorder) CallEnd(string, CallEndMeta)           {}
func (noopRecorder) Close(context.Context) error           { return nil }
