package coreserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

const maximumWorkspaceCredentialRequestBytes = int64(512 * 1024)

type WorkspaceCredentialCommands interface {
	ListSchemas(context.Context) (corecontract.ListWorkspaceCredentialProviderSchemasResponse, error)
	ListBindings(context.Context, string, string, string) (corecontract.ListWorkspaceCredentialsResponse, error)
	CreateBinding(context.Context, string, string, string, corecontract.CreateWorkspaceCredentialRequest) (corecontract.CreateWorkspaceCredentialResponse, error)
	RotateBinding(context.Context, string, string, string, string, corecontract.RotateWorkspaceCredentialRequest) (corecontract.RotateWorkspaceCredentialResponse, error)
	RenameBinding(context.Context, string, string, string, string, corecontract.RenameWorkspaceCredentialRequest) (corecontract.RenameWorkspaceCredentialResponse, error)
	RevokeBinding(context.Context, string, string, string, string, int64) (corecontract.RevokeWorkspaceCredentialResponse, error)
	DeleteBinding(context.Context, string, string, string, string, int64) (corecontract.DeleteWorkspaceCredentialResponse, error)
	SetDefaultBinding(context.Context, string, string, string, string, int64) (corecontract.SetDefaultWorkspaceCredentialResponse, error)
}

type WorkspaceCredentialHandler struct {
	workload WorkloadAuthorizer
	users    UserTokenAuthorizer
	commands WorkspaceCredentialCommands
}

func NewWorkspaceCredentialHandler(workload WorkloadAuthorizer, users UserTokenAuthorizer, commands WorkspaceCredentialCommands) (*WorkspaceCredentialHandler, error) {
	if workload == nil || users == nil || commands == nil {
		return nil, errors.New("workspace credential workload authorizer, user authorizer, and commands are required")
	}
	return &WorkspaceCredentialHandler{workload: workload, users: users, commands: commands}, nil
}

func (handler *WorkspaceCredentialHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(corecontract.WorkspaceCredentialProviderSchemasPath, handler)
	mux.Handle(corecontract.WorkspaceCredentialCollectionRoutePattern, handler)
	mux.Handle(corecontract.WorkspaceCredentialResourceRoutePattern, handler)
	return mux
}

func (handler *WorkspaceCredentialHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if request.URL.RawQuery != "" {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "workspace credential endpoints do not accept query parameters", "")
		return
	}
	if request.URL.Path == corecontract.WorkspaceCredentialProviderSchemasPath {
		handler.schemas(response, request)
		return
	}
	workspaceID, kind, bindingAction := request.PathValue("workspaceId"), request.PathValue("kind"), request.PathValue("bindingId")
	if workspaceID == "" || kind == "" {
		writePublicRunError(response, http.StatusNotFound, "not_found", "workspace credential endpoint was not found", "")
		return
	}
	switch {
	case strings.HasSuffix(bindingAction, ":rotate"):
		handler.rotate(response, request, workspaceID, kind, strings.TrimSuffix(bindingAction, ":rotate"))
	case strings.HasSuffix(bindingAction, ":revoke"):
		handler.revoke(response, request, workspaceID, kind, strings.TrimSuffix(bindingAction, ":revoke"))
	case strings.HasSuffix(bindingAction, ":delete"):
		handler.delete(response, request, workspaceID, kind, strings.TrimSuffix(bindingAction, ":delete"))
	case strings.HasSuffix(bindingAction, ":setDefault"):
		handler.setDefault(response, request, workspaceID, kind, strings.TrimSuffix(bindingAction, ":setDefault"))
	case bindingAction != "":
		handler.resource(response, request, workspaceID, kind, bindingAction)
	default:
		handler.collection(response, request, workspaceID, kind)
	}
}

func (handler *WorkspaceCredentialHandler) schemas(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "credential provider schemas require GET", "")
		return
	}
	if _, ok := handler.authorize(response, request, "credentials.schemas"); !ok || !requireCredentialEmptyBody(response, request) {
		return
	}
	result, err := handler.commands.ListSchemas(request.Context())
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *WorkspaceCredentialHandler) collection(response http.ResponseWriter, request *http.Request, workspaceID, kind string) {
	switch request.Method {
	case http.MethodGet:
		actorID, ok := handler.authorize(response, request, "credentials.list")
		if !ok || !requireCredentialEmptyBody(response, request) {
			return
		}
		result, err := handler.commands.ListBindings(request.Context(), workspaceID, kind, actorID)
		if err != nil {
			handler.writeError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	case http.MethodPost:
		actorID, ok := handler.authorize(response, request, "credentials.create")
		if !ok {
			return
		}
		var input corecontract.CreateWorkspaceCredentialRequest
		if !decodeWorkspaceCredentialJSON(response, request, &input) {
			return
		}
		result, err := handler.commands.CreateBinding(request.Context(), workspaceID, kind, actorID, input)
		if err != nil {
			handler.writeError(response, err)
			return
		}
		status := http.StatusCreated
		if !result.Created {
			status = http.StatusOK
		}
		writeJSON(response, status, result)
	default:
		response.Header().Set("Allow", "GET, POST")
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "credential collection requires GET or POST", "")
	}
}

