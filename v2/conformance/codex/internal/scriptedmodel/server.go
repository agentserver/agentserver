// Package scriptedmodel provides a bounded loopback Responses API server for
// stock Codex conformance probes. It is deterministic test infrastructure, not
// a model implementation or a production network dependency.
package scriptedmodel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxRequestBytes  = 4 * 1024 * 1024
	defaultMaxResponseBytes = 4 * 1024 * 1024
	defaultMaxHeaderBytes   = 32 * 1024
	maxScriptedResponses    = 64
	maxRecordedFailures     = 64
)

// Response is one scripted HTTP response. A zero StatusCode means 200 and an
// empty ContentType means text/event-stream. RedirectURL requires an explicit
// 3xx StatusCode and returns only a Location header. HoldOpen records the
// request but sends no response; the handler remains pending until the client
// cancels the request or the server closes. Held and redirect responses cannot
// set body fields.
type Response struct {
	StatusCode  int
	ContentType string
	Body        []byte
	HoldOpen    bool
	RedirectURL string
}

type Config struct {
	MaxRequestBytes int64
	Responses       []Response
}

// Request is a bounded copy of one accepted Responses API request.
type Request struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

type Server struct {
	server          *http.Server
	listener        net.Listener
	url             string
	done            chan struct{}
	closeOnce       sync.Once
	maxRequestBytes int64

	mu        sync.Mutex
	responses []Response
	next      int
	requests  []Request
	failures  []string
	requestCh chan struct{}
}

func Start(config Config) (*Server, error) {
	result, err := newServer(config)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for scripted model: %w", err)
	}
	result.listener = listener
	result.url = "http://" + listener.Addr().String()
	result.done = make(chan struct{})
	go func() {
		defer close(result.done)
		if err := result.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			result.mu.Lock()
			result.recordFailureLocked("serve scripted model: " + err.Error())
			result.mu.Unlock()
		}
	}()
	return result, nil
}

func newServer(config Config) (*Server, error) {
	if config.MaxRequestBytes < 0 {
		return nil, errors.New("scripted model request bound cannot be negative")
	}
	if config.MaxRequestBytes == 0 {
		config.MaxRequestBytes = defaultMaxRequestBytes
	}
	if len(config.Responses) == 0 {
		return nil, errors.New("at least one scripted model response is required")
	}
	if len(config.Responses) > maxScriptedResponses {
		return nil, fmt.Errorf("scripted model response count exceeds %d", maxScriptedResponses)
	}
	responses := make([]Response, len(config.Responses))
	for index, response := range config.Responses {
		if response.HoldOpen && (response.StatusCode != 0 || response.ContentType != "" || len(response.Body) != 0 || response.RedirectURL != "") {
			return nil, fmt.Errorf("held scripted response %d cannot set status, content type, or body", index)
		}
		if response.StatusCode != 0 && (response.StatusCode < 200 || response.StatusCode > 599) {
			return nil, fmt.Errorf("scripted response %d has invalid status %d", index, response.StatusCode)
		}
		if response.RedirectURL != "" {
			switch response.StatusCode {
			case http.StatusMovedPermanently,
				http.StatusFound,
				http.StatusSeeOther,
				http.StatusTemporaryRedirect,
				http.StatusPermanentRedirect:
			default:
				return nil, fmt.Errorf("redirect scripted response %d has unsupported status %d", index, response.StatusCode)
			}
			if response.ContentType != "" || len(response.Body) != 0 {
				return nil, fmt.Errorf("redirect scripted response %d cannot set content type or body", index)
			}
			if len(response.RedirectURL) > defaultMaxHeaderBytes || strings.ContainsAny(response.RedirectURL, "\r\n") {
				return nil, fmt.Errorf("redirect scripted response %d has an invalid target", index)
			}
		}
		if len(response.Body) > defaultMaxResponseBytes {
			return nil, fmt.Errorf("scripted response %d exceeds %d bytes", index, defaultMaxResponseBytes)
		}
		response.Body = append([]byte(nil), response.Body...)
		responses[index] = response
	}

	result := &Server{
		maxRequestBytes: config.MaxRequestBytes,
		responses:       responses,
		requestCh:       make(chan struct{}, 1),
	}
	server := &http.Server{
		Handler:           http.HandlerFunc(result.serveHTTP),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       5 * time.Second,
		MaxHeaderBytes:    defaultMaxHeaderBytes,
	}
	result.server = server
	return result, nil
}

func (s *Server) URL() string {
	return s.url
}

func (s *Server) Close() {
	s.closeOnce.Do(func() {
		if s.listener == nil {
			return
		}
		_ = s.server.Close()
		if s.done != nil {
			<-s.done
		}
	})
}

func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := make([]Request, len(s.requests))
	for index, request := range s.requests {
		request.Header = request.Header.Clone()
		request.Body = append([]byte(nil), request.Body...)
		requests[index] = request
	}
	return requests
}

// WaitForRequests waits without polling until at least count bounded requests
// have been captured. It is used by crash probes as a deterministic in-flight
// model-call barrier.
func (s *Server) WaitForRequests(ctx context.Context, count int) error {
	if ctx == nil {
		return errors.New("request wait context is required")
	}
	if count <= 0 {
		return errors.New("request wait count must be positive")
	}
	for {
		s.mu.Lock()
		observed := len(s.requests)
		s.mu.Unlock()
		if observed >= count {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.requestCh:
		}
	}
}

// Failures returns protocol violations observed by the loopback server, such
// as an unexpected route, malformed JSON, or exhaustion of the response script.
func (s *Server) Failures() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.failures...)
}

