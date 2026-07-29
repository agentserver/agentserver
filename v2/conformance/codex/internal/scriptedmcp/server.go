// Package scriptedmcp provides a bounded, sessionless Streamable HTTP MCP
// server for stock Codex conformance probes. It implements only the MCP
// initialize, initialized notification, tools/list, and tools/call flow needed
// by the probes, plus a bounded server-originated elicitation during a scripted
// tool call. It fails closed on every other method.
package scriptedmcp

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
	"reflect"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxRequestBytes  = 1024 * 1024
	defaultMaxResponseBytes = 1024 * 1024
	defaultMaxHeaderBytes   = 32 * 1024
	maxTools                = 64
	maxExpectedCalls        = 64
	maxRecordedRequests     = 256
	maxRecordedFailures     = 64
	elicitationWaitTimeout  = 4 * time.Second
)

// Tool is one tool advertised by tools/list. InputSchema and, when present,
// Annotations must be JSON objects. An empty Description is allowed because
// MCP itself permits it. The default annotation marks the tool read-only so
// existing probes remain side-effect free.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Annotations json.RawMessage
}

// ExpectedElicitation scripts one server-to-client elicitation/create request
// made while a tools/call request is in flight. ID must be a non-empty JSON
// string. Params and Response must be JSON objects.
type ExpectedElicitation struct {
	ID       json.RawMessage
	Params   json.RawMessage
	Response json.RawMessage
}

// ExpectedCall scripts one tools/call request and its deterministic result.
// Arguments and Result must both be JSON objects. When Elicitation is set, the
// result is sent only after the expected client response arrives.
type ExpectedCall struct {
	Name        string
	Arguments   json.RawMessage
	Result      json.RawMessage
	Elicitation *ExpectedElicitation
}

type Config struct {
	MaxRequestBytes int64
	Tools           []Tool
	ExpectedCalls   []ExpectedCall
}

// Request is a bounded copy of one accepted HTTP request.
type Request struct {
	Method    string
	Path      string
	Header    http.Header
	Body      []byte
	// RPCMethod is empty for a JSON-RPC response to a scripted elicitation.
	RPCMethod string
}

// Call is a decoded tools/call request received by the server.
type Call struct {
	Name      string
	Arguments json.RawMessage
	Meta      json.RawMessage
}

// ElicitationResponse is one client response to a scripted
// elicitation/create request.
type ElicitationResponse struct {
	ID     json.RawMessage
	Result json.RawMessage
}

type pendingElicitation struct {
	expected json.RawMessage
	result   chan json.RawMessage
}

type Server struct {
	server          *http.Server
	listener        net.Listener
	url             string
	done            chan struct{}
	closeOnce       sync.Once
	maxRequestBytes int64
	tools           []Tool
	expectedCalls   []ExpectedCall

	mu               sync.Mutex
	phase            int
	nextExpectedCall int
	requests         []Request
	calls            []Call
	elicitations     []ElicitationResponse
	pending          map[string]*pendingElicitation
	failures         []string
}

func Start(config Config) (*Server, error) {
	result, err := newServer(config)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for scripted MCP: %w", err)
	}
	result.listener = listener
	result.url = "http://" + listener.Addr().String() + "/mcp"
	result.done = make(chan struct{})
	go func() {
		defer close(result.done)
		if err := result.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			result.mu.Lock()
			result.recordFailureLocked("serve scripted MCP: " + err.Error())
			result.mu.Unlock()
		}
	}()
	return result, nil
}

