package coreserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/agentserver/agentserver/v2/internal/trajectorycursor"
)

const maximumUserSessionRequestBytes = int64(64 * 1024)

type UserSessionCommands interface {
	ListSessions(context.Context, string, string) (corecontract.ListUserSessionsResponse, error)
	GetSession(context.Context, string, string, string) (corecontract.UserSessionState, error)
	GetTranscript(context.Context, string, string, string) (corecontract.GetUserSessionTranscriptResponse, error)
	GetTrajectory(context.Context, string, string, string, string, int) (corecontract.GetUserSessionTrajectoryResponse, error)
	CreateSession(context.Context, string, string, corecontract.CreateUserSessionRequest) (corecontract.CreateUserSessionResponse, error)
	UpdateSession(context.Context, string, string, string, corecontract.UpdateUserSessionRequest) (corecontract.UpdateUserSessionResponse, error)
	ArchiveSession(context.Context, string, string, string, corecontract.ArchiveUserSessionRequest) (corecontract.ArchiveUserSessionResponse, error)
}

// UserSessionPermissionModeCommands is kept separate from the historical
// session command interface so embedders that only implement title/archive
// operations remain source-compatible while the new route is rolled out.
type UserSessionPermissionModeCommands interface {
	UpdatePermissionMode(context.Context, string, string, string, corecontract.UpdateUserSessionPermissionModeRequest) (corecontract.UpdateUserSessionPermissionModeResponse, error)
}

type UserSessionHandler struct {
	workload WorkloadAuthorizer
	users    UserTokenAuthorizer
	commands UserSessionCommands
}

func NewUserSessionHandler(workload WorkloadAuthorizer, users UserTokenAuthorizer, commands UserSessionCommands) (*UserSessionHandler, error) {
	if workload == nil || users == nil || commands == nil {
		return nil, errors.New("browser workload, user authorizer, and session commands are required")
	}
	return &UserSessionHandler{workload: workload, users: users, commands: commands}, nil
}

func (handler *UserSessionHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(corecontract.UserSessionCollectionRoutePattern, handler.collection)
	mux.HandleFunc(corecontract.UserSessionResourceRoutePattern, handler.resource)
	mux.HandleFunc(corecontract.UserSessionPermissionModeRoutePattern, handler.permissionMode)
	mux.HandleFunc(corecontract.UserSessionTranscriptRoutePattern, handler.transcript)
	mux.HandleFunc(corecontract.UserSessionTrajectoryRoutePattern, handler.trajectory)
	mux.HandleFunc(corecontract.UserSessionArchiveRoutePattern, handler.archive)
	return mux
}

func (handler *UserSessionHandler) permissionMode(response http.ResponseWriter, request *http.Request) {
	userSessionNoStore(response)
	if request.Method != http.MethodPatch {
		response.Header().Set("Allow", http.MethodPatch)
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "session permission mode requires PATCH", "")
		return
	}
	if request.URL.RawQuery != "" {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "session permission mode does not accept query parameters", "")
		return
	}
	commands, ok := handler.commands.(UserSessionPermissionModeCommands)
	if !ok {
		writePublicRunError(response, http.StatusServiceUnavailable, "unavailable", "session permission mode is unavailable", "")
		return
	}
	actorID, ok := handler.authorize(response, request, "sessions.update")
	if !ok {
		return
	}
	var input corecontract.UpdateUserSessionPermissionModeRequest
	if !decodeUserSessionJSON(response, request, &input) {
		return
	}
	result, err := commands.UpdatePermissionMode(request.Context(), request.PathValue("workspaceId"), request.PathValue("sessionId"), actorID, input)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *UserSessionHandler) transcript(response http.ResponseWriter, request *http.Request) {
	userSessionNoStore(response)
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "session transcript requires GET", "")
		return
	}
	if request.URL.RawQuery != "" {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "session transcript does not accept query parameters", "")
		return
	}
	actorID, ok := handler.authorize(response, request, "sessions.transcript")
	if !ok || !requireEmptyUserSessionBody(response, request, "session transcript") {
		return
	}
	result, err := handler.commands.GetTranscript(
		request.Context(), request.PathValue("workspaceId"), request.PathValue("sessionId"), actorID,
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *UserSessionHandler) trajectory(response http.ResponseWriter, request *http.Request) {
	userSessionNoStore(response)
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "session trajectory requires GET", "")
		return
	}
	before, limit, ok := parseUserSessionTrajectoryQuery(response, request.URL.RawQuery)
	if !ok {
		return
	}
	actorID, ok := handler.authorize(response, request, "sessions.trajectory")
	if !ok || !requireEmptyUserSessionBody(response, request, "session trajectory") {
		return
	}
	result, err := handler.commands.GetTrajectory(
		request.Context(), request.PathValue("workspaceId"), request.PathValue("sessionId"), actorID, before, limit,
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func parseUserSessionTrajectoryQuery(response http.ResponseWriter, rawQuery string) (string, int, bool) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "session trajectory query is malformed", "")
		return "", 0, false
	}
	for key, current := range values {
		if (key != "before" && key != "limit") || len(current) != 1 {
			writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "session trajectory accepts one before and one limit parameter", "")
			return "", 0, false
		}
	}
	before := ""
	if cursors, present := values["before"]; present {
		before = cursors[0]
		if before == "" || len(before) > 4096 || strings.ContainsAny(before, "\x00\r\n") {
			writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "session trajectory before cursor is invalid", "")
			return "", 0, false
		}
	}
	limit := defaultUserTrajectoryLimit
	if limits, present := values["limit"]; present {
		raw := limits[0]
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maximumUserTrajectoryLimit {
			writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "session trajectory limit must be between 1 and 200", "")
			return "", 0, false
		}
	}
	return before, limit, true
}

