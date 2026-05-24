// Package wsticket mints and verifies the short-lived HMAC bearer that
// authorises a `/codex-exec/{exe_id}?token=...` ws upgrade. Used by:
//   - codex-exec-gateway's cloud_register handler (mint)
//   - codex-exec-gateway's inbound handler (verify)
//   - codex-exec-edge's wsproxy (verify before proxying upstream)
//
// Layout: <exe_id>.<expiry_unix>.<base64url(HMAC-SHA256(secret, "<exe_id>.<expiry>"))>
// 5-minute TTL; codex re-registers on every reconnect so no need for longer.
package wsticket

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const TTL = 5 * time.Minute

// Mint returns a short-lived bearer that authorises the
// `/codex-exec/{exe_id}?token=...` ws upgrade. The ticket is valid for TTL
// from the moment it is issued; callers should use it immediately.
func Mint(exeID, secret string) (string, error) {
	if secret == "" {
		return "", fmt.Errorf("internal secret not configured")
	}
	expiry := time.Now().Add(TTL).Unix()
	payload := exeID + "." + strconv.FormatInt(expiry, 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig, nil
}

// Verify returns nil iff the ticket is well-formed, signed with secret,
// names the expected exe_id, and has not yet expired.
func Verify(ticket, expectedExeID, secret string) error {
	if secret == "" {
		return fmt.Errorf("internal secret not configured")
	}
	parts := strings.Split(ticket, ".")
	if len(parts) != 3 {
		return fmt.Errorf("malformed ticket")
	}
	exeID, expStr, sigB64 := parts[0], parts[1], parts[2]
	if exeID != expectedExeID {
		return fmt.Errorf("ticket exe_id mismatch")
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return fmt.Errorf("bad expiry: %w", err)
	}
	if time.Now().Unix() >= exp {
		return fmt.Errorf("ticket expired")
	}
	want := hmac.New(sha256.New, []byte(secret))
	want.Write([]byte(exeID + "." + expStr))
	wantSig := want.Sum(nil)
	gotSig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("bad signature encoding: %w", err)
	}
	if !hmac.Equal(wantSig, gotSig) {
		return fmt.Errorf("bad signature")
	}
	return nil
}
