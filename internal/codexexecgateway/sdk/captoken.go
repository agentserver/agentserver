package sdk

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// sdkCapTokenTTL is how long a workspace-scoped cap-token minted for
// the in-process SDK Pool is valid. The pool keeps the same token for
// the workspace's lifetime in wsCache; renewing on demand is a v2
// concern (sessions today don't outlive a single deploy by 24h). The
// codex-app-gateway path uses a per-turn token (~15min); SDK clients
// hold the token continuously, so we choose a longer window.
const sdkCapTokenTTL = 24 * time.Hour

// mintWorkspaceToken produces a cap-token the gateway's own /bridge
// verifier (codexexecgateway.VerifyCapabilityToken) will accept. The
// token format must stay byte-compatible with
// codexappgateway.MintCapToken — duplicated here only to avoid an
// import cycle (codexappgateway already imports codexexecgateway for
// its execmodel types and *Config*; pulling codexappgateway in from
// the sdk sub-package would close the loop at test time).
//
// turn_id is unused for authorization at verify time (the /bridge
// handler authorises against workspace_executors, not turn_id). To
// signal "this is an SDK-Pool-managed bridge — skip the per-frame audit
// session", we set the typed CapPayload.SkipAudit field (I10). The
// previous magic-string `TurnID="sdk-pool:..."` was load-bearing but
// silently mis-typed code could break it. The SDK REST handlers in
// sdk/handlers.go already record each tool call at CallStart/CallEnd
// granularity; without SkipAudit we'd double-record every SDK call
// (once at the handler, once per WS frame).
func mintWorkspaceToken(secret []byte, workspaceID string) (string, error) {
	if len(secret) == 0 {
		return "", fmt.Errorf("captoken: empty secret")
	}
	if workspaceID == "" {
		return "", fmt.Errorf("captoken: workspaceID required")
	}
	now := time.Now().UTC().Unix()
	payload := struct {
		TurnID      string `json:"turn_id"`
		WorkspaceID string `json:"workspace_id"`
		IAT         int64  `json:"iat"`
		EXP         int64  `json:"exp"`
		SkipAudit   bool   `json:"skip_audit,omitempty"`
	}{
		TurnID:      "sdk",
		WorkspaceID: workspaceID,
		IAT:         now,
		EXP:         now + int64(sdkCapTokenTTL.Seconds()),
		SkipAudit:   true,
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
