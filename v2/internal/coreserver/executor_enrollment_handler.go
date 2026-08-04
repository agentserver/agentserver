package coreserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

type ExecutorManagementCommands interface {
	CreateExecutor(context.Context, string, string, string) (corecontract.CreateExecutorResourceResponse, error)
	ListExecutors(context.Context, string, string) (corecontract.ListExecutorResourcesResponse, error)
	ArchiveExecutor(context.Context, string, string, string) (corecontract.ArchiveExecutorResourceResponse, error)
	IssueEnrollmentToken(context.Context, string, string, string, string) (corecontract.IssueExecutorEnrollmentTokenResponse, error)
}

type UserExecutorManagementHandler struct {
	workload WorkloadAuthorizer
	users    UserTokenAuthorizer
	commands ExecutorManagementCommands
}

func NewUserExecutorManagementHandler(workload WorkloadAuthorizer, users UserTokenAuthorizer, commands ExecutorManagementCommands) (*UserExecutorManagementHandler, error) {
	if workload == nil || users == nil || commands == nil {
		return nil, errors.New("browser workload, user authorizer, and executor management commands are required")
	}
	return &UserExecutorManagementHandler{workload: workload, users: users, commands: commands}, nil
}

func (handler *UserExecutorManagementHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET "+corecontract.ExecutorManagementRoutePattern, handler)
	mux.Handle("POST "+corecontract.ExecutorManagementRoutePattern, handler)
	mux.Handle("POST "+corecontract.ExecutorEnrollmentTokenRoutePattern, handler)
	mux.Handle("DELETE "+corecontract.ExecutorEnrollmentTokenRoutePattern, handler)
	return mux
}

func (handler *UserExecutorManagementHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	workspaceID := request.PathValue("workspaceId")
	if action := request.PathValue("executorAction"); action != "" {
		if executorID, ok := strings.CutSuffix(action, ":enrollmentToken"); ok && executorID != "" && request.Method == http.MethodPost {
			handler.issueToken(response, request, workspaceID, executorID)
			return
		}
		if request.Method == http.MethodDelete && action != "" && !strings.Contains(action, ":") {
			handler.archive(response, request, workspaceID, action)
			return
		}
		writePublicRunError(response, http.StatusNotFound, "not_found", "executor management endpoint not found", "")
		return
	}
	if request.Method == http.MethodGet {
		handler.list(response, request, workspaceID)
		return
	}
	handler.create(response, request, workspaceID)
}

func (handler *UserExecutorManagementHandler) list(response http.ResponseWriter, request *http.Request, workspaceID string) {
	actorID, ok := handler.authorize(response, request, "executors.list")
	if !ok {
		return
	}
	if request.URL.RawQuery != "" || request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "executor list requires an empty request without query parameters", "")
		return
	}
	result, err := handler.commands.ListExecutors(request.Context(), actorID, workspaceID)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *UserExecutorManagementHandler) create(response http.ResponseWriter, request *http.Request, workspaceID string) {
	actorID, ok := handler.authorize(response, request, "executors.create")
	if !ok {
		return
	}
	if request.URL.RawQuery != "" {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "executor creation does not accept query parameters", "")
		return
	}
	var input corecontract.CreateExecutorResourceRequest
	if !decodeExecutorManagementJSON(response, request, &input) {
		return
	}
	result, err := handler.commands.CreateExecutor(request.Context(), actorID, workspaceID, input.ExecutorID)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(response, status, result)
}

func (handler *UserExecutorManagementHandler) issueToken(response http.ResponseWriter, request *http.Request, workspaceID, executorID string) {
	actorID, ok := handler.authorize(response, request, "executors.enrollment-token.issue")
	if !ok {
		return
	}
	if request.URL.RawQuery != "" || request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "enrollment token issuance requires an empty request", "")
		return
	}
	idempotencyKey, err := publicIdempotencyKey(request.Header)
	if err != nil {
		writePublicRunError(response, http.StatusBadRequest, "invalid_idempotency_key", err.Error(), "")
		return
	}
	result, err := handler.commands.IssueEnrollmentToken(request.Context(), actorID, workspaceID, executorID, idempotencyKey)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(response, status, result)
}