func newServer(config Config) (*Server, error) {
	if config.MaxRequestBytes < 0 {
		return nil, errors.New("scripted MCP request bound cannot be negative")
	}
	if config.MaxRequestBytes == 0 {
		config.MaxRequestBytes = defaultMaxRequestBytes
	}
	if len(config.Tools) == 0 {
		return nil, errors.New("at least one scripted MCP tool is required")
	}
	if len(config.Tools) > maxTools {
		return nil, fmt.Errorf("scripted MCP tool count exceeds %d", maxTools)
	}
	if len(config.ExpectedCalls) > maxExpectedCalls {
		return nil, fmt.Errorf("scripted MCP expected call count exceeds %d", maxExpectedCalls)
	}

	tools := make([]Tool, len(config.Tools))
	toolNames := make(map[string]struct{}, len(config.Tools))
	toolListBytes := 0
	for index, tool := range config.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			return nil, fmt.Errorf("scripted MCP tool %d has an empty name", index)
		}
		if _, duplicate := toolNames[tool.Name]; duplicate {
			return nil, fmt.Errorf("duplicate scripted MCP tool %q", tool.Name)
		}
		if !jsonObject(tool.InputSchema) {
			return nil, fmt.Errorf("scripted MCP tool %q input schema must be a JSON object", tool.Name)
		}
		if len(tool.Annotations) != 0 && !jsonObject(tool.Annotations) {
			return nil, fmt.Errorf("scripted MCP tool %q annotations must be a JSON object", tool.Name)
		}
		toolListBytes += len(tool.Name) + len(tool.Description) + len(tool.InputSchema) + len(tool.Annotations)
		if toolListBytes > defaultMaxResponseBytes {
			return nil, fmt.Errorf("scripted MCP tools/list response exceeds %d bytes", defaultMaxResponseBytes)
		}
		toolNames[tool.Name] = struct{}{}
		tool.InputSchema = append(json.RawMessage(nil), tool.InputSchema...)
		tool.Annotations = append(json.RawMessage(nil), tool.Annotations...)
		tools[index] = tool
	}

	expectedCalls := make([]ExpectedCall, len(config.ExpectedCalls))
	elicitationIDs := make(map[string]struct{})
	for index, call := range config.ExpectedCalls {
		if _, exists := toolNames[call.Name]; !exists {
			return nil, fmt.Errorf("scripted MCP expected call %d references unknown tool %q", index, call.Name)
		}
		if !jsonObject(call.Arguments) {
			return nil, fmt.Errorf("scripted MCP expected call %d arguments must be a JSON object", index)
		}
		if !jsonObject(call.Result) {
			return nil, fmt.Errorf("scripted MCP expected call %d result must be a JSON object", index)
		}
		if len(call.Result) > defaultMaxResponseBytes {
			return nil, fmt.Errorf("scripted MCP expected call %d result exceeds %d bytes", index, defaultMaxResponseBytes)
		}
		if call.Elicitation != nil {
			elicitation := *call.Elicitation
			if !nonEmptyJSONString(elicitation.ID) {
				return nil, fmt.Errorf("scripted MCP expected call %d elicitation id must be a non-empty JSON string", index)
			}
			elicitationID := string(bytes.TrimSpace(elicitation.ID))
			if _, duplicate := elicitationIDs[elicitationID]; duplicate {
				return nil, fmt.Errorf("duplicate scripted MCP elicitation id %s", elicitationID)
			}
			if !jsonObject(elicitation.Params) {
				return nil, fmt.Errorf("scripted MCP expected call %d elicitation params must be a JSON object", index)
			}
			if !jsonObject(elicitation.Response) {
				return nil, fmt.Errorf("scripted MCP expected call %d elicitation response must be a JSON object", index)
			}
			if len(elicitation.Params)+len(elicitation.Response) > defaultMaxResponseBytes {
				return nil, fmt.Errorf("scripted MCP expected call %d elicitation exceeds %d bytes", index, defaultMaxResponseBytes)
			}
			elicitationIDs[elicitationID] = struct{}{}
			elicitation.ID = append(json.RawMessage(nil), elicitation.ID...)
			elicitation.Params = append(json.RawMessage(nil), elicitation.Params...)
			elicitation.Response = append(json.RawMessage(nil), elicitation.Response...)
			call.Elicitation = &elicitation
		}
		call.Arguments = append(json.RawMessage(nil), call.Arguments...)
		call.Result = append(json.RawMessage(nil), call.Result...)
		expectedCalls[index] = call
	}

	result := &Server{
		maxRequestBytes: config.MaxRequestBytes,
		tools:           tools,
		expectedCalls:   expectedCalls,
		pending:         make(map[string]*pendingElicitation),
	}
	result.server = &http.Server{
		Handler:           http.HandlerFunc(result.serveHTTP),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       5 * time.Second,
		MaxHeaderBytes:    defaultMaxHeaderBytes,
	}
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

