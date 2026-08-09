package egresscapability

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPlaceholderBindsExactOperationGrantAndExpiry(t *testing.T) {
	now := time.Date(2026, 8, 6, 22, 0, 0, 0, time.UTC)
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, "egress-placeholder-test-seed")
	privateKey := ed25519.NewKeyFromSeed(seed)
	signer, err := NewSigner("execution-gateway", "egress-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier([]TrustedKey{{
		Issuer: "execution-gateway", KeyID: "egress-key-1",
		PublicKey: privateKey.Public().(ed25519.PublicKey),
	}})
	if err != nil {
		t.Fatal(err)
	}
	claims := validEgressClaims(now)
	token, err := signer.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifier.Verify(token, now)
	if err != nil || verified != claims {
		t.Fatalf("verified claims = %+v, %v", verified, err)
	}
	if _, err := verifier.Verify(token, now.Add(time.Minute)); err == nil {
		t.Fatal("expired placeholder was accepted")
	}
	parts := strings.Split(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	var changed Claims
	if err := json.Unmarshal(raw, &changed); err != nil {
		t.Fatal(err)
	}
	changed.GrantVersion++
	raw, err = json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	parts[2] = base64.RawURLEncoding.EncodeToString(raw)
	if _, err := verifier.Verify(strings.Join(parts, "."), now); err == nil {
		t.Fatal("tampered grant version passed verification")
	}
}

func TestPlaceholderKeyringRejectsAnotherAudience(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, "egress-keyring-test-seed")
	privateKey := ed25519.NewKeyFromSeed(seed)
	document := KeyringDocument{Version: KeyringVersion, Keys: []VerificationKeyDocument{{
		Issuer: "execution-gateway", Audience: "sandbox-backend", KeyID: "key-1",
		Algorithm: SignatureAlgorithm,
		PublicKey: base64.RawURLEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
	}}}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVerifier(raw); err == nil {
		t.Fatal("another capability audience was accepted")
	}
	document.Keys[0].Audience = AudienceLarkReadOnly
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVerifier(raw); err != nil {
		t.Fatal(err)
	}
}

func validEgressClaims(now time.Time) Claims {
	return Claims{
		Version: Version, Issuer: "execution-gateway", Audience: AudienceLarkReadOnly,
		CapabilityID: "placeholder-1",
		WorkspaceID:  "workspace-1", SessionID: "session-1", ActorID: "actor-1",
		EnvironmentID: "environment-1", RunID: "run-1", RunAttemptID: "attempt-1",
		RunAttemptGeneration: 2, ExecutionID: "execution-1", OperationID: "operation-1",
		SandboxID: "sandbox-1", TargetGeneration: 3,
		PackID: PackLarkReadOnly, GrantID: "grant-1", GrantVersion: 4,
		PolicySHA256: strings.Repeat("a", 64), Executable: "lark-cli",
		IssuedAtUnixMS: now.Add(-time.Second).UnixMilli(), ExpiresAtUnixMS: now.Add(30 * time.Second).UnixMilli(),
	}
}