func (handler *WorkspaceCredentialHandler) resource(response http.ResponseWriter, request *http.Request, workspaceID, kind, bindingID string) {
	if request.Method != http.MethodPatch {
		response.Header().Set("Allow", http.MethodPatch)
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "credential resource requires PATCH", "")
		return
	}
	actorID, ok := handler.authorize(response, request, "credentials.update")
	if !ok {
		return
	}
	var input corecontract.RenameWorkspaceCredentialRequest
	if !decodeWorkspaceCredentialJSON(response, request, &input) {
		return
	}
	result, err := handler.commands.RenameBinding(request.Context(), workspaceID, kind, bindingID, actorID, input)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *WorkspaceCredentialHandler) rotate(response http.ResponseWriter, request *http.Request, workspaceID, kind, bindingID string) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "credential rotation requires POST", "")
		return
	}
	actorID, ok := handler.authorize(response, request, "credentials.rotate")
	if !ok {
		return
	}
	var input corecontract.RotateWorkspaceCredentialRequest
	if !decodeWorkspaceCredentialJSON(response, request, &input) {
		return
	}
	result, err := handler.commands.RotateBinding(request.Context(), workspaceID, kind, bindingID, actorID, input)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *WorkspaceCredentialHandler) revoke(response http.ResponseWriter, request *http.Request, workspaceID, kind, bindingID string) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "credential revoke requires POST", "")
		return
	}
	actorID, ok := handler.authorize(response, request, "credentials.revoke")
	if !ok {
		return
	}
	var input corecontract.RevokeWorkspaceCredentialRequest
	if !decodeWorkspaceCredentialJSON(response, request, &input) {
		return
	}
	result, err := handler.commands.RevokeBinding(request.Context(), workspaceID, kind, bindingID, actorID, input.ExpectedAuthorityVersion)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *WorkspaceCredentialHandler) delete(response http.ResponseWriter, request *http.Request, workspaceID, kind, bindingID string) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "credential deletion requires POST", "")
		return
	}
	actorID, ok := handler.authorize(response, request, "credentials.delete")
	if !ok {
		return
	}
	var input corecontract.DeleteWorkspaceCredentialRequest
	if !decodeWorkspaceCredentialJSON(response, request, &input) {
		return
	}
	result, err := handler.commands.DeleteBinding(request.Context(), workspaceID, kind, bindingID, actorID, input.ExpectedAuthorityVersion)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *WorkspaceCredentialHandler) setDefault(response http.ResponseWriter, request *http.Request, workspaceID, kind, bindingID string) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "credential default action requires POST", "")
		return
	}
	actorID, ok := handler.authorize(response, request, "credentials.set-default")
	if !ok {
		return
	}
	var input corecontract.SetDefaultWorkspaceCredentialRequest
	if !decodeWorkspaceCredentialJSON(response, request, &input) {
		return
	}
	result, err := handler.commands.SetDefaultBinding(request.Context(), workspaceID, kind, bindingID, actorID, input.ExpectedAuthorityVersion)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *WorkspaceCredentialHandler) authorize(response http.ResponseWriter, request *http.Request, action string) (string, bool) {
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
	response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-platform-api"`)
	writePublicRunError(response, http.StatusUnauthorized, "unauthorized", "a valid agentserver-platform-api access token is required", "")
	return "", false
}

func (handler *WorkspaceCredentialHandler) writeError(response http.ResponseWriter, err error) {
	var stateError *coredb.StateError
	if !errors.As(err, &stateError) || stateError.Code == coredb.ErrorDatabase {
		writePublicRunError(response, http.StatusInternalServerError, "internal_error", "core could not complete the workspace credential request", "")
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

func requireCredentialEmptyBody(response http.ResponseWriter, request *http.Request) bool {
	if request.ContentLength == 0 && len(request.TransferEncoding) == 0 {
		return true
	}
	writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "credential read requires an empty request", "")
	return false
}

func decodeWorkspaceCredentialJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" || len(parameters) != 0 {
		writePublicRunError(response, http.StatusUnsupportedMediaType, "invalid_argument", "Content-Type must be exactly application/json", "")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumWorkspaceCredentialRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "workspace credential request JSON is invalid", "")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "workspace credential request contains trailing data", "")
		return false
	}
	return true
}