func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := make([]Call, len(s.calls))
	for index, call := range s.calls {
		call.Arguments = append(json.RawMessage(nil), call.Arguments...)
		call.Meta = append(json.RawMessage(nil), call.Meta...)
		calls[index] = call
	}
	return calls
}

func (s *Server) ElicitationResponses() []ElicitationResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	responses := make([]ElicitationResponse, len(s.elicitations))
	for index, response := range s.elicitations {
		response.ID = append(json.RawMessage(nil), response.ID...)
		response.Result = append(json.RawMessage(nil), response.Result...)
		responses[index] = response
	}
	return responses
}

func (s *Server) Failures() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.failures...)
}

func (s *Server) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		s.failHTTP(writer, http.StatusMethodNotAllowed, "unexpected method "+request.Method)
		return
	}
	if request.URL.Path != "/mcp" || request.URL.RawQuery != "" {
		s.failHTTP(writer, http.StatusNotFound, "unexpected MCP target "+request.URL.RequestURI())
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		s.failHTTP(writer, http.StatusUnsupportedMediaType, "MCP Content-Type must be application/json")
		return
	}
	if encoding := request.Header.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
		s.failHTTP(writer, http.StatusUnsupportedMediaType, "unsupported request content encoding "+encoding)
		return
	}

	request.Body = http.MaxBytesReader(writer, request.Body, s.maxRequestBytes)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			s.failHTTP(writer, http.StatusRequestEntityTooLarge, fmt.Sprintf("request exceeded %d bytes", s.maxRequestBytes))
			return
		}
		s.failHTTP(writer, http.StatusBadRequest, "read request body: "+err.Error())
		return
	}

	var rpc struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &rpc); err != nil || rpc.JSONRPC != "2.0" {
		s.failHTTP(writer, http.StatusBadRequest, "MCP body must be one JSON-RPC 2.0 message")
		return
	}
	hasID := len(rpc.ID) != 0 && !bytes.Equal(bytes.TrimSpace(rpc.ID), []byte("null"))
	hasResult := len(rpc.Result) != 0
	hasError := len(rpc.Error) != 0
	if rpc.Method == "" {
		if !hasID || hasResult == hasError {
			s.failHTTP(writer, http.StatusBadRequest, "MCP response must have an id and exactly one of result or error")
			return
		}
	} else if hasResult || hasError {
		s.failHTTP(writer, http.StatusBadRequest, "MCP request or notification cannot contain result or error")
		return
	}

	s.mu.Lock()
	if len(s.requests) >= maxRecordedRequests {
		s.recordFailureLocked(fmt.Sprintf("MCP request count exceeds %d", maxRecordedRequests))
		s.mu.Unlock()
		writeRPCError(writer, rpc.ID, -32000, "scripted MCP request bound exceeded")
		return
	}
	s.requests = append(s.requests, Request{
		Method:    request.Method,
		Path:      request.URL.Path,
		Header:    request.Header.Clone(),
		Body:      append([]byte(nil), body...),
		RPCMethod: rpc.Method,
	})
	s.mu.Unlock()

	if rpc.Method == "" {
		s.handleClientResponse(writer, rpc.ID, rpc.Result, rpc.Error)
		return
	}
	s.dispatch(request.Context(), writer, rpc.ID, rpc.Method, rpc.Params)
}

