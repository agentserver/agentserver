package coreserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

const (
	CreateUserRunRoutePattern     = "POST /v2/workspaces/{workspaceId}/sessions/{sessionId}/runs"
	CancelUserRunRoutePattern     = "POST /v2/workspaces/{workspaceId}/runs/{runAction}"
	ReadUserRunEventsRoutePattern = "GET /v2/workspaces/{workspaceId}/runs/{runId}/events"
	maxPublicRunRequestBytes      = int64(512 * 1024)
	defaultPublicEventLimit       = 128
	defaultPublicEventWait        = 15 * time.Second
)

var (
	ErrInvalidUserAccessToken = errors.New("invalid user access token")
	ErrUserAuthUnavailable    = errors.New("user authorization is unavailable")
)

type UserTokenAuthorizer interface {
	AuthorizeUser(*http.Request, string) (actorID string, err error)
}

type UserRunCommands interface {
	CreateUserRun(context.Context, CreateUserRunCommand) (corecontract.CreateUserRunResponse, error)
	CancelUserRun(context.Context, CancelUserRunCommand) (corecontract.CancelUserRunResponse, error)
	ReadUserRunEvents(context.Context, ReadUserRunEventsQuery) (corecontract.ReadUserRunEventsResponse, error)
}

type UserRunHandler struct {
	workload WorkloadAuthorizer
	users    UserTokenAuthorizer
	commands UserRunCommands
	logger   *slog.Logger
}

func NewUserRunHandler(workload WorkloadAuthorizer, users UserTokenAuthorizer, commands UserRunCommands) (*UserRunHandler, error) {
	if workload == nil || users == nil || commands == nil {
		return nil, errors.New("browser workload authorizer, user token authorizer, and user run commands are required")
	}
	return &UserRunHandler{workload: workload, users: users, commands: commands, logger: slog.Default()}, nil
}

func (handler *UserRunHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(CreateUserRunRoutePattern, handler)
	mux.Handle(CancelUserRunRoutePattern, handler)
	mux.Handle(ReadUserRunEventsRoutePattern, handler)
	return mux
}

func (handler *UserRunHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	workspaceID := request.PathValue("workspaceId")
	if request.Method == http.MethodPost && request.PathValue("sessionId") != "" && request.PathValue("runId") == "" {
		handler.create(response, request, workspaceID, request.PathValue("sessionId"))
		return
	}
	if request.Method == http.MethodPost && request.PathValue("runAction") != "" && request.PathValue("sessionId") == "" {
		runID, ok := strings.CutSuffix(request.PathValue("runAction"), ":cancel")
		if ok && runID != "" {
			handler.cancel(response, request, workspaceID, runID)
			return
		}
	}
	if request.Method == http.MethodGet && request.PathValue("runId") != "" && request.PathValue("sessionId") == "" {
		handler.readEvents(response, request, workspaceID, request.PathValue("runId"))
		return
	}
	writePublicRunError(response, http.StatusNotFound, "not_found", "user run endpoint not found", "")
}