func (handler *UserSessionHandler) collection(response http.ResponseWriter, request *http.Request) {
	userSessionNoStore(response)
	if request.URL.RawQuery != "" {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "session collection does not accept query parameters", "")
		return
	}
	workspaceID := request.PathValue("workspaceId")
	switch request.Method {
	case http.MethodGet:
		actorID, ok := handler.authorize(response, request, "sessions.list")
		if !ok || !requireEmptyUserSessionBody(response, request, "session list") {
			return
		}
		result, err := handler.commands.ListSessions(request.Context(), workspaceID, actorID)
		if err != nil {
			handler.writeError(response, request, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	case http.MethodPost:
		actorID, ok := handler.authorize(response, request, "sessions.create")
		if !ok {
			return
		}
		var input corecontract.CreateUserSessionRequest
		if !decodeUserSessionJSON(response, request, &input) {
			return
		}
		result, err := handler.commands.CreateSession(request.Context(), workspaceID, actorID, input)
		if err != nil {
			handler.writeError(response, request, err)
			return
		}
		status := http.StatusOK
		if result.Created {
			status = http.StatusCreated
		}
		writeJSON(response, status, result)
	default:
		response.Header().Set("Allow", "GET, POST")
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "session collection requires GET or POST", "")
	}
}

func (handler *UserSessionHandler) resource(response http.ResponseWriter, request *http.Request) {
	userSessionNoStore(response)
	if request.URL.RawQuery != "" {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "session resource does not accept query parameters", "")
		return
	}
	workspaceID, sessionID := request.PathValue("workspaceId"), request.PathValue("sessionId")
	switch request.Method {
	case http.MethodGet:
		actorID, ok := handler.authorize(response, request, "sessions.get")
		if !ok || !requireEmptyUserSessionBody(response, request, "session read") {
			return
		}
		result, err := handler.commands.GetSession(request.Context(), workspaceID, sessionID, actorID)
		if err != nil {
			handler.writeError(response, request, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	case http.MethodPatch:
		actorID, ok := handler.authorize(response, request, "sessions.update")
		if !ok {
			return
		}
		var input corecontract.UpdateUserSessionRequest
		if !decodeUserSessionJSON(response, request, &input) {
			return
		}
		result, err := handler.commands.UpdateSession(request.Context(), workspaceID, sessionID, actorID, input)
		if err != nil {
			handler.writeError(response, request, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	default:
		response.Header().Set("Allow", "GET, PATCH")
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "session resource requires GET or PATCH", "")
	}
}

func (handler *UserSessionHandler) archive(response http.ResponseWriter, request *http.Request) {
	userSessionNoStore(response)
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "session archive requires POST", "")
		return
	}
	if request.URL.RawQuery != "" {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "session archive does not accept query parameters", "")
		return
	}
	actorID, ok := handler.authorize(response, request, "sessions.archive")
	if !ok {
		return
	}
	var input corecontract.ArchiveUserSessionRequest
	if !decodeUserSessionJSON(response, request, &input) {
		return
	}
	result, err := handler.commands.ArchiveSession(
		request.Context(), request.PathValue("workspaceId"), request.PathValue("sessionId"), actorID, input,
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *UserSessionHandler) authorize(response http.ResponseWriter, request *http.Request, action string) (string, bool) {
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

func (handler *UserSessionHandler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	if request.Context().Err() != nil {
		return
	}
	var stateError *coredb.StateError
	if !errors.As(err, &stateError) || stateError.Code == coredb.ErrorDatabase {
		writePublicRunError(response, http.StatusInternalServerError, "internal_error", "core could not complete the user session request", "")
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
	writePublicRunError(response, status, string(stateError.Code), stateError.Message, "")
}

func userSessionNoStore(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
}

func requireEmptyUserSessionBody(response http.ResponseWriter, request *http.Request, operation string) bool {
	if request.ContentLength == 0 && len(request.TransferEncoding) == 0 {
		return true
	}
	writePublicRunError(response, http.StatusBadRequest, "invalid_argument", operation+" requires an empty request", "")
	return false
}

func decodeUserSessionJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" || len(parameters) != 0 {
		writePublicRunError(response, http.StatusUnsupportedMediaType, "invalid_argument", "Content-Type must be exactly application/json", "")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumUserSessionRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "request body is not valid user session JSON", "")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "request body contains trailing data", "")
		return false
	}
	return true
}

