package egressgateway

import (
	"context"
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/egresscapability"
)

type providerCredentialResolverFunc func(context.Context, corecredentials.UseRequest) (corecredentials.HeaderMutation, corecredentials.ResolveResult, error)

func (function providerCredentialResolverFunc) ResolveInjection(ctx context.Context, request corecredentials.UseRequest) (corecredentials.HeaderMutation, corecredentials.ResolveResult, error) {
	return function(ctx, request)
}

type providerAuditSinkFunc func(context.Context, AuditRecord) error

func (function providerAuditSinkFunc) RecordEgressDecision(ctx context.Context, record AuditRecord) error {
	return function(ctx, record)
}

type providerZTIVerifier struct{ psm string }

func (verifier providerZTIVerifier) VerifyZTI(context.Context, string) (ZTIPrincipal, error) {
	return ZTIPrincipal{PSM: verifier.psm}, nil
}

func TestProviderServiceCarriesResolvedCredentialVersionIntoAllowAudit(t *testing.T) {
	now := time.Date(2026, 8, 8, 9, 30, 0, 0, time.UTC)
	placeholder, verifier := providerPlaceholder(t, now)
	const (
		ztiToken = "signed-zti-token"
		psm      = "bytedance.sandbox.agentserver"
	)
	var resolvedRequest corecredentials.UseRequest
	resolver := providerCredentialResolverFunc(func(_ context.Context, request corecredentials.UseRequest) (corecredentials.HeaderMutation, corecredentials.ResolveResult, error) {
		resolvedRequest = request
		return corecredentials.HeaderMutation{Headers: map[string]string{AuthorizationHeader: "Bearer real-lark-token"}}, corecredentials.ResolveResult{
			ProviderKind: "lark",
			Binding: corecredentials.BindingMetadata{
				ID: "binding-1", Kind: "lark", AuthorityVersion: 3, CredentialVersion: 7,
			},
			AuthorityVersion: 3, CredentialVersion: 7, ResolvedAt: now,
		}, nil
	})
	var audits []AuditRecord
	audit := providerAuditSinkFunc(func(_ context.Context, record AuditRecord) error {
		audits = append(audits, record)
		return nil
	})
	service, err := NewProviderService(ProviderServiceConfig{
		Placeholders: verifier, ZTI: providerZTIVerifier{psm: psm}, Resolver: resolver,
		Policy: ProviderPolicyFunc(func(providerKind, host, requestPath, method, digest string) bool {
			return providerKind == "lark" && host == "open.feishu.cn" &&
				requestPath == "/open-apis/docx/v1/documents/document-1/raw_content" &&
				method == "GET" && digest == strings.Repeat("a", 64)
		}),
		Audit: audit, AllowedPSM: psm, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	decision := service.Authorize(t.Context(), OriginalRequest{
		Host: "open.feishu.cn", Path: "/open-apis/docx/v1/documents/document-1/raw_content", Method: "GET",
		Headers: map[string]string{AuthorizationHeader: "Bearer " + placeholder, ZTIHeader: ztiToken},
	}, ztiToken)
	if !decision.Allow || decision.ReasonCode != "allowed" || decision.Headers[AuthorizationHeader] != "Bearer real-lark-token" {
		t.Fatalf("decision = %+v", decision)
	}
	if resolvedRequest.Headers[AuthorizationHeader] != "Bearer "+placeholder {
		t.Fatalf("resolver authorization header = %q", resolvedRequest.Headers[AuthorizationHeader])
	}
	for name := range resolvedRequest.Headers {
		if strings.EqualFold(name, ZTIHeader) {
			t.Fatal("ZTI header was forwarded from the webhook request into Core")
		}
	}
	if len(audits) != 1 || audits[0].Decision != "allow" || audits[0].CredentialVersion != 7 ||
		audits[0].BindingID != "binding-1" || audits[0].AuthorityVersion != 3 {
		t.Fatalf("allow audits = %+v", audits)
	}
}

func TestProviderServiceRejectsOutOfScopeResolverResult(t *testing.T) {
	now := time.Date(2026, 8, 8, 9, 30, 0, 0, time.UTC)
	placeholder, verifier := providerPlaceholder(t, now)
	const (
		ztiToken = "signed-zti-token"
		psm      = "bytedance.sandbox.agentserver"
	)
	resolver := providerCredentialResolverFunc(func(context.Context, corecredentials.UseRequest) (corecredentials.HeaderMutation, corecredentials.ResolveResult, error) {
		return corecredentials.HeaderMutation{Headers: map[string]string{AuthorizationHeader: "Bearer real-lark-token"}}, corecredentials.ResolveResult{
			ProviderKind: "lark", Binding: corecredentials.BindingMetadata{ID: "another-binding"},
			AuthorityVersion: 3, CredentialVersion: 7, ResolvedAt: now,
		}, nil
	})
	var audit AuditRecord
	service, err := NewProviderService(ProviderServiceConfig{
		Placeholders: verifier, ZTI: providerZTIVerifier{psm: psm}, Resolver: resolver,
		Policy:     ProviderPolicyFunc(func(string, string, string, string, string) bool { return true }),
		Audit:      providerAuditSinkFunc(func(_ context.Context, record AuditRecord) error { audit = record; return nil }),
		AllowedPSM: psm, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	decision := service.Authorize(t.Context(), OriginalRequest{
		Host: "open.feishu.cn", Path: "/open-apis/docx/v1/documents/document-1/raw_content", Method: "GET",
		Headers: map[string]string{AuthorizationHeader: "Bearer " + placeholder, ZTIHeader: ztiToken},
	}, ztiToken)
	if decision.Allow || decision.ReasonCode != "core_credential_denied" || audit.Decision != "deny" || audit.CredentialVersion != 0 {
		t.Fatalf("decision = %+v, audit = %+v", decision, audit)
	}
}

func providerPlaceholder(t *testing.T, now time.Time) (string, *egresscapability.Verifier) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, "provider-service-placeholder-seed")
	privateKey := ed25519.NewKeyFromSeed(seed)
	audience := egresscapability.AudienceForProvider("lark")
	signer, err := egresscapability.NewSigner("execution-gateway", "provider-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := egresscapability.NewVerifier([]egresscapability.TrustedKey{{
		Issuer: "execution-gateway", Audience: audience, KeyID: "provider-key-1",
		PublicKey: privateKey.Public().(ed25519.PublicKey),
	}})
	if err != nil {
		t.Fatal(err)
	}
	placeholder, err := signer.Sign(egresscapability.Claims{
		Version: egresscapability.Version, Issuer: "execution-gateway", Audience: audience,
		CapabilityID: "capability-1", WorkspaceID: "workspace-1", SessionID: "session-1",
		ActorID: "actor-1", EnvironmentID: "environment-1", RunID: "run-1",
		RunAttemptID: "attempt-1", RunAttemptGeneration: 2, ExecutionID: "execution-1",
		OperationID: "operation-1", SandboxID: "sandbox-1", TargetGeneration: 3,
		PackID: egresscapability.PackLarkReadOnly, ProviderKind: "lark", BindingID: "binding-1",
		AuthorityVersion: 3, PolicySHA256: strings.Repeat("a", 64), Executable: "lark-cli",
		IssuedAtUnixMS: now.Add(-time.Second).UnixMilli(), ExpiresAtUnixMS: now.Add(time.Minute).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return placeholder, verifier
}
