package egressgateway

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

type providerCredentialResolverFunc func(context.Context, corecredentials.UseRequest) (corecredentials.HeaderMutation, corecredentials.ResolveResult, error)

func (function providerCredentialResolverFunc) ResolveInjection(ctx context.Context, request corecredentials.UseRequest) (corecredentials.HeaderMutation, corecredentials.ResolveResult, error) {
	return function(ctx, request)
}

func (providerCredentialResolverFunc) AuthorizeProcessEnvironmentEgress(context.Context, corecontract.AuthorizeProcessEnvironmentEgressRequest) (corecredentials.HeaderMutation, corecredentials.ResolveResult, error) {
	return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("process environment resolution was not configured by this test")
}

type providerAuditSinkFunc func(context.Context, AuditRecord) error

func (function providerAuditSinkFunc) RecordEgressDecision(ctx context.Context, record AuditRecord) error {
	return function(ctx, record)
}

type processProviderCredentialResolverFunc func(context.Context, corecontract.AuthorizeProcessEnvironmentEgressRequest) (corecredentials.HeaderMutation, corecredentials.ResolveResult, error)

func (function processProviderCredentialResolverFunc) ResolveInjection(context.Context, corecredentials.UseRequest) (corecredentials.HeaderMutation, corecredentials.ResolveResult, error) {
	return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("placeholder resolution was not configured by this test")
}

