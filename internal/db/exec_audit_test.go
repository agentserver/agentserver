package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
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

// insertTestPayload writes a unique payload row and returns its id. The
// caller is responsible for arranging cleanup (e.g. via t.Cleanup deleting
// the parent session/call that references it).
func insertTestPayload(t *testing.T, d *DB, bytes []byte) string {
	t.Helper()
	sum := sha256.Sum256(bytes)
	hash := hex.EncodeToString(sum[:]) + "-" + uuid.NewString()[:8]
	id, err := d.UpsertAuditPayload(AuditPayload{
		Sha256: hash, Compressed: bytes,
		OriginalSize: len(bytes), CompressedSize: len(bytes),
	})
	if err != nil {
		t.Fatalf("insertTestPayload: %v", err)
	}
	return id
}

func TestListAuditSessions_FilterByWorkspace(t *testing.T) {
	d := newTestDB(t)
	wsA := "ws_list_a_" + uuid.NewString()[:8]
	wsB := "ws_list_b_" + uuid.NewString()[:8]

	now := time.Now().UTC().Truncate(time.Microsecond)
	older := AuditSession{
		ID: uuid.NewString(), WorkspaceID: wsA, ExeID: "exe_1", StreamID: "s1",
		OpenedAt: now.Add(-2 * time.Hour),
	}
	newer := AuditSession{
		ID: uuid.NewString(), WorkspaceID: wsA, ExeID: "exe_1", StreamID: "s2",
		OpenedAt: now.Add(-1 * time.Hour),
	}
	other := AuditSession{
		ID: uuid.NewString(), WorkspaceID: wsB, ExeID: "exe_1", StreamID: "s3",
		OpenedAt: now,
	}
	for _, s := range []AuditSession{older, newer, other} {
		s := s
		t.Cleanup(func() { d.Exec(`DELETE FROM exec_audit_sessions WHERE id=$1`, s.ID) })
		if err := d.UpsertAuditSession(s); err != nil {
			t.Fatal(err)
		}
	}

	got, err := d.ListAuditSessions(ListAuditSessionsFilter{WorkspaceID: wsA})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(got))
	}
	// Newest first.
	if got[0].ID != newer.ID || got[1].ID != older.ID {
		t.Errorf("order wrong: got [%s, %s], want [%s, %s]", got[0].ID, got[1].ID, newer.ID, older.ID)
	}
}

func TestListAuditSessions_MissingWorkspace(t *testing.T) {
	d := newTestDB(t)
	if _, err := d.ListAuditSessions(ListAuditSessionsFilter{}); err == nil {
		t.Fatal("expected error for empty workspace_id")
	}
}

