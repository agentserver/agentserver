package executorgateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/bkectlpolicy"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

func TestWorkspaceManagedEnvironmentIssuerInjectsByteCloudJWTOnlyForReadOnlyBkectl(t *testing.T) {
	now := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	authority := ManagedCredentialAuthority{
		CredentialMode:   managedcredential.ModeProcessEnv,
		ProviderKind:     bkectlpolicy.CredentialKind,
		BindingID:        "90000000-0000-4000-8000-000000000009",
		AuthorityVersion: 3, CredentialVersion: 7,
		PolicySHA256: bkectlpolicy.SHA256Hex(),
	}
	authorityCalls, credentialCalls := 0, 0
	authorities := workspaceAuthoritySourceFunc(func(context.Context, ManagedProcessEnvironmentRequest) (ManagedCredentialAuthority, error) {
		authorityCalls++
		return authority, nil
	})
	credentials := workspaceProcessCredentialSourceFunc(func(
		_ context.Context,
		_ ManagedProcessEnvironmentRequest,
		taePSM string,
		selected ManagedCredentialAuthority,
	) (ManagedProcessCredential, error) {
		credentialCalls++
		return ManagedProcessCredential{
			Configured: true, CredentialMode: managedcredential.ModeProcessEnv,
			ProviderKind: selected.ProviderKind, Credential: "workspace-bytecloud-jwt",
			BindingID: selected.BindingID, AuthorityVersion: selected.AuthorityVersion,
			CredentialVersion: selected.CredentialVersion, PolicySHA256: selected.PolicySHA256,
			TAEPSM: taePSM, ResolvedAt: now,
		}, nil
	})
	issuer, err := NewDirectWorkspaceManagedEnvironmentIssuer(
		authorities, credentials, "bytedance.sandbox.agentserver", nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	request := testManagedLarkEnvironmentRequest(now)
	request.Executable = bkectlpolicy.Executable
	request.Arguments = []string{"bytetree", "node", "get", "--id", "4428303", "--region", "i18nbd", "--json"}
	environment, err := issuer.IssueManagedProcessEnvironment(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(environment) != 4 || environment[ManagedBkectlJWTEnvironment] != "workspace-bytecloud-jwt" ||
		environment[ManagedBkectlAuthModeEnvironment] != ManagedBkectlAuthModeValue ||
		environment[ManagedBkectlRegionEnvironment] != ManagedBkectlRegionValue ||
		environment[ManagedToolPathEnvironment] != ManagedToolPathValue ||
		environment[ManagedLarkUserAccessTokenEnvironment] != "" {
		t.Fatalf("managed bkectl environment = %#v", environment)
	}
	if authorityCalls != 1 || credentialCalls != 1 {
		t.Fatalf("managed bkectl resolver calls = authority %d, credential %d", authorityCalls, credentialCalls)
	}

	request.Arguments = []string{"--help"}
	discovery, err := issuer.IssueManagedProcessEnvironment(t.Context(), request)
	if err != nil || len(discovery) != 1 || discovery[ManagedToolPathEnvironment] != ManagedToolPathValue ||
		authorityCalls != 1 || credentialCalls != 1 {
		t.Fatalf("bkectl discovery environment/calls = %#v, %v / %d/%d", discovery, err, authorityCalls, credentialCalls)
	}

	for name, arguments := range map[string][]string{
		"credential disclosure": {"auth", "get", "jwt", "--json"},
		"write":                 {"bytesd", "node", "block", "--ip", "10.0.0.1"},
		"risky global flag":     {"k8s", "pod", "get", "--debug"},
		"unknown":               {"future", "command", "get"},
	} {
		t.Run(name, func(t *testing.T) {
			request.Arguments = arguments
			if environment, err := issuer.IssueManagedProcessEnvironment(t.Context(), request); err == nil || len(environment) != 0 ||
				authorityCalls != 1 || credentialCalls != 1 {
				t.Fatalf("unsafe bkectl invocation received environment: %#v, %v / %d/%d", environment, err, authorityCalls, credentialCalls)
			}
		})
	}
}

func TestWorkspaceManagedEnvironmentIssuerRejectsBkectlWebhookAndMissingBinding(t *testing.T) {
	now := time.Now().UTC()
	request := testManagedLarkEnvironmentRequest(now)
	request.Executable = bkectlpolicy.Executable
	request.Arguments = []string{"bytetree", "node", "get", "--id", "4428303", "--json"}
	authority := ManagedCredentialAuthority{
		CredentialMode:   managedcredential.ModeWebhookSwap,
		ProviderKind:     bkectlpolicy.CredentialKind,
		BindingID:        "90000000-0000-4000-8000-000000000009",
		AuthorityVersion: 3, CredentialVersion: 7,
		PolicySHA256: bkectlpolicy.SHA256Hex(),
	}
	issuer, err := NewDirectWorkspaceManagedEnvironmentIssuer(
		workspaceAuthoritySourceFunc(func(context.Context, ManagedProcessEnvironmentRequest) (ManagedCredentialAuthority, error) {
			return authority, nil
		}),
		workspaceProcessCredentialSourceFunc(func(context.Context, ManagedProcessEnvironmentRequest, string, ManagedCredentialAuthority) (ManagedProcessCredential, error) {
			t.Fatal("bkectl webhook mode attempted to resolve a process credential")
			return ManagedProcessCredential{}, nil
		}),
		"bytedance.sandbox.agentserver", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if environment, err := issuer.IssueManagedProcessEnvironment(t.Context(), request); err == nil || len(environment) != 0 {
		t.Fatalf("bkectl webhook mode was accepted: %#v, %v", environment, err)
	}

	authority = ManagedCredentialAuthority{
		CredentialMode: managedcredential.ModeProcessEnv,
		ProviderKind:   bkectlpolicy.CredentialKind,
		PolicySHA256:   bkectlpolicy.SHA256Hex(),
	}
	if environment, err := issuer.IssueManagedProcessEnvironment(t.Context(), request); err == nil || len(environment) != 0 ||
		!strings.Contains(err.Error(), "no active ByteCloud credential") {
		t.Fatalf("bkectl without workspace binding was accepted: %#v, %v", environment, err)
	}
}
