package safediagnostic

import (
	"strings"
	"testing"
)

func TestSanitizeRedactsCredentialShapesAndBoundsUTF8(t *testing.T) {
	input := []byte("\x1b[31mfailed\x1b[0m Authorization: Bearer should-not-leak {\"refresh_token\":\"also-secret\"} 你好")
	result := Sanitize(input, 48)
	if strings.Contains(result.Value, "should-not-leak") || strings.Contains(result.Value, "also-secret") || strings.Contains(result.Value, "\x1b") {
		t.Fatalf("sanitized value leaked secret or ANSI: %q", result.Value)
	}
	if !result.Redacted || !result.Truncated || len(result.Value) > 48 {
		t.Fatalf("sanitized metadata = %+v", result)
	}
}
