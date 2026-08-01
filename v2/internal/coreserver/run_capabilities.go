package coreserver

import (
	"errors"
	"net/http"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

const maximumRunCapabilityCommandBytes int64 = 64 * 1024

// RunCapabilityHandler keeps the three workload identities and actions
// separate even though they share one authority service. A pool can issue but
// cannot live-authorize itself; executor-gateway cannot present a model token;
// llmproxy cannot present an executor token.
type RunCapabilityHandler struct {
	poolAuthorizer     WorkloadAuthorizer
	executorAuthorizer WorkloadAuthorizer
	llmproxyAuthorizer WorkloadAuthorizer
	authority          RunCapabilityAuthority
}

func NewRunCapabilityHandler(
	poolAuthorizer WorkloadAuthorizer,
	executorAuthorizer WorkloadAuthorizer,
	llmproxyAuthorizer WorkloadAuthorizer,
	authority RunCapabilityAuthority,
) (*RunCapabilityHandler, error) {
	if poolAuthorizer == nil || executorAuthorizer == nil || llmproxyAuthorizer == nil {
		return nil, errors.New("all production run capability workload authorizers are required")
	}
	if authority == nil {
		return nil, errors.New("production run capability authority is required")
	}
	return &RunCapabilityHandler{
		poolAuthorizer: poolAuthorizer, executorAuthorizer: executorAuthorizer,
		llmproxyAuthorizer: llmproxyAuthorizer, authority: authority,
	}, nil
}

func (handler *RunCapabilityHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if request.Method != http.MethodPost || request.URL.RawQuery != "" {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal run capability endpoint not found"})
		return
	}
	switch request.URL.Path {
	case corecontract.IssueRunCapabilitiesPath:
		handler.issue(response, request)
	case corecontract.AuthorizeExecutorRunCapabilityPath:
		handler.authorizeExecutor(response, request)
	case corecontract.AuthorizeLLMProxyRunCapabilityPath:
		handler.authorizeLLMProxy(response, request)
	default:
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal run capability endpoint not found"})
	}
}

func (handler *RunCapabilityHandler) issue(response http.ResponseWriter, request *http.Request) {
	if !authorizeRunCapabilityWorkload(response, request, handler.poolAuthorizer, "run-capabilities.issue") {
		return
	}
	var command corecontract.IssueRunCapabilitiesRequest
	if !decodeCommandWithLimit(response, request, &command, maximumRunCapabilityCommandBytes) {
		return
	}
	result, err := handler.authority.IssueRunCapabilities(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *RunCapabilityHandler) authorizeExecutor(response http.ResponseWriter, request *http.Request) {
	if !authorizeRunCapabilityWorkload(response, request, handler.executorAuthorizer, "run-capabilities.authorize-executor-mcp") {
		return
	}
	token, ok := readRunCapabilityBearer(response, request)
	if !ok {
		return
	}
	var command corecontract.AuthorizeExecutorRunCapabilityRequest
	if !decodeCommandWithLimit(response, request, &command, maximumRunCapabilityCommandBytes) {
		return
	}
	result, err := handler.authority.AuthorizeExecutorRunCapability(request.Context(), token, command)
	if err != nil {
		writeRunCapabilityAuthorizationError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *RunCapabilityHandler) authorizeLLMProxy(response http.ResponseWriter, request *http.Request) {
	if !authorizeRunCapabilityWorkload(response, request, handler.llmproxyAuthorizer, "run-capabilities.authorize-llmproxy") {
		return
	}
	token, ok := readRunCapabilityBearer(response, request)
	if !ok {
		return
	}
	var command corecontract.AuthorizeLLMProxyRunCapabilityRequest
	if !decodeCommandWithLimit(response, request, &command, maximumRunCapabilityCommandBytes) {
		return
	}
	result, err := handler.authority.AuthorizeLLMProxyRunCapability(request.Context(), token, command)
	if err != nil {
		writeRunCapabilityAuthorizationError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func authorizeRunCapabilityWorkload(
	response http.ResponseWriter,
	request *http.Request,
	authorizer WorkloadAuthorizer,
	action string,
) bool {
	if err := authorizer.AuthorizeWorkload(request, action); err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "workload is not authorized for this run capability action"})
		return false
	}
	return true
}

func readRunCapabilityBearer(response http.ResponseWriter, request *http.Request) (string, bool) {
	values := request.Header.Values("Authorization")
	if len(values) != 1 || strings.Contains(values[0], ",") || !strings.HasPrefix(values[0], "Bearer ") {
		writeRunCapabilityBearerError(response)
		return "", false
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if token == "" || strings.TrimSpace(token) != token || len(token) > 32*1024 {
		writeRunCapabilityBearerError(response)
		return "", false
	}
	return token, true
}

func writeRunCapabilityBearerError(response http.ResponseWriter) {
	response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-core", error="invalid_token"`)
	writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "run capability is not currently authorized"})
}

func writeRunCapabilityAuthorizationError(response http.ResponseWriter, err error) {
	var stateError *coredb.StateError
	if !errors.As(err, &stateError) {
		writeError(response, http.StatusInternalServerError, corecontract.ErrorResponse{Code: "internal_error", Message: "internal run capability authorization failed"})
		return
	}
	if stateError.Code == coredb.ErrorDatabase {
		writeError(response, http.StatusInternalServerError, corecontract.ErrorResponse{Code: "internal_error", Message: "internal run capability authorization failed"})
		return
	}
	if stateError.Code == coredb.ErrorInvalidArgument {
		writeError(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "run capability authorization request is invalid"})
		return
	}
	writeRunCapabilityBearerError(response)
}
