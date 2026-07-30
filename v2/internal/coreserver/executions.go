package coreserver

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const maxExecutionCommandBytes int64 = 5 * 1024 * 1024

type ExecutionCommands interface {
	PrepareExecution(context.Context, corecontract.PrepareExecutionRequest) (corecontract.PrepareExecutionResponse, error)
	PrepareOperation(context.Context, corecontract.PrepareOperationRequest) (corecontract.PrepareOperationResponse, error)
	BeginOperationDispatch(context.Context, corecontract.BeginOperationDispatchRequest) (corecontract.BeginOperationDispatchResponse, error)
	AcknowledgeOperation(context.Context, corecontract.AcknowledgeOperationRequest) (corecontract.AcknowledgeOperationResponse, error)
	CompleteOperation(context.Context, corecontract.CompleteOperationRequest) (corecontract.CompleteOperationResponse, error)
	SkipOperation(context.Context, corecontract.SkipOperationRequest) (corecontract.SkipOperationResponse, error)
	CompleteExecution(context.Context, corecontract.CompleteExecutionRequest) (corecontract.CompleteExecutionResponse, error)
}

type ExecutionHandler struct {
	authorizer WorkloadAuthorizer
	commands   ExecutionCommands
}

func NewExecutionHandler(authorizer WorkloadAuthorizer, commands ExecutionCommands) (*ExecutionHandler, error) {
	if authorizer == nil {
		return nil, errors.New("workload authorizer is required")
	}
	if commands == nil {
		return nil, errors.New("execution commands are required")
	}
	return &ExecutionHandler{authorizer: authorizer, commands: commands}, nil
}

func (handler *ExecutionHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal command endpoint not found"})
		return
	}
	if request.URL.Path == corecontract.PrepareExecutionPath {
		handler.prepareExecution(response, request)
		return
	}
	executionID, operationID, action, ok := parseExecutionAction(request.URL.Path)
	if !ok {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal command endpoint not found"})
		return
	}
	switch action {
	case "prepare-operation":
		handler.prepareOperation(response, request, executionID)
	case "begin-dispatch":
		handler.beginOperationDispatch(response, request, executionID, operationID)
	case "acknowledge":
		handler.acknowledgeOperation(response, request, executionID, operationID)
	case "complete-operation":
		handler.completeOperation(response, request, executionID, operationID)
	case "skip-operation":
		handler.skipOperation(response, request, executionID, operationID)
	case "complete-execution":
		handler.completeExecution(response, request, executionID)
	default:
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal command endpoint not found"})
	}
}

