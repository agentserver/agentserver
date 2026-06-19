package codexexecgateway

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/noise"
	"github.com/go-chi/chi/v5"
)

type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(string(bytes.TrimRight(p, "\n")))
	return len(p), nil
}

// TestLiveCodexNoiseStream_JSONRPCInitialize is the Phase 3
// end-to-end gate: real codex exec-server speaks JSON-RPC through
// the actual encrypted noise relay our gateway terminates.
//
// Flow:
//  1. Stand up the gateway (NoiseHandlers + NoiseRouter) on httptest.
//  2. Spawn `codex exec-server --remote http://gw`.
//  3. Codex POSTs /register → our handlers persist the registration,
//     codex opens the relay WS → NoiseRouter.ServeExecutorFrames takes
//     over the read side.
//  4. We call NoiseRouter.OpenStream(envID, harness) — the harness is
//     an in-process pipe. Router runs full hybrid IK against codex,
//     gets back a Transport.
//  5. Write one JSON-RPC initialize request through the harness pipe.
//     Router encrypts as RelayData, ships to codex. Codex decrypts,
//     dispatches to its INITIALIZE handler, encrypts the JSON-RPC
//     response, ships it back. Router decrypts, writes plaintext to
//     the harness reader.
//  6. Assert the response contains a non-empty sessionId.
//
// Gated by NOISE_LIVE_CODEX=1 + TEST_DATABASE_URL set.
func TestLiveCodexNoiseStream_JSONRPCInitialize(t *testing.T) {
	if os.Getenv("NOISE_LIVE_CODEX") != "1" {
		t.Skip("set NOISE_LIVE_CODEX=1 to run live codex noise router test")
	}
	codexBin, err := exec.LookPath("codex")
	if err != nil {
		t.Skipf("codex binary not on PATH: %v", err)
	}

	store := newTestStore(t)
	gwIdentity, err := noise.GenerateIdentity()
	if err != nil {
		t.Fatalf("gw identity: %v", err)
	}
	hmacKey := mustRandKey(t, 32)
	handlers := NewNoiseHandlers(store, hmacKey, "")
	router := NewNoiseRouter(store, handlers.WSHub(), gwIdentity, hmacKey)
	handlers.AttachRouter(router)

	SetNoiseRouterDebug(testLogWriter{t})
	defer SetNoiseRouterDebug(io.Discard)

	r := chi.NewRouter()
	handlers.Mount(r)
	srv := httptest.NewServer(r)
	defer srv.Close()

	const envID = "live-noise-router-env"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, codexBin,
		"exec-server",
		"--remote", srv.URL,
		"--environment-id", envID,
	)
	cmd.Env = append(os.Environ(),
		"CODEX_API_KEY=sk-test-noise-router",
		"RUST_LOG=codex_exec_server=debug,info",
	)
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

	// Wait for the registration row to land AND the relay WS to be
	// up. Both are required before OpenStream can succeed.
	deadline := time.Now().Add(15 * time.Second)
	var reg NoiseExecutorRegistration
	for time.Now().Before(deadline) {
		reg, err = store.LookupNoiseExecutorRegistrationByEnv(context.Background(), envID)
		if err == nil && handlers.WSHub().ConnectionFor(reg.RegistrationID) != nil {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("registration never landed: %v", err)
	}
	if handlers.WSHub().ConnectionFor(reg.RegistrationID) == nil {
		t.Fatalf("executor WS never connected for reg %s", reg.RegistrationID)
	}

	// Open the noise stream. Harness side is an in-process pipe.
	harnessRouter, harnessApp := newDuplexPipe()
	streamCtx, streamCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer streamCancel()
	openErr := make(chan error, 1)
	go func() {
		err := router.OpenStream(streamCtx, envID, harnessRouter)
		t.Logf("OpenStream returned: %v", err)
		openErr <- err
	}()

	// Send JSON-RPC initialize. codex's noise relay JSON-RPC framing
	// (codex-rs/exec-server/src/noise_relay/message_framing.rs) wraps
	// each message with a 4-byte big-endian length prefix; emit the
	// same shape so codex's JsonRpcMessageDecoder accepts it.
	reqJSON, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"clientName": "phase3-live-test"},
	})
	framed := make([]byte, 4+len(reqJSON))
	binary.BigEndian.PutUint32(framed[:4], uint32(len(reqJSON)))
	copy(framed[4:], reqJSON)
	if _, err := harnessApp.Write(framed); err != nil {
		t.Fatalf("harness write: %v", err)
	}

	// Read the response. Codex sends ONE RelayData per JSON-RPC
	// message, which the router decrypts into one Write to the
	// harness side — so a single Read should yield the full payload.
	// Read the framed response: 4-byte BE length + JSON body.
	prefixBuf := make([]byte, 4)
	readDone := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(harnessApp, prefixBuf)
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("read length prefix: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for response length prefix")
	}
	respLen := binary.BigEndian.Uint32(prefixBuf)
	if respLen == 0 || respLen > 64*1024 {
		t.Fatalf("implausible response length %d", respLen)
	}
	resp := make([]byte, respLen)
	if _, err := io.ReadFull(harnessApp, resp); err != nil {
		t.Fatalf("read response body: %v", err)
	}
	t.Logf("codex JSON-RPC response: %s", resp)

	var parsed struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  *struct {
			SessionID string `json:"sessionId"`
		} `json:"result,omitempty"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(resp, &parsed); err != nil {
		t.Fatalf("parse response JSON: %v\nbody=%s", err, resp)
	}
	if parsed.Error != nil {
		t.Fatalf("codex returned JSON-RPC error: code=%d msg=%s", parsed.Error.Code, parsed.Error.Message)
	}
	if parsed.Result == nil || parsed.Result.SessionID == "" {
		t.Fatalf("missing sessionId in response: %s", resp)
	}
	// codex 0.141 omits the "jsonrpc" field on success responses;
	// the result object + matching id is enough to confirm.
	if parsed.ID != 1 {
		t.Errorf("response id = %d, want 1", parsed.ID)
	}

	// Cleanly tear down.
	_ = harnessApp.Close()
	_ = harnessRouter.Close()
	select {
	case <-openErr:
	case <-time.After(3 * time.Second):
		t.Logf("OpenStream still running after harness close")
	}
}
