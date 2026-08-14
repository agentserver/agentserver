package coreserver

import (
	"errors"
	"net/http"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/corecredentials"
)

// ExecutionCredentialHandler is intentionally separate from the Policy
// Webhook handler. It is always mounted for managed execution, accepts only
// the executor-gateway workload identity, and exposes the two calls needed at
// an exact managed CLI process boundary: resolve the workspace mode/binding
// authority, then materialize the process_env credential. Neither route is a
// Policy Webhook or egress-authorizer surface.
type ExecutionCredentialHandler struct {
	authorizer WorkloadAuthorizer
	service    *EgressCredentialService
}

func NewExecutionCredentialHandler(authorizer WorkloadAuthorizer, service *EgressCredentialService) (*ExecutionCredentialHandler, error) {
	if authorizer == nil || service == nil {
		return nil, errors.New("v2 execution credential workload identity and service are required")
	}
	return &ExecutionCredentialHandler{authorizer: authorizer, service: service}, nil
}

func (handler *ExecutionCredentialHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if request == nil || request.URL == nil || request.Method != http.MethodPost ||
		request.URL.RawQuery != "" ||
		request.URL.ForceQuery || request.URL.Fragment != "" || request.URL.RawPath != "" {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "v2 execution credential endpoint not found"})
		return
	}
	switch request.URL.Path {
	case corecontract.ResolveExecutionCredentialAuthorityPath:
		handler.authority(response, request)
	case corecontract.ResolveExecutionCredentialPath:
		handler.resolve(response, request)
	default:
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "v2 execution credential endpoint not found"})
	}
}

func (handler *ExecutionCredentialHandler) authority(response http.ResponseWriter, request *http.Request) {
	if err := handler.authorizer.AuthorizeWorkload(request, "execution.credentials.resolve-authority"); err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "workload is not authorized for credential authority resolution"})
		return
	}
	var command corecontract.ResolveEgressCredentialAuthorityRequest
	if !decodeCommandWithLimit(response, request, &command, maxEgressCredentialCommandBytes) {
		return
	}
	result, err := handler.service.ResolveAuthority(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ExecutionCredentialHandler) resolve(response http.ResponseWriter, request *http.Request) {
	if err := handler.authorizer.AuthorizeWorkload(request, "execution.credentials.resolve"); err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "workload is not authorized for execution credential resolution"})
		return
	}
	var command corecontract.ResolveExecutionCredentialRequest
	if !decodeCommandWithLimit(response, request, &command, maxEgressCredentialCommandBytes) {
		return
	}
	result, err := handler.service.ResolveExecutionCredential(request.Context(), command)
	if err != nil {
		var resolved *corecredentials.ResolveError
		if errors.As(err, &resolved) {
			writeEgressCredentialError(response, err)
		} else {
			writeCommandError(response, err)
		}
		return
	}
	writeJSON(response, http.StatusOK, result)
}

var _ http.Handler = (*ExecutionCredentialHandler)(nil)
