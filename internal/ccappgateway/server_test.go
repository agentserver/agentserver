package ccappgateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/ccappgateway"
	"github.com/agentserver/agentserver/internal/ccappgateway/runner"
	"github.com/agentserver/agentserver/internal/ccappgateway/workspace"
)

// buildTestCfg creates a minimal ServeConfig for server tests.
// agentserverURL is the URL of a mock agentserver healthz endpoint.
// claudeBin is the path to a fake or real claude binary (may not exist for negative tests).
func buildTestCfg(claudeBin, agentserverURL, internalSecret string) ccappgateway.ServeConfig {
	return ccappgateway.ServeConfig{
		ListenAddr:             "127.0.0.1:0",
		ClaudeBin:              claudeBin,
		InternalSecret:         internalSecret,
		AgentserverInternalURL: agentserverURL,
		LLMProxyURL:            "http://localhost:8081",
		DefaultModel:           "haiku",
		TurnTimeout:            30 * time.Second,
		TmpRoot:                os.TempDir(),
	}
}

// TC12: GET /healthz returns 200 "ok" without auth headers.
func TestServer_Healthz(t *testing.T) {
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer agentSrv.Close()

	cfg := buildTestCfg("/nonexistent/claude", agentSrv.URL, "")
	srv, err := ccappgateway.NewServerWithRunner(cfg, happyRunner)
	if err != nil {
		t.Fatalf("NewServerWithRunner: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ok") {
		t.Errorf("body = %q, want to contain 'ok'", rr.Body.String())
	}
}

// TC13: GET /readyz returns 503 when ClaudeBin path doesn't exist.
func TestServer_Readyz_NoBinary(t *testing.T) {
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer agentSrv.Close()

	cfg := buildTestCfg("/nonexistent/claude", agentSrv.URL, "")
	srv, err := ccappgateway.NewServerWithRunner(cfg, happyRunner)
	if err != nil {
		t.Fatalf("NewServerWithRunner: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d; body: %s", rr.Code, rr.Body.String())
	}

	var body map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&body)
	failures, _ := body["failures"].([]interface{})
	if len(failures) == 0 {
		t.Errorf("expected failures in body, got: %+v", body)
	}
	// Check that at least one failure mentions the binary
	found := false
	for _, f := range failures {
		if strings.Contains(fmt.Sprintf("%v", f), "claude") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected failure mentioning claude binary, got: %v", failures)
	}
}

// TC14: GET /readyz returns 503 when agentserver is unreachable.
func TestServer_Readyz_AgentserverUnreachable(t *testing.T) {
	// Start and immediately stop a server so we have an unreachable URL.
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	agentURL := agentSrv.URL
	agentSrv.Close() // closed before readyz check

	// Create a real fake claude binary for this test so only agentserver check fails.
	tmpBin, err := os.CreateTemp(t.TempDir(), "claude-fake-*")
	if err != nil {
		t.Fatalf("create temp bin: %v", err)
	}
	tmpBin.Close()
	os.Chmod(tmpBin.Name(), 0755)

	cfg := buildTestCfg(tmpBin.Name(), agentURL, "")
	srv, err := ccappgateway.NewServerWithRunner(cfg, happyRunner)
	if err != nil {
		t.Fatalf("NewServerWithRunner: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d; body: %s", rr.Code, rr.Body.String())
	}

	var body map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&body)
	failures, _ := body["failures"].([]interface{})
	if len(failures) == 0 {
		t.Errorf("expected failures in body, got: %+v", body)
	}
}

