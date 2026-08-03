package coreserver

import (
	"errors"
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
}

func NewLoginBridgeHandler(workload WorkloadAuthorizer, bridge *LoginBridge) (*LoginBridgeHandler, error) {
	if workload == nil || bridge == nil {
		return nil, errors.New("browser workload authorizer and login bridge are required")
	}
	return &LoginBridgeHandler{workload: workload, bridge: bridge}, nil
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
	if !handler.authorize(response, request, "auth.login") || !emptyAuthBridgeRequest(response, request) {
		return
	}
	query, err := parseAuthBridgeQuery(request.URL.RawQuery)
	if err != nil {
		writeLoginBridgeError(response, http.StatusBadRequest)
		return
	}
	challenge, err := exactAuthQuery(query, "login_challenge", nil)
	if err != nil {
		writeLoginBridgeError(response, http.StatusBadRequest)
		return
	}
	binding, err := exactLoginBridgeCookie(request)
	if err != nil {
		writeLoginBridgeError(response, http.StatusBadRequest)
		return
	}
	result, err := handler.bridge.BeginLogin(request.Context(), challenge, binding)
	if err != nil {
		writeLoginBridgeServiceError(response, err)
		return
	}
	if result.External {
		setLoginBridgeCookie(response, result.BrowserBinding, result.ExpiresAt)
	}
	http.Redirect(response, request, result.RedirectTo, http.StatusFound)
}

func (handler *LoginBridgeHandler) consent(response http.ResponseWriter, request *http.Request) {
	setLoginBridgeHeaders(response.Header())
	if !handler.authorize(response, request, "auth.consent") || !emptyAuthBridgeRequest(response, request) {
		return
	}
	query, err := parseAuthBridgeQuery(request.URL.RawQuery)
	if err != nil {
		writeLoginBridgeError(response, http.StatusBadRequest)
		return
	}
	challenge, err := exactAuthQuery(query, "consent_challenge", nil)
	if err != nil {
		writeLoginBridgeError(response, http.StatusBadRequest)
		return
	}
	result, err := handler.bridge.Consent(request.Context(), challenge)
	if err != nil {
		writeLoginBridgeServiceError(response, err)
		return
	}
	http.Redirect(response, request, result.RedirectTo, http.StatusFound)
}

func (handler *LoginBridgeHandler) callback(response http.ResponseWriter, request *http.Request) {
	setLoginBridgeHeaders(response.Header())
	if !handler.authorize(response, request, "auth.callback") || !emptyAuthBridgeRequest(response, request) {
		return
	}
	query, err := parseAuthBridgeQuery(request.URL.RawQuery)
	if err != nil {
		writeLoginBridgeError(response, http.StatusBadRequest)
		return
	}
	state, err := exactAuthQuery(query, "state", map[string]bool{
		"code": true, "error": true, "error_description": true, "error_uri": true,
		"iss": true, "scope": true, "session_state": true,
	})
	if err != nil {
		writeLoginBridgeError(response, http.StatusBadRequest)
		return
	}
	code, err := optionalSingularAuthQuery(query, "code")
	if err != nil {
		writeLoginBridgeError(response, http.StatusBadRequest)
		return
	}
	providerError, err := optionalSingularAuthQuery(query, "error")
	if err != nil {
		writeLoginBridgeError(response, http.StatusBadRequest)
		return
	}
	for _, optional := range []string{"error_description", "error_uri", "scope", "session_state"} {
		if _, err := optionalSingularAuthQuery(query, optional); err != nil {
			writeLoginBridgeError(response, http.StatusBadRequest)
			return
		}
	}
	issuer, err := optionalSingularAuthQuery(query, "iss")
	if err != nil || (issuer != "" && issuer != handler.bridge.identityProvider.Issuer()) {
		writeLoginBridgeError(response, http.StatusBadRequest)
		return
	}
	binding, err := exactLoginBridgeCookie(request)
	if err != nil || binding == "" {
		writeLoginBridgeError(response, http.StatusBadRequest)
		return
	}
	result, err := handler.bridge.CompleteCallback(request.Context(), state, code, providerError, binding)
	if err != nil {
		clearLoginBridgeCookie(response)
		writeLoginBridgeServiceError(response, err)
		return
	}
	if result.ClearBinding {
		clearLoginBridgeCookie(response)
	}
	http.Redirect(response, request, result.RedirectTo, http.StatusFound)
}

func (handler *LoginBridgeHandler) authorize(response http.ResponseWriter, request *http.Request, action string) bool {
	if err := handler.workload.AuthorizeWorkload(request, action); err != nil {
		writeLoginBridgeError(response, http.StatusForbidden)
		return false
	}
	return true
}

func emptyAuthBridgeRequest(response http.ResponseWriter, request *http.Request) bool {
	if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		writeLoginBridgeError(response, http.StatusBadRequest)
		return false
	}
	return true
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

func writeLoginBridgeServiceError(response http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var stateError *coredb.StateError
	var hydraError *HydraAdminError
	if (errors.As(err, &stateError) && stateError.Code == coredb.ErrorDatabase) || errors.As(err, &hydraError) {
		status = http.StatusServiceUnavailable
	}
	writeLoginBridgeError(response, status)
}

func writeLoginBridgeError(response http.ResponseWriter, status int) {
	http.Error(response, "authorization request could not be completed", status)
}
