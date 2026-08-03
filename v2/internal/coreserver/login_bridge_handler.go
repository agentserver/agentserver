package coreserver

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

const LoginBridgeCookieName = "__Host-agentserver-oidc"

type LoginBridgeHandler struct {
	workload WorkloadAuthorizer
	bridge   *LoginBridge
	logger   *slog.Logger
}

func NewLoginBridgeHandler(workload WorkloadAuthorizer, bridge *LoginBridge) (*LoginBridgeHandler, error) {
	if workload == nil || bridge == nil {
		return nil, errors.New("browser workload authorizer and login bridge are required")
	}
	return &LoginBridgeHandler{workload: workload, bridge: bridge, logger: slog.Default()}, nil
}

func (handler *LoginBridgeHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+corecontract.HydraLoginBridgePath, handler.login)
	mux.HandleFunc("GET "+corecontract.HydraConsentBridgePath, handler.consent)
	mux.HandleFunc("GET "+corecontract.OIDCCallbackBridgePath, handler.callback)
	return mux
}

func (handler *LoginBridgeHandler) login(response http.ResponseWriter, request *http.Request) {
	setLoginBridgeHeaders(response.Header())
	if !handler.authorize(response, request, "auth.login") {
		return
	}
	if err := emptyAuthBridgeRequest(request); err != nil {
		handler.writeError(response, request, "login", "request_body", http.StatusBadRequest, err)
		return
	}
	query, err := parseAuthBridgeQuery(request.URL.RawQuery)
	if err != nil {
		handler.writeError(response, request, "login", "query", http.StatusBadRequest, err)
		return
	}
	challenge, err := exactAuthQuery(query, "login_challenge", nil)
	if err != nil {
		handler.writeError(response, request, "login", "challenge", http.StatusBadRequest, err)
		return
	}
	binding, err := exactLoginBridgeCookie(request)
	if err != nil {
		handler.writeError(response, request, "login", "browser_binding", http.StatusBadRequest, err)
		return
	}
	result, err := handler.bridge.BeginLogin(request.Context(), challenge, binding)
	if err != nil {
		handler.writeServiceError(response, request, "login", "begin", err)
		return
	}
	if result.External {
		setLoginBridgeCookie(response, result.BrowserBinding, result.ExpiresAt)
	}
	http.Redirect(response, request, result.RedirectTo, http.StatusFound)
}

func (handler *LoginBridgeHandler) consent(response http.ResponseWriter, request *http.Request) {
	setLoginBridgeHeaders(response.Header())
	if !handler.authorize(response, request, "auth.consent") {
		return
	}
	if err := emptyAuthBridgeRequest(request); err != nil {
		handler.writeError(response, request, "consent", "request_body", http.StatusBadRequest, err)
		return
	}
	query, err := parseAuthBridgeQuery(request.URL.RawQuery)
	if err != nil {
		handler.writeError(response, request, "consent", "query", http.StatusBadRequest, err)
		return
	}
	challenge, err := exactAuthQuery(query, "consent_challenge", nil)
	if err != nil {
		handler.writeError(response, request, "consent", "challenge", http.StatusBadRequest, err)
		return
	}
	result, err := handler.bridge.Consent(request.Context(), challenge)
	if err != nil {
		handler.writeServiceError(response, request, "consent", "complete", err)
		return
	}
	http.Redirect(response, request, result.RedirectTo, http.StatusFound)
}

func (handler *LoginBridgeHandler) callback(response http.ResponseWriter, request *http.Request) {
	setLoginBridgeHeaders(response.Header())
	if !handler.authorize(response, request, "auth.callback") {
		return
	}
	if err := emptyAuthBridgeRequest(request); err != nil {
		handler.writeError(response, request, "callback", "request_body", http.StatusBadRequest, err)
		return
	}
	query, err := parseAuthBridgeQuery(request.URL.RawQuery)
	if err != nil {
		handler.writeError(response, request, "callback", "query", http.StatusBadRequest, err)
		return
	}
	state, err := exactAuthQuery(query, "state", map[string]bool{
		"code": true, "error": true, "error_description": true, "error_uri": true,
		"iss": true, "scope": true, "session_state": true,
	})
	if err != nil {
		handler.writeError(response, request, "callback", "state", http.StatusBadRequest, err)
		return
	}
	code, err := optionalSingularAuthQuery(query, "code")
	if err != nil {
		handler.writeError(response, request, "callback", "code", http.StatusBadRequest, err)
		return
	}
	providerError, err := optionalSingularAuthQuery(query, "error")
	if err != nil {
		handler.writeError(response, request, "callback", "provider_error", http.StatusBadRequest, err)
		return
	}
	for _, optional := range []string{"error_description", "error_uri", "scope", "session_state"} {
		if _, err := optionalSingularAuthQuery(query, optional); err != nil {
			handler.writeError(response, request, "callback", optional, http.StatusBadRequest, err)
			return
		}
	}
	issuer, err := optionalSingularAuthQuery(query, "iss")
	if err != nil {
		handler.writeError(response, request, "callback", "issuer", http.StatusBadRequest, err)
		return
	}
	if issuer != "" && issuer != handler.bridge.identityProvider.Issuer() {
		handler.writeError(response, request, "callback", "issuer", http.StatusBadRequest, errors.New("callback issuer does not match the configured provider"))
		return
	}
	binding, err := exactLoginBridgeCookie(request)
	if err != nil {
		handler.writeError(response, request, "callback", "browser_binding", http.StatusBadRequest, err)
		return
	}
	if binding == "" {
		handler.writeError(response, request, "callback", "browser_binding", http.StatusBadRequest, errors.New("login bridge cookie is missing"))
		return
	}
	result, err := handler.bridge.CompleteCallback(request.Context(), state, code, providerError, binding)
	if err != nil {
		clearLoginBridgeCookie(response)
		handler.writeServiceError(response, request, "callback", "complete", err)
		return
	}
	if result.ClearBinding {
		clearLoginBridgeCookie(response)
	}
	http.Redirect(response, request, result.RedirectTo, http.StatusFound)
}

