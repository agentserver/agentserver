package noise_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"nhooyr.io/websocket"

	"github.com/agentserver/agentserver/internal/codexexecgateway/noise"
	relayv1 "github.com/agentserver/agentserver/internal/codexexecgateway/noise/relayproto"
)

// TestLiveCodexHandshake spawns an unmodified `codex exec-server --remote`
// against a mock gateway built with our Go noise initiator. A successful
// handshake proves the Go impl is bit-compatible with codex's clatter
// (any divergent byte in the Noise wire would make the responder fail
// AEAD verification, so the test would never reach this point).
//
// Set `NOISE_LIVE_CODEX=1` to enable. Default off because the test
// requires the `codex` binary on PATH and spawns it as a subprocess.
func TestLiveCodexHandshake(t *testing.T) {
	if os.Getenv("NOISE_LIVE_CODEX") != "1" {
		t.Skip("set NOISE_LIVE_CODEX=1 to run live codex bit-compat test")
	}
	codexBin, err := exec.LookPath("codex")
	if err != nil {
		t.Skipf("codex binary not on PATH: %v", err)
	}

	const envID = "live-bitcompat-env"
	const streamID = "live-bitcompat-stream-1"

	srv := newMockGateway(t, envID, streamID)
	defer srv.shutdown()
	srv.start()

	// Spawn codex pointed at our mock gateway. API-key auth path requires
	// loopback base URL so we use httptest's 127.0.0.1.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, codexBin,
		"exec-server",
		"--remote", srv.baseURL,
		"--environment-id", envID,
	)
	cmd.Env = append(os.Environ(),
		"CODEX_API_KEY=sk-test-live-bitcompat",
		"RUST_LOG=codex_exec_server=trace,debug",
		"CODEX_LOG_TO_STDERR=1",
	)
	stdout, _ := os.Create(t.TempDir() + "/codex.stdout")
	stderr, _ := os.Create(t.TempDir() + "/codex.stderr")
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
		// always show on failure
		if b, _ := os.ReadFile(stdout.Name()); len(b) > 0 {
			t.Logf("codex stdout:\n%s", b)
		}
		if b, _ := os.ReadFile(stderr.Name()); len(b) > 0 {
			t.Logf("codex stderr:\n%s", b)
		}
	}()

	if err := srv.awaitHandshakeSuccess(20 * time.Second); err != nil {
		t.Fatalf("live handshake against codex failed: %v", err)
	}
}

type mockGateway struct {
	t                *testing.T
	envID, streamID  string
	registrationID   string
	httpsrv          *httptest.Server
	baseURL          string
	executorPubKey   atomic.Pointer[noise.PublicKey]
	gatewayIdentity  *noise.Identity
	handshakeSuccess chan error
	wsClosedOnce     sync.Once
}