// TC15: GET /readyz returns 200 in happy case.
func TestServer_Readyz_Happy(t *testing.T) {
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer agentSrv.Close()

	// Create a real fake claude binary.
	tmpBin, err := os.CreateTemp(t.TempDir(), "claude-fake-*")
	if err != nil {
		t.Fatalf("create temp bin: %v", err)
	}
	tmpBin.Close()
	os.Chmod(tmpBin.Name(), 0755)

	cfg := buildTestCfg(tmpBin.Name(), agentSrv.URL, "")
	srv, err := ccappgateway.NewServerWithRunner(cfg, happyRunner)
	if err != nil {
		t.Fatalf("NewServerWithRunner: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
}

// TC16: POST /api/turns without X-Internal-Secret → 401.
func TestServer_TurnsRequiresAuth(t *testing.T) {
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer agentSrv.Close()

	cfg := buildTestCfg("/nonexistent/claude", agentSrv.URL, "supersecret")
	srv, err := ccappgateway.NewServerWithRunner(cfg, happyRunner)
	if err != nil {
		t.Fatalf("NewServerWithRunner: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/turns", strings.NewReader(validBody(nil)))
	req.Header.Set("Content-Type", "application/json")
	// No X-Internal-Secret header
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// TC17: POST /api/turns with correct X-Internal-Secret reaches handler.
func TestServer_TurnsWithAuth(t *testing.T) {
	// Use a mock wstoken server
	wstokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":"deadbeef"}`)
	}))
	defer wstokenSrv.Close()

	var called bool
	recordingRunner := func(ctx context.Context, in runner.RunInput) (*runner.RunResult, error) {
		called = true
		return happyRunner(ctx, in)
	}

	cfg := buildTestCfg("/nonexistent/claude", wstokenSrv.URL, "supersecret")
	cfg.AgentserverInternalURL = wstokenSrv.URL
	cfg.TmpRoot = t.TempDir()

	srv, err := ccappgateway.NewServerWithRunner(cfg, recordingRunner)
	if err != nil {
		t.Fatalf("NewServerWithRunner: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/turns", strings.NewReader(validBody(nil)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Secret", "supersecret")
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	if !called {
		t.Errorf("runner was not called")
	}
}

// TC18: Shutdown drains in-flight requests.
func TestServer_ShutdownDrainsInFlight(t *testing.T) {
	wstokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":"deadbeef"}`)
	}))
	defer wstokenSrv.Close()

	requestStarted := make(chan struct{})
	requestFinish := make(chan struct{})
	slowRunner := func(ctx context.Context, in runner.RunInput) (*runner.RunResult, error) {
		close(requestStarted)
		// Block until the test signals us to finish (or context cancels)
		select {
		case <-requestFinish:
		case <-ctx.Done():
		}
		return &runner.RunResult{
			AssistantText: "done",
			Meta: &runner.ResultMeta{
				Subtype:    "success",
				DurationMs: 100,
			},
			DurationMs: 100,
		}, nil
	}

	cfg := buildTestCfg("/nonexistent/claude", wstokenSrv.URL, "")
	cfg.AgentserverInternalURL = wstokenSrv.URL
	cfg.TmpRoot = t.TempDir()

	// Use httptest.NewUnstartedServer to get a server with a known address,
	// then start it ourselves with the server's router.
	srv, err := ccappgateway.NewServerWithRunner(cfg, slowRunner)
	if err != nil {
		t.Fatalf("NewServerWithRunner: %v", err)
	}

	// Start an httptest server with the router for reliable address binding.
	testHTTPSrv := httptest.NewServer(srv.Routes())
	defer testHTTPSrv.Close()
	addr := testHTTPSrv.Listener.Addr().String()

	// Send a slow request in the background.
	// We include X-Internal-Secret so Either() dispatches to InternalSecretMiddleware
	// (which is permissive when cfg.InternalSecret == "").
	var wg sync.WaitGroup
	wg.Add(1)
	var requestStatus int
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/api/turns", strings.NewReader(validBody(nil)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Secret", "any-value-permissive")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			requestStatus = -1
			return
		}
		resp.Body.Close()
		requestStatus = resp.StatusCode
	}()

	// Wait for the slow runner to start.
	select {
	case <-requestStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for slow runner to start")
	}

	// Close the httptest server (simulates shutdown initiation — stops accepting new connections,
	// then waits for in-flight to drain).
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		testHTTPSrv.Close()
	}()

	// Let the slow runner finish.
	close(requestFinish)

	// Wait for the request to complete.
	wg.Wait()

	// Wait for server close to complete.
	select {
	case <-shutdownDone:
	case <-time.After(5 * time.Second):
		t.Fatal("server close timed out")
	}

	// The in-flight request must have gotten a response (not connection reset).
	if requestStatus != http.StatusOK {
		t.Errorf("in-flight request status = %d, want 200", requestStatus)
	}
}

// errorStore is a fake workspace.ObjectStore whose Get always returns a non-NotFound error.
// Used by TestReadyz_S3Unreachable.
type errorStore struct {
	err error
}

func (e *errorStore) Get(_ context.Context, _ string) ([]byte, error) { return nil, e.err }
func (e *errorStore) Put(_ context.Context, _ string, _ []byte) error { return nil }
func (e *errorStore) Delete(_ context.Context, _ string) error        { return nil }

// newTestServer creates a Server with a noopStore and happyRunner, plus a mock agentserver.
// Returns the server and a cleanup func that closes the mock agentserver.
func newTestServer(t *testing.T) (*ccappgateway.Server, func()) {
	t.Helper()
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	cfg := buildTestCfg("/nonexistent/claude", agentSrv.URL, "")
	srv, err := ccappgateway.NewServerWithRunner(cfg, happyRunner)
	if err != nil {
		agentSrv.Close()
		t.Fatalf("NewServerWithRunner: %v", err)
	}
	return srv, agentSrv.Close
}

// newTestServerWithStore creates a Server with the provided store and happyRunner,
// plus a mock agentserver.
func newTestServerWithStore(t *testing.T, store workspace.ObjectStore) (*ccappgateway.Server, func()) {
	t.Helper()
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	cfg := buildTestCfg("/nonexistent/claude", agentSrv.URL, "")
	srv, err := ccappgateway.NewServerWithRunnerAndStore(cfg, happyRunner, store)
	if err != nil {
		agentSrv.Close()
		t.Fatalf("NewServerWithRunnerAndStore: %v", err)
	}
	return srv, agentSrv.Close
}