func (s *Server) dispatch(ctx context.Context, writer http.ResponseWriter, id json.RawMessage, method string, params json.RawMessage) {
	hasID := len(id) != 0 && !bytes.Equal(bytes.TrimSpace(id), []byte("null"))
	switch method {
	case "initialize":
		if !hasID {
			s.failRPC(writer, id, -32600, "initialize must be a request")
			return
		}
		var initialize struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(params, &initialize) != nil || initialize.ProtocolVersion == "" {
			s.failRPC(writer, id, -32602, "initialize requires protocolVersion")
			return
		}
		s.mu.Lock()
		if s.phase != 0 {
			s.recordFailureLocked("initialize received out of order")
			s.mu.Unlock()
			writeRPCError(writer, id, -32600, "initialize received out of order")
			return
		}
		s.phase = 1
		s.mu.Unlock()
		writeRPCResult(writer, id, map[string]any{
			"protocolVersion": initialize.ProtocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{
				"name":    "agentserver-v2-scripted-mcp",
				"version": "0.0.0",
			},
		})
	case "notifications/initialized":
		if hasID {
			s.failRPC(writer, id, -32600, "notifications/initialized must not have an id")
			return
		}
		s.mu.Lock()
		if s.phase != 1 {
			s.recordFailureLocked("notifications/initialized received out of order")
			s.mu.Unlock()
			writeAccepted(writer)
			return
		}
		s.phase = 2
		s.mu.Unlock()
		writeAccepted(writer)
	case "tools/list":
		if !hasID {
			s.failRPC(writer, id, -32600, "tools/list must be a request")
			return
		}
		if !s.ready() {
			s.failRPC(writer, id, -32600, "tools/list received before initialization")
			return
		}
		tools := make([]map[string]any, 0, len(s.tools))
		for _, tool := range s.tools {
			annotations := tool.Annotations
			if len(annotations) == 0 {
				annotations = json.RawMessage(`{"readOnlyHint":true}`)
			}
			tools = append(tools, map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"inputSchema": tool.InputSchema,
				"annotations": annotations,
			})
		}
		writeRPCResult(writer, id, map[string]any{"tools": tools})
	case "tools/call":
		if !hasID {
			s.failRPC(writer, id, -32600, "tools/call must be a request")
			return
		}
		if !s.ready() {
			s.failRPC(writer, id, -32600, "tools/call received before initialization")
			return
		}
		s.handleToolCall(ctx, writer, id, params)
	default:
		s.failRPC(writer, id, -32601, "unsupported MCP method "+method)
	}
}

func (s *Server) handleToolCall(ctx context.Context, writer http.ResponseWriter, id, params json.RawMessage) {
	var request struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      json.RawMessage `json:"_meta"`
	}
	if json.Unmarshal(params, &request) != nil || request.Name == "" || !jsonObject(request.Arguments) {
		s.failRPC(writer, id, -32602, "tools/call requires a name and object arguments")
		return
	}
	call := Call{
		Name:      request.Name,
		Arguments: append(json.RawMessage(nil), request.Arguments...),
		Meta:      append(json.RawMessage(nil), request.Meta...),
	}

	s.mu.Lock()
	s.calls = append(s.calls, call)
	if s.nextExpectedCall >= len(s.expectedCalls) {
		s.recordFailureLocked("unexpected tools/call for " + request.Name)
		s.mu.Unlock()
		writeRPCError(writer, id, -32602, "unexpected scripted tools/call")
		return
	}
	expected := s.expectedCalls[s.nextExpectedCall]
	if expected.Name != request.Name || !jsonSemanticEqual(expected.Arguments, request.Arguments) {
		s.recordFailureLocked(fmt.Sprintf("tools/call %d = %s %s, want %s %s", s.nextExpectedCall, request.Name, request.Arguments, expected.Name, expected.Arguments))
		s.mu.Unlock()
		writeRPCError(writer, id, -32602, "scripted tools/call mismatch")
		return
	}
	s.nextExpectedCall++
	s.mu.Unlock()
	if expected.Elicitation != nil {
		s.handleElicitingToolCall(ctx, writer, id, expected)
		return
	}
	writeRPCResult(writer, id, expected.Result)
}

