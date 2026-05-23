# Exec-Gateway Audit — gateway-side + integration Implementation Plan (Plan 2b)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Prerequisite:** Plan 2a (`2026-05-23-exec-audit-agentserver.md`) must be merged first. This plan depends on the existence of `POST /internal/exec-audit/batch` on agentserver and the protobuf schema in `internal/server/exec_audit_pb/audit.proto`.

**Goal:** Build the codex-exec-gateway-side producer for the audit subsystem — local WAL, async batch uploader, JSON-RPC request/response pairing, and the integration hooks in the three traffic surfaces (envmcp WS bridge, SDK REST handlers, relay PUT/GET). Plus the Helm PVC + env wiring, plus the codex-app-gateway cap-token signing change to embed `user_id`.

**Architecture:** New package `internal/codexexecgateway/audit/` owns the Recorder, WAL writer, Uploader goroutine, and JSON-RPC parser. Recorder is a small interface; production replaces a noop default at server boot when `CXG_AUDIT_ENABLED=true`. WAL is append-only protobuf records on a PVC-backed directory; rotation by hour, fsync every 100 ms or 256 records, fail-closed when disk quota is hit. Uploader maintains a cursor file, batches ≤1 MiB or 200 records, POSTs to agentserver with exponential backoff. Integration points are: (1) `bridge.go` `runBridgePump` + `handleBridge` (envmcp source), (2) `inbound.go` `runInboundReader` (the matching backend→client direction), (3) `sdk/handlers.go` `handleToolCall` etc. wrap with CallStart/CallEnd (rest source), (4) `handlers_relay.go` `handleRelayPut`/`handleRelayGet` wrap (relay source). Cap token gets a new optional `user_id` field for envmcp attribution.

**Tech Stack:** Go 1.22+, `google.golang.org/protobuf` v1.36, `github.com/klauspost/compress/zstd`, chi router, slog. Schema shared with agentserver via `internal/server/exec_audit_pb` (added by Plan 2a — this plan reuses it).

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `internal/codexexecgateway/audit/recorder.go` | Create | `Recorder` interface + `noopRecorder` + `NewRecorder(cfg)` factory wiring WAL + Uploader. ~150 LOC. |
| `internal/codexexecgateway/audit/wal.go` | Create | Append-only WAL writer with rotation, fsync policy, disk quota. ~250 LOC. |
| `internal/codexexecgateway/audit/wal_test.go` | Create | Round-trip writes/reads, rotation triggers, quota fail-closed. |
| `internal/codexexecgateway/audit/cursor.go` | Create | `Cursor` type — atomic load/save of upload progress, per-file offset. ~80 LOC. |
| `internal/codexexecgateway/audit/cursor_test.go` | Create | Atomic write semantics, concurrent reader safety. |
| `internal/codexexecgateway/audit/uploader.go` | Create | Goroutine that reads from WAL using Cursor, batches, POSTs to agentserver with exponential backoff. ~300 LOC. |
| `internal/codexexecgateway/audit/uploader_test.go` | Create | Backoff schedule, batch flush triggers, cursor advance on 200, no-advance on 5xx. Uses `httptest.Server`. |
| `internal/codexexecgateway/audit/rpcparser.go` | Create | Parses RelayData payload as JSON-RPC, pairs request+response by id, emits CallStart/CallEnd. Per-session state with timeout sweeper. ~200 LOC. |
| `internal/codexexecgateway/audit/rpcparser_test.go` | Create | Pair matching, notifications, malformed payloads, timeout sweep. |
| `internal/codexexecgateway/audit/config.go` | Create | `Config` struct + env binding. ~80 LOC. |
| `internal/codexexecgateway/auth.go` | Modify | Add `UserID string \`json:"user_id,omitempty"\`` to `CapPayload`. Verify side accepts both old (no field) and new tokens. ~5 LOC. |
| `internal/codexexecgateway/auth_test.go` | Modify | New test: verify token signed by codex-app-gateway with user_id round-trips correctly. |
| `internal/codexexecgateway/bridge.go` | Modify | At `handleBridge` after Resume frame parsed → `recorder.SessionOpen`. At end of `handleBridge` → `recorder.SessionClose`. In `runBridgePump` per-frame → `recorder.OnFrameToBackend`. ~30 LOC added. |
| `internal/codexexecgateway/inbound.go` | Modify | In `runInboundReader` per-frame, look up the matching bridge session, → `recorder.OnFrameToClient`. ~20 LOC added. |
| `internal/codexexecgateway/sdk/handlers.go` | Modify | Wrap `handleToolCall` (and the four siblings) with `recorder.CallStart` / `recorder.CallEnd`. ~30 LOC added per handler. |
| `internal/codexexecgateway/handlers_relay.go` | Modify | Wrap `handleRelayPut` and `handleRelayGet` with CallStart/CallEnd (source="relay"). ~25 LOC added. |
| `internal/codexexecgateway/server.go` | Modify | Build the Recorder in `NewServer` when `cfg.Audit.Enabled`; pass it to bridge/inbound/sdk subsystems; Close on shutdown. ~20 LOC added. |
| `internal/codexexecgateway/config.go` | Modify | Add `Audit audit.Config` field; load env vars. ~30 LOC added. |
| `cmd/codex-exec-gateway/serve_args.go` | Modify | Optional CLI flags for the audit settings (defaults to env). ~30 LOC added. |
| `deploy/helm/agentserver/templates/codex-exec-gateway.yaml` | Modify | Add `volumeMounts` + `volumes` for the PVC; inject `CXG_AUDIT_*` env vars from new values. ~30 LOC added. |
| `deploy/helm/agentserver/templates/codex-exec-gateway-pvc.yaml` | Create | New PVC manifest gated by `.Values.execAudit.pvc.enabled`. ~25 LOC. |
| `deploy/helm/agentserver/values.yaml` | Modify | New `execAudit:` config block (enabled, payloadMaxBytes, walOverflow, pvc.{enabled,storageClass,size}, internalSecret reference). ~25 LOC. |
| `internal/codexappgateway/auth.go` (sign side) | Modify | `MintCapToken` (or whatever the signer is named) takes user_id and embeds it. ~5 LOC. |
| `internal/codexappgateway/handler_or_wherever_minting_happens.go` | Modify | Pass the user_id through at every signer callsite. Discover via grep. ~10 LOC added across 1-2 files. |

---

## Task 1: Audit package skeleton — config + Recorder interface + noop

**Files:**
- Create: `internal/codexexecgateway/audit/config.go`
- Create: `internal/codexexecgateway/audit/recorder.go`
- Create: `internal/codexexecgateway/audit/recorder_test.go`

- [ ] **Step 1: Write the failing test for noopRecorder**

Create `internal/codexexecgateway/audit/recorder_test.go`:

```go
package audit_test

import (
	"context"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
)

func TestNoopRecorder_AllMethodsAreSafe(t *testing.T) {
	r := audit.NewNoopRecorder()
	sid := r.SessionOpen(audit.SessionMeta{
		WorkspaceID: "ws", ExeID: "exe", StreamID: "s1",
		OpenedAt:    time.Now(),
	})
	if sid == "" {
		t.Fatal("expected non-empty session id even from noop")
	}
	r.OnFrameToBackend(sid, nil, nil)
	r.OnFrameToClient(sid, nil, nil)
	cid := r.CallStart(audit.CallStartMeta{
		Source: "rest", WorkspaceID: "ws", ExeID: "exe",
		StartedAt: time.Now(),
	})
	if cid == "" {
		t.Fatal("expected non-empty call id even from noop")
	}
	r.CallEnd(cid, audit.CallEndMeta{CompletedAt: time.Now()})
	r.SessionClose(sid, "ok", audit.Counters{})
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/codexexecgateway/audit/ -v
```

