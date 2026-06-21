package ccappgateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/ccappgateway"
	"github.com/agentserver/agentserver/internal/ccappgateway/runner"
	"github.com/agentserver/agentserver/internal/ccappgateway/workspace"
)

// --- Shared test helpers for Task 7 tests ---

// fakeStore is a map-backed ObjectStore for tests.
type fakeStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newFakeStore() *fakeStore {
	return &fakeStore{data: make(map[string][]byte)}
}

func (s *fakeStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[key]
	if !ok {
		return nil, workspace.ErrObjectNotFound
	}
	return v, nil
}

func (s *fakeStore) Put(_ context.Context, key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = data
	return nil
}

func (s *fakeStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

// newTestServerWithStoreAndRunner creates a ccappgateway.Server backed by the given
// store and runner, with a mock wstoken/agentserver endpoint.
func newTestServerWithStoreAndRunner(t *testing.T, store workspace.ObjectStore, runFn ccappgateway.RunnerFunc) *ccappgateway.Server {
	t.Helper()
	wstokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":"deadbeef"}`)
	}))
	t.Cleanup(wstokenSrv.Close)

	cfg := ccappgateway.ServeConfig{
		DefaultModel:           "haiku",
		TurnTimeout:            30 * time.Second,
		TmpRoot:                t.TempDir(),
		AgentserverInternalURL: wstokenSrv.URL,
		InternalSecret:         "",
	}
	srv, err := ccappgateway.NewServerWithRunnerAndStore(cfg, runFn, store)
	if err != nil {
		t.Fatalf("NewServerWithRunnerAndStore: %v", err)
	}
	return srv
}

// postTurn fires a POST /api/turns request against the server's router and returns the recorder.
func postTurn(t *testing.T, srv *ccappgateway.Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/turns", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// InternalSecret is empty in test configs, so Either() passes with any value.
	req.Header.Set("X-Internal-Secret", "test")
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	return rr
}

// happyRunner returns a successful RunResult with assistantText="pong".
func happyRunner(ctx context.Context, in runner.RunInput) (*runner.RunResult, error) {
	return &runner.RunResult{
		AssistantText: "pong",
		Meta: &runner.ResultMeta{
			Subtype:      "success",
			IsError:      false,
			DurationMs:   100,
			TotalCostUSD: 0.0001,
			ModelUsage: map[string]runner.ModelUsage{
				"claude-haiku": {InputTokens: 10, OutputTokens: 5},
			},
		},
		DurationMs: 100,
		ExitCode:   0,
	}, nil
}

// newWstokenServer starts a httptest server simulating the agentserver workspace-token endpoint.
// statusCode controls what status it returns; for 200 it returns {"token":"deadbeef"}.
func newWstokenServer(statusCode int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if statusCode != http.StatusOK {
			w.WriteHeader(statusCode)
			fmt.Fprintf(w, `{"error":"mock error"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":"deadbeef"}`)
	}))
}

// buildHandler creates a Server pointing at the given wstoken server URL with a fake runner.
// Returns the server's Routes() as an http.Handler.
// Uses a noopStore (Get→ErrObjectNotFound) so existing Phase 1 tests see a fresh session.
// InternalSecret is empty → permissive; doPost sends "X-Internal-Secret: test" to satisfy Either().
func buildHandler(t *testing.T, wstokenURL string, runFn ccappgateway.RunnerFunc) http.Handler {
	t.Helper()
	cfg := ccappgateway.ServeConfig{
		DefaultModel:           "haiku",
		TurnTimeout:            30 * time.Second,
		TmpRoot:                t.TempDir(),
		AgentserverInternalURL: wstokenURL,
		InternalSecret:         "", // permissive — Either() accepts any X-Internal-Secret
	}
	srv, err := ccappgateway.NewServerWithRunner(cfg, runFn)
	if err != nil {
		t.Fatalf("buildHandler: NewServerWithRunner: %v", err)
	}
	return srv.Routes()
}