func TestListAuditCalls_FilterByErrors(t *testing.T) {
	d := newTestDB(t)
	ws := "ws_calls_err_" + uuid.NewString()[:8]
	now := time.Now().UTC().Truncate(time.Microsecond)

	makeCall := func(isErr bool) string {
		id := uuid.NewString()
		t.Cleanup(func() { d.Exec(`DELETE FROM exec_audit_calls WHERE id=$1`, id) })
		if err := d.UpsertAuditCall(AuditCall{
			ID: id, WorkspaceID: ws, ExeID: "exe_c", Source: "rest",
			StartedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		if isErr {
			if _, err := d.Exec(`UPDATE exec_audit_calls SET is_error=true WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	errID := makeCall(true)
	_ = makeCall(false)
	_ = makeCall(false)

	tru := true
	got, err := d.ListAuditCalls(ListAuditCallsFilter{WorkspaceID: ws, IsError: &tru})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 error row, got %d", len(got))
	}
	if got[0].ID != errID {
		t.Errorf("got id %s, want %s", got[0].ID, errID)
	}
	if !got[0].IsError {
		t.Error("IsError should be true")
	}

	// is_error = false → 2 rows
	fls := false
	got, err = d.ListAuditCalls(ListAuditCallsFilter{WorkspaceID: ws, IsError: &fls})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 success rows, got %d", len(got))
	}

	// nil → all 3
	got, err = d.ListAuditCalls(ListAuditCallsFilter{WorkspaceID: ws})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(got))
	}
}

func TestListAuditCalls_InvalidSource(t *testing.T) {
	d := newTestDB(t)
	if _, err := d.ListAuditCalls(ListAuditCallsFilter{WorkspaceID: "ws", Source: "bogus"}); err == nil {
		t.Fatal("expected error for invalid source")
	}
}

func TestGetAuditSession_NotFound(t *testing.T) {
	d := newTestDB(t)
	_, err := d.GetAuditSession(uuid.NewString())
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestGetAuditSession_RoundTrip(t *testing.T) {
	d := newTestDB(t)
	id := uuid.NewString()
	opened := time.Now().UTC().Truncate(time.Microsecond)
	t.Cleanup(func() { d.Exec(`DELETE FROM exec_audit_sessions WHERE id=$1`, id) })

	in := AuditSession{
		ID: id, WorkspaceID: "ws_get", ExeID: "exe_g", StreamID: "sg",
		OpenedAt: opened,
	}
	if err := d.UpsertAuditSession(in); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetAuditSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.WorkspaceID != "ws_get" || got.ExeID != "exe_g" {
		t.Errorf("got %+v", got)
	}
	if !got.OpenedAt.Equal(opened) {
		t.Errorf("opened_at: got %v, want %v", got.OpenedAt, opened)
	}
}

func TestGetAuditCall_NotFound(t *testing.T) {
	d := newTestDB(t)
	_, err := d.GetAuditCall(uuid.NewString())
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestGetAuditPayload_RoundTripBytes(t *testing.T) {
	d := newTestDB(t)
	payload := make([]byte, 100)
	for i := range payload {
		payload[i] = byte(i)
	}
	id := insertTestPayload(t, d, payload)
	t.Cleanup(func() { d.Exec(`DELETE FROM exec_audit_payloads WHERE id=$1`, id) })

	got, err := d.GetAuditPayload(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id {
		t.Errorf("id: got %s, want %s", got.ID, id)
	}
	if len(got.Compressed) != len(payload) {
		t.Fatalf("bytes len: got %d, want %d", len(got.Compressed), len(payload))
	}
	for i := range payload {
		if got.Compressed[i] != payload[i] {
			t.Fatalf("byte %d mismatch: got %d, want %d", i, got.Compressed[i], payload[i])
		}
	}
	if got.OriginalSize != len(payload) {
		t.Errorf("original_size: got %d", got.OriginalSize)
	}
}

func TestGetAuditPayload_NotFound(t *testing.T) {
	d := newTestDB(t)
	_, err := d.GetAuditPayload(uuid.NewString())
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestPruneAuditOlderThan_CascadesAndSweepOrphans(t *testing.T) {
	d := newTestDB(t)
	ws := "ws_prune_" + uuid.NewString()[:8]

	// Time anchors:
	//   - oldT: 10 days ago (will be pruned)
	//   - newT: 1 hour ago  (will survive)
	//   - cutoff: 5 days ago (midway)
	now := time.Now().UTC().Truncate(time.Microsecond)
	oldT := now.Add(-10 * 24 * time.Hour)
	newT := now.Add(-1 * time.Hour)
	cutoff := now.Add(-5 * 24 * time.Hour)

	// Old payload (created_at backdated >>1 day so it qualifies for sweep).
	oldPayload := []byte("old payload content " + uuid.NewString())
	oldPID := insertTestPayload(t, d, oldPayload)
	if _, err := d.Exec(`UPDATE exec_audit_payloads SET created_at=$2 WHERE id=$1`, oldPID, oldT); err != nil {
		t.Fatal(err)
	}

	// New payload (created now; protected by 1-day grace).
	newPayload := []byte("new payload content " + uuid.NewString())
	newPID := insertTestPayload(t, d, newPayload)

	// Always-clean-up safety net in case the prune doesn't fire.
	t.Cleanup(func() {
		d.Exec(`DELETE FROM exec_audit_payloads WHERE id IN ($1, $2)`, oldPID, newPID)
	})

	// Old session + call referencing old payload (session cascade should
	// delete the call before payload sweep runs).
	oldSessID := uuid.NewString()
	oldCallID := uuid.NewString()
	t.Cleanup(func() {
		d.Exec(`DELETE FROM exec_audit_calls WHERE id=$1`, oldCallID)
		d.Exec(`DELETE FROM exec_audit_sessions WHERE id=$1`, oldSessID)
	})
	if err := d.UpsertAuditSession(AuditSession{
		ID: oldSessID, WorkspaceID: ws, ExeID: "exe_old", StreamID: "so",
		OpenedAt: oldT,
	}); err != nil {
		t.Fatal(err)
	}
	sessIDPtr := oldSessID
	if err := d.UpsertAuditCall(AuditCall{
		ID: oldCallID, SessionID: &sessIDPtr, WorkspaceID: ws, ExeID: "exe_old",
		Source: "envmcp", RequestPayloadID: &oldPID, RequestSize: len(oldPayload),
		StartedAt: oldT,
	}); err != nil {
		t.Fatal(err)
	}

	// New session + call referencing new payload (all survive).
	newSessID := uuid.NewString()
	newCallID := uuid.NewString()
	t.Cleanup(func() {
		d.Exec(`DELETE FROM exec_audit_calls WHERE id=$1`, newCallID)
		d.Exec(`DELETE FROM exec_audit_sessions WHERE id=$1`, newSessID)
	})
	if err := d.UpsertAuditSession(AuditSession{
		ID: newSessID, WorkspaceID: ws, ExeID: "exe_new", StreamID: "sn",
		OpenedAt: newT,
	}); err != nil {
		t.Fatal(err)
	}
	newSessPtr := newSessID
	if err := d.UpsertAuditCall(AuditCall{
		ID: newCallID, SessionID: &newSessPtr, WorkspaceID: ws, ExeID: "exe_new",
		Source: "envmcp", RequestPayloadID: &newPID, RequestSize: len(newPayload),
		StartedAt: newT,
	}); err != nil {
		t.Fatal(err)
	}

	sessions, calls, payloads, err := d.PruneAuditOlderThan(cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if sessions < 1 {
		t.Errorf("sessions: got %d, want >=1", sessions)
	}
	if payloads < 1 {
		t.Errorf("payloads: got %d, want >=1", payloads)
	}
	// calls counter only counts standalone (session-less) deletes; the
	// cascade is silent. Don't assert on it strictly.
	_ = calls

	// Old session gone.
	if _, err := d.GetAuditSession(oldSessID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("old session: got err=%v, want sql.ErrNoRows", err)
	}
	// Old call gone (cascade).
	if _, err := d.GetAuditCall(oldCallID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("old call: got err=%v, want sql.ErrNoRows", err)
	}
	// Old payload gone (orphan sweep).
	if _, err := d.GetAuditPayload(oldPID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("old payload: got err=%v, want sql.ErrNoRows", err)
	}

	// New session/call/payload all survive.
	if _, err := d.GetAuditSession(newSessID); err != nil {
		t.Errorf("new session: %v", err)
	}
	if _, err := d.GetAuditCall(newCallID); err != nil {
		t.Errorf("new call: %v", err)
	}
	if _, err := d.GetAuditPayload(newPID); err != nil {
		t.Errorf("new payload: %v", err)
	}
}
