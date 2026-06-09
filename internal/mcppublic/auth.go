package mcppublic

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

// Middleware authenticates incoming MCP requests via the Authorization
// header. Resolved Principal is plumbed in request context (see
// WithPrincipal/PrincipalFromContext). On any auth failure, the
// middleware short-circuits with 401 and the documented MCP
// WWW-Authenticate header pointing at the OAuth resource-metadata
// endpoint — clients use that to bootstrap the OAuth flow.
//
// Token dispatch is by prefix:
//   - agpat_… → PATResolver
//   - anything else, Phase 2 → OAuthResolver (HTTP introspect via Hydra)
//
// Phase 1 (PAT only) wires just the PAT resolver; the middleware code
// is general and the OAuth resolver drops in without changes.
type Middleware struct {
	// Resolvers are tried in order; the first one whose Resolve
	// returns anything other than ErrUnknown wins. ErrInvalid from any
	// resolver short-circuits to 401 (don't fall through to the next
	// resolver — that would let a revoked agpat_ try to validate as an
	// OAuth opaque token).
	Resolvers []PrincipalResolver

	// ResourceMetadataURL is the absolute URL the 401 response
	// advertises in `WWW-Authenticate: Bearer resource_metadata="…"`.
	// MCP 2025-11-25 § 6.1 mandates this for clients to discover the
	// OAuth flow. Leave empty in tests / for clients that already
	// know how to authenticate (Codex CLI with bearer_token_env_var).
	ResourceMetadataURL string

	Logger *slog.Logger
}

// Wrap returns an http.Handler that runs the middleware in front of
// next. Typical use: r.Use(mw.Wrap) inside a chi.Router group.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	log := m.Logger
	if log == nil {
		log = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := extractBearer(r.Header.Get("Authorization"))
		if err != nil {
			m.unauthorized(w, "missing or malformed Authorization header")
			return
		}
		var resolved *Principal
		var lastErr error
		for _, res := range m.Resolvers {
			p, err := res.Resolve(r.Context(), raw)
			if err == nil {
				resolved = p
				break
			}
			if errors.Is(err, ErrUnknown) {
				// Prefix didn't match this resolver — try next.
				continue
			}
			// ErrInvalid or other resolver-side failure: 401, don't
			// fall through (see Resolvers field comment).
			lastErr = err
			break
		}
		if resolved == nil {
			if lastErr != nil && !errors.Is(lastErr, ErrInvalid) {
				log.Error("mcppublic: resolver error", "err", lastErr)
			}
			m.unauthorized(w, "invalid bearer token")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), resolved)))
	})
}

// extractBearer parses "Bearer <token>" from the Authorization header.
// Returns the bare token (no leading scheme). Case-insensitive on the
// scheme per RFC 7235.
func extractBearer(h string) (string, error) {
	if h == "" {
		return "", errors.New("missing Authorization")
	}
	const prefix = "bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", errors.New("not a Bearer scheme")
	}
	tok := strings.TrimSpace(h[len(prefix):])
	if tok == "" {
		return "", errors.New("empty bearer token")
	}
	return tok, nil
}

// unauthorized writes a 401 with the MCP-spec-mandated WWW-Authenticate
// header pointing at the resource-metadata endpoint, plus a tiny
// human-readable body. Body content is deliberately uniform across all
// failure modes (no "key revoked" vs "key not found" leakage).
func (m *Middleware) unauthorized(w http.ResponseWriter, _ string) {
	if m.ResourceMetadataURL != "" {
		w.Header().Set("WWW-Authenticate",
			`Bearer resource_metadata="`+m.ResourceMetadataURL+`"`)
	} else {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