// validBody builds a valid JSON turn request body.
func validBody(extras map[string]interface{}) string {
	m := map[string]interface{}{
		"workspaceId": "ws-123",
		"sessionId":   "12345678-1234-1234-1234-123456789abc",
		"userMessage": "hello",
	}
	for k, v := range extras {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func doPost(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/turns", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// Send X-Internal-Secret so Either() dispatches to InternalSecretMiddleware;
	// buildHandler uses InternalSecret="" (permissive), so any value passes.
	req.Header.Set("X-Internal-Secret", "test")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// --- Test cases ---

// TC1: Happy path → 200 with expected CcTurnResponse shape.
func TestTurnHandler_HappyPath(t *testing.T) {
	srv := newWstokenServer(http.StatusOK)
	defer srv.Close()

	h := buildHandler(t, srv.URL, happyRunner)
	rr := doPost(t, h, validBody(nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}

	var resp ccappgateway.CcTurnResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.AssistantText != "pong" {
		t.Errorf("assistantText = %q, want %q", resp.AssistantText, "pong")
	}
	if resp.IsError {
		t.Errorf("isError = true, want false")
	}
	if resp.SessionID != "12345678-1234-1234-1234-123456789abc" {
		t.Errorf("sessionId = %q, want expected uuid", resp.SessionID)
	}
	if resp.ModelUsage == nil || resp.ModelUsage["claude-haiku"].InputTokens != 10 {
		t.Errorf("modelUsage missing or wrong: %+v", resp.ModelUsage)
	}
}

// TC2: Missing workspaceId → 400, code="validation".
func TestTurnHandler_MissingWorkspaceID(t *testing.T) {
	srv := newWstokenServer(http.StatusOK)
	defer srv.Close()

	h := buildHandler(t, srv.URL, happyRunner)
	body := `{"sessionId":"12345678-1234-1234-1234-123456789abc","userMessage":"hello"}`
	rr := doPost(t, h, body)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	var errResp map[string]string
	json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp["code"] != "validation" {
		t.Errorf("code = %q, want validation", errResp["code"])
	}
	if !strings.Contains(errResp["error"], "workspaceId") {
		t.Errorf("error should mention workspaceId: %s", errResp["error"])
	}
}

// TC3: Missing sessionId → 400, code="validation".
func TestTurnHandler_MissingSessionID(t *testing.T) {
	srv := newWstokenServer(http.StatusOK)
	defer srv.Close()

	h := buildHandler(t, srv.URL, happyRunner)
	body := `{"workspaceId":"ws-123","userMessage":"hello"}`
	rr := doPost(t, h, body)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	var errResp map[string]string
	json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp["code"] != "validation" {
		t.Errorf("code = %q, want validation", errResp["code"])
	}
}

// TC4: Invalid sessionId (not UUID) → 400, code="validation".
func TestTurnHandler_InvalidSessionID(t *testing.T) {
	srv := newWstokenServer(http.StatusOK)
	defer srv.Close()

	h := buildHandler(t, srv.URL, happyRunner)
	body := `{"workspaceId":"ws-123","sessionId":"not-a-uuid","userMessage":"hello"}`
	rr := doPost(t, h, body)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	var errResp map[string]string
	json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp["code"] != "validation" {
		t.Errorf("code = %q, want validation", errResp["code"])
	}
	if !strings.Contains(errResp["error"], "sessionId") {
		t.Errorf("error should mention sessionId: %s", errResp["error"])
	}
}

// TC5: Missing userMessage → 400, code="validation".
func TestTurnHandler_MissingUserMessage(t *testing.T) {
	srv := newWstokenServer(http.StatusOK)
	defer srv.Close()

	h := buildHandler(t, srv.URL, happyRunner)
	body := `{"workspaceId":"ws-123","sessionId":"12345678-1234-1234-1234-123456789abc"}`
	rr := doPost(t, h, body)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	var errResp map[string]string
	json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp["code"] != "validation" {
		t.Errorf("code = %q, want validation", errResp["code"])
	}
}

// TC6: userMessage > 100KB → 413, code="payload_too_large".
func TestTurnHandler_UserMessageTooLarge(t *testing.T) {
	srv := newWstokenServer(http.StatusOK)
	defer srv.Close()

	h := buildHandler(t, srv.URL, happyRunner)

	huge := strings.Repeat("a", 100*1024+1)
	body := validBody(map[string]interface{}{"userMessage": huge})
	rr := doPost(t, h, body)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rr.Code)
	}
	var errResp map[string]string
	json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp["code"] != "payload_too_large" {
		t.Errorf("code = %q, want payload_too_large", errResp["code"])
	}
}

// TC7: callbackUrl set → 501, code="not_implemented".
func TestTurnHandler_CallbackURLReturns501(t *testing.T) {
	srv := newWstokenServer(http.StatusOK)
	defer srv.Close()

	h := buildHandler(t, srv.URL, happyRunner)
	body := validBody(map[string]interface{}{"callbackUrl": "https://example.com/cb"})
	rr := doPost(t, h, body)

	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rr.Code)
	}
	var errResp map[string]string
	json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp["code"] != "not_implemented" {
		t.Errorf("code = %q, want not_implemented", errResp["code"])
	}
}

