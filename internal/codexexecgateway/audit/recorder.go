package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

// Recorder is the audit interface used by the gateway pumps and
// handlers. Production wires this to a real Recorder backed by WAL +
// Uploader. Tests and audit-disabled deployments use NewNoopRecorder.
//
// SessionOpen and CallStart return error: when the WAL is in
// WALOverflow=fail mode and the disk quota has been hit, the realized
// recorder refuses to record (and thus the caller refuses to admit the
// session/call). This is the fail-closed contract the spec promises.
// SessionClose, OnFrameToBackend, and Close are best-effort — the
// session/call is already in flight; refusing mid-flight wouldn't
// improve the audit trail.
type Recorder interface {
	SessionOpen(SessionMeta) (sessionID string, err error)
	SessionClose(sessionID, reason string, c Counters)
	OnFrameToBackend(sessionID string, frame any, rawBytes []byte)
	CallStart(CallStartMeta) (callID string, err error)
	Close(ctx context.Context) error
}

type noopRecorder struct{}

// NewNoopRecorder returns a Recorder that mints fresh UUIDs but does
// not persist anything. Use when CXG_AUDIT_ENABLED=false.
func NewNoopRecorder() Recorder { return noopRecorder{} }

func (noopRecorder) SessionOpen(SessionMeta) (string, error) { return uuid.NewString(), nil }
func (noopRecorder) SessionClose(string, string, Counters)   {}
func (noopRecorder) OnFrameToBackend(string, any, []byte)    {}
func (noopRecorder) CallStart(CallStartMeta) (string, error) { return uuid.NewString(), nil }
func (noopRecorder) Close(context.Context) error             { return nil }

// ---------- real Recorder ----------

// realRecorder backs the Recorder interface with WAL + Uploader +
// RPCParser. Lifecycle: NewRecorder constructs all subsystems and
// spawns the Uploader goroutine; Close cancels it and final-flushes
// the WAL.
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
}

// sessionState holds the immutable identity fields the per-frame
// recorder hooks need to attribute frames. Counters intentionally are
// NOT tracked here — they were dead writes (SessionClose uses the
// caller-supplied bridgeSession atomic counters as the source of
// truth). Removing them lets OnFrameToBackend skip the global recorder
// mutex on the hot per-frame path.
type sessionState struct {
	id          string
	workspaceID string
	userID      string
	exeID       string
}

// NewRecorder constructs the appropriate Recorder for cfg. If
// cfg.Enabled is false (or cfg.WALDir is empty) returns a noopRecorder.
// On success starts the upload goroutine and registers a Close-to-stop
// it. Caller MUST Close on shutdown to flush the WAL.
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
	r.parser = NewRPCParser(r)

	if cfg.UploadURL != "" && cfg.UploadSecret != "" {
		u, uerr := NewUploader(UploaderConfig{
			WALDir:        cfg.WALDir,
			Cursor:        cur,
			UploadURL:     cfg.UploadURL,
			UploadSecret:  cfg.UploadSecret,
			BatchRecords:  cfg.UploadBatchRecords,
			BatchBytes:    cfg.UploadBatchBytes,
			FlushInterval: cfg.UploadFlushInterval,
			GatewayID:     cfg.GatewayID,
		})
		if uerr != nil {
			_ = wal.Close()
			return nil, fmt.Errorf("uploader init: %w", uerr)
		}
		r.uploader = u
		ctx, cancel := context.WithCancel(context.Background())
		r.uploadCtxCancel = cancel
		go r.uploader.Run(ctx)
	}

	return r, nil
}

func (r *realRecorder) SessionOpen(m SessionMeta) (string, error) {
	id := uuid.NewString()
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
	// Append BEFORE registering in r.sessions so a failure leaves no
	// orphan state behind (the bridge will refuse the session anyway).
	if err := r.wal.Append(rec); err != nil {
		slog.Error("exec-audit: SessionOpen append (refusing)", "id", id, "err", err)
		return "", err
	}
	r.mu.Lock()
	r.sessions[id] = &sessionState{
		id:          id,
		workspaceID: m.WorkspaceID,
		userID:      m.UserID,
		exeID:       m.ExeID,
	}
	r.mu.Unlock()
	return id, nil
}

func (r *realRecorder) SessionClose(sessionID, reason string, c Counters) {
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

// OnFrameToBackend looks up the (immutable) session identity and
// delegates payload extraction + parser dispatch.
func (r *realRecorder) OnFrameToBackend(sessionID string, frame any, raw []byte) {
	st := r.session(sessionID)
	if st == nil {
		return
	}
	if payload := extractRelayDataPayload(frame, raw); len(payload) > 0 {
		r.parser.OnFrameToBackend(sessionID, st.workspaceID, st.userID, st.exeID, payload)
	}
}

func (r *realRecorder) CallStart(m CallStartMeta) (string, error) {
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
		slog.Error("exec-audit: CallStart append (refusing)", "id", id, "err", err)
		return "", err
	}
	return id, nil
}

func (r *realRecorder) Close(ctx context.Context) error {
	if r.uploadCtxCancel != nil {
		r.uploadCtxCancel()
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
