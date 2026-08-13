package executorgateway

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

type workspaceAuthoritySourceFunc func(context.Context, ManagedProcessEnvironmentRequest) (ManagedLarkEgressAuthority, error)

func (function workspaceAuthoritySourceFunc) ResolveManagedLarkEgressAuthority(
	ctx context.Context,
	request ManagedProcessEnvironmentRequest,
) (ManagedLarkEgressAuthority, error) {
	return function(ctx, request)
}

type workspaceProcessCredentialSourceFunc func(context.Context, ManagedProcessEnvironmentRequest, string, ManagedLarkEgressAuthority) (ManagedLarkProcessCredential, error)

func (function workspaceProcessCredentialSourceFunc) ResolveManagedLarkProcessCredential(
	ctx context.Context,
	request ManagedProcessEnvironmentRequest,
	taePSM string,
	authority ManagedLarkEgressAuthority,
) (ManagedLarkProcessCredential, error) {
	return function(ctx, request, taePSM, authority)
}

func TestWorkspaceManagedLarkEnvironmentIssuerSelectsModePerProcessStart(t *testing.T) {
	now := time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, "workspace-managed-lark-mode-seed")
	privateKey := ed25519.NewKeyFromSeed(seed)
	signer, err := egresscapability.NewSigner("executor-gateway/egress", "workspace-mode-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	authority := ManagedLarkEgressAuthority{
		CredentialMode:   managedcredential.ModeWebhookSwap,
		ApplicationID:    "cli_agentserver_sg",
		BindingID:        "90000000-0000-4000-8000-000000000009",
		AuthorityVersion: 7, CredentialVersion: 11, PolicySHA256: strings.Repeat("a", 64),
	}
	authorityCalls, credentialCalls := 0, 0
	authorities := workspaceAuthoritySourceFunc(func(context.Context, ManagedProcessEnvironmentRequest) (ManagedLarkEgressAuthority, error) {
		authorityCalls++
		return authority, nil
	})
	credentials := workspaceProcessCredentialSourceFunc(func(
		_ context.Context,
		_ ManagedProcessEnvironmentRequest,
		taePSM string,
		selected ManagedLarkEgressAuthority,
	) (ManagedLarkProcessCredential, error) {
		credentialCalls++
		return ManagedLarkProcessCredential{
			Configured: true, CredentialMode: managedcredential.ModeProcessEnv,
			AccessToken: "real-workspace-token", ApplicationID: selected.ApplicationID, BindingID: selected.BindingID,
			AuthorityVersion: selected.AuthorityVersion, CredentialVersion: selected.CredentialVersion,
			PolicySHA256: selected.PolicySHA256, TAEPSM: taePSM, ResolvedAt: now,
		}, nil
	})
	capabilityIndex := 0
	capabilityIDs := []string{
		"91000000-0000-4000-8000-000000000009",
		"92000000-0000-4000-8000-000000000009",
	}
	issuer, err := NewWorkspaceManagedLarkEnvironmentIssuer(
		signer, authorities, credentials, "bytedance.sandbox.agentserver",
		func() (string, error) {
			if capabilityIndex >= len(capabilityIDs) {
				return "", errors.New("test capability IDs exhausted")
			}
			value := capabilityIDs[capabilityIndex]
			capabilityIndex++
			return value, nil
		},
		func() time.Time { return now }, time.Minute, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request := testManagedLarkEnvironmentRequest(now)

	webhookEnvironment, err := issuer.IssueManagedProcessEnvironment(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	placeholder := webhookEnvironment[ManagedLarkUserAccessTokenEnvironment]
	if !egresscapability.IsPlaceholderToken(placeholder) || webhookEnvironment[ManagedLarkApplicationIDEnvironment] != authority.ApplicationID ||
		webhookEnvironment[ManagedLarkAgentTraceEnvironment] != "" || credentialCalls != 0 {
		t.Fatalf("webhook workspace environment/calls = %#v / %d", webhookEnvironment, credentialCalls)
	}

	authority.CredentialMode = managedcredential.ModeProcessEnv
	processEnvironment, err := issuer.IssueManagedProcessEnvironment(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if processEnvironment[ManagedLarkUserAccessTokenEnvironment] != "real-workspace-token" ||
		processEnvironment[ManagedLarkApplicationIDEnvironment] != authority.ApplicationID ||
		processEnvironment[ManagedLarkAgentTraceEnvironment] != "" ||
		len(processEnvironment) != 5 || credentialCalls != 1 || authorityCalls != 2 {
		t.Fatalf("process workspace environment/calls = %#v / authority=%d credential=%d", processEnvironment, authorityCalls, credentialCalls)
	}

	request.Executable = "curl"
	withheld, err := issuer.IssueManagedProcessEnvironment(t.Context(), request)
	if err != nil || len(withheld) != 0 || authorityCalls != 2 || credentialCalls != 1 {
		t.Fatalf("non-Lark environment/calls = %#v, %v / authority=%d credential=%d", withheld, err, authorityCalls, credentialCalls)
	}
}

func TestWorkspaceManagedLarkEnvironmentIssuerFailsClosedAcrossModeAndVersionChanges(t *testing.T) {
	now := time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, "workspace-managed-lark-fence-seed")
	signer, err := egresscapability.NewSigner("executor-gateway/egress", "workspace-fence-key-1", ed25519.NewKeyFromSeed(seed))
	if err != nil {
		t.Fatal(err)
	}
	authority := ManagedLarkEgressAuthority{
		CredentialMode:   managedcredential.ModeProcessEnv,
		ApplicationID:    "cli_agentserver_sg",
		BindingID:        "90000000-0000-4000-8000-000000000009",
		AuthorityVersion: 7, CredentialVersion: 11, PolicySHA256: strings.Repeat("b", 64),
	}
	authorities := workspaceAuthoritySourceFunc(func(context.Context, ManagedProcessEnvironmentRequest) (ManagedLarkEgressAuthority, error) {
		return authority, nil
	})
	credential := ManagedLarkProcessCredential{
		Configured: true, CredentialMode: managedcredential.ModeProcessEnv,
		AccessToken: "stale-token", ApplicationID: authority.ApplicationID, BindingID: authority.BindingID,
		AuthorityVersion: authority.AuthorityVersion, CredentialVersion: authority.CredentialVersion - 1,
		PolicySHA256: authority.PolicySHA256, TAEPSM: "bytedance.sandbox.agentserver", ResolvedAt: now,
	}
	credentials := workspaceProcessCredentialSourceFunc(func(
		context.Context,
		ManagedProcessEnvironmentRequest,
		string,
		ManagedLarkEgressAuthority,
	) (ManagedLarkProcessCredential, error) {
		return credential, nil
	})
	issuer, err := NewWorkspaceManagedLarkEnvironmentIssuer(
		signer, authorities, credentials, "bytedance.sandbox.agentserver",
		func() (string, error) { return "91000000-0000-4000-8000-000000000009", nil },
		func() time.Time { return now }, time.Minute, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if environment, err := issuer.IssueManagedProcessEnvironment(t.Context(), testManagedLarkEnvironmentRequest(now)); err == nil || len(environment) != 0 {
		t.Fatalf("stale workspace credential was injected: %#v, %v", environment, err)
	}

	credential.CredentialVersion = authority.CredentialVersion
	credential.ApplicationID = "cli_another_app"
	if environment, err := issuer.IssueManagedProcessEnvironment(t.Context(), testManagedLarkEnvironmentRequest(now)); err == nil || len(environment) != 0 {
		t.Fatalf("cross-application workspace credential was injected: %#v, %v", environment, err)
	}

	authority.CredentialMode = "global"
	if environment, err := issuer.IssueManagedProcessEnvironment(t.Context(), testManagedLarkEnvironmentRequest(now)); err == nil || len(environment) != 0 {
		t.Fatalf("unknown workspace mode was accepted: %#v, %v", environment, err)
	}
}

func TestDirectWorkspaceManagedLarkEnvironmentIssuerRejectsWebhookMode(t *testing.T) {
	now := time.Now().UTC()
	authority := ManagedLarkEgressAuthority{
		CredentialMode: managedcredential.ModeWebhookSwap, ApplicationID: "cli_agentserver_sg",
		BindingID: "90000000-0000-4000-8000-000000000009", AuthorityVersion: 9,
		CredentialVersion: 1, PolicySHA256: strings.Repeat("a", 64),
	}
	issuer, err := NewDirectWorkspaceManagedLarkEnvironmentIssuer(
		workspaceAuthoritySourceFunc(func(context.Context, ManagedProcessEnvironmentRequest) (ManagedLarkEgressAuthority, error) {
			return authority, nil
		}),
		workspaceProcessCredentialSourceFunc(func(context.Context, ManagedProcessEnvironmentRequest, string, ManagedLarkEgressAuthority) (ManagedLarkProcessCredential, error) {
			t.Fatal("direct profile resolved a process credential for webhook mode")
			return ManagedLarkProcessCredential{}, nil
		}),
		"bytedance.sandbox.agentserver", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if environment, err := issuer.IssueManagedProcessEnvironment(t.Context(), testManagedLarkEnvironmentRequest(now)); err == nil || len(environment) != 0 {
		t.Fatalf("direct profile accepted webhook mode: %#v, %v", environment, err)
	}
}