// TC8: wstoken endpoint returns 500 → 502, code="wstoken_failed".
func TestTurnHandler_WstokenFails502(t *testing.T) {
	srv := newWstokenServer(http.StatusInternalServerError)
	defer srv.Close()

	h := buildHandler(t, srv.URL, happyRunner)
	rr := doPost(t, h, validBody(nil))

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rr.Code)
	}
	var errResp map[string]string
	json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp["code"] != "wstoken_failed" {
		t.Errorf("code = %q, want wstoken_failed", errResp["code"])
	}
}

// TC9: Fake runner returns context.DeadlineExceeded → 504, code="runner_timeout".
func TestTurnHandler_RunnerTimeout504(t *testing.T) {
	srv := newWstokenServer(http.StatusOK)
	defer srv.Close()

	timeoutRunner := func(ctx context.Context, in runner.RunInput) (*runner.RunResult, error) {
		return nil, context.DeadlineExceeded
	}

	h := buildHandler(t, srv.URL, timeoutRunner)
	rr := doPost(t, h, validBody(nil))

	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d", rr.Code)
	}
	var errResp map[string]string
	json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp["code"] != "runner_timeout" {
		t.Errorf("code = %q, want runner_timeout", errResp["code"])
	}
}

// TC10: Fake runner returns generic error → 500, code="runner_failed".
func TestTurnHandler_RunnerFails500(t *testing.T) {
	srv := newWstokenServer(http.StatusOK)
	defer srv.Close()

	failRunner := func(ctx context.Context, in runner.RunInput) (*runner.RunResult, error) {
		return nil, fmt.Errorf("subprocess crashed unexpectedly")
	}

	h := buildHandler(t, srv.URL, failRunner)
	rr := doPost(t, h, validBody(nil))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
	var errResp map[string]string
	json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp["code"] != "runner_failed" {
		t.Errorf("code = %q, want runner_failed", errResp["code"])
	}
}

