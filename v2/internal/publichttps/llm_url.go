package publichttps

import (
	"errors"
	"net/url"
	"path"
	"strings"
)

// ResponsesURLFromBase accepts a provider API prefix, not a full endpoint.
// An origin alone uses /v1; custom API prefixes are preserved. The controlled
// transport still enforces public DNS/IP checks and refuses redirects.
func ResponsesURLFromBase(raw string) (string, error) {
	u, err := ValidateURL(raw, "")
	if err != nil {
		return "", err
	}
	prefix := strings.TrimSuffix(u.Path, "/")
	if prefix == "" {
		prefix = "/v1"
	}
	if path.Clean(prefix) != prefix || strings.Contains(prefix, "\\") ||
		strings.HasSuffix(prefix, "/responses") || strings.HasSuffix(prefix, "/chat/completions") {
		return "", errors.New("base URL must contain a clean API prefix, not an endpoint")
	}
	u.Path = prefix + "/responses"
	if _, err := ValidateResponsesURL(u.String()); err != nil {
		return "", err
	}
	return u.String(), nil
}

func ValidateResponsesURL(raw string) (*url.URL, error) {
	u, err := ValidateURL(raw, "")
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(u.Path, "/responses") || path.Clean(u.Path) != u.Path || strings.Contains(u.Path, "\\") {
		return nil, errors.New("Responses URL must have a clean path ending in /responses")
	}
	return u, nil
}
