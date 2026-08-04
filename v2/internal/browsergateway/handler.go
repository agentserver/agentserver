package browsergateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	aguitypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"
	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const (
	AGUIRoutePattern        = "POST /v2/workspaces/{workspaceId}/sessions/{sessionId}/agui"
	CancelRoutePattern      = "POST /v2/workspaces/{workspaceId}/runs/{runAction}"
	ApprovalRoutePattern    = "POST /v2/workspaces/{workspaceId}/approvals/{approvalAction}"
	EventCursorCustomName   = "agentserver.event_cursor"
	defaultMaxRequestBytes  = int64(1024 * 1024)
	defaultPollLimit        = 128
	defaultLongPollWait     = 15 * time.Second
	maxPromptBytes          = 256 * 1024
	maxApprovalRequestBytes = int64(16 * 1024)
)

var canonicalUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var canonicalSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type HandlerConfig struct {
	MaxRequestBytes int64
	PollLimit       int
	LongPollWait    time.Duration
	Now             func() time.Time
	Logger          *slog.Logger
}

func DefaultHandlerConfig() HandlerConfig {
	return HandlerConfig{
		MaxRequestBytes: defaultMaxRequestBytes,
		PollLimit:       defaultPollLimit,
		LongPollWait:    defaultLongPollWait,
		Now:             time.Now,
		Logger:          slog.Default(),
	}
}

type AGUIHandler struct {
	backend   RunBackend
	commands  RunCommandBackend
	approvals ApprovalCommandBackend
	config    HandlerConfig
	writer    *sse.SSEWriter
}

func NewAGUIHandler(backend RunBackend, config HandlerConfig) (*AGUIHandler, error) {
	if backend == nil {
		return nil, errors.New("run backend is required")
	}
	commands, ok := backend.(RunCommandBackend)
	if !ok {
		return nil, errors.New("explicit run command backend is required")
	}
	approvals, ok := backend.(ApprovalCommandBackend)
	if !ok {
		return nil, errors.New("explicit approval command backend is required")
	}
	if config.MaxRequestBytes <= 0 || config.MaxRequestBytes > 16*1024*1024 {
		return nil, errors.New("maximum AG-UI request bytes must be positive and at most 16 MiB")
	}
	if config.PollLimit < 1 || config.PollLimit > 1024 {
		return nil, errors.New("event poll limit must be between 1 and 1024")
	}
	if config.LongPollWait <= 0 || config.LongPollWait > time.Minute {
		return nil, errors.New("long-poll wait must be positive and at most one minute")
	}
	if config.Now == nil {
		return nil, errors.New("clock is required")
	}
	if config.Logger == nil {
		return nil, errors.New("logger is required")
	}
	return &AGUIHandler{
		backend: backend, commands: commands, approvals: approvals, config: config,
		writer: sse.NewSSEWriter().WithLogger(config.Logger),
	}, nil
}

func (handler *AGUIHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(AGUIRoutePattern, handler)
	mux.Handle(CancelRoutePattern, handler)
	mux.Handle(ApprovalRoutePattern, handler)
	return mux
}

