package browsergateway

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
	maximumLLMGatewayProxyRequestBytes  = int64(128 * 1024)
	maximumLLMGatewayProxyResponseBytes = int64(512 * 1024)
)

// WorkspaceLLMGatewayProxy exposes only the bounded workspace Gateway
// resource surface. Core remains the protocol and authorization authority;
// browser-gateway preserves the original user bearer and adds only its mTLS
// workload identity through the configured HTTP client.
type WorkspaceLLMGatewayProxy struct {
	coreOrigin *url.URL
	client     *http.Client
}

func NewWorkspaceLLMGatewayProxy(coreOrigin string, client *http.Client) (*WorkspaceLLMGatewayProxy, error) {
	if client == nil {
		return nil, errors.New("workspace LLM gateway proxy HTTP client is required")
	}
	parsed, err := url.Parse(coreOrigin)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" ||
		parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("workspace LLM gateway proxy Core URL must be an HTTP(S) origin")
	}
	if parsed.Scheme == "http" && !coreRunLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("cleartext workspace LLM gateway Core URL is allowed only on loopback")
	}
	parsed.Path = ""
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &WorkspaceLLMGatewayProxy{coreOrigin: parsed, client: &clientCopy}, nil
}

func (proxy *WorkspaceLLMGatewayProxy) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(corecontract.LLMGatewayCollectionRoutePattern, proxy)
	mux.Handle(corecontract.LLMGatewayActionRoutePattern, proxy)
	return mux
}

func (proxy *WorkspaceLLMGatewayProxy) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if proxy == nil || proxy.coreOrigin == nil || proxy.client == nil {
		writeLLMGatewayProxyError(response, http.StatusServiceUnavailable, "gateway_proxy_unavailable", "workspace LLM gateway proxy is unavailable")
		return
	}
	if request.URL.RawQuery != "" || request.URL.RawPath != "" || request.URL.Fragment != "" {
		writeLLMGatewayProxyError(response, http.StatusBadRequest, "invalid_argument", "workspace LLM gateway proxy accepts only canonical paths")
		return
	}
	action := request.PathValue("gatewayAction")
	if (action == "" && request.Method != http.MethodGet && request.Method != http.MethodPost) ||
		(action != "" && request.Method != http.MethodPost) {
		allow := http.MethodPost
		if action == "" {
			allow = "GET, POST"
		}
		response.Header().Set("Allow", allow)
		writeLLMGatewayProxyError(response, http.StatusMethodNotAllowed, "method_not_allowed", "workspace LLM gateway method is not allowed")
		return
	}
	authorizations := request.Header.Values("Authorization")
	if len(authorizations) != 1 || !strings.HasPrefix(authorizations[0], "Bearer ") ||
		len(authorizations[0]) < len("Bearer ")+1 || len(authorizations[0]) > 16*1024 ||
		strings.ContainsAny(authorizations[0], "\x00\r\n,") {
		response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-platform-api"`)
		writeLLMGatewayProxyError(response, http.StatusUnauthorized, "unauthorized", "a single bounded user bearer is required")
		return
	}

	body, err := readLLMGatewayProxyRequestBody(request)
	if err != nil {
		writeLLMGatewayProxyError(response, http.StatusBadRequest, "invalid_argument", err.Error())
		return
	}
	endpoint := *proxy.coreOrigin
	endpoint.Path = request.URL.Path
	upstream, err := http.NewRequestWithContext(request.Context(), request.Method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		writeLLMGatewayProxyError(response, http.StatusBadGateway, "core_unavailable", "could not construct the Core request")
		return
	}
	upstream.Header.Set("Authorization", authorizations[0])
	upstream.Header.Set("Accept", "application/json")
	if contentType := request.Header.Get("Content-Type"); contentType != "" {
		upstream.Header.Set("Content-Type", contentType)
	}

	result, err := proxy.client.Do(upstream)
	if err != nil {
		writeLLMGatewayProxyError(response, http.StatusBadGateway, "core_unavailable", "Core workspace LLM gateway API is unavailable")
		return
	}
	defer result.Body.Close()
	mediaType, parameters, mediaErr := mime.ParseMediaType(result.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" || len(parameters) != 0 {
		writeLLMGatewayProxyError(response, http.StatusBadGateway, "invalid_core_response", "Core returned an invalid workspace LLM gateway response")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(result.Body, maximumLLMGatewayProxyResponseBytes+1))
	if err != nil || int64(len(raw)) > maximumLLMGatewayProxyResponseBytes {
		writeLLMGatewayProxyError(response, http.StatusBadGateway, "invalid_core_response", "Core workspace LLM gateway response exceeded its bound")
		return
	}
	if challenge := result.Header.Get("WWW-Authenticate"); challenge != "" && len(challenge) <= 1024 && !strings.ContainsAny(challenge, "\x00\r\n") {
		response.Header().Set("WWW-Authenticate", challenge)
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(result.StatusCode)
	_, _ = response.Write(raw)
}

func readLLMGatewayProxyRequestBody(request *http.Request) ([]byte, error) {
	if request.Body == nil {
		return nil, nil
	}
	if request.ContentLength > maximumLLMGatewayProxyRequestBytes {
		return nil, errors.New("workspace LLM gateway request exceeds its size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, maximumLLMGatewayProxyRequestBytes+1))
	if err != nil {
		return nil, errors.New("could not read workspace LLM gateway request")
	}
	if int64(len(raw)) > maximumLLMGatewayProxyRequestBytes {
		return nil, errors.New("workspace LLM gateway request exceeds its size limit")
	}
	return raw, nil
}

func writeLLMGatewayProxyError(response http.ResponseWriter, status int, code, message string) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_, _ = fmt.Fprintf(response, "{\"error\":{\"code\":%q,\"message\":%q}}\n", code, message)
}
