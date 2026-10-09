package sandboxcapability

import (
	"testing"
	"time"
)

func TestRepositoryPreparationHasOwnTargetBoundAction(t *testing.T) {
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	signer, key := testSigner(t, "executor-gateway", AudienceLifecycle, "test-key", "seed")
	verifier, err := NewVerifier([]TrustedKey{key})
	if err != nil {
		t.Fatal(err)
	}
	c := testLifecycleClaims(now)
	c.Action = "prepare_repository"
	c.Issuer = signer.Issuer()
	c.Audience = signer.Audience()
	token, err := signer.Sign(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(token, AudienceLifecycle, "prepare_repository", now); err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(token, AudienceLifecycle, "ensure", now); err == nil {
		t.Fatal("preparation token usable for another lifecycle action")
	}
	c.SandboxID = ""
	if _, err := signer.Sign(c); err == nil {
		t.Fatal("unbound preparation capability accepted")
	}
}