func (s *Server) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		s.fail(writer, http.StatusMethodNotAllowed, "unexpected method "+request.Method)
		return
	}
	if request.URL.Path != "/v1/responses" || request.URL.RawQuery != "" {
		s.fail(writer, http.StatusNotFound, "unexpected Responses API target "+request.URL.RequestURI())
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		s.fail(writer, http.StatusUnsupportedMediaType, "Responses API Content-Type must be application/json")
		return
	}
	if encoding := request.Header.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
		s.fail(writer, http.StatusUnsupportedMediaType, "unsupported request content encoding "+encoding)
		return
	}

	request.Body = http.MaxBytesReader(writer, request.Body, s.maxRequestBytes)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			s.fail(writer, http.StatusRequestEntityTooLarge, fmt.Sprintf("request exceeded %d bytes", s.maxRequestBytes))
			return
		}
		s.fail(writer, http.StatusBadRequest, "read request body: "+err.Error())
		return
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil || object == nil {
		s.fail(writer, http.StatusBadRequest, "Responses API body must be one JSON object")
		return
	}

	captured := Request{
		Method: request.Method,
		Path:   request.URL.Path,
		Header: request.Header.Clone(),
		Body:   append([]byte(nil), body...),
	}
	s.mu.Lock()
	if len(s.requests) < len(s.responses)+1 {
		s.requests = append(s.requests, captured)
		select {
		case s.requestCh <- struct{}{}:
		default:
		}
	}
	if s.next >= len(s.responses) {
		s.recordFailureLocked("scripted response sequence exhausted")
		s.mu.Unlock()
		writeError(writer, http.StatusInternalServerError)
		return
	}
	response := s.responses[s.next]
	s.next++
	s.mu.Unlock()
	if response.HoldOpen {
		<-request.Context().Done()
		return
	}
	if response.RedirectURL != "" {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Location", response.RedirectURL)
		writer.WriteHeader(response.StatusCode)
		return
	}

	status := response.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	contentType := response.ContentType
	if contentType == "" {
		contentType = "text/event-stream"
	}
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_, _ = writer.Write(response.Body)
}

func (s *Server) fail(writer http.ResponseWriter, status int, failure string) {
	s.mu.Lock()
	s.recordFailureLocked(failure)
	s.mu.Unlock()
	writeError(writer, status)
}

func (s *Server) recordFailureLocked(failure string) {
	if len(s.failures) < maxRecordedFailures {
		s.failures = append(s.failures, failure)
	}
}

func writeError(writer http.ResponseWriter, status int) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_, _ = io.WriteString(writer, `{"error":"scripted model request rejected"}`)
}

// AssistantMessage builds the minimal Responses API SSE sequence needed to
// complete one Codex turn with a final assistant message.
func AssistantMessage(responseID, itemID, message string) (Response, error) {
	if responseID == "" || itemID == "" {
		return Response{}, errors.New("response and item IDs must be non-empty")
	}
	return responseFromEvents([]map[string]any{
		{
			"type":     "response.created",
			"response": map[string]any{"id": responseID},
		},
		{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type":    "message",
				"role":    "assistant",
				"id":      itemID,
				"content": []map[string]any{{"type": "output_text", "text": message}},
			},
		},
		{
			"type": "response.completed",
			"response": map[string]any{
				"id": responseID,
				"usage": map[string]any{
					"input_tokens":          0,
					"input_tokens_details":  nil,
					"output_tokens":         0,
					"output_tokens_details": nil,
					"total_tokens":          0,
				},
			},
		},
	})
}

// FunctionCall builds one completed Responses API turn that asks Codex to
// execute a function tool. arguments must contain one valid JSON value encoded
// as a string, matching the Responses API function_call wire shape.
func FunctionCall(responseID, callID, name, arguments string) (Response, error) {
	return functionCall(responseID, callID, "", name, arguments)
}

// NamespacedFunctionCall builds one completed Responses API turn that asks
// Codex to execute a function within a Responses API namespace. Stock Codex
// uses this shape for MCP tools, for example namespace mcp__executor and child
// tool approved_echo.
func NamespacedFunctionCall(responseID, callID, namespace, name, arguments string) (Response, error) {
	if namespace == "" {
		return Response{}, errors.New("function call namespace must be non-empty")
	}
	return functionCall(responseID, callID, namespace, name, arguments)
}

func functionCall(responseID, callID, namespace, name, arguments string) (Response, error) {
	if responseID == "" || callID == "" || name == "" {
		return Response{}, errors.New("response, call, and tool IDs must be non-empty")
	}
	if !json.Valid([]byte(arguments)) {
		return Response{}, errors.New("function call arguments must be valid JSON")
	}
	item := map[string]any{
		"type":      "function_call",
		"call_id":   callID,
		"name":      name,
		"arguments": arguments,
	}
	if namespace != "" {
		item["namespace"] = namespace
	}
	return responseFromEvents([]map[string]any{
		{
			"type":     "response.created",
			"response": map[string]any{"id": responseID},
		},
		{
			"type": "response.output_item.done",
			"item": item,
		},
		{
			"type": "response.completed",
			"response": map[string]any{
				"id": responseID,
				"usage": map[string]any{
					"input_tokens":          0,
					"input_tokens_details":  nil,
					"output_tokens":         0,
					"output_tokens_details": nil,
					"total_tokens":          0,
				},
			},
		},
	})
}

func responseFromEvents(events []map[string]any) (Response, error) {
	var body bytes.Buffer
	for _, event := range events {
		encoded, err := json.Marshal(event)
		if err != nil {
			return Response{}, fmt.Errorf("encode scripted model event: %w", err)
		}
		kind, _ := event["type"].(string)
		_, _ = fmt.Fprintf(&body, "event: %s\ndata: %s\n\n", kind, encoded)
	}
	return Response{Body: body.Bytes()}, nil
}
