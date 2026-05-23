package audit_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
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
