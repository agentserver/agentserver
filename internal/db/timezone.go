package db

import (
	"fmt"
	"strings"
	"time"
)

// ParseZonedToUTC parses an ISO 8601 timestamp and returns it in UTC.
// Accepts either an offset-bearing form ("...Z" or "...+HH:MM") or a
// naive local form (no offset) which is interpreted in `tz` (IANA name).
// Mirrors nanoclaw's parseZonedToUtc(timezone.ts).
func ParseZonedToUTC(s, tz string) (time.Time, error) {
	if hasOffset(s) {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse offset timestamp %q: %w", s, err)
		}
		return t.UTC(), nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("unknown timezone %q: %w", tz, err)
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		t, err := time.ParseInLocation(layout, s, loc)
		if err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable timestamp %q", s)
}

func hasOffset(s string) bool {
	if strings.HasSuffix(s, "Z") {
		return true
	}
	if len(s) < 6 {
		return false
	}
	tail := s[len(s)-6:]
	return (tail[0] == '+' || tail[0] == '-') && tail[3] == ':'
}
