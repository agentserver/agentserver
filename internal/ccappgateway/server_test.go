package ccappgateway_test

import (
	"context"
	"encoding/json"
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
