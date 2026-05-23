package db

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUpsertAuditPayload_NewRow(t *testing.T) {
	d := newTestDB(t)
	bytes := []byte("hello world payload " + t.Name())
	sum := sha256.Sum256(bytes)
	hash := hex.EncodeToString(sum[:])
	t.Cleanup(func() { d.Exec(`DELETE FROM exec_audit_payloads WHERE sha256=$1`, hash) })

	id, err := d.UpsertAuditPayload(AuditPayload{
		Sha256: hash, Compressed: bytes,
		OriginalSize: len(bytes), CompressedSize: len(bytes),
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}

	// Same hash → same id, ref_count incremented.
	id2, err := d.UpsertAuditPayload(AuditPayload{
		Sha256: hash, Compressed: bytes,
		OriginalSize: len(bytes), CompressedSize: len(bytes),
	})
	if err != nil {
		t.Fatal(err)
	}
	if id2 != id {
		t.Fatalf("expected same id on dedupe, got %s vs %s", id2, id)
	}

	var refCount int
	if err := d.QueryRow(`SELECT ref_count FROM exec_audit_payloads WHERE id=$1`, id).Scan(&refCount); err != nil {
		t.Fatal(err)
	}
	if refCount != 2 {
		t.Fatalf("expected ref_count=2, got %d", refCount)
	}
}

func TestUpsertAuditPayload_MissingSha256(t *testing.T) {
	d := newTestDB(t)
	_, err := d.UpsertAuditPayload(AuditPayload{
		Compressed:     []byte("x"),
		OriginalSize:   1,
		CompressedSize: 1,
	})
	if err == nil {
		t.Fatal("expected error for empty sha256")
	}
}

func TestUpsertAuditSession_Idempotent(t *testing.T) {
	d := newTestDB(t)
	id := uuid.NewString()
	s := AuditSession{
		ID:          id,
		WorkspaceID: "ws_test_idem",
		ExeID:       "exe_x",
		StreamID:    "s1",
		OpenedAt:    time.Now().UTC(),
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM exec_audit_sessions WHERE id=$1`, id) })

	if err := d.UpsertAuditSession(s); err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertAuditSession(s); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM exec_audit_sessions WHERE id=$1`, s.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}
}

func TestUpsertAuditSession_MissingID(t *testing.T) {
	d := newTestDB(t)
	if err := d.UpsertAuditSession(AuditSession{WorkspaceID: "ws", ExeID: "e", StreamID: "s", OpenedAt: time.Now()}); err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestUpdateAuditSessionClose(t *testing.T) {
	d := newTestDB(t)
	id := uuid.NewString()
	opened := time.Now().UTC().Truncate(time.Microsecond)
	t.Cleanup(func() { d.Exec(`DELETE FROM exec_audit_sessions WHERE id=$1`, id) })

	if err := d.UpsertAuditSession(AuditSession{
		ID: id, WorkspaceID: "ws_close", ExeID: "exe_y", StreamID: "s2", OpenedAt: opened,
	}); err != nil {
		t.Fatal(err)
	}

	closedAt := opened.Add(2 * time.Second)
	if err := d.UpdateAuditSessionClose(id, closedAt, "client-disconnect", 5, 7, 1024, 2048); err != nil {
		t.Fatal(err)
	}

	var (
		gotClosedAt        time.Time
		gotReason          string
		gotFramesToBackend int
		gotFramesToClient  int
		gotBytesToBackend  int64
		gotBytesToClient   int64
	)
	err := d.QueryRow(`
        SELECT closed_at, close_reason, frames_to_backend, frames_to_client, bytes_to_backend, bytes_to_client
          FROM exec_audit_sessions WHERE id=$1`, id).Scan(
		&gotClosedAt, &gotReason, &gotFramesToBackend, &gotFramesToClient, &gotBytesToBackend, &gotBytesToClient,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !gotClosedAt.Equal(closedAt) {
		t.Errorf("closed_at: got %v, want %v", gotClosedAt, closedAt)
	}
	if gotReason != "client-disconnect" {
		t.Errorf("close_reason: got %q", gotReason)
	}
	if gotFramesToBackend != 5 || gotFramesToClient != 7 {
		t.Errorf("frames: got (%d,%d) want (5,7)", gotFramesToBackend, gotFramesToClient)
	}
	if gotBytesToBackend != 1024 || gotBytesToClient != 2048 {
		t.Errorf("bytes: got (%d,%d) want (1024,2048)", gotBytesToBackend, gotBytesToClient)
	}
}

func TestUpsertAuditCall_Idempotent(t *testing.T) {
	d := newTestDB(t)
	id := uuid.NewString()
	method := "tools/call"
	t.Cleanup(func() { d.Exec(`DELETE FROM exec_audit_calls WHERE id=$1`, id) })

	c := AuditCall{
		ID:          id,
		WorkspaceID: "ws_call",
		ExeID:       "exe_c",
		Source:      "envmcp",
		RPCMethod:   &method,
		RequestSize: 42,
		StartedAt:   time.Now().UTC(),
	}
	if err := d.UpsertAuditCall(c); err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertAuditCall(c); err != nil {
		t.Fatal(err)
	}

	var (
		count        int
		gotSource    string
		gotMethod    *string
		gotReqSize   int
	)
	if err := d.QueryRow(`SELECT COUNT(*) FROM exec_audit_calls WHERE id=$1`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}
	if err := d.QueryRow(`SELECT source, rpc_method, request_size FROM exec_audit_calls WHERE id=$1`, id).Scan(
		&gotSource, &gotMethod, &gotReqSize,
	); err != nil {
		t.Fatal(err)
	}
	if gotSource != "envmcp" || gotMethod == nil || *gotMethod != "tools/call" || gotReqSize != 42 {
		t.Errorf("row: source=%q method=%v size=%d", gotSource, gotMethod, gotReqSize)
	}
}

func TestUpsertAuditCall_InvalidSource(t *testing.T) {
	d := newTestDB(t)
	err := d.UpsertAuditCall(AuditCall{
		ID:          uuid.NewString(),
		WorkspaceID: "ws",
		ExeID:       "e",
		Source:      "bogus",
		StartedAt:   time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for invalid source")
	}
}

func TestUpsertAuditCall_MissingID(t *testing.T) {
	d := newTestDB(t)
	err := d.UpsertAuditCall(AuditCall{
		WorkspaceID: "ws", ExeID: "e", Source: "envmcp", StartedAt: time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestUpdateAuditCallEnd(t *testing.T) {
	d := newTestDB(t)
	id := uuid.NewString()
	startedAt := time.Now().UTC().Add(-100 * time.Millisecond).Truncate(time.Microsecond)
	t.Cleanup(func() { d.Exec(`DELETE FROM exec_audit_calls WHERE id=$1`, id) })

	if err := d.UpsertAuditCall(AuditCall{
		ID: id, WorkspaceID: "ws_end", ExeID: "exe_e", Source: "rest",
		RequestSize: 10, StartedAt: startedAt,
	}); err != nil {
		t.Fatal(err)
	}

	completedAt := startedAt.Add(100 * time.Millisecond)
	respSha := "deadbeef"
	respPayloadID := uuid.NewString()

	// Insert a payload row so the FK on response_payload_id is satisfied.
	t.Cleanup(func() { d.Exec(`DELETE FROM exec_audit_payloads WHERE id=$1`, respPayloadID) })
	if _, err := d.Exec(`
        INSERT INTO exec_audit_payloads (id, sha256, compressed, original_size, compressed_size, ref_count)
        VALUES ($1, $2, $3, 1, 1, 1)`, respPayloadID, "sha-"+respPayloadID, []byte("x"),
	); err != nil {
		t.Fatal(err)
	}

	if err := d.UpdateAuditCallEnd(id, completedAt, true, "boom", &respPayloadID, 99, &respSha); err != nil {
		t.Fatal(err)
	}

	var (
		gotCompletedAt    time.Time
		gotIsError        bool
		gotErrorSummary   string
		gotRespPayloadID  *string
		gotRespSize       int
		gotRespSha        *string
		gotDurationMs     int
	)
	err := d.QueryRow(`
        SELECT completed_at, is_error, error_summary, response_payload_id, response_size, response_sha256, duration_ms
          FROM exec_audit_calls WHERE id=$1`, id).Scan(
		&gotCompletedAt, &gotIsError, &gotErrorSummary, &gotRespPayloadID, &gotRespSize, &gotRespSha, &gotDurationMs,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !gotCompletedAt.Equal(completedAt) {
		t.Errorf("completed_at: got %v, want %v", gotCompletedAt, completedAt)
	}
	if !gotIsError {
		t.Error("is_error: got false, want true")
	}
	if gotErrorSummary != "boom" {
		t.Errorf("error_summary: got %q", gotErrorSummary)
	}
	if gotRespPayloadID == nil || *gotRespPayloadID != respPayloadID {
		t.Errorf("response_payload_id: got %v, want %v", gotRespPayloadID, respPayloadID)
	}
	if gotRespSize != 99 {
		t.Errorf("response_size: got %d, want 99", gotRespSize)
	}
	if gotRespSha == nil || *gotRespSha != respSha {
		t.Errorf("response_sha256: got %v", gotRespSha)
	}
	// duration_ms ≈ 100 (allow 80..120 for any clock fuzz)
	if gotDurationMs < 80 || gotDurationMs > 120 {
		t.Errorf("duration_ms: got %d, want ~100", gotDurationMs)
	}
}
