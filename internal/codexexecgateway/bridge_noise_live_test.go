package codexexecgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"nhooyr.io/websocket"

	"github.com/agentserver/agentserver/internal/relaypb"
)

// TestLiveCodex_BridgeViaNoise is the full Phase 3.5 acceptance test:
//   - unmodified codex exec-server registers via noise mode
//   - a fake harness BridgeClient dials /bridge/{exe_id} with a real
//     cap token, sends a Resume + a Data frame carrying a JSON-RPC
//     initialize payload (NO length prefix — the env-mcp wire format)
//   - the bridge handler detects no legacy inbound, falls back to the
//     noise branch, drives OpenStreamForBridge, encrypts the payload,
//     ships it to codex over the noise relay
//   - codex's response comes back through the noise channel, gets
//     unwrapped from the noise framing by the router, wrapped in a
//     Data frame, sent to the harness ws
//   - the harness verifies the JSON-RPC sessionId
//
// This is the test that proves noise mode is wired correctly into the
// production /bridge handler. Gated by NOISE_LIVE_CODEX=1 and a real
// Postgres.
func TestLiveCodex_BridgeViaNoise(t *testing.T) {
	if os.Getenv("NOISE_LIVE_CODEX") != "1" {
		t.Skip("set NOISE_LIVE_CODEX=1 to run")
	}
	codexBin, err := exec.LookPath("codex")
	if err != nil {
		t.Skipf("codex binary not on PATH: %v", err)
	}

	const exeID = "noise-bridge-e2e-exe"
	const streamID = "noise-bridge-e2e-stream-1"

	store := newTestStore(t)
	clearNoiseRegistrations(t, store)

	// Pre-create the executor row + workspace binding so the bridge's
	// OwnsExecutor check passes. In production these are written when
	// the workspace operator adds the executor to a workspace.
	if err := store.CreateExecutor(context.Background(), Executor{
		ExeID: exeID, UserID: "u", RegisteredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateExecutor: %v", err)
	}
	if err := store.BindWorkspaceExecutor(context.Background(), "ws_1", exeID, "noise-bridge-e2e", "", false); err != nil {
		t.Fatalf("BindWorkspaceExecutor: %v", err)
	}

	srv, err := NewServer(Config{
		CapTokenHMACSecret:        []byte("test-hmac-secret"),
		InternalSharedSecret:      "internal-secret",
		AgentserverInternalSecret: "internal-secret",
		NoiseRelayHMACKey:         []byte("noise-hmac-key-32-bytes-xxxxxxxx"),
	}, store)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	httpSrv := httptest.NewServer(srv.Routes())
	defer httpSrv.Close()
	SetNoiseRouterDebug(testLogWriter{t})
	defer SetNoiseRouterDebug(discardWriter{})

	// Spawn codex with --environment-id == exeID so the env_id lookup
	// in handleBridge succeeds.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, codexBin,
		"exec-server",
		"--remote", httpSrv.URL,
		"--environment-id", exeID,
	)
	cmd.Env = append(os.Environ(), "CODEX_API_KEY=sk-bridge-noise-e2e")
	stdoutPath := t.TempDir() + "/codex.stdout"
	stderrPath := t.TempDir() + "/codex.stderr"
	stdout, _ := os.Create(stdoutPath)
	stderr, _ := os.Create(stderrPath)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn codex: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		stdout.Close()
		stderr.Close()
		if b, _ := os.ReadFile(stdoutPath); len(b) > 0 {
			t.Logf("codex stdout:\n%s", b)
		}
		if b, _ := os.ReadFile(stderrPath); len(b) > 0 {
			t.Logf("codex stderr:\n%s", b)
		}
	}()

	// Wait for codex to register + open its relay WS.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		reg, err := store.LookupNoiseExecutorRegistrationByEnv(context.Background(), exeID)
		if err == nil && srv.noiseHandlers.WSHub().ConnectionFor(reg.RegistrationID) != nil {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	// Mint a cap token for workspace ws_1 (matching the binding above).
	tok := mintBridgeToken(srv.config.CapTokenHMACSecret, CapPayload{
		WorkspaceID: "ws_1",
		UserID:      "u",
		TurnID:      "trn_noise_bridge",
		IAT:         time.Now().Unix(),
		EXP:         time.Now().Add(5 * time.Minute).Unix(),
	})

	bridgeConn, _, err := dialBridge(ctx, httpSrv.URL, exeID, tok)
	if err != nil {
		t.Fatalf("bridge dial: %v", err)
	}
	defer bridgeConn.Close(websocket.StatusNormalClosure, "test end")
	bridgeConn.SetReadLimit(-1)

	// Step 1: send Resume frame so the handler learns stream_id.
	resume := &relaypb.RelayMessageFrame{
		Version:  1,
		StreamId: streamID,
		Body:     &relaypb.RelayMessageFrame_Resume{Resume: &relaypb.RelayResume{NextSeq: 0}},
	}
	resumeBytes, _ := proto.Marshal(resume)
	if err := bridgeConn.Write(ctx, websocket.MessageBinary, resumeBytes); err != nil {
		t.Fatalf("write Resume: %v", err)
	}

	// Step 2: send Data frame carrying the JSON-RPC initialize (raw,
	// NO length prefix — bridge.go's noise branch adds it).
	reqJSON, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"clientName": "phase35-bridge-e2e"},
	})
	dataFrame := &relaypb.RelayMessageFrame{
		Version:  1,
		StreamId: streamID,
		Body: &relaypb.RelayMessageFrame_Data{
			Data: &relaypb.RelayData{Seq: 0, SegmentIndex: 0, SegmentCount: 1, Payload: reqJSON},
		},
	}
	dataBytes, _ := proto.Marshal(dataFrame)
	if err := bridgeConn.Write(ctx, websocket.MessageBinary, dataBytes); err != nil {
		t.Fatalf("write Data: %v", err)
	}

	// Step 3: read frames until we see a Data response.
	readCtx, readCancel := context.WithTimeout(ctx, 15*time.Second)
	defer readCancel()
	for {
		mt, payload, err := bridgeConn.Read(readCtx)
		if err != nil {
			t.Fatalf("bridge read: %v", err)
		}
		if mt != websocket.MessageBinary {
			continue
		}
		var resp relaypb.RelayMessageFrame
		if err := proto.Unmarshal(payload, &resp); err != nil {
			t.Logf("ignoring unparseable frame")
			continue
		}
		if resp.StreamId != streamID {
			continue
		}
		body, ok := resp.Body.(*relaypb.RelayMessageFrame_Data)
		if !ok {
			t.Logf("ignoring non-Data frame: %T", resp.Body)
			continue
		}
		t.Logf("response JSON: %s", body.Data.Payload)
		var parsed struct {
			ID     int `json:"id"`
			Result *struct {
				SessionID string `json:"sessionId"`
			} `json:"result,omitempty"`
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error,omitempty"`
		}
		if err := json.Unmarshal(body.Data.Payload, &parsed); err != nil {
			t.Fatalf("parse response: %v", err)
		}
		if parsed.Error != nil {
			t.Fatalf("codex error: code=%d msg=%s", parsed.Error.Code, parsed.Error.Message)
		}
		if parsed.Result == nil || parsed.Result.SessionID == "" {
			t.Fatalf("missing sessionId")
		}
		if parsed.ID != 1 {
			t.Errorf("response id = %d, want 1", parsed.ID)
		}
		return // success
	}
}

// silence imports flagged as unused on builds where the live test is
// skipped (Go skips imports for skipped tests anyway, but a build tag
// boundary would otherwise nag).
var _ = http.StatusOK