func (handler *AGUIHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if approvalAction := request.PathValue("approvalAction"); approvalAction != "" {
		approvalID, ok := strings.CutSuffix(approvalAction, ":decide")
		if ok && approvalID != "" {
			handler.decideApproval(response, request, request.PathValue("workspaceId"), approvalID)
			return
		}
		writeHTTPError(response, http.StatusNotFound, "not_found", "approval command endpoint not found")
		return
	}
	if runAction := request.PathValue("runAction"); runAction != "" {
		runID, ok := strings.CutSuffix(runAction, ":cancel")
		if ok && runID != "" {
			handler.cancel(response, request, request.PathValue("workspaceId"), runID)
			return
		}
		writeHTTPError(response, http.StatusNotFound, "not_found", "run command endpoint not found")
		return
	}
	if _, ok := response.(http.Flusher); !ok {
		writeHTTPError(response, http.StatusInternalServerError, "streaming_unsupported", "HTTP response does not support streaming")
		return
	}
	workspaceID := request.PathValue("workspaceId")
	sessionID := request.PathValue("sessionId")
	if err := validateCanonicalUUID("workspaceId", workspaceID); err != nil {
		writeHTTPError(response, http.StatusBadRequest, "invalid_scope", err.Error())
		return
	}
	if err := validateCanonicalUUID("sessionId", sessionID); err != nil {
		writeHTTPError(response, http.StatusBadRequest, "invalid_scope", err.Error())
		return
	}
	bearer, err := extractBearer(request.Header)
	if err != nil {
		response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-browser-api"`)
		writeHTTPError(response, http.StatusUnauthorized, "unauthorized", "a single bearer token is required")
		return
	}
	idempotencyKey, err := extractIdempotencyKey(request.Header)
	if err != nil {
		writeHTTPError(response, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeHTTPError(response, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	input, err := decodeRunAgentInput(response, request, handler.config.MaxRequestBytes)
	if err != nil {
		writeHTTPError(response, http.StatusBadRequest, "invalid_agui_input", err.Error())
		return
	}
	prompt, resumeCursor, err := validateRunAgentInput(input, sessionID)
	if err != nil {
		writeHTTPError(response, http.StatusBadRequest, "invalid_agui_input", err.Error())
		return
	}

	started, err := handler.backend.StartRun(request.Context(), StartRunRequest{
		BearerToken:    bearer,
		WorkspaceID:    workspaceID,
		SessionID:      sessionID,
		IdempotencyKey: idempotencyKey,
		ClientRunID:    input.RunID,
		Prompt:         prompt,
		ResumeCursor:   resumeCursor,
	})
	if err != nil {
		handler.writeStartError(response, request, err)
		return
	}
	if err := validateStartResult(started, workspaceID, sessionID); err != nil {
		handler.config.Logger.ErrorContext(request.Context(), "browser-gateway received invalid StartRun result", "error", err)
		writeHTTPError(response, http.StatusBadGateway, "backend_contract_error", "run backend returned an invalid result")
		return
	}
	projector, err := NewProjector(ProjectionScope{
		WorkspaceID: workspaceID,
		SessionID:   sessionID,
		RunID:       started.RunID,
	}, started.LastEventSequence)
	if err != nil {
		handler.config.Logger.ErrorContext(request.Context(), "browser-gateway could not initialize projector", "error", err)
		writeHTTPError(response, http.StatusBadGateway, "backend_contract_error", "run backend returned an invalid projection cursor")
		return
	}

	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache, no-transform")
	response.Header().Set("X-Accel-Buffering", "no")
	response.WriteHeader(http.StatusOK)

	runStarted := events.NewRunStartedEvent(sessionID, started.RunID)
	runStarted.SetTimestamp(started.CreatedAt.UnixMilli())
	if err := handler.writer.WriteEvent(request.Context(), response, runStarted); err != nil {
		return
	}
	if started.RebaseSnapshot != nil {
		snapshot := events.NewStateSnapshotEvent(started.RebaseSnapshot)
		snapshot.SetTimestamp(handler.config.Now().UnixMilli())
		if err := handler.writer.WriteEvent(request.Context(), response, snapshot); err != nil {
			return
		}
	}
	if err := handler.writeCursorEvent(request.Context(), response, started.RunID, started.Cursor, started.LastEventSequence, started.CreatedAt); err != nil {
		return
	}
	handler.streamCommittedEvents(request.Context(), response, bearer, started, projector)
}

func (handler *AGUIHandler) decideApproval(response http.ResponseWriter, request *http.Request, workspaceID, approvalID string) {
	if request.Method != http.MethodPost || request.URL.RawQuery != "" {
		writeHTTPError(response, http.StatusBadRequest, "invalid_approval_request", "approval decision requires POST with no query parameters")
		return
	}
	if err := validateCanonicalUUID("workspaceId", workspaceID); err != nil {
		writeHTTPError(response, http.StatusBadRequest, "invalid_scope", err.Error())
		return
	}
	if err := validateCanonicalUUID("approvalId", approvalID); err != nil {
		writeHTTPError(response, http.StatusBadRequest, "invalid_scope", err.Error())
		return
	}
	bearer, err := extractBearer(request.Header)
	if err != nil {
		response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-browser-api"`)
		writeHTTPError(response, http.StatusUnauthorized, "unauthorized", "a single bearer token is required")
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeHTTPError(response, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	input, err := decodeApprovalDecisionInput(response, request)
	if err != nil {
		writeHTTPError(response, http.StatusBadRequest, "invalid_approval_request", err.Error())
		return
	}
	result, err := handler.approvals.DecideApproval(request.Context(), DecideApprovalRequest{
		BearerToken: bearer, WorkspaceID: workspaceID, ApprovalID: approvalID,
		Decision: input.Decision, Nonce: input.Nonce, ContextDigest: input.ContextDigest,
		ExpectedApprovalVersion: input.ExpectedApprovalVersion,
	})
	if err != nil {
		handler.writeCommandError(response, request, err)
		return
	}
	if err := validateCoreApprovalDecisionResult(result, DecideApprovalRequest{
		WorkspaceID: workspaceID, ApprovalID: approvalID, Decision: input.Decision,
		Nonce: input.Nonce, ContextDigest: input.ContextDigest,
		ExpectedApprovalVersion: input.ExpectedApprovalVersion,
	}); err != nil {
		handler.config.Logger.ErrorContext(request.Context(), "browser-gateway received invalid DecideApproval result", "error", err)
		writeHTTPError(response, http.StatusBadGateway, "backend_contract_error", "approval backend returned an invalid decision result")
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(response).Encode(result)
}

func (handler *AGUIHandler) cancel(response http.ResponseWriter, request *http.Request, workspaceID, runID string) {
	if request.Method != http.MethodPost || request.URL.RawQuery != "" || request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		writeHTTPError(response, http.StatusBadRequest, "invalid_cancel_request", "cancel requires POST with an empty body and no query parameters")
		return
	}
	if err := validateCanonicalUUID("workspaceId", workspaceID); err != nil {
		writeHTTPError(response, http.StatusBadRequest, "invalid_scope", err.Error())
		return
	}
	if err := validateCanonicalUUID("runId", runID); err != nil {
		writeHTTPError(response, http.StatusBadRequest, "invalid_scope", err.Error())
		return
	}
	bearer, err := extractBearer(request.Header)
	if err != nil {
		response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-browser-api"`)
		writeHTTPError(response, http.StatusUnauthorized, "unauthorized", "a single bearer token is required")
		return
	}
	result, err := handler.commands.CancelRun(request.Context(), CancelRunRequest{
		BearerToken: bearer, WorkspaceID: workspaceID, RunID: runID,
	})
	if err != nil {
		handler.writeCommandError(response, request, err)
		return
	}
	if err := validateCancelResult(result, workspaceID, runID); err != nil {
		handler.config.Logger.ErrorContext(request.Context(), "browser-gateway received invalid CancelRun result", "error", err)
		writeHTTPError(response, http.StatusBadGateway, "backend_contract_error", "run backend returned an invalid cancel result")
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(response).Encode(struct {
		WorkspaceID string `json:"workspaceId"`
		SessionID   string `json:"sessionId"`
		RunID       string `json:"runId"`
		Status      string `json:"status"`
		RunVersion  int64  `json:"runVersion"`
		Terminal    bool   `json:"terminal"`
		Changed     bool   `json:"changed"`
	}{
		WorkspaceID: result.WorkspaceID, SessionID: result.SessionID, RunID: result.RunID,
		Status: result.Status, RunVersion: result.RunVersion, Terminal: result.Terminal, Changed: result.Changed,
	})
}

func (handler *AGUIHandler) streamCommittedEvents(ctx context.Context, response http.ResponseWriter, bearer string, started StartRunResult, projector *Projector) {
	cursor := started.Cursor
	for {
		pollContext, cancel := context.WithTimeout(ctx, handler.config.LongPollWait)
		batch, err := handler.backend.ReadRunEvents(pollContext, ReadRunEventsRequest{
			BearerToken: bearer,
			WorkspaceID: started.WorkspaceID,
			SessionID:   started.SessionID,
			RunID:       started.RunID,
			After:       cursor,
			Limit:       handler.config.PollLimit,
			Wait:        handler.config.LongPollWait,
		})
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, context.DeadlineExceeded) {
				if writeHeartbeat(response) != nil {
					return
				}
				continue
			}
			var expired *CursorExpiredError
			if errors.As(err, &expired) {
				if err := handler.rebase(ctx, response, projector, expired); err != nil {
					handler.writeStreamError(ctx, response, started.RunID, "invalid_cursor_rebase", err)
					return
				}
				cursor = expired.RebaseCursor
				continue
			}
			handler.writeStreamError(ctx, response, started.RunID, "event_stream_unavailable", err)
			return
		}
		if err := validateEventBatch(batch, cursor, handler.config.PollLimit); err != nil {
			handler.writeStreamError(ctx, response, started.RunID, "invalid_event_batch", err)
			return
		}
		for index, canonical := range batch.Events {
			projection, err := projector.Project(canonical)
			if err != nil {
				handler.writeStreamError(ctx, response, started.RunID, "invalid_run_event_stream", err)
				return
			}
			for _, projected := range projection.Events {
				if err := handler.writer.WriteEvent(ctx, response, projected); err != nil {
					return
				}
			}
			if projection.Terminal {
				return
			}
			if projector.AtLifecycleBoundary() {
				if err := handler.writeCursorEvent(ctx, response, started.RunID, batch.EventCursors[index], canonical.Seq, canonical.CreatedAt); err != nil {
					return
				}
			}
		}
		cursor = batch.NextCursor
		if len(batch.Events) == 0 {
			if writeHeartbeat(response) != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
}

func (handler *AGUIHandler) rebase(ctx context.Context, response http.ResponseWriter, projector *Projector, expired *CursorExpiredError) error {
	if expired == nil || expired.Snapshot == nil {
		return errors.New("cursor rebase snapshot is required")
	}
	if err := validateCursor("rebase cursor", expired.RebaseCursor); err != nil {
		return err
	}
	if err := projector.Rebase(expired.LastEventSequence); err != nil {
		return err
	}
	snapshot := events.NewStateSnapshotEvent(expired.Snapshot)
	snapshot.SetTimestamp(handler.config.Now().UnixMilli())
	if err := handler.writer.WriteEvent(ctx, response, snapshot); err != nil {
		return err
	}
	return handler.writeCursorEvent(ctx, response, projector.scope.RunID, expired.RebaseCursor, expired.LastEventSequence, handler.config.Now())
}

func (handler *AGUIHandler) writeCursorEvent(ctx context.Context, response http.ResponseWriter, runID, cursor string, sequence int64, timestamp time.Time) error {
	if err := validateCursor("projected event cursor", cursor); err != nil {
		return err
	}
	if sequence < 0 || sequence >= 1<<53-1 {
		return errors.New("projected event cursor sequence is outside the JSON-safe range")
	}
	event := events.NewCustomEvent(EventCursorCustomName, events.WithValue(map[string]any{
		"version": 1, "runId": runID, "cursor": cursor, "lastEventSequence": sequence,
	}))
	event.SetTimestamp(timestamp.UnixMilli())
	return handler.writer.WriteEvent(ctx, response, event)
}

func (handler *AGUIHandler) writeStartError(response http.ResponseWriter, request *http.Request, err error) {
	var public *BackendHTTPError
	if errors.As(err, &public) && validBackendHTTPError(public) {
		if public.Status >= http.StatusInternalServerError {
			handler.config.Logger.ErrorContext(
				request.Context(),
				"browser-gateway StartRun backend failed",
				"workspace_id", request.PathValue("workspaceId"),
				"session_id", request.PathValue("sessionId"),
				"status", public.Status,
				"code", public.Code,
				"error", err,
			)
		}
		if public.Status == http.StatusUnauthorized {
			response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-browser-api"`)
		}
		writeHTTPError(response, public.Status, public.Code, public.Message, public.CurrentRunID)
		return
	}
	handler.config.Logger.ErrorContext(
		request.Context(),
		"browser-gateway StartRun failed",
		"workspace_id", request.PathValue("workspaceId"),
		"session_id", request.PathValue("sessionId"),
		"error", err,
	)
	writeHTTPError(response, http.StatusBadGateway, "run_backend_unavailable", "run backend is unavailable")
}

func (handler *AGUIHandler) writeCommandError(response http.ResponseWriter, request *http.Request, err error) {
	var public *BackendHTTPError
	if errors.As(err, &public) && validBackendHTTPError(public) {
		if public.Status == http.StatusUnauthorized {
			response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-browser-api"`)
		}
		writeHTTPError(response, public.Status, public.Code, public.Message, public.CurrentRunID)
		return
	}
	handler.config.Logger.ErrorContext(request.Context(), "browser-gateway run command failed", "error", err)
	writeHTTPError(response, http.StatusBadGateway, "run_backend_unavailable", "run backend is unavailable")
}

func validBackendHTTPError(public *BackendHTTPError) bool {
	if public == nil || public.Status < 400 || public.Status > 599 || len(public.Code) < 1 || len(public.Code) > 128 ||
		len(public.Message) < 1 || len(public.Message) > 1024 || !utf8.ValidString(public.Message) ||
		strings.ContainsAny(public.Message, "\x00\r\n") {
		return false
	}
	for _, character := range []byte(public.Code) {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return public.CurrentRunID == "" || validateCanonicalUUID("currentRunId", public.CurrentRunID) == nil
}

func (handler *AGUIHandler) writeStreamError(ctx context.Context, response http.ResponseWriter, runID, code string, cause error) {
	handler.config.Logger.ErrorContext(ctx, "browser-gateway stopped an AG-UI projection", "code", code, "run_id", runID, "error", cause)
	event := events.NewRunErrorEvent("run event projection stopped", events.WithErrorCode(code), events.WithRunID(runID))
	event.SetTimestamp(handler.config.Now().UnixMilli())
	_ = handler.writer.WriteEvent(ctx, response, event)
}

func decodeRunAgentInput(response http.ResponseWriter, request *http.Request, maximumBytes int64) (aguitypes.RunAgentInput, error) {
	request.Body = http.MaxBytesReader(response, request.Body, maximumBytes)
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		return aguitypes.RunAgentInput{}, fmt.Errorf("read AG-UI input: %w", err)
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxJSONValues = 32 * 1024
	limits.MaxJSONDepth = 64
	value, _, err := braincatalog.DecodeCanonicalJSON(raw, int(maximumBytes), limits)
	if err != nil {
		return aguitypes.RunAgentInput{}, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return aguitypes.RunAgentInput{}, errors.New("AG-UI input must be a JSON object")
	}
	if err := validateRunAgentInputKeys(object); err != nil {
		return aguitypes.RunAgentInput{}, err
	}
	var input aguitypes.RunAgentInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return aguitypes.RunAgentInput{}, fmt.Errorf("decode RunAgentInput: %w", err)
	}
	return input, nil
}

func decodeApprovalDecisionInput(response http.ResponseWriter, request *http.Request) (corecontract.DecideUserApprovalRequest, error) {
	request.Body = http.MaxBytesReader(response, request.Body, maxApprovalRequestBytes)
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		return corecontract.DecideUserApprovalRequest{}, fmt.Errorf("read approval decision: %w", err)
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxJSONValues = 32
	limits.MaxJSONDepth = 8
	value, _, err := braincatalog.DecodeCanonicalJSON(raw, int(maxApprovalRequestBytes), limits)
	if err != nil {
		return corecontract.DecideUserApprovalRequest{}, err
	}
	object, ok := value.(map[string]any)
	if !ok || !hasExactKeys(object, "decision", "nonce", "contextDigest", "expectedApprovalVersion") {
		return corecontract.DecideUserApprovalRequest{}, errors.New("approval decision must contain exactly decision, nonce, contextDigest, and expectedApprovalVersion")
	}
	digest, ok := object["contextDigest"].(map[string]any)
	if !ok || !hasExactKeys(digest, "domain", "canonicalizerVersion", "sha256") {
		return corecontract.DecideUserApprovalRequest{}, errors.New("contextDigest must contain exactly domain, canonicalizerVersion, and sha256")
	}
	var input corecontract.DecideUserApprovalRequest
	if err := json.Unmarshal(raw, &input); err != nil {
		return corecontract.DecideUserApprovalRequest{}, fmt.Errorf("decode approval decision: %w", err)
	}
	if err := validateApprovalDecisionInput(input); err != nil {
		return corecontract.DecideUserApprovalRequest{}, err
	}
	return input, nil
}

func validateApprovalDecisionInput(input corecontract.DecideUserApprovalRequest) error {
	if input.Decision != "approve" && input.Decision != "deny" {
		return errors.New("decision must be approve or deny")
	}
	if err := validateCanonicalUUID("nonce", input.Nonce); err != nil {
		return err
	}
	if input.ContextDigest.Domain != "approval-context" || input.ContextDigest.CanonicalizerVersion != "rfc8785-v1" ||
		!canonicalSHA256Pattern.MatchString(input.ContextDigest.SHA256) || input.ContextDigest.SHA256 == strings.Repeat("0", 64) {
		return errors.New("contextDigest must be a non-zero approval-context rfc8785-v1 SHA-256 digest")
	}
	if input.ExpectedApprovalVersion < 1 || input.ExpectedApprovalVersion >= 1<<53-1 {
		return errors.New("expectedApprovalVersion must be a positive JSON-safe integer")
	}
	return nil
}

func hasExactKeys(object map[string]any, keys ...string) bool {
	if len(object) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			return false
		}
	}
	return true
}

func validateRunAgentInputKeys(object map[string]any) error {
	aliases := [][]string{
		{"threadId", "thread_id"},
		{"runId", "run_id"},
		{"parentRunId", "parent_run_id"},
		{"state"}, {"messages"}, {"tools"}, {"context"},
		{"forwardedProps", "forwarded_props"},
		{"resume"},
	}
	allowed := make(map[string]struct{})
	for _, group := range aliases {
		present := 0
		for _, key := range group {
			allowed[key] = struct{}{}
			if _, exists := object[key]; exists {
				present++
			}
		}
		if present > 1 {
			return fmt.Errorf("AG-UI aliases %q must not be supplied together", group)
		}
	}
	for key := range object {
		if _, exists := allowed[key]; !exists {
			return fmt.Errorf("unknown RunAgentInput field %q", key)
		}
	}
	return nil
}

func validateRunAgentInput(input aguitypes.RunAgentInput, sessionID string) (string, string, error) {
	if input.ThreadID != "" && input.ThreadID != sessionID {
		return "", "", errors.New("threadId must be empty or match the sessionId path")
	}
	if input.ParentRunID != nil {
		return "", "", errors.New("parentRunId is not supported by this endpoint")
	}
	if len(input.Tools) != 0 {
		return "", "", errors.New("client-declared tools are forbidden; the server freezes the tool catalog")
	}
	if len(input.Context) != 0 {
		return "", "", errors.New("client-declared agent context is not supported")
	}
	if len(input.Resume) != 0 {
		return "", "", errors.New("AG-UI interrupt resume is not implemented in this phase")
	}
	if input.State != nil {
		return "", "", errors.New("client-declared state is not supported")
	}
	if len(input.Messages) != 1 {
		return "", "", errors.New("messages must contain exactly one new user message")
	}
	message := input.Messages[len(input.Messages)-1]
	if message.Role != aguitypes.RoleUser {
		return "", "", errors.New("the message must have role user")
	}
	prompt, ok := message.ContentString()
	if !ok {
		return "", "", errors.New("the user message must contain text in this phase")
	}
	if !utf8.ValidString(prompt) || strings.ContainsRune(prompt, '\x00') || prompt == "" || len(prompt) > maxPromptBytes {
		return "", "", fmt.Errorf("user prompt must contain between 1 and %d bytes of UTF-8 text without NUL", maxPromptBytes)
	}
	if input.RunID != "" && (len(input.RunID) > 256 || strings.ContainsAny(input.RunID, "\x00\r\n")) {
		return "", "", errors.New("client runId must be bounded text without NUL or line breaks")
	}
	resumeCursor, err := eventCursorFromForwardedProps(input.ForwardedProps)
	if err != nil {
		return "", "", err
	}
	return prompt, resumeCursor, nil
}

func eventCursorFromForwardedProps(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	root, ok := value.(map[string]any)
	if !ok {
		return "", errors.New("forwardedProps must be an object")
	}
	if len(root) == 0 {
		return "", nil
	}
	if len(root) != 1 {
		return "", errors.New("forwardedProps may contain only the agentserver extension")
	}
	extension, ok := root["agentserver"].(map[string]any)
	if !ok || len(extension) != 1 {
		return "", errors.New("forwardedProps.agentserver must contain exactly eventCursor")
	}
	cursor, ok := extension["eventCursor"].(string)
	if !ok {
		return "", errors.New("forwardedProps.agentserver.eventCursor must be a string")
	}
	if err := validateCursor("forwarded event cursor", cursor); err != nil {
		return "", err
	}
	return cursor, nil
}

func extractBearer(header http.Header) (string, error) {
	values := header.Values("Authorization")
	if len(values) != 1 || strings.Contains(values[0], ",") || !strings.HasPrefix(values[0], "Bearer ") {
		return "", errors.New("invalid authorization header")
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if token == "" || len(token) > 8192 || strings.ContainsAny(token, " \t\r\n\x00") {
		return "", errors.New("invalid bearer token")
	}
	return token, nil
}

func extractIdempotencyKey(header http.Header) (string, error) {
	values := header.Values("Idempotency-Key")
	if len(values) != 1 {
		return "", errors.New("a single Idempotency-Key header is required")
	}
	value := values[0]
	if len(value) == 0 || len(value) > 256 {
		return "", errors.New("Idempotency-Key must contain between 1 and 256 bytes")
	}
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e {
			return "", errors.New("Idempotency-Key must contain visible ASCII without spaces")
		}
	}
	return value, nil
}

func validateStartResult(result StartRunResult, workspaceID, sessionID string) error {
	if result.WorkspaceID != workspaceID || result.SessionID != sessionID {
		return errors.New("StartRun result escaped request scope")
	}
	if err := validateCanonicalUUID("runId", result.RunID); err != nil {
		return err
	}
	if result.CreatedAt.IsZero() {
		return errors.New("StartRun createdAt is required")
	}
	if result.LastEventSequence < 1 || result.LastEventSequence >= 1<<53-1 {
		return errors.New("StartRun last event sequence is outside the JSON-safe range")
	}
	return validateCursor("StartRun cursor", result.Cursor)
}

func validateCancelResult(result CancelRunResult, workspaceID, runID string) error {
	if result.WorkspaceID != workspaceID || result.RunID != runID {
		return errors.New("CancelRun result escaped request scope")
	}
	if err := validateCanonicalUUID("sessionId", result.SessionID); err != nil {
		return err
	}
	if result.RunVersion < 1 || result.RunVersion >= 1<<53-1 {
		return errors.New("CancelRun version is outside the JSON-safe range")
	}
	if !validCancelRunStatus(result.Status) || result.Terminal != terminalCancelRunStatus(result.Status) {
		return errors.New("CancelRun status and terminal flag disagree")
	}
	return nil
}

func validateEventBatch(batch ReadRunEventsResult, previousCursor string, maximumEvents int) error {
	if len(batch.Events) > maximumEvents {
		return fmt.Errorf("event backend returned %d events, limit is %d", len(batch.Events), maximumEvents)
	}
	if len(batch.EventCursors) != len(batch.Events) {
		return errors.New("event cursor count does not match the canonical event batch")
	}
	seen := map[string]struct{}{previousCursor: {}}
	for index, cursor := range batch.EventCursors {
		if err := validateCursor(fmt.Sprintf("event cursor %d", index), cursor); err != nil {
			return err
		}
		if _, duplicate := seen[cursor]; duplicate {
			return fmt.Errorf("event cursor %d did not advance exactly once", index)
		}
		seen[cursor] = struct{}{}
	}
	if err := validateCursor("next cursor", batch.NextCursor); err != nil {
		return err
	}
	if len(batch.Events) == 0 {
		if batch.NextCursor != previousCursor {
			return errors.New("empty event batch advanced the cursor")
		}
		return nil
	}
	if batch.NextCursor != batch.EventCursors[len(batch.EventCursors)-1] {
		return errors.New("next cursor does not match the final event cursor")
	}
	return nil
}

func validateCursor(label, cursor string) error {
	if cursor == "" || len(cursor) > 4096 || strings.ContainsAny(cursor, "\x00\r\n") {
		return fmt.Errorf("%s must be bounded opaque text without NUL or line breaks", label)
	}
	return nil
}

func validateCanonicalUUID(label, value string) error {
	if value == "00000000-0000-0000-0000-000000000000" || !canonicalUUIDPattern.MatchString(value) {
		return fmt.Errorf("%s must be a non-zero canonical lowercase UUID", label)
	}
	return nil
}

func writeHeartbeat(response http.ResponseWriter) error {
	if _, err := io.WriteString(response, ": heartbeat\n\n"); err != nil {
		return err
	}
	if flusher, ok := response.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func writeHTTPError(response http.ResponseWriter, status int, code, message string, currentRunID ...string) {
	var runID string
	if len(currentRunID) != 0 {
		runID = currentRunID[0]
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(struct {
		Code         string `json:"code"`
		Message      string `json:"message"`
		CurrentRunID string `json:"currentRunId,omitempty"`
	}{Code: code, Message: message, CurrentRunID: runID})
}
