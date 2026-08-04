// Package a2uiweb embeds the Browser production SPA into browser-gateway.
package a2uiweb

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

const contentSecurityPolicy = "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; img-src 'self' data:; font-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'"

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
		panic(fmt.Sprintf("open embedded Browser bundle: %v", err))
	}
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		panic(fmt.Sprintf("read embedded Browser index: %v", err))
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

func Handler() http.Handler { return assetHandler{contentSecurityPolicy: contentSecurityPolicy} }
func HandlerForAPIOrigin(apiOrigin string) (http.Handler, error) {
	return HandlerForConnectionOrigins(apiOrigin)
}

func HandlerForConnectionOrigins(origins ...string) (http.Handler, error) {
	if len(origins) < 1 || len(origins) > 2 {
		return nil, fmt.Errorf("Browser web requires one or two connection origins")
	}
	seen := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		if err := validateOrigin(origin); err != nil {
			return nil, fmt.Errorf("Browser web connection origin must be an exact HTTPS origin")
		}
		if _, duplicate := seen[origin]; duplicate {
			return nil, fmt.Errorf("Browser web connection origins must be unique")
		}
		seen[origin] = struct{}{}
	}
	return assetHandler{contentSecurityPolicy: contentSecurityPolicy + " " + strings.Join(origins, " ")}, nil
}

type assetHandler struct{ contentSecurityPolicy string }

func (handler assetHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	setSecurityHeaders(response.Header(), handler.contentSecurityPolicy)
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		response.Header().Set("Allow", "GET, HEAD")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if request.URL.Path == "/" || request.URL.Path == "/index.html" || isBrowserProductRoute(request.URL.Path) {
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

func isBrowserProductRoute(raw string) bool {
	parts := strings.Split(strings.Trim(strings.TrimSuffix(raw, "/"), "/"), "/")
	return len(parts) == 2 && parts[0] == "workspaces" && canonicalUUID(parts[1])
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
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", policy)
	header.Set("Cross-Origin-Opener-Policy", "same-origin-allow-popups")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}

func AssetSummary() string { return fmt.Sprintf("browser web (%d embedded assets)", bundle.count) }
