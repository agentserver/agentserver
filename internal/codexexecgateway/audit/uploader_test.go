package audit_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestUploader_SuccessfulBatchAdvancesCursor(t *testing.T) {
	dir := t.TempDir()

	// Write 3 WAL records.
	w, err := audit.OpenWAL(audit.WALConfig{
		Dir: dir, FsyncRecords: 1, FsyncInterval: time.Minute,
		FileMaxBytes: 1 << 20, DiskQuotaBytes: 1 << 20, Overflow: "fail",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := w.Append(&pb.WALRecord{
			Id: fmt.Sprintf("rec-%d", i),
			Body: &pb.WALRecord_SessionOpen{SessionOpen: &pb.SessionOpen{
				WorkspaceId: "ws", ExeId: "exe", StreamId: "s",
				OpenedAt: timestamppb.New(time.Now()),
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	_ = w.Close()

	// Stub agentserver: accepts protobuf, returns 200.
	var receivedRecords int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Secret") != "test-secret" {
			w.WriteHeader(401)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var batch pb.BatchRecords
		_ = proto.Unmarshal(body, &batch)
		atomic.AddInt32(&receivedRecords, int32(len(batch.Records)))
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"processed":3,"skipped":0}`))
	}))
	defer srv.Close()

	cur, err := audit.OpenCursor(filepath.Join(dir, "cursor.json"))
	if err != nil {
		t.Fatal(err)
	}
	u := audit.NewUploader(audit.UploaderConfig{
		WALDir:        dir,
		Cursor:        cur,
		UploadURL:     srv.URL,
		UploadSecret:  "test-secret",
		BatchRecords:  10,
		BatchBytes:    1 << 20,
		FlushInterval: 50 * time.Millisecond,
		GatewayID:     "test",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go u.Run(ctx)

	// Wait for the upload to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&receivedRecords) >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&receivedRecords); got != 3 {
		t.Fatalf("expected 3 records, got %d", got)
	}

	// Give the uploader a beat to save the cursor.
	time.Sleep(100 * time.Millisecond)

	// Cursor should now reflect every WAL file fully consumed.
	cur2, err := audit.OpenCursor(filepath.Join(dir, "cursor.json"))
	if err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "wal-*.log"))
	for _, m := range matches {
		info, _ := os.Stat(m)
		if cur2.Offset(filepath.Base(m)) != info.Size() {
			t.Errorf("cursor for %s not at EOF: got %d / want %d",
				filepath.Base(m), cur2.Offset(filepath.Base(m)), info.Size())
		}
	}
}

func TestUploader_RetriesOn5xx(t *testing.T) {
	dir := t.TempDir()
	w, _ := audit.OpenWAL(audit.WALConfig{
		Dir: dir, FsyncRecords: 1, FsyncInterval: time.Minute,
		FileMaxBytes: 1 << 20, DiskQuotaBytes: 1 << 20, Overflow: "fail",
	})
	_ = w.Append(&pb.WALRecord{
		Id: "rec-retry",
		Body: &pb.WALRecord_SessionOpen{SessionOpen: &pb.SessionOpen{
			WorkspaceId: "ws", ExeId: "exe", StreamId: "s",
			OpenedAt: timestamppb.New(time.Now()),
		}},
	})
	_ = w.Close()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"processed":1,"skipped":0}`))
	}))
	defer srv.Close()

	cur, _ := audit.OpenCursor(filepath.Join(dir, "cursor.json"))
	u := audit.NewUploader(audit.UploaderConfig{
		WALDir:        dir,
		Cursor:        cur,
		UploadURL:     srv.URL,
		BatchRecords:  10,
		BatchBytes:    1 << 20,
		FlushInterval: 20 * time.Millisecond,
		GatewayID:     "test",
		BackoffStart:  5 * time.Millisecond,
		BackoffMax:    50 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go u.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	if got := atomic.LoadInt32(&calls); got < 3 {
		t.Fatalf("expected >=3 calls (2 failures + 1 success), got %d", got)
	}
}

// TestUploader_NoReplayAfterRestart ensures that after a successful upload,
// starting a fresh Uploader against the same WAL+cursor does NOT re-send
// the records (cursor was advanced and persisted).
func TestUploader_NoReplayAfterRestart(t *testing.T) {
	dir := t.TempDir()
	w, _ := audit.OpenWAL(audit.WALConfig{
		Dir: dir, FsyncRecords: 1, FsyncInterval: time.Minute,
		FileMaxBytes: 1 << 20, DiskQuotaBytes: 1 << 20, Overflow: "fail",
	})
	_ = w.Append(&pb.WALRecord{
		Id: "rec-noreplay",
		Body: &pb.WALRecord_SessionOpen{SessionOpen: &pb.SessionOpen{
			WorkspaceId: "ws", ExeId: "exe", StreamId: "s",
			OpenedAt: timestamppb.New(time.Now()),
		}},
	})
	_ = w.Close()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"processed":1,"skipped":0}`))
	}))
	defer srv.Close()

	cfg := audit.UploaderConfig{
		WALDir: dir, UploadURL: srv.URL, GatewayID: "test",
		BatchRecords: 10, BatchBytes: 1 << 20,
		FlushInterval: 30 * time.Millisecond,
	}

	// First uploader run: should send 1 batch.
	{
		cur, _ := audit.OpenCursor(filepath.Join(dir, "cursor.json"))
		cfg.Cursor = cur
		u := audit.NewUploader(cfg)
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		go u.Run(ctx)
		time.Sleep(300 * time.Millisecond)
		cancel()
	}

	first := atomic.LoadInt32(&calls)
	if first < 1 {
		t.Fatalf("first run: expected >=1 call, got %d", first)
	}

	// Second uploader run with the persisted cursor: should NOT re-send.
	{
		cur, _ := audit.OpenCursor(filepath.Join(dir, "cursor.json"))
		cfg.Cursor = cur
		u := audit.NewUploader(cfg)
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		go u.Run(ctx)
		time.Sleep(300 * time.Millisecond)
		cancel()
	}
	if got := atomic.LoadInt32(&calls); got != first {
		t.Fatalf("second run: expected no new calls (cursor persisted), got %d additional (total %d)", got-first, got)
	}

	// Sanity: ensure bytes.Reader and other imports used.
	_ = bytes.NewReader(nil)
}
