package coreserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const maxManagedSandboxCommandBytes int64 = 256 * 1024

type ManagedSandboxCommands interface {
	ReserveManagedSandbox(context.Context, corecontract.ReserveManagedSandboxRequest) (corecontract.ReserveManagedSandboxResponse, error)
	GetManagedSandbox(context.Context, string, int64) (corecontract.GetManagedSandboxResponse, error)
	BeginManagedSandboxCreate(context.Context, corecontract.BeginManagedSandboxCreateRequest) (corecontract.ManagedSandboxMutationResponse, error)
	ObserveManagedSandbox(context.Context, corecontract.ObserveManagedSandboxRequest) (corecontract.ManagedSandboxMutationResponse, error)
	RenewManagedSandboxActivity(context.Context, corecontract.RenewManagedSandboxActivityRequest) (corecontract.ManagedSandboxMutationResponse, error)
	ReleaseManagedSandboxActivity(context.Context, corecontract.ReleaseManagedSandboxActivityRequest) (corecontract.ManagedSandboxMutationResponse, error)
	BeginManagedSandboxDelete(context.Context, corecontract.BeginManagedSandboxDeleteRequest) (corecontract.ManagedSandboxMutationResponse, error)
	ListManagedSandboxesForReconcile(context.Context, corecontract.ListManagedSandboxesForReconcileRequest) (corecontract.ListManagedSandboxesForReconcileResponse, error)
	AuthorizeManagedSandboxOperation(context.Context, corecontract.AuthorizeManagedSandboxOperationRequest) (corecontract.AuthorizeManagedSandboxOperationResponse, error)
}

type ManagedSandboxHandler struct {
	authorizer WorkloadAuthorizer
	commands   ManagedSandboxCommands
}

func NewManagedSandboxHandler(authorizer WorkloadAuthorizer, commands ManagedSandboxCommands) (*ManagedSandboxHandler, error) {
	if authorizer == nil {
		return nil, errors.New("workload authorizer is required")
	}
	if commands == nil {
		return nil, errors.New("managed sandbox commands are required")
	}
	return &ManagedSandboxHandler{authorizer: authorizer, commands: commands}, nil
}

func (handler *ManagedSandboxHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path == corecontract.AuthorizeManagedSandboxOperationPath && request.Method == http.MethodPost {
		handler.authorizeOperation(response, request)
		return
	}
	if request.URL.Path == corecontract.ReserveManagedSandboxPath && request.Method == http.MethodPost {
		handler.reserve(response, request)
		return
	}
	if request.URL.Path == corecontract.ListManagedSandboxesForReconcilePath && request.Method == http.MethodPost {
		handler.listReconcile(response, request)
		return
	}
	sandboxID, action, ok := parseManagedSandboxAction(request.URL.Path)
	if !ok {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "managed sandbox endpoint not found"})
		return
	}
	if action == "get" && request.Method == http.MethodGet {
		handler.get(response, request, sandboxID)
		return
	}
	if request.Method != http.MethodPost {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "managed sandbox endpoint not found"})
		return
	}
	switch action {
	case "begin-create":
		handler.beginCreate(response, request, sandboxID)
	case "observe":
		handler.observe(response, request, sandboxID)
	case "renew-activity":
		handler.renewActivity(response, request, sandboxID)
	case "release-activity":
		handler.releaseActivity(response, request, sandboxID)
	case "begin-delete":
		handler.beginDelete(response, request, sandboxID)
	default:
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "managed sandbox endpoint not found"})
	}
}