type StateStoreUserSessionCommands struct {
	Store             *coredb.StateStore
	Prompts           UserPromptReader
	TrajectoryCursors *trajectorycursor.Codec
}

func (commands StateStoreUserSessionCommands) ListSessions(ctx context.Context, workspaceID, actorID string) (corecontract.ListUserSessionsResponse, error) {
	items, err := commands.Store.ListUserSessions(ctx, workspaceID, actorID)
	if err != nil {
		return corecontract.ListUserSessionsResponse{}, err
	}
	sessions := make([]corecontract.UserSessionState, len(items))
	for index := range items {
		sessions[index] = contractUserSession(items[index])
	}
	return corecontract.ListUserSessionsResponse{Sessions: sessions}, nil
}

func (commands StateStoreUserSessionCommands) GetSession(ctx context.Context, workspaceID, sessionID, actorID string) (corecontract.UserSessionState, error) {
	session, err := commands.Store.GetUserSession(ctx, workspaceID, sessionID, actorID)
	return contractUserSession(session), err
}

func (commands StateStoreUserSessionCommands) UpdatePermissionMode(ctx context.Context, workspaceID, sessionID, actorID string, input corecontract.UpdateUserSessionPermissionModeRequest) (corecontract.UpdateUserSessionPermissionModeResponse, error) {
	result, err := commands.Store.UpdateUserSessionPermissionMode(ctx, coredb.UpdateUserSessionPermissionModeCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		PermissionMode:                runmanifest.CodexPermissionMode(input.PermissionMode),
		ExpectedPermissionModeVersion: input.ExpectedPermissionModeVersion,
	})
	return corecontract.UpdateUserSessionPermissionModeResponse{Session: contractUserSession(result.Session), Changed: result.Changed}, err
}

func (commands StateStoreUserSessionCommands) CreateSession(ctx context.Context, workspaceID, actorID string, input corecontract.CreateUserSessionRequest) (corecontract.CreateUserSessionResponse, error) {
	result, err := commands.Store.CreateUserSession(ctx, coredb.CreateUserSessionCommand{
		WorkspaceID: workspaceID, SessionID: input.SessionID, ActorID: actorID, Title: input.Title,
	})
	return corecontract.CreateUserSessionResponse{Session: contractUserSession(result.Session), Created: result.Created}, err
}

func (commands StateStoreUserSessionCommands) UpdateSession(ctx context.Context, workspaceID, sessionID, actorID string, input corecontract.UpdateUserSessionRequest) (corecontract.UpdateUserSessionResponse, error) {
	result, err := commands.Store.UpdateUserSession(ctx, coredb.UpdateUserSessionCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		Title: input.Title, ExpectedVersion: input.ExpectedVersion,
	})
	return corecontract.UpdateUserSessionResponse{Session: contractUserSession(result.Session), Changed: result.Changed}, err
}

func (commands StateStoreUserSessionCommands) ArchiveSession(ctx context.Context, workspaceID, sessionID, actorID string, input corecontract.ArchiveUserSessionRequest) (corecontract.ArchiveUserSessionResponse, error) {
	result, err := commands.Store.ArchiveUserSession(ctx, coredb.ArchiveUserSessionCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID, ExpectedVersion: input.ExpectedVersion,
	})
	return corecontract.ArchiveUserSessionResponse{Session: contractUserSession(result.Session), Changed: result.Changed}, err
}

func contractUserSession(session coredb.UserSession) corecontract.UserSessionState {
	return corecontract.UserSessionState{
		SessionID: session.ID, WorkspaceID: session.WorkspaceID, Title: session.Title,
		Status: session.Status, ActiveRunID: session.ActiveRunID, Version: session.Version,
		PermissionMode: string(session.PermissionMode), PermissionModeVersion: session.PermissionModeVersion,
		CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
	}
}

var _ UserSessionCommands = StateStoreUserSessionCommands{}
