package executorgateway

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

func TestSignedManagedLarkEnvironmentIssuerBindsExactOperation(t *testing.T) {
	now := time.Date(2026, 8, 6, 22, 30, 0, 0, time.UTC)
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, "managed-lark-environment-seed")
	privateKey := ed25519.NewKeyFromSeed(seed)
	signer, err := egresscapability.NewSigner("execution-gateway", "egress-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	authorities, err := NewFrozenManagedLarkEgressAuthoritySource(ManagedLarkEgressAuthority{
		CredentialMode: managedcredential.ModeWebhookSwap,
		BindingID:      "90000000-0000-4000-8000-000000000009", AuthorityVersion: 7, CredentialVersion: 11,
		PolicySHA256: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := NewSignedManagedLarkEnvironmentIssuer(
		signer, authorities, "cli_agentserver_sg",
		func() (string, error) { return "91000000-0000-4000-8000-000000000009", nil },
		func() time.Time { return now }, 60*time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	request := testManagedLarkEnvironmentRequest(now)
	environment, err := issuer.IssueManagedProcessEnvironment(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	placeholder := environment[ManagedLarkUserAccessTokenEnvironment]
	if placeholder == "" || len(environment) != 5 ||
		environment[ManagedLarkApplicationIDEnvironment] != "cli_agentserver_sg" ||
		environment[ManagedLarkNoUpdateNotifierEnvironment] != "1" ||
		environment[ManagedLarkNoSkillsNotifierEnvironment] != "1" ||
		environment[ManagedLarkPathEnvironment] != ManagedLarkPathValue {
		t.Fatalf("managed environment = %+v", environment)
	}
	if _, err := injectManagedProcessEnvironment(t.Context(), issuer, request, map[string]string{ManagedLarkPathEnvironment: "/workspace"}); err == nil {
		t.Fatal("managed Lark caller was allowed to override the pinned PATH")
	}
	verifier, err := egresscapability.NewVerifier([]egresscapability.TrustedKey{{
		Issuer: "execution-gateway", Audience: egresscapability.AudienceForProvider("lark"),
		KeyID: "egress-key-1", PublicKey: privateKey.Public().(ed25519.PublicKey),
	}})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifier.Verify(placeholder, now)
	if err != nil {
		t.Fatal(err)
	}
	if claims.WorkspaceID != request.Principal.WorkspaceID || claims.SessionID != request.Principal.SessionID ||
		claims.ActorID != request.Principal.ActorID || claims.OperationID != request.Operation.OperationID ||
		claims.SandboxID != request.Target.ID || claims.TargetGeneration != request.Target.Generation ||
		claims.BindingID != "90000000-0000-4000-8000-000000000009" || claims.AuthorityVersion != 7 ||
		claims.ExpiresAtUnixMS != now.Add(30*time.Second).UnixMilli() {
		t.Fatalf("placeholder claims = %+v", claims)
	}
}

func TestSignedManagedLarkEnvironmentIssuerWithholdsPlaceholderFromOtherCommands(t *testing.T) {
	now := time.Now().UTC()
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, "managed-lark-withhold-seed")
	signer, err := egresscapability.NewSigner("execution-gateway", "egress-key-1", ed25519.NewKeyFromSeed(seed))
	if err != nil {
		t.Fatal(err)
	}
	authorities, err := NewFrozenManagedLarkEgressAuthoritySource(ManagedLarkEgressAuthority{
		CredentialMode: managedcredential.ModeWebhookSwap,
		BindingID:      "90000000-0000-4000-8000-000000000009", AuthorityVersion: 1, CredentialVersion: 1,
		PolicySHA256: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := NewSignedManagedLarkEnvironmentIssuer(signer, authorities, "cli_agentserver_sg",
		func() (string, error) { return "91000000-0000-4000-8000-000000000009", nil }, func() time.Time { return now }, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	request := testManagedLarkEnvironmentRequest(now)
	request.Executable = "curl"
	environment, err := issuer.IssueManagedProcessEnvironment(t.Context(), request)
	if err != nil || len(environment) != 0 {
		t.Fatalf("curl environment = %+v, %v", environment, err)
	}
}

func testManagedLarkEnvironmentRequest(now time.Time) ManagedProcessEnvironmentRequest {
	principal := ExecutorMCPPrincipal{
		CapabilityID:      "mcp-capability-1",
		WorkspaceID:       "40000000-0000-4000-8000-000000000004",
		SessionID:         "41000000-0000-4000-8000-000000000004",
		ActorID:           "42000000-0000-4000-8000-000000000004",
		ToolCatalogDigest: strings.Repeat("c", 64), MaxApprovalTTL: time.Minute,
		RunDeadline: now.Add(30 * time.Second), CapabilityExpiresAt: now.Add(40 * time.Second),
		Run: ExecutorMCPRunContext{
			RunID:        "43000000-0000-4000-8000-000000000004",
			RunAttemptID: "44000000-0000-4000-8000-000000000004", RunAttemptGeneration: 2,
			HolderID: "holder-1", ExpectedRunVersion: 3, ExpectedRunAttemptVersion: 4,
		},
	}
	target := executionbackend.Target{
		Kind: executionbackend.KindTAE, ID: "45000000-0000-4000-8000-000000000004", Generation: 5,
		EnvironmentID: "46000000-0000-4000-8000-000000000004",
	}
	return ManagedProcessEnvironmentRequest{
		Principal: principal, Target: target, ToolName: "shell", Executable: "lark-cli",
		Operation: executionbackend.OperationContext{
			WorkspaceID: principal.WorkspaceID, SessionID: principal.SessionID,
			RunID: principal.Run.RunID, RunAttemptID: principal.Run.RunAttemptID,
			RunAttemptGeneration: principal.Run.RunAttemptGeneration,
			ExecutionID:          "47000000-0000-4000-8000-000000000004",
			OperationID:          "48000000-0000-4000-8000-000000000004",
			MutationKey:          "49000000-0000-4000-8000-000000000004",
		},
	}
}
