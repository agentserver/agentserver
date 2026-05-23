package codexexecgateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// CapPayload is the parsed JSON payload from a CODEX_EXEC_GATEWAY_TOKEN.
//
// Per the 2026-05-16 fixed-tools redesign, tokens are workspace-scoped:
// a single token authorises any exe_id bound to the named workspace.
// The /bridge handler verifies workspace ownership against the
// workspace_executors table at request time, replacing the prior
// exe_ids[] allow-list shipped in the payload.
type CapPayload struct {
	TurnID      string `json:"turn_id"`
	WorkspaceID string `json:"workspace_id"`
	// UserID is the workspace member who triggered the cap-token mint.
	// Optional for backward compatibility: tokens minted by older
	// codex-app-gateway versions don't carry it, which Verify accepts
	// (UserID is left as ""). The exec-audit subsystem stores it on
	// the session row when present — see
	// docs/superpowers/specs/2026-05-23-codex-exec-gateway-audit-design.md.
	UserID string `json:"user_id,omitempty"`
	IAT    int64  `json:"iat"`
	EXP    int64  `json:"exp"`
}

var (
	ErrMalformed    = errors.New("malformed capability token")
	ErrBadSignature = errors.New("bad capability token signature")
	ErrExpired      = errors.New("capability token expired")
)

// VerifyCapabilityToken parses and verifies a 3-part HMAC capability token.
//
// Token format (spec § Capability token):
//
//	token   = base64url(header) "." base64url(payload) "." base64url(sig)
//	header  = '{"alg":"HS256","typ":"CXG"}'
//	payload = '{"turn_id":"...","workspace_id":"...","iat":...,"exp":...}'
//	sig     = HMAC-SHA256(secret, base64url(header) "." base64url(payload))
//
// base64url encoding uses no padding (RFC 7515 / JWT convention).
// HMAC comparison is constant-time via hmac.Equal to prevent timing attacks.
// Expiry is checked in UTC.
func VerifyCapabilityToken(token string, secret []byte) (CapPayload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return CapPayload{}, ErrMalformed
	}
	headerB64, payloadB64, sigB64 := parts[0], parts[1], parts[2]
	if headerB64 == "" || payloadB64 == "" || sigB64 == "" {
		return CapPayload{}, ErrMalformed
	}

	enc := base64.RawURLEncoding

	// Decode and validate header.
	headerBytes, err := enc.DecodeString(headerB64)
	if err != nil {
		return CapPayload{}, ErrMalformed
	}
	var hdr struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return CapPayload{}, ErrMalformed
	}
	if hdr.Alg != "HS256" || hdr.Typ != "CXG" {
		return CapPayload{}, ErrMalformed
	}

	// Decode claimed signature.
	gotSig, err := enc.DecodeString(sigB64)
	if err != nil {
		return CapPayload{}, ErrMalformed
	}

	// Recompute HMAC over "headerB64.payloadB64" and compare constant-time.
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(headerB64 + "." + payloadB64))
	wantSig := mac.Sum(nil)
	if !hmac.Equal(gotSig, wantSig) {
		return CapPayload{}, ErrBadSignature
	}

	// Decode and unmarshal payload (after signature check so we don't act on
	// attacker-supplied JSON before verifying integrity).
	payloadBytes, err := enc.DecodeString(payloadB64)
	if err != nil {
		return CapPayload{}, ErrMalformed
	}
	var payload CapPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return CapPayload{}, ErrMalformed
	}

	// Check expiry in UTC.
	if time.Now().UTC().Unix() > payload.EXP {
		return CapPayload{}, ErrExpired
	}

	return payload, nil
}
