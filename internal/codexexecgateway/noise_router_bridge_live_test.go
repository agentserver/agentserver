package codexexecgateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/noise"
	"github.com/go-chi/chi/v5"
)

// TestLiveCodexNoiseStream_OpenStreamForBridge proves the bridge-style
// router API (OpenStreamForBridge) speaks the right framing to a real
// codex exec-server. The caller hands the router whole JSON-RPC
// payloads on a channel; the router does framing + chunking + crypto
// transparently. This is the API bridge.go will use to route harness
// traffic through the noise channel without env-mcp having to change.
func TestLiveCodexNoiseStream_OpenStreamForBridge(t *testing.T) {
	if os.Getenv("NOISE_LIVE_CODEX") != "1" {
		t.Skip("set NOISE_LIVE_CODEX=1 to run")
	}
	codexBin, err := exec.LookPath("codex")
	if err != nil {
		t.Skipf("codex binary not on PATH: %v", err)
	}

	store := newTestStore(t)
	clearNoiseRegistrations(t, store)
	gwIdentity, err := noise.GenerateIdentity()
	if err != nil {
		t.Fatalf("gw identity: %v", err)
	}
	hmacKey := mustRandKey(t, 32)
	handlers := NewNoiseHandlers(store, hmacKey, "")
	router := NewNoiseRouter(store, handlers.WSHub(), gwIdentity, hmacKey)
	handlers.AttachRouter(router)
	SetNoiseRouterDebug(testLogWriter{t})
	defer SetNoiseRouterDebug(discardWriter{})

	r := chi.NewRouter()
	handlers.Mount(r)
	srv := httptest.NewServer(r)
	defer srv.Close()

	const envID = "live-bridge-router-env"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, codexBin,
		"exec-server",
		"--remote", srv.URL,
		"--environment-id", envID,
	)
	cmd.Env = append(os.Environ(), "CODEX_API_KEY=sk-test-bridge")
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

	deadline := time.Now().Add(15 * time.Second)
	var reg NoiseExecutorRegistration
	for time.Now().Before(deadline) {
		reg, err = store.LookupNoiseExecutorRegistrationByEnv(context.Background(), envID)
		if err == nil && handlers.WSHub().ConnectionFor(reg.RegistrationID) != nil {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err != nil || handlers.WSHub().ConnectionFor(reg.RegistrationID) == nil {
		t.Fatalf("executor not ready (err=%v)", err)
	}

	streamID := "bridge-stream-1"
	harnessIn := make(chan []byte, 4)
	harnessOut := make(chan []byte, 4)

	streamCtx, streamCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer streamCancel()
	openErr := make(chan error, 1)
	go func() {
		err := router.OpenStreamForBridge(streamCtx, envID, streamID, harnessIn, harnessOut)
		t.Logf("OpenStreamForBridge returned: %v", err)
		openErr <- err
	}()

	// Send raw JSON-RPC initialize — no length prefix; router adds it.
	reqJSON, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"clientName": "phase35-bridge-test"},
	})
	harnessIn <- reqJSON

	select {
	case resp := <-harnessOut:
		t.Logf("codex JSON-RPC response: %s", resp)
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
		if err := json.Unmarshal(resp, &parsed); err != nil {
			t.Fatalf("parse response: %v\nbody=%s", err, resp)
		}
		if parsed.Error != nil {
			t.Fatalf("codex error: code=%d msg=%s", parsed.Error.Code, parsed.Error.Message)
		}
		if parsed.Result == nil || parsed.Result.SessionID == "" {
			t.Fatalf("missing sessionId: %s", resp)
		}
		if parsed.ID != 1 {
			t.Errorf("response id = %d", parsed.ID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for response on harnessOut")
	}

	// Tear down by closing harness input.
	close(harnessIn)
	select {
	case <-openErr:
	case <-time.After(3 * time.Second):
		t.Log("OpenStreamForBridge did not return after harness close")
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
