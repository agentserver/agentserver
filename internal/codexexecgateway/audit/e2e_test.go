//go:build integration

package audit_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/server"
)

// newE2EAgentserver brings up a real *server.Server backed by the
// integration DB and wraps it in an httptest.Server. Skips when
// TEST_DATABASE_URL is unset so the test is a no-op in CI envs that
// haven't provisioned Postgres.
func newE2EAgentserver(t *testing.T) (*server.Server, *httptest.Server, string) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	d, err := db.Open(url)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	srv := &server.Server{DB: d}
	const secret = "e2e-internal-secret"
	t.Setenv("INTERNAL_API_SECRET", secret)
	httpSrv := httptest.NewServer(srv.Router())
	t.Cleanup(func() {
		httpSrv.Close()
		_ = d.Close()
	})
	return srv, httpSrv, secret
}

// uniqueSuffix mirrors the agentserver-side helper without crossing the
// package boundary: 12 hex chars derived from test name + nanosecond
// timestamp so concurrent runs against a shared TEST_DATABASE_URL don't
// collide on workspace_id.
func uniqueSuffix(t *testing.T) string {
	t.Helper()
	sum := sha256.Sum256([]byte(t.Name() + time.Now().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:6])
}

// TestExecAudit_EndToEnd exercises the full pipeline:
//
//	real Recorder → WAL → Uploader → httptest agentserver
//	  → real postInternalExecAuditBatch → DAL → Postgres
//
// Asserts that the session + paired call land in exec_audit_sessions /
// exec_audit_calls with the expected fields and that both request and
// response payloads are stored.
func TestExecAudit_EndToEnd(t *testing.T) {
	srv, httpSrv, secret := newE2EAgentserver(t)

	dir := t.TempDir()
	cfg := audit.Config{
		Enabled:             true,
		WALDir:              dir,
		WALFsyncRecords:     1,
		WALFsyncInterval:    100 * time.Millisecond,
		WALFileMaxBytes:     1 << 20,
		WALDiskQuotaBytes:   10 << 20,
		WALOverflow:         "fail",
		PayloadMaxBytes:     4 << 20,
		UploadURL:           httpSrv.URL + "/internal/exec-audit/batch",
		UploadSecret:        secret,
		UploadBatchRecords:  50,
		UploadBatchBytes:    1 << 20,
		UploadFlushInterval: 100 * time.Millisecond,
		RPCPairTimeout:      time.Second,
		GatewayID:           "e2e-test",
	}
	rec, err := audit.NewRecorder(cfg)
	if err != nil {
		t.Fatal(err)
	}

	wsID := "ws_e2e_" + uniqueSuffix(t)
	t.Cleanup(func() {
		_, _ = srv.DB.Exec(`DELETE FROM exec_audit_calls WHERE workspace_id=$1`, wsID)
		_, _ = srv.DB.Exec(`DELETE FROM exec_audit_sessions WHERE workspace_id=$1`, wsID)
	})

	// Emit one session + one paired call + a close.
	sid, err := rec.SessionOpen(audit.SessionMeta{
		WorkspaceID: wsID,
		UserID:      "u_e2e",
		ExeID:       "exe_e2e",
		StreamID:    "s1",
		OpenedAt:    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SessionOpen: %v", err)
	}
	cid, err := rec.CallStart(audit.CallStartMeta{
		SessionID:   sid,
		WorkspaceID: wsID,
		UserID:      "u_e2e",
		ExeID:       "exe_e2e",
		Source:      "envmcp",
		RPCID:       "1",
		RPCMethod:   "shell",
		RPCKind:     "request",
		Request:     []byte(`{"cmd":"ls"}`),
		StartedAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CallStart: %v", err)
	}
	rec.CallEnd(cid, audit.CallEndMeta{
		CompletedAt: time.Now().UTC(),
		Response:    []byte(`{"stdout":"file1\nfile2\n"}`),
	})
	rec.SessionClose(sid, "test_done", audit.Counters{
		FramesToBackend: 1, FramesToClient: 1,
	})

	// Give the uploader a chance to flush. Close blocks until the WAL is
	// drained or the context expires, which is a stronger signal than
	// raw polling — but we still poll because the agentserver ingest path
	// is async with respect to the upload HTTP response (DAL writes happen
	// synchronously in the handler, so this should be fast).
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rec.Close(closeCtx); err != nil {
		t.Fatalf("recorder close: %v", err)
	}

	// Poll the DB for the records to land. Close() should have flushed by
	// now, but allow a small grace window for the HTTP round-trip.
	var sessions []db.AuditSession
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sessions, _ = srv.DB.ListAuditSessions(db.ListAuditSessionsFilter{
			WorkspaceID: wsID, Limit: 10,
		})
		if len(sessions) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if len(sessions) != 1 {
		t.Fatalf("expected 1 session in DB, got %d", len(sessions))
	}
	s := sessions[0]
	if s.WorkspaceID != wsID || s.ExeID != "exe_e2e" {
		t.Errorf("session row mismatch: %+v", s)
	}
	if s.UserID == nil || *s.UserID != "u_e2e" {
		t.Errorf("user_id mismatch: %v", s.UserID)
	}

	calls, err := srv.DB.ListAuditCalls(db.ListAuditCallsFilter{
		WorkspaceID: wsID, Limit: 10,
	})
	if err != nil {
		t.Fatalf("list calls: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	c := calls[0]
	if c.Source != "envmcp" {
		t.Errorf("call source mismatch: %q", c.Source)
	}
	if c.RPCMethod == nil || *c.RPCMethod != "shell" {
		t.Errorf("rpc_method mismatch: %v", c.RPCMethod)
	}
	if c.RequestPayloadID == nil {
		t.Error("expected request_payload_id to be set")
	}
	if c.ResponsePayloadID == nil {
		t.Error("expected response_payload_id to be set")
	}
	if c.UserID == nil || *c.UserID != "u_e2e" {
		t.Errorf("call user_id mismatch: %v", c.UserID)
	}
}
