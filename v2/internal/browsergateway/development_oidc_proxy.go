package browsergateway

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const maximumDevelopmentOIDCAuthorizationResponseBytes = int64(64 * 1024)

// DevelopmentOIDCAuthorizationProxy exposes only the browser-facing
// authorization endpoint of the loopback development IdP. Discovery, token,
// and JWKS traffic remain private Core-to-fixture calls.
type DevelopmentOIDCAuthorizationProxy struct {
	upstream *url.URL
	client   *http.Client
}

func NewDevelopmentOIDCAuthorizationProxy(upstream string, client *http.Client) (*DevelopmentOIDCAuthorizationProxy, error) {
	if client == nil {
		return nil, errors.New("development OIDC authorization proxy HTTP client is required")
	}
	parsed, err := url.Parse(upstream)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("development OIDC upstream must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && hydraLoopbackHost(parsed.Hostname())) {
		return nil, errors.New("cleartext development OIDC upstream is allowed only on explicit loopback")
	}
	parsed.Path = ""
	clientCopy := *client
	clientCopy.Jar = nil
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &DevelopmentOIDCAuthorizationProxy{upstream: parsed, client: &clientCopy}, nil
}

func (proxy *DevelopmentOIDCAuthorizationProxy) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/idp/authorize", proxy.forward)
	return mux
}

func (proxy *DevelopmentOIDCAuthorizationProxy) forward(response http.ResponseWriter, request *http.Request) {
	setDevelopmentOIDCProxyHeaders(response.Header())
	if proxy == nil || proxy.upstream == nil || proxy.client == nil {
		http.Error(response, "development identity provider unavailable", http.StatusServiceUnavailable)
		return
	}
	// The same-origin login transaction cookie is expected here. It is never
	// copied to the private fixture request.
	if request.ContentLength != 0 || len(request.TransferEncoding) != 0 || request.Header.Get("Content-Encoding") != "" ||
		request.Header.Get("Authorization") != "" || request.URL.RawQuery == "" || len(request.URL.RawQuery) > 32*1024 {
		http.Error(response, "invalid development identity authorization request", http.StatusBadRequest)
		return
	}
	endpoint := *proxy.upstream
	endpoint.Path = "/idp/authorize"
	endpoint.RawQuery = request.URL.RawQuery
	upstream, err := http.NewRequestWithContext(request.Context(), http.MethodGet, endpoint.String(), nil)
	if err != nil {
		http.Error(response, "development identity provider unavailable", http.StatusServiceUnavailable)
		return
	}
	upstream.Header.Set("Accept", "application/json")
	result, err := proxy.client.Do(upstream)
	if err != nil {
		http.Error(response, "development identity provider unavailable", http.StatusServiceUnavailable)
		return
	}
	defer result.Body.Close()
	body, err := io.ReadAll(io.LimitReader(result.Body, maximumDevelopmentOIDCAuthorizationResponseBytes+1))
	if err != nil || int64(len(body)) > maximumDevelopmentOIDCAuthorizationResponseBytes || !validDevelopmentOIDCAuthorizationStatus(result.StatusCode) {
		http.Error(response, "development identity provider returned an invalid response", http.StatusBadGateway)
		return
	}
	location := result.Header.Get("Location")
	if result.StatusCode == http.StatusFound {
		if !validDevelopmentOIDCCallback(location, request.Host) {
			http.Error(response, "development identity provider returned an invalid redirect", http.StatusBadGateway)
			return
		}
		response.Header().Set("Location", location)
	} else if location != "" {
		http.Error(response, "development identity provider returned an invalid response", http.StatusBadGateway)
		return
	}
	if contentType := result.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || (mediaType != "application/json" && len(body) != 0) {
			http.Error(response, "development identity provider returned an invalid content type", http.StatusBadGateway)
			return
		}
		response.Header().Set("Content-Type", "application/json")
	}
	response.Header().Set("Content-Length", strconv.Itoa(len(body)))
	response.WriteHeader(result.StatusCode)
	_, _ = response.Write(body)
}

func validDevelopmentOIDCAuthorizationStatus(status int) bool {
	return status == http.StatusFound || status == http.StatusBadRequest || status == http.StatusServiceUnavailable
}

func validDevelopmentOIDCCallback(raw, requestHost string) bool {
	publicOrigin, err := url.Parse("https://" + requestHost)
	if err != nil || publicOrigin.Host == "" || publicOrigin.Hostname() == "" || publicOrigin.User != nil ||
		publicOrigin.Path != "" || publicOrigin.RawQuery != "" || publicOrigin.Fragment != "" {
		return false
	}
	parsed, err := url.Parse(raw)
	return err == nil && len(raw) <= 8192 && !strings.ContainsAny(raw, "\x00\r\n") &&
		!strings.Contains(raw, "#") && parsed.Scheme+"://"+parsed.Host == publicOrigin.Scheme+"://"+publicOrigin.Host &&
		parsed.User == nil && parsed.Path == "/auth/oidc/callback" && parsed.RawPath == "" && parsed.RawQuery != "" && parsed.Fragment == ""
}

func setDevelopmentOIDCProxyHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
	header.Set("Pragma", "no-cache")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}