Expected: FAIL (package doesn't exist).

- [ ] **Step 3: Write `config.go`**

Create `internal/codexexecgateway/audit/config.go`:

```go
package audit

import (
	"os"
	"strconv"
	"time"
)

// Config controls every knob of the audit subsystem. NewConfigFromEnv
// reads CXG_AUDIT_* env vars with reasonable defaults.
type Config struct {
	Enabled                bool
	WALDir                 string
	WALFsyncInterval       time.Duration
	WALFsyncRecords        int
	WALFileMaxBytes        int64
	WALDiskQuotaBytes      int64
	WALOverflow            string // "fail" | "drop"
	PayloadMaxBytes        int
	UploadURL              string
	UploadSecret           string
	UploadBatchBytes       int
	UploadBatchRecords     int
	UploadFlushInterval    time.Duration
	RPCPairTimeout         time.Duration
	GatewayID              string
}

func NewConfigFromEnv() Config {
	cfg := Config{
		Enabled:             envBool("CXG_AUDIT_ENABLED", false),
		WALDir:              envStr("CXG_AUDIT_WAL_DIR", "/var/cxg-audit"),
		WALFsyncInterval:    envDur("CXG_AUDIT_WAL_FSYNC_INTERVAL", 100*time.Millisecond),
		WALFsyncRecords:     envInt("CXG_AUDIT_WAL_FSYNC_RECORDS", 256),
		WALFileMaxBytes:     envInt64("CXG_AUDIT_WAL_FILE_MAX_BYTES", 1<<30),    // 1 GiB
		WALDiskQuotaBytes:   envInt64("CXG_AUDIT_WAL_DISK_QUOTA_BYTES", 10<<30), // 10 GiB
		WALOverflow:         envStr("CXG_AUDIT_WAL_OVERFLOW", "fail"),
		PayloadMaxBytes:     envInt("CXG_AUDIT_PAYLOAD_MAX_BYTES", 4<<20),       // 4 MiB
		UploadURL:           envStr("CXG_AUDIT_UPLOAD_URL", ""),
		UploadSecret:        envStr("CXG_AUDIT_UPLOAD_SECRET", ""),
		UploadBatchBytes:    envInt("CXG_AUDIT_UPLOAD_BATCH_BYTES", 1<<20),       // 1 MiB
		UploadBatchRecords:  envInt("CXG_AUDIT_UPLOAD_BATCH_RECORDS", 200),
		UploadFlushInterval: envDur("CXG_AUDIT_UPLOAD_FLUSH_INTERVAL", time.Second),
		RPCPairTimeout:      envDur("CXG_AUDIT_RPC_PAIR_TIMEOUT", 30*time.Second),
		GatewayID:           envStr("CXG_AUDIT_GATEWAY_ID", "cxg"),
	}
	return cfg
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes" || v == "on"
}
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
func envInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}
func envDur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
```

- [ ] **Step 4: Write `recorder.go` with the interface + noop**

Create `internal/codexexecgateway/audit/recorder.go`:

```go
package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// SessionMeta is the input to Recorder.SessionOpen.
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

// Counters is the running totals tracked by the bridge pumps over the
// life of one session.
type Counters struct {
	FramesToBackend int
	FramesToClient  int
	BytesToBackend  int64
	BytesToClient   int64
}

// CallStartMeta is the input to Recorder.CallStart. SessionID is the
// audit session id (only set for envmcp source). For rest/relay
// sources it's empty.
type CallStartMeta struct {
	SessionID     string
	WorkspaceID   string
	UserID        string
	ExeID         string
	Source        string // "envmcp" | "rest" | "relay"
	RPCID         string
	RPCMethod     string
	RPCKind       string // "request" | "notification" | "frame" | "" for non-RPC
	Request       []byte // raw bytes; recorder owns truncation/hashing
	StartedAt     time.Time
}

// CallEndMeta is the input to Recorder.CallEnd.
type CallEndMeta struct {
	CompletedAt  time.Time
	IsError      bool
	ErrorSummary string
	Response     []byte
}

// Recorder is the audit interface used by the gateway pumps and handlers.
// Production wires this to a real Recorder backed by WAL + Uploader.
// Tests and audit-disabled deployments use NewNoopRecorder.
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

func NewNoopRecorder() Recorder { return noopRecorder{} }

func (noopRecorder) SessionOpen(m SessionMeta) string       { return uuid.NewString() }
func (noopRecorder) SessionClose(string, string, Counters)  {}
func (noopRecorder) OnFrameToBackend(string, any, []byte)   {}
func (noopRecorder) OnFrameToClient(string, any, []byte)    {}
func (noopRecorder) CallStart(m CallStartMeta) string       { return uuid.NewString() }
func (noopRecorder) CallEnd(string, CallEndMeta)            {}
func (noopRecorder) Close(context.Context) error             { return nil }
```

- [ ] **Step 5: Run the test**

```bash
go test ./internal/codexexecgateway/audit/ -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/codexexecgateway/audit/config.go internal/codexexecgateway/audit/recorder.go internal/codexexecgateway/audit/recorder_test.go
git commit -m "$(cat <<'EOF'
feat(exec-audit-gw): package skeleton — Config + Recorder + noop

NewNoopRecorder() returns a Recorder that mints UUIDs but writes
nowhere; used in tests and when CXG_AUDIT_ENABLED is false. Production
Recorder backed by WAL + Uploader follows in subsequent commits.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: WAL writer (TDD)

**Files:**
- Create: `internal/codexexecgateway/audit/wal.go`
- Create: `internal/codexexecgateway/audit/wal_test.go`

- [ ] **Step 1: Write the failing test — round trip one record**

Create `internal/codexexecgateway/audit/wal_test.go`:

```go
package audit_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestWAL_RoundTripSingleRecord(t *testing.T) {
	dir := t.TempDir()
	w, err := audit.OpenWAL(audit.WALConfig{
		Dir:              dir,
		FsyncInterval:    50 * time.Millisecond,
		FsyncRecords:     1,
		FileMaxBytes:     1 << 20,
		DiskQuotaBytes:   10 << 20,
		Overflow:         "fail",
	})
	if err != nil { t.Fatal(err) }
	defer w.Close()

	rec := &pb.WALRecord{
		Id: "11111111-2222-3333-4444-555555555555",
		Body: &pb.WALRecord_SessionOpen{SessionOpen: &pb.SessionOpen{
			WorkspaceId: "ws1", ExeId: "exe1", StreamId: "s1",
			OpenedAt: timestamppb.New(time.Now()),
		}},
	}
	if err := w.Append(rec); err != nil { t.Fatal(err) }
	if err := w.Sync(); err != nil { t.Fatal(err) }

	// Open a reader over the same dir, expect to read exactly one record.
	r, err := audit.OpenWALReader(dir)
	if err != nil { t.Fatal(err) }
	defer r.Close()

	got, _, err := r.Next()
	if err != nil { t.Fatalf("read: %v", err) }
	if got.Id != rec.Id { t.Fatalf("id mismatch: got %s want %s", got.Id, rec.Id) }

	// EOF on second read.
	_, _, err = r.Next()
	if err == nil { t.Fatal("expected EOF on second read") }

	// Check the file landed where we expect.
	matches, _ := filepath.Glob(filepath.Join(dir, "wal-*.log"))
	if len(matches) != 1 { t.Fatalf("expected 1 wal file, got %d", len(matches)) }

	// Verify it's parseable as a length-prefixed protobuf stream.
	_ = proto.Marshal // ensure import used
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/codexexecgateway/audit/ -run TestWAL -v
```

Expected: FAIL (`undefined: audit.OpenWAL`).

- [ ] **Step 3: Implement WAL writer**

Create `internal/codexexecgateway/audit/wal.go`:

```go
package audit

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
	"google.golang.org/protobuf/proto"
)

type WALConfig struct {
	Dir              string
	FsyncInterval    time.Duration
	FsyncRecords     int
	FileMaxBytes     int64
	DiskQuotaBytes   int64
	Overflow         string // "fail" | "drop"
	Logger           *slog.Logger
}

type WAL struct {
	cfg     WALConfig
	mu      sync.Mutex
	cur     *os.File
	curSize int64
	logger  *slog.Logger

	recsSinceSync int
	stopSync      chan struct{}
}

func OpenWAL(cfg WALConfig) (*WAL, error) {
	if cfg.Dir == "" {
		return nil, errors.New("wal: Dir required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		return nil, fmt.Errorf("wal: mkdir %s: %w", cfg.Dir, err)
	}
	w := &WAL{cfg: cfg, logger: cfg.Logger, stopSync: make(chan struct{})}
	if err := w.rotate(); err != nil {
		return nil, err
	}
	go w.fsyncLoop()
	return w, nil
}

func (w *WAL) Append(rec *pb.WALRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cur == nil {
		return errors.New("wal: closed")
	}

	// Check disk quota before serializing — cheap upfront rejection.
	if err := w.enforceQuotaLocked(); err != nil {
		return err
	}

	if rec.WrittenAt == nil {
		rec.WrittenAt = nowPB()
	}
	bytes, err := proto.Marshal(rec)
	if err != nil {
		return fmt.Errorf("wal: marshal: %w", err)
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(bytes)))
	if _, err := w.cur.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("wal: write len: %w", err)
	}
	if _, err := w.cur.Write(bytes); err != nil {
		return fmt.Errorf("wal: write body: %w", err)
	}
	w.curSize += int64(4 + len(bytes))
	w.recsSinceSync++

	if w.recsSinceSync >= w.cfg.FsyncRecords {
		if err := w.cur.Sync(); err != nil {
			return fmt.Errorf("wal: sync: %w", err)
		}
		w.recsSinceSync = 0
	}
	if w.curSize >= w.cfg.FileMaxBytes {
		if err := w.rotateLocked(); err != nil {
			return err
		}
	}
	return nil
}

// Sync forces an fsync on the current file.
func (w *WAL) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cur == nil {
		return nil
	}
	w.recsSinceSync = 0
	return w.cur.Sync()
}

func (w *WAL) Close() error {
	close(w.stopSync)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cur != nil {
		_ = w.cur.Sync()
		err := w.cur.Close()
		w.cur = nil
		return err
	}
	return nil
}

func (w *WAL) fsyncLoop() {
	t := time.NewTicker(w.cfg.FsyncInterval)
	defer t.Stop()
	for {
		select {
		case <-w.stopSync:
			return
		case <-t.C:
			w.mu.Lock()
			if w.cur != nil && w.recsSinceSync > 0 {
				if err := w.cur.Sync(); err != nil {
					w.logger.Warn("wal: periodic sync", "err", err)
				} else {
					w.recsSinceSync = 0
				}
			}
			w.mu.Unlock()
		}
	}
}

func (w *WAL) rotate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rotateLocked()
}

func (w *WAL) rotateLocked() error {
	if w.cur != nil {
		_ = w.cur.Sync()
		_ = w.cur.Close()
		w.cur = nil
	}
	name := time.Now().UTC().Format("wal-20060102-150405.log")
	path := filepath.Join(w.cfg.Dir, name)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("wal: open %s: %w", path, err)
	}
	w.cur = f
	w.curSize = 0
	w.recsSinceSync = 0
	return nil
}

func (w *WAL) enforceQuotaLocked() error {
	entries, err := os.ReadDir(w.cfg.Dir)
	if err != nil {
		return fmt.Errorf("wal: readdir: %w", err)
	}
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		total += info.Size()
	}
	if total < w.cfg.DiskQuotaBytes {
		return nil
	}
	if w.cfg.Overflow == "drop" {
		// Walk oldest files and unlink until under quota.
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, n := range names {
			if total < w.cfg.DiskQuotaBytes {
				break
			}
			p := filepath.Join(w.cfg.Dir, n)
			info, err := os.Stat(p)
			if err != nil {
				continue
			}
			if err := os.Remove(p); err != nil {
				w.logger.Warn("wal: drop unlink", "path", p, "err", err)
				continue
			}
			total -= info.Size()
		}
		if total >= w.cfg.DiskQuotaBytes {
			return fmt.Errorf("wal: disk quota %d still exceeded after drop", w.cfg.DiskQuotaBytes)
		}
		return nil
	}
	// "fail" overflow mode: refuse the append.
	return fmt.Errorf("wal: disk quota %d exceeded (mode=fail)", w.cfg.DiskQuotaBytes)
}

// WALReader is an iterator over all WAL files in a directory.
type WALReader struct {
	dir   string
	files []string
	cur   *os.File
	curIx int
}

func OpenWALReader(dir string) (*WALReader, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "wal-*.log"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return &WALReader{dir: dir, files: matches}, nil
}

// Next reads one WAL record. Returns (rec, fileName, nil) on success,
// (nil, "", io.EOF) when all files are exhausted.
func (r *WALReader) Next() (*pb.WALRecord, string, error) {
	for {
		if r.cur == nil {
			if r.curIx >= len(r.files) {
				return nil, "", io.EOF
			}
			f, err := os.Open(r.files[r.curIx])
			if err != nil {
				return nil, "", err
			}
			r.cur = f
		}
		var lenBuf [4]byte
		if _, err := io.ReadFull(r.cur, lenBuf[:]); err != nil {
			_ = r.cur.Close()
			r.cur = nil
			r.curIx++
			if err == io.EOF {
				continue
			}
			return nil, "", err
		}
		length := binary.BigEndian.Uint32(lenBuf[:])
		body := make([]byte, length)
		if _, err := io.ReadFull(r.cur, body); err != nil {
			return nil, "", err
		}
		var rec pb.WALRecord
		if err := proto.Unmarshal(body, &rec); err != nil {
			return nil, "", err
		}
		return &rec, filepath.Base(r.files[r.curIx]), nil
	}
}

func (r *WALReader) Close() error {
	if r.cur != nil {
		_ = r.cur.Close()
		r.cur = nil
	}
	return nil
}

