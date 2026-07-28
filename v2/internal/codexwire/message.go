// Package codexwire implements the newline-delimited Codex JSON-RPC dialect.
//
// The dialect intentionally omits the standard `jsonrpc` member. Both stock
// app-server and stock exec-server use it over stdio. This package only owns
// framing and envelope validation; product state and method semantics belong
// to their respective components.
package codexwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

const (
	// DefaultMaxFrameBytes matches the current stock exec-server stdio bound.
	// A production runtime manifest must pin and probe this value before use.
	DefaultMaxFrameBytes = 64 * 1024 * 1024

	// DefaultMaxJSONNodes mirrors stock exec-server's JSON complexity guard.
	DefaultMaxJSONNodes = 256 * 1024
)

// Kind identifies the shape of a validated wire envelope.
type Kind uint8

const (
	KindRequest Kind = iota + 1
	KindNotification
	KindResponse
	KindError
)

func (k Kind) String() string {
	switch k {
	case KindRequest:
		return "request"
	case KindNotification:
		return "notification"
	case KindResponse:
		return "response"
	case KindError:
		return "error"
	default:
		return fmt.Sprintf("unknown(%d)", k)
	}
}

// RPCError is the error payload returned by the Codex JSON-RPC dialect.
type RPCError struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Message is a validated inbound or outbound Codex wire envelope. Raw fields
// preserve upstream payloads so adapters do not silently rewrite method data.
type Message struct {
	Kind   Kind
	ID     json.RawMessage
	Method string
	Params json.RawMessage
	Result json.RawMessage
	Error  *RPCError
	Raw    json.RawMessage
}

// DecodeParams decodes the params field and rejects a missing field.
func (m Message) DecodeParams(destination any) error {
	if len(m.Params) == 0 {
		return errors.New("message has no params")
	}
	if err := json.Unmarshal(m.Params, destination); err != nil {
		return fmt.Errorf("decode params: %w", err)
	}
	return nil
}

// DecodeResult decodes the result field and rejects a non-response message.
func (m Message) DecodeResult(destination any) error {
	if m.Kind != KindResponse {
		return fmt.Errorf("message kind is %s, not response", m.Kind)
	}
	if err := json.Unmarshal(m.Result, destination); err != nil {
		return fmt.Errorf("decode result: %w", err)
	}
	return nil
}

// Parse validates one complete JSON object in the Codex JSON-RPC dialect.
func Parse(frame []byte) (Message, error) {
	return parseWithNodeLimit(frame, DefaultMaxJSONNodes)
}

func parseWithNodeLimit(frame []byte, maxNodes int) (Message, error) {
	if maxNodes <= 0 {
		return Message{}, errors.New("max JSON nodes must be positive")
	}
	trimmed := bytes.TrimSpace(frame)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Message{}, errors.New("Codex wire message must be a JSON object")
	}
	if err := validateJSON(frame, maxNodes); err != nil {
		return Message{}, err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(frame, &fields); err != nil {
		return Message{}, fmt.Errorf("decode envelope: %w", err)
	}
	if fields == nil {
		return Message{}, errors.New("Codex wire message must be a JSON object")
	}
	if _, exists := fields["jsonrpc"]; exists {
		return Message{}, errors.New("Codex wire message must omit the jsonrpc field")
	}

	_, hasID := fields["id"]
	methodRaw, hasMethod := fields["method"]
	params, hasParams := fields["params"]
	result, hasResult := fields["result"]
	errorRaw, hasError := fields["error"]

	message := Message{Raw: append(json.RawMessage(nil), frame...)}
	if hasID {
		if err := validateID(fields["id"]); err != nil {
			return Message{}, err
		}
		message.ID = append(json.RawMessage(nil), fields["id"]...)
	}
	if hasParams {
		message.Params = append(json.RawMessage(nil), params...)
	}

	if hasMethod {
		if hasResult || hasError {
			return Message{}, errors.New("request or notification cannot contain result or error")
		}
		if err := json.Unmarshal(methodRaw, &message.Method); err != nil || message.Method == "" {
			return Message{}, errors.New("method must be a non-empty string")
		}
		if hasID {
			message.Kind = KindRequest
		} else {
			message.Kind = KindNotification
		}
		return message, nil
	}

	if !hasID {
		return Message{}, errors.New("response must contain id")
	}
	if hasResult == hasError {
		return Message{}, errors.New("response must contain exactly one of result or error")
	}
	if hasParams {
		return Message{}, errors.New("response cannot contain params")
	}
	if hasResult {
		message.Kind = KindResponse
		message.Result = append(json.RawMessage(nil), result...)
		return message, nil
	}

	var rpcError RPCError
	if err := json.Unmarshal(errorRaw, &rpcError); err != nil {
		return Message{}, fmt.Errorf("decode error response: %w", err)
	}
	if rpcError.Message == "" {
		return Message{}, errors.New("error response message must be non-empty")
	}
	message.Kind = KindError
	message.Error = &rpcError
	return message, nil
}

func validateID(raw json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode request id: %w", err)
	}
	switch id := value.(type) {
	case string:
		return nil
	case json.Number:
		if _, err := strconv.ParseInt(id.String(), 10, 64); err != nil {
			return errors.New("request id number must be a signed 64-bit integer")
		}
		return nil
	default:
		return errors.New("request id must be a string or signed 64-bit integer")
	}
}

func validateJSON(frame []byte, maxNodes int) error {
	decoder := json.NewDecoder(bytes.NewReader(frame))
	decoder.UseNumber()
	nodes := 0
	if err := validateJSONValue(decoder, &nodes, maxNodes); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON frame contains more than one value")
		}
		return fmt.Errorf("decode trailing JSON data: %w", err)
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder, nodes *int, maxNodes int) error {
	(*nodes)++
	if *nodes > maxNodes {
		return fmt.Errorf("JSON message exceeds the limit of %d values", maxNodes)
	}

	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("decode object key: %w", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key must be a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[key] = struct{}{}
			if err := validateJSONValue(decoder, nodes, maxNodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("decode object close: %w", err)
		}
		if closing != json.Delim('}') {
			return errors.New("JSON object did not end with }")
		}
		return nil
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder, nodes, maxNodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("decode array close: %w", err)
		}
		if closing != json.Delim(']') {
			return errors.New("JSON array did not end with ]")
		}
		return nil
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}
