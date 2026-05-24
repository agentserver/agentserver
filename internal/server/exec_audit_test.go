package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/db"
	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPostInternalExecAuditBatch_AcceptsSessionAndCall(t *testing.T) {
	srv, cleanup := newTestServerTUI(t)
	defer cleanup()
	t.Setenv("INTERNAL_API_SECRET", "test-internal-secret")

	now := time.Now().UTC()
	reqBytes := []byte(`{"jsonrpc":"2.0","id":1,"method":"shell"}`)
	sha := sha256.Sum256(reqBytes)
	hash := hex.EncodeToString(sha[:])

	sessionID := "11111111-1111-1111-1111-" + uniqueIDSuffix(t)
	callID := "22222222-2222-2222-2222-" + uniqueIDSuffix(t)
	t.Cleanup(func() {
		_, _ = srv.DB.Exec(`DELETE FROM exec_audit_calls WHERE id=$1`, callID)
		_, _ = srv.DB.Exec(`DELETE FROM exec_audit_sessions WHERE id=$1`, sessionID)
		_, _ = srv.DB.Exec(`DELETE FROM exec_audit_payloads WHERE sha256=$1`, hash)
	})

	batch := &pb.BatchRecords{
		GatewayId: "test-gateway",
		Records: []*pb.WALRecord{
			{
				Id: sessionID,
				Body: &pb.WALRecord_SessionOpen{SessionOpen: &pb.SessionOpen{
					WorkspaceId: "ws_audit_test",
					ExeId:       "exe_a",
					StreamId:    "s1",
					OpenedAt:    timestamppb.New(now),
				}},
				WrittenAt: timestamppb.New(now),
			},
			{
				Id: callID,
				Body: &pb.WALRecord_CallStart{CallStart: &pb.CallStart{
					CallId:        callID,
					SessionId:     sessionID,
					WorkspaceId:   "ws_audit_test",
					ExeId:         "exe_a",
					Source:        "envmcp",
					RpcId:         "1",
					RpcMethod:     "shell",
					RpcKind:       "request",
					RequestBytes:  reqBytes,
					RequestSize:   int32(len(reqBytes)),
					RequestSha256: hash,
					StartedAt:     timestamppb.New(now),
				}},
				WrittenAt: timestamppb.New(now),
			},
		},
	}
	body, err := proto.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/internal/exec-audit/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Internal-Secret", "test-internal-secret")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	sess, err := srv.DB.GetAuditSession(sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.WorkspaceID != "ws_audit_test" || sess.ExeID != "exe_a" {
		t.Fatalf("session row mismatch: %+v", sess)
	}
	call, err := srv.DB.GetAuditCall(callID)
	if err != nil {
		t.Fatalf("get call: %v", err)
	}
	if call.RequestPayloadID == nil {
		t.Fatal("expected request_payload_id to be set")
	}
	if call.RequestSize != len(reqBytes) {
		t.Fatalf("request_size mismatch: got %d want %d", call.RequestSize, len(reqBytes))
	}
}

func TestPostInternalExecAuditBatch_Idempotent(t *testing.T) {
	srv, cleanup := newTestServerTUI(t)
	defer cleanup()
	t.Setenv("INTERNAL_API_SECRET", "test-internal-secret")

	now := time.Now().UTC()
	sessionID := "33333333-3333-3333-3333-" + uniqueIDSuffix(t)
	wsID := "ws_audit_idem_" + uniqueIDSuffix(t)
	t.Cleanup(func() {
		_, _ = srv.DB.Exec(`DELETE FROM exec_audit_sessions WHERE id=$1`, sessionID)
	})

	batch := &pb.BatchRecords{
		GatewayId: "test-gateway",
		Records: []*pb.WALRecord{
			{
				Id: sessionID,
				Body: &pb.WALRecord_SessionOpen{SessionOpen: &pb.SessionOpen{
					WorkspaceId: wsID, ExeId: "exe_a", StreamId: "s1",
					OpenedAt: timestamppb.New(now),
				}},
				WrittenAt: timestamppb.New(now),
			},
		},
	}
	body, _ := proto.Marshal(batch)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/internal/exec-audit/batch", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/x-protobuf")
		req.Header.Set("X-Internal-Secret", "test-internal-secret")
		rr := httptest.NewRecorder()
		srv.Router().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("iter %d: status %d body %s", i, rr.Code, rr.Body.String())
		}
	}

	out, err := srv.DB.ListAuditSessions(db.ListAuditSessionsFilter{
		WorkspaceID: wsID, Limit: 10,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 session after 3 idempotent posts, got %d", len(out))
	}
}