func nowPB() *timestamppb.Timestamp {
	return timestamppb.New(time.Now().UTC())
}
```

(Add `timestamppb "google.golang.org/protobuf/types/known/timestamppb"` to imports.)

- [ ] **Step 4: Run the test**

```bash
go test ./internal/codexexecgateway/audit/ -run TestWAL -v
```

Expected: PASS.

- [ ] **Step 5: Write the failing test for rotation**

```go
func TestWAL_RotationAtFileMaxBytes(t *testing.T) {
	dir := t.TempDir()
	w, err := audit.OpenWAL(audit.WALConfig{
		Dir: dir, FsyncRecords: 1, FsyncInterval: time.Minute,
		FileMaxBytes: 200,            // very small to force rotation
		DiskQuotaBytes: 1 << 20,
		Overflow: "fail",
	})
	if err != nil { t.Fatal(err) }
	defer w.Close()

	rec := func() *pb.WALRecord {
		return &pb.WALRecord{
			Id: "11111111-1111-1111-1111-111111111111",
			Body: &pb.WALRecord_SessionOpen{SessionOpen: &pb.SessionOpen{
				WorkspaceId: "ws", ExeId: "exe", StreamId: "s",
				OpenedAt: timestamppb.New(time.Now()),
			}},
		}
	}
	for i := 0; i < 10; i++ {
		if err := w.Append(rec()); err != nil { t.Fatalf("append %d: %v", i, err) }
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "wal-*.log"))
	if len(matches) < 2 {
		t.Fatalf("expected >=2 files after rotation, got %d", len(matches))
	}
}
```

Run, fix if needed (the impl should already rotate correctly; this is just a defensive check).

- [ ] **Step 6: Write the failing test for fail-closed quota**

```go
func TestWAL_FailClosedOnQuota(t *testing.T) {
	dir := t.TempDir()
	w, err := audit.OpenWAL(audit.WALConfig{
		Dir: dir, FsyncRecords: 1, FsyncInterval: time.Minute,
		FileMaxBytes: 1 << 20, DiskQuotaBytes: 100, // quota smaller than one record's overhead
		Overflow: "fail",
	})
	if err != nil { t.Fatal(err) }
	defer w.Close()

	// Pre-populate with a junk file >100 bytes.
	if err := os.WriteFile(filepath.Join(dir, "wal-19700101-000000.log"),
		make([]byte, 200), 0o640); err != nil { t.Fatal(err) }

	rec := &pb.WALRecord{
		Id: "00000000-0000-0000-0000-000000000001",
		Body: &pb.WALRecord_SessionOpen{SessionOpen: &pb.SessionOpen{
			WorkspaceId: "ws", ExeId: "exe", StreamId: "s",
			OpenedAt: timestamppb.New(time.Now()),
		}},
	}
	if err := w.Append(rec); err == nil {
		t.Fatal("expected Append to error under fail-mode quota")
	}
}
```

Run, fix.

- [ ] **Step 7: Commit**

```bash
git add internal/codexexecgateway/audit/wal.go internal/codexexecgateway/audit/wal_test.go
git commit -m "$(cat <<'EOF'
feat(exec-audit-gw): WAL writer + reader with rotation and quota

Append-only protobuf records (length-prefixed). Per-file size cap
triggers hourly-ish rotation (file name = wal-YYYYMMDD-HHMMSS.log).
Background goroutine fsyncs every FsyncInterval or FsyncRecords —
whichever first. Disk quota in "fail" mode errors on Append; in "drop"
mode unlinks oldest files. WALReader is a forward iterator over all
files (sorted by name) used by the uploader.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Cursor (TDD)

**Files:**
- Create: `internal/codexexecgateway/audit/cursor.go`
- Create: `internal/codexexecgateway/audit/cursor_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/codexexecgateway/audit/cursor_test.go`:

```go
package audit_test

import (
	"path/filepath"
	"testing"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
)

func TestCursor_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := audit.OpenCursor(filepath.Join(dir, "cursor.json"))
	if err != nil { t.Fatal(err) }

	// Fresh cursor reports zero offset for any file.
	if got := c.Offset("wal-1.log"); got != 0 {
		t.Fatalf("expected 0 for fresh cursor, got %d", got)
	}

	// Advance + save + reload.
	c.Advance("wal-1.log", 1024)
	c.Advance("wal-1.log", 1024) // 2nd advance: should be cumulative? or absolute?
	if err := c.Save(); err != nil { t.Fatal(err) }

	c2, err := audit.OpenCursor(filepath.Join(dir, "cursor.json"))
	if err != nil { t.Fatal(err) }
	// Convention: Advance is cumulative — the caller passes the bytes consumed
	// in this batch, not the new absolute offset.
	if got := c2.Offset("wal-1.log"); got != 2048 {
		t.Fatalf("expected 2048 after reload, got %d", got)
	}
}

func TestCursor_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	c, err := audit.OpenCursor(filepath.Join(dir, "cursor.json"))
	if err != nil { t.Fatal(err) }
	c.Advance("wal-a.log", 100)
	if err := c.Save(); err != nil { t.Fatal(err) }

	// After Save there should be no .tmp file left behind.
	matches, _ := filepath.Glob(filepath.Join(dir, "cursor*"))
	for _, m := range matches {
		if filepath.Ext(m) == ".tmp" {
			t.Fatalf("found leftover .tmp file: %s", m)
		}
	}
}
```

- [ ] **Step 2: Run to confirm failure, then implement**

```bash
go test ./internal/codexexecgateway/audit/ -run TestCursor -v
```

Create `internal/codexexecgateway/audit/cursor.go`:

```go
package audit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// Cursor tracks per-file upload progress for the audit WAL. Persistence
// is via atomic rename to the canonical path.
type Cursor struct {
	mu      sync.Mutex
	path    string
	offsets map[string]int64
}

type cursorOnDisk struct {
	Files []cursorFile `json:"files"`
}

type cursorFile struct {
	Name           string `json:"name"`
	UploadedOffset int64  `json:"uploaded_offset"`
}

func OpenCursor(path string) (*Cursor, error) {
	c := &Cursor{path: path, offsets: map[string]int64{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	var d cursorOnDisk
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	for _, f := range d.Files {
		c.offsets[f.Name] = f.UploadedOffset
	}
	return c, nil
}

func (c *Cursor) Offset(name string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offsets[name]
}

// Advance adds n bytes to the cumulative offset of name.
func (c *Cursor) Advance(name string, n int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offsets[name] += n
}

func (c *Cursor) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := cursorOnDisk{Files: make([]cursorFile, 0, len(c.offsets))}
	for k, v := range c.offsets {
		d.Files = append(d.Files, cursorFile{Name: k, UploadedOffset: v})
	}
	body, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(c.path), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, body, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}
```

- [ ] **Step 3: Run, expect pass, commit**

```bash
go test ./internal/codexexecgateway/audit/ -run TestCursor -v
```

```bash
git add internal/codexexecgateway/audit/cursor.go internal/codexexecgateway/audit/cursor_test.go
git commit -m "feat(exec-audit-gw): Cursor — per-file upload progress with atomic save

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Uploader (TDD)

**Files:**
- Create: `internal/codexexecgateway/audit/uploader.go`
- Create: `internal/codexexecgateway/audit/uploader_test.go`

The uploader is the trickiest piece in this plan. It reads from `OpenWALReader(cfg.Dir)`, advances past the cursor's offset, batches records, POSTs them to agentserver, advances the cursor on 200, retries with exponential backoff on 5xx / network errors.

- [ ] **Step 1: Write the failing test — success path**

Create `internal/codexexecgateway/audit/uploader_test.go`:

```go
package audit_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestUploader_SuccessfulBatch(t *testing.T) {
	dir := t.TempDir()

	// Write 3 WAL records.
	w, _ := audit.OpenWAL(audit.WALConfig{
		Dir: dir, FsyncRecords: 1, FsyncInterval: time.Minute,
		FileMaxBytes: 1 << 20, DiskQuotaBytes: 1 << 20, Overflow: "fail",
	})
	for i := 0; i < 3; i++ {
		_ = w.Append(&pb.WALRecord{
			Id: fmt.Sprintf("rec-%d", i),
			Body: &pb.WALRecord_SessionOpen{SessionOpen: &pb.SessionOpen{
				WorkspaceId: "ws", ExeId: "exe", StreamId: "s",
				OpenedAt: timestamppb.New(time.Now()),
			}},
		})
	}
	_ = w.Close()

	// Set up a stub agentserver.
	var receivedBatches int32
	var receivedRecords int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Secret") != "test-secret" {
			w.WriteHeader(401); return
		}
		body, _ := io.ReadAll(r.Body)
		var batch pb.BatchRecords
		_ = proto.Unmarshal(body, &batch)
		atomic.AddInt32(&receivedBatches, 1)
		atomic.AddInt32(&receivedRecords, int32(len(batch.Records)))
		w.WriteHeader(200)
		w.Write([]byte(`{"processed":3,"skipped":0}`))
	}))
	defer srv.Close()

	cur, _ := audit.OpenCursor(filepath.Join(dir, "cursor.json"))
	u := audit.NewUploader(audit.UploaderConfig{
		WALDir:            dir,
		Cursor:            cur,
		UploadURL:         srv.URL,
		UploadSecret:      "test-secret",
		BatchRecords:      10,
		BatchBytes:        1 << 20,
		FlushInterval:     50 * time.Millisecond,
		GatewayID:         "test",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go u.Run(ctx)

	// Wait for at least one batch.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&receivedRecords) >= 3 { break }
		time.Sleep(20 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&receivedRecords); got != 3 {
		t.Fatalf("expected 3 records, got %d", got)
	}

	// Cursor advanced — replay yields no new bytes.
	cur2, _ := audit.OpenCursor(filepath.Join(dir, "cursor.json"))
	matches, _ := filepath.Glob(filepath.Join(dir, "wal-*.log"))
	for _, m := range matches {
		info, _ := os.Stat(m)
		if cur2.Offset(filepath.Base(m)) != info.Size() {
			t.Fatalf("cursor for %s not at EOF: %d / %d", m, cur2.Offset(filepath.Base(m)), info.Size())
		}
	}
}

func TestUploader_RetriesOn5xx(t *testing.T) {
	dir := t.TempDir()
	w, _ := audit.OpenWAL(audit.WALConfig{Dir: dir, FsyncRecords: 1, FsyncInterval: time.Minute,
		FileMaxBytes: 1<<20, DiskQuotaBytes: 1<<20, Overflow: "fail"})
	_ = w.Append(&pb.WALRecord{
		Id: "r1",
		Body: &pb.WALRecord_SessionOpen{SessionOpen: &pb.SessionOpen{
			WorkspaceId: "ws", ExeId: "exe", StreamId: "s",
			OpenedAt: timestamppb.New(time.Now()),
		}},
	})
	_ = w.Close()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 { w.WriteHeader(500); return }
		w.WriteHeader(200)
		w.Write([]byte(`{"processed":1,"skipped":0}`))
	}))
	defer srv.Close()

	cur, _ := audit.OpenCursor(filepath.Join(dir, "cursor.json"))
	u := audit.NewUploader(audit.UploaderConfig{
		WALDir: dir, Cursor: cur, UploadURL: srv.URL,
		UploadSecret: "", BatchRecords: 10, BatchBytes: 1<<20,
		FlushInterval: 20 * time.Millisecond, GatewayID: "test",
		BackoffStart: 5 * time.Millisecond, BackoffMax: 50 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go u.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	if got := atomic.LoadInt32(&calls); got < 3 {
		t.Fatalf("expected ≥3 calls (2 failures + 1 success), got %d", got)
	}
}
```

(Add `"fmt"`, `"os"` to imports as needed.)

- [ ] **Step 2: Run, expect FAIL, implement Uploader**

```bash
go test ./internal/codexexecgateway/audit/ -run TestUploader -v
```

Create `internal/codexexecgateway/audit/uploader.go`:

```go
package audit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
	"google.golang.org/protobuf/proto"
)

type UploaderConfig struct {
	WALDir         string
	Cursor         *Cursor
	UploadURL      string
	UploadSecret   string
	BatchRecords   int
	BatchBytes     int
	FlushInterval  time.Duration
	GatewayID      string
	Logger         *slog.Logger
	HTTPClient     *http.Client
	BackoffStart   time.Duration
	BackoffMax     time.Duration
}

type Uploader struct {
	cfg UploaderConfig
	log *slog.Logger
	hc  *http.Client
}

