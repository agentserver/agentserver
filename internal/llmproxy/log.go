package llmproxy

import (
	"log/slog"
	"os"
)

// NewLogger creates a structured JSON logger for the LLM proxy.
func NewLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})).With("component", "llmproxy")
}
