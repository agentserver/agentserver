package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/agentserver/agentserver/internal/relaypb"
	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
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

// ---------- real Recorder ----------

// realRecorder backs the Recorder interface with WAL + Uploader +
// RPCParser. Lifecycle: NewRecorder constructs all subsystems and
// spawns the Uploader + RPCParser sweep goroutines; Close cancels them
// and final-flushes the WAL.
type realRecorder struct {
	cfg       Config
	wal       *WAL
	cursor    *Cursor
	uploader  *Uploader
	parser    *RPCParser
	gatewayID string

	mu              sync.Mutex
	sessions        map[string]*sessionState
	uploadCtxCancel context.CancelFunc
	sweepCtxCancel  context.CancelFunc
}

type sessionState struct {
	id          string
	workspaceID string
	userID      string
	exeID       string
	counters    Counters
}

// NewRecorder constructs the appropriate Recorder for cfg. If
// cfg.Enabled is false (or cfg.WALDir is empty) returns a noopRecorder.
// On success starts the upload and RPC-sweep goroutines and registers
// a Close-to-stop them. Caller MUST Close on shutdown to flush the WAL.
func NewRecorder(cfg Config) (Recorder, error) {
	if !cfg.Enabled {
		return NewNoopRecorder(), nil
	}
	wal, err := OpenWAL(WALConfig{
		Dir:            cfg.WALDir,
		FsyncInterval:  cfg.WALFsyncInterval,
		FsyncRecords:   cfg.WALFsyncRecords,
		FileMaxBytes:   cfg.WALFileMaxBytes,
		DiskQuotaBytes: cfg.WALDiskQuotaBytes,
		Overflow:       cfg.WALOverflow,
	})
	if err != nil {
		return nil, err
	}
	cur, err := OpenCursor(filepath.Join(cfg.WALDir, "cursor.json"))
	if err != nil {
		_ = wal.Close()
		return nil, err
	}
	r := &realRecorder{
		cfg:       cfg,
		wal:       wal,
		cursor:    cur,
		gatewayID: cfg.GatewayID,
		sessions:  map[string]*sessionState{},
	}
	r.parser = NewRPCParser(r, RPCParserConfig{PairTimeout: cfg.RPCPairTimeout})

	if cfg.UploadURL != "" && cfg.UploadSecret != "" {
		r.uploader = NewUploader(UploaderConfig{
			WALDir:        cfg.WALDir,
			Cursor:        cur,
			UploadURL:     cfg.UploadURL,
			UploadSecret:  cfg.UploadSecret,
			BatchRecords:  cfg.UploadBatchRecords,
			BatchBytes:    cfg.UploadBatchBytes,
			FlushInterval: cfg.UploadFlushInterval,
			GatewayID:     cfg.GatewayID,
		})
		ctx, cancel := context.WithCancel(context.Background())
		r.uploadCtxCancel = cancel
		go r.uploader.Run(ctx)
	}

	// Periodic RPC pair-timeout sweep, half the pair timeout.
	sweepCtx, sweepCancel := context.WithCancel(context.Background())
	r.sweepCtxCancel = sweepCancel
	go r.sweepLoop(sweepCtx)

	return r, nil
}

func (r *realRecorder) sweepLoop(ctx context.Context) {
	interval := r.cfg.RPCPairTimeout / 2
	if interval <= 0 {
		interval = 15 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			r.parser.SweepTimeouts(now)
		}
	}
}

func (r *realRecorder) SessionOpen(m SessionMeta) string {
	id := uuid.NewString()
	r.mu.Lock()
	r.sessions[id] = &sessionState{
		id:          id,
		workspaceID: m.WorkspaceID,
		userID:      m.UserID,
		exeID:       m.ExeID,
	}
	r.mu.Unlock()
	rec := &pb.WALRecord{
		Id: id,
		Body: &pb.WALRecord_SessionOpen{SessionOpen: &pb.SessionOpen{
			WorkspaceId: m.WorkspaceID,
			UserId:      m.UserID,
			ExeId:       m.ExeID,
			TurnId:      m.TurnID,
			StreamId:    m.StreamID,
			ClientIp:    m.ClientIP,
			CapIat:      tsOrNil(m.CapIAT),
			CapExp:      tsOrNil(m.CapEXP),
			OpenedAt:    timestamppb.New(m.OpenedAt),
		}},
	}
	if err := r.wal.Append(rec); err != nil {
		slog.Error("exec-audit: SessionOpen append", "id", id, "err", err)
	}
	return id
}

