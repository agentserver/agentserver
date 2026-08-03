package browsergateway

import (
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const maximumHydraPublicResponseBytes = int64(512 * 1024)

type HydraPublicProxy struct {
	upstream *url.URL
	client   *http.Client
}

func NewHydraPublicProxy(upstream string, client *http.Client) (*HydraPublicProxy, error) {
	if client == nil {
		return nil, errors.New("Hydra public proxy HTTP client is required")
	}
	parsed, err := url.Parse(upstream)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("Hydra public upstream must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && hydraLoopbackHost(parsed.Hostname())) {
		return nil, errors.New("cleartext Hydra public upstream is allowed only on explicit loopback")
	}
	parsed.Path = ""
	clientCopy := *client
	// Browser cookies are never Hydra authority. A fresh upstream request already
	// drops request headers; clearing the jar also prevents ambient client state
	// from adding a Cookie header behind the proxy's back.
	clientCopy.Jar = nil
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HydraPublicProxy{upstream: parsed, client: &clientCopy}, nil
}

func (proxy *HydraPublicProxy) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth2/auth", proxy.forwardAuthorization)
	mux.HandleFunc("POST /oauth2/token", proxy.forwardToken)
	return mux
}

func (proxy *HydraPublicProxy) forwardAuthorization(response http.ResponseWriter, request *http.Request) {
	setHydraProxyHeaders(response.Header())
	if request.ContentLength != 0 || len(request.TransferEncoding) != 0 || request.Header.Get("Authorization") != "" ||
		request.URL.RawQuery == "" || len(request.URL.RawQuery) > 32*1024 {
		http.Error(response, "invalid authorization request", http.StatusBadRequest)
		return
	}
	proxy.forward(response, request, http.MethodGet, "/oauth2/auth", nil, "")
}

func (proxy *HydraPublicProxy) forwardToken(response http.ResponseWriter, request *http.Request) {
	setHydraProxyHeaders(response.Header())
	mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" || len(parameters) != 0 ||
		len(request.Header.Values("Content-Type")) != 1 || request.Header.Get("Authorization") != "" ||
		request.Header.Get("Content-Encoding") != "" ||
		len(request.TransferEncoding) != 0 || request.ContentLength < 1 || request.ContentLength > maximumHydraPublicResponseBytes ||
		request.URL.RawQuery != "" {
		http.Error(response, "invalid token request", http.StatusBadRequest)
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumHydraPublicResponseBytes)
	body, err := io.ReadAll(request.Body)
	if err != nil || int64(len(body)) != request.ContentLength {
		http.Error(response, "invalid token request", http.StatusBadRequest)
		return
	}
	proxy.forward(response, request, http.MethodPost, "/oauth2/token", strings.NewReader(string(body)), "application/x-www-form-urlencoded")
}

func (proxy *HydraPublicProxy) forward(
	response http.ResponseWriter,
	request *http.Request,
	method, path string,
	body io.Reader,
	contentType string,
) {
	if proxy == nil || proxy.upstream == nil || proxy.client == nil {
		http.Error(response, "authorization service unavailable", http.StatusServiceUnavailable)
		return
	}
	endpoint := *proxy.upstream
	endpoint.Path = path
	endpoint.RawQuery = request.URL.RawQuery
	upstream, err := http.NewRequestWithContext(request.Context(), method, endpoint.String(), body)
	if err != nil {
		http.Error(response, "authorization service unavailable", http.StatusServiceUnavailable)
		return
	}
	upstream.Header.Set("Accept", "application/json")
	if contentType != "" {
		upstream.Header.Set("Content-Type", contentType)
	}
	result, err := proxy.client.Do(upstream)
	if err != nil {
		http.Error(response, "authorization service unavailable", http.StatusServiceUnavailable)
		return
	}
	defer result.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(result.Body, maximumHydraPublicResponseBytes+1))
	if err != nil || int64(len(raw)) > maximumHydraPublicResponseBytes || !validHydraPublicStatus(method, result.StatusCode) {
		http.Error(response, "authorization service returned an invalid response", http.StatusBadGateway)
		return
	}
	if result.StatusCode == http.StatusFound {
		locations := result.Header.Values("Location")
		if len(locations) != 1 {
			http.Error(response, "authorization service returned an invalid redirect", http.StatusBadGateway)
			return
		}
		location := locations[0]
		parsed, err := url.Parse(location)
		if method != http.MethodGet || err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" ||
			parsed.User != nil || len(location) > 8192 || strings.ContainsAny(location, "\x00\r\n") {
			http.Error(response, "authorization service returned an invalid redirect", http.StatusBadGateway)
			return
		}
		response.Header().Set("Location", location)
		// Hydra 26 returns a text/html convenience body and a Hydra cookie with
		// its authorization redirect. Neither is browser authority at this
		// boundary: the validated Location is sufficient and is the only
		// upstream response value that crosses the proxy.
		response.Header().Set("Content-Length", "0")
		response.WriteHeader(result.StatusCode)
		return
	}
	if contentType := result.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || (mediaType != "application/json" && len(raw) != 0) {
			http.Error(response, "authorization service returned an invalid content type", http.StatusBadGateway)
			return
		}
		response.Header().Set("Content-Type", "application/json")
	}
	response.Header().Set("Content-Length", strconv.Itoa(len(raw)))
	response.WriteHeader(result.StatusCode)
	_, _ = response.Write(raw)
}

func validHydraPublicStatus(method string, status int) bool {
	if method == http.MethodGet {
		return status == http.StatusFound || status == http.StatusBadRequest || status == http.StatusServiceUnavailable
	}
	return status == http.StatusOK || status == http.StatusBadRequest || status == http.StatusUnauthorized ||
		status == http.StatusServiceUnavailable
}

func setHydraProxyHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
	header.Set("Pragma", "no-cache")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}

func hydraLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