func NewUploader(cfg UploaderConfig) *Uploader {
	if cfg.Logger == nil { cfg.Logger = slog.Default() }
	if cfg.HTTPClient == nil { cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second} }
	if cfg.BackoffStart == 0 { cfg.BackoffStart = time.Second }
	if cfg.BackoffMax == 0 { cfg.BackoffMax = 5 * time.Minute }
	if cfg.BatchRecords <= 0 { cfg.BatchRecords = 200 }
	if cfg.BatchBytes <= 0 { cfg.BatchBytes = 1 << 20 }
	if cfg.FlushInterval <= 0 { cfg.FlushInterval = time.Second }
	return &Uploader{cfg: cfg, log: cfg.Logger, hc: cfg.HTTPClient}
}

// Run drives uploads until ctx is canceled. Blocks; intended for `go u.Run(ctx)`.
func (u *Uploader) Run(ctx context.Context) {
	backoff := u.cfg.BackoffStart
	for {
		if ctx.Err() != nil { return }
		batch, perFile, totalBytes, err := u.readNextBatch()
		if err != nil {
			u.log.Warn("exec-audit uploader: read batch", "err", err)
			u.sleep(ctx, u.cfg.FlushInterval)
			continue
		}
		if len(batch.Records) == 0 {
			// Nothing to send; wait for new data.
			u.sleep(ctx, u.cfg.FlushInterval)
			continue
		}

		if err := u.postBatch(ctx, batch); err != nil {
			u.log.Warn("exec-audit uploader: post failed", "err", err, "backoff", backoff)
			u.sleep(ctx, backoff)
			backoff = nextBackoff(backoff, u.cfg.BackoffMax)
			continue
		}

		// Success — advance cursor by the bytes we just shipped.
		for fname, n := range perFile {
			u.cfg.Cursor.Advance(fname, n)
		}
		if err := u.cfg.Cursor.Save(); err != nil {
			u.log.Warn("exec-audit uploader: cursor save", "err", err)
		}
		u.log.Debug("exec-audit uploader: sent",
			"records", len(batch.Records), "bytes", totalBytes)
		backoff = u.cfg.BackoffStart
	}
}

// readNextBatch reads up to BatchRecords/BatchBytes from the WAL starting
// after the cursor. perFile maps file name → bytes consumed so the caller
// can advance the cursor after a successful upload.
func (u *Uploader) readNextBatch() (*pb.BatchRecords, map[string]int64, int, error) {
	r, err := OpenWALReader(u.cfg.WALDir)
	if err != nil { return nil, nil, 0, err }
	defer r.Close()

	// Skip past cursor offsets file-by-file.
	if err := r.SeekPastCursor(u.cfg.Cursor); err != nil {
		return nil, nil, 0, err
	}

	batch := &pb.BatchRecords{GatewayId: u.cfg.GatewayID}
	perFile := map[string]int64{}
	totalBytes := 0
	for {
		rec, fname, n, err := r.NextWithSize()
		if errors.Is(err, io.EOF) { break }
		if err != nil { return nil, nil, 0, err }
		batch.Records = append(batch.Records, rec)
		perFile[fname] += n
		totalBytes += int(n)
		if len(batch.Records) >= u.cfg.BatchRecords { break }
		if totalBytes >= u.cfg.BatchBytes { break }
	}
	return batch, perFile, totalBytes, nil
}

func (u *Uploader) postBatch(ctx context.Context, batch *pb.BatchRecords) error {
	body, err := proto.Marshal(batch)
	if err != nil { return fmt.Errorf("marshal: %w", err) }
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.cfg.UploadURL, bytes.NewReader(body))
	if err != nil { return err }
	req.Header.Set("Content-Type", "application/x-protobuf")
	if u.cfg.UploadSecret != "" {
		req.Header.Set("X-Internal-Secret", u.cfg.UploadSecret)
	}
	resp, err := u.hc.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		// Auth misconfig — don't retry. Treat as fatal.
		return fmt.Errorf("auth: %d", resp.StatusCode)
	}
	if resp.StatusCode >= 500 || resp.StatusCode == 429 {
		return fmt.Errorf("server: %d", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("unexpected: %d", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func (u *Uploader) sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func nextBackoff(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max { return max }
	return next
}
```

Extend `WALReader` (in `wal.go`) with the two helpers:

```go
// SeekPastCursor positions the reader to start at the first byte past
// the cursor's offset for each file.
func (r *WALReader) SeekPastCursor(c *Cursor) error {
	// Filter out files we've fully consumed, and seek into the first
	// partially-consumed file.
	out := r.files[:0]
	for _, p := range r.files {
		name := filepath.Base(p)
		info, err := os.Stat(p)
		if err != nil { return err }
		off := c.Offset(name)
		if off >= info.Size() {
			continue // already done
		}
		out = append(out, p)
	}
	r.files = out
	r.curIx = 0
	return nil
}

// NextWithSize is like Next but also returns the byte count consumed.
func (r *WALReader) NextWithSize() (*pb.WALRecord, string, int64, error) {
	if r.cur == nil {
		if r.curIx >= len(r.files) {
			return nil, "", 0, io.EOF
		}
		f, err := os.Open(r.files[r.curIx])
		if err != nil { return nil, "", 0, err }
		// Seek past cursor offset for this file.
		// (Caller must have invoked SeekPastCursor first.)
		r.cur = f
	}
	var lenBuf [4]byte
	if _, err := io.ReadFull(r.cur, lenBuf[:]); err != nil {
		_ = r.cur.Close()
		r.cur = nil
		r.curIx++
		if err == io.EOF { return r.NextWithSize() }
		return nil, "", 0, err
	}
	length := binary.BigEndian.Uint32(lenBuf[:])
	body := make([]byte, length)
	if _, err := io.ReadFull(r.cur, body); err != nil {
		return nil, "", 0, err
	}
	var rec pb.WALRecord
	if err := proto.Unmarshal(body, &rec); err != nil {
		return nil, "", 0, err
	}
	return &rec, filepath.Base(r.files[r.curIx]), int64(4 + length), nil
}
```

(Hmm — the SeekPastCursor logic above filters out fully-consumed files but doesn't seek INTO a partially-consumed file. Fix that: when opening the file in NextWithSize, before reading the first length-prefix, do `f.Seek(cursor.Offset(name), 0)`. To do this cleanly, give WALReader an optional `cursor *Cursor` field set by SeekPastCursor.)

Update the impl accordingly. The TDD test from Step 1 will catch the bug if seek isn't applied (replay after a successful upload would re-send the same records).

- [ ] **Step 3: Run tests until both pass**

```bash
go test ./internal/codexexecgateway/audit/ -run TestUploader -v
```

Expected: both PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/codexexecgateway/audit/uploader.go internal/codexexecgateway/audit/uploader_test.go internal/codexexecgateway/audit/wal.go
git commit -m "$(cat <<'EOF'
feat(exec-audit-gw): Uploader goroutine — batched POST with backoff

Reads WAL forward from the persisted Cursor, batches ≤BatchRecords or
≤BatchBytes (whichever first), POSTs to agentserver as protobuf-encoded
BatchRecords with X-Internal-Secret. On 200: advance cursor + reset
backoff. On 5xx/network: exponential backoff (capped at BackoffMax).
On 401/403: fatal — auth misconfig requires operator intervention.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: JSON-RPC parser (TDD)

**Files:**
- Create: `internal/codexexecgateway/audit/rpcparser.go`
- Create: `internal/codexexecgateway/audit/rpcparser_test.go`

Parses `RelayData.payload` bytes as JSON-RPC; pairs request+response by id within a session; emits `CallStart` on request, `CallEnd` on matching response, and `CallEnd(is_error: timeout)` after `RPCPairTimeout` if no response arrived.

- [ ] **Step 1: Write failing tests covering: request/response pairing, notification (no pair), malformed JSON (graceful no-op), timeout sweep**

Create `internal/codexexecgateway/audit/rpcparser_test.go`:

```go
package audit_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
)

type capRecorder struct {
	mu    sync.Mutex
	starts []audit.CallStartMeta
	ends   map[string]audit.CallEndMeta
}

func newCapRecorder() *capRecorder { return &capRecorder{ends: map[string]audit.CallEndMeta{}} }
func (r *capRecorder) SessionOpen(audit.SessionMeta) string                                  { return "" }
func (r *capRecorder) SessionClose(string, string, audit.Counters)                           {}
func (r *capRecorder) OnFrameToBackend(string, any, []byte)                                  {}
func (r *capRecorder) OnFrameToClient(string, any, []byte)                                   {}
func (r *capRecorder) CallStart(m audit.CallStartMeta) string {
	r.mu.Lock(); defer r.mu.Unlock()
	id := "call-" + m.RPCID
	r.starts = append(r.starts, m)
	return id
}
func (r *capRecorder) CallEnd(id string, m audit.CallEndMeta) {
	r.mu.Lock(); defer r.mu.Unlock()
	r.ends[id] = m
}
func (r *capRecorder) Close(context.Context) error { return nil }

func TestRPCParser_RequestResponsePair(t *testing.T) {
	cap := newCapRecorder()
	p := audit.NewRPCParser(cap, audit.RPCParserConfig{PairTimeout: time.Minute})

	p.OnFrameToBackend("session-1", "ws", "user", "exe", []byte(`
		{"jsonrpc":"2.0","id":42,"method":"shell","params":{"cmd":"ls"}}
	`))
	p.OnFrameToClient("session-1", []byte(`
		{"jsonrpc":"2.0","id":42,"result":{"stdout":"foo"}}
	`))

	cap.mu.Lock(); defer cap.mu.Unlock()
	if len(cap.starts) != 1 { t.Fatalf("expected 1 CallStart, got %d", len(cap.starts)) }
	if cap.starts[0].RPCMethod != "shell" { t.Fatalf("bad method: %+v", cap.starts[0]) }
	if _, ok := cap.ends["call-42"]; !ok { t.Fatal("expected CallEnd for id=42") }
}

func TestRPCParser_NotificationProducesCallStartOnly(t *testing.T) {
	cap := newCapRecorder()
	p := audit.NewRPCParser(cap, audit.RPCParserConfig{PairTimeout: time.Minute})
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`
		{"jsonrpc":"2.0","method":"progress","params":{"n":1}}
	`))
	cap.mu.Lock(); defer cap.mu.Unlock()
	if len(cap.starts) != 1 { t.Fatalf("expected 1 CallStart for notification, got %d", len(cap.starts)) }
	if cap.starts[0].RPCKind != "notification" { t.Fatalf("expected kind=notification, got %s", cap.starts[0].RPCKind) }
	if len(cap.ends) != 0 { t.Fatalf("notifications shouldn't produce CallEnd, got %d", len(cap.ends)) }
}

func TestRPCParser_MalformedPayloadIgnored(t *testing.T) {
	cap := newCapRecorder()
	p := audit.NewRPCParser(cap, audit.RPCParserConfig{PairTimeout: time.Minute})
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`not json at all`))
	cap.mu.Lock(); defer cap.mu.Unlock()
	if len(cap.starts) != 0 { t.Fatalf("expected no calls for malformed payload, got %d", len(cap.starts)) }
}

