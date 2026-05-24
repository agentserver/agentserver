package audit_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
)

func TestRealRecorder_SessionOpenLandsInWAL(t *testing.T) {
	dir := t.TempDir()
	cfg := audit.Config{
		Enabled:           true,
		WALDir:            dir,
		WALFsyncRecords:   1,
		WALFsyncInterval:  time.Minute,
		WALFileMaxBytes:   1 << 20,
		WALDiskQuotaBytes: 10 << 20,
		WALOverflow:       "fail",
		PayloadMaxBytes:   4 << 20,
		UploadURL:         "", // upload disabled
		RPCPairTimeout:    time.Minute,
		GatewayID:         "test",
	}
	r, err := audit.NewRecorder(cfg)
	if err != nil {
		t.Fatal(err)
	}

	sid, err := r.SessionOpen(audit.SessionMeta{
		WorkspaceID: "ws", ExeID: "exe", StreamID: "s1",
		OpenedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SessionOpen: %v", err)
	}
	if sid == "" {
		t.Fatal("expected non-empty sessionID")
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reader, err := audit.OpenWALReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	rec, _, err := reader.Next()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, ok := rec.Body.(*pb.WALRecord_SessionOpen); !ok {
		t.Fatalf("expected SessionOpen body, got %T", rec.Body)
	}
	if rec.Id != sid {
		t.Fatalf("expected id %s, got %s", sid, rec.Id)
	}
}

func TestRealRecorder_DisabledReturnsNoop(t *testing.T) {
	r, err := audit.NewRecorder(audit.Config{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	// noop returns non-empty UUIDs but writes nowhere.
	sid, err := r.SessionOpen(audit.SessionMeta{OpenedAt: time.Now()})
	if err != nil {
		t.Fatalf("noop SessionOpen: %v", err)
	}
	if sid == "" {
		t.Fatal("expected non-empty sessionID from noop")
	}
	_ = r.Close(context.Background())
}

func TestRealRecorder_LargePayloadHashedNotInlined(t *testing.T) {
	dir := t.TempDir()
	cfg := audit.Config{
		Enabled:           true,
		WALDir:            dir,
		WALFsyncRecords:   1,
		WALFsyncInterval:  time.Minute,
		WALFileMaxBytes:   100 << 20, // large
		WALDiskQuotaBytes: 100 << 20,
		WALOverflow:       "fail",
		PayloadMaxBytes:   1024, // cap at 1 KiB
		RPCPairTimeout:    time.Minute,
		GatewayID:         "test",
	}
	r, err := audit.NewRecorder(cfg)
	if err != nil {
		t.Fatal(err)
	}

	big := make([]byte, 4096)
	for i := range big {
		big[i] = byte(i & 0xff)
	}
	cid, err := r.CallStart(audit.CallStartMeta{
		Source: "rest", WorkspaceID: "ws", ExeID: "exe",
		Request: big, StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CallStart: %v", err)
	}
	if cid == "" {
		t.Fatal("expected non-empty callID")
	}
	_ = r.Close(context.Background())

	reader, _ := audit.OpenWALReader(dir)
	defer reader.Close()
	rec, _, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	cs, ok := rec.Body.(*pb.WALRecord_CallStart)
	if !ok {
		t.Fatalf("expected CallStart, got %T", rec.Body)
	}
	if cs.CallStart.RequestSize != 4096 {
		t.Errorf("RequestSize: got %d want 4096", cs.CallStart.RequestSize)
	}
	if cs.CallStart.RequestSha256 == "" {
		t.Error("RequestSha256: empty")
	}
	if len(cs.CallStart.RequestBytes) != 0 {
		t.Errorf("RequestBytes should be empty when over cap, got %d bytes", len(cs.CallStart.RequestBytes))
	}
}

// TestRealRecorder_SessionOpenErrorsOnFailModeFullDisk asserts the
// fail-closed contract: when the WAL refuses Append (overflow=fail with
// quota exceeded), SessionOpen propagates the error to the caller so
// the bridge handler can refuse the new session.
func TestRealRecorder_SessionOpenErrorsOnFailModeFullDisk(t *testing.T) {
	dir := t.TempDir()
	// Pre-populate with a junk wal file already over quota.
	if err := writeBlob(t, dir, "wal-19700101-000000.log", 200); err != nil {
		t.Fatal(err)
	}
	cfg := audit.Config{
		Enabled:           true,
		WALDir:            dir,
		WALFsyncRecords:   1,
		WALFsyncInterval:  time.Minute,
		WALFileMaxBytes:   1 << 20,
		WALDiskQuotaBytes: 100, // already blown by the pre-populated junk
		WALOverflow:       "fail",
		PayloadMaxBytes:   4 << 20,
		RPCPairTimeout:    time.Minute,
		GatewayID:         "test",
	}
	r, err := audit.NewRecorder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())

	_, err = r.SessionOpen(audit.SessionMeta{
		WorkspaceID: "ws", ExeID: "exe", StreamID: "s1",
		OpenedAt: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("expected SessionOpen to refuse on fail-mode quota; got nil error")
	}
}

// TestRealRecorder_CallStartErrorsOnFailModeFullDisk: same as above but
// for CallStart.
func TestRealRecorder_CallStartErrorsOnFailModeFullDisk(t *testing.T) {
	dir := t.TempDir()
	if err := writeBlob(t, dir, "wal-19700101-000000.log", 200); err != nil {
		t.Fatal(err)
	}
	cfg := audit.Config{
		Enabled:           true,
		WALDir:            dir,
		WALFsyncRecords:   1,
		WALFsyncInterval:  time.Minute,
		WALFileMaxBytes:   1 << 20,
		WALDiskQuotaBytes: 100,
		WALOverflow:       "fail",
		PayloadMaxBytes:   4 << 20,
		RPCPairTimeout:    time.Minute,
		GatewayID:         "test",
	}
	r, err := audit.NewRecorder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())

	_, err = r.CallStart(audit.CallStartMeta{
		Source: "rest", WorkspaceID: "ws", ExeID: "exe",
		StartedAt: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("expected CallStart to refuse on fail-mode quota; got nil error")
	}
}

// TestRealRecorder_SessionOpenSucceedsInDropMode: under drop mode the
// WAL silently unlinks old files to fit; SessionOpen should succeed.
func TestRealRecorder_SessionOpenSucceedsInDropMode(t *testing.T) {
	dir := t.TempDir()
	if err := writeBlob(t, dir, "wal-19700101-000000.log", 200); err != nil {
		t.Fatal(err)
	}
	cfg := audit.Config{
		Enabled:           true,
		WALDir:            dir,
		WALFsyncRecords:   1,
		WALFsyncInterval:  time.Minute,
		WALFileMaxBytes:   1 << 20,
		WALDiskQuotaBytes: 100,
		WALOverflow:       "drop",
		PayloadMaxBytes:   4 << 20,
		RPCPairTimeout:    time.Minute,
		GatewayID:         "test",
	}
	r, err := audit.NewRecorder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())

	if _, err := r.SessionOpen(audit.SessionMeta{
		WorkspaceID: "ws", ExeID: "exe", StreamID: "s1",
		OpenedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("drop-mode SessionOpen should succeed, got %v", err)
	}
}

func writeBlob(t *testing.T, dir, name string, size int) error {
	t.Helper()
	return os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o640)
}
