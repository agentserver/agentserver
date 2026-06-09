// Package captoken issues workspace-scoped capability tokens for
// codex-exec-gateway's /bridge endpoint.
//
// Verification lives in internal/codexexecgateway.VerifyCapabilityToken;
// the wire format here (HS256 over "headerB64.payloadB64",
// base64url-no-pad) must stay byte-compatible with that verifier's
// CapPayload. JSON field tags are the contract — adding a new optional
// field on either side is safe as long as it carries the same tag.
//
// Per the 2026-05-16 fixed-tools redesign, one token covers any
// executor in the workspace; /bridge enforces workspace ownership at
// request time via the workspace_executors table.
package captoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// Payload is what gets HMAC-signed inside the token. WorkspaceID is the
// only field /bridge actually authorises on; TurnID is opaque to the
// verifier but used by /api/exec-gateway/revoke-turn. UserID is threaded
// into the exec-audit subsystem when present. SkipAudit signals to the
// bridge handler that per-frame audit recording should be skipped (the
// caller does its own coarser-grained recording — see
// codexexecgateway/sdk/handlers.go).
type Payload struct {
	TurnID      string `json:"turn_id"`
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id,omitempty"`
	IAT         int64  `json:"iat"`
	EXP         int64  `json:"exp"`
	SkipAudit   bool   `json:"skip_audit,omitempty"`
}

// Mint signs p with secret and returns a 3-part token. IAT and EXP on p
// are ignored; they are derived from now + ttl so callers cannot mint
// pre-aged or never-expiring tokens by accident.
func Mint(secret []byte, p Payload, ttl time.Duration) (string, error) {
	if len(secret) == 0 {
		return "", fmt.Errorf("captoken: empty secret")
	}
	if p.TurnID == "" || p.WorkspaceID == "" {
		return "", fmt.Errorf("captoken: turn_id/workspace_id required")
	}
	now := time.Now().UTC().Unix()
	p.IAT = now
	p.EXP = now + int64(ttl.Seconds())
	pj, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("captoken: marshal payload: %w", err)
	}
	enc := base64.RawURLEncoding
	headerB64 := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"CXG"}`))
	payloadB64 := enc.EncodeToString(pj)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(headerB64 + "." + payloadB64))
	return headerB64 + "." + payloadB64 + "." + enc.EncodeToString(mac.Sum(nil)), nil
}