func (r *realRecorder) SessionClose(sessionID, reason string, c Counters) {
	r.parser.SessionClosed(sessionID, time.Now().UTC())
	rec := &pb.WALRecord{
		Id: sessionID,
		Body: &pb.WALRecord_SessionClose{SessionClose: &pb.SessionClose{
			SessionId:       sessionID,
			ClosedAt:        timestamppb.New(time.Now().UTC()),
			CloseReason:     reason,
			FramesToBackend: int32(c.FramesToBackend),
			FramesToClient:  int32(c.FramesToClient),
			BytesToBackend:  c.BytesToBackend,
			BytesToClient:   c.BytesToClient,
		}},
	}
	if err := r.wal.Append(rec); err != nil {
		slog.Error("exec-audit: SessionClose append", "id", sessionID, "err", err)
	}
	r.mu.Lock()
	delete(r.sessions, sessionID)
	r.mu.Unlock()
}

func (r *realRecorder) OnFrameToBackend(sessionID string, frame any, raw []byte) {
	st := r.session(sessionID)
	if st == nil {
		return
	}
	r.mu.Lock()
	st.counters.FramesToBackend++
	st.counters.BytesToBackend += int64(len(raw))
	r.mu.Unlock()
	if payload := extractRelayDataPayload(frame, raw); len(payload) > 0 {
		r.parser.OnFrameToBackend(sessionID, st.workspaceID, st.userID, st.exeID, payload)
	}
}

func (r *realRecorder) OnFrameToClient(sessionID string, frame any, raw []byte) {
	st := r.session(sessionID)
	if st == nil {
		return
	}
	r.mu.Lock()
	st.counters.FramesToClient++
	st.counters.BytesToClient += int64(len(raw))
	r.mu.Unlock()
	if payload := extractRelayDataPayload(frame, raw); len(payload) > 0 {
		r.parser.OnFrameToClient(sessionID, payload)
	}
}

func (r *realRecorder) CallStart(m CallStartMeta) string {
	id := uuid.NewString()
	cs := &pb.CallStart{
		CallId:      id,
		SessionId:   m.SessionID,
		WorkspaceId: m.WorkspaceID,
		UserId:      m.UserID,
		ExeId:       m.ExeID,
		Source:      m.Source,
		RpcId:       m.RPCID,
		RpcMethod:   m.RPCMethod,
		RpcKind:     m.RPCKind,
		StartedAt:   timestamppb.New(m.StartedAt),
	}
	r.populatePayload(&cs.RequestBytes, &cs.RequestSize, &cs.RequestSha256, m.Request)
	rec := &pb.WALRecord{Id: id, Body: &pb.WALRecord_CallStart{CallStart: cs}}
	if err := r.wal.Append(rec); err != nil {
		slog.Error("exec-audit: CallStart append", "id", id, "err", err)
	}
	return id
}

func (r *realRecorder) CallEnd(callID string, m CallEndMeta) {
	ce := &pb.CallEnd{
		CallId:       callID,
		CompletedAt:  timestamppb.New(m.CompletedAt),
		IsError:      m.IsError,
		ErrorSummary: m.ErrorSummary,
	}
	r.populatePayload(&ce.ResponseBytes, &ce.ResponseSize, &ce.ResponseSha256, m.Response)
	rec := &pb.WALRecord{Id: callID, Body: &pb.WALRecord_CallEnd{CallEnd: ce}}
	if err := r.wal.Append(rec); err != nil {
		slog.Error("exec-audit: CallEnd append", "id", callID, "err", err)
	}
}

func (r *realRecorder) Close(ctx context.Context) error {
	if r.uploadCtxCancel != nil {
		r.uploadCtxCancel()
	}
	if r.sweepCtxCancel != nil {
		r.sweepCtxCancel()
	}
	if err := r.wal.Sync(); err != nil {
		slog.Warn("exec-audit: final sync", "err", err)
	}
	return r.wal.Close()
}

func (r *realRecorder) session(id string) *sessionState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[id]
}

// populatePayload sets size + sha256 always; sets the inline bytes only
// if under PayloadMaxBytes. Above-cap payloads land in the DB with hash
// + size metadata only, matching the ingest handler's contract.
func (r *realRecorder) populatePayload(out *[]byte, size *int32, hash *string, raw []byte) {
	*size = int32(len(raw))
	if len(raw) == 0 {
		return
	}
	sum := sha256.Sum256(raw)
	*hash = hex.EncodeToString(sum[:])
	if len(raw) > r.cfg.PayloadMaxBytes {
		return
	}
	*out = raw
}

func tsOrNil(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// extractRelayDataPayload pulls the inner payload bytes from a
// RelayMessageFrame. Returns nil for non-Data body kinds (Resume,
// Reset, Ack, Heartbeat). Falls back to unmarshaling raw if the typed
// frame is nil.
func extractRelayDataPayload(frame any, raw []byte) []byte {
	if rmf, ok := frame.(*relaypb.RelayMessageFrame); ok {
		if d := rmf.GetData(); d != nil {
			return d.GetPayload()
		}
		return nil
	}
	var rmf relaypb.RelayMessageFrame
	if err := proto.Unmarshal(raw, &rmf); err == nil {
		if d := rmf.GetData(); d != nil {
			return d.GetPayload()
		}
	}
	return nil
}
