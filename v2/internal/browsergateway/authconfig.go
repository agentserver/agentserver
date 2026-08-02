package browsergateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

type BrowserAuthorizationConfig struct {
	Version               int      `json:"version"`
	AuthorizationEndpoint string   `json:"authorizationEndpoint"`
	TokenEndpoint         string   `json:"tokenEndpoint"`
	RedirectPath          string   `json:"redirectPath"`
	ClientID              string   `json:"clientId"`
	Scopes                []string `json:"scopes"`
	Audience              string   `json:"audience"`
	APIOrigin             string   `json:"apiOrigin"`
}

type BrowserAuthorizationConfigHandler struct {
	body []byte
}

func NewBrowserAuthorizationConfigHandler(clientID, audience string, scopes []string, apiOrigin string) (*BrowserAuthorizationConfigHandler, error) {
	if !boundedAuthorizationText(clientID, 512) || !boundedAuthorizationText(audience, 512) || len(scopes) < 1 || len(scopes) > 16 {
		return nil, errors.New("browser OAuth client, audience, and scopes are required within protocol bounds")
	}
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if !boundedAuthorizationText(scope, 128) || strings.ContainsAny(scope, " \t") {
			return nil, errors.New("browser OAuth scope is outside protocol bounds")
		}
		if _, duplicate := seen[scope]; duplicate {
			return nil, errors.New("browser OAuth scopes must be unique")
		}
		seen[scope] = struct{}{}
	}
	if apiOrigin != "" {
		parsed, err := url.Parse(apiOrigin)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
			parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.String() != apiOrigin {
			return nil, errors.New("browser API origin must be an exact HTTPS origin when configured")
		}
	}
	document := BrowserAuthorizationConfig{
		Version: 1, AuthorizationEndpoint: "/oauth2/auth", TokenEndpoint: "/oauth2/token", RedirectPath: "/",
		ClientID: clientID, Scopes: append([]string(nil), scopes...), Audience: audience, APIOrigin: apiOrigin,
	}
	body, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')
	return &BrowserAuthorizationConfigHandler{body: body}, nil
}

func (handler *BrowserAuthorizationConfigHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if request.Method != http.MethodGet || request.ContentLength != 0 || len(request.TransferEncoding) != 0 ||
		request.Header.Get("Authorization") != "" || request.URL.RawQuery != "" {
		http.Error(response, "invalid browser authorization configuration request", http.StatusBadRequest)
		return
	}
	if handler == nil || len(handler.body) == 0 {
		http.Error(response, "browser authorization configuration unavailable", http.StatusServiceUnavailable)
		return
	}
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(handler.body)
}

func boundedAuthorizationText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}
