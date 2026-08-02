package enrollmenttoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCodecRoundTripAndExactAuthority(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	codec, err := New("https://agentserver.example.test/core", []byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	claims := testClaims(now)
	token, err := codec.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := codec.Verify(token, now)
	if err != nil {
		t.Fatal(err)
	}
	if verified != claims || Fingerprint(token) == [32]byte{} {
		t.Fatalf("verified claims/fingerprint = %+v / %x", verified, Fingerprint(token))
	}
	second, err := codec.Sign(claims)
	if err != nil || second != token {
		t.Fatalf("exact signing retry = %q, %v", second, err)
	}
}

func TestCodecRejectsTamperingCanonicalDriftAndTimeBounds(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	codec, _ := New("https://agentserver.example.test/core", []byte(strings.Repeat("k", 32)))
	token, _ := codec.Sign(testClaims(now))
	parts := strings.Split(token, ".")

	tamperedMAC := parts[2]
	if tamperedMAC[0] == 'A' {
		tamperedMAC = "B" + tamperedMAC[1:]
	} else {
		tamperedMAC = "A" + tamperedMAC[1:]
	}
	for name, candidate := range map[string]string{
		"tampered MAC": parts[0] + "." + parts[1] + "." + tamperedMAC,
		"padded":       token + " ",
		"wrong prefix": "asv2cap1." + parts[1] + "." + parts[2],
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := codec.Verify(candidate, now); err == nil {
				t.Fatal("invalid token was accepted")
			}
		})
	}

	for name, at := range map[string]time.Time{
		"before issuance": now.Add(-time.Millisecond),
		"at expiry":       now.Add(10 * time.Minute),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := codec.Verify(token, at); err == nil {
				t.Fatal("token outside time window was accepted")
			}
		})
	}

	nonCanonical := `{"v":1,"iss":"https://agentserver.example.test/core","jti":"10000000-0000-4000-8000-000000000001","workspace_id":"10000000-0000-4000-8000-000000000002","executor_id":"10000000-0000-4000-8000-000000000003","issued_by":"10000000-0000-4000-8000-000000000004","iat_ms":` +
		strconv.FormatInt(now.UnixMilli(), 10) + `,"exp_ms":` + strconv.FormatInt(now.Add(10*time.Minute).UnixMilli(), 10) + ` }`
	mac := hmac.New(sha256.New, []byte(strings.Repeat("k", 32)))
	_, _ = mac.Write([]byte(tokenDomain))
	_, _ = mac.Write([]byte(nonCanonical))
	candidate := Prefix + "." + base64.RawURLEncoding.EncodeToString([]byte(nonCanonical)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if _, err := codec.Verify(candidate, now); err == nil {
		t.Fatal("non-canonical claim bytes were accepted")
	}
}

func TestCodecValidatesConstructionAndClaims(t *testing.T) {
	if _, err := New("issuer", make([]byte, 31)); err == nil {
		t.Fatal("short key was accepted")
	}
	if _, err := New("issuer", make([]byte, 32)); err == nil {
		t.Fatal("zero key was accepted")
	}
	codec, _ := New("issuer", []byte(strings.Repeat("k", 32)))
	claims := testClaims(time.Now().UTC())
	claims.Issuer = "other"
	if _, err := codec.Sign(claims); err == nil {
		t.Fatal("wrong issuer was accepted")
	}
	claims = testClaims(time.Now().UTC())
	claims.ExpiresAtUnixMS = claims.IssuedAtUnixMS + MaximumTTL.Milliseconds() + 1
	if _, err := codec.Sign(claims); err == nil {
		t.Fatal("oversized TTL was accepted")
	}
	claims = testClaims(time.Now().UTC())
	claims.ExpiresAtUnixMS = maximumSafeInt
	if _, err := codec.Sign(claims); err == nil {
		t.Fatal("overflow-sized TTL was accepted")
	}
}

func testClaims(now time.Time) Claims {
	return Claims{
		Version: Version, Issuer: "https://agentserver.example.test/core",
		TokenID: "10000000-0000-4000-8000-000000000001", WorkspaceID: "10000000-0000-4000-8000-000000000002",
		ExecutorID: "10000000-0000-4000-8000-000000000003", IssuedByActorID: "10000000-0000-4000-8000-000000000004",
		IssuedAtUnixMS: now.UnixMilli(), ExpiresAtUnixMS: now.Add(10 * time.Minute).UnixMilli(),
	}
}