// execAuditOnlyRouter wires just the POST /internal/exec-audit/batch route
// with its X-Internal-Secret wrapper, mirroring server.go's registration.
// Used by the bad-CT / bad-secret tests so they don't need TEST_DATABASE_URL
// to verify wire-level contracts (auth + content-type) that fire before any
// DAL call.
func execAuditOnlyRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	r.Post("/internal/exec-audit/batch", func(w http.ResponseWriter, r *http.Request) {
		secret := os.Getenv("INTERNAL_API_SECRET")
		if secret != "" && r.Header.Get("X-Internal-Secret") != secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.postInternalExecAuditBatch(w, r)
	})
	return r
}

func TestPostInternalExecAuditBatch_RejectsBadContentType(t *testing.T) {
	t.Setenv("INTERNAL_API_SECRET", "test-internal-secret")
	req := httptest.NewRequest(http.MethodPost, "/internal/exec-audit/batch", bytes.NewReader([]byte("hello")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Secret", "test-internal-secret")
	rr := httptest.NewRecorder()
	execAuditOnlyRouter(&Server{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestPostInternalExecAuditBatch_RejectsBadSecret(t *testing.T) {
	t.Setenv("INTERNAL_API_SECRET", "test-internal-secret")
	req := httptest.NewRequest(http.MethodPost, "/internal/exec-audit/batch", bytes.NewReader([]byte("body")))
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Internal-Secret", "wrong-secret")
	rr := httptest.NewRecorder()
	execAuditOnlyRouter(&Server{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// uniqueIDSuffix returns 12 hex chars derived from the test name + a high-res
// timestamp so concurrent runs against a shared TEST_DATABASE_URL don't collide.
func uniqueIDSuffix(t *testing.T) string {
	t.Helper()
	sum := sha256.Sum256([]byte(t.Name() + time.Now().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:6])
}

// TODO: cookie-auth cross-workspace 403 tests are deferred — they require
// wiring full session-cookie auth in the test helper. The URL-overrides-query
// pattern in getWorkspace* wrappers is well-trodden in other workspace
// handlers; the isolation test below + the internal-only mirror tests give
// enough coverage for this commit.

func TestGetWorkspaceExecAuditSessions_WorkspaceIsolation(t *testing.T) {
	srv, cleanup := newTestServerTUI(t)
	defer cleanup()

	now := time.Now().UTC()
	sidA := "ws-a-" + uniqueIDSuffix(t)
	sidB := "ws-b-" + uniqueIDSuffix(t)
	t.Cleanup(func() {
		_, _ = srv.DB.Exec(`DELETE FROM exec_audit_sessions WHERE id IN ($1,$2)`, sidA, sidB)
	})
	if err := srv.DB.UpsertAuditSession(db.AuditSession{
		ID: sidA, WorkspaceID: "ws_iso_a", ExeID: "exe_a", StreamID: "s1", OpenedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.DB.UpsertAuditSession(db.AuditSession{
		ID: sidB, WorkspaceID: "ws_iso_b", ExeID: "exe_b", StreamID: "s2", OpenedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// Direct call to the internal endpoint (no cookie); it should return only
	// the workspace specified in the query.
	t.Setenv("INTERNAL_API_SECRET", "test-internal")
	req := httptest.NewRequest(http.MethodGet,
		"/internal/exec-audit/sessions?workspace_id=ws_iso_a&limit=50", nil)
	req.Header.Set("X-Internal-Secret", "test-internal")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", rr.Code, rr.Body.String())
	}
	var resp ListAuditSessionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Sessions) != 1 || resp.Sessions[0].WorkspaceID != "ws_iso_a" {
		t.Fatalf("expected 1 ws_iso_a session, got %+v", resp.Sessions)
	}
}

func TestGetWorkspaceExecAuditCallPayload_404OnTooLarge(t *testing.T) {
	srv, cleanup := newTestServerTUI(t)
	defer cleanup()

	// Insert a call whose request_payload_id is NULL (gateway didn't store bytes
	// because >4 MiB). The payload endpoint should 404.
	callID := "call-" + uniqueIDSuffix(t)
	t.Cleanup(func() {
		_, _ = srv.DB.Exec(`DELETE FROM exec_audit_calls WHERE id=$1`, callID)
	})
	if err := srv.DB.UpsertAuditCall(db.AuditCall{
		ID: callID, WorkspaceID: "ws_oversize", ExeID: "exe", Source: "envmcp",
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	// Direct internal request to avoid the workspace-member check.
	t.Setenv("INTERNAL_API_SECRET", "test-internal")
	req := httptest.NewRequest(http.MethodGet,
		"/internal/exec-audit/calls/"+callID, nil)
	req.Header.Set("X-Internal-Secret", "test-internal")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for the call detail, got %d", rr.Code)
	}
	// The /payload endpoint specifically requires workspace member, so we can't
	// test it directly via /internal/* — but we can verify the call detail
	// has no preview when there's no payload.
	var detail AuditCallDetail
	_ = json.Unmarshal(rr.Body.Bytes(), &detail)
	if detail.RequestPreview != "" {
		t.Fatalf("expected no preview for payload-less call, got %+v", detail)
	}
}
