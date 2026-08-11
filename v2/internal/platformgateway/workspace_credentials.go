package platformgateway

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const (
	maximumWorkspaceCredentialRoutesRequestBytes  = int64(512 * 1024)
	maximumWorkspaceCredentialRoutesResponseBytes = int64(1024 * 1024)
)

// WorkspaceCredentialRoutes is the thin Platform edge for Core-owned
// workspace-credential configuration. It forwards only the user bearer and
// bounded JSON; it never stores or interprets a credential secret and is not a
// runtime credential data plane.
type WorkspaceCredentialRoutes struct {
	coreOrigin *url.URL
	client     *http.Client
}

func NewWorkspaceCredentialRoutes(coreOrigin string, client *http.Client) (*WorkspaceCredentialRoutes, error) {
	if client == nil {
		return nil, errors.New("platform workspace credential HTTP client is required")
	}
	parsed, err := url.Parse(coreOrigin)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || parsed.RawPath != "" || (parsed.Path != "" && parsed.Path != "/") ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, errors.New("platform workspace credential Core URL must be an HTTP(S) origin")
	}
	if parsed.Scheme == "http" && !workspaceCredentialLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("cleartext platform credential Core URL is allowed only on loopback")
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	parsed.Path = ""
	return &WorkspaceCredentialRoutes{coreOrigin: parsed, client: &copyClient}, nil
}

func (routes *WorkspaceCredentialRoutes) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(corecontract.WorkspaceCredentialProviderSchemasPath, routes)
	mux.Handle(corecontract.WorkspaceCredentialCollectionRoutePattern, routes)
	mux.Handle(corecontract.WorkspaceCredentialResourceRoutePattern, routes)
	mux.Handle(corecontract.WorkspaceCredentialAuthorizationCollectionRoutePattern, routes)
	mux.Handle(corecontract.WorkspaceCredentialAuthorizationResourceRoutePattern, routes)
	return mux
}

func (routes *WorkspaceCredentialRoutes) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if routes == nil || routes.coreOrigin == nil || routes.client == nil {
		writeWorkspaceCredentialRoutesError(response, http.StatusServiceUnavailable, "core_unavailable", "credential configuration is temporarily unavailable")
		return
	}
	if request == nil || request.URL == nil || request.URL.RawQuery != "" || request.URL.RawPath != "" || request.URL.Fragment != "" {
		writeWorkspaceCredentialRoutesError(response, http.StatusBadRequest, "invalid_argument", "credential route must be canonical")
		return
	}
	allowed := workspaceCredentialMethods(request.URL.Path)
	if !credentialMethodAllowed(request.Method, allowed) {
		response.Header().Set("Allow", strings.Join(allowed, ", "))
		writeWorkspaceCredentialRoutesError(response, http.StatusMethodNotAllowed, "method_not_allowed", "credential method is not allowed")
		return
	}
	authorization, ok := boundedCredentialBearer(request.Header)
	if !ok {
		response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-platform-api"`)
		writeWorkspaceCredentialRoutesError(response, http.StatusUnauthorized, "unauthorized", "a single bounded user bearer is required")
		return
	}
	body, err := readWorkspaceCredentialRoutesBody(request)
	if err != nil {
		writeWorkspaceCredentialRoutesError(response, http.StatusBadRequest, "invalid_argument", err.Error())
		return
	}
	endpoint := *routes.coreOrigin
	endpoint.Path = request.URL.Path
	upstream, err := http.NewRequestWithContext(request.Context(), request.Method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		writeWorkspaceCredentialRoutesError(response, http.StatusBadGateway, "core_unavailable", "could not construct the Core credential request")
		return
	}
	upstream.Header.Set("Authorization", authorization)
	upstream.Header.Set("Accept", "application/json")
	if contentType := request.Header.Get("Content-Type"); contentType != "" {
		upstream.Header.Set("Content-Type", contentType)
	}
	result, err := routes.client.Do(upstream)
	if err != nil {
		writeWorkspaceCredentialRoutesError(response, http.StatusBadGateway, "core_unavailable", "Core credential configuration is unavailable")
		return
	}
	defer result.Body.Close()
	mediaType, _, mediaErr := mime.ParseMediaType(result.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		writeWorkspaceCredentialRoutesError(response, http.StatusBadGateway, "invalid_core_response", "Core returned an invalid credential response")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(result.Body, maximumWorkspaceCredentialRoutesResponseBytes+1))
	if err != nil || int64(len(raw)) > maximumWorkspaceCredentialRoutesResponseBytes {
		writeWorkspaceCredentialRoutesError(response, http.StatusBadGateway, "invalid_core_response", "Core credential response exceeded its bound")
		return
	}
	if challenge := result.Header.Get("WWW-Authenticate"); challenge != "" && len(challenge) <= 1024 && !strings.ContainsAny(challenge, "\x00\r\n") {
		response.Header().Set("WWW-Authenticate", challenge)
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(result.StatusCode)
	_, _ = response.Write(raw)
}

func workspaceCredentialLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func credentialMethodAllowed(method string, allowed []string) bool {
	for _, candidate := range allowed {
		if method == candidate {
			return true
		}
	}
	return false
}

func workspaceCredentialMethods(path string) []string {
	switch {
	case path == corecontract.WorkspaceCredentialProviderSchemasPath:
		return []string{http.MethodGet}
	case strings.HasSuffix(path, ":rotate"), strings.HasSuffix(path, ":revoke"), strings.HasSuffix(path, ":delete"), strings.HasSuffix(path, ":setDefault"):
		return []string{http.MethodPost}
	case strings.Contains(path, "/credential-authorizations/") && (strings.HasSuffix(path, ":poll") || strings.HasSuffix(path, ":cancel")):
		return []string{http.MethodPost}
	case strings.Contains(path, "/credential-authorizations/") && workspaceCredentialAuthorizationPathSegmentCount(path) == 5:
		return []string{http.MethodPost}
	case strings.Contains(path, "/credential-authorizations/") && workspaceCredentialAuthorizationPathSegmentCount(path) == 6:
		return []string{http.MethodGet}
	case workspaceCredentialPathSegmentCount(path) == 5:
		return []string{http.MethodGet, http.MethodPost}
	default:
		return []string{http.MethodPatch}
	}
}

func workspaceCredentialAuthorizationPathSegmentCount(value string) int {
	if value == "" || strings.HasSuffix(value, "/") {
		return 0
	}
	parts := strings.Split(strings.TrimPrefix(value, "/"), "/")
	if len(parts) < 5 || parts[0] != "v2" || parts[1] != "workspaces" || parts[2] == "" ||
		parts[3] != "credential-authorizations" || parts[4] == "" {
		return 0
	}
	return len(parts)
}

func workspaceCredentialPathSegmentCount(value string) int {
	if value == "" || strings.HasSuffix(value, "/") {
		return 0
	}
	parts := strings.Split(strings.TrimPrefix(value, "/"), "/")
	if len(parts) < 5 || parts[0] != "v2" || parts[1] != "workspaces" || parts[2] == "" ||
		parts[3] != "credentials" || parts[4] == "" {
		return 0
	}
	return len(parts)
}

func boundedCredentialBearer(header http.Header) (string, bool) {
	values := header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || len(values[0]) <= len("Bearer ") ||
		len(values[0]) > 16*1024 || strings.ContainsAny(values[0], "\x00\r\n,") {
		return "", false
	}
	return values[0], true
}

func readWorkspaceCredentialRoutesBody(request *http.Request) ([]byte, error) {
	if request.Body == nil {
		return nil, nil
	}
	if request.ContentLength > maximumWorkspaceCredentialRoutesRequestBytes {
		return nil, errors.New("credential request exceeds its size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, maximumWorkspaceCredentialRoutesRequestBytes+1))
	if err != nil || int64(len(raw)) > maximumWorkspaceCredentialRoutesRequestBytes {
		return nil, errors.New("credential request exceeds its size limit")
	}
	return raw, nil
}

func writeWorkspaceCredentialRoutesError(response http.ResponseWriter, status int, code, message string) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_, _ = fmt.Fprintf(response, `{"error":{"code":%q,"message":%q}}`+"\n", code, message)
}

var _ http.Handler = (*WorkspaceCredentialRoutes)(nil)
