package coreserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

type RunLaunchStateQueries interface {
	ResolveRunLaunchState(context.Context, corecontract.ResolveRunLaunchStateRequest) (corecontract.ResolveRunLaunchStateResponse, error)
}

type RunLaunchStateHandler struct {
	authorizer WorkloadAuthorizer
	queries    RunLaunchStateQueries
}

func NewRunLaunchStateHandler(authorizer WorkloadAuthorizer, queries RunLaunchStateQueries) (*RunLaunchStateHandler, error) {
	if authorizer == nil {
		return nil, errors.New("workload authorizer is required")
	}
	if queries == nil {
		return nil, errors.New("run launch state queries are required")
	}
	return &RunLaunchStateHandler{authorizer: authorizer, queries: queries}, nil
}

func (handler *RunLaunchStateHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.Path != corecontract.ResolveRunLaunchStatePath {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal query endpoint not found"})
		return
	}
	if err := handler.authorizer.AuthorizeWorkload(request, "run-launch-states.resolve"); err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "workload is not authorized for this query"})
		return
	}
	var query corecontract.ResolveRunLaunchStateRequest
	if !decodeCommand(response, request, &query) {
		return
	}
	result, err := handler.queries.ResolveRunLaunchState(request.Context(), query)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}