func (handler *LoginBridgeHandler) authorize(response http.ResponseWriter, request *http.Request, action string) bool {
	if err := handler.workload.AuthorizeWorkload(request, action); err != nil {
		handler.writeError(response, request, action, "workload_authorization", http.StatusForbidden, err)
		return false
	}
	return true
}

func emptyAuthBridgeRequest(request *http.Request) error {
	if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		return errors.New("authorization bridge request body must be empty")
	}
	return nil
}

func parseAuthBridgeQuery(raw string) (url.Values, error) {
	if raw == "" || len(raw) > 32*1024 || strings.ContainsAny(raw, "\x00\r\n") {
		return nil, errors.New("authorization bridge query is empty or outside protocol bounds")
	}
	return url.ParseQuery(raw)
}

func exactAuthQuery(query url.Values, required string, allowed map[string]bool) (string, error) {
	for key, values := range query {
		if key != required && !allowed[key] {
			return "", errors.New("unknown auth query parameter")
		}
		if len(values) != 1 {
			return "", errors.New("auth query parameter must be singular")
		}
	}
	values, exists := query[required]
	if !exists || len(values) != 1 || values[0] == "" {
		return "", errors.New("required auth query parameter is missing")
	}
	return values[0], nil
}

func optionalSingularAuthQuery(query url.Values, name string) (string, error) {
	values, exists := query[name]
	if !exists {
		return "", nil
	}
	if len(values) != 1 || values[0] == "" {
		return "", errors.New("optional auth query parameter must be singular and non-empty")
	}
	if len(values[0]) > 8192 || strings.ContainsAny(values[0], "\x00\r\n") {
		return "", errors.New("optional auth query parameter is outside protocol bounds")
	}
	return values[0], nil
}

func exactLoginBridgeCookie(request *http.Request) (string, error) {
	value := ""
	for _, cookie := range request.Cookies() {
		if cookie.Name != LoginBridgeCookieName {
			continue
		}
		if value != "" || cookie.Value == "" {
			return "", errors.New("login bridge cookie is duplicate or empty")
		}
		value = cookie.Value
	}
	if value != "" && validateOIDCSecret("browser binding", value) != nil {
		return "", errors.New("login bridge cookie is outside protocol bounds")
	}
	return value, nil
}

func setLoginBridgeCookie(response http.ResponseWriter, value string, expiry time.Time) {
	http.SetCookie(response, &http.Cookie{
		Name: LoginBridgeCookieName, Value: value, Path: "/", Expires: expiry.UTC(),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func clearLoginBridgeCookie(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{
		Name: LoginBridgeCookieName, Value: "", Path: "/", Expires: time.Unix(1, 0).UTC(), MaxAge: -1,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func setLoginBridgeHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
	header.Set("Cross-Origin-Opener-Policy", "same-origin")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}

func (handler *LoginBridgeHandler) writeServiceError(
	response http.ResponseWriter,
	request *http.Request,
	operation, stage string,
	err error,
) {
	status := http.StatusBadRequest
	var stateError *coredb.StateError
	var hydraError *HydraAdminError
	if (errors.As(err, &stateError) && stateError.Code == coredb.ErrorDatabase) || errors.As(err, &hydraError) {
		status = http.StatusServiceUnavailable
	}
	handler.writeError(response, request, operation, stage, status, err)
}

func (handler *LoginBridgeHandler) writeError(
	response http.ResponseWriter,
	request *http.Request,
	operation, stage string,
	status int,
	err error,
) {
	if handler != nil && handler.logger != nil {
		level := slog.LevelWarn
		if status >= http.StatusInternalServerError {
			level = slog.LevelError
		}
		attributes := []any{
			"operation", operation,
			"stage", stage,
			"status", status,
			"error_type", fmt.Sprintf("%T", err),
			"error", safeLoginBridgeLogError(err),
		}
		if request != nil {
			attributes = append(attributes, "method", request.Method, "path", request.URL.Path)
			if requestID := safeLoginBridgeRequestID(request.Header.Get("X-Request-Id")); requestID != "" {
				attributes = append(attributes, "request_id", requestID)
			}
		}
		handler.logger.Log(request.Context(), level, "authorization bridge request failed", attributes...)
	}
	writeLoginBridgeError(response, status)
}

func safeLoginBridgeLogError(err error) string {
	if err == nil {
		return "unspecified authorization bridge failure"
	}
	message := strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(err.Error())
	for offset := 0; ; {
		query := strings.IndexByte(message[offset:], '?')
		if query < 0 {
			break
		}
		query += offset
		end := query + 1
		for end < len(message) && !strings.ContainsRune(" \"'<>)]}", rune(message[end])) {
			end++
		}
		message = message[:query+1] + "<redacted>" + message[end:]
		offset = query + len("?<redacted>")
	}
	message = strings.TrimSpace(message)
	if len(message) > 512 {
		message = message[:512] + "..."
	}
	return message
}

func safeLoginBridgeRequestID(value string) string {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value || strings.ContainsAny(value, " \t\r\n\x00") {
		return ""
	}
	return value
}

func writeLoginBridgeError(response http.ResponseWriter, status int) {
	http.Error(response, "authorization request could not be completed", status)
}
