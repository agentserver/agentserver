package codexappgateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// MintCapToken produces a workspace-scoped capability token consumed
// by codex-exec-gateway's VerifyCapabilityToken. Format and HMAC are
// kept identical (HS256 over "headerB64.payloadB64",
// base64url-no-pad) — see internal/codexexecgateway/auth.go for the
// verifier.
//
// Per the 2026-05-16 fixed-tools redesign, one token covers any
// executor in the workspace; /bridge enforces workspace ownership at
// request time via the workspace_executors table.
//
// userID is the workspace member who triggered the mint, threaded into
// the exec-audit subsystem (omitempty so older verifiers reading new
// tokens, and new verifiers reading older tokens, both work). Pass ""
// when no user context is available (e.g. scheduled-task spawns).
func MintCapToken(secret []byte, turnID, workspaceID, userID string, ttl time.Duration) (string, error) {
	if len(secret) == 0 {
		return "", fmt.Errorf("captoken: empty secret")
	}
	if turnID == "" || workspaceID == "" {
		return "", fmt.Errorf("captoken: turnID/workspaceID required")
	}
	now := time.Now().UTC().Unix()
	payload := struct {
		TurnID      string `json:"turn_id"`
		WorkspaceID string `json:"workspace_id"`
		UserID      string `json:"user_id,omitempty"`
		IAT         int64  `json:"iat"`
		EXP         int64  `json:"exp"`
	}{
		TurnID:      turnID,
		WorkspaceID: workspaceID,
		UserID:      userID,
		IAT:         now,
		EXP:         now + int64(ttl.Seconds()),
	}
	pj, err := json.Marshal(payload)
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
