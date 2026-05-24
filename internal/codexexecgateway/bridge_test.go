package codexexecgateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/handlers"
	"nhooyr.io/websocket"
)

// dialBridge dials the /bridge endpoint with the cap-token in the
// Authorization: Bearer header (matching the env-mcp child's wire shape).
func dialBridge(ctx context.Context, baseURL, exeID, tok string) (*websocket.Conn, *http.Response, error) {
	wsURL := "ws" + baseURL[len("http"):] + "/bridge/" + exeID
	return websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + tok}},
	})
}

func mintBridgeToken(secret []byte, p CapPayload) string {
	header := []byte(`{"alg":"HS256","typ":"CXG"}`)
	pj, _ := json.Marshal(p)
	enc := base64.RawURLEncoding
	si := enc.EncodeToString(header) + "." + enc.EncodeToString(pj)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(si))
	return si + "." + enc.EncodeToString(mac.Sum(nil))
}

// connectInbound registers an executor (db row + workspace binding to "ws_1"
// so bridge ownership checks pass), dials the inbound endpoint with an HMAC
// ticket, and waits until the registry shows a live conn for exeID.
func connectInbound(t *testing.T, srv *Server, baseURL, exeID string) *websocket.Conn {
	t.Helper()
	srv.store.CreateExecutor(context.Background(), Executor{
		ExeID: exeID, UserID: "u", RegisteredAt: time.Now().UTC(),
	})
	if err := srv.store.BindWorkspaceExecutor(context.Background(), "ws_1", exeID, "test-"+exeID, "", false); err != nil {
		t.Fatalf("BindWorkspaceExecutor: %v", err)
	}
	ticket, err := handlers.MintWSTicket(exeID, srv.config.AgentserverInternalSecret)
	if err != nil {
		t.Fatalf("MintWSTicket: %v", err)
	}
	url := "ws" + baseURL[len("http"):] + "/codex-exec/" + exeID + "?token=" + ticket
	c, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		t.Fatalf("inbound dial: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := srv.registry.Lookup(exeID); ok {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("inbound not registered for %s", exeID)
	return nil
}

// newBridgeNoDBServer returns a test server with no store — only routes that
// don't touch the DB can be exercised. Used for bridge auth-rejection tests
// that fail before any store lookup, allowing them to run without TEST_DATABASE_URL.
func newBridgeNoDBServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	cfg := Config{CapTokenHMACSecret: []byte("k"), InternalSharedSecret: "s"}
	srv, err := newServerNoStoreForTesting(cfg)
	if err != nil {
		t.Fatalf("newServerNoStoreForTesting: %v", err)
	}
	hs := httptest.NewServer(srv.Routes())
	t.Cleanup(hs.Close)
	return hs, srv
}

// TODO(test-analyzer-#4): wiring test that bridge handleBridge invokes
// s.recorder.SessionOpen. Deferred from this PR because the test
// requires the full DB-backed setup (newInboundTestServer needs
// TEST_DATABASE_URL — see multiplex_e2e_test.go pattern). The audit
// fail-closed path IS covered indirectly by TestRealRecorder_
// SessionOpenErrorsOnFailModeFullDisk, which pins the contract the
// bridge depends on. A future PR should add bridge_audit_test.go that
// swaps in a capRelayRec via srv.recorder = ... and asserts SessionOpen
// fires (mirroring TestHandleRelay_RecorderObservesPutGet in
// handlers_relay_test.go).

func TestBridge_Rejects401OnBadToken(t *testing.T) {
	hs, _ := newBridgeNoDBServer(t)
	_, resp, err := dialBridge(context.Background(), hs.URL, "exe_x", "garbage")
	if err == nil {
		t.Fatal("dial should fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %v", resp)
	}
}

func TestBridge_Rejects403WhenExeIDNotInWorkspace(t *testing.T) {
	// DB-backed: token's workspace_id has no binding to the URL's exe_id
	// → /bridge returns 403 via the workspace_executors ownership check.
	hs, srv := newInboundTestServer(t)
	srv.store.CreateExecutor(context.Background(), Executor{
		ExeID: "exe_target", UserID: "u", RegisteredAt: time.Now().UTC(),
	})
	// Intentionally no BindWorkspaceExecutor — ws_1 does not own exe_target.
	now := time.Now().Unix()
	tok := mintBridgeToken(srv.config.CapTokenHMACSecret, CapPayload{
		TurnID: "trn_1", WorkspaceID: "ws_1",
		IAT: now, EXP: now + 60,
	})
	_, resp, err := dialBridge(context.Background(), hs.URL, "exe_target", tok)
	if err == nil {
		t.Fatal("dial should fail")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got %v", resp)
	}
}

func TestBridge_Rejects503WhenExecutorOffline(t *testing.T) {
	hs, srv := newBridgeNoDBServer(t)
	now := time.Now().Unix()
	tok := mintBridgeToken(srv.config.CapTokenHMACSecret, CapPayload{
		TurnID: "trn_1", WorkspaceID: "ws_1",
		IAT: now, EXP: now + 60,
	})
	// exe_offline is not in the registry → 503
	_, resp, err := dialBridge(context.Background(), hs.URL, "exe_offline", tok)
	if err == nil {
		t.Fatal("dial should fail")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %v", resp)
	}
}

func TestBridge_RejectsRevokedTurn(t *testing.T) {
	hs, srv := newBridgeNoDBServer(t)
	// Register a fake inbound so the revocation check is reached.
	srv.registry.Register("exe_rev", newInboundConn("exe_rev", nil, nil))
	now := time.Now().Unix()
	srv.revoked.Add("trn_revoked", now+60)
	tok := mintBridgeToken(srv.config.CapTokenHMACSecret, CapPayload{
		TurnID: "trn_revoked", WorkspaceID: "ws_1",
		IAT: now, EXP: now + 60,
	})
	_, resp, err := dialBridge(context.Background(), hs.URL, "exe_rev", tok)
	if err == nil {
		t.Fatal("dial should fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %v", resp)
	}
}

// 409 Conflict is gone in v0.53.0: bridges multiplex by stream_id on
// one inbound, no longer serialised by an exe-wide mutex. The old
// TestBridge_Returns409WhenAnotherSessionActive test is removed;
// multi-bridge concurrency is covered by
// TestBridge_TwoConcurrentBridgesShareInbound below.

// v0.53.0: the bridge handler is no longer a transparent text-frame
// proxy. It parses incoming frames as RelayMessageFrame protobuf, the
// first frame must be Resume, and forwarding is gated on stream_id
// matching the session's. The tests below (PairsAndForwards,
// CloseFromBridge/Inbound, E2EByteFidelity) were written against the
// old transparent-forwarding model and have been removed. Coverage of
// the new behavior lives in:
//   - TestBridge_RejectsFirstFrameNonResume
//   - TestBridge_TwoConcurrentBridgesShareInbound (in multiplex_e2e_test.go)
//   - TestBridge_StreamIdCollisionEvictsFirst (in multiplex_e2e_test.go)

