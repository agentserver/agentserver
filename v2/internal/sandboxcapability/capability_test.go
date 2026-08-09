package sandboxcapability

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCapabilitiesBindAudienceActionAndExactAuthority(t *testing.T) {
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	lifecycleSigner, lifecycleKey := testSigner(t, "harness-pool", AudienceLifecycle, "lifecycle-key", "lifecycle-seed")
	backendSigner, backendKey := testSigner(t, "execution-gateway", AudienceBackend, "backend-key", "backend-seed")
	verifier, err := NewVerifier([]TrustedKey{lifecycleKey, backendKey})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := testLifecycleClaims(now)
	lifecycle.Issuer = lifecycleSigner.Issuer()
	lifecycle.Audience = lifecycleSigner.Audience()
	token, err := lifecycleSigner.Sign(lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifier.Verify(token, AudienceLifecycle, "renew_activity", now)
	if err != nil || verified != lifecycle {
		t.Fatalf("verified lifecycle claims = %+v, error = %v", verified, err)
	}
	if _, err := verifier.Verify(token, AudienceBackend, "renew_activity", now); err == nil {
		t.Fatal("lifecycle token was accepted for the backend audience")
	}
	if _, err := verifier.Verify(token, AudienceLifecycle, "release_activity", now); err == nil {
		t.Fatal("lifecycle token was accepted for another action")
	}
	if _, err := verifier.Verify(token, AudienceLifecycle, "renew_activity", now.Add(time.Minute)); err == nil {
		t.Fatal("expired lifecycle token was accepted")
	}

	backend := testBackendClaims(now)
	backend.Issuer = backendSigner.Issuer()
	backend.Audience = backendSigner.Audience()
	backendToken, err := backendSigner.Sign(backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(backendToken, AudienceBackend, "run_command", now); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(backendToken, ".")
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	var changed Claims
	if err := json.Unmarshal(claimsBytes, &changed); err != nil {
		t.Fatal(err)
	}
	changed.TargetGeneration++
	changedBytes, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	parts[2] = base64.RawURLEncoding.EncodeToString(changedBytes)
	if _, err := verifier.Verify(strings.Join(parts, "."), AudienceBackend, "run_command", now); err == nil {
		t.Fatal("target-generation tampering passed signature verification")
	}
}

func TestParseVerifierRejectsDuplicateOrAudienceAmbiguousKeys(t *testing.T) {
	_, key := testSigner(t, "execution-gateway", AudienceBackend, "shared-key", "key-seed")
	entry := VerificationKeyDocument{
		Issuer: key.Issuer, Audience: key.Audience, KeyID: key.KeyID,
		Algorithm: SignatureAlgorithm, PublicKey: base64.RawURLEncoding.EncodeToString(key.PublicKey),
	}
	raw, err := json.Marshal(KeyringDocument{Version: KeyringVersion, Keys: []VerificationKeyDocument{entry}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVerifier(raw); err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(KeyringDocument{Version: KeyringVersion, Keys: []VerificationKeyDocument{entry, entry}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVerifier(raw); err == nil {
		t.Fatal("duplicate key ID was accepted")
	}
	entry.Audience = "all-sandbox-actions"
	raw, err = json.Marshal(KeyringDocument{Version: KeyringVersion, Keys: []VerificationKeyDocument{entry}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVerifier(raw); err == nil {
		t.Fatal("broad unknown sandbox audience was accepted")
	}
}

func testSigner(t *testing.T, issuer, audience, keyID, seedText string) (*Signer, TrustedKey) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, []byte(seedText))
	privateKey := ed25519.NewKeyFromSeed(seed)
	signer, err := NewSigner(issuer, audience, keyID, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer, TrustedKey{
		Issuer: issuer, Audience: audience, KeyID: keyID,
		PublicKey: append(ed25519.PublicKey(nil), privateKey.Public().(ed25519.PublicKey)...),
	}
}

func testLifecycleClaims(now time.Time) Claims {
	return Claims{
		Version: Version, Action: "renew_activity", CapabilityID: "attempt:renew",
		WorkspaceID:   "40000000-0000-4000-8000-000000000004",
		SessionID:     "50000000-0000-4000-8000-000000000005",
		EnvironmentID: "60000000-0000-4000-8000-000000000006",
		RunID:         "70000000-0000-4000-8000-000000000007",
		RunAttemptID:  "71000000-0000-4000-8000-000000000007", RunAttemptGeneration: 3,
		HolderID:  "72000000-0000-4000-8000-000000000007",
		SandboxID: "73000000-0000-4000-8000-000000000007", TargetGeneration: 4,
		IssuedAtUnixMS: now.Add(-time.Second).UnixMilli(), ExpiresAtUnixMS: now.Add(30 * time.Second).UnixMilli(),
	}
}

func testBackendClaims(now time.Time) Claims {
	return Claims{
		Version: Version, Action: "run_command", CapabilityID: "operation:run",
		WorkspaceID:   "40000000-0000-4000-8000-000000000004",
		SessionID:     "50000000-0000-4000-8000-000000000005",
		EnvironmentID: "60000000-0000-4000-8000-000000000006",
		RunID:         "70000000-0000-4000-8000-000000000007",
		RunAttemptID:  "71000000-0000-4000-8000-000000000007", RunAttemptGeneration: 3,
		ExecutionID: "74000000-0000-4000-8000-000000000007",
		OperationID: "75000000-0000-4000-8000-000000000007", MutationKey: "mutation-key",
		SandboxID: "73000000-0000-4000-8000-000000000007", TargetGeneration: 4,
		IssuedAtUnixMS: now.Add(-time.Second).UnixMilli(), ExpiresAtUnixMS: now.Add(30 * time.Second).UnixMilli(),
	}
}
