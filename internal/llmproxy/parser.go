package llmproxy

import (
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"
)

// anthropicMessageResponse is a minimal structure for non-streaming Anthropic message responses.
type anthropicMessageResponse struct {
	ID    string          `json:"id"`
	Model string          `json:"model"`
	Usage anthropic.Usage `json:"usage"`
}

// ParseNonStreamingResponse parses a complete JSON response from the Anthropic messages API.
func ParseNonStreamingResponse(body []byte) (model, msgID string, u anthropic.Usage, err error) {
	var resp anthropicMessageResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", "", anthropic.Usage{}, err
	}
	return resp.Model, resp.ID, resp.Usage, nil
}

// streamEventData holds the minimal fields we extract from SSE events.
type streamEventData struct {
	Type    string `json:"type"`
	Model   string `json:"model,omitempty"`
	Message struct {
		ID    string          `json:"id,omitempty"`
		Model string          `json:"model,omitempty"`
		Usage anthropic.Usage `json:"usage,omitempty"`
	} `json:"message,omitempty"`
	Usage anthropic.Usage `json:"usage,omitempty"`
}

// ParseStreamEvent parses a single SSE data payload from an Anthropic streaming response.
// Returns the event type, model, message ID, usage, and whether usage data was found.
func ParseStreamEvent(data []byte) (eventType, model, msgID string, u anthropic.Usage, hasUsage bool) {
	var evt streamEventData
	if err := json.Unmarshal(data, &evt); err != nil {
		return "", "", "", anthropic.Usage{}, false
	}

	eventType = evt.Type

	switch evt.Type {
	case "message_start":
		// message_start contains the initial message with model and usage.
		model = evt.Message.Model
		msgID = evt.Message.ID
		u = evt.Message.Usage
		hasUsage = true
	case "message_delta":
		// message_delta contains the final usage (output tokens).
		u = evt.Usage
		hasUsage = true
	}

	return eventType, model, msgID, u, hasUsage
}
