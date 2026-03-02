package llmproxy

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/google/uuid"
)

// Trace ID prefixes.
const (
	traceIDPrefix   = "at-"
	requestIDPrefix = "ar-"
)

// sessionUUIDRegex matches the session UUID in Claude Code's metadata.user_id.
// Format: user_{64hex}_account__session_{uuid}
var sessionUUIDRegex = regexp.MustCompile(`session_([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)

// ExtractTraceID extracts a trace ID from the request.
// Priority: custom header → Claude Code metadata → auto-generate.
// Returns (traceID, source).
func (s *Server) ExtractTraceID(r *http.Request, body []byte) (string, string) {
	// 1. Check custom trace header.
	if s.config.TraceHeader != "" {
		if hdr := r.Header.Get(s.config.TraceHeader); hdr != "" {
			return hdr, "header"
		}
	}

	// 2. Try Claude Code metadata.user_id.
	if sessionID := extractFromClaudeCode(body); sessionID != "" {
		return traceIDPrefix + sessionID, "claude-code"
	}

	// 3. Auto-generate.
	return GenerateTraceID(), "auto"
}

// extractFromClaudeCode extracts a session UUID from Claude Code's metadata.user_id.
// The user_id format is: user_{64hex}_account__session_{uuid}
func extractFromClaudeCode(body []byte) string {
	var msg struct {
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return ""
	}
	if msg.Metadata.UserID == "" {
		return ""
	}
	matches := sessionUUIDRegex.FindStringSubmatch(msg.Metadata.UserID)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

// GenerateTraceID creates a new trace ID with the "at-" prefix.
func GenerateTraceID() string {
	return traceIDPrefix + uuid.New().String()
}

// GenerateRequestID creates a new request ID with the "ar-" prefix.
func GenerateRequestID() string {
	return requestIDPrefix + uuid.New().String()
}
