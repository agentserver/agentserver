package coreserver

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

type WorkspaceCredentialAuthorizationCommands interface {
	BeginAuthorization(context.Context, string, string, string, corecontract.BeginWorkspaceCredentialAuthorizationRequest) (corecontract.BeginWorkspaceCredentialAuthorizationResponse, error)
	GetAuthorization(context.Context, string, string, string, string) (corecontract.GetWorkspaceCredentialAuthorizationResponse, error)
	PollAuthorization(context.Context, string, string, string, string) (corecontract.PollWorkspaceCredentialAuthorizationResponse, error)
	CancelAuthorization(context.Context, string, string, string, string, corecontract.CancelWorkspaceCredentialAuthorizationRequest) (corecontract.CancelWorkspaceCredentialAuthorizationResponse, error)
}

type WorkspaceCredentialAuthorizationHandler struct {
	base     *WorkspaceCredentialHandler
	commands WorkspaceCredentialAuthorizationCommands
}

func NewWorkspaceCredentialAuthorizationHandler(workload WorkloadAuthorizer, users UserTokenAuthorizer, commands WorkspaceCredentialAuthorizationCommands) (*WorkspaceCredentialAuthorizationHandler, error) {
	if workload == nil || users == nil || commands == nil {
		return nil, errors.New("workspace credential authorization workload authorizer, user authorizer, and commands are required")
	}
	base := &WorkspaceCredentialHandler{workload: workload, users: users}
	return &WorkspaceCredentialAuthorizationHandler{base: base, commands: commands}, nil
}

func (handler *WorkspaceCredentialAuthorizationHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(corecontract.WorkspaceCredentialAuthorizationCollectionRoutePattern, handler)
	mux.Handle(corecontract.WorkspaceCredentialAuthorizationResourceRoutePattern, handler)
	return mux
}

func (handler *WorkspaceCredentialAuthorizationHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if handler == nil || handler.base == nil || handler.commands == nil {
		writePublicRunError(response, http.StatusServiceUnavailable, "authorization_unavailable", "credential authorization is temporarily unavailable", "")
		return
	}
	if request.URL.RawQuery != "" || request.URL.RawPath != "" || request.URL.Fragment != "" {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "credential authorization route must be canonical", "")
		return
	}
	workspaceID, kind := request.PathValue("workspaceId"), request.PathValue("kind")
	authorizationAction := request.PathValue("authorizationId")
	if workspaceID == "" || kind == "" {
		writePublicRunError(response, http.StatusNotFound, "not_found", "credential authorization endpoint was not found", "")
		return
	}
	switch {
	case strings.HasSuffix(authorizationAction, ":poll"):
		handler.poll(response, request, workspaceID, kind, strings.TrimSuffix(authorizationAction, ":poll"))
	case strings.HasSuffix(authorizationAction, ":cancel"):
		handler.cancel(response, request, workspaceID, kind, strings.TrimSuffix(authorizationAction, ":cancel"))
	case authorizationAction != "":
		handler.get(response, request, workspaceID, kind, authorizationAction)
	default:
		handler.begin(response, request, workspaceID, kind)
	}
}

func (handler *WorkspaceCredentialAuthorizationHandler) begin(response http.ResponseWriter, request *http.Request, workspaceID, kind string) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "credential authorization begin requires POST", "")
		return
	}
	actorID, ok := handler.base.authorize(response, request, "credentials.authorizations.begin")
	if !ok {
		return
	}
	var input corecontract.BeginWorkspaceCredentialAuthorizationRequest
	if !decodeWorkspaceCredentialJSON(response, request, &input) {
		return
	}
	result, err := handler.commands.BeginAuthorization(request.Context(), workspaceID, kind, actorID, input)
	if err != nil {
		handler.base.writeError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, result)
}

func (handler *WorkspaceCredentialAuthorizationHandler) get(response http.ResponseWriter, request *http.Request, workspaceID, kind, authorizationID string) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "credential authorization resource requires GET", "")
		return
	}
	actorID, ok := handler.base.authorize(response, request, "credentials.authorizations.get")
	if !ok || !requireCredentialEmptyBody(response, request) {
		return
	}
	result, err := handler.commands.GetAuthorization(request.Context(), workspaceID, kind, authorizationID, actorID)
	if err != nil {
		handler.base.writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *WorkspaceCredentialAuthorizationHandler) poll(response http.ResponseWriter, request *http.Request, workspaceID, kind, authorizationID string) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "credential authorization poll requires POST", "")
		return
	}
	actorID, ok := handler.base.authorize(response, request, "credentials.authorizations.poll")
	if !ok || !requireCredentialEmptyBody(response, request) {
		return
	}
	result, err := handler.commands.PollAuthorization(request.Context(), workspaceID, kind, authorizationID, actorID)
	if err != nil {
		handler.base.writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *WorkspaceCredentialAuthorizationHandler) cancel(response http.ResponseWriter, request *http.Request, workspaceID, kind, authorizationID string) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "credential authorization cancel requires POST", "")
		return
	}
	actorID, ok := handler.base.authorize(response, request, "credentials.authorizations.cancel")
	if !ok {
		return
	}
	var input corecontract.CancelWorkspaceCredentialAuthorizationRequest
	if !decodeWorkspaceCredentialJSON(response, request, &input) {
		return
	}
	result, err := handler.commands.CancelAuthorization(request.Context(), workspaceID, kind, authorizationID, actorID, input)
	if err != nil {
		handler.base.writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

var _ http.Handler = (*WorkspaceCredentialAuthorizationHandler)(nil)
var _ WorkspaceCredentialAuthorizationCommands = StateStoreWorkspaceCredentialCommands{}