func (handler *ManagedSandboxHandler) authorizeOperation(response http.ResponseWriter, request *http.Request) {
	if !handler.authorize(response, request, "managed-sandboxes.authorize-operation") {
		return
	}
	var command corecontract.AuthorizeManagedSandboxOperationRequest
	if !decodeCommandWithLimit(response, request, &command, maxManagedSandboxCommandBytes) {
		return
	}
	result, err := handler.commands.AuthorizeManagedSandboxOperation(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ManagedSandboxHandler) reserve(response http.ResponseWriter, request *http.Request) {
	if !handler.authorize(response, request, "managed-sandboxes.reserve") {
		return
	}
	var command corecontract.ReserveManagedSandboxRequest
	if !decodeCommandWithLimit(response, request, &command, maxManagedSandboxCommandBytes) {
		return
	}
	result, err := handler.commands.ReserveManagedSandbox(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ManagedSandboxHandler) get(response http.ResponseWriter, request *http.Request, sandboxID string) {
	if !handler.authorize(response, request, "managed-sandboxes.get") {
		return
	}
	generation, err := strconv.ParseInt(request.URL.Query().Get("generation"), 10, 64)
	if err != nil || generation < 1 {
		writeError(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "generation query must be a positive integer"})
		return
	}
	result, err := handler.commands.GetManagedSandbox(request.Context(), sandboxID, generation)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ManagedSandboxHandler) beginCreate(response http.ResponseWriter, request *http.Request, sandboxID string) {
	if !handler.authorize(response, request, "managed-sandboxes.begin-create") {
		return
	}
	var command corecontract.BeginManagedSandboxCreateRequest
	if !decodeManagedSandboxPathCommand(response, request, sandboxID, &command, func() string { return command.SandboxID }) {
		return
	}
	result, err := handler.commands.BeginManagedSandboxCreate(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ManagedSandboxHandler) observe(response http.ResponseWriter, request *http.Request, sandboxID string) {
	if !handler.authorize(response, request, "managed-sandboxes.observe") {
		return
	}
	var command corecontract.ObserveManagedSandboxRequest
	if !decodeManagedSandboxPathCommand(response, request, sandboxID, &command, func() string { return command.SandboxID }) {
		return
	}
	result, err := handler.commands.ObserveManagedSandbox(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ManagedSandboxHandler) renewActivity(response http.ResponseWriter, request *http.Request, sandboxID string) {
	if !handler.authorize(response, request, "managed-sandboxes.renew-activity") {
		return
	}
	var command corecontract.RenewManagedSandboxActivityRequest
	if !decodeManagedSandboxPathCommand(response, request, sandboxID, &command, func() string { return command.SandboxID }) {
		return
	}
	result, err := handler.commands.RenewManagedSandboxActivity(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ManagedSandboxHandler) releaseActivity(response http.ResponseWriter, request *http.Request, sandboxID string) {
	if !handler.authorize(response, request, "managed-sandboxes.release-activity") {
		return
	}
	var command corecontract.ReleaseManagedSandboxActivityRequest
	if !decodeManagedSandboxPathCommand(response, request, sandboxID, &command, func() string { return command.SandboxID }) {
		return
	}
	result, err := handler.commands.ReleaseManagedSandboxActivity(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ManagedSandboxHandler) beginDelete(response http.ResponseWriter, request *http.Request, sandboxID string) {
	if !handler.authorize(response, request, "managed-sandboxes.begin-delete") {
		return
	}
	var command corecontract.BeginManagedSandboxDeleteRequest
	if !decodeManagedSandboxPathCommand(response, request, sandboxID, &command, func() string { return command.SandboxID }) {
		return
	}
	result, err := handler.commands.BeginManagedSandboxDelete(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ManagedSandboxHandler) listReconcile(response http.ResponseWriter, request *http.Request) {
	if !handler.authorize(response, request, "managed-sandboxes.reconcile") {
		return
	}
	var command corecontract.ListManagedSandboxesForReconcileRequest
	if !decodeCommandWithLimit(response, request, &command, maxManagedSandboxCommandBytes) {
		return
	}
	result, err := handler.commands.ListManagedSandboxesForReconcile(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ManagedSandboxHandler) authorize(response http.ResponseWriter, request *http.Request, action string) bool {
	if err := handler.authorizer.AuthorizeWorkload(request, action); err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "workload is not authorized for managed sandbox command"})
		return false
	}
	return true
}

func parseManagedSandboxAction(value string) (sandboxID, action string, ok bool) {
	if !strings.HasPrefix(value, corecontract.ManagedSandboxPathPrefix) {
		return "", "", false
	}
	remainder := strings.TrimPrefix(value, corecontract.ManagedSandboxPathPrefix)
	if remainder == "" || strings.Contains(remainder, "/") {
		return "", "", false
	}
	separator := strings.LastIndexByte(remainder, ':')
	if separator < 0 {
		return remainder, "get", true
	}
	if separator == 0 || separator == len(remainder)-1 {
		return "", "", false
	}
	return remainder[:separator], remainder[separator+1:], true
}

func decodeManagedSandboxPathCommand(response http.ResponseWriter, request *http.Request, sandboxID string, destination any, bodyID func() string) bool {
	if !decodeCommandWithLimit(response, request, destination, maxManagedSandboxCommandBytes) {
		return false
	}
	if bodyID() != sandboxID {
		writePathIdentityError(response, "sandboxId")
		return false
	}
	return true
}
