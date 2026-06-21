package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
)

// KeepFrame reports whether an SDKMessage should be retained for downstream
// processing or dropped as noise. Per Phase 0 (claude 2.1.185):
//
//	keep: Type=="system" && Subtype=="init"
//	      Type=="assistant"
//	      Type=="user"      (wraps tool_result content; relevant when MCP tools come in Phase 3)
//	      Type=="result"    (any subtype: success, error, etc.)
//
//	drop: Type=="stream_event"
//	      Type=="system" && Subtype=="status"
//	      Type=="system" && Subtype=="thinking_tokens"
//
//	unknown: anything else → keep (we'd rather over-log than silently drop new data;
//	        warning logged when unknown type is encountered).
func KeepFrame(m SDKMessage) bool {
	switch m.Type {
	case "system":
		// keep system/init, drop system/status and system/thinking_tokens
		return m.Subtype == "init"
	case "stream_event":
		return false
	case "assistant", "user", "result":
		return true
	default:
		// Unknown type: keep it and log a warning for observability (prefer over-logging
		// new data rather than silently dropping it — new claude versions may add types).
		log.Printf("[cc-app-gateway/runner] unknown SDKMessage type=%q subtype=%q — keeping for downstream; new claude version?", m.Type, m.Subtype)
		return true
	}
}

// ModelUsage captures per-model token counts from a result frame.
type ModelUsage struct {
	InputTokens              int64   `json:"inputTokens"`
	OutputTokens             int64   `json:"outputTokens"`
	CacheReadInputTokens     int64   `json:"cacheReadInputTokens,omitempty"`
	CacheCreationInputTokens int64   `json:"cacheCreationInputTokens,omitempty"`
}

// ResultMeta captures the closing-frame metadata we surface in the HTTP response.
type ResultMeta struct {
	Subtype       string                `json:"subtype"`
	IsError       bool                  `json:"isError"`
	DurationMs    int64                 `json:"durationMs"`
	TotalCostUSD  float64               `json:"totalCostUsd"`
	ModelUsage    map[string]ModelUsage `json:"modelUsage,omitempty"`
	ErrorMessage  string                `json:"errorMessage,omitempty"`
}

// ExtractAssistantText drains the channel and returns:
//   - the final assistant text (last non-empty text content from any
//     assistant frame's message.content[*].text — concatenated within a
//     single frame if multiple text blocks exist; the LAST frame wins).
//   - ResultMeta from the closing result frame.
//
// If the channel closes without a result frame, returns ("", nil,
// fmt.Errorf("stream closed without result frame: %w", io.ErrUnexpectedEOF)).
// If only a result frame appears (no assistant content), returns ("", meta, nil) —
// callers decide what an empty assistantText means.
//
// Important: this consumes the channel fully — caller must NOT also range over it.
func ExtractAssistantText(in <-chan SDKMessage) (string, *ResultMeta, error) {
	var lastAssistantText string
	var resultMeta *ResultMeta

	for msg := range in {
		if msg.Type == "assistant" {
			// Extract text from the message's content array.
			text, err := extractAssistantFrameText(msg.Message)
			if err != nil {
				// Log but continue — a malformed assistant frame shouldn't
				// kill the whole extraction.
				log.Printf("warning: failed to parse assistant frame: %v", err)
			} else if text != "" {
				// Overwrite with the latest non-empty text.
				lastAssistantText = text
			}
		} else if msg.Type == "result" {
			// Parse the result frame's top-level fields.
			meta, err := extractResultFrameMeta(msg.Raw)
			if err != nil {
				// Log but continue — similar reasoning.
				log.Printf("warning: failed to parse result frame: %v", err)
			} else {
				resultMeta = meta
			}
		}
		// Ignore other frame types (they've already been filtered by the caller
		// or will be handled elsewhere).
	}

	// Ensure we got a result frame.
	if resultMeta == nil {
		return "", nil, fmt.Errorf("stream closed without result frame: %w", io.ErrUnexpectedEOF)
	}

	return lastAssistantText, resultMeta, nil
}

// extractAssistantFrameText decodes the message field of an assistant frame
// and returns the concatenated text content.
func extractAssistantFrameText(rawMessage json.RawMessage) (string, error) {
	var msg struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rawMessage, &msg); err != nil {
		return "", fmt.Errorf("unmarshal assistant message: %w", err)
	}

	var sb strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return sb.String(), nil
}

// extractResultFrameMeta decodes the top-level fields from a result frame
// (stored in Raw) and returns the ResultMeta.
func extractResultFrameMeta(rawFrame json.RawMessage) (*ResultMeta, error) {
	// Parse the top-level result frame structure.
	var resultFrame struct {
		Subtype        string `json:"subtype"`
		IsError        bool   `json:"is_error"`
		DurationMs     int64  `json:"duration_ms"`
		TotalCostUSD   float64 `json:"total_cost_usd"`
		Error          string `json:"error,omitempty"` // populated when is_error=true
		ModelUsageMap  map[string]struct {
			InputTokens              int64 `json:"inputTokens"`
			OutputTokens             int64 `json:"outputTokens"`
			CacheReadInputTokens     int64 `json:"cacheReadInputTokens,omitempty"`
			CacheCreationInputTokens int64 `json:"cacheCreationInputTokens,omitempty"`
		} `json:"modelUsage"`
	}
	if err := json.Unmarshal(rawFrame, &resultFrame); err != nil {
		return nil, fmt.Errorf("unmarshal result frame: %w", err)
	}

	// Build ModelUsage map.
	modelUsage := make(map[string]ModelUsage)
	for modelName, usage := range resultFrame.ModelUsageMap {
		modelUsage[modelName] = ModelUsage{
			InputTokens:              usage.InputTokens,
			OutputTokens:             usage.OutputTokens,
			CacheReadInputTokens:     usage.CacheReadInputTokens,
			CacheCreationInputTokens: usage.CacheCreationInputTokens,
		}
	}

	meta := &ResultMeta{
		Subtype:      resultFrame.Subtype,
		IsError:      resultFrame.IsError,
		DurationMs:   resultFrame.DurationMs,
		TotalCostUSD: resultFrame.TotalCostUSD,
		ModelUsage:   modelUsage,
	}
	if resultFrame.IsError {
		meta.ErrorMessage = resultFrame.Error
	}

	return meta, nil
}
