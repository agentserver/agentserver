// Package a2uiweb provides the dependency-free reference browser client for
// the Agentserver v2 AG-UI boundary. The assets are embedded into
// browser-gateway so the reference client and API always share an origin.
package a2uiweb

import (
	_ "embed"
	"fmt"
	"net/http"
	"strconv"
)

const contentSecurityPolicy = "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; img-src 'self' data:; font-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'"

//go:embed index.html
var indexHTML []byte

//go:embed app.js
var applicationJavaScript []byte

//go:embed protocol.js
var protocolJavaScript []byte

//go:embed styles.css
var styleSheet []byte

type asset struct {
	contentType string
	contents    []byte
}

var assets = map[string]asset{
	"/":                      {contentType: "text/html; charset=utf-8", contents: indexHTML},
	"/index.html":            {contentType: "text/html; charset=utf-8", contents: indexHTML},
	"/reference/app.js":      {contentType: "text/javascript; charset=utf-8", contents: applicationJavaScript},
	"/reference/protocol.js": {contentType: "text/javascript; charset=utf-8", contents: protocolJavaScript},
	"/reference/styles.css":  {contentType: "text/css; charset=utf-8", contents: styleSheet},
}

// Handler returns a closed static-file handler. It deliberately has no SPA
// fallback: an unknown API or asset path must remain a visible 404.
func Handler() http.Handler {
	return http.HandlerFunc(serveAsset)
}

func serveAsset(response http.ResponseWriter, request *http.Request) {
	setSecurityHeaders(response.Header())
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

func setSecurityHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", contentSecurityPolicy)
	header.Set("Cross-Origin-Opener-Policy", "same-origin")
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
