package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/db"
	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
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

func TestPostInternalExecAuditBatch_RejectsBadContentType(t *testing.T) {
	srv, cleanup := newTestServerTUI(t)
	defer cleanup()
	t.Setenv("INTERNAL_API_SECRET", "test-internal-secret")
	req := httptest.NewRequest(http.MethodPost, "/internal/exec-audit/batch", bytes.NewReader([]byte("hello")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Secret", "test-internal-secret")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestPostInternalExecAuditBatch_RejectsBadSecret(t *testing.T) {
	srv, cleanup := newTestServerTUI(t)
	defer cleanup()
	t.Setenv("INTERNAL_API_SECRET", "test-internal-secret")
	req := httptest.NewRequest(http.MethodPost, "/internal/exec-audit/batch", bytes.NewReader([]byte("body")))
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Internal-Secret", "wrong-secret")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)
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
