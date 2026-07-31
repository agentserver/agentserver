package devfixtures

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

func assistantMessage(responseID, itemID, message string) ([]byte, error) {
	if responseID == "" || itemID == "" || message == "" {
		return nil, errors.New("scripted assistant response IDs and message must be non-empty")
	}
	return responseFromEvents([]map[string]any{
		{"type": "response.created", "response": map[string]any{"id": responseID}},
		{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type": "message", "role": "assistant", "id": itemID,
				"content": []map[string]any{{"type": "output_text", "text": message}},
			},
		},
		completedEvent(responseID),
	})
}

func namespacedFunctionCall(responseID, callID, namespace, name, arguments string) ([]byte, error) {
	if responseID == "" || callID == "" || namespace == "" || name == "" {
		return nil, errors.New("scripted function response IDs, namespace, and name must be non-empty")
	}
	if !json.Valid([]byte(arguments)) {
		return nil, errors.New("scripted function arguments must be valid JSON")
	}
	return responseFromEvents([]map[string]any{
		{"type": "response.created", "response": map[string]any{"id": responseID}},
		{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type": "function_call", "call_id": callID, "namespace": namespace,
				"name": name, "arguments": arguments,
			},
		},
		completedEvent(responseID),
	})
}

func completedEvent(responseID string) map[string]any {
	return map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": responseID,
			"usage": map[string]any{
				"input_tokens": 0, "input_tokens_details": nil,
				"output_tokens": 0, "output_tokens_details": nil, "total_tokens": 0,
			},
		},
	}
}

func responseFromEvents(events []map[string]any) ([]byte, error) {
	var body bytes.Buffer
	for _, event := range events {
		kind, ok := event["type"].(string)
		if !ok || kind == "" {
			return nil, errors.New("scripted Responses event type is required")
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			return nil, fmt.Errorf("encode scripted Responses event: %w", err)
		}
		_, _ = fmt.Fprintf(&body, "event: %s\ndata: %s\n\n", kind, encoded)
	}
	return body.Bytes(), nil
}
