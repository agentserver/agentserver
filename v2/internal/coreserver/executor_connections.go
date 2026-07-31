package coreserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

const maxInternalCommandBytes = 512 * 1024

type WorkloadAuthorizer interface {
	AuthorizeWorkload(*http.Request, string) error
}

type ExecutorConnectionCommands interface {
	AcquireExecutorConnection(context.Context, corecontract.AcquireExecutorConnectionRequest) (corecontract.ConnectionHolder, error)
	RenewExecutorConnection(context.Context, corecontract.RenewExecutorConnectionRequest) (corecontract.ConnectionHolder, error)
	ActivateExecutorConnection(context.Context, corecontract.ActivateExecutorConnectionRequest) (corecontract.ConnectionHolder, error)
	FenceExecutorConnection(context.Context, corecontract.FenceExecutorConnectionRequest) error
}

type ExecutorConnectionHandler struct {
	authorizer WorkloadAuthorizer
	commands   ExecutorConnectionCommands
}

func NewExecutorConnectionHandler(authorizer WorkloadAuthorizer, commands ExecutorConnectionCommands) (*ExecutorConnectionHandler, error) {
	if authorizer == nil {
		return nil, errors.New("workload authorizer is required")
	}
	if commands == nil {
		return nil, errors.New("executor connection commands are required")
	}
	return &ExecutorConnectionHandler{authorizer: authorizer, commands: commands}, nil
}

func (handler *ExecutorConnectionHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal command endpoint not found"})
		return
	}
	if request.URL.Path == corecontract.AcquireExecutorConnectionPath {
		handler.acquire(response, request)
		return
	}
	executorID, action, ok := parseExecutorConnectionAction(request.URL.Path)
	if !ok {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal command endpoint not found"})
		return
	}
	switch action {
	case "renew":
		handler.renew(response, request, executorID)
	case "activate":
		handler.activate(response, request, executorID)
	case "fence":
		handler.fence(response, request, executorID)
	default:
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal command endpoint not found"})
	}
}

func (handler *ExecutorConnectionHandler) acquire(response http.ResponseWriter, request *http.Request) {
	if !handler.authorize(response, request, "executor-connections.acquire") {
		return
	}
	var command corecontract.AcquireExecutorConnectionRequest
	if !decodeCommand(response, request, &command) {
		return
	}
	holder, err := handler.commands.AcquireExecutorConnection(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, corecontract.ExecutorConnectionResponse{Holder: holder})
}

func (handler *ExecutorConnectionHandler) renew(response http.ResponseWriter, request *http.Request, executorID string) {
	if !handler.authorize(response, request, "executor-connections.renew") {
		return
	}
	var command corecontract.RenewExecutorConnectionRequest
	if !decodeCommand(response, request, &command) {
		return
	}
	if command.Holder.ExecutorID != executorID {
		writeError(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "path executorId does not match holder"})
		return
	}
	holder, err := handler.commands.RenewExecutorConnection(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, corecontract.ExecutorConnectionResponse{Holder: holder})
}

func (handler *ExecutorConnectionHandler) activate(response http.ResponseWriter, request *http.Request, executorID string) {
	if !handler.authorize(response, request, "executor-connections.activate") {
		return
	}
	var command corecontract.ActivateExecutorConnectionRequest
	if !decodeCommand(response, request, &command) {
		return
	}
	if command.Holder.ExecutorID != executorID {
		writeError(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "path executorId does not match holder"})
		return
	}
	holder, err := handler.commands.ActivateExecutorConnection(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, corecontract.ExecutorConnectionResponse{Holder: holder})
}

func (handler *ExecutorConnectionHandler) fence(response http.ResponseWriter, request *http.Request, executorID string) {
	if !handler.authorize(response, request, "executor-connections.fence") {
		return
	}
	var command corecontract.FenceExecutorConnectionRequest
	if !decodeCommand(response, request, &command) {
		return
	}
	if command.Holder.ExecutorID != executorID {
		writeError(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "path executorId does not match holder"})
		return
	}
	if err := handler.commands.FenceExecutorConnection(request.Context(), command); err != nil {
		writeCommandError(response, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (handler *ExecutorConnectionHandler) authorize(response http.ResponseWriter, request *http.Request, action string) bool {
	if err := handler.authorizer.AuthorizeWorkload(request, action); err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "workload is not authorized for this command"})
		return false
	}
	return true
}

func parseExecutorConnectionAction(path string) (string, string, bool) {
	if !strings.HasPrefix(path, corecontract.ExecutorConnectionPathPrefix) {
		return "", "", false
	}
	remainder := strings.TrimPrefix(path, corecontract.ExecutorConnectionPathPrefix)
	if strings.Contains(remainder, "/") {
		return "", "", false
	}
	separator := strings.LastIndexByte(remainder, ':')
	if separator < 1 || separator == len(remainder)-1 {
		return "", "", false
	}
	return remainder[:separator], remainder[separator+1:], true
}

func decodeCommand(response http.ResponseWriter, request *http.Request, destination any) bool {
	return decodeCommandWithLimit(response, request, destination, maxInternalCommandBytes)
}

func decodeCommandWithLimit(response http.ResponseWriter, request *http.Request, destination any, maximumBytes int64) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(response, http.StatusUnsupportedMediaType, corecontract.ErrorResponse{Code: "invalid_argument", Message: "Content-Type must be application/json"})
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "request body is not a valid command"})
		return false
	}
	if err := finishJSON(decoder); err != nil {
		writeError(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "request body contains trailing data"})
		return false
	}
	return true
}

func finishJSON(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("additional JSON value")
	}
	return err
}

func writeCommandError(response http.ResponseWriter, err error) {
	var stateError *coredb.StateError
	if !errors.As(err, &stateError) {
		writeError(response, http.StatusInternalServerError, corecontract.ErrorResponse{Code: "internal_error", Message: "internal core command failed"})
		return
	}
	status := http.StatusConflict
	switch stateError.Code {
	case coredb.ErrorInvalidArgument:
		status = http.StatusBadRequest
	case coredb.ErrorNotFound:
		status = http.StatusNotFound
	case coredb.ErrorDatabase:
		writeError(response, http.StatusInternalServerError, corecontract.ErrorResponse{Code: "internal_error", Message: "internal core command failed"})
		return
	}
	writeError(response, status, corecontract.ErrorResponse{
		Code:              string(stateError.Code),
		Message:           stateError.Message,
		CurrentVersion:    stateError.CurrentVersion,
		CurrentGeneration: stateError.CurrentGeneration,
	})
}

func writeError(response http.ResponseWriter, status int, value corecontract.ErrorResponse) {
	writeJSON(response, status, value)
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	encoder := json.NewEncoder(response)
	// Canonical JSON documents are carried as json.RawMessage in a few
	// internal commands. The default encoder rewrites <, >, &, U+2028, and
	// U+2029 inside RawMessage values, which would change their signed or
	// persisted byte representation. Internal JSON is never embedded in HTML.
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return
	}
}

func commandConversionError(operation, executorID string, err error) error {
	return &coredb.StateError{
		Code:       coredb.ErrorInvalidArgument,
		Operation:  operation,
		Resource:   "executor_connection",
		ResourceID: executorID,
		Message:    fmt.Sprintf("invalid internal command: %v", err),
	}
}
