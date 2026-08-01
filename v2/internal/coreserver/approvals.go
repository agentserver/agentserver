package coreserver

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

type ApprovalCommands interface {
	CreateApproval(context.Context, corecontract.CreateApprovalRequest) (corecontract.CreateApprovalResponse, error)
	ExpireApproval(context.Context, corecontract.ApprovalTerminalRequest) (corecontract.ApprovalTerminalResponse, error)
	CancelApproval(context.Context, corecontract.ApprovalTerminalRequest) (corecontract.ApprovalTerminalResponse, error)
	ConsumeApproval(context.Context, corecontract.ConsumeApprovalRequest) (corecontract.ConsumeApprovalResponse, error)
}

type ApprovalHandler struct {
	authorizer WorkloadAuthorizer
	commands   ApprovalCommands
}

func NewApprovalHandler(authorizer WorkloadAuthorizer, commands ApprovalCommands) (*ApprovalHandler, error) {
	if authorizer == nil || commands == nil {
		return nil, errors.New("approval workload authorizer and commands are required")
	}
	return &ApprovalHandler{authorizer: authorizer, commands: commands}, nil
}

func (handler *ApprovalHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal command endpoint not found"})
		return
	}
	if request.URL.Path == corecontract.CreateApprovalPath {
		handler.create(response, request)
		return
	}
	approvalID, action, ok := parseApprovalAction(request.URL.Path)
	if !ok {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal command endpoint not found"})
		return
	}
	switch action {
	case "expire":
		handler.expire(response, request, approvalID)
	case "cancel":
		handler.cancel(response, request, approvalID)
	case "consume":
		handler.consume(response, request, approvalID)
	default:
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal command endpoint not found"})
	}
}

func (handler *ApprovalHandler) create(response http.ResponseWriter, request *http.Request) {
	if !handler.authorize(response, request, "approvals.create") {
		return
	}
	var command corecontract.CreateApprovalRequest
	if !decodeCommandWithLimit(response, request, &command, maxExecutionCommandBytes) {
		return
	}
	result, err := handler.commands.CreateApproval(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ApprovalHandler) expire(response http.ResponseWriter, request *http.Request, approvalID string) {
	if !handler.authorize(response, request, "approvals.expire") {
		return
	}
	var command corecontract.ApprovalTerminalRequest
	if !decodeCommandWithLimit(response, request, &command, maxExecutionCommandBytes) || !approvalPathMatches(response, approvalID, command.ApprovalID) {
		return
	}
	result, err := handler.commands.ExpireApproval(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ApprovalHandler) cancel(response http.ResponseWriter, request *http.Request, approvalID string) {
	if !handler.authorize(response, request, "approvals.cancel") {
		return
	}
	var command corecontract.ApprovalTerminalRequest
	if !decodeCommandWithLimit(response, request, &command, maxExecutionCommandBytes) || !approvalPathMatches(response, approvalID, command.ApprovalID) {
		return
	}
	result, err := handler.commands.CancelApproval(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ApprovalHandler) consume(response http.ResponseWriter, request *http.Request, approvalID string) {
	if !handler.authorize(response, request, "approvals.consume") {
		return
	}
	var command corecontract.ConsumeApprovalRequest
	if !decodeCommandWithLimit(response, request, &command, maxExecutionCommandBytes) || !approvalPathMatches(response, approvalID, command.ApprovalID) {
		return
	}
	result, err := handler.commands.ConsumeApproval(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ApprovalHandler) authorize(response http.ResponseWriter, request *http.Request, action string) bool {
	if err := handler.authorizer.AuthorizeWorkload(request, action); err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "workload is not authorized for this command"})
		return false
	}
	return true
}

func parseApprovalAction(path string) (string, string, bool) {
	if !strings.HasPrefix(path, corecontract.ApprovalPathPrefix) {
		return "", "", false
	}
	remainder := strings.TrimPrefix(path, corecontract.ApprovalPathPrefix)
	separator := strings.LastIndexByte(remainder, ':')
	if separator < 1 || separator == len(remainder)-1 || strings.ContainsRune(remainder[:separator], '/') {
		return "", "", false
	}
	action := remainder[separator+1:]
	if action != "expire" && action != "cancel" && action != "consume" {
		return "", "", false
	}
	return remainder[:separator], action, true
}

func approvalPathMatches(response http.ResponseWriter, pathID, commandID string) bool {
	if pathID != commandID {
		writePathIdentityError(response, "approvalId")
		return false
	}
	return true
}
