package browsergateway

// DSH Web API wire primitives.  The browser client in deepseek-harness uses
// one JSON RPC envelope for unary calls and a small JSON frame vocabulary for
// the multiplexed Remote stream WebSocket.  Keeping these values independent
// from the Core resource contracts makes the compatibility adapter testable
// without a running Core.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	// DSHAPIPath is the HTTP prefix used by the DSH Connection carrier.
	DSHAPIPath = "/api/"
	// DSHRemoteMuxPath carries every DSH Remote stream.
	DSHRemoteMuxPath = "/api/remote.mux"
)

type dshClientRequest struct {
	Type    string          `json:"type"`
	RPCID   string          `json:"rpcId"`
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload"`
}

type dshPayload struct {
	Args json.RawMessage `json:"args"`
}

type dshServerResponse struct {
	Type   string       `json:"type"`
	RPCID  string       `json:"rpcId"`
	Result dshRPCResult `json:"result"`
}

type dshRPCResult struct {
	OK    bool         `json:"ok"`
	Value any          `json:"value,omitempty"`
	Error *dshRPCError `json:"error,omitempty"`
}

type dshRPCError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

type dshStreamClientMessage struct {
	Type     string          `json:"type"`
	StreamID string          `json:"streamId"`
	Endpoint string          `json:"endpoint,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
}

type dshStreamServerMessage struct {
	Type     string       `json:"type"`
	StreamID string       `json:"streamId"`
	Value    any          `json:"value,omitempty"`
	Error    *dshRPCError `json:"error,omitempty"`
}

func parseDSHClientRequest(raw []byte) (dshClientRequest, map[string]json.RawMessage, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return dshClientRequest{}, nil, fmt.Errorf("request is not JSON: %w", err)
	}
	var request dshClientRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return dshClientRequest{}, nil, fmt.Errorf("invalid client-request envelope: %w", err)
	}
	if request.Type != "client-request" {
		return dshClientRequest{}, nil, errors.New("type must be client-request")
	}
	if !validDSHID(request.RPCID) {
		return dshClientRequest{}, nil, errors.New("rpcId must be non-empty bounded text")
	}
	if !validDSHEndpoint(request.Method) {
		return dshClientRequest{}, nil, errors.New("method is not a valid DSH endpoint")
	}
	if request.Payload == nil {
		return dshClientRequest{}, nil, errors.New("payload is required")
	}
	var payload dshPayload
	if err := json.Unmarshal(request.Payload, &payload); err != nil || payload.Args == nil {
		return dshClientRequest{}, nil, errors.New("payload.args is required")
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(payload.Args, &args); err != nil || args == nil {
		return dshClientRequest{}, nil, errors.New("payload.args must be an object")
	}
	if _, ok := envelope["type"]; !ok {
		return dshClientRequest{}, nil, errors.New("type is required")
	}
	return request, args, nil
}

func parseDSHStreamClientMessage(raw []byte) (dshStreamClientMessage, error) {
	var message dshStreamClientMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return dshStreamClientMessage{}, fmt.Errorf("stream message is not JSON: %w", err)
	}
	if !validDSHID(message.StreamID) {
		return dshStreamClientMessage{}, errors.New("streamId must be non-empty bounded text")
	}
	switch message.Type {
	case "open":
		if !validDSHEndpoint(message.Endpoint) || message.Payload == nil {
			return dshStreamClientMessage{}, errors.New("open requires endpoint and payload")
		}
	case "item":
		if message.Value == nil {
			return dshStreamClientMessage{}, errors.New("item requires value")
		}
	case "end", "cancel":
	default:
		return dshStreamClientMessage{}, fmt.Errorf("unknown stream message type %q", message.Type)
	}
	return message, nil
}

func validDSHID(value string) bool {
	return value != "" && len(value) <= 256 && !strings.ContainsAny(value, "\x00\r\n")
}

func validDSHEndpoint(value string) bool {
	if value == "" || len(value) > 512 || strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return value == "$events" || value == "$events/result"
	}
	for _, part := range parts {
		for _, character := range part {
			if !(character >= 'A' && character <= 'Z') && !(character >= 'a' && character <= 'z') &&
				!(character >= '0' && character <= '9') && !strings.ContainsRune("_$.-", character) {
				return false
			}
		}
	}
	return true
}

func dshSuccess(rpcID string, value any, present bool) dshServerResponse {
	result := dshRPCResult{OK: true}
	if present {
		result.Value = value
	}
	return dshServerResponse{Type: "server-response", RPCID: rpcID, Result: result}
}

func dshFailure(rpcID, code, message string, details map[string]any) dshServerResponse {
	if details == nil {
		details = map[string]any{}
	}
	return dshServerResponse{
		Type: "server-response", RPCID: rpcID,
		Result: dshRPCResult{OK: false, Error: &dshRPCError{Code: code, Message: message, Details: details}},
	}
}

func dshStreamItem(streamID string, value any) dshStreamServerMessage {
	return dshStreamServerMessage{Type: "item", StreamID: streamID, Value: value}
}

func dshStreamEnd(streamID string) dshStreamServerMessage {
	return dshStreamServerMessage{Type: "end", StreamID: streamID}
}

func dshStreamError(streamID, code, message string, details map[string]any) dshStreamServerMessage {
	if details == nil {
		details = map[string]any{}
	}
	return dshStreamServerMessage{Type: "error", StreamID: streamID, Error: &dshRPCError{Code: code, Message: message, Details: details}}
}
