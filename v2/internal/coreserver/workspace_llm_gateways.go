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

const maximumPublicLLMGatewayRequestBytes = int64(128 * 1024)

type WorkspaceLLMGatewayCommands interface {
	CreateGateway(context.Context, string, string, corecontract.CreateWorkspaceLLMGatewayRequest) (corecontract.CreateWorkspaceLLMGatewayResponse, error)
	ListGateways(context.Context, string, string) (corecontract.ListWorkspaceLLMGatewaysResponse, error)
	BeginAuthorization(context.Context, string, string, string, corecontract.BeginWorkspaceLLMGatewayAuthorizationRequest) (corecontract.BeginWorkspaceLLMGatewayAuthorizationResponse, error)
	CompleteAuthorization(context.Context, string, string, string, corecontract.CompleteWorkspaceLLMGatewayAuthorizationRequest) (corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse, error)
	RevokeGrant(context.Context, string, string, string) (corecontract.RevokeWorkspaceLLMGatewayGrantResponse, error)
	DisableGateway(context.Context, string, string, string) (corecontract.DisableWorkspaceLLMGatewayResponse, error)
}

type WorkspaceLLMGatewayHandler struct {
	workload WorkloadAuthorizer
	users    UserTokenAuthorizer
	commands WorkspaceLLMGatewayCommands
}

func NewWorkspaceLLMGatewayHandler(
	workload WorkloadAuthorizer,
	users UserTokenAuthorizer,
	commands WorkspaceLLMGatewayCommands,
) (*WorkspaceLLMGatewayHandler, error) {
	if workload == nil || users == nil || commands == nil {
		return nil, errors.New("browser workload authorizer, user authorizer, and workspace LLM gateway commands are required")
	}
	return &WorkspaceLLMGatewayHandler{workload: workload, users: users, commands: commands}, nil
}

func (handler *WorkspaceLLMGatewayHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(corecontract.LLMGatewayCollectionRoutePattern, handler)
	mux.Handle(corecontract.LLMGatewayActionRoutePattern, handler)
	return mux
}

func (handler *WorkspaceLLMGatewayHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if request.URL.RawQuery != "" {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "workspace LLM gateway endpoints do not accept query parameters", "")
		return
	}
	workspaceID := request.PathValue("workspaceId")
	if request.PathValue("gatewayAction") == "" {
		handler.collection(response, request, workspaceID)
		return
	}
	handler.action(response, request, workspaceID, request.PathValue("gatewayAction"))
}

func (handler *WorkspaceLLMGatewayHandler) collection(response http.ResponseWriter, request *http.Request, workspaceID string) {
	action := "llm-gateways.list"
	if request.Method == http.MethodPost {
		action = "llm-gateways.create"
	} else if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET, POST")
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "workspace LLM gateway collection requires GET or POST", "")
		return
	}
	actorID, ok := handler.authorize(response, request, action)
	if !ok {
		return
	}
	if request.Method == http.MethodGet {
		if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
			writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "workspace LLM gateway list requires an empty request", "")
			return
		}
		result, err := handler.commands.ListGateways(request.Context(), workspaceID, actorID)
		if err != nil {
			handler.writeServiceError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
		return
	}
	var input corecontract.CreateWorkspaceLLMGatewayRequest
	if !decodePublicLLMGatewayJSON(response, request, &input) {
		return
	}
	result, err := handler.commands.CreateGateway(request.Context(), workspaceID, actorID, input)
	if err != nil {
		handler.writeServiceError(response, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(response, status, result)
}

func (handler *WorkspaceLLMGatewayHandler) action(response http.ResponseWriter, request *http.Request, workspaceID, gatewayAction string) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "workspace LLM gateway actions require POST", "")
		return
	}
	var action string
	var gatewayID string
	switch {
	case strings.HasSuffix(gatewayAction, ":authorize"):
		gatewayID = strings.TrimSuffix(gatewayAction, ":authorize")
		action = "llm-gateways.authorize"
	case strings.HasSuffix(gatewayAction, ":completeAuthorization"):
		gatewayID = strings.TrimSuffix(gatewayAction, ":completeAuthorization")
		action = "llm-gateways.complete-authorization"
	case strings.HasSuffix(gatewayAction, ":revoke"):
		gatewayID = strings.TrimSuffix(gatewayAction, ":revoke")
		action = "llm-gateways.revoke"
	case strings.HasSuffix(gatewayAction, ":disable"):
		gatewayID = strings.TrimSuffix(gatewayAction, ":disable")
		action = "llm-gateways.disable"
	default:
		writePublicRunError(response, http.StatusNotFound, "not_found", "workspace LLM gateway action was not found", "")
		return
	}
	if gatewayID == "" || strings.Contains(gatewayID, ":") {
		writePublicRunError(response, http.StatusNotFound, "not_found", "workspace LLM gateway action was not found", "")
		return
	}
	actorID, ok := handler.authorize(response, request, action)
	if !ok {
		return
	}
	switch action {
	case "llm-gateways.authorize":
		var input corecontract.BeginWorkspaceLLMGatewayAuthorizationRequest
		if !decodePublicLLMGatewayJSON(response, request, &input) {
			return
		}
		result, err := handler.commands.BeginAuthorization(request.Context(), workspaceID, gatewayID, actorID, input)
		if err != nil {
			handler.writeServiceError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	case "llm-gateways.complete-authorization":
		var input corecontract.CompleteWorkspaceLLMGatewayAuthorizationRequest
		if !decodePublicLLMGatewayJSON(response, request, &input) {
			return
		}
		result, err := handler.commands.CompleteAuthorization(request.Context(), workspaceID, gatewayID, actorID, input)
		if err != nil {
			handler.writeServiceError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	case "llm-gateways.revoke":
		if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
			writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "workspace LLM gateway revoke requires an empty request", "")
			return
		}
		result, err := handler.commands.RevokeGrant(request.Context(), workspaceID, gatewayID, actorID)
		if err != nil {
			handler.writeServiceError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	case "llm-gateways.disable":
		if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
			writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "workspace LLM gateway disable requires an empty request", "")
			return
		}
		result, err := handler.commands.DisableGateway(request.Context(), workspaceID, gatewayID, actorID)
		if err != nil {
			handler.writeServiceError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func (handler *WorkspaceLLMGatewayHandler) authorize(
	response http.ResponseWriter,
	request *http.Request,
	action string,
) (string, bool) {
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
	response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-api"`)
	writePublicRunError(response, http.StatusUnauthorized, "unauthorized", "a valid agentserver-api user access token is required", "")
	return "", false
}

func (handler *WorkspaceLLMGatewayHandler) writeServiceError(response http.ResponseWriter, err error) {
	var stateError *coredb.StateError
	if !errors.As(err, &stateError) || stateError.Code == coredb.ErrorDatabase {
		writePublicRunError(response, http.StatusInternalServerError, "internal_error", "core could not complete the workspace LLM gateway request", "")
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

func decodePublicLLMGatewayJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" || len(parameters) != 0 {
		writePublicRunError(response, http.StatusUnsupportedMediaType, "invalid_argument", "Content-Type must be exactly application/json", "")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumPublicLLMGatewayRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "request body is not valid workspace LLM gateway JSON", "")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "request body contains trailing data", "")
		return false
	}
	return true
}
