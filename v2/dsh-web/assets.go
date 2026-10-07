// Package dshweb embeds the DSH Web production bundle into the v2
// browser-gateway. The gateway mounts the handler below at the dedicated DSH
// host and keeps
// the DSH RPC/WebSocket facade on the same origin.
package dshweb

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
)

const contentSecurityPolicy = "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; img-src 'self' data:; font-src 'self' data:; script-src 'self' 'unsafe-inline' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; connect-src 'self'"

//go:embed all:dist
var embedded embed.FS

var bundle = mustBundle()

type staticBundle struct {
	files           fs.FS
	index           []byte
	count           int
	pluginResources map[string]string
	err             error
}

func mustBundle() staticBundle {
	files, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(fmt.Sprintf("open embedded DSH bundle: %v", err))
	}
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		return staticBundle{err: errors.New("DSH frontend is not built; run bash v2/dsh-web/build.sh before building browser-gateway")}
	}
	count := 0
	_ = fs.WalkDir(files, ".", func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() {
			count++
		}
		return walkErr
	})
	resources := map[string]string{}
	{
		raw, readErr := fs.ReadFile(files, "plugin-resources.json")
		if readErr != nil {
			panic(fmt.Sprintf("open DSH plugin resource map: %v", readErr))
		}
		if err := json.Unmarshal(raw, &resources); err != nil {
			panic(fmt.Sprintf("read DSH plugin resource map: %v", err))
		}
		if len(resources) == 0 {
			panic("empty DSH plugin resource map")
		}
		for uri, name := range resources {
			if !strings.HasPrefix(uri, "/plugins/") || !strings.HasPrefix(name, "plugins/") || !fs.ValidPath(name) {
				panic("invalid DSH plugin resource mapping")
			}
			if _, err := fs.Stat(files, name); err != nil {
				panic(fmt.Sprintf("DSH plugin resource missing: %s", name))
			}
		}
	}
	return staticBundle{files: files, index: withAuthenticationBootstrap(index), count: count, pluginResources: resources}
}

// Handler returns the DSH static bundle. API and WebSocket paths are mounted
// by the browser-gateway caller so this handler remains a pure asset server.
func Handler() http.Handler { return assetHandler{contentSecurityPolicy: contentSecurityPolicy} }

// HandlerForOAuthOrigin adds the exact Hydra origin used by the browser PKCE
// exchange to connect-src while keeping all other connection authorities out.
func HandlerForOAuthOrigin(origin string) (http.Handler, error) {
	if origin == "" {
		return nil, fmt.Errorf("DSH OAuth origin is required")
	}
	if bundle.err != nil {
		return nil, bundle.err
	}
	return assetHandler{contentSecurityPolicy: contentSecurityPolicy + " " + origin}, nil
}

type assetHandler struct{ contentSecurityPolicy string }

func (handler assetHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	setSecurityHeaders(response.Header(), handler.contentSecurityPolicy)
	if bundle.err != nil {
		http.Error(response, bundle.err.Error(), http.StatusServiceUnavailable)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		response.Header().Set("Allow", "GET, HEAD")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/plugins/") || request.URL.Path == "/plugins" {
		name, ok := bundle.pluginResources[request.URL.RequestURI()]
		if !ok {
			http.NotFound(response, request)
			return
		}
		contents, err := fs.ReadFile(bundle.files, name)
		if err != nil {
			http.NotFound(response, request)
			return
		}
		serveAsset(response, request, contents, "text/javascript; charset=utf-8", true)
		return
	}
	cleaned := path.Clean(request.URL.Path)
	if cleaned == "." || cleaned == "/" || cleaned == "/index.html" || isDSHRoute(cleaned) {
		serveAsset(response, request, bundle.index, "text/html; charset=utf-8", false)
		return
	}
	name := strings.TrimPrefix(cleaned, "/")
	contents, err := fs.ReadFile(bundle.files, name)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	serveAsset(response, request, contents, assetContentType(name), true)
}

func isDSHRoute(name string) bool {
	if strings.HasPrefix(name, "/assets/") || strings.HasPrefix(name, "/plugins/") {
		return false
	}
	return name != "/plugins-dsh-boot.js" && name != "/favicon.svg" && name != "/favicon-dark.svg" && name != "/manifest.webmanifest"
}

func serveAsset(response http.ResponseWriter, request *http.Request, contents []byte, contentType string, immutable bool) {
	response.Header().Set("Content-Type", contentType)
	response.Header().Set("Content-Length", strconv.Itoa(len(contents)))
	if immutable {
		digest := sha256.Sum256(contents)
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		response.Header().Set("ETag", `"`+hex.EncodeToString(digest[:])+`"`)
	} else {
		response.Header().Set("Cache-Control", "no-store")
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
	case ".svg":
		return "image/svg+xml"
	case ".webmanifest":
		return "application/manifest+json"
	case ".woff", ".woff2", ".ttf":
		return "font/*"
	default:
		return "application/octet-stream"
	}
}

func setSecurityHeaders(header http.Header, policy string) {
	header.Set("Content-Security-Policy", policy)
	header.Set("Cross-Origin-Opener-Policy", "same-origin-allow-popups")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}

// AssetSummary is used by production smoke tests and diagnostics.
func AssetSummary() string { return fmt.Sprintf("DSH web (%d embedded assets)", bundle.count) }