func (handler *ExecutionHandler) prepareExecution(response http.ResponseWriter, request *http.Request) {
	if !handler.authorize(response, request, "executions.prepare") {
		return
	}
	var command corecontract.PrepareExecutionRequest
	if !decodeCommandWithLimit(response, request, &command, maxExecutionCommandBytes) {
		return
	}
	result, err := handler.commands.PrepareExecution(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ExecutionHandler) prepareOperation(response http.ResponseWriter, request *http.Request, executionID string) {
	if !handler.authorize(response, request, "execution-operations.prepare") {
		return
	}
	var command corecontract.PrepareOperationRequest
	if !decodeCommandWithLimit(response, request, &command, maxExecutionCommandBytes) {
		return
	}
	if command.ExecutionID != executionID {
		writePathIdentityError(response, "executionId")
		return
	}
	result, err := handler.commands.PrepareOperation(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ExecutionHandler) beginOperationDispatch(response http.ResponseWriter, request *http.Request, executionID, operationID string) {
	if !handler.authorize(response, request, "execution-operations.begin-dispatch") {
		return
	}
	var command corecontract.BeginOperationDispatchRequest
	if !decodeCommandWithLimit(response, request, &command, maxExecutionCommandBytes) {
		return
	}
	if !executionOperationPathMatches(response, executionID, operationID, command.ExecutionID, command.OperationID) {
		return
	}
	result, err := handler.commands.BeginOperationDispatch(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ExecutionHandler) acknowledgeOperation(response http.ResponseWriter, request *http.Request, executionID, operationID string) {
	if !handler.authorize(response, request, "execution-operations.acknowledge") {
		return
	}
	var command corecontract.AcknowledgeOperationRequest
	if !decodeCommandWithLimit(response, request, &command, maxExecutionCommandBytes) {
		return
	}
	if !executionOperationPathMatches(response, executionID, operationID, command.ExecutionID, command.OperationID) {
		return
	}
	result, err := handler.commands.AcknowledgeOperation(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ExecutionHandler) completeOperation(response http.ResponseWriter, request *http.Request, executionID, operationID string) {
	if !handler.authorize(response, request, "execution-operations.complete") {
		return
	}
	var command corecontract.CompleteOperationRequest
	if !decodeCommandWithLimit(response, request, &command, maxExecutionCommandBytes) {
		return
	}
	if !executionOperationPathMatches(response, executionID, operationID, command.ExecutionID, command.OperationID) {
		return
	}
	result, err := handler.commands.CompleteOperation(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ExecutionHandler) skipOperation(response http.ResponseWriter, request *http.Request, executionID, operationID string) {
	if !handler.authorize(response, request, "execution-operations.skip") {
		return
	}
	var command corecontract.SkipOperationRequest
	if !decodeCommandWithLimit(response, request, &command, maxExecutionCommandBytes) {
		return
	}
	if !executionOperationPathMatches(response, executionID, operationID, command.ExecutionID, command.OperationID) {
		return
	}
	result, err := handler.commands.SkipOperation(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ExecutionHandler) completeExecution(response http.ResponseWriter, request *http.Request, executionID string) {
	if !handler.authorize(response, request, "executions.complete") {
		return
	}
	var command corecontract.CompleteExecutionRequest
	if !decodeCommandWithLimit(response, request, &command, maxExecutionCommandBytes) {
		return
	}
	if command.ExecutionID != executionID {
		writePathIdentityError(response, "executionId")
		return
	}
	result, err := handler.commands.CompleteExecution(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ExecutionHandler) authorize(response http.ResponseWriter, request *http.Request, action string) bool {
	if err := handler.authorizer.AuthorizeWorkload(request, action); err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "workload is not authorized for this command"})
		return false
	}
	return true
}

func parseExecutionAction(path string) (executionID, operationID, action string, ok bool) {
	if !strings.HasPrefix(path, corecontract.ExecutionPathPrefix) {
		return "", "", "", false
	}
	remainder := strings.TrimPrefix(path, corecontract.ExecutionPathPrefix)
	if remainder == "" {
		return "", "", "", false
	}
	if !strings.Contains(remainder, "/") {
		const suffix = ":complete"
		if !strings.HasSuffix(remainder, suffix) || len(remainder) == len(suffix) {
			return "", "", "", false
		}
		return strings.TrimSuffix(remainder, suffix), "", "complete-execution", true
	}
	parts := strings.Split(remainder, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] == "operations:prepare" {
		return parts[0], "", "prepare-operation", true
	}
	if len(parts) != 3 || parts[0] == "" || parts[1] != "operations" {
		return "", "", "", false
	}
	separator := strings.LastIndexByte(parts[2], ':')
	if separator < 1 || separator == len(parts[2])-1 {
		return "", "", "", false
	}
	operationID = parts[2][:separator]
	switch parts[2][separator+1:] {
	case "begin-dispatch":
		action = "begin-dispatch"
	case "acknowledge":
		action = "acknowledge"
	case "complete":
		action = "complete-operation"
	case "skip":
		action = "skip-operation"
	default:
		return "", "", "", false
	}
	return parts[0], operationID, action, true
}

func executionOperationPathMatches(response http.ResponseWriter, pathExecutionID, pathOperationID, executionID, operationID string) bool {
	if pathExecutionID != executionID {
		writePathIdentityError(response, "executionId")
		return false
	}
	if pathOperationID != operationID {
		writePathIdentityError(response, "operationId")
		return false
	}
	return true
}

func writePathIdentityError(response http.ResponseWriter, field string) {
	writeError(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "path " + field + " does not match command"})
}
