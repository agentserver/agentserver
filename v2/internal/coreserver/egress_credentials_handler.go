package coreserver

import (
	"errors"
	"net/http"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/corecredentials"
)

const maxEgressCredentialCommandBytes int64 = 128 * 1024

// EgressCredentialHandler exposes only workload-authenticated internal
// endpoints. Platform users never reach these routes; they use the
// workspace-credential routes instead.
type EgressCredentialHandler struct {
	executorAuthorizer WorkloadAuthorizer
	egressAuthorizer   WorkloadAuthorizer
	service            *EgressCredentialService
}

func NewEgressCredentialHandler(executorAuthorizer, egressAuthorizer WorkloadAuthorizer, service *EgressCredentialService) (*EgressCredentialHandler, error) {
	if executorAuthorizer == nil || egressAuthorizer == nil || service == nil {
		return nil, errors.New("v2 egress credential executor/authorizer identities and service are required")
	}
	return &EgressCredentialHandler{executorAuthorizer: executorAuthorizer, egressAuthorizer: egressAuthorizer, service: service}, nil
}

func (handler *EgressCredentialHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if request == nil || request.URL == nil || request.Method != http.MethodPost || request.URL.RawQuery != "" ||
		request.URL.ForceQuery || request.URL.Fragment != "" || request.URL.RawPath != "" {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "v2 egress credential endpoint not found"})
		return
	}
	switch request.URL.Path {
	case corecontract.ResolveEgressCredentialAuthorityPath:
		handler.authority(response, request)
	case corecontract.ResolveEgressCredentialPath:
		handler.resolve(response, request)
	case corecontract.RecordEgressCredentialAuditPath:
		handler.audit(response, request)
	default:
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "v2 egress credential endpoint not found"})
	}
}

func (handler *EgressCredentialHandler) authority(response http.ResponseWriter, request *http.Request) {
	if err := handler.executorAuthorizer.AuthorizeWorkload(request, "egress.credentials.resolve-authority"); err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "workload is not authorized for egress credential authority"})
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

func (handler *EgressCredentialHandler) resolve(response http.ResponseWriter, request *http.Request) {
	if err := handler.egressAuthorizer.AuthorizeWorkload(request, "egress.credentials.resolve"); err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "workload is not authorized for egress credential resolution"})
		return
	}
	var command corecontract.ResolveEgressCredentialRequest
	if !decodeCommandWithLimit(response, request, &command, maxEgressCredentialCommandBytes) {
		return
	}
	result, err := handler.service.Resolve(request.Context(), command)
	if err != nil {
		writeEgressCredentialError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *EgressCredentialHandler) audit(response http.ResponseWriter, request *http.Request) {
	if err := handler.egressAuthorizer.AuthorizeWorkload(request, "egress.credentials.audit"); err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "workload is not authorized for egress credential audit"})
		return
	}
	var command corecontract.RecordEgressCredentialAuditRequest
	if !decodeCommandWithLimit(response, request, &command, maxEgressCredentialCommandBytes) {
		return
	}
	result, err := handler.service.RecordAudit(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func writeEgressCredentialError(response http.ResponseWriter, err error) {
	var resolved *corecredentials.ResolveError
	if !errors.As(err, &resolved) {
		writeError(response, http.StatusInternalServerError, corecontract.ErrorResponse{Code: "internal_error", Message: "v2 egress credential resolution failed"})
		return
	}
	status := resolved.Status
	if status < 400 || status > 599 {
		status = http.StatusServiceUnavailable
	}
	code := resolved.Code
	if code == corecredentials.ReasonCredentialNotConfigured {
		status = http.StatusNotFound
	} else if code == corecredentials.ReasonCredentialUnauthorized || code == corecredentials.ReasonCredentialRevoked {
		status = http.StatusForbidden
	} else if code == corecredentials.ReasonCredentialInvalid {
		status = http.StatusBadRequest
	}
	// Do not return ResolveError.Error(): it can include provider adapter text.
	message := "v2 egress credential request was denied"
	if status >= 500 {
		message = "v2 egress credential resolver is temporarily unavailable"
	}
	writeError(response, status, corecontract.ErrorResponse{Code: code, Message: message})
}

var _ interface{ http.Handler } = (*EgressCredentialHandler)(nil)
