// Package a2uiweb provides the dependency-free reference browser client for
// the Agentserver v2 AG-UI boundary. The assets are embedded into
// browser-gateway so the reference client and API always share an origin.
package a2uiweb

import (
	_ "embed"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const contentSecurityPolicy = "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; img-src 'self' data:; font-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'"

//go:embed index.html
var indexHTML []byte

//go:embed app.js
var applicationJavaScript []byte

//go:embed protocol.js
var protocolJavaScript []byte

//go:embed auth.js
var authorizationJavaScript []byte

//go:embed llm-gateways.js
var llmGatewaysJavaScript []byte

//go:embed styles.css
var styleSheet []byte

type asset struct {
	contentType string
	contents    []byte
}

var assets = map[string]asset{
	"/":                          {contentType: "text/html; charset=utf-8", contents: indexHTML},
	"/index.html":                {contentType: "text/html; charset=utf-8", contents: indexHTML},
	"/reference/app.js":          {contentType: "text/javascript; charset=utf-8", contents: applicationJavaScript},
	"/reference/protocol.js":     {contentType: "text/javascript; charset=utf-8", contents: protocolJavaScript},
	"/reference/auth.js":         {contentType: "text/javascript; charset=utf-8", contents: authorizationJavaScript},
	"/reference/llm-gateways.js": {contentType: "text/javascript; charset=utf-8", contents: llmGatewaysJavaScript},
	"/reference/styles.css":      {contentType: "text/css; charset=utf-8", contents: styleSheet},
}

// Handler returns a closed static-file handler. It deliberately has no SPA
// fallback: an unknown API or asset path must remain a visible 404.
func Handler() http.Handler {
	return assetHandler{contentSecurityPolicy: contentSecurityPolicy}
}

// HandlerForAPIOrigin serves the same closed asset set while allowing the
// reviewed reference client to connect to one exact cross-origin AG-UI API.
func HandlerForAPIOrigin(apiOrigin string) (http.Handler, error) {
	return HandlerForConnectionOrigins(apiOrigin)

}

// HandlerForConnectionOrigins serves the closed asset set while allowing the
// reference client to call only the reviewed HTTPS API and OAuth authorities.
func HandlerForConnectionOrigins(origins ...string) (http.Handler, error) {
	if len(origins) < 1 || len(origins) > 2 {
		return nil, fmt.Errorf("reference web requires one or two connection origins")
	}
	seen := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
			parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.String() != origin {
			return nil, fmt.Errorf("reference web connection origin must be an exact HTTPS origin")
		}
		if _, duplicate := seen[origin]; duplicate {
			return nil, fmt.Errorf("reference web connection origins must be unique")
		}
		seen[origin] = struct{}{}
	}
	return assetHandler{contentSecurityPolicy: contentSecurityPolicy + " " + strings.Join(origins, " ")}, nil
}

type assetHandler struct {
	contentSecurityPolicy string
}

func (handler assetHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	setSecurityHeaders(response.Header(), handler.contentSecurityPolicy)
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		response.Header().Set("Allow", "GET, HEAD")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	requested, exists := assets[request.URL.Path]
	if !exists {
		http.NotFound(response, request)
		return
	}
	response.Header().Set("Content-Type", requested.contentType)
	response.Header().Set("Content-Length", strconv.Itoa(len(requested.contents)))
	response.WriteHeader(http.StatusOK)
	if request.Method == http.MethodHead {
		return
	}
	if _, err := response.Write(requested.contents); err != nil {
		return
	}
}

func setSecurityHeaders(header http.Header, policy string) {
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", policy)
	// The workspace Gateway OIDC flow uses a same-origin callback popup. The
	// allow-popups variant deliberately preserves its opener while the popup is
	// temporarily navigated through the third-party authorization endpoint.
	header.Set("Cross-Origin-Opener-Policy", "same-origin-allow-popups")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}

// AssetSummary is intentionally small and secret-free; it is useful in the
// command startup log and tests without exposing embedded bytes.
func AssetSummary() string {
	return fmt.Sprintf("reference web (%d embedded assets)", len(assets))
}
