package browsergateway

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const maximumAuthBridgeResponseBytes = int64(64 * 1024)

type AuthBridgeProxy struct {
	coreOrigin *url.URL
	client     *http.Client
}

func NewAuthBridgeProxy(coreOrigin string, client *http.Client) (*AuthBridgeProxy, error) {
	if client == nil {
		return nil, errors.New("auth bridge Core HTTP client is required")
	}
	parsed, err := url.Parse(coreOrigin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("auth bridge Core URL must be an HTTPS origin without credentials, path, query, or fragment")
	}
	parsed.Path = ""
	clientCopy := *client
	clientCopy.Jar = nil
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &AuthBridgeProxy{coreOrigin: parsed, client: &clientCopy}, nil
}

func (proxy *AuthBridgeProxy) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+corecontract.PublicHydraLoginPath, proxy.forward(corecontract.HydraLoginBridgePath))
	mux.HandleFunc("GET "+corecontract.PublicHydraConsentPath, proxy.forward(corecontract.HydraConsentBridgePath))
	mux.HandleFunc("GET "+corecontract.PublicOIDCCallbackPath, proxy.forward(corecontract.OIDCCallbackBridgePath))
	return mux
}

func (proxy *AuthBridgeProxy) forward(corePath string) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		setAuthProxySecurityHeaders(response.Header())
		if proxy == nil || proxy.coreOrigin == nil || proxy.client == nil {
			http.Error(response, "authorization bridge unavailable", http.StatusServiceUnavailable)
			return
		}
		if request.ContentLength != 0 || len(request.TransferEncoding) != 0 || request.Header.Get("Authorization") != "" ||
			!validAuthBridgeQuery(request.URL.RawQuery) {
			http.Error(response, "invalid authorization bridge request", http.StatusBadRequest)
			return
		}
		endpoint := *proxy.coreOrigin
		endpoint.Path = corePath
		endpoint.RawQuery = request.URL.RawQuery
		upstream, err := http.NewRequestWithContext(request.Context(), http.MethodGet, endpoint.String(), nil)
		if err != nil {
			http.Error(response, "authorization bridge unavailable", http.StatusServiceUnavailable)
			return
		}
		upstream.Header.Set("Accept", "text/html,application/xhtml+xml")
		for _, cookie := range request.Header.Values("Cookie") {
			upstream.Header.Add("Cookie", cookie)
		}
		result, err := proxy.client.Do(upstream)
		if err != nil {
			http.Error(response, "authorization bridge unavailable", http.StatusServiceUnavailable)
			return
		}
		defer result.Body.Close()
		body, err := io.ReadAll(io.LimitReader(result.Body, maximumAuthBridgeResponseBytes+1))
		if err != nil || int64(len(body)) > maximumAuthBridgeResponseBytes {
			http.Error(response, "authorization bridge returned an invalid response", http.StatusBadGateway)
			return
		}
		if !validAuthBridgeStatus(result.StatusCode) {
			http.Error(response, "authorization bridge returned an invalid response", http.StatusBadGateway)
			return
		}
		for _, name := range []string{
			"Cache-Control", "Content-Security-Policy", "Content-Type", "Cross-Origin-Opener-Policy",
			"Location", "Referrer-Policy", "X-Content-Type-Options", "X-Frame-Options",
		} {
			if value := result.Header.Get(name); value != "" {
				response.Header().Set(name, value)
			}
		}
		for _, cookie := range result.Header.Values("Set-Cookie") {
			response.Header().Add("Set-Cookie", cookie)
		}
		if location := response.Header().Get("Location"); location != "" {
			parsed, err := url.Parse(location)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || len(location) > 8192 || strings.ContainsAny(location, "\x00\r\n") {
				response.Header().Del("Location")
				http.Error(response, "authorization bridge returned an invalid redirect", http.StatusBadGateway)
				return
			}
		}
		response.Header().Set("Content-Length", strconv.Itoa(len(body)))
		response.WriteHeader(result.StatusCode)
		_, _ = response.Write(body)
	}
}

func validAuthBridgeQuery(raw string) bool {
	if raw == "" || len(raw) > 32*1024 || strings.ContainsAny(raw, "\x00\r\n") {
		return false
	}
	_, err := url.ParseQuery(raw)
	return err == nil
}

func validAuthBridgeStatus(status int) bool {
	return status == http.StatusFound || status == http.StatusBadRequest || status == http.StatusForbidden ||
		status == http.StatusServiceUnavailable
}

func setAuthProxySecurityHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
	header.Set("Cross-Origin-Opener-Policy", "same-origin")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}