func (handler *UserExecutorManagementHandler) archive(response http.ResponseWriter, request *http.Request, workspaceID, executorID string) {
	actorID, ok := handler.authorize(response, request, "executors.archive")
	if !ok {
		return
	}
	if request.URL.RawQuery != "" || request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "executor archive requires an empty request without query parameters", "")
		return
	}
	result, err := handler.commands.ArchiveExecutor(request.Context(), actorID, workspaceID, executorID)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *UserExecutorManagementHandler) authorize(response http.ResponseWriter, request *http.Request, action string) (string, bool) {
	if err := handler.workload.AuthorizeWorkload(request, action); err != nil {
		writePublicRunError(response, http.StatusForbidden, "forbidden", "calling workload is not authorized", "")
		return "", false
	}
	actorID, err := handler.users.AuthorizeUser(request, action)
	if err == nil {
		return actorID, true
	}
	if errors.Is(err, ErrUserAuthUnavailable) {
		writePublicRunError(response, http.StatusServiceUnavailable, "authorization_unavailable", "user authorization is temporarily unavailable", "")
		return "", false
	}
	response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-platform-api"`)
	writePublicRunError(response, http.StatusUnauthorized, "unauthorized", "a valid agentserver-platform-api access token is required", "")
	return "", false
}

func (handler *UserExecutorManagementHandler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	if request.Context().Err() != nil {
		return
	}
	var stateError *coredb.StateError
	if !errors.As(err, &stateError) || stateError.Code == coredb.ErrorDatabase {
		writePublicRunError(response, http.StatusInternalServerError, "internal_error", "core could not complete executor management", "")
		return
	}
	status := http.StatusConflict
	switch stateError.Code {
	case coredb.ErrorInvalidArgument:
		status = http.StatusBadRequest
	case coredb.ErrorForbidden:
		status = http.StatusForbidden
	case coredb.ErrorNotFound:
		status = http.StatusNotFound
	}
	writePublicRunError(response, status, string(stateError.Code), stateError.Message, "")
}

func decodeExecutorManagementJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writePublicRunError(response, http.StatusUnsupportedMediaType, "invalid_argument", "Content-Type must be application/json", "")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, 64*1024)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "request body is not valid executor management JSON", "")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "request body contains trailing data", "")
		return false
	}
	return true
}

type InternalExecutorEnrollmentCommands interface {
	CompleteEnrollment(context.Context, string, string, corecontract.CompleteExecutorEnrollmentRequest) (corecontract.CompleteExecutorEnrollmentResponse, error)
}

type InternalExecutorConnectionAuthorizer interface {
	Authorize(context.Context, string) (corecontract.AuthorizeExecutorConnectionResponse, error)
}

type InternalExecutorIdentityHandler struct {
	workload    WorkloadAuthorizer
	enrollment  InternalExecutorEnrollmentCommands
	connections InternalExecutorConnectionAuthorizer
}

func NewInternalExecutorIdentityHandler(workload WorkloadAuthorizer, enrollment InternalExecutorEnrollmentCommands, connections InternalExecutorConnectionAuthorizer) (*InternalExecutorIdentityHandler, error) {
	if workload == nil || enrollment == nil || connections == nil {
		return nil, errors.New("executor-gateway workload, enrollment service, and connection authorizer are required")
	}
	return &InternalExecutorIdentityHandler{workload: workload, enrollment: enrollment, connections: connections}, nil
}

func (handler *InternalExecutorIdentityHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if request.Method != http.MethodPost || request.URL.RawQuery != "" {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "executor identity endpoint not found"})
		return
	}
	switch request.URL.Path {
	case corecontract.CompleteExecutorEnrollmentPath:
		handler.complete(response, request)
	case corecontract.AuthorizeExecutorConnectionPath:
		handler.authorizeConnection(response, request)
	default:
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "executor identity endpoint not found"})
	}
}