func (handler *UserRunHandler) cancel(response http.ResponseWriter, request *http.Request, workspaceID, runID string) {
	actorID, ok := handler.authorize(response, request, "runs.cancel")
	if !ok {
		return
	}
	if request.URL.RawQuery != "" || request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "cancel requires an empty request without query parameters", "")
		return
	}
	result, err := handler.commands.CancelUserRun(request.Context(), CancelUserRunCommand{
		ActorID: actorID, WorkspaceID: workspaceID, RunID: runID,
	})
	if err != nil {
		handler.writeServiceError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *UserRunHandler) create(response http.ResponseWriter, request *http.Request, workspaceID, sessionID string) {
	actorID, ok := handler.authorize(response, request, "runs.create")
	if !ok {
		return
	}
	idempotencyKey, err := publicIdempotencyKey(request.Header)
	if err != nil {
		writePublicRunError(response, http.StatusBadRequest, "invalid_idempotency_key", err.Error(), "")
		return
	}
	var input corecontract.CreateUserRunRequest
	if !decodePublicRunJSON(response, request, &input) {
		return
	}
	result, err := handler.commands.CreateUserRun(request.Context(), CreateUserRunCommand{
		ActorID: actorID, WorkspaceID: workspaceID, SessionID: sessionID,
		IdempotencyKey: idempotencyKey, ClientRunID: input.ClientRunID, Prompt: input.Prompt,
		ExpectedPermissionModeVersion:   input.ExpectedPermissionModeVersion,
		ExpectedWorkingDirectoryVersion: input.ExpectedWorkingDirectoryVersion,
	})
	if err != nil {
		handler.writeServiceError(response, request, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(response, status, result)
}

func (handler *UserRunHandler) readEvents(response http.ResponseWriter, request *http.Request, workspaceID, runID string) {
	actorID, ok := handler.authorize(response, request, "runs.events.read")
	if !ok {
		return
	}
	after, limit, wait, err := publicEventQuery(request)
	if err != nil {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", err.Error(), "")
		return
	}
	result, err := handler.commands.ReadUserRunEvents(request.Context(), ReadUserRunEventsQuery{
		ActorID: actorID, WorkspaceID: workspaceID, RunID: runID, After: after, Limit: limit, Wait: wait,
	})
	if err != nil {
		handler.writeServiceError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *UserRunHandler) authorize(response http.ResponseWriter, request *http.Request, action string) (string, bool) {
	if err := handler.workload.AuthorizeWorkload(request, action); err != nil {
		writePublicRunError(response, http.StatusForbidden, "forbidden", "calling workload is not authorized", "")
		return "", false
	}
	actorID, err := handler.users.AuthorizeUser(request, action)
	if err == nil {
		return actorID, true
	}
	if errors.Is(err, ErrUserAuthUnavailable) {
		writePublicRunError(response, http.StatusServiceUnavailable, "authorization_unavailable", "user authorization is temporarily unavailable", "")
		return "", false
	}
	response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-browser-api"`)
	writePublicRunError(response, http.StatusUnauthorized, "unauthorized", "a valid agentserver-browser-api access token is required", "")
	return "", false
}

func (handler *UserRunHandler) writeServiceError(response http.ResponseWriter, request *http.Request, err error) {
	if request.Context().Err() != nil {
		return
	}
	var expired *UserRunCursorExpiredError
	if errors.As(err, &expired) {
		writeJSON(response, http.StatusGone, expired.Response)
		return
	}
	var stateError *coredb.StateError
	if !errors.As(err, &stateError) || stateError.Code == coredb.ErrorDatabase {
		logger := handler.logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.ErrorContext(
			request.Context(),
			"core user run request failed",
			"method", request.Method,
			"workspace_id", request.PathValue("workspaceId"),
			"session_id", request.PathValue("sessionId"),
			"run_id", request.PathValue("runId"),
			"error", err,
		)
		writePublicRunError(response, http.StatusInternalServerError, "internal_error", "core could not complete the user run request", "")
		return
	}
	status := http.StatusConflict
	switch stateError.Code {
	case coredb.ErrorInvalidArgument:
		status = http.StatusBadRequest
	case coredb.ErrorForbidden:
		status = http.StatusForbidden
	case coredb.ErrorNotFound:
		status = http.StatusNotFound
	}
	writePublicRunError(response, status, string(stateError.Code), stateError.Message, stateError.CurrentRunID)
}

func decodePublicRunJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writePublicRunError(response, http.StatusUnsupportedMediaType, "invalid_argument", "Content-Type must be application/json", "")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maxPublicRunRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "request body is not valid user run JSON", "")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "request body contains trailing data", "")
		return false
	}
	return true
}

func publicIdempotencyKey(header http.Header) (string, error) {
	values := header.Values("Idempotency-Key")
	if len(values) != 1 {
		return "", errors.New("a single Idempotency-Key header is required")
	}
	if err := validatePublicIdempotencyKey(values[0]); err != nil {
		return "", err
	}
	return values[0], nil
}

func publicEventQuery(request *http.Request) (string, int, time.Duration, error) {
	query := request.URL.Query()
	for key := range query {
		if key != "after" && key != "limit" && key != "waitMs" {
			return "", 0, 0, errors.New("event query contains an unknown parameter")
		}
		if len(query[key]) != 1 {
			return "", 0, 0, errors.New("event query parameters must be singular")
		}
	}
	afterValues, exists := query["after"]
	if !exists || len(afterValues) != 1 || afterValues[0] == "" {
		return "", 0, 0, errors.New("after cursor is required")
	}
	limit := defaultPublicEventLimit
	if value := query.Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || parsed > 1024 {
			return "", 0, 0, errors.New("limit must be between 0 and 1024")
		}
		limit = parsed
	}
	wait := defaultPublicEventWait
	if value := query.Get("waitMs"); value != "" {
		milliseconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil || milliseconds < 0 || milliseconds > 30_000 {
			return "", 0, 0, errors.New("waitMs must be between 0 and 30000")
		}
		wait = time.Duration(milliseconds) * time.Millisecond
	}
	return afterValues[0], limit, wait, nil
}

func writePublicRunError(response http.ResponseWriter, status int, code, message, currentRunID string) {
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, status, corecontract.PublicErrorResponse{
		Code: code, Message: strings.TrimSpace(message), CurrentRunID: currentRunID,
	})
}
