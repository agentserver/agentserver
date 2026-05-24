package audit_test

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func newSessionOpenRecord(id string) *pb.WALRecord {
	return &pb.WALRecord{
		Id: id,
		Body: &pb.WALRecord_SessionOpen{SessionOpen: &pb.SessionOpen{
			WorkspaceId: "ws", ExeId: "exe", StreamId: "s1",
			OpenedAt: timestamppb.New(time.Now()),
		}},
	}
}

func TestWAL_RoundTripSingleRecord(t *testing.T) {
	dir := t.TempDir()
	w, err := audit.OpenWAL(audit.WALConfig{
		Dir:            dir,
		FsyncInterval:  50 * time.Millisecond,
		FsyncRecords:   1,
		FileMaxBytes:   1 << 20,
		DiskQuotaBytes: 10 << 20,
		Overflow:       "fail",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	rec := newSessionOpenRecord("11111111-2222-3333-4444-555555555555")
	if err := w.Append(rec); err != nil {
		t.Fatal(err)
	}
	if err := w.Sync(); err != nil {
		t.Fatal(err)
	}

	r, err := audit.OpenWALReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	got, _, err := r.Next()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Id != rec.Id {
		t.Fatalf("id mismatch: got %s want %s", got.Id, rec.Id)
	}

	// EOF on second read.
	if _, _, err = r.Next(); err == nil {
		t.Fatal("expected EOF on second read")
	}

	// Verify exactly one wal file.
	matches, _ := filepath.Glob(filepath.Join(dir, "wal-*.log"))
	if len(matches) != 1 {
		t.Fatalf("expected 1 wal file, got %d", len(matches))
	}
}

func TestWAL_RotationAtFileMaxBytes(t *testing.T) {
	dir := t.TempDir()
	w, err := audit.OpenWAL(audit.WALConfig{
		Dir: dir, FsyncRecords: 1, FsyncInterval: time.Minute,
		FileMaxBytes:   200, // tiny — force rotation
		DiskQuotaBytes: 1 << 20,
		Overflow:       "fail",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	for i := 0; i < 10; i++ {
		if err := w.Append(newSessionOpenRecord("rec-rotation")); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		// Sleep 1s between filenames so file-name uniqueness (per-second
		// resolution) is preserved. In practice rotation is rare so this
		// is fine; the test just stresses it.
		time.Sleep(1100 * time.Millisecond)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "wal-*.log"))
	if len(matches) < 2 {
		t.Fatalf("expected >=2 files after rotation, got %d", len(matches))
	}
}

func TestWAL_FailClosedOnQuota(t *testing.T) {
	dir := t.TempDir()
	w, err := audit.OpenWAL(audit.WALConfig{
		Dir: dir, FsyncRecords: 1, FsyncInterval: time.Minute,
		FileMaxBytes: 1 << 20, DiskQuotaBytes: 100,
		Overflow: "fail",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Pre-populate with a junk file >100 bytes so the quota is already
	// blown when Append runs.
	if err := os.WriteFile(filepath.Join(dir, "wal-19700101-000000.log"),
		make([]byte, 200), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(newSessionOpenRecord("over-quota")); err == nil {
		t.Fatal("expected Append to error under fail-mode quota")
	}
}

func TestWAL_CloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	w, err := audit.OpenWAL(audit.WALConfig{
		Dir: dir, FsyncRecords: 1, FsyncInterval: time.Minute,
		FileMaxBytes: 1 << 20, DiskQuotaBytes: 1 << 20,
		Overflow: "fail",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second Close must not panic and should return nil.
	if err := w.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
}

func TestWAL_QuotaSkipsCursorJSON(t *testing.T) {
	dir := t.TempDir()
	// Pre-populate cursor.json + two wal files. Cursor file is small; if
	// it's mistakenly unlinked under drop mode, the next reader would
	// silently re-process every shipped record on next restart.
	if err := os.WriteFile(filepath.Join(dir, "cursor.json"),
		[]byte(`{"per_file":{}}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wal-20000101-000000.log"),
		make([]byte, 1000), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wal-20000102-000000.log"),
		make([]byte, 1000), 0o640); err != nil {
		t.Fatal(err)
	}
	w, err := audit.OpenWAL(audit.WALConfig{
		Dir: dir, FsyncRecords: 1, FsyncInterval: time.Minute,
		// 2 prepopulated wal × 1000 B = 2000 B > 1500 B quota → drop
		// would trigger if cursor.json were counted; with the W4 filter
		// only wal-* files are considered.
		FileMaxBytes: 1 << 20, DiskQuotaBytes: 1500,
		Overflow: "drop",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Append(newSessionOpenRecord("after-drop")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cursor.json")); err != nil {
		t.Fatalf("cursor.json missing after drop — quota filter regression: %v", err)
	}
}

func TestWAL_TornWriteRecoverable(t *testing.T) {
	dir := t.TempDir()
	w, err := audit.OpenWAL(audit.WALConfig{
		Dir: dir, FsyncRecords: 1, FsyncInterval: time.Minute,
		FileMaxBytes: 1 << 20, DiskQuotaBytes: 1 << 20,
		Overflow: "fail",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(newSessionOpenRecord("first")); err != nil {
		t.Fatal(err)
	}
	if err := w.Sync(); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "wal-*.log"))
	if len(matches) != 1 {
		t.Fatalf("want 1 wal file, got %d", len(matches))
	}
	// Truncate the file mid-body: keep the first record's 4-byte length
	// prefix but only half its body, simulating an EAGAIN/EIO between
	// the (now-fused) write and the next sync.
	info, _ := os.Stat(matches[0])
	if err := os.Truncate(matches[0], info.Size()-3); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	r, err := audit.OpenWALReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// First record body is now truncated; reader should skip the file
	// gracefully (returning EOF when only this file is present), and
	// bump the corrupt counter.
	rec, _, err := r.Next()
	if err != io.EOF {
		t.Fatalf("expected EOF on truncated single-record file, got rec=%v err=%v", rec, err)
	}
	if r.CorruptRecordsSkipped() == 0 {
		t.Fatalf("expected CorruptRecordsSkipped > 0 after torn-write recovery")
	}
}

func TestWAL_CorruptRecordSkipped(t *testing.T) {
	dir := t.TempDir()
	w, err := audit.OpenWAL(audit.WALConfig{
		Dir: dir, FsyncRecords: 1, FsyncInterval: time.Minute,
		FileMaxBytes: 1 << 20, DiskQuotaBytes: 1 << 20,
		Overflow: "fail",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(newSessionOpenRecord("first-poison")); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(newSessionOpenRecord("second-good")); err != nil {
		t.Fatal(err)
	}
	if err := w.Sync(); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "wal-*.log"))
	if len(matches) != 1 {
		t.Fatal("expected 1 wal file")
	}
	_ = w.Close()

	// Corrupt the first record's body in place. The file layout is:
	//   [4 byte length][N bytes body][4 byte length][N bytes body]
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	length1 := binary.BigEndian.Uint32(raw[:4])
	// Overwrite body 1 with garbage that still passes Read but fails
	// proto.Unmarshal.
	for i := uint32(4); i < 4+length1; i++ {
		raw[i] = 0xFF
	}
	if err := os.WriteFile(matches[0], raw, 0o640); err != nil {
		t.Fatal(err)
	}

	r, err := audit.OpenWALReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// First call: skips poison, returns second record. Behavior is the
	// same whether we use Next or NextWithSize+cursor — exercise Next.
	rec, _, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if rec == nil || rec.Id != "second-good" {
		t.Fatalf("expected second-good record, got %+v", rec)
	}
	if r.CorruptRecordsSkipped() != 1 {
		t.Fatalf("CorruptRecordsSkipped: want 1, got %d", r.CorruptRecordsSkipped())
	}
	// And then EOF.
	if _, _, err := r.Next(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

// silence unused warnings for imports if a test refactor drops a dep.
var _ = proto.Marshal

func TestWAL_DropOldestUnderOverflowDrop(t *testing.T) {
	dir := t.TempDir()
	// Pre-populate with two large old files
	if err := os.WriteFile(filepath.Join(dir, "wal-20000101-000000.log"),
		make([]byte, 1000), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wal-20000102-000000.log"),
		make([]byte, 1000), 0o640); err != nil {
		t.Fatal(err)
	}
	w, err := audit.OpenWAL(audit.WALConfig{
		Dir: dir, FsyncRecords: 1, FsyncInterval: time.Minute,
		// 2 prepopulated × 1000 B = 2000 B > 1500 B quota → drop triggers.
		FileMaxBytes: 1 << 20, DiskQuotaBytes: 1500,
		Overflow: "drop",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Appending should succeed and the oldest file should have been
	// unlinked to make room.
	if err := w.Append(newSessionOpenRecord("after-drop")); err != nil {
		t.Fatalf("Append under drop-mode: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "wal-2000*"))
	if len(matches) > 1 {
		t.Fatalf("expected at most 1 old file after drop, got %d", len(matches))
	}
}
