package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strings"
)

const (
	defaultMaxErrorBytes = int64(64 * 1024)
	defaultMaxSSEBytes   = 1024 * 1024
)

var sessionDNSLabelPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

var requestCorrelationIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)

var providerErrorCodePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,127}$`)

type HeaderSource interface {
	Headers(context.Context) (http.Header, error)
}

type HeaderSourceFunc func(context.Context) (http.Header, error)

func (function HeaderSourceFunc) Headers(ctx context.Context) (http.Header, error) {
	return function(ctx)
}

type EndpointResolver interface {
	Resolve(string) (*url.URL, error)
}

type EndpointResolverFunc func(string) (*url.URL, error)

func (function EndpointResolverFunc) Resolve(sessionID string) (*url.URL, error) {
	return function(sessionID)
}

func NewSandboxdEndpointResolver(domainSuffix string) (EndpointResolver, error) {
	domainSuffix = strings.TrimSpace(strings.ToLower(domainSuffix))
	parsed, err := url.Parse("https://controlplane." + domainSuffix)
	if err != nil || parsed.Hostname() == "" || parsed.Port() != "" || parsed.Hostname() != "controlplane."+domainSuffix ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return nil, errors.New("TAE sandboxd domain suffix is invalid")
	}
	return EndpointResolverFunc(func(sessionID string) (*url.URL, error) {
		if !sessionDNSLabelPattern.MatchString(sessionID) || strings.EqualFold(sessionID, "controlplane") {
			return nil, errors.New("TAE session ID is not a valid DNS label")
		}
		return &url.URL{Scheme: "https", Host: strings.ToLower(sessionID) + "." + domainSuffix}, nil
	}), nil
}

type HTTPDataPlaneConfig struct {
	Client        *http.Client
	Headers       HeaderSource
	Endpoint      EndpointResolver
	SandboxID     string
	RequireHTTPS  bool
	MaxErrorBytes int64
	MaxEventBytes int
}

type HTTPDataPlane struct {
	client        *http.Client
	headers       HeaderSource
	endpoint      EndpointResolver
	sandboxID     string
	requireHTTPS  bool
	maxErrorBytes int64
	maxEventBytes int
}

func NewHTTPDataPlane(config HTTPDataPlaneConfig) (*HTTPDataPlane, error) {
	if config.Client == nil || config.Client.Transport == nil || config.Headers == nil || config.Endpoint == nil {
		return nil, errors.New("TAE data-plane HTTP client, header source, and endpoint resolver are required")
	}
	if !sessionDNSLabelPattern.MatchString(config.SandboxID) || strings.ToLower(config.SandboxID) != config.SandboxID {
		return nil, errors.New("TAE data-plane terminal sandbox ID is invalid")
	}
	if config.Client.Timeout != 0 {
		return nil, errors.New("TAE streaming HTTP client must not have a client-wide timeout")
	}
	if config.MaxErrorBytes == 0 {
		config.MaxErrorBytes = defaultMaxErrorBytes
	}
	if config.MaxErrorBytes < 1024 || config.MaxErrorBytes > 1024*1024 {
		return nil, errors.New("TAE error response bound must be between 1KiB and 1MiB")
	}
	if config.MaxEventBytes == 0 {
		config.MaxEventBytes = defaultMaxSSEBytes
	}
	if config.MaxEventBytes < 1024 || config.MaxEventBytes > 4*1024*1024 {
		return nil, errors.New("TAE SSE event bound must be between 1KiB and 4MiB")
	}
	client := *config.Client
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPDataPlane{
		client: &client, headers: config.Headers, endpoint: config.Endpoint, sandboxID: config.SandboxID,
		requireHTTPS: config.RequireHTTPS, maxErrorBytes: config.MaxErrorBytes, maxEventBytes: config.MaxEventBytes,
	}, nil
}

func (dataPlane *HTTPDataPlane) StartProcess(ctx context.Context, sessionID string, input StartProcessInput) (EventStream, error) {
	if !requestCorrelationIDPattern.MatchString(input.RequestID) {
		return nil, &RequestError{Code: "bad_request", Cause: errors.New("TAE process request correlation ID is invalid")}
	}
	timeoutMilliseconds := input.Timeout.Milliseconds()
	if timeoutMilliseconds < 1 || timeoutMilliseconds > int64(^uint(0)>>1) {
		return nil, &RequestError{Code: "bad_request", Cause: errors.New("TAE process timeout is invalid")}
	}
	payload := map[string]any{
		"command": map[string]any{
			"path": input.Executable, "args": input.Arguments, "cwd": input.WorkingDirectory, "envs": input.Environment,
		},
		"timeout": int(timeoutMilliseconds), "non_stream": false,
	}
	return dataPlane.doSSE(ctx, sessionID, "/api/process/start", payload, input.RequestID)
}

func (dataPlane *HTTPDataPlane) ConnectProcess(ctx context.Context, sessionID string, pid int) (EventStream, error) {
	if pid < 1 {
		return nil, &RequestError{Code: "bad_request", Cause: errors.New("TAE process PID is invalid")}
	}
	return dataPlane.doSSE(ctx, sessionID, "/api/process/connect", map[string]any{"pid": pid}, "")
}

func (dataPlane *HTTPDataPlane) SignalProcess(ctx context.Context, sessionID string, pid, signal int) (string, error) {
	if pid < 1 || (signal != 2 && signal != 9 && signal != 15) {
		return "", &RequestError{Code: "bad_request", Cause: errors.New("TAE process signal is invalid")}
	}
	response, err := dataPlane.do(ctx, sessionID, http.MethodPost, "/api/process/signal", map[string]any{"pid": pid, "signal": signal}, "application/json", "")
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, dataPlane.maxErrorBytes))
	return providerRequestID(response.Header), nil
}

func (dataPlane *HTTPDataPlane) Stat(ctx context.Context, sessionID, path string) (FileInfo, string, error) {
	response, err := dataPlane.do(ctx, sessionID, http.MethodPost, "/api/fs/stat", map[string]any{"path": path}, "application/json", "")
	if err != nil {
		return FileInfo{}, "", err
	}
	defer response.Body.Close()
	var envelope struct {
		Entry struct {
			Type          string `json:"type"`
			Size          int64  `json:"size"`
			SymlinkTarget string `json:"symlink_target"`
		} `json:"entry"`
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, dataPlane.maxErrorBytes+1))
	if readErr != nil || int64(len(raw)) > dataPlane.maxErrorBytes || json.Unmarshal(raw, &envelope) != nil {
		return FileInfo{}, providerRequestID(response.Header), &RequestError{
			WroteRequest: true, StatusCode: response.StatusCode, Code: "invalid_response",
			RequestID: providerRequestID(response.Header), Cause: errors.New("TAE stat response was invalid"),
		}
	}
	return FileInfo{Type: envelope.Entry.Type, Size: envelope.Entry.Size, SymlinkTarget: envelope.Entry.SymlinkTarget}, providerRequestID(response.Header), nil
}

func (dataPlane *HTTPDataPlane) Download(ctx context.Context, sessionID, path string) (Download, error) {
	response, err := dataPlane.do(ctx, sessionID, http.MethodPost, "/api/fs/download", map[string]any{"path": path}, "application/octet-stream", "")
	if err != nil {
		return Download{}, err
	}
	return Download{Body: response.Body, ContentLength: response.ContentLength, RequestID: providerRequestID(response.Header)}, nil
}

func (dataPlane *HTTPDataPlane) doSSE(ctx context.Context, sessionID, requestPath string, payload any, requestID string) (EventStream, error) {
	response, err := dataPlane.do(ctx, sessionID, http.MethodPost, requestPath, payload, "text/event-stream", requestID)
	if err != nil {
		return nil, err
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if contentType != "text/event-stream" {
		response.Body.Close()
		return nil, &RequestError{
			WroteRequest: true, StatusCode: response.StatusCode, Code: "invalid_response",
			RequestID: responseRequestID(response.Header, requestID), Cause: errors.New("TAE process response was not an SSE stream"),
		}
	}
	return &sseStream{
		body: response.Body, scanner: newSSEScanner(response.Body, dataPlane.maxEventBytes),
		requestID: responseRequestID(response.Header, requestID), maxEventBytes: dataPlane.maxEventBytes,
	}, nil
}

func (dataPlane *HTTPDataPlane) do(ctx context.Context, sessionID, method, requestPath string, payload any, accept, correlationID string) (*http.Response, error) {
	if ctx == nil {
		return nil, &RequestError{Code: "bad_request", Cause: errors.New("TAE request context is required")}
	}
	endpoint, err := dataPlane.endpoint.Resolve(sessionID)
	if err != nil {
		return nil, &RequestError{Code: "bad_request", Cause: errors.New("TAE session endpoint is invalid")}
	}
	if err := validateEndpoint(endpoint, dataPlane.requireHTTPS); err != nil {
		return nil, &RequestError{Code: "bad_request", Cause: err}
	}
	endpoint = cloneURL(endpoint)
	endpoint.Path = requestPath
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, &RequestError{Code: "bad_request", Cause: errors.New("TAE request payload could not be encoded")}
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, &RequestError{Code: "bad_request", Cause: errors.New("TAE request could not be constructed")}
	}
	headers, err := dataPlane.headers.Headers(ctx)
	if err != nil {
		return nil, &RequestError{Code: "identity_unavailable", Cause: errors.New("TAE data-plane provider identity is unavailable")}
	}
	if err := copyIdentityHeaders(request.Header, headers); err != nil {
		return nil, &RequestError{Code: "identity_unavailable", Cause: errors.New("TAE data-plane provider identity header is invalid")}
	}
	forwardedPrefix, err := terminalForwardedPrefix(dataPlane.sandboxID, sessionID)
	if err != nil {
		return nil, &RequestError{Code: "bad_request", Cause: err}
	}
	request.Header.Set("X-Forwarded-Prefix", forwardedPrefix)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", accept)
	request.Header.Set("Cache-Control", "no-store")
	if correlationID != "" {
		if !requestCorrelationIDPattern.MatchString(correlationID) {
			return nil, &RequestError{Code: "bad_request", Cause: errors.New("TAE request correlation ID is invalid")}
		}
		// TAE documents x-tt-logid as the caller-supplied correlation header.
		// It is observability only; dispatch idempotency continues to be owned by
		// Core and a written request with no definitive response stays unknown.
		request.Header.Set("X-Tt-Logid", correlationID)
	}
	wroteRequest := false
	trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { wroteRequest = true }}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	response, err := dataPlane.client.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		code := "provider_unavailable"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = "request_timeout"
		}
		return nil, &RequestError{WroteRequest: wroteRequest, Code: code, Cause: errors.New("TAE transport failed")}
	}
	if response.StatusCode != http.StatusOK {
		boundedBody, _ := io.ReadAll(io.LimitReader(response.Body, dataPlane.maxErrorBytes+1))
		response.Body.Close()
		providerCode, bodyRequestID := responseErrorMetadata(boundedBody, dataPlane.maxErrorBytes)
		requestID := providerRequestID(response.Header)
		if requestID == "" {
			requestID = bodyRequestID
		}
		if requestID == "" && requestCorrelationIDPattern.MatchString(correlationID) {
			requestID = correlationID
		}
		if response.StatusCode == http.StatusUnauthorized {
			refreshRejectedProviderIdentity(dataPlane.headers, ctx, headers)
		}
		return nil, &RequestError{
			WroteRequest: wroteRequest, StatusCode: response.StatusCode,
			Code:         responseCode(response.StatusCode, boundedBody, dataPlane.maxErrorBytes),
			ProviderCode: providerCode, RequestID: requestID,
			Cause: errors.New("TAE returned a non-success response"),
		}
	}
	return response, nil
}

func responseErrorMetadata(body []byte, maximum int64) (string, string) {
	if len(body) == 0 || int64(len(body)) > maximum {
		return "", ""
	}
	var envelope struct {
		ErrorCode any    `json:"error_code"`
		LogID     string `json:"log_id"`
	}
	if decodeSingleJSON(body, &envelope) != nil {
		return "", ""
	}
	providerCode := strings.TrimSpace(fmt.Sprint(envelope.ErrorCode))
	if envelope.ErrorCode == nil || !providerErrorCodePattern.MatchString(providerCode) {
		providerCode = ""
	}
	requestID := strings.TrimSpace(envelope.LogID)
	if !requestCorrelationIDPattern.MatchString(requestID) {
		requestID = ""
	}
	return providerCode, requestID
}

func decodeSingleJSON(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON response contained trailing data")
		}
		return err
	}
	return nil
}

func terminalForwardedPrefix(sandboxID, sessionID string) (string, error) {
	if !sessionDNSLabelPattern.MatchString(sandboxID) || strings.ToLower(sandboxID) != sandboxID ||
		!sessionDNSLabelPattern.MatchString(sessionID) || strings.ToLower(sessionID) != sessionID {
		return "", errors.New("TAE terminal sandbox route identity is invalid")
	}
	return "/api/v1/terminal_sandbox/sandboxes/" + sandboxID + "/sessions/" + sessionID + "/bash_server", nil
}

func validateEndpoint(endpoint *url.URL, requireHTTPS bool) error {
	if endpoint == nil || endpoint.Host == "" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Opaque != "" ||
		endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.RawPath != "" || endpoint.Path != "" {
		return errors.New("TAE endpoint must be a canonical origin")
	}
	if requireHTTPS && endpoint.Scheme != "https" {
		return errors.New("TAE production endpoint must use HTTPS")
	}
	if endpoint.Scheme != "https" && endpoint.Scheme != "http" {
		return errors.New("TAE endpoint has an unsupported scheme")
	}
	return nil
}

func copyIdentityHeaders(destination, source http.Header) error {
	if source == nil {
		return errors.New("TAE identity header source returned no headers")
	}
	seenIdentity := false
	for name, values := range source {
		canonical := http.CanonicalHeaderKey(name)
		if canonical != "X-Zti-Token" && canonical != "X-Jwt-Token" {
			return fmt.Errorf("TAE identity header source returned unsupported header %s", canonical)
		}
		if len(values) != 1 || values[0] == "" || len(values[0]) > 64*1024 || strings.ContainsAny(values[0], "\r\n") {
			return errors.New("TAE identity header value is invalid")
		}
		if seenIdentity {
			return errors.New("TAE identity header source returned multiple provider identities")
		}
		destination.Set(canonical, values[0])
		seenIdentity = true
	}
	if !seenIdentity {
		return errors.New("TAE identity header source returned no identity")
	}
	return nil
}

func responseCode(status int, body []byte, maximum int64) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestTimeout:
		return "request_timeout"
	case http.StatusTooManyRequests:
		return "rate_limited"
	}
	if int64(len(body)) <= maximum {
		var envelope struct {
			ErrorCode any `json:"error_code"`
		}
		if json.Unmarshal(body, &envelope) == nil {
			value := strings.ToLower(fmt.Sprint(envelope.ErrorCode))
			if strings.Contains(value, "notfound") || strings.Contains(value, "not_found") {
				return "not_found"
			}
		}
	}
	if status >= 500 {
		return "provider_unavailable"
	}
	return "invalid_response"
}

func providerRequestID(headers http.Header) string {
	for _, name := range []string{"X-Tt-Logid", "X-Tt-Log-Id", "X-Request-Id", "X-Request-ID"} {
		if value := strings.TrimSpace(headers.Get(name)); value != "" && len(value) <= 1024 && !strings.ContainsAny(value, "\r\n") {
			return value
		}
	}
	return ""
}

func responseRequestID(headers http.Header, fallback string) string {
	if requestID := providerRequestID(headers); requestID != "" {
		return requestID
	}
	if requestCorrelationIDPattern.MatchString(fallback) {
		return fallback
	}
	return ""
}

func cloneURL(original *url.URL) *url.URL {
	clone := *original
	return &clone
}

type sseStream struct {
	body          io.ReadCloser
	scanner       *bufio.Scanner
	requestID     string
	maxEventBytes int
	closed        bool
}

func newSSEScanner(reader io.Reader, maximum int) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maximum+1)
	return scanner
}

func (stream *sseStream) Next(ctx context.Context) (StreamEvent, error) {
	if ctx == nil {
		return StreamEvent{}, errors.New("TAE SSE context is required")
	}
	if stream.closed {
		return StreamEvent{}, io.EOF
	}
	var name string
	var data strings.Builder
	for stream.scanner.Scan() {
		select {
		case <-ctx.Done():
			return StreamEvent{}, ctx.Err()
		default:
		}
		line := stream.scanner.Text()
		if line == "" {
			if data.Len() == 0 {
				name = ""
				continue
			}
			return decodeSSEEvent(name, data.String(), stream.requestID)
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if found && strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		switch field {
		case "event":
			name = value
		case "data":
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			if data.Len()+len(value) > stream.maxEventBytes {
				return StreamEvent{}, &RequestError{WroteRequest: true, Code: "invalid_response", RequestID: stream.requestID, Cause: errors.New("TAE SSE event exceeded the configured bound")}
			}
			data.WriteString(value)
		}
	}
	if err := stream.scanner.Err(); err != nil {
		return StreamEvent{}, &RequestError{WroteRequest: true, Code: "stream_lost", RequestID: stream.requestID, Cause: errors.New("TAE SSE stream read failed")}
	}
	if data.Len() > 0 {
		return decodeSSEEvent(name, data.String(), stream.requestID)
	}
	return StreamEvent{}, io.EOF
}

func decodeSSEEvent(name, payload, requestID string) (StreamEvent, error) {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	data := make(map[string]any)
	if err := decoder.Decode(&data); err != nil {
		return StreamEvent{}, &RequestError{WroteRequest: true, Code: "invalid_response", RequestID: requestID, Cause: errors.New("TAE SSE data was not a JSON object")}
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return StreamEvent{}, &RequestError{WroteRequest: true, Code: "invalid_response", RequestID: requestID, Cause: errors.New("TAE SSE data contained trailing JSON")}
	}
	return StreamEvent{Name: name, Data: data}, nil
}

func (stream *sseStream) RequestID() string { return stream.requestID }

func (stream *sseStream) Close() error {
	if stream.closed {
		return nil
	}
	stream.closed = true
	return stream.body.Close()
}

var _ DataPlane = (*HTTPDataPlane)(nil)
var _ EventStream = (*sseStream)(nil)
