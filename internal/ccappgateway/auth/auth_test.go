package auth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestInternalSecretMiddleware tests the X-Internal-Secret authentication middleware.
func TestInternalSecretMiddleware(t *testing.T) {
	tests := []struct {
		name          string
		secret        string
		headerValue   string
		expectStatus  int
		expectHandled bool
		description   string
	}{
		{
			name:          "missing X-Internal-Secret header when secret is configured",
			secret:        "configured-secret",
			headerValue:   "",
			expectStatus:  http.StatusUnauthorized,
			expectHandled: false,
			description:   "Test 1: Missing X-Internal-Secret + secret is configured → 401",
		},
		{
			name:          "wrong X-Internal-Secret value",
			secret:        "configured-secret",
			headerValue:   "wrong-secret",
			expectStatus:  http.StatusUnauthorized,
			expectHandled: false,
			description:   "Test 2: Wrong X-Internal-Secret value → 401",
		},
		{
			name:          "matching X-Internal-Secret",
			secret:        "configured-secret",
			headerValue:   "configured-secret",
			expectStatus:  http.StatusOK,
			expectHandled: true,
			description:   "Test 3: Matching X-Internal-Secret → next handler invoked",
		},
		{
			name:          "empty secret config is permissive",
			secret:        "",
			headerValue:   "any-value",
			expectStatus:  http.StatusOK,
			expectHandled: true,
			description:   "Test 4: Empty secret config → permissive (next invoked regardless)",
		},
		{
			name:          "empty secret config is permissive with missing header",
			secret:        "",
			headerValue:   "",
			expectStatus:  http.StatusOK,
			expectHandled: true,
			description:   "Test 4b: Empty secret config with missing header → permissive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Sentinel to track if next handler was invoked
			handlerInvoked := false
			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerInvoked = true
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("ok"))
			})

			// Wrap with middleware
			middleware := InternalSecretMiddleware(tt.secret)
			handler := middleware(nextHandler)

			// Create request
			req := httptest.NewRequest("POST", "/api/turns", nil)
			if tt.headerValue != "" {
				req.Header.Set("X-Internal-Secret", tt.headerValue)
			}

			// Execute
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			// Assertions
			if w.Code != tt.expectStatus {
				t.Errorf("expected status %d, got %d (%s)", tt.expectStatus, w.Code, tt.description)
			}
			if handlerInvoked != tt.expectHandled {
				t.Errorf("expected handler invoked=%v, got %v (%s)", tt.expectHandled, handlerInvoked, tt.description)
			}
		})
	}
}

// TestBearerMiddleware tests the Bearer token authentication middleware (Phase 1 stub).
func TestBearerMiddleware(t *testing.T) {
	tests := []struct {
		name          string
		authHeader    string
		expectStatus  int
		expectJSON    string
		expectHandled bool
		description   string
	}{
		{
			name:          "Bearer token present returns 501 Not Implemented",
			authHeader:    "Bearer xyz123",
			expectStatus:  http.StatusNotImplemented,
			expectJSON:    `{"error":"bearer auth not implemented in phase 1"}`,
			expectHandled: false,
			description:   "Test 5: Authorization: Bearer xyz → 501 with JSON",
		},
		{
			name:          "no auth header lets request pass through",
			authHeader:    "",
			expectStatus:  http.StatusOK,
			expectHandled: true,
			description:   "Test 5b: No Bearer header → next handler invoked",
		},
		{
			name:          "non-Bearer auth header lets request pass through",
			authHeader:    "Basic dXNlcjpwYXNz",
			expectStatus:  http.StatusOK,
			expectHandled: true,
			description:   "Test 5c: Non-Bearer auth → next handler invoked",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Sentinel to track if next handler was invoked
			handlerInvoked := false
			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerInvoked = true
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("ok"))
			})

			// Wrap with middleware
			middleware := BearerMiddleware()
			handler := middleware(nextHandler)

			// Create request
			req := httptest.NewRequest("POST", "/api/turns", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			// Execute
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			// Assertions
			if w.Code != tt.expectStatus {
				t.Errorf("expected status %d, got %d (%s)", tt.expectStatus, w.Code, tt.description)
			}
			if handlerInvoked != tt.expectHandled {
				t.Errorf("expected handler invoked=%v, got %v (%s)", tt.expectHandled, handlerInvoked, tt.description)
			}
			if tt.expectJSON != "" {
				body, _ := io.ReadAll(w.Body)
				bodyStr := strings.TrimSpace(string(body))
				if bodyStr != tt.expectJSON {
					t.Errorf("expected body %q, got %q (%s)", tt.expectJSON, bodyStr, tt.description)
				}
			}
		})
	}
}

// TestEitherMiddleware tests the composed middleware that tries internal first, then bearer.
func TestEitherMiddleware(t *testing.T) {
	tests := []struct {
		name            string
		internalSecret  string
		xInternalSecret string
		authHeader      string
		expectStatus    int
		expectInvoked   bool
		expectJSON      string
		description     string
	}{
		{
			name:            "Either with X-Internal-Secret routes through internal correctly",
			internalSecret:  "secret123",
			xInternalSecret: "secret123",
			authHeader:      "",
			expectStatus:    http.StatusOK,
			expectInvoked:   true,
			description:     "Test 6: Either with X-Internal-Secret → internal runs, bearer doesn't",
		},
		{
			name:           "Either with Bearer header routes through bearer",
			internalSecret: "secret123",
			authHeader:     "Bearer xyz",
			expectStatus:   http.StatusNotImplemented,
			expectInvoked:  false,
			expectJSON:     `{"error":"bearer auth not implemented in phase 1"}`,
			description:    "Test 7: Either with Bearer header → bearer runs",
		},
		{
			name:           "Either with neither header returns 401",
			internalSecret: "secret123",
			expectStatus:   http.StatusUnauthorized,
			expectInvoked:  false,
			expectJSON:     `{"error":"missing auth"}`,
			description:    "Test 8: Either with neither → 401 missing auth",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Sentinel to track if next handler was invoked
			handlerInvoked := false
			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerInvoked = true
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("ok"))
			})

			// Create internal and bearer middlewares
			internalMW := InternalSecretMiddleware(tt.internalSecret)
			bearerMW := BearerMiddleware()

			// Compose with Either
			composedMW := Either(internalMW, bearerMW)
			handler := composedMW(nextHandler)

			// Create request
			req := httptest.NewRequest("POST", "/api/turns", nil)
			if tt.xInternalSecret != "" {
				req.Header.Set("X-Internal-Secret", tt.xInternalSecret)
			}
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			// Execute
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			// Assertions
			if w.Code != tt.expectStatus {
				t.Errorf("expected status %d, got %d (%s)", tt.expectStatus, w.Code, tt.description)
			}
			if handlerInvoked != tt.expectInvoked {
				t.Errorf("expected handler invoked=%v, got %v (%s)", tt.expectInvoked, handlerInvoked, tt.description)
			}
			if tt.expectJSON != "" {
				body, _ := io.ReadAll(w.Body)
				bodyStr := strings.TrimSpace(string(body))
				if bodyStr != tt.expectJSON {
					t.Errorf("expected body %q, got %q (%s)", tt.expectJSON, bodyStr, tt.description)
				}
			}
		})
	}
}
