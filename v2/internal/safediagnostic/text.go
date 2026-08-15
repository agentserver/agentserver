// Package safediagnostic provides the shared last-line redaction boundary for
// bounded user-visible diagnostics. Callers must still avoid selecting secret
// fields in the first place; regex redaction is defense in depth.
package safediagnostic

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	jsonSecret     = regexp.MustCompile(`(?i)("(?:access[_-]?token|refresh[_-]?token|id[_-]?token|x[_-]?jwt[_-]?token|authorization|api[_-]?key|client[_-]?secret|secret[_-]?access[_-]?key|password|credential|capability|token|secret)"\s*:\s*")[^"]*(")`)
	keyValueSecret = regexp.MustCompile(`(?i)\b(access[_-]?token|refresh[_-]?token|id[_-]?token|x[_-]?jwt[_-]?token|authorization|api[_-]?key|client[_-]?secret|secret[_-]?access[_-]?key|password|credential|capability|token|secret)(\s*[:=]\s*)([^\s,;]+)`)
	bearerSecret   = regexp.MustCompile(`(?i)\b(bearer)(\s+)[A-Za-z0-9._~+/=-]{8,}`)
	jwtSecret      = regexp.MustCompile(`\b[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	ansiSequence   = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
)

type Text struct {
	Value         string
	OriginalBytes int
	Truncated     bool
	Redacted      bool
}

func Sanitize(contents []byte, maximumBytes int) Text {
	if len(contents) == 0 || maximumBytes < 1 {
		return Text{OriginalBytes: len(contents), Truncated: len(contents) != 0}
	}
	value := strings.ToValidUTF8(string(contents), "�")
	value = strings.ReplaceAll(value, "\x00", "�")
	value = ansiSequence.ReplaceAllString(value, "")
	redacted := jsonSecret.ReplaceAllString(value, `${1}<redacted>${2}`)
	redacted = bearerSecret.ReplaceAllString(redacted, `${1}${2}<redacted>`)
	redacted = keyValueSecret.ReplaceAllString(redacted, `${1}${2}<redacted>`)
	redacted = jwtSecret.ReplaceAllString(redacted, `<redacted-jwt>`)
	result := Text{
		Value: redacted, OriginalBytes: len(contents), Redacted: redacted != value,
		Truncated: len(redacted) > maximumBytes,
	}
	if result.Truncated {
		limit := maximumBytes
		for limit > 0 && !utf8.ValidString(redacted[:limit]) {
			limit--
		}
		result.Value = redacted[:limit]
	}
	return result
}