func TestAcquireSessionLock_SameKeySerializes(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	mu1 := srv.AcquireSessionLock("ws", "sid")
	released := make(chan struct{})
	go func() {
		// Second acquire blocks until mu1 is unlocked.
		mu2 := srv.AcquireSessionLock("ws", "sid")
		close(released)
		mu2.Unlock()
	}()
	// Confirm second acquire is blocked.
	select {
	case <-released:
		t.Error("second AcquireSessionLock for same key should block while first held")
	case <-time.After(50 * time.Millisecond):
	}
	mu1.Unlock()
	// Now second should release.
	select {
	case <-released:
	case <-time.After(1 * time.Second):
		t.Error("second AcquireSessionLock did not release after first was unlocked")
	}
}

func TestAcquireSessionLock_DifferentKeysConcurrent(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	mu1 := srv.AcquireSessionLock("ws_a", "sid_1")
	defer mu1.Unlock()
	// Different (workspace, session) → different lock → does not block.
	done := make(chan struct{})
	go func() {
		mu2 := srv.AcquireSessionLock("ws_b", "sid_2")
		close(done)
		mu2.Unlock()
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("different keys should not block each other")
	}
}

func TestShutdown_DrainsTeardownWG(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	srv.TeardownWG.Add(1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		srv.TeardownWG.Done()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	// If WG didn't drain, the goroutine above would still be running and srv.Shutdown
	// would have returned via ctx-deadline exceeded — but here we expect clean drain.
}

// --- Phase 3 NewServer validation tests ---

// buildPhase3Cfg returns a ServeConfig with all Phase 3 fields populated correctly.
func buildPhase3Cfg(claudeBin, agentserverURL string) ccappgateway.ServeConfig {
	cfg := buildTestCfg(claudeBin, agentserverURL, "")
	cfg.EnvMcpBinary = "/usr/local/bin/codex-app-gateway"
	cfg.ExecGatewayWSURL = "ws://exec-gw:8080"
	cfg.ExecGatewayInternalURL = "http://exec-gw-internal:9090"
	cfg.ExecGatewayInternalSecret = "secret"
	cfg.CapTokenHMACSecret = []byte("hmac-secret-32-bytes-long-enough!")
	cfg.CapTokenTTL = 2 * time.Hour // exceeds TurnTimeout=30s
	return cfg
}

func TestNewServer_Phase3_RejectsIfWSURLMissing(t *testing.T) {
	cfg := buildPhase3Cfg("/nonexistent/claude", "http://localhost:9999")
	cfg.ExecGatewayWSURL = ""
	_, err := ccappgateway.NewServerWithRunner(cfg, happyRunner)
	if err == nil {
		t.Fatal("expected error when ExecGatewayWSURL is empty, got nil")
	}
	if !strings.Contains(err.Error(), "CCAPPGW_EXEC_GATEWAY_WS_URL") {
		t.Errorf("error should mention CCAPPGW_EXEC_GATEWAY_WS_URL; got: %v", err)
	}
}

func TestNewServer_Phase3_RejectsIfHMACMissing(t *testing.T) {
	cfg := buildPhase3Cfg("/nonexistent/claude", "http://localhost:9999")
	cfg.CapTokenHMACSecret = nil
	_, err := ccappgateway.NewServerWithRunner(cfg, happyRunner)
	if err == nil {
		t.Fatal("expected error when CapTokenHMACSecret is empty, got nil")
	}
	if !strings.Contains(err.Error(), "CCAPPGW_CAPTOKEN_HMAC_SECRET") {
		t.Errorf("error should mention CCAPPGW_CAPTOKEN_HMAC_SECRET; got: %v", err)
	}
}

func TestNewServer_Phase3_RejectsIfTTLTooShort(t *testing.T) {
	cfg := buildPhase3Cfg("/nonexistent/claude", "http://localhost:9999")
	cfg.CapTokenTTL = 5 * time.Second // less than TurnTimeout=30s
	_, err := ccappgateway.NewServerWithRunner(cfg, happyRunner)
	if err == nil {
		t.Fatal("expected error when CapTokenTTL <= TurnTimeout, got nil")
	}
	if !strings.Contains(err.Error(), "CCAPPGW_CAPTOKEN_TTL") {
		t.Errorf("error should mention CCAPPGW_CAPTOKEN_TTL; got: %v", err)
	}
}

func TestNewServer_Phase3_Disabled_NoValidation(t *testing.T) {
	// When EnvMcpBinary is empty, Phase 3 fields are ignored.
	cfg := buildTestCfg("/nonexistent/claude", "http://localhost:9999", "")
	// All Phase 3 fields empty — should succeed.
	_, err := ccappgateway.NewServerWithRunner(cfg, happyRunner)
	if err != nil {
		t.Fatalf("expected no error when EnvMcpBinary is empty; got: %v", err)
	}
}

func TestReadyz_S3Unreachable(t *testing.T) {
	srv, cleanup := newTestServerWithStore(t, &errorStore{err: errors.New("s3 unreachable")})
	defer cleanup()
	// Existing readyz test asserts 200 when all checks pass; here we expect 503.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/readyz", nil)
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("readyz: got %d, want 503", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "s3") {
		t.Errorf("readyz body should mention s3; got %q", rr.Body.String())
	}
}
