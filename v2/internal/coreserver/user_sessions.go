package coreserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

const maximumUserSessionRequestBytes = int64(64 * 1024)

type UserSessionCommands interface {
	ListSessions(context.Context, string, string) (corecontract.ListUserSessionsResponse, error)
	GetSession(context.Context, string, string, string) (corecontract.UserSessionState, error)
	CreateSession(context.Context, string, string, corecontract.CreateUserSessionRequest) (corecontract.CreateUserSessionResponse, error)
	UpdateSession(context.Context, string, string, string, corecontract.UpdateUserSessionRequest) (corecontract.UpdateUserSessionResponse, error)
	ArchiveSession(context.Context, string, string, string, corecontract.ArchiveUserSessionRequest) (corecontract.ArchiveUserSessionResponse, error)
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
	mux.HandleFunc(corecontract.UserSessionArchiveRoutePattern, handler.archive)
	return mux
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

type StateStoreUserSessionCommands struct{ Store *coredb.StateStore }

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
		CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
	}
}

var _ UserSessionCommands = StateStoreUserSessionCommands{}
