package codexexecgateway

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/noise"
	"github.com/go-chi/chi/v5"
)

// TestLiveCodexRegistersAgainstGateway spawns an unmodified
// `codex exec-server --remote http://<gateway>` and verifies that:
//  1. /cloud/environment/{env_id}/register accepts codex's JSON body
//     and inserts a noise_executor_registrations row;
//  2. /cloud/relay/{registration_id} accepts the WS upgrade so codex
//     enters its run_multiplexed_environment loop (i.e. registration
//     was honored as a real noise rendezvous, not a malformed one).
//
// Gated by NOISE_LIVE_CODEX=1 + TEST_DATABASE_URL set. Requires the
// codex binary on PATH (any 0.140+).
func TestLiveCodexRegistersAgainstGateway(t *testing.T) {
	if os.Getenv("NOISE_LIVE_CODEX") != "1" {
		t.Skip("set NOISE_LIVE_CODEX=1 to run live codex registration test")
	}
	codexBin, err := exec.LookPath("codex")
	if err != nil {
		t.Skipf("codex binary not on PATH: %v", err)
	}

	store := newTestStore(t)
	handlers := NewNoiseHandlers(store, []byte("live-test-hmac-key-32-bytes-aaaa"), "")
	r := chi.NewRouter()
	handlers.Mount(r)
	srv := httptest.NewServer(r)
	defer srv.Close()

	const envID = "live-cxg-env"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, codexBin,
		"exec-server",
		"--remote", srv.URL,
		"--environment-id", envID,
	)
	cmd.Env = append(os.Environ(),
		"CODEX_API_KEY=sk-test-cxg-live",
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

	// Poll the DB for a registration row to land.
	deadline := time.Now().Add(15 * time.Second)
	var reg NoiseExecutorRegistration
	for time.Now().Before(deadline) {
		reg, err = store.LookupNoiseExecutorRegistrationByEnv(context.Background(), envID)
		if err == nil {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("no registration row after deadline: %v", err)
	}
	if reg.PublicKey.Suite != noise.SuiteName {
		t.Errorf("registered suite = %q, want %q", reg.PublicKey.Suite, noise.SuiteName)
	}
	if !strings.HasPrefix(reg.RegistrationID, "exr_") {
		t.Errorf("registration_id shape = %q", reg.RegistrationID)
	}

	// Wait for the relay WS to actually be opened by codex — proves
	// the response URL was usable.
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if conn := handlers.wsHub.ConnectionFor(reg.RegistrationID); conn != nil {
			return // success: codex registered AND opened the relay WS
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("codex never opened relay WS for registration_id %s", reg.RegistrationID)
}