func (function processProviderCredentialResolverFunc) AuthorizeProcessEnvironmentEgress(
	ctx context.Context,
	request corecontract.AuthorizeProcessEnvironmentEgressRequest,
) (corecredentials.HeaderMutation, corecredentials.ResolveResult, error) {
	return function(ctx, request)
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

func TestProviderServiceAuthorizesRealBearerOnlyWithOperationProofAndSanitizesTrace(t *testing.T) {
	now := time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC)
	proof, verifier, claims := providerProcessEnvironmentProof(t, now)
	const (
		ztiToken = "signed-zti-token"
		psm      = "bytedance.sandbox.agentserver"
	)
	var resolvedRequest corecontract.AuthorizeProcessEnvironmentEgressRequest
	resolver := processProviderCredentialResolverFunc(func(
		_ context.Context,
		request corecontract.AuthorizeProcessEnvironmentEgressRequest,
	) (corecredentials.HeaderMutation, corecredentials.ResolveResult, error) {
		resolvedRequest = request
		return corecredentials.HeaderMutation{Headers: map[string]string{
				AuthorizationHeader:                    "Bearer real-lark-token",
				managedcredential.LarkAgentTraceHeader: managedcredential.LarkSanitizedAgentTrace,
			}}, corecredentials.ResolveResult{
				ProviderKind: "lark",
				Binding: corecredentials.BindingMetadata{
					ID: claims.BindingID, Kind: "lark", AuthorityVersion: claims.AuthorityVersion,
					CredentialVersion: claims.CredentialVersion,
				},
				AuthorityVersion: claims.AuthorityVersion, CredentialVersion: claims.CredentialVersion, ResolvedAt: now,
			}, nil
	})
	var audits []AuditRecord
	service, err := NewProviderService(ProviderServiceConfig{
		Placeholders: verifier, ZTI: providerZTIVerifier{psm: psm}, Resolver: resolver,
		Policy: ProviderPolicyFunc(func(providerKind, host, requestPath, method, digest string) bool {
			return providerKind == "lark" && host == "open.feishu.cn" &&
				requestPath == "/open-apis/docx/v1/documents/document-1/raw_content" &&
				method == "GET" && digest == claims.PolicySHA256
		}),
		Audit: providerAuditSinkFunc(func(_ context.Context, record AuditRecord) error {
			audits = append(audits, record)
			return nil
		}),
		AllowedPSM: psm, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	decision := service.Authorize(t.Context(), OriginalRequest{
		Host: "open.feishu.cn", Path: "/open-apis/docx/v1/documents/document-1/raw_content", Method: "GET",
		Headers: map[string]string{
			AuthorizationHeader:                    "Bearer real-lark-token",
			managedcredential.LarkAgentTraceHeader: proof,
			ZTIHeader:                              ztiToken,
		},
	}, ztiToken)
	if !decision.Allow || decision.Headers[AuthorizationHeader] != "Bearer real-lark-token" ||
		decision.Headers[managedcredential.LarkAgentTraceHeader] != managedcredential.LarkSanitizedAgentTrace ||
		len(decision.Headers) != 2 {
		t.Fatalf("process environment decision = %+v", decision)
	}
	if resolvedRequest.ProcessProof != proof || resolvedRequest.Operation.WorkspaceID != claims.WorkspaceID ||
		resolvedRequest.CredentialVersion != claims.CredentialVersion ||
		resolvedRequest.Headers[managedcredential.LarkAgentTraceHeader] != proof ||
		resolvedRequest.Headers[AuthorizationHeader] != "Bearer real-lark-token" {
		t.Fatalf("Core process environment request = %#v", resolvedRequest)
	}
	for name := range resolvedRequest.Headers {
		if strings.EqualFold(name, ZTIHeader) {
			t.Fatal("ZTI header was forwarded into Core process authorization")
		}
	}
	if len(audits) != 1 || audits[0].Decision != "allow" || audits[0].CapabilityID != claims.CapabilityID ||
		audits[0].WorkspaceID != claims.WorkspaceID || audits[0].CredentialVersion != claims.CredentialVersion {
		t.Fatalf("process environment audits = %+v", audits)
	}
}

func TestProviderServiceDoesNotConfuseMalformedPlaceholderWithProcessBearer(t *testing.T) {
	now := time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC)
	proof, verifier, _ := providerProcessEnvironmentProof(t, now)
	processCalls := 0
	service, err := NewProviderService(ProviderServiceConfig{
		Placeholders: verifier, ZTI: providerZTIVerifier{psm: "bytedance.sandbox.agentserver"},
		Resolver: processProviderCredentialResolverFunc(func(
			context.Context,
			corecontract.AuthorizeProcessEnvironmentEgressRequest,
		) (corecredentials.HeaderMutation, corecredentials.ResolveResult, error) {
			processCalls++
			return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, nil
		}),
		Policy:     ProviderPolicyFunc(func(string, string, string, string, string) bool { return true }),
		Audit:      providerAuditSinkFunc(func(context.Context, AuditRecord) error { return nil }),
		AllowedPSM: "bytedance.sandbox.agentserver", Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	decision := service.Authorize(t.Context(), OriginalRequest{
		Host: "open.feishu.cn", Path: "/open-apis/docx/v1/documents/document-1/raw_content", Method: "GET",
		Headers: map[string]string{
			AuthorizationHeader:                    "Bearer asv2egress1.not-a-valid-placeholder",
			managedcredential.LarkAgentTraceHeader: proof,
			ZTIHeader:                              "signed-zti-token",
		},
	}, "signed-zti-token")
	if decision.Allow || decision.ReasonCode != "placeholder_invalid" || processCalls != 0 {
		t.Fatalf("malformed placeholder decision/calls = %+v / %d", decision, processCalls)
	}

	last := "A"
	if strings.HasSuffix(proof, last) {
		last = "B"
	}
	tampered := proof[:len(proof)-1] + last
	decision = service.Authorize(t.Context(), OriginalRequest{
		Host: "open.feishu.cn", Path: "/open-apis/docx/v1/documents/document-1/raw_content", Method: "GET",
		Headers: map[string]string{
			AuthorizationHeader:                    "Bearer real-lark-token",
			managedcredential.LarkAgentTraceHeader: tampered,
			ZTIHeader:                              "signed-zti-token",
		},
	}, "signed-zti-token")
	if decision.Allow || decision.ReasonCode != "process_environment_proof_invalid" || processCalls != 0 {
		t.Fatalf("tampered process proof decision/calls = %+v / %d", decision, processCalls)
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

func providerProcessEnvironmentProof(
	t *testing.T,
	now time.Time,
) (string, *egresscapability.Verifier, egresscapability.ProcessEnvironmentClaims) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, "provider-process-environment-seed")
	privateKey := ed25519.NewKeyFromSeed(seed)
	signer, err := egresscapability.NewSigner("executor-gateway/egress", "provider-process-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := egresscapability.NewVerifier([]egresscapability.TrustedKey{{
		Issuer: signer.Issuer(), Audience: egresscapability.AudienceForProvider("lark"),
		KeyID: "provider-process-key-1", PublicKey: privateKey.Public().(ed25519.PublicKey),
	}})
	if err != nil {
		t.Fatal(err)
	}
	claims := egresscapability.ProcessEnvironmentClaims{
		Version: egresscapability.ProcessEnvironmentVersion, Issuer: signer.Issuer(),
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
		AuthorityVersion: 4, CredentialVersion: 5, PolicySHA256: strings.Repeat("a", 64),
		IssuedAtUnixMS: now.Add(-time.Second).UnixMilli(), ExpiresAtUnixMS: now.Add(time.Minute).UnixMilli(),
	}
	proof, err := signer.SignProcessEnvironment(claims)
	if err != nil {
		t.Fatal(err)
	}
	return proof, verifier, claims
}