func newMockGateway(t *testing.T, envID, streamID string) *mockGateway {
	id, err := noise.GenerateIdentity()
	if err != nil {
		t.Fatalf("generate gateway identity: %v", err)
	}
	g := &mockGateway{
		t:                t,
		envID:            envID,
		streamID:         streamID,
		registrationID:   "live-bitcompat-reg-1",
		gatewayIdentity:  id,
		handshakeSuccess: make(chan error, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/cloud/environment/%s/register", envID), g.handleRegister)
	mux.HandleFunc(fmt.Sprintf("/cloud/environment/%s/validate", envID), g.handleValidate)
	mux.HandleFunc(fmt.Sprintf("/relay/%s", g.registrationID), g.handleRelayWS)
	g.httpsrv = httptest.NewServer(mux)
	g.baseURL = g.httpsrv.URL
	return g
}

func (g *mockGateway) start() {}

func (g *mockGateway) shutdown() {
	g.httpsrv.Close()
}

func (g *mockGateway) awaitHandshakeSuccess(timeout time.Duration) error {
	select {
	case err := <-g.handshakeSuccess:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("timed out waiting for handshake completion")
	}
}

func (g *mockGateway) signalHandshake(err error) {
	g.wsClosedOnce.Do(func() {
		g.handshakeSuccess <- err
	})
}

func (g *mockGateway) handleRegister(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	g.t.Logf("register %s %s ct=%q cl=%q len=%d body=%s",
		r.Method, r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("Content-Length"), len(raw), raw)
	var req struct {
		SecurityProfile   string          `json:"security_profile"`
		ExecutorPublicKey noise.PublicKey `json:"executor_public_key"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		g.t.Logf("register decode err: %v", err)
		http.Error(w, "bad register body", http.StatusBadRequest)
		return
	}
	if req.SecurityProfile != "noise_hybrid_ik_v1" {
		http.Error(w, "wrong security_profile", http.StatusBadRequest)
		return
	}
	if req.ExecutorPublicKey.Suite != noise.SuiteName {
		http.Error(w, "wrong suite", http.StatusBadRequest)
		return
	}
	g.executorPubKey.Store(&req.ExecutorPublicKey)

	wsURL := "ws" + strings.TrimPrefix(g.baseURL, "http") + "/relay/" + g.registrationID
	resp := map[string]string{
		"environment_id":           g.envID,
		"url":                      wsURL,
		"security_profile":         "noise_hybrid_ik_v1",
		"executor_registration_id": g.registrationID,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (g *mockGateway) handleValidate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"valid": true})
}

func (g *mockGateway) handleRelayWS(w http.ResponseWriter, r *http.Request) {
	// codex's tungstenite client does not send an Origin header so the
	// default origin check passes; bound the read at the same 256 KiB
	// limit codex enforces (noise_relay_websocket_config upstream).
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		g.signalHandshake(fmt.Errorf("ws accept: %w", err))
		return
	}
	c.SetReadLimit(256 * 1024)
	defer c.Close(websocket.StatusInternalError, "test done")

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	if err := g.runHandshake(ctx, c); err != nil {
		g.signalHandshake(err)
		return
	}
	g.signalHandshake(nil)
}

// runHandshake initiates the noise hybrid IK from the gateway side,
// sends msg1 wrapped in a RelayHandshake frame, waits for codex's
// RelayHandshake response with msg2, and completes the handshake.
func (g *mockGateway) runHandshake(ctx context.Context, c *websocket.Conn) error {
	pkPtr := g.executorPubKey.Load()
	if pkPtr == nil {
		return errors.New("executor pubkey not learned from registration")
	}
	prologue := noise.Prologue(g.envID, g.registrationID, g.streamID)
	auth := []byte("live-test-harness-key-authorization")
	hs, msg1, err := noise.StartInitiator(g.gatewayIdentity, *pkPtr, prologue, auth)
	if err != nil {
		return fmt.Errorf("start initiator: %w", err)
	}

	// Wrap msg1 in RelayMessageFrame{handshake}
	frame := &relayv1.RelayMessageFrame{
		Version:  1,
		StreamId: g.streamID,
		Body: &relayv1.RelayMessageFrame_Handshake{
			Handshake: &relayv1.RelayHandshake{Payload: msg1},
		},
	}
	out, err := proto.Marshal(frame)
	if err != nil {
		return fmt.Errorf("marshal msg1 frame: %w", err)
	}
	if err := c.Write(ctx, websocket.MessageBinary, out); err != nil {
		return fmt.Errorf("write msg1: %w", err)
	}

	// Read frames until we see a Handshake or Reset for our stream
	for {
		_, payload, err := c.Read(ctx)
		if err != nil {
			return fmt.Errorf("read frame: %w", err)
		}
		var got relayv1.RelayMessageFrame
		if err := proto.Unmarshal(payload, &got); err != nil {
			return fmt.Errorf("unmarshal frame: %w", err)
		}
		if got.StreamId != g.streamID {
			continue
		}
		switch body := got.Body.(type) {
		case *relayv1.RelayMessageFrame_Handshake:
			if _, err := hs.Finish(body.Handshake.Payload); err != nil {
				return fmt.Errorf("initiator finish: %w", err)
			}
			return nil
		case *relayv1.RelayMessageFrame_Reset_:
			return fmt.Errorf("codex sent RelayReset: %s", body.Reset_.Reason)
		default:
			// ignore acks/heartbeats
		}
	}
}
