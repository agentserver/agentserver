// Package platformweb embeds the closed Platform production bundle into
// platform-gateway. Product routes have an explicit SPA fallback; API and
// unknown asset paths never do.
package platformweb

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

const contentSecurityPolicy = "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; img-src 'self'; font-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'"

//go:embed all:dist
var embedded embed.FS

var bundle = mustBundle()

type staticBundle struct {
	files fs.FS
	index []byte
	count int
}

func mustBundle() staticBundle {
	files, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(fmt.Sprintf("open embedded Platform bundle: %v", err))
	}
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		panic(fmt.Sprintf("read embedded Platform index: %v", err))
	}
	count := 0
	_ = fs.WalkDir(files, ".", func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() {
			count++
		}
		return walkErr
	})
	return staticBundle{files: files, index: index, count: count}
}

// Handler returns the production bundle with no cross-origin connection authority.
func Handler() http.Handler { return assetHandler{contentSecurityPolicy: contentSecurityPolicy} }

// HandlerForOAuthOrigin allows only the exact public OAuth authority used for
// public-client PKCE exchange.
func HandlerForOAuthOrigin(origin string) (http.Handler, error) {
	if err := validateOrigin(origin); err != nil {
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
	if request.URL.Path == "/" || request.URL.Path == "/index.html" || isPlatformProductRoute(request.URL.Path) {
		serveAsset(response, request, bundle.index, "text/html; charset=utf-8", false)
		return
	}
	if !strings.HasPrefix(request.URL.Path, "/assets/") {
		http.NotFound(response, request)
		return
	}
	name := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
	contents, err := fs.ReadFile(bundle.files, name)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	serveAsset(response, request, contents, assetContentType(name), true)
}

func isPlatformProductRoute(raw string) bool {
	cleaned := strings.TrimSuffix(raw, "/")
	if cleaned == "/workspaces" {
		return true
	}
	parts := strings.Split(strings.TrimPrefix(cleaned, "/"), "/")
	if len(parts) < 2 || len(parts) > 3 || parts[0] != "workspaces" || !canonicalUUID(parts[1]) {
		return false
	}
	return len(parts) == 2 || parts[2] == "overview" || parts[2] == "members" || parts[2] == "executors" || parts[2] == "gateways"
}

func canonicalUUID(value string) bool {
	if len(value) != 36 || value == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func serveAsset(response http.ResponseWriter, request *http.Request, contents []byte, contentType string, immutable bool) {
	response.Header().Set("Content-Type", contentType)
	response.Header().Set("Content-Length", strconv.Itoa(len(contents)))
	if immutable {
		digest := sha256.Sum256(contents)
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		response.Header().Set("ETag", `"`+hex.EncodeToString(digest[:])+`"`)
	}
	response.WriteHeader(http.StatusOK)
	if request.Method == http.MethodGet {
		_, _ = response.Write(contents)
	}
}

func assetContentType(name string) string {
	switch path.Ext(name) {
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

func validateOrigin(origin string) error {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.String() != origin {
		return fmt.Errorf("invalid origin")
	}
	return nil
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

func AssetSummary() string { return fmt.Sprintf("platform web (%d embedded assets)", bundle.count) }
