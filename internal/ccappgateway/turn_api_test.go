package ccappgateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/ccappgateway"
	"github.com/agentserver/agentserver/internal/ccappgateway/runner"
)

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

// buildHandler creates a TurnHandler pointing at the given wstoken server URL with a fake runner.
func buildHandler(t *testing.T, wstokenURL string, runFn ccappgateway.RunnerFunc) *ccappgateway.TurnHandler {
	t.Helper()
	cfg := ccappgateway.ServeConfig{
		DefaultModel: "haiku",
		TurnTimeout:  30 * 1e9, // 30s
		TmpRoot:      t.TempDir(),
	}
	wstoken := ccappgateway.NewWSTokenClient(wstokenURL, "test-secret")
	return &ccappgateway.TurnHandler{
		Cfg:     cfg,
		WSToken: wstoken,
		Runner:  runFn,
		TmpRoot: cfg.TmpRoot,
	}
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