func (s *Server) handleElicitingToolCall(
	ctx context.Context,
	writer http.ResponseWriter,
	toolCallID json.RawMessage,
	expected ExpectedCall,
) {
	elicitation := expected.Elicitation
	key := string(bytes.TrimSpace(elicitation.ID))
	pending := &pendingElicitation{
		expected: elicitation.Response,
		result:   make(chan json.RawMessage, 1),
	}
	s.mu.Lock()
	if _, exists := s.pending[key]; exists {
		s.recordFailureLocked("elicitation already pending for id " + key)
		s.mu.Unlock()
		writeRPCError(writer, toolCallID, -32000, "scripted MCP elicitation id collision")
		return
	}
	s.pending[key] = pending
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.pending[key] == pending {
			delete(s.pending, key)
		}
		s.mu.Unlock()
	}()

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	if err := writeSSEMessage(writer, map[string]any{
		"jsonrpc": "2.0",
		"id":      elicitation.ID,
		"method":  "elicitation/create",
		"params":  elicitation.Params,
	}); err != nil {
		s.recordFailure("write scripted MCP elicitation: " + err.Error())
		return
	}

	timer := time.NewTimer(elicitationWaitTimeout)
	defer timer.Stop()
	select {
	case <-pending.result:
		if err := writeSSEMessage(writer, map[string]any{
			"jsonrpc": "2.0",
			"id":      toolCallID,
			"result":  expected.Result,
		}); err != nil {
			s.recordFailure("write scripted MCP tool result after elicitation: " + err.Error())
		}
	case <-ctx.Done():
		s.recordFailure("scripted MCP elicitation request context ended: " + ctx.Err().Error())
	case <-timer.C:
		s.recordFailure(fmt.Sprintf("scripted MCP elicitation %s exceeded %s", key, elicitationWaitTimeout))
		_ = writeSSEMessage(writer, map[string]any{
			"jsonrpc": "2.0",
			"id":      toolCallID,
			"error": map[string]any{
				"code":    -32000,
				"message": "scripted MCP elicitation timed out",
			},
		})
	}
}

func (s *Server) handleClientResponse(
	writer http.ResponseWriter,
	id json.RawMessage,
	result json.RawMessage,
	errorPayload json.RawMessage,
) {
	if len(errorPayload) != 0 {
		s.failHTTP(writer, http.StatusBadRequest, "scripted MCP elicitation received an error response")
		return
	}
	if !jsonObject(result) {
		s.failHTTP(writer, http.StatusBadRequest, "scripted MCP elicitation result must be a JSON object")
		return
	}
	key := string(bytes.TrimSpace(id))
	s.mu.Lock()
	pending, exists := s.pending[key]
	if !exists {
		s.recordFailureLocked("unexpected MCP client response id " + key)
		s.mu.Unlock()
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	delete(s.pending, key)
	resultCopy := append(json.RawMessage(nil), result...)
	s.elicitations = append(s.elicitations, ElicitationResponse{
		ID:     append(json.RawMessage(nil), id...),
		Result: resultCopy,
	})
	if !jsonSemanticEqual(pending.expected, result) {
		s.recordFailureLocked(fmt.Sprintf("elicitation response %s = %s, want %s", key, result, pending.expected))
	}
	s.mu.Unlock()
	pending.result <- resultCopy
	writeAccepted(writer)
}

func (s *Server) ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase == 2
}

func (s *Server) failHTTP(writer http.ResponseWriter, status int, failure string) {
	s.mu.Lock()
	s.recordFailureLocked(failure)
	s.mu.Unlock()
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_, _ = io.WriteString(writer, `{"error":"scripted MCP request rejected"}`)
}

func (s *Server) failRPC(writer http.ResponseWriter, id json.RawMessage, code int, failure string) {
	s.mu.Lock()
	s.recordFailureLocked(failure)
	s.mu.Unlock()
	writeRPCError(writer, id, code, failure)
}

func (s *Server) recordFailureLocked(failure string) {
	if len(s.failures) < maxRecordedFailures {
		s.failures = append(s.failures, failure)
	}
}

func (s *Server) recordFailure(failure string) {
	s.mu.Lock()
	s.recordFailureLocked(failure)
	s.mu.Unlock()
}

func writeRPCResult(writer http.ResponseWriter, id json.RawMessage, result any) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func writeRPCError(writer http.ResponseWriter, id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func writeAccepted(writer http.ResponseWriter) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusAccepted)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}

func writeSSEMessage(writer http.ResponseWriter, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(body) > defaultMaxResponseBytes {
		return fmt.Errorf("SSE message exceeds %d bytes", defaultMaxResponseBytes)
	}
	if _, err := fmt.Fprintf(writer, "event: message\ndata: %s\n\n", body); err != nil {
		return err
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		return errors.New("HTTP response writer does not support flushing")
	}
	flusher.Flush()
	return nil
}

func jsonObject(raw json.RawMessage) bool {
	if !json.Valid(raw) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}

func nonEmptyJSONString(raw json.RawMessage) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && value != ""
}

func jsonSemanticEqual(left, right json.RawMessage) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
