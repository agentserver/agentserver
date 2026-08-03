package browsergateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const maxExecutorResourceRequestBytes = int64(64 * 1024)

type ExecutorResourceBackend interface {
	CreateExecutorResource(context.Context, string, string, corecontract.CreateExecutorResourceRequest) (corecontract.CreateExecutorResourceResponse, error)
	IssueExecutorEnrollmentToken(context.Context, string, string, string, string) (corecontract.IssueExecutorEnrollmentTokenResponse, error)
}

type ExecutorResourceHandlerConfig struct{}

type ExecutorResourceHandler struct {
	backend ExecutorResourceBackend
}

func NewExecutorResourceHandler(backend ExecutorResourceBackend, config ExecutorResourceHandlerConfig) (*ExecutorResourceHandler, error) {
	if backend == nil {
		return nil, errors.New("executor resource backend is required")
	}
	return &ExecutorResourceHandler{backend: backend}, nil
}

func (handler *ExecutorResourceHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST "+corecontract.ExecutorManagementRoutePattern, handler)
	mux.Handle("POST "+corecontract.ExecutorEnrollmentTokenRoutePattern, handler)
	return mux
}

func (handler *ExecutorResourceHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	workspaceID := request.PathValue("workspaceId")
	if err := validateCanonicalUUID("workspaceId", workspaceID); err != nil {
		writeHTTPError(response, http.StatusBadRequest, "invalid_scope", err.Error())
		return
	}
	if action := request.PathValue("executorAction"); action != "" {
		executorID, ok := strings.CutSuffix(action, ":enrollmentToken")
		if !ok || executorID == "" {
			writeHTTPError(response, http.StatusNotFound, "not_found", "executor resource endpoint not found")
			return
		}
		handler.issueToken(response, request, workspaceID, executorID)
		return
	}
	handler.create(response, request, workspaceID)
}

func (handler *ExecutorResourceHandler) create(response http.ResponseWriter, request *http.Request, workspaceID string) {
	if request.URL.RawQuery != "" {
		writeHTTPError(response, http.StatusBadRequest, "invalid_argument", "executor creation does not accept query parameters")
		return
	}
	bearer, ok := executorResourceBearer(response, request)
	if !ok {
		return
	}
	var input corecontract.CreateExecutorResourceRequest
	if !decodeExecutorResourceJSON(response, request, &input) {
		return
	}
	if err := validateCanonicalUUID("executorId", input.ExecutorID); err != nil {
		writeHTTPError(response, http.StatusBadRequest, "invalid_argument", err.Error())
		return
	}
	result, err := handler.backend.CreateExecutorResource(request.Context(), bearer, workspaceID, input)
	if err != nil {
		writeExecutorBackendError(response, request, err)
		return
	}
	if err := validateExecutorResourceState(result.Executor, workspaceID, input.ExecutorID); err != nil {
		writeHTTPError(response, http.StatusBadGateway, "backend_contract_error", "core returned an invalid executor resource")
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeExecutorResourceJSON(response, status, result)
}

func (handler *ExecutorResourceHandler) issueToken(response http.ResponseWriter, request *http.Request, workspaceID, executorID string) {
	if err := validateCanonicalUUID("executorId", executorID); err != nil {
		writeHTTPError(response, http.StatusBadRequest, "invalid_scope", err.Error())
		return
	}
	if request.URL.RawQuery != "" || request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		writeHTTPError(response, http.StatusBadRequest, "invalid_argument", "enrollment token issuance requires an empty request")
		return
	}
	bearer, ok := executorResourceBearer(response, request)
	if !ok {
		return
	}
	idempotencyKey, err := extractIdempotencyKey(request.Header)
	if err != nil {
		writeHTTPError(response, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	result, err := handler.backend.IssueExecutorEnrollmentToken(request.Context(), bearer, workspaceID, executorID, idempotencyKey)
	if err != nil {
		writeExecutorBackendError(response, request, err)
		return
	}
	// ExpiresAt is Core/DB authority. The browser gateway must not reject an
	// exact idempotent replay using its own (possibly skewed) wall clock; the
	// caller can request a replacement with a new idempotency key.
	if result.ExecutorID != executorID || !validEnrollmentBearer(result.Token) || result.ExpiresAt.IsZero() {
		writeHTTPError(response, http.StatusBadGateway, "backend_contract_error", "core returned an invalid enrollment token")
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeExecutorResourceJSON(response, status, result)
}

func executorResourceBearer(response http.ResponseWriter, request *http.Request) (string, bool) {
	bearer, err := extractBearer(request.Header)
	if err == nil {
		return bearer, true
	}
	response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-platform-api"`)
	writeHTTPError(response, http.StatusUnauthorized, "unauthorized", "a single user bearer token is required")
	return "", false
}

func decodeExecutorResourceJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeHTTPError(response, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maxExecutorResourceRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeHTTPError(response, http.StatusBadRequest, "invalid_argument", "request body is not valid executor resource JSON")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeHTTPError(response, http.StatusBadRequest, "invalid_argument", "request body contains trailing data")
		return false
	}
	return true
}

func validateExecutorResourceState(resource corecontract.ExecutorResourceState, workspaceID, executorID string) error {
	if resource.WorkspaceID != workspaceID || resource.ExecutorID != executorID ||
		resource.Version < 1 || resource.Version >= 1<<53-1 || resource.CreatedAt.IsZero() ||
		resource.UpdatedAt.IsZero() || resource.UpdatedAt.Before(resource.CreatedAt) {
		return errors.New("executor resource escaped its requested scope or contains invalid state")
	}
	switch resource.Status {
	case "enrolling", "offline", "online", "revoked":
		return nil
	default:
		return errors.New("executor resource status is invalid")
	}
}

func validEnrollmentBearer(token string) bool {
	if token == "" || len(token) > 8192 || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\x00\r\n") {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "asv2enr1" {
		return false
	}
	claims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(claims) == 0 || len(claims) > 4096 || base64.RawURLEncoding.EncodeToString(claims) != parts[1] {
		return false
	}
	mac, err := base64.RawURLEncoding.DecodeString(parts[2])
	return err == nil && len(mac) == 32 && base64.RawURLEncoding.EncodeToString(mac) == parts[2]
}

func writeExecutorBackendError(response http.ResponseWriter, request *http.Request, err error) {
	if request.Context().Err() != nil {
		return
	}
	var backendError *BackendHTTPError
	if errors.As(err, &backendError) && validExecutorBackendStatus(backendError.Status) {
		writeHTTPError(response, backendError.Status, backendError.Code, backendError.Message)
		return
	}
	writeHTTPError(response, http.StatusBadGateway, "backend_unavailable", "core executor resource API is unavailable")
}

func validExecutorBackendStatus(status int) bool {
	switch status {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusConflict, http.StatusServiceUnavailable:
		return true
	default:
		return false
	}
}

func writeExecutorResourceJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
