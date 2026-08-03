// Package platformgateway contains the bounded public HTTP surface specific to
// the AgentServer Platform application.
package platformgateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type AuthorizationConfig struct {
	Version               int      `json:"version"`
	AuthorizationEndpoint string   `json:"authorizationEndpoint"`
	TokenEndpoint         string   `json:"tokenEndpoint"`
	RedirectPath          string   `json:"redirectPath"`
	ClientID              string   `json:"clientId"`
	Scopes                []string `json:"scopes"`
	Audience              string   `json:"audience"`
}

type AuthorizationConfigHandler struct{ body []byte }

func NewAuthorizationConfigHandler(clientID, audience string, scopes []string) (*AuthorizationConfigHandler, error) {
	if !boundedText(clientID, 512) || !boundedText(audience, 512) || len(scopes) < 1 || len(scopes) > 32 {
		return nil, errors.New("platform OAuth client, audience, and scopes are required within protocol bounds")
	}
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if !boundedText(scope, 128) || strings.ContainsAny(scope, " \t") {
			return nil, errors.New("platform OAuth scope is outside protocol bounds")
		}
		if _, duplicate := seen[scope]; duplicate {
			return nil, errors.New("platform OAuth scopes must be unique")
		}
		seen[scope] = struct{}{}
	}
	document := AuthorizationConfig{
		Version: 1, AuthorizationEndpoint: "/oauth2/auth", TokenEndpoint: "/oauth2/token", RedirectPath: "/",
		ClientID: clientID, Scopes: append([]string(nil), scopes...), Audience: audience,
	}
	body, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	return &AuthorizationConfigHandler{body: append(body, '\n')}, nil
}

func (handler *AuthorizationConfigHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if request.Method != http.MethodGet || request.ContentLength != 0 || len(request.TransferEncoding) != 0 ||
		request.Header.Get("Authorization") != "" || request.URL.RawQuery != "" {
		http.Error(response, "invalid platform authorization configuration request", http.StatusBadRequest)
		return
	}
	if handler == nil || len(handler.body) == 0 {
		http.Error(response, "platform authorization configuration unavailable", http.StatusServiceUnavailable)
		return
	}
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(handler.body)
}

func boundedText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}
