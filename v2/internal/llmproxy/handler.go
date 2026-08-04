package llmproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/agentserver/agentserver/v2/internal/publichttps"
)

const (
	ResponsesPath              = "/v1/responses"
	defaultMaximumRequestSize  = int64(64 * 1024 * 1024)
	defaultMaximumResponseSize = int64(64 * 1024 * 1024)
)

type ModelRequestAuthenticator interface {
	AuthenticateModelRequest(*http.Request, string) (Principal, error)
}

type HandlerConfig struct {
	Authenticator        ModelRequestAuthenticator
	HTTPClient           *http.Client
	MaximumRequestBytes  int64
	MaximumResponseBytes int64
	Now                  func() time.Time
	Logger               *slog.Logger
}

// Handler is a closed Responses API adapter. It authenticates and live-
// authorizes the complete model route before obtaining an upstream credential,
// replaces the run bearer rather than forwarding it, and never follows an
// upstream redirect.
type Handler struct {
	authenticator ModelRequestAuthenticator
	client        *http.Client
	maxRequest    int64
	maxResponse   int64
	now           func() time.Time
	logger        *slog.Logger
}

func NewHandler(config HandlerConfig) (*Handler, error) {
	if config.Authenticator == nil || config.HTTPClient == nil {
		return nil, errors.New("llmproxy authenticator and upstream HTTP client are required")
	}
	if config.MaximumRequestBytes == 0 {
		config.MaximumRequestBytes = defaultMaximumRequestSize
	}
	if config.MaximumResponseBytes == 0 {
		config.MaximumResponseBytes = defaultMaximumResponseSize
	}
	if config.MaximumRequestBytes < 1 || config.MaximumRequestBytes > defaultMaximumRequestSize ||
		config.MaximumResponseBytes < 1 || config.MaximumResponseBytes > defaultMaximumResponseSize {
		return nil, errors.New("llmproxy request and response bounds must be positive and at most 64 MiB")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	clientCopy := *config.HTTPClient
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Handler{
		authenticator: config.Authenticator, client: &clientCopy,
		maxRequest: config.MaximumRequestBytes, maxResponse: config.MaximumResponseBytes,
		now: config.Now, logger: config.Logger,
	}, nil
}

func (handler *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	setNoStore(response.Header())
	if handler == nil || handler.authenticator == nil || handler.client == nil || handler.now == nil {
		writeProxyError(response, http.StatusServiceUnavailable, "service_unavailable")
		return
	}
	if request.TLS == nil {
		writeProxyError(response, http.StatusBadRequest, "tls_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeProxyError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if request.URL.Path != ResponsesPath || request.URL.RawPath != "" || request.URL.RawQuery != "" {
		writeProxyError(response, http.StatusNotFound, "not_found")
		return
	}
	if !validModelRequestMediaType(request.Header) {
		writeProxyError(response, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return
	}
	body, model, err := readResponsesRequest(response, request, handler.maxRequest)
	if err != nil {
		writeProxyError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	principal, err := handler.authenticator.AuthenticateModelRequest(request, model)
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			handler.logDisposition("local_authentication", http.StatusUnauthorized)
			response.Header().Set("WWW-Authenticate", `Bearer realm="llmproxy"`)
			writeProxyError(response, http.StatusUnauthorized, "unauthenticated")
			return
		}
		handler.logDisposition("core_live_authorization", http.StatusForbidden)
		writeProxyError(response, http.StatusForbidden, "forbidden")
		return
	}
	if principal.RunDeadline.IsZero() || !handler.now().UTC().Before(principal.RunDeadline) {
		writeProxyError(response, http.StatusForbidden, "run_deadline_exceeded")
		return
	}
	operationContext, cancelOperation := context.WithDeadline(request.Context(), principal.RunDeadline)
	defer cancelOperation()
	upstream, err := publichttps.ValidateURL(principal.ResponsesURL, ResponsesPath)
	if err != nil || !validUpstreamAuthorization(principal.UpstreamAuthorization) ||
		principal.BearerExpiresAt.IsZero() || !handler.now().UTC().Before(principal.BearerExpiresAt) {
		handler.logDisposition("resolved_credential_validation", http.StatusServiceUnavailable)
		writeProxyError(response, http.StatusServiceUnavailable, "credential_unavailable")
		return
	}
	upstreamRequest, err := http.NewRequestWithContext(
		operationContext, http.MethodPost, upstream.String(), bytes.NewReader(body),
	)
	if err != nil {
		writeProxyError(response, http.StatusBadGateway, "upstream_unavailable")
		return
	}
	copyModelRequestHeaders(upstreamRequest.Header, request.Header)
	upstreamRequest.Header.Set("Authorization", principal.UpstreamAuthorization)
	upstreamResponse, err := handler.client.Do(upstreamRequest)
	if err != nil {
		handler.logDisposition("upstream_transport", http.StatusBadGateway)
		writeProxyError(response, http.StatusBadGateway, "upstream_unavailable")
		return
	}
	defer upstreamResponse.Body.Close()
	if upstreamResponse.StatusCode >= 300 && upstreamResponse.StatusCode < 400 {
		handler.logDisposition("upstream_redirect", upstreamResponse.StatusCode)
		writeProxyError(response, http.StatusBadGateway, "upstream_redirect_rejected")
		return
	}
	if upstreamResponse.StatusCode >= http.StatusBadRequest {
		handler.logDisposition("upstream_response", upstreamResponse.StatusCode)
	}
	copyModelResponseHeaders(response.Header(), upstreamResponse.Header)
	setNoStore(response.Header())
	response.WriteHeader(upstreamResponse.StatusCode)
	writer := newFlushWriter(response)
	if _, copyErr := io.Copy(writer, io.LimitReader(upstreamResponse.Body, handler.maxResponse)); copyErr != nil {
		return
	}
	var trailing [1]byte
	if count, _ := upstreamResponse.Body.Read(trailing[:]); count != 0 {
		return
	}
}

// logDisposition deliberately records only a fixed stage and numeric status.
// Model input, route URLs, capabilities, provider credentials, response bodies,
// and request headers must never enter llmproxy logs.
func (handler *Handler) logDisposition(stage string, status int) {
	if handler != nil && handler.logger != nil {
		handler.logger.Warn("llmproxy model request did not complete", "stage", stage, "status", status)
	}
}

func readResponsesRequest(response http.ResponseWriter, request *http.Request, maximum int64) ([]byte, string, error) {
	request.Body = http.MaxBytesReader(response, request.Body, maximum)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, "", err
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxCatalogBytes = int(maximum)
	limits.MaxSchemaBytes = int(maximum)
	limits.MaxJSONValues = 1_048_576
	limits.MaxJSONDepth = 256
	value, _, err := braincatalog.DecodeCanonicalJSON(body, int(maximum), limits)
	if err != nil {
		return nil, "", err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, "", errors.New("Responses request root is not an object")
	}
	model, ok := object["model"].(string)
	if !ok || !validRouteText(model) {
		return nil, "", errors.New("Responses request model is invalid")
	}
	stream, ok := object["stream"].(bool)
	if !ok || !stream {
		return nil, "", errors.New("Responses request must enable streaming")
	}
	return body, model, nil
}

func validModelRequestMediaType(header http.Header) bool {
	values := header.Values("Content-Type")
	if len(values) != 1 {
		return false
	}
	mediaType, parameters, err := mime.ParseMediaType(values[0])
	return err == nil && mediaType == "application/json" && len(parameters) == 0 &&
		(header.Get("Content-Encoding") == "" || header.Get("Content-Encoding") == "identity") &&
		len(header.Values("Content-Encoding")) <= 1
}

func validUpstreamAuthorization(value string) bool {
	return strings.HasPrefix(value, "Bearer ") && len(value) > len("Bearer ") && len(value) <= 16*1024 &&
		strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00")
}

func copyModelRequestHeaders(destination, source http.Header) {
	for _, name := range []string{"Accept", "Content-Type", "OpenAI-Beta", "User-Agent"} {
		values := source.Values(name)
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}

func copyModelResponseHeaders(destination, source http.Header) {
	for _, name := range []string{"Content-Type", "Retry-After", "X-Request-Id", "OpenAI-Processing-Ms"} {
		values := source.Values(name)
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}

func setNoStore(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
}

func writeProxyError(response http.ResponseWriter, status int, code string) {
	setNoStore(response.Header())
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]any{
		"error": map[string]string{
			"message": "agentserver llmproxy request rejected",
			"type":    "agentserver_proxy_error", "code": code,
		},
	})
}

type flushWriter struct {
	writer  io.Writer
	flusher http.Flusher
}

func newFlushWriter(response http.ResponseWriter) io.Writer {
	flusher, ok := response.(http.Flusher)
	if !ok {
		return response
	}
	return flushWriter{writer: response, flusher: flusher}
}

func (writer flushWriter) Write(data []byte) (int, error) {
	written, err := writer.writer.Write(data)
	writer.flusher.Flush()
	return written, err
}
