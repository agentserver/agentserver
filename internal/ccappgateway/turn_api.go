package ccappgateway

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"time"

	"github.com/agentserver/agentserver/internal/ccappgateway/runner"
	"github.com/agentserver/agentserver/internal/ccappgateway/workspace"
)

// RunnerFunc abstracts runner.Run for testability. In production it's
// runner.Run; in tests it's a fake returning a canned RunResult.
type RunnerFunc func(ctx context.Context, in runner.RunInput) (*runner.RunResult, error)

// uuidRe matches any UUID format (not enforcing v4 specifically).
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// TurnHandler handles POST /api/turns.
type TurnHandler struct {
	Cfg     ServeConfig
	WSToken *WSTokenClient
	Runner  RunnerFunc
	TmpRoot string // from cfg.TmpRoot; injected so tests can override
}

// CcTurnRequest is the JSON body for POST /api/turns.
type CcTurnRequest struct {
	WorkspaceID string `json:"workspaceId"`
	SessionID   string `json:"sessionId"`
	UserMessage string `json:"userMessage"`
	Model       string `json:"model,omitempty"`
	TimeoutMs   int    `json:"timeoutMs,omitempty"`
	CallbackURL string `json:"callbackUrl,omitempty"` // Phase 4 only; Phase 1 returns 501
}

// CcTurnResponse is the JSON body returned on success.
type CcTurnResponse struct {
	SessionID     string                       `json:"sessionId"`
	AssistantText string                       `json:"assistantText"`
	IsError       bool                         `json:"isError"`
	DurationMs    int64                        `json:"durationMs"`
	TotalCostUSD  float64                      `json:"totalCostUsd"`
	ModelUsage    map[string]runner.ModelUsage `json:"modelUsage,omitempty"`
}

// errorResponse is the 4xx/5xx body shape.
type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errorResponse{Error: msg, Code: code}) //nolint:errcheck
}

// ServeHTTP implements http.Handler for POST /api/turns.
func (h *TurnHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Cap the raw request body at 1MB to prevent giant payload exhaustion.
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)

	var req CcTurnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	// Validate required fields.
	if req.WorkspaceID == "" {
		writeError(w, http.StatusBadRequest, "validation", "workspaceId required")
		return
	}
	if req.SessionID == "" {
		writeError(w, http.StatusBadRequest, "validation", "sessionId required")
		return
	}
	if !uuidRe.MatchString(req.SessionID) {
		writeError(w, http.StatusBadRequest, "validation", "sessionId must be a valid UUID")
		return
	}
	if req.UserMessage == "" {
		writeError(w, http.StatusBadRequest, "validation", "userMessage required")
		return
	}
	if len(req.UserMessage) > 100*1024 {
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "userMessage exceeds 100KB limit")
		return
	}

	// Phase 4 only: callback mode not implemented.
	if req.CallbackURL != "" {
		writeError(w, http.StatusNotImplemented, "not_implemented", "callback mode not implemented in phase 1")
		return
	}

	// Apply defaults.
	model := req.Model
	if model == "" {
		model = h.Cfg.DefaultModel
	}
	turnTimeout := h.Cfg.TurnTimeout
	if req.TimeoutMs > 0 {
		perTurn := time.Duration(req.TimeoutMs) * time.Millisecond
		if perTurn < turnTimeout {
			turnTimeout = perTurn
		}
	}

	// Fetch workspace token with 5s deadline.
	tokCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	wsToken, err := h.WSToken.GetOrCreate(tokCtx, req.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "wstoken_failed", err.Error())
		return
	}

	// Set up ephemeral workspace.
	ws, err := workspace.Setup(r.Context(), h.TmpRoot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace_setup_failed", err.Error())
		return
	}
	defer ws.Teardown() //nolint:errcheck

	// Run claude with per-turn timeout.
	runCtx, rcancel := context.WithTimeout(r.Context(), turnTimeout)
	defer rcancel()

	result, err := h.Runner(runCtx, runner.RunInput{
		ClaudeBin:   h.Cfg.ClaudeBin,
		ClaudeDir:   ws.ClaudeDir,
		ProjectDir:  ws.ProjectDir,
		SessionID:   req.SessionID,
		Model:       model,
		UserMessage: req.UserMessage,
		WSToken:     wsToken,
		LLMProxyURL: h.Cfg.LLMProxyURL,
		Timeout:     turnTimeout,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "runner_timeout", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "runner_failed", err.Error())
		return
	}

	// Build response. Anthropic-side errors (Meta.IsError=true) still return 200.
	resp := CcTurnResponse{
		SessionID:     req.SessionID,
		AssistantText: result.AssistantText,
		DurationMs:    result.DurationMs,
	}
	if result.Meta != nil {
		resp.IsError = result.Meta.IsError
		resp.TotalCostUSD = result.Meta.TotalCostUSD
		resp.ModelUsage = result.Meta.ModelUsage
	} else {
		log.Printf("[cc-app-gateway] warning: RunResult.Meta is nil for session %s", req.SessionID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}
