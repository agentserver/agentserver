package coreserver

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

type RunDispatchCommands interface {
	ClaimRunDispatches(context.Context, corecontract.ClaimRunDispatchesRequest) (corecontract.ClaimRunDispatchesResponse, error)
	CompleteRunDispatch(context.Context, string, corecontract.CompleteRunDispatchRequest) (corecontract.CompleteRunDispatchResponse, error)
	ReleaseRunDispatch(context.Context, string, corecontract.ReleaseRunDispatchRequest) (corecontract.ReleaseRunDispatchResponse, error)
}

type RunDispatchHandler struct {
	authorizer WorkloadAuthorizer
	commands   RunDispatchCommands
}

func NewRunDispatchHandler(authorizer WorkloadAuthorizer, commands RunDispatchCommands) (*RunDispatchHandler, error) {
	if authorizer == nil {
		return nil, errors.New("workload authorizer is required")
	}
	if commands == nil {
		return nil, errors.New("run dispatch commands are required")
	}
	return &RunDispatchHandler{authorizer: authorizer, commands: commands}, nil
}

func (handler *RunDispatchHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal command endpoint not found"})
		return
	}
	if request.URL.Path == corecontract.ClaimRunDispatchesPath {
		handler.claim(response, request)
		return
	}
	dispatchID, action, ok := parseRunDispatchAction(request.URL.Path)
	if !ok {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal command endpoint not found"})
		return
	}
	switch action {
	case "complete":
		handler.complete(response, request, dispatchID)
	case "release":
		handler.release(response, request, dispatchID)
	default:
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal command endpoint not found"})
	}
}

func (handler *RunDispatchHandler) claim(response http.ResponseWriter, request *http.Request) {
	if !handler.authorize(response, request, "run-dispatches.claim") {
		return
	}
	var command corecontract.ClaimRunDispatchesRequest
	if !decodeCommand(response, request, &command) {
		return
	}
	result, err := handler.commands.ClaimRunDispatches(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *RunDispatchHandler) complete(response http.ResponseWriter, request *http.Request, dispatchID string) {
	if !handler.authorize(response, request, "run-dispatches.complete") {
		return
	}
	var command corecontract.CompleteRunDispatchRequest
	if !decodeCommand(response, request, &command) {
		return
	}
	result, err := handler.commands.CompleteRunDispatch(request.Context(), dispatchID, command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *RunDispatchHandler) release(response http.ResponseWriter, request *http.Request, dispatchID string) {
	if !handler.authorize(response, request, "run-dispatches.release") {
		return
	}
	var command corecontract.ReleaseRunDispatchRequest
	if !decodeCommand(response, request, &command) {
		return
	}
	result, err := handler.commands.ReleaseRunDispatch(request.Context(), dispatchID, command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *RunDispatchHandler) authorize(response http.ResponseWriter, request *http.Request, action string) bool {
	if err := handler.authorizer.AuthorizeWorkload(request, action); err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "workload is not authorized for this command"})
		return false
	}
	return true
}

func parseRunDispatchAction(path string) (dispatchID, action string, ok bool) {
	if !strings.HasPrefix(path, corecontract.RunDispatchPathPrefix) {
		return "", "", false
	}
	remainder := strings.TrimPrefix(path, corecontract.RunDispatchPathPrefix)
	if remainder == "" || strings.Contains(remainder, "/") {
		return "", "", false
	}
	for _, candidate := range []string{"complete", "release"} {
		suffix := ":" + candidate
		if strings.HasSuffix(remainder, suffix) && len(remainder) > len(suffix) {
			return strings.TrimSuffix(remainder, suffix), candidate, true
		}
	}
	return "", "", false
}
