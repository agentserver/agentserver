// Package platformgateway contains the bounded public HTTP surface specific to
// the AgentServer Platform application.
package platformgateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
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
	return NewAuthorizationConfigHandlerWithEndpoints(clientID, audience, scopes, "/oauth2/auth", "/oauth2/token")
}

func NewAuthorizationConfigHandlerWithEndpoints(
	clientID, audience string,
	scopes []string,
	authorizationEndpoint, tokenEndpoint string,
) (*AuthorizationConfigHandler, error) {
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
	if err := validateOAuthEndpoint("authorization", authorizationEndpoint, "/oauth2/auth"); err != nil {
		return nil, err
	}
	if err := validateOAuthEndpoint("token", tokenEndpoint, "/oauth2/token"); err != nil {
		return nil, err
	}
	if oauthEndpointAuthority(authorizationEndpoint) != oauthEndpointAuthority(tokenEndpoint) {
		return nil, errors.New("platform OAuth authorization and token endpoints must use the same authority")
	}
	document := AuthorizationConfig{
		Version: 1, AuthorizationEndpoint: authorizationEndpoint, TokenEndpoint: tokenEndpoint, RedirectPath: "/",
		ClientID: clientID, Scopes: append([]string(nil), scopes...), Audience: audience,
	}
	body, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	return &AuthorizationConfigHandler{body: append(body, '\n')}, nil
}

func validateOAuthEndpoint(name, raw, requiredPath string) error {
	if raw == requiredPath {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.Path != requiredPath || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.String() != raw {
		return errors.New("platform OAuth " + name + " endpoint must be the local path or one exact HTTPS endpoint")
	}
	return nil
}

func oauthEndpointAuthority(raw string) string {
	if strings.HasPrefix(raw, "/") {
		return ""
	}
	parsed, _ := url.Parse(raw)
	return parsed.Scheme + "://" + parsed.Host
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
