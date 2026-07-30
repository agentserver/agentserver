package coreserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

type ExecutorEnvironmentQueries interface {
	ListExecutorEnvironments(context.Context, corecontract.ListExecutorEnvironmentsRequest) ([]corecontract.ExecutorEnvironment, error)
}

type ExecutorEnvironmentHandler struct {
	authorizer WorkloadAuthorizer
	queries    ExecutorEnvironmentQueries
}

func NewExecutorEnvironmentHandler(authorizer WorkloadAuthorizer, queries ExecutorEnvironmentQueries) (*ExecutorEnvironmentHandler, error) {
	if authorizer == nil {
		return nil, errors.New("workload authorizer is required")
	}
	if queries == nil {
		return nil, errors.New("executor environment queries are required")
	}
	return &ExecutorEnvironmentHandler{authorizer: authorizer, queries: queries}, nil
}

func (handler *ExecutorEnvironmentHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.Path != corecontract.ListExecutorEnvironmentsPath {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal query endpoint not found"})
		return
	}
	if err := handler.authorizer.AuthorizeWorkload(request, "executor-environments.list"); err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "workload is not authorized for this query"})
		return
	}
	var query corecontract.ListExecutorEnvironmentsRequest
	if !decodeCommand(response, request, &query) {
		return
	}
	environments, err := handler.queries.ListExecutorEnvironments(request.Context(), query)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	if environments == nil {
		environments = []corecontract.ExecutorEnvironment{}
	}
	writeJSON(response, http.StatusOK, corecontract.ListExecutorEnvironmentsResponse{Environments: environments})
}