func TestRPCParser_TimeoutEmitsErrorCallEnd(t *testing.T) {
	cap := newCapRecorder()
	p := audit.NewRPCParser(cap, audit.RPCParserConfig{PairTimeout: 50 * time.Millisecond})
	p.OnFrameToBackend("s1", "ws", "user", "exe", []byte(`
		{"jsonrpc":"2.0","id":99,"method":"slow","params":{}}
	`))
	time.Sleep(200 * time.Millisecond)
	p.SweepTimeouts(time.Now())
	cap.mu.Lock(); defer cap.mu.Unlock()
	end, ok := cap.ends["call-99"]
	if !ok { t.Fatal("expected timeout CallEnd for id=99") }
	if !end.IsError || end.ErrorSummary == "" {
		t.Fatalf("expected is_error + summary, got %+v", end)
	}
}
```

- [ ] **Step 2: Implement**

Create `internal/codexexecgateway/audit/rpcparser.go`:

```go
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
	callID     string
	startedAt  time.Time
}

type RPCParser struct {
	rec Recorder
	cfg RPCParserConfig
	mu  sync.Mutex
	// pending: session_id → rpc_id → call info
	pending map[string]map[string]pendingCall
}

func NewRPCParser(rec Recorder, cfg RPCParserConfig) *RPCParser {
	if cfg.PairTimeout <= 0 { cfg.PairTimeout = 30 * time.Second }
	return &RPCParser{rec: rec, cfg: cfg, pending: map[string]map[string]pendingCall{}}
}

