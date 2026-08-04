package platformgateway

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const (
	maximumResourceProxyRequestBytes  = int64(128 * 1024)
	maximumResourceProxyResponseBytes = int64(1024 * 1024)
)

// ResourceProxy is a closed, bounded proxy for Platform-owned workspace and
// membership resources. It forwards the user's Hydra bearer unchanged; the
// HTTP client contributes the platform-gateway mTLS workload identity.
type ResourceProxy struct {
	coreOrigin *url.URL
	client     *http.Client
}

func NewResourceProxy(coreOrigin string, client *http.Client) (*ResourceProxy, error) {
	if client == nil {
		return nil, errors.New("platform resource proxy HTTP client is required")
	}
	parsed, err := url.Parse(coreOrigin)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" ||
		parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("platform resource proxy Core URL must be an HTTP(S) origin")
	}
	if parsed.Scheme == "http" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1" {
		return nil, errors.New("cleartext platform resource Core URL is allowed only on loopback")
	}
	parsed.Path = ""
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &ResourceProxy{coreOrigin: parsed, client: &clientCopy}, nil
}

func (proxy *ResourceProxy) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(corecontract.WorkspaceCollectionRoutePattern, proxy)
	mux.Handle(corecontract.WorkspaceResourceRoutePattern, proxy)
	mux.Handle(corecontract.WorkspaceArchiveRoutePattern, proxy)
	mux.Handle(corecontract.WorkspaceMembersCollectionPattern, proxy)
	mux.Handle(corecontract.WorkspaceMemberResourceRoutePattern, proxy)
	return mux
}

func (proxy *ResourceProxy) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	resourceProxyNoStore(response)
	if proxy == nil || proxy.coreOrigin == nil || proxy.client == nil {
		writeResourceProxyError(response, http.StatusServiceUnavailable, "resource_proxy_unavailable", "platform resource proxy is unavailable")
		return
	}
	if request.URL.RawQuery != "" || request.URL.RawPath != "" || request.URL.Fragment != "" {
		writeResourceProxyError(response, http.StatusBadRequest, "invalid_argument", "platform resource proxy accepts only canonical paths")
		return
	}
	if allow := allowedResourceProxyMethods(request); !methodIn(request.Method, allow) {
		response.Header().Set("Allow", strings.Join(allow, ", "))
		writeResourceProxyError(response, http.StatusMethodNotAllowed, "method_not_allowed", "platform resource method is not allowed")
		return
	}
	authorization, ok := boundedResourceBearer(request.Header)
	if !ok {
		response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-platform-api"`)
		writeResourceProxyError(response, http.StatusUnauthorized, "unauthorized", "a single bounded user bearer is required")
		return
	}
	body, err := readResourceProxyBody(request)
	if err != nil {
		writeResourceProxyError(response, http.StatusBadRequest, "invalid_argument", err.Error())
		return
	}
	endpoint := *proxy.coreOrigin
	endpoint.Path = request.URL.Path
	upstream, err := http.NewRequestWithContext(request.Context(), request.Method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		writeResourceProxyError(response, http.StatusBadGateway, "core_unavailable", "could not construct the Core request")
		return
	}
	upstream.Header.Set("Authorization", authorization)
	upstream.Header.Set("Accept", "application/json")
	if contentType := request.Header.Get("Content-Type"); contentType != "" {
		upstream.Header.Set("Content-Type", contentType)
	}
	result, err := proxy.client.Do(upstream)
	if err != nil {
		writeResourceProxyError(response, http.StatusBadGateway, "core_unavailable", "Core platform resource API is unavailable")
		return
	}
	defer result.Body.Close()
	mediaType, _, mediaErr := mime.ParseMediaType(result.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		writeResourceProxyError(response, http.StatusBadGateway, "invalid_core_response", "Core returned an invalid platform resource response")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(result.Body, maximumResourceProxyResponseBytes+1))
	if err != nil || int64(len(raw)) > maximumResourceProxyResponseBytes {
		writeResourceProxyError(response, http.StatusBadGateway, "invalid_core_response", "Core platform resource response exceeded its bound")
		return
	}
	if challenge := result.Header.Get("WWW-Authenticate"); challenge != "" && len(challenge) <= 1024 && !strings.ContainsAny(challenge, "\x00\r\n") {
		response.Header().Set("WWW-Authenticate", challenge)
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(result.StatusCode)
	_, _ = response.Write(raw)
}

func allowedResourceProxyMethods(request *http.Request) []string {
	path := request.URL.Path
	if strings.HasSuffix(path, "/actions/archive") {
		return []string{http.MethodPost}
	}
	if request.PathValue("memberId") != "" {
		return []string{http.MethodPatch, http.MethodDelete}
	}
	if strings.HasSuffix(path, "/members") {
		return []string{http.MethodGet, http.MethodPost}
	}
	if request.PathValue("workspaceId") != "" {
		return []string{http.MethodGet, http.MethodPatch}
	}
	return []string{http.MethodGet, http.MethodPost}
}

func methodIn(method string, allowed []string) bool {
	for _, candidate := range allowed {
		if method == candidate {
			return true
		}
	}
	return false
}

func boundedResourceBearer(header http.Header) (string, bool) {
	values := header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || len(values[0]) <= len("Bearer ") ||
		len(values[0]) > 16*1024 || strings.ContainsAny(values[0], "\x00\r\n,") {
		return "", false
	}
	return values[0], true
}

func readResourceProxyBody(request *http.Request) ([]byte, error) {
	if request.Body == nil {
		return nil, nil
	}
	if request.ContentLength > maximumResourceProxyRequestBytes {
		return nil, errors.New("platform resource request exceeds its size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, maximumResourceProxyRequestBytes+1))
	if err != nil {
		return nil, errors.New("could not read platform resource request")
	}
	if int64(len(raw)) > maximumResourceProxyRequestBytes {
		return nil, errors.New("platform resource request exceeds its size limit")
	}
	return raw, nil
}

func resourceProxyNoStore(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
}

func writeResourceProxyError(response http.ResponseWriter, status int, code, message string) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_, _ = fmt.Fprintf(response, "{\"error\":{\"code\":%q,\"message\":%q}}\n", code, message)
}
