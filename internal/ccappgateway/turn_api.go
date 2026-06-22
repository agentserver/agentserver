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

// workspaceIDRe matches valid workspaceID format: alphanumeric + underscore + dash, 1-64 chars.
// Rejects "..", "/", "\", and other path-traversal characters that would escape S3 prefix
// or filesystem tmpdir scoping in workspace.Setup.
var workspaceIDRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// TurnHandler handles POST /api/turns.
type TurnHandler struct {
	Cfg     ServeConfig
	WSToken *WSTokenClient
	Runner  RunnerFunc
	TmpRoot string // from cfg.TmpRoot; injected so tests can override

	// Store and Server are wired by newServerInternal (Task 6) and used by
	// Task 7 to implement per-session mutex + S3 persistence in ServeHTTP.
	Store  workspace.ObjectStore
	Server *Server
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
	if !workspaceIDRe.MatchString(req.WorkspaceID) {
		writeError(w, http.StatusBadRequest, "validation", "workspaceId must match ^[a-zA-Z0-9_-]{1,64}$")
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
		// Log full error server-side; return generic message to caller.
		// Even though /api/turns is X-Internal-Secret-authenticated, internal
		// errors (upstream HTTP status codes, agentserver URLs, etc.) shouldn't
		// echo back to the HTTP body — caller has the `code` field for branching.
		log.Printf("[cc-app-gateway] wstoken_failed (session=%s workspace=%s): %v", req.SessionID, req.WorkspaceID, err)
		writeError(w, http.StatusBadGateway, "wstoken_failed", "upstream agentserver failure")
		return
	}

	// Acquire per-session mutex to serialize turns for the same (workspace, session)
	// within this pod. Released by the Teardown goroutine after S3 Put completes
	// (or after Teardown errors). See spec § Concurrency.
	mu := h.Server.AcquireSessionLock(req.WorkspaceID, req.SessionID)
	mutexReleased := false
	defer func() {
		if !mutexReleased {
			// Only fires if Setup itself failed (Teardown never ran → goroutine never started).
			mu.Unlock()
		}
	}()

	// Set up ephemeral workspace: download prior tarball from S3 if one exists.
	ws, err := workspace.Setup(r.Context(), h.TmpRoot, req.WorkspaceID, req.SessionID, h.Store)
	if err != nil {
		log.Printf("[cc-app-gateway] workspace_setup_failed (session=%s): %v", req.SessionID, err)
		writeError(w, http.StatusInternalServerError, "workspace_setup_failed", "workspace setup failed")
		return
	}

	// Background Teardown — uploads ClaudeDir to S3 and releases the mutex AFTER
	// the upload completes. Uses context.Background() (not r.Context()) so the
	// upload is not cancelled when the HTTP response is written.
	defer func() {
		h.Server.TeardownWG.Add(1)
		mutexReleased = true // tell the outer defer not to unlock again
		go func() {
			defer h.Server.TeardownWG.Done()
			defer mu.Unlock()
			bctx, bcancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer bcancel()
			if err := ws.Teardown(bctx, h.Store); err != nil {
				log.Printf("[cc-app-gateway] workspace teardown failed (session=%s): %v", req.SessionID, err)
			}
		}()
	}()

	// Determine session mode based on whether a prior tarball was found.
	sessionMode := "fresh"
	if ws.IsResume {
		sessionMode = "resume"
	}

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
		SessionMode: sessionMode,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			log.Printf("[cc-app-gateway] runner_timeout (session=%s after=%v): %v", req.SessionID, turnTimeout, err)
			writeError(w, http.StatusGatewayTimeout, "runner_timeout", "turn exceeded timeout")
			return
		}
		log.Printf("[cc-app-gateway] runner_failed (session=%s): %v", req.SessionID, err)
		writeError(w, http.StatusInternalServerError, "runner_failed", "runner execution failed")
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
