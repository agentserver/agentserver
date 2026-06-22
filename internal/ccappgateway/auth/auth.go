package auth

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// InternalSecretMiddleware returns a middleware that validates the X-Internal-Secret header.
// If secret is empty, the middleware is permissive (allows all requests).
// If secret is configured and the header matches, the next handler is invoked.
// Otherwise, the middleware responds with 401 Unauthorized.
func InternalSecretMiddleware(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Permissive if secret is empty (useful for local dev)
			if secret == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Check for X-Internal-Secret header
			headerValue := r.Header.Get("X-Internal-Secret")
			if headerValue == "" {
				// Missing header
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "missing auth"})
				return
			}

			// Constant-time comparison
			if subtle.ConstantTimeCompare([]byte(headerValue), []byte(secret)) != 1 {
				// Wrong secret
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid secret"})
				return
			}

			// Secret matches, invoke next handler
			next.ServeHTTP(w, r)
		})
	}
}

// BearerMiddleware returns a middleware that handles Bearer token authentication.
// In Phase 1, this is a stub: if a Bearer token is present in the Authorization header,
// it responds with 501 Not Implemented. Otherwise, it passes the request through to the next handler.
func BearerMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")

			// Check if the Authorization header contains a Bearer token
			if strings.HasPrefix(authHeader, "Bearer ") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotImplemented)
				json.NewEncoder(w).Encode(map[string]string{"error": "bearer auth not implemented in phase 1"})
				return
			}

			// No Bearer token, pass through to next handler
			next.ServeHTTP(w, r)
		})
	}
}

// Either returns a middleware that composes internal and bearer middlewares.
// For each request, it checks credentials in order:
// 1. If X-Internal-Secret header is present, route through internal middleware
// 2. Else if Authorization: Bearer is present, route through bearer middleware
// 3. Else respond with 401 Unauthorized
func Either(internal, bearer func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			xInternalSecret := r.Header.Get("X-Internal-Secret")
			authHeader := r.Header.Get("Authorization")

			// Check if X-Internal-Secret header is present
			if xInternalSecret != "" {
				// Route through internal middleware
				internalHandler := internal(next)
				internalHandler.ServeHTTP(w, r)
				return
			}

			// Check if Bearer token is present
			if strings.HasPrefix(authHeader, "Bearer ") {
				// Route through bearer middleware
				bearerHandler := bearer(next)
				bearerHandler.ServeHTTP(w, r)
				return
			}

			// Neither credential type present
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "missing auth"})
		})
	}
}