// TC11: Fake runner returns RunResult with Meta.IsError=true → 200 with isError=true.
func TestTurnHandler_LLMError_Returns200WithIsError(t *testing.T) {
	srv := newWstokenServer(http.StatusOK)
	defer srv.Close()

	errorRunner := func(ctx context.Context, in runner.RunInput) (*runner.RunResult, error) {
		return &runner.RunResult{
			AssistantText: "",
			Meta: &runner.ResultMeta{
				Subtype:      "error",
				IsError:      true,
				ErrorMessage: "upstream rejected",
				DurationMs:   50,
			},
			DurationMs: 50,
			ExitCode:   0,
		}, nil
	}

	h := buildHandler(t, srv.URL, errorRunner)
	rr := doPost(t, h, validBody(nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
	var resp ccappgateway.CcTurnResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if !resp.IsError {
		t.Errorf("isError = false, want true")
	}
}

// --- Task 7: S3 store + per-session mutex + SessionMode tests ---

func TestServeHTTP_ResumeOnPriorTarball(t *testing.T) {
	// Seed store with a prior tarball for this (workspace, session).
	store := newFakeStore()
	seedDir := t.TempDir()
	os.MkdirAll(filepath.Join(seedDir, "projects", "-tmp-x"), 0o700)
	os.WriteFile(filepath.Join(seedDir, "projects", "-tmp-x", "00000000-0000-0000-0000-000000000001.jsonl"), []byte("seed\n"), 0o600)
	workspace.TarUpload(context.Background(), store, "cc-app-gateway/ws_test/00000000-0000-0000-0000-000000000001.tar.gz", seedDir)

	// Fake runner captures the SessionMode it received.
	var gotMode string
	fakeRunner := func(_ context.Context, in runner.RunInput) (*runner.RunResult, error) {
		gotMode = in.SessionMode
		return &runner.RunResult{
			AssistantText: "ok",
			Meta:          &runner.ResultMeta{Subtype: "success"},
		}, nil
	}

	srv := newTestServerWithStoreAndRunner(t, store, fakeRunner)
	rr := postTurn(t, srv, `{"workspaceId":"ws_test","sessionId":"00000000-0000-0000-0000-000000000001","userMessage":"hi"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body: %s", rr.Code, rr.Body.String())
	}
	if gotMode != "resume" {
		t.Errorf("expected SessionMode=resume on prior tarball; got %q", gotMode)
	}
}

func TestServeHTTP_FreshSessionMode(t *testing.T) {
	store := newFakeStore() // empty
	var gotMode string
	fakeRunner := func(_ context.Context, in runner.RunInput) (*runner.RunResult, error) {
		gotMode = in.SessionMode
		return &runner.RunResult{AssistantText: "ok", Meta: &runner.ResultMeta{Subtype: "success"}}, nil
	}
	srv := newTestServerWithStoreAndRunner(t, store, fakeRunner)
	rr := postTurn(t, srv, `{"workspaceId":"ws_test","sessionId":"00000000-0000-0000-0000-000000000001","userMessage":"hi"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if gotMode != "fresh" {
		t.Errorf("expected SessionMode=fresh on empty store; got %q", gotMode)
	}
}

func TestServeHTTP_SameSessionSecondTurnBlocks(t *testing.T) {
	store := newFakeStore()
	// Slow runner so we can observe the second turn blocking on the mutex.
	runnerEntered := make(chan struct{})
	runnerRelease := make(chan struct{})
	fakeRunner := func(_ context.Context, _ runner.RunInput) (*runner.RunResult, error) {
		runnerEntered <- struct{}{}
		<-runnerRelease
		return &runner.RunResult{AssistantText: "ok", Meta: &runner.ResultMeta{Subtype: "success"}}, nil
	}
	srv := newTestServerWithStoreAndRunner(t, store, fakeRunner)

	// First call (in goroutine — will block on runnerRelease)
	body := `{"workspaceId":"ws_test","sessionId":"00000000-0000-0000-0000-000000000001","userMessage":"hi"}`
	go postTurn(t, srv, body)
	<-runnerEntered

	// Second call (should block on AcquireSessionLock because Teardown still holds mutex)
	secondReturned := make(chan struct{})
	go func() {
		postTurn(t, srv, body)
		close(secondReturned)
	}()
	select {
	case <-secondReturned:
		t.Error("second turn should not return while first turn's mutex is held")
	case <-time.After(100 * time.Millisecond):
	}

	// Release first runner; first will complete, Teardown will run and release mutex.
	close(runnerRelease)
	// Now second call can proceed — give the test the same release signal next time.
	// (For this test we let it complete naturally; the assertion is that it was blocked.)
}

// TestServeHTTP_S3GetFailsNonNotFound_Returns500 verifies that a non-ErrObjectNotFound
// error from S3 on Setup maps to 500 / workspace_setup_failed.
// (The test function name says "502" in the brief but the spec and body both say 500.)
func TestServeHTTP_S3GetFailsNonNotFound_Returns500(t *testing.T) {
	store := &errorStore{err: errors.New("network unreachable")}
	srv := newTestServerWithStoreAndRunner(t, store, nil) // runner won't be called
	rr := postTurn(t, srv, `{"workspaceId":"ws","sessionId":"00000000-0000-0000-0000-000000000001","userMessage":"hi"}`)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("S3 non-NotFound err should map to 500; got %d", rr.Code)
	}
	var resp struct {
		Code string `json:"code"`
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Code != "workspace_setup_failed" {
		t.Errorf("expected code=workspace_setup_failed; got %q", resp.Code)
	}
}
