package handlers

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
)

// RequireAgentserverSecret rejects requests whose X-Internal-Secret
// header does not constant-time-match `secret`. When `secret` is empty,
// this middleware is a no-op (dev mode).
//
// This is separate from RequireSharedSecret because the two represent
// different trust scopes:
//   - RequireSharedSecret       → cap-token admin API
//                                 (called by codex-app-gateway via
//                                 CXG_INTERNAL_SHARED_SECRET)
//   - RequireAgentserverSecret  → user-management API
//                                 (called by agentserver on behalf of
//                                 session-authenticated humans, via
//                                 CXG_AGENTSERVER_INTERNAL_SECRET)
func RequireAgentserverSecret(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if secret == "" {
				next.ServeHTTP(w, r)
				return
			}
			got := r.Header.Get("X-Internal-Secret")
			if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CapTokenClaims is what RequireCapToken plumbs via request context after
// a successful authentication. Read it via CapTokenClaimsFromContext.
//
// The fields mirror the verified cap-token payload: WorkspaceID is the
// workspace the token authorises ("which executors am I allowed to
// see/touch"), UserID is the human attribution carried for audit, and
// TurnID is the synthetic-or-real id the token was minted under
// (preserved for revoke-turn semantics).
type CapTokenClaims struct {
	WorkspaceID string
	UserID      string
	TurnID      string
}

// CapTokenVerifier is the function RequireCapToken delegates to for
// signature + freshness checks. Defined as a function (not an
// interface) so the handlers package doesn't import the parent
// codexexecgateway package, which would be a cyclic import. The
// caller (server.go) closes over the HMAC secret + the revoked-set
// and returns the parsed claims, or any error to short-circuit to
// 401. The middleware itself logs and surfaces only the canonical
// "unauthorized" message; the verifier's error text is for the log,
// not the wire.
type CapTokenVerifier func(token string) (CapTokenClaims, error)

type capTokenCtxKey struct{}

// WithCapTokenClaims returns a context carrying the verified claims.
// Exported so tests that bypass the middleware can plumb a fake set.
func WithCapTokenClaims(ctx context.Context, c CapTokenClaims) context.Context {
	return context.WithValue(ctx, capTokenCtxKey{}, c)
}

// CapTokenClaimsFromContext returns the claims set by RequireCapToken,
// or zero-value + false if the middleware didn't run (or the context
// is from elsewhere). Handlers MUST check ok before using.
func CapTokenClaimsFromContext(ctx context.Context) (CapTokenClaims, bool) {
	c, ok := ctx.Value(capTokenCtxKey{}).(CapTokenClaims)
	return c, ok
}

// RequireCapToken authenticates requests via `Authorization: Bearer
// <captoken>` and exposes the verified claims via ctx. Used by
// per-workspace endpoints (notably `/api/exec-gateway/connected` post
// the 2026-06-14 loopback removal) that need to act on the
// workspace_id embedded in the token, not a query-string parameter
// the caller could forge.
//
// Verifier-injected design (see CapTokenVerifier doc) keeps the
// handlers package free of an import cycle on its parent.
func RequireCapToken(verify CapTokenVerifier, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(h, prefix) {
				http.Error(w, "missing Bearer", http.StatusUnauthorized)
				return
			}
			tok := strings.TrimPrefix(h, prefix)
			if tok == "" {
				http.Error(w, "empty bearer", http.StatusUnauthorized)
				return
			}
			claims, err := verify(tok)
			if err != nil {
				logger.Warn("cap-token auth failed", "remote", r.RemoteAddr, "error", err)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithCapTokenClaims(r.Context(), claims)))
		})
	}
}

// RequireSharedSecret rejects requests whose Authorization: Bearer header
// does not constant-time-match `secret`.
func RequireSharedSecret(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(h, prefix) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			got := h[len(prefix):]
			if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
