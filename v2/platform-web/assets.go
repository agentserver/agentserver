// Package platformweb provides the dependency-free Platform control-plane
// shell embedded into platform-gateway. It deliberately contains no Browser
// conversation UI or persistent access-token storage.
package platformweb

import (
	_ "embed"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const contentSecurityPolicy = "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; img-src 'self'; font-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'"

//go:embed index.html
var indexHTML []byte

//go:embed app.js
var applicationJavaScript []byte

//go:embed auth.js
var authorizationJavaScript []byte

//go:embed resources.js
var resourceJavaScript []byte

//go:embed llm-gateways.js
var llmGatewayJavaScript []byte

//go:embed styles.css
var styleSheet []byte

type asset struct {
	contentType string
	contents    []byte
}

var assets = map[string]asset{
	"/":                         {contentType: "text/html; charset=utf-8", contents: indexHTML},
	"/index.html":               {contentType: "text/html; charset=utf-8", contents: indexHTML},
	"/platform/app.js":          {contentType: "text/javascript; charset=utf-8", contents: applicationJavaScript},
	"/platform/auth.js":         {contentType: "text/javascript; charset=utf-8", contents: authorizationJavaScript},
	"/platform/resources.js":    {contentType: "text/javascript; charset=utf-8", contents: resourceJavaScript},
	"/platform/llm-gateways.js": {contentType: "text/javascript; charset=utf-8", contents: llmGatewayJavaScript},
	"/platform/styles.css":      {contentType: "text/css; charset=utf-8", contents: styleSheet},
}

// Handler returns a closed static asset handler without an SPA fallback.
func Handler() http.Handler { return assetHandler{contentSecurityPolicy: contentSecurityPolicy} }

// HandlerForOAuthOrigin allows the Platform SPA to exchange its public-client
// PKCE code with one exact, separately hosted OAuth authority.
func HandlerForOAuthOrigin(origin string) (http.Handler, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.String() != origin {
		return nil, fmt.Errorf("platform web OAuth origin must be an exact HTTPS origin")
	}
	return assetHandler{contentSecurityPolicy: contentSecurityPolicy + " " + origin}, nil
}

type assetHandler struct{ contentSecurityPolicy string }

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
	_, _ = response.Write(requested.contents)
}

func setSecurityHeaders(header http.Header, policy string) {
	if strings.TrimSpace(policy) == "" {
		policy = contentSecurityPolicy
	}
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", policy)
	header.Set("Cross-Origin-Opener-Policy", "same-origin-allow-popups")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}

func AssetSummary() string { return fmt.Sprintf("platform web (%d embedded assets)", len(assets)) }
