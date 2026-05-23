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

	sid := r.SessionOpen(audit.SessionMeta{
		WorkspaceID: "ws", ExeID: "exe", StreamID: "s1",
		OpenedAt: time.Now().UTC(),
	})
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
	sid := r.SessionOpen(audit.SessionMeta{OpenedAt: time.Now()})
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
	cid := r.CallStart(audit.CallStartMeta{
		Source: "rest", WorkspaceID: "ws", ExeID: "exe",
		Request: big, StartedAt: time.Now().UTC(),
	})
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