// OnFrameToBackend parses a request/notification payload. Workspace/user/exe
// come from the session context (caller has them already).
func (p *RPCParser) OnFrameToBackend(sessionID, wsID, userID, exeID string, payload []byte) {
	id, method, kind, ok := parseRPC(payload)
	if !ok { return }
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
	callID := p.rec.CallStart(startMeta)
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

// OnFrameToClient parses a response payload. If it matches a pending
// request (by session+id), emits CallEnd.
func (p *RPCParser) OnFrameToClient(sessionID string, payload []byte) {
	id, _, kind, ok := parseRPC(payload)
	if !ok { return }
	if kind != "response" && kind != "error" {
		return
	}
	p.mu.Lock()
	pc, found := p.pending[sessionID][id]
	if found {
		delete(p.pending[sessionID], id)
	}
	p.mu.Unlock()
	if !found { return }

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
// any request older than cfg.PairTimeout. Caller invokes periodically.
func (p *RPCParser) SweepTimeouts(now time.Time) {
	p.mu.Lock()
	timed := []pendingCall{}
	for sid, m := range p.pending {
		for id, pc := range m {
			if now.Sub(pc.startedAt) >= p.cfg.PairTimeout {
				timed = append(timed, pc)
				delete(m, id)
			}
		}
		if len(m) == 0 { delete(p.pending, sid) }
	}
	p.mu.Unlock()
	for _, pc := range timed {
		p.rec.CallEnd(pc.callID, CallEndMeta{
			CompletedAt:  now,
			IsError:      true,
			ErrorSummary: fmt.Sprintf("rpc pair timeout after %s", p.cfg.PairTimeout),
		})
	}
}

// SessionClosed flushes any still-pending calls for sid as timeouts.
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

// parseRPC returns (id, method, kind, ok). kind = "request" | "notification" |
// "response" | "error". Returns ok=false for non-JSON or invalid messages.
func parseRPC(b []byte) (string, string, string, bool) {
	var m struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(b, &m); err != nil { return "", "", "", false }
	if m.JSONRPC != "2.0" { return "", "", "", false }
	idStr := ""
	if len(m.ID) > 0 {
		// id may be number or string; normalize to string
		idStr = string(m.ID)
		idStr = trimQuotes(idStr)
	}
	if m.Method != "" {
		if idStr == "" { return "", m.Method, "notification", true }
		return idStr, m.Method, "request", true
	}
	if len(m.Error) > 0 { return idStr, "", "error", true }
	if len(m.Result) > 0 { return idStr, "", "response", true }
	return "", "", "", false
}

func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func extractErrorMessage(payload []byte) string {
	var m struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &m); err != nil { return "" }
	return m.Error.Message
}
```

- [ ] **Step 3: Run, fix until green, commit**

```bash
go test ./internal/codexexecgateway/audit/ -run TestRPCParser -v
```

Expected: all four PASS.

```bash
git add internal/codexexecgateway/audit/rpcparser.go internal/codexexecgateway/audit/rpcparser_test.go
git commit -m "$(cat <<'EOF'
feat(exec-audit-gw): JSON-RPC request/response pairing

Parses RelayData.payload as JSON-RPC. Requests emit CallStart and stash
a pending entry by session+id. Matching responses pop the entry and
emit CallEnd. Notifications emit CallStart only. Malformed payloads are
ignored. SweepTimeouts emits is_error CallEnds for requests that never
got a response within PairTimeout.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Real Recorder implementation

**Files:**
- Modify: `internal/codexexecgateway/audit/recorder.go` (add `realRecorder` struct)
- Create: `internal/codexexecgateway/audit/recorder_real_test.go`

This wires WAL + Uploader + RPCParser behind the `Recorder` interface so the production gateway uses one cohesive thing.

- [ ] **Step 1: Test — recorder writes a session-open WAL record**

Create `internal/codexexecgateway/audit/recorder_real_test.go`:

```go
package audit_test

import (
	"context"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
)

func TestRealRecorder_SessionOpenLandsInWAL(t *testing.T) {
	dir := t.TempDir()
	cfg := audit.Config{
		Enabled: true, WALDir: dir, WALFsyncRecords: 1, WALFsyncInterval: time.Minute,
		WALFileMaxBytes: 1 << 20, WALDiskQuotaBytes: 10 << 20, WALOverflow: "fail",
		PayloadMaxBytes: 4 << 20, UploadURL: "", // upload disabled by empty URL — but recorder still buffers
		RPCPairTimeout: time.Minute, GatewayID: "test",
	}
	r, err := audit.NewRecorder(cfg)
	if err != nil { t.Fatal(err) }
	defer r.Close(context.Background())

	sid := r.SessionOpen(audit.SessionMeta{
		WorkspaceID: "ws", ExeID: "exe", StreamID: "s1",
		OpenedAt: time.Now().UTC(),
	})
	if sid == "" { t.Fatal("expected non-empty sessionID") }
	_ = r.Close(context.Background()) // flush

	reader, _ := audit.OpenWALReader(dir)
	defer reader.Close()
	rec, _, err := reader.Next()
	if err != nil { t.Fatalf("read: %v", err) }
	if _, ok := rec.Body.(*pb.WALRecord_SessionOpen); !ok {
		t.Fatalf("expected SessionOpen body, got %T", rec.Body)
	}
	if rec.Id != sid {
		t.Fatalf("expected id %s, got %s", sid, rec.Id)
	}
}
```

- [ ] **Step 2: Implement realRecorder**

Append to `internal/codexexecgateway/audit/recorder.go`:

```go
type realRecorder struct {
	cfg       Config
	wal       *WAL
	cursor    *Cursor
	uploader  *Uploader
	parser    *RPCParser
	gatewayID string

	mu       sync.Mutex
	sessions map[string]*sessionState
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

func NewRecorder(cfg Config) (Recorder, error) {
	if !cfg.Enabled {
		return NewNoopRecorder(), nil
	}
	wal, err := OpenWAL(WALConfig{
		Dir: cfg.WALDir, FsyncInterval: cfg.WALFsyncInterval,
		FsyncRecords: cfg.WALFsyncRecords, FileMaxBytes: cfg.WALFileMaxBytes,
		DiskQuotaBytes: cfg.WALDiskQuotaBytes, Overflow: cfg.WALOverflow,
	})
	if err != nil { return nil, err }
	cur, err := OpenCursor(filepath.Join(cfg.WALDir, "cursor.json"))
	if err != nil { _ = wal.Close(); return nil, err }
	r := &realRecorder{
		cfg: cfg, wal: wal, cursor: cur, gatewayID: cfg.GatewayID,
		sessions: map[string]*sessionState{},
	}
	r.parser = NewRPCParser(r, RPCParserConfig{PairTimeout: cfg.RPCPairTimeout})

	if cfg.UploadURL != "" && cfg.UploadSecret != "" {
		r.uploader = NewUploader(UploaderConfig{
			WALDir: cfg.WALDir, Cursor: cur, UploadURL: cfg.UploadURL,
			UploadSecret: cfg.UploadSecret, BatchRecords: cfg.UploadBatchRecords,
			BatchBytes: cfg.UploadBatchBytes, FlushInterval: cfg.UploadFlushInterval,
			GatewayID: cfg.GatewayID,
		})
		ctx, cancel := context.WithCancel(context.Background())
		r.uploadCtxCancel = cancel
		go r.uploader.Run(ctx)
	}
	// Periodic timeout sweep.
	ctx, cancel := context.WithCancel(context.Background())
	r.sweepCtxCancel = cancel
	go r.sweepLoop(ctx)

	return r, nil
}

func (r *realRecorder) sweepLoop(ctx context.Context) {
	t := time.NewTicker(r.cfg.RPCPairTimeout / 2)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done(): return
		case now := <-t.C: r.parser.SweepTimeouts(now)
		}
	}
}

func (r *realRecorder) SessionOpen(m SessionMeta) string {
	id := uuid.NewString()
	r.mu.Lock()
	r.sessions[id] = &sessionState{
		id: id, workspaceID: m.WorkspaceID, userID: m.UserID, exeID: m.ExeID,
	}
	r.mu.Unlock()
	rec := &pb.WALRecord{
		Id: id,
		Body: &pb.WALRecord_SessionOpen{SessionOpen: &pb.SessionOpen{
			WorkspaceId: m.WorkspaceID, UserId: m.UserID, ExeId: m.ExeID,
			TurnId: m.TurnID, StreamId: m.StreamID, ClientIp: m.ClientIP,
			CapIat: tsOrNil(m.CapIAT), CapExp: tsOrNil(m.CapEXP),
			OpenedAt: timestamppb.New(m.OpenedAt),
		}},
	}
	if err := r.wal.Append(rec); err != nil {
		slog.Error("exec-audit: SessionOpen append", "err", err)
	}
	return id
}

func (r *realRecorder) SessionClose(sessionID, reason string, c Counters) {
	r.parser.SessionClosed(sessionID, time.Now().UTC())
	rec := &pb.WALRecord{
		Id: sessionID,
		Body: &pb.WALRecord_SessionClose{SessionClose: &pb.SessionClose{
			SessionId: sessionID,
			ClosedAt: timestamppb.New(time.Now().UTC()),
			CloseReason: reason,
			FramesToBackend: int32(c.FramesToBackend),
			FramesToClient: int32(c.FramesToClient),
			BytesToBackend: c.BytesToBackend,
			BytesToClient: c.BytesToClient,
		}},
	}
	if err := r.wal.Append(rec); err != nil {
		slog.Error("exec-audit: SessionClose append", "err", err)
	}
	r.mu.Lock()
	delete(r.sessions, sessionID)
	r.mu.Unlock()
}

func (r *realRecorder) OnFrameToBackend(sessionID string, frame any, raw []byte) {
	st := r.session(sessionID)
	if st == nil { return }
	r.mu.Lock()
	st.counters.FramesToBackend++
	st.counters.BytesToBackend += int64(len(raw))
	r.mu.Unlock()
	// Try to parse the inner payload as JSON-RPC.
	payload := extractRelayDataPayload(frame, raw)
	if len(payload) > 0 {
		r.parser.OnFrameToBackend(sessionID, st.workspaceID, st.userID, st.exeID, payload)
	}
}

func (r *realRecorder) OnFrameToClient(sessionID string, frame any, raw []byte) {
	st := r.session(sessionID)
	if st == nil { return }
	r.mu.Lock()
	st.counters.FramesToClient++
	st.counters.BytesToClient += int64(len(raw))
	r.mu.Unlock()
	payload := extractRelayDataPayload(frame, raw)
	if len(payload) > 0 {
		r.parser.OnFrameToClient(sessionID, payload)
	}
}

func (r *realRecorder) CallStart(m CallStartMeta) string {
	id := uuid.NewString()
	cs := &pb.CallStart{
		CallId: id, SessionId: m.SessionID, WorkspaceId: m.WorkspaceID,
		UserId: m.UserID, ExeId: m.ExeID, Source: m.Source,
		RpcId: m.RPCID, RpcMethod: m.RPCMethod, RpcKind: m.RPCKind,
		StartedAt: timestamppb.New(m.StartedAt),
	}
	r.populatePayload(&cs.RequestBytes, &cs.RequestSize, &cs.RequestSha256, m.Request)
	rec := &pb.WALRecord{Id: id, Body: &pb.WALRecord_CallStart{CallStart: cs}}
	if err := r.wal.Append(rec); err != nil {
		slog.Error("exec-audit: CallStart append", "err", err)
	}
	return id
}

func (r *realRecorder) CallEnd(callID string, m CallEndMeta) {
	ce := &pb.CallEnd{
		CallId: callID,
		CompletedAt: timestamppb.New(m.CompletedAt),
		IsError: m.IsError,
		ErrorSummary: m.ErrorSummary,
	}
	r.populatePayload(&ce.ResponseBytes, &ce.ResponseSize, &ce.ResponseSha256, m.Response)
	rec := &pb.WALRecord{Id: callID, Body: &pb.WALRecord_CallEnd{CallEnd: ce}}
	if err := r.wal.Append(rec); err != nil {
		slog.Error("exec-audit: CallEnd append", "err", err)
	}
}

func (r *realRecorder) Close(ctx context.Context) error {
	if r.uploadCtxCancel != nil { r.uploadCtxCancel() }
	if r.sweepCtxCancel != nil { r.sweepCtxCancel() }
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

func (r *realRecorder) populatePayload(out *[]byte, size *int32, hash *string, raw []byte) {
	*size = int32(len(raw))
	if len(raw) == 0 { return }
	sum := sha256.Sum256(raw)
	*hash = hex.EncodeToString(sum[:])
	if len(raw) > r.cfg.PayloadMaxBytes {
		// Don't inline; size+hash only.
		return
	}
	*out = raw
}

func tsOrNil(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() { return nil }
	return timestamppb.New(t)
}

// extractRelayDataPayload tries to extract the inner protobuf payload
// bytes from a RelayMessageFrame. Returns nil for non-Data frames.
// Caller should pass either the typed frame (*relaypb.RelayMessageFrame)
// or rely on raw bytes — we parse raw if the typed frame is nil/unknown.
func extractRelayDataPayload(frame any, raw []byte) []byte {
	if rmf, ok := frame.(*relaypb.RelayMessageFrame); ok {
		if d := rmf.GetData(); d != nil { return d.GetPayload() }
		return nil
	}
	// Fallback: try to unmarshal raw.
	var rmf relaypb.RelayMessageFrame
	if err := proto.Unmarshal(raw, &rmf); err == nil {
		if d := rmf.GetData(); d != nil { return d.GetPayload() }
	}
	return nil
}
```

Add to imports:

```go
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
```

- [ ] **Step 3: Run, fix, commit**

```bash
go test ./internal/codexexecgateway/audit/ -v
```

Expected: all PASS.

```bash
git add internal/codexexecgateway/audit/recorder.go internal/codexexecgateway/audit/recorder_real_test.go
git commit -m "$(cat <<'EOF'
feat(exec-audit-gw): real Recorder wiring WAL + Uploader + RPCParser

NewRecorder(cfg) constructs WAL, Cursor, Uploader, RPCParser. SessionOpen
mints a UUID + appends a SessionOpen WAL record; SessionClose drains any
still-pending JSON-RPC pairs as timeouts. OnFrameTo{Backend,Client}
updates per-session byte/frame counters and feeds payload to the
RPC parser. CallStart/CallEnd append records directly. Close cancels the
upload goroutine and final-flushes the WAL.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Extend CapPayload with optional user_id (verify side)

**Files:**
- Modify: `internal/codexexecgateway/auth.go`
- Modify: `internal/codexexecgateway/auth_test.go`

- [ ] **Step 1: Add a test for the new field**

Append to `internal/codexexecgateway/auth_test.go`:

```go
func TestCapPayload_AcceptsUserID(t *testing.T) {
	secret := []byte("test-secret-1234567890")
	payload := codexexecgateway.CapPayload{
		TurnID: "turn-1", WorkspaceID: "ws-1", UserID: "user-7",
		IAT: time.Now().Unix(), EXP: time.Now().Add(5 * time.Minute).Unix(),
	}
	tok, err := codexexecgateway.SignCapToken(payload, secret) // adjust to existing API
	if err != nil { t.Fatal(err) }
	got, err := codexexecgateway.VerifyCapabilityToken(tok, secret)
	if err != nil { t.Fatalf("verify: %v", err) }
	if got.UserID != "user-7" {
		t.Fatalf("expected user_id=user-7, got %q", got.UserID)
	}
}

func TestCapPayload_OldTokensStillVerify(t *testing.T) {
	// Hand-crafted token without user_id field (sim old signer).
	// ... build with json.Marshal of {turn_id, workspace_id, iat, exp}, HMAC, decode
	// Expected: VerifyCapabilityToken returns payload with UserID == ""
}
```

(Look up the actual signing helper name in `internal/codexexecgateway/auth.go` — likely something like `MintCapabilityToken` or there's a test helper called `signTestCap`. If signing doesn't have a public API, use the package's existing test helper or expose a small internal test helper.)

- [ ] **Step 2: Run, expect failure (field doesn't exist), then add it**

Edit `internal/codexexecgateway/auth.go`. Find:

```go
type CapPayload struct {
	TurnID      string `json:"turn_id"`
	WorkspaceID string `json:"workspace_id"`
	IAT         int64  `json:"iat"`
	EXP         int64  `json:"exp"`
}
```

Add the optional UserID field:

```go
type CapPayload struct {
	TurnID      string `json:"turn_id"`
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id,omitempty"`
	IAT         int64  `json:"iat"`
	EXP         int64  `json:"exp"`
}
```

The `,omitempty` keeps tokens minted without user_id wire-identical, and JSON unmarshalling of old tokens leaves UserID as "".

- [ ] **Step 3: Run, expect pass, commit**

```bash
go test ./internal/codexexecgateway/ -run TestCapPayload -v
```

```bash
git add internal/codexexecgateway/auth.go internal/codexexecgateway/auth_test.go
git commit -m "$(cat <<'EOF'
feat(exec-gw-auth): CapPayload accepts optional user_id

Old tokens minted without this field continue to verify (UserID
defaults to ""). New tokens minted by codex-app-gateway (separate
commit) embed it so the audit subsystem can attribute envmcp bridge
sessions to a user.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Sign-side — codex-app-gateway embeds user_id

**Files:**
- Modify: `internal/codexappgateway/auth.go` (signer)
- Modify: per-callsite Go files where the cap token is minted (discover via grep)

- [ ] **Step 1: Find the signer + callsites**

```bash
grep -rn 'CapPayload\|MintCap\|SignCap\|cap_token\|hmac\..*Cap' internal/codexappgateway/ --include='*.go' | grep -v test
```

The likely names: `MintCapabilityToken`, `SignCap`, `signCap`. There will be one definition (signer) and 1-2 callsites that pass `(turnID, workspaceID, iat, exp)`.

- [ ] **Step 2: Add `userID` parameter to the signer**

If signature was:
```go
func MintCapabilityToken(secret []byte, turnID, workspaceID string, ttl time.Duration) (string, error)
```

Change to:
```go
func MintCapabilityToken(secret []byte, turnID, workspaceID, userID string, ttl time.Duration) (string, error)
```

Embed `UserID: userID` in the `CapPayload` literal inside.

- [ ] **Step 3: Update callsites to pass user_id**

Each callsite knows the user (it's the workspace owner driving the codex session). Look for where the workspace's user is already in scope:

```bash
grep -rn 'MintCapabilityToken\|workspace.OwnerID\|workspace.UserID\|userID' internal/codexappgateway/ --include='*.go' | grep -v test
```

For each callsite, pass the user_id from the surrounding context. If the user_id is not in scope at a particular callsite, pass `""` (audit will just have NULL user_id for those bridges — backward compatible).

- [ ] **Step 4: Test**

```bash
go build ./...
go test ./internal/codexappgateway/... -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/codexappgateway/
git commit -m "$(cat <<'EOF'
feat(codex-app-gw): embed user_id in minted cap tokens

The codex-exec-gateway audit subsystem uses this field to attribute
envmcp bridge sessions. Callsites that don't have a user_id in scope
pass "" — bridge still works, audit row just has NULL user_id (which
the read API tolerates).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: Integrate Recorder into envmcp bridge pumps

**Files:**
- Modify: `internal/codexexecgateway/server.go` — build Recorder in NewServer, pass into bridge/inbound
- Modify: `internal/codexexecgateway/bridge.go` — hook handleBridge + runBridgePump
- Modify: `internal/codexexecgateway/inbound.go` — hook runInboundReader
- Modify: `internal/codexexecgateway/multiplex.go` — expose a way to retrieve bridgeSession.auditSessionID from stream_id

- [ ] **Step 1: Add Recorder field to Server**

Edit `internal/codexexecgateway/server.go`. Find the `type Server struct` and add:

```go
	// Recorder for the exec-audit subsystem. noopRecorder when audit is disabled.
	recorder audit.Recorder
```

In `NewServer`, after the existing setup, build the recorder:

```go
	if cfg.Audit.Enabled {
		rec, err := audit.NewRecorder(cfg.Audit)
		if err != nil {
			return nil, fmt.Errorf("audit recorder: %w", err)
		}
		s.recorder = rec
	} else {
		s.recorder = audit.NewNoopRecorder()
	}
```

In the shutdown path, add `_ = s.recorder.Close(ctx)`.

- [ ] **Step 2: Wire Recorder into bridgeSession**

Edit `internal/codexexecgateway/multiplex.go`. Find the `type bridgeSession struct` and add:

```go
	// auditSessionID is set by handleBridge after recorder.SessionOpen.
	// Empty when audit is disabled (noopRecorder still mints UUIDs).
	auditSessionID string
```

- [ ] **Step 3: Hook handleBridge**

Edit `internal/codexexecgateway/bridge.go`. In `handleBridge`, after the WS upgrade and first Resume frame is read (around line 130-137), and before adding the route to inbound, capture session metadata:

```go
	// Audit: open the session and remember the id on the bridgeSession
	// so inbound.go can attribute incoming frames.
	auditSessID := s.recorder.SessionOpen(audit.SessionMeta{
		WorkspaceID: cap.WorkspaceID,
		UserID:      cap.UserID,
		ExeID:       exeID,
		TurnID:      cap.TurnID,
		StreamID:    streamID,
		ClientIP:    clientIP(r),
		CapIAT:      time.Unix(cap.IAT, 0),
		CapEXP:      time.Unix(cap.EXP, 0),
		OpenedAt:    time.Now().UTC(),
	})
	session.auditSessionID = auditSessID
```

(`clientIP(r)` may need a small helper — check if `r.RemoteAddr` is enough; if there's an X-Forwarded-For handling helper elsewhere in the package, reuse it.)

After the pump returns (in the defer or at the end of handleBridge), close the session:

```go
	defer func() {
		s.recorder.SessionClose(auditSessID, closeReason, audit.Counters{
			FramesToBackend: session.framesOut,
			FramesToClient:  session.framesIn,
			BytesToBackend:  session.bytesOut,
			BytesToClient:   session.bytesIn,
		})
	}()
```

(If `bridgeSession` doesn't already have counter fields, add them and increment in the pumps.)

- [ ] **Step 4: Hook runBridgePump (envmcp → backend direction)**

In `runBridgePump`, find where the frame is forwarded to inbound. Before `inbound.write(frame)`, call:

```go
	s.recorder.OnFrameToBackend(session.auditSessionID, frame, rawBytes)
	session.framesOut++
	session.bytesOut += int64(len(rawBytes))
```

(`rawBytes` is the protobuf-encoded `RelayMessageFrame`. `frame` is the parsed `*relaypb.RelayMessageFrame`. Pass both — the recorder uses the parsed form for fast path and falls back to raw if needed.)

- [ ] **Step 5: Hook runInboundReader (backend → client direction)**

Edit `internal/codexexecgateway/inbound.go`. In `runInboundReader`, where the frame is routed to a bridge session via `bridgeSession.write(frame)`, add:

```go
	s.recorder.OnFrameToClient(bridgeSess.auditSessionID, frame, rawBytes)
	bridgeSess.framesIn++
	bridgeSess.bytesIn += int64(len(rawBytes))
```

(`bridgeSess` here is the session looked up by `stream_id` in `inboundConn.routes`.)

- [ ] **Step 6: Build + run all gateway tests**

```bash
go build ./...
go test ./internal/codexexecgateway/... -count=1
```

Address any compilation errors (likely about missing counter fields or argument order). Existing tests should still pass since recorder defaults to noop.

- [ ] **Step 7: Commit**

```bash
git add internal/codexexecgateway/server.go internal/codexexecgateway/bridge.go internal/codexexecgateway/inbound.go internal/codexexecgateway/multiplex.go internal/codexexecgateway/config.go
git commit -m "$(cat <<'EOF'
feat(exec-gw): hook audit Recorder into envmcp bridge pumps

handleBridge opens an audit session after the Resume handshake;
runBridgePump records every frame going to codex-exec; runInboundReader
records every frame coming back from codex-exec. Session counters
(frames/bytes per direction) accumulate on bridgeSession and flush in
the SessionClose defer.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: Integrate Recorder into SDK REST handlers

**Files:**
- Modify: `internal/codexexecgateway/sdk/server.go` — accept Recorder in Server struct
- Modify: `internal/codexexecgateway/sdk/handlers.go` — wrap handleToolCall + handleEnvsList + handleStdin + handleOutput + handleTerminate with CallStart/CallEnd

The SDK handlers run inside the gateway process but call `bridge.Pool.Get(exeID)` internally; those bridge connections also flow through `runBridgePump` and would get double-recorded if we didn't skip them. We add a `Source` flag on the bridge.Pool's tag and the bridge.go pump skips frames originating from internal pool bridges.

**Decision**: rather than the per-frame skipping logic, just record SDK calls at the handler level only (source="rest"), and the WS frames from SDK-originated pool bridges *also* record at the frame level (source="envmcp" — wrong) — OR — give the pool-managed bridges a distinct origin tag and have the recorder skip them entirely.

Simpler: tag pool-managed bridge sessions with `auditSessionID = ""` so the frame hooks become no-ops for them.

- [ ] **Step 1: Add Recorder to sdk.Server**

Edit `internal/codexexecgateway/sdk/server.go`. Add to `Server` struct:

```go
	Recorder audit.Recorder // optional; nil → noop
```

In `Mount` or wherever Server is constructed, accept it as a parameter (or via a setter); upper-level code in `internal/codexexecgateway/server.go` passes `s.recorder`.

- [ ] **Step 2: Wrap handleToolCall**

In `internal/codexexecgateway/sdk/handlers.go`, modify `handleToolCall`:

```go
func (s *Server) handleToolCall(w http.ResponseWriter, r *http.Request) {
	wsID := workspaceFromCtx(r.Context())
	userID := userIDFromCtx(r.Context()) // new helper, mirrors workspaceFromCtx
	wsCtx, err := s.wsCtxFor(wsID)
	if err != nil { writeErr(w, http.StatusInternalServerError, "workspace_init", err.Error()); return }

	envName := chi.URLParam(r, "name")
	var req ConnectorToolCallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error()); return
	}
	argsJSON, _ := json.Marshal(req.Arguments)

	// Resolve env name → exe_id for audit attribution.
	exeID, _ := wsCtx.resolver.Resolve(envName) // method may differ — adapt
	if exeID == "" {
		// Fall back to environment_id in args (existing behaviour) — but for
		// audit we still need *some* exe_id; use envName as the audit value.
		exeID = envName
	}

	rec := s.Recorder
	if rec == nil { rec = audit.NewNoopRecorder() }
	callID := rec.CallStart(audit.CallStartMeta{
		WorkspaceID: wsID, UserID: userID, ExeID: exeID,
		Source: "rest", RPCMethod: req.Tool, RPCKind: "request",
		Request: argsJSON, StartedAt: time.Now().UTC(),
	})

	tool, ok := wsCtx.tools[req.Tool]
	if !ok {
		rec.CallEnd(callID, audit.CallEndMeta{
			CompletedAt: time.Now().UTC(), IsError: true,
			ErrorSummary: "unknown tool: " + req.Tool,
		})
		writeErr(w, http.StatusBadRequest, "unknown_tool", "no such tool: "+req.Tool)
		return
	}
	result, callErr := tool.Call(r.Context(), argsJSON)
	end := audit.CallEndMeta{CompletedAt: time.Now().UTC()}
	if callErr != nil {
		end.IsError = true
		end.ErrorSummary = callErr.Error()
	} else {
		resJSON, _ := json.Marshal(result)
		end.Response = resJSON
	}
	rec.CallEnd(callID, end)

	if callErr != nil {
		writeErr(w, http.StatusInternalServerError, "tool_error", callErr.Error())
		return
	}
	if sid := extractSessionID(result); sid != "" && s.Sessions != nil {
		s.Sessions.Register(&processes.Session{ID: sid, WorkspaceID: wsID})
	}
	writeJSON(w, result)
}
```

(Add the `userIDFromCtx` helper mirroring `workspaceFromCtx` — both should use `ctxUserID` key set in `authMiddleware`.)

- [ ] **Step 3: Wrap the other four handlers analogously**

`handleEnvsList`: source="rest", method="envs/list", request=`{}`, response=marshaled response payload.

`handleStdin`: method="processes/stdin", request=request body bytes.

`handleOutput`: method="processes/output", request=query string serialized, response=output bytes (size-capped by recorder).

`handleTerminate`: method="processes/terminate".

For each, also resolve exe_id where possible (from the session's known exe_id for process handlers; "unknown" if not derivable).

- [ ] **Step 4: Suppress duplicate frame recording for pool-managed bridges**

Edit `internal/envtools/bridge/pool.go` (or wherever the bridge.Pool opens a connection) to set a marker on the resulting bridgeSession that the gateway can detect. Then in `bridge.go runBridgePump` / `inbound.go runInboundReader`, skip the audit hook when this marker is set:

```go
	if session.auditSessionID != "" {
		s.recorder.OnFrameToBackend(session.auditSessionID, frame, rawBytes)
	}
```

This works because pool-managed bridges go through handleBridge ONLY when bridge.Pool is used by the SDK — which it is in this codebase, since the pool opens the WS via the same /bridge/{exe_id} endpoint. The simplest distinguishing marker: when the cap token used is the gateway-internal one (mint from `internalCapTokenForSDK`), pass an extra flag through, and `handleBridge` sets `auditSessionID = ""` for those.

If the actual code structure doesn't allow that distinction cleanly, fall back to: tag the bridge.Pool-issued cap token with a special `Source: "pool"` field; handleBridge sees that and skips `recorder.SessionOpen`, leaving `auditSessionID` empty.

- [ ] **Step 5: Build + test**

```bash
go build ./... && go test ./internal/codexexecgateway/... ./internal/envtools/... -count=1
```

- [ ] **Step 6: Commit**

```bash
git add internal/codexexecgateway/sdk/server.go internal/codexexecgateway/sdk/handlers.go internal/codexexecgateway/server.go internal/envtools/bridge/pool.go internal/codexexecgateway/bridge.go internal/codexexecgateway/inbound.go
git commit -m "$(cat <<'EOF'
feat(exec-gw-sdk): record /api/connectors/* tool calls in audit

Each SDK handler wraps its tool.Call (or its semantic equivalent) with
CallStart/CallEnd, source="rest". The internal bridge.Pool-managed WS
sessions that these handlers use are flagged with auditSessionID="" so
the frame-level pumps skip them — avoiding double-recording the same
logical call once at handler level and once per frame.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: Integrate Recorder into relay PUT/GET

**Files:**
- Modify: `internal/codexexecgateway/handlers_relay.go`

- [ ] **Step 1: Wrap handleRelayPut**

In `handleRelayPut`, after the ticket is looked up (so we have workspace/source/dest exe_id), record a CallStart before consuming the body, and CallEnd after:

```go
func (s *Server) handleRelayPut(w http.ResponseWriter, r *http.Request) {
	if s.relayRegistry == nil { http.Error(w, "relay disabled (no public HTTPS base URL configured)", http.StatusNotFound); return }
	urlTicket := chi.URLParam(r, "ticket")
	authTicket, ok := relay.ExtractBearerTicket(r.Header.Get("Authorization"))
	if !ok || authTicket != urlTicket { http.Error(w, "unauthorized", http.StatusUnauthorized); return }
	rel, found := s.relayRegistry.Lookup(urlTicket)
	if !found { http.Error(w, "ticket not found or expired", http.StatusGone); return }

	rec := s.recorder
	if rec == nil { rec = audit.NewNoopRecorder() }
	callID := rec.CallStart(audit.CallStartMeta{
		WorkspaceID: rel.WorkspaceID,
		ExeID: rel.DestExeID, // PUT writes to dest
		Source: "relay",
		RPCMethod: "relay_put",
		StartedAt: time.Now().UTC(),
	})

	// Use a TeeReader so we can compute size + sha256 without buffering the whole body.
	hasher := sha256.New()
	counted := &countingReader{r: io.TeeReader(r.Body, hasher)}
	status, body := rel.AcceptPut(counted)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)

	end := audit.CallEndMeta{CompletedAt: time.Now().UTC()}
	if status >= 400 {
		end.IsError = true
		end.ErrorSummary = string(body)
	}
	// For relay we never inline payload bytes (could be GB).
	// CallEnd populates only response_sha256 + size = 0 since response is the JSON
	// status, not the bytes that flowed. We could put body in here, but it's
	// the JSON status — small. For request-side, record size + hash via the
	// CallStart's omitted Request bytes; we patch by emitting a synthetic
	// "summary" CallEnd field instead. Simplest: include a one-line summary
	// in ErrorSummary or in the CallEnd's response.
	end.Response = []byte(fmt.Sprintf(`{"relay_put_bytes":%d,"relay_put_sha256":"%s"}`,
		counted.n, hex.EncodeToString(hasher.Sum(nil))))
	rec.CallEnd(callID, end)
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
```

- [ ] **Step 2: Wrap handleRelayGet symmetrically**

Same pattern, with `RPCMethod: "relay_get"` and `ExeID: rel.SourceExeID` (GET reads from source). Track downloaded bytes via TeeReader on the writer.

- [ ] **Step 3: Build + test + commit**

```bash
go build ./... && go test ./internal/codexexecgateway/... -count=1
git add internal/codexexecgateway/handlers_relay.go
git commit -m "$(cat <<'EOF'
feat(exec-gw-relay): record relay PUT/GET in audit

source="relay", exe_id = dest for PUT and source for GET. Body bytes are
never inlined (could be GB) — only size + sha256 are recorded in the
CallEnd response field as a small JSON summary.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: Helm — PVC + env wiring

**Files:**
- Create: `deploy/helm/agentserver/templates/codex-exec-gateway-pvc.yaml`
- Modify: `deploy/helm/agentserver/templates/codex-exec-gateway.yaml`
- Modify: `deploy/helm/agentserver/values.yaml`

- [ ] **Step 1: Add the PVC manifest**

Create `deploy/helm/agentserver/templates/codex-exec-gateway-pvc.yaml`:

```yaml
{{- if .Values.execAudit.pvc.enabled }}
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: {{ .Release.Name }}-codex-exec-gateway-audit
  namespace: {{ .Release.Namespace }}
  labels:
    app.kubernetes.io/name: codex-exec-gateway
    app.kubernetes.io/instance: {{ .Release.Name }}
    app.kubernetes.io/component: audit
spec:
  accessModes:
    - ReadWriteOnce
  {{- if .Values.execAudit.pvc.storageClass }}
  storageClassName: {{ .Values.execAudit.pvc.storageClass | quote }}
  {{- end }}
  resources:
    requests:
      storage: {{ .Values.execAudit.pvc.size | default "20Gi" | quote }}
{{- end }}
```

- [ ] **Step 2: Mount the PVC + inject env vars** in `templates/codex-exec-gateway.yaml`

Add to `volumeMounts:`:

```yaml
            {{- if .Values.execAudit.enabled }}
            - name: audit
              mountPath: /var/cxg-audit
            {{- end }}
```

Add to `volumes:`:

```yaml
        {{- if .Values.execAudit.enabled }}
        - name: audit
          {{- if .Values.execAudit.pvc.enabled }}
          persistentVolumeClaim:
            claimName: {{ .Release.Name }}-codex-exec-gateway-audit
          {{- else }}
          emptyDir: {}
          {{- end }}
        {{- end }}
```

Add to `env:`:

```yaml
            {{- if .Values.execAudit.enabled }}
            - name: CXG_AUDIT_ENABLED
              value: "true"
            - name: CXG_AUDIT_UPLOAD_URL
              value: "http://{{ .Release.Name }}.{{ .Release.Namespace }}.svc:{{ .Values.service.port }}/internal/exec-audit/batch"
            - name: CXG_AUDIT_UPLOAD_SECRET
              valueFrom:
                secretKeyRef:
                  name: {{ include "agentserver.internalSecretName" . }}
                  key: internal-api-secret
            - name: CXG_AUDIT_PAYLOAD_MAX_BYTES
              value: {{ .Values.execAudit.payloadMaxBytes | default 4194304 | quote }}
            - name: CXG_AUDIT_WAL_OVERFLOW
              value: {{ .Values.execAudit.walOverflow | default "fail" | quote }}
            - name: CXG_AUDIT_GATEWAY_ID
              value: "{{ .Release.Name }}-cxg-$(POD_NAME)"
            - name: POD_NAME
              valueFrom: { fieldRef: { fieldPath: metadata.name } }
            {{- end }}
```

- [ ] **Step 3: Add values.yaml block**

Edit `deploy/helm/agentserver/values.yaml`. Append (or insert before another top-level key):

```yaml
# Exec-gateway audit (Plan 2): records all instructions sent to codex-exec
# and the responses, both for envmcp WS bridges and SDK REST handlers and
# relay PUT/GET. Data lands in agentserver Postgres via /internal/exec-audit/batch.
execAudit:
  enabled: true
  payloadMaxBytes: 4194304  # 4 MiB hard cap above which only sha256+size are stored
  walOverflow: fail         # "fail" refuses new bridge handshakes when WAL disk quota hit
  pvc:
    enabled: true
    storageClass: ""
    size: 20Gi
```

- [ ] **Step 4: Render the chart**

```bash
helm template deploy/helm/agentserver 2>&1 | grep -E "exec-audit|CXG_AUDIT|persistentVolumeClaim" | head
helm lint deploy/helm/agentserver 2>&1 | tail
```

Expected: clean render, lint passes.

- [ ] **Step 5: Commit**

```bash
git add deploy/helm/agentserver/templates/codex-exec-gateway-pvc.yaml deploy/helm/agentserver/templates/codex-exec-gateway.yaml deploy/helm/agentserver/values.yaml
git commit -m "$(cat <<'EOF'
feat(helm): mount audit PVC and inject CXG_AUDIT_* env on codex-exec-gateway

ReadWriteOnce PVC at /var/cxg-audit, default 20 GiB, configurable
storageClassName via values. WAL fail-closed mode (refuses new bridges
when disk quota hit) is the default — Plan 2b spec rationale: "missing
records" violates the "完整记录" guarantee more severely than refusing
new bridges does.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: End-to-end smoke test

**Files:**
- Create: `internal/codexexecgateway/audit/e2e_test.go`

Single test that stands up: real `*audit.Recorder` → real `*WAL` → real `*Uploader` posting to a fake agentserver `httptest.Server` that runs the actual `postInternalExecAuditBatch` handler against an in-memory DB. End-to-end smoke.

- [ ] **Step 1: Write the e2e test**

Create `internal/codexexecgateway/audit/e2e_test.go`:

```go
//go:build integration

package audit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/server"
)

func TestExecAudit_EndToEnd(t *testing.T) {
	// Boot a test agentserver with the real exec-audit handler.
	srv := newTestServer(t); defer srv.Close()

	// Spin up the real Recorder pointed at the test server.
	dir := t.TempDir()
	cfg := audit.Config{
		Enabled: true, WALDir: dir, WALFsyncRecords: 1, WALFsyncInterval: 100 * time.Millisecond,
		WALFileMaxBytes: 1 << 20, WALDiskQuotaBytes: 10 << 20, WALOverflow: "fail",
		PayloadMaxBytes: 4 << 20,
		UploadURL: srv.URL + "/internal/exec-audit/batch",
		UploadSecret: srv.InternalSecret(),
		BatchRecords: 50, BatchBytes: 1 << 20,
		FlushInterval: 100 * time.Millisecond,
		RPCPairTimeout: time.Second, GatewayID: "e2e-test",
	}
	rec, err := audit.NewRecorder(cfg)
	if err != nil { t.Fatal(err) }

	// Emit a session + a paired call.
	sid := rec.SessionOpen(audit.SessionMeta{
		WorkspaceID: "ws-e2e", UserID: "user-e2e", ExeID: "exe-e2e", StreamID: "s1",
		OpenedAt: time.Now().UTC(),
	})
	cid := rec.CallStart(audit.CallStartMeta{
		SessionID: sid, WorkspaceID: "ws-e2e", UserID: "user-e2e", ExeID: "exe-e2e",
		Source: "envmcp", RPCID: "1", RPCMethod: "shell", RPCKind: "request",
		Request: []byte(`{"cmd":"ls"}`),
		StartedAt: time.Now().UTC(),
	})
	rec.CallEnd(cid, audit.CallEndMeta{
		CompletedAt: time.Now().UTC(),
		Response: []byte(`{"stdout":"file1\nfile2\n"}`),
	})
	rec.SessionClose(sid, "test_done", audit.Counters{FramesToBackend: 1, FramesToClient: 1})

	// Give the uploader time to flush.
	deadline := time.Now().Add(5 * time.Second)
	var sessions []db.AuditSession
	for time.Now().Before(deadline) {
		sessions, _ = db.ListAuditSessions(context.Background(), srv.DB(),
			db.ListAuditSessionsFilter{WorkspaceID: "ws-e2e", Limit: 10})
		if len(sessions) > 0 { break }
		time.Sleep(50 * time.Millisecond)
	}
	if len(sessions) != 1 { t.Fatalf("expected 1 session in DB, got %d", len(sessions)) }
	if sessions[0].ID != sid { t.Fatalf("id mismatch: %s vs %s", sessions[0].ID, sid) }

	calls, _ := db.ListAuditCalls(context.Background(), srv.DB(),
		db.ListAuditCallsFilter{WorkspaceID: "ws-e2e", Limit: 10})
	if len(calls) != 1 { t.Fatalf("expected 1 call, got %d", len(calls)) }

	_ = rec.Close(context.Background())
}
```

Tagged `// +build integration` so it doesn't run in the default `go test`.

- [ ] **Step 2: Run the e2e test**

```bash
go test -tags=integration ./internal/codexexecgateway/audit/ -run TestExecAudit_EndToEnd -v
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/codexexecgateway/audit/e2e_test.go
git commit -m "$(cat <<'EOF'
test(exec-audit): end-to-end smoke (recorder → WAL → uploader → DB)

Build-tagged 'integration' so it doesn't run by default. Verifies that
records flow all the way through: SessionOpen + CallStart/End + Close
emitted from the Recorder land in the agentserver Postgres exec_audit_*
tables via the real /internal/exec-audit/batch handler.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 14: Final verification + open PR

- [ ] **Step 1: Full sweep**

```bash
make test 2>&1 | tail
make build 2>&1 | tail
helm lint deploy/helm/agentserver 2>&1 | tail
```

All green.

- [ ] **Step 2: Push and open PR**

```bash
git push -u github HEAD
gh pr create --base main \
  --title "feat(exec-audit): gateway-side WAL + uploader + integrations + helm" \
  --body "$(cat <<'EOF'
## Summary

Plan 2b: gateway-side producer for the exec-audit subsystem.

- New \`internal/codexexecgateway/audit/\` package: Recorder, WAL, Cursor, Uploader, JSON-RPC parser
- CapPayload extended with optional \`user_id\` (codex-app-gateway sign-side updated to embed it)
- Integration points:
  - envmcp WS bridge (\`bridge.go\`, \`inbound.go\`): session open/close + per-frame, both directions
  - SDK REST (\`sdk/handlers.go\`): wrap all 5 \`/api/connectors/*\` handlers with CallStart/CallEnd
  - Relay (\`handlers_relay.go\`): PUT and GET wrap (size+sha256 only, never inline body)
- Helm: PVC at \`/var/cxg-audit\` (default 20 GiB), \`CXG_AUDIT_*\` env wiring
- E2E smoke test (build-tagged \`integration\`) covers the whole pipeline

## Prerequisites

- Plan 2a (#TBD) merged: agentserver-side ingester + tables + read API + retention.

## Test plan

- [x] All unit tests green (\`make test\`)
- [x] Integration test passes (\`go test -tags=integration ./internal/codexexecgateway/audit/\`)
- [x] \`helm template deploy/helm/agentserver\` renders cleanly with the new PVC + env
- [ ] Post-deploy: \`kubectl exec\` into codex-exec-gateway pod → \`ls /var/cxg-audit\` shows WAL files appearing
- [ ] Post-deploy: hit the test workspace, then \`GET /api/workspaces/{id}/exec-audit/calls\` returns non-empty list

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 3: Report PR URL.**
