package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// capTokenPayload mirrors codexexecgateway.CapPayload's wire shape.
//
// TODO(captoken-consolidate): once PR #233 (internal/captoken extract)
// merges, hoist VerifyCapabilityToken from internal/codexexecgateway
// alongside captoken.Mint and delete this duplicate. The HMAC scheme
// is byte-compatible — both packages will share one implementation.
type capTokenPayload struct {
	TurnID      string `json:"turn_id"`
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id,omitempty"`
	IAT         int64  `json:"iat"`
	EXP         int64  `json:"exp"`
}

// verifyCapToken parses a 3-part HMAC capability token (header.payload.sig,
// each base64url-no-pad) and returns its payload, or an error if the
// signature is wrong, the token is expired, or it's malformed.
//
// This is duplicated from internal/codexexecgateway.VerifyCapabilityToken
// because the agentserver-main binary doesn't import codexexecgateway
// (and shouldn't — the latter is a sibling service). See the
// TODO(captoken-consolidate) above.
func verifyCapToken(token string, secret []byte) (capTokenPayload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return capTokenPayload{}, errors.New("malformed cap-token")
	}
	headerB64, payloadB64, sigB64 := parts[0], parts[1], parts[2]
	if headerB64 == "" || payloadB64 == "" || sigB64 == "" {
		return capTokenPayload{}, errors.New("malformed cap-token")
	}
	enc := base64.RawURLEncoding

	hdrBytes, err := enc.DecodeString(headerB64)
	if err != nil {
		return capTokenPayload{}, errors.New("malformed cap-token header")
	}
	var hdr struct{ Alg, Typ string }
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
		return capTokenPayload{}, errors.New("malformed cap-token header json")
	}
	if hdr.Alg != "HS256" || hdr.Typ != "CXG" {
		return capTokenPayload{}, errors.New("unexpected cap-token header alg/typ")
	}

	gotSig, err := enc.DecodeString(sigB64)
	if err != nil {
		return capTokenPayload{}, errors.New("malformed cap-token signature")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(headerB64 + "." + payloadB64))
	if !hmac.Equal(gotSig, mac.Sum(nil)) {
		return capTokenPayload{}, errors.New("bad cap-token signature")
	}

	payloadBytes, err := enc.DecodeString(payloadB64)
	if err != nil {
		return capTokenPayload{}, errors.New("malformed cap-token payload")
	}
	var p capTokenPayload
	if err := json.Unmarshal(payloadBytes, &p); err != nil {
		return capTokenPayload{}, errors.New("malformed cap-token payload json")
	}
	if time.Now().UTC().Unix() > p.EXP {
		return capTokenPayload{}, errors.New("cap-token expired")
	}
	return p, nil
}

// requireCapTokenWithMatchingWID authenticates a request via
// Authorization: Bearer <cap-token>, then enforces that the URL's
// `wid` chi param equals the token payload's workspace_id. Together
// these mean "this request is a cap-token bearer for the URL's
// workspace" — the same invariant the prior loopback design got by
// having app-gateway look up workspace_id from a per-spawn token map
// and only then synthesise the URL.
//
// The wid-match check is defensive: the handlers further down already
// scope their queries by chi.URLParam(r, "wid"), so accepting a token
// for ws_alpha but URL ws_beta would silently scope to ws_beta — the
// wrong workspace. Failing fast at the middleware layer means the
// handler can keep reading wid from the URL with no surprises.
func requireCapTokenWithMatchingWID(getSecret func() []byte, logger *slog.Logger) func(http.HandlerFunc) http.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			secret := getSecret()
			if len(secret) == 0 {
				logger.Error("scheduled-tasks: CXG_CAPTOKEN_HMAC_SECRET not configured; refusing requests")
				http.Error(w, "captoken auth not configured", http.StatusServiceUnavailable)
				return
			}
			h := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(h, prefix) {
				http.Error(w, "missing Bearer", http.StatusUnauthorized)
				return
			}
			tok := strings.TrimPrefix(h, prefix)
			payload, err := verifyCapToken(tok, secret)
			if err != nil {
				logger.Warn("cap-token verify failed",
					"path", r.URL.Path, "remote", r.RemoteAddr, "error", err)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			urlWID := chi.URLParam(r, "wid")
			if urlWID == "" || urlWID != payload.WorkspaceID {
				logger.Warn("cap-token wid mismatch",
					"path", r.URL.Path, "url_wid", urlWID,
					"token_wid", payload.WorkspaceID, "remote", r.RemoteAddr)
				http.Error(w, "workspace mismatch", http.StatusForbidden)
				return
			}
			next(w, r)
		}
	}
}

// envCapTokenSecret returns the HMAC secret env-mcp's cap-tokens are
// signed with. Read from CXG_CAPTOKEN_HMAC_SECRET so all three pods
// (app-gateway as signer, exec-gateway and agentserver-main as
// verifiers) share one source of truth via the same K8s Secret key.
//
// Re-read on every request rather than cached at boot so a Secret
// rotation doesn't need an agentserver-main restart to take effect —
// the read is one syscall and runs only on the (rare) scheduled-task
// endpoints, not on hot paths.
func envCapTokenSecret() []byte {
	return []byte(os.Getenv("CXG_CAPTOKEN_HMAC_SECRET"))
}