func (handler *InternalExecutorIdentityHandler) complete(response http.ResponseWriter, request *http.Request) {
	if !handler.authorizeWorkload(response, request, "executor-enrollments.complete") {
		return
	}
	bearer, err := exactUserBearer(request.Header)
	if err != nil {
		response.Header().Set("WWW-Authenticate", `Bearer realm="executor-enrollment"`)
		writeError(response, http.StatusUnauthorized, corecontract.ErrorResponse{Code: "unauthorized", Message: "a valid executor enrollment bearer is required"})
		return
	}
	expectedExecutorID, err := exactExpectedExecutorID(request.Header)
	if err != nil {
		writeError(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "a valid gateway executor binding is required"})
		return
	}
	var command corecontract.CompleteExecutorEnrollmentRequest
	if !decodeExecutorEnrollmentJSON(response, request, &command, 512*1024) {
		return
	}
	result, err := handler.enrollment.CompleteEnrollment(request.Context(), bearer, expectedExecutorID, command)
	if err != nil {
		writeExecutorIdentityError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func exactExpectedExecutorID(header http.Header) (string, error) {
	values := header.Values(corecontract.ExpectedExecutorIDHeader)
	if len(values) != 1 || !canonicalPublicUUID(values[0]) {
		return "", errors.New("expected executor ID header is invalid")
	}
	return values[0], nil
}

func decodeExecutorEnrollmentJSON(response http.ResponseWriter, request *http.Request, destination any, maximumBytes int64) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(response, http.StatusUnsupportedMediaType, corecontract.ErrorResponse{Code: "invalid_argument", Message: "Content-Type must be application/json"})
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumBytes)
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		writeError(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "request body is not a valid enrollment command"})
		return false
	}
	limits := braincatalog.DefaultLimits()
	value, canonical, err := braincatalog.DecodeCanonicalJSON(raw, int(maximumBytes), limits)
	if err != nil {
		writeError(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "request body is not a canonical closed-world enrollment command"})
		return false
	}
	if _, ok := value.(map[string]any); !ok {
		writeError(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "enrollment command must be a JSON object"})
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "request body is not a valid enrollment command"})
		return false
	}
	return true
}

func (handler *InternalExecutorIdentityHandler) authorizeConnection(response http.ResponseWriter, request *http.Request) {
	if !handler.authorizeWorkload(response, request, "executor-connections.authorize") {
		return
	}
	if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		writeError(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "executor connection authorization requires an empty request"})
		return
	}
	bearer, err := exactUserBearer(request.Header)
	if err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "executor OAuth token is not authorized"})
		return
	}
	result, err := handler.connections.Authorize(request.Context(), bearer)
	if err != nil {
		writeExecutorIdentityError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *InternalExecutorIdentityHandler) authorizeWorkload(response http.ResponseWriter, request *http.Request, action string) bool {
	if err := handler.workload.AuthorizeWorkload(request, action); err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "workload is not authorized for executor identity"})
		return false
	}
	return true
}

func writeExecutorIdentityError(response http.ResponseWriter, request *http.Request, err error) {
	if request.Context().Err() != nil {
		return
	}
	var stateError *coredb.StateError
	if !errors.As(err, &stateError) || stateError.Code == coredb.ErrorDatabase {
		writeError(response, http.StatusServiceUnavailable, corecontract.ErrorResponse{Code: "unavailable", Message: "executor identity authority is unavailable"})
		return
	}
	status := http.StatusConflict
	message := "executor identity state conflicts with the request"
	switch stateError.Code {
	case coredb.ErrorInvalidArgument:
		status = http.StatusBadRequest
		message = "executor identity request is invalid"
	case coredb.ErrorForbidden:
		status = http.StatusForbidden
		message = "executor identity authority rejected the request"
	case coredb.ErrorNotFound:
		status = http.StatusNotFound
		message = "executor identity authority was not found"
	}
	writeError(response, status, corecontract.ErrorResponse{Code: string(stateError.Code), Message: message})
}
