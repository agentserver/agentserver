package egresscapability

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"
)

func TestProcessEnvironmentProofRoundTripIsCompactAndOperationBound(t *testing.T) {
	now := time.Date(2026, 8, 10, 2, 0, 0, 0, time.UTC)
	signer, verifier := processEnvironmentTestAuthorities(t, AudienceForProvider("lark"))
	claims := processEnvironmentTestClaims(now)
	proof, err := signer.SignProcessEnvironment(claims)
	if err != nil {
		t.Fatal(err)
	}
	if len(proof) > 1024 || !IsProcessEnvironmentProof(proof) {
		t.Fatalf("process proof framing/size = %d %q", len(proof), proof)
	}
	resolved, err := verifier.VerifyProcessEnvironment(proof, now)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.WorkspaceID != claims.WorkspaceID || resolved.OperationID != claims.OperationID ||
		resolved.BindingID != claims.BindingID || resolved.CredentialVersion != claims.CredentialVersion ||
		resolved.PolicySHA256 != claims.PolicySHA256 || resolved.Issuer != claims.Issuer {
		t.Fatalf("resolved process proof = %#v", resolved)
	}

	parts := strings.Split(proof, ".")
	last := parts[2][len(parts[2])-1]
	if last == 'A' {
		last = 'B'
	} else {
		last = 'A'
	}
	parts[2] = parts[2][:len(parts[2])-1] + string(last)
	if _, err := verifier.VerifyProcessEnvironment(strings.Join(parts, "."), now); err == nil {
		t.Fatal("tampered process proof was accepted")
	}
	if _, err := verifier.VerifyProcessEnvironment(proof, claims.ExpiresAt()); err == nil {
		t.Fatal("expired process proof was accepted")
	}
	if _, err := verifier.VerifyProcessEnvironment(proof, time.UnixMilli(claims.IssuedAtUnixMS-1)); err == nil {
		t.Fatal("not-yet-valid process proof was accepted")
	}
}

func TestProcessEnvironmentProofRejectsWrongAudienceAndNonCanonicalAuthority(t *testing.T) {
	now := time.Date(2026, 8, 10, 2, 0, 0, 0, time.UTC)
	signer, _ := processEnvironmentTestAuthorities(t, AudienceForProvider("lark"))
	claims := processEnvironmentTestClaims(now)
	proof, err := signer.SignProcessEnvironment(claims)
	if err != nil {
		t.Fatal(err)
	}
	_, wrongAudience := processEnvironmentTestAuthorities(t, AudienceForProvider("github"))
	if _, err := wrongAudience.VerifyProcessEnvironment(proof, now); err == nil {
		t.Fatal("process proof trusted under a non-Lark audience")
	}

	claims.WorkspaceID = "A0000000-0000-4000-8000-000000000002"
	if _, err := signer.SignProcessEnvironment(claims); err == nil {
		t.Fatal("non-canonical workspace UUID was signed")
	}
	claims = processEnvironmentTestClaims(now)
	claims.ExpiresAtUnixMS = claims.IssuedAtUnixMS + (2*time.Minute + time.Millisecond).Milliseconds()
	if _, err := signer.SignProcessEnvironment(claims); err == nil {
		t.Fatal("overlong process proof authority was signed")
	}
}

func processEnvironmentTestAuthorities(t *testing.T, audience string) (*Signer, *Verifier) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, "process-environment-proof-test-key")
	privateKey := ed25519.NewKeyFromSeed(seed)
	signer, err := NewSigner("executor-gateway/egress", "process-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier([]TrustedKey{{
		Issuer: signer.Issuer(), Audience: audience, KeyID: "process-key-1",
		PublicKey: privateKey.Public().(ed25519.PublicKey),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return signer, verifier
}

func processEnvironmentTestClaims(now time.Time) ProcessEnvironmentClaims {
	return ProcessEnvironmentClaims{
		Version: ProcessEnvironmentVersion, Issuer: "executor-gateway/egress",
		CapabilityID:  "10000000-0000-4000-8000-000000000001",
		WorkspaceID:   "20000000-0000-4000-8000-000000000002",
		SessionID:     "30000000-0000-4000-8000-000000000003",
		ActorID:       "40000000-0000-4000-8000-000000000004",
		EnvironmentID: "50000000-0000-4000-8000-000000000005",
		RunID:         "60000000-0000-4000-8000-000000000006",
		RunAttemptID:  "70000000-0000-4000-8000-000000000007", RunAttemptGeneration: 2,
		ExecutionID: "80000000-0000-4000-8000-000000000008",
		OperationID: "90000000-0000-4000-8000-000000000009",
		SandboxID:   "a0000000-0000-4000-8000-00000000000a", TargetGeneration: 3,
		ProviderKind: "lark", BindingID: "b0000000-0000-4000-8000-00000000000b",
		AuthorityVersion: 4, CredentialVersion: 5, PolicySHA256: strings.Repeat("c", 64),
		IssuedAtUnixMS: now.Add(-time.Second).UnixMilli(), ExpiresAtUnixMS: now.Add(time.Minute).UnixMilli(),
	}
}

func (claims ProcessEnvironmentClaims) ExpiresAt() time.Time {
	return time.UnixMilli(claims.ExpiresAtUnixMS)
}
