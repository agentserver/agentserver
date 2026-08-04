package browsergateway

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const (
	maximumSessionResourceRequestBytes  = int64(64 * 1024)
	maximumSessionResourceResponseBytes = int64(2 * 1024 * 1024)
)

// SessionResourceProxy is a closed Browser control-plane proxy. It forwards
// only the reviewed session lifecycle routes and preserves the user's opaque
// Hydra bearer; Core remains the authorization and state authority.
type SessionResourceProxy struct {
	backend *CoreRunBackend
}

func NewSessionResourceProxy(backend *CoreRunBackend) (*SessionResourceProxy, error) {
	if backend == nil || backend.baseURL == nil || backend.httpClient == nil {
		return nil, errors.New("session resource Core backend is required")
	}
	return &SessionResourceProxy{backend: backend}, nil
}

func (proxy *SessionResourceProxy) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(corecontract.UserSessionCollectionRoutePattern, proxy)
	mux.Handle(corecontract.UserSessionResourceRoutePattern, proxy)
	mux.Handle(corecontract.UserSessionTranscriptRoutePattern, proxy)
	mux.Handle(corecontract.UserSessionArchiveRoutePattern, proxy)
	return mux
}

func (proxy *SessionResourceProxy) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if proxy == nil || proxy.backend == nil {
		writeHTTPError(response, http.StatusServiceUnavailable, "session_proxy_unavailable", "session resource proxy is unavailable")
		return
	}
	workspaceID := request.PathValue("workspaceId")
	if err := validateCanonicalUUID("workspaceId", workspaceID); err != nil {
		writeHTTPError(response, http.StatusBadRequest, "invalid_scope", err.Error())
		return
	}
	if sessionID := request.PathValue("sessionId"); sessionID != "" {
		if err := validateCanonicalUUID("sessionId", sessionID); err != nil {
			writeHTTPError(response, http.StatusBadRequest, "invalid_scope", err.Error())
			return
		}
	}
	if request.URL.RawQuery != "" || request.URL.RawPath != "" || request.URL.Fragment != "" {
		writeHTTPError(response, http.StatusBadRequest, "invalid_argument", "session resource proxy accepts only canonical paths")
		return
	}
	allowed := allowedSessionResourceMethods(request)
	if !sessionResourceMethodAllowed(request.Method, allowed) {
		response.Header().Set("Allow", strings.Join(allowed, ", "))
		writeHTTPError(response, http.StatusMethodNotAllowed, "method_not_allowed", "session resource method is not allowed")
		return
	}
	bearer, err := extractBearer(request.Header)
	if err != nil {
		response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-browser-api"`)
		writeHTTPError(response, http.StatusUnauthorized, "unauthorized", "a single user bearer token is required")
		return
	}
	body, err := readSessionResourceBody(request)
	if err != nil {
		writeHTTPError(response, http.StatusBadRequest, "invalid_argument", err.Error())
		return
	}
	endpoint := proxy.backend.endpoint(request.URL.Path)
	upstream, err := http.NewRequestWithContext(request.Context(), request.Method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		writeHTTPError(response, http.StatusBadGateway, "core_unavailable", "could not construct the Core session request")
		return
	}
	upstream.Header.Set("Authorization", "Bearer "+bearer)
	upstream.Header.Set("Accept", "application/json")
	if contentType := request.Header.Get("Content-Type"); contentType != "" {
		upstream.Header.Set("Content-Type", contentType)
	}
	result, raw, err := proxy.backend.doBounded(upstream, maximumSessionResourceResponseBytes)
	if err != nil {
		writeHTTPError(response, http.StatusBadGateway, "core_unavailable", "Core session API is unavailable")
		return
	}
	defer result.Body.Close()
	if challenge := result.Header.Get("WWW-Authenticate"); challenge != "" && len(challenge) <= 1024 && !strings.ContainsAny(challenge, "\x00\r\n") {
		response.Header().Set("WWW-Authenticate", challenge)
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(result.StatusCode)
	_, _ = response.Write(raw)
}

func allowedSessionResourceMethods(request *http.Request) []string {
	if strings.HasSuffix(request.URL.Path, "/actions/archive") {
		return []string{http.MethodPost}
	}
	if strings.HasSuffix(request.URL.Path, "/transcript") {
		return []string{http.MethodGet}
	}
	if request.PathValue("sessionId") != "" {
		return []string{http.MethodGet, http.MethodPatch}
	}
	return []string{http.MethodGet, http.MethodPost}
}

func sessionResourceMethodAllowed(method string, allowed []string) bool {
	for _, candidate := range allowed {
		if method == candidate {
			return true
		}
	}
	return false
}

func readSessionResourceBody(request *http.Request) ([]byte, error) {
	if request.Method == http.MethodGet {
		if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
			return nil, errors.New("session reads require an empty request")
		}
		return nil, nil
	}
	mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" || len(parameters) != 0 {
		return nil, errors.New("Content-Type must be exactly application/json")
	}
	if request.ContentLength > maximumSessionResourceRequestBytes {
		return nil, errors.New("session resource request exceeds its size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, maximumSessionResourceRequestBytes+1))
	if err != nil {
		return nil, errors.New("could not read session resource request")
	}
	if int64(len(raw)) > maximumSessionResourceRequestBytes {
		return nil, errors.New("session resource request exceeds its size limit")
	}
	return raw, nil
}
