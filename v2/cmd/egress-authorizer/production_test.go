package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"code.byted.org/security/go-spiffe-v2/spiffeid"
	"code.byted.org/security/zti-jwt-golang/common"
	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/taepolicy"
	"github.com/go-jose/go-jose/v3/jwt"
)

func TestProductionProviderPolicyUsesExactCompiledLarkPaths(t *testing.T) {
	config := egressAuthorizerConfig{taePolicy: taepolicy.Binding{PolicySHA256: larkegresspolicy.SHA256Hex()}}
	policy, err := productionProviderPolicy(config)
	if err != nil {
		t.Fatal(err)
	}
	digest := larkegresspolicy.SHA256Hex()
	for _, allowed := range []string{
		"/open-apis/wiki/v2/spaces/get_node",
		"/open-apis/docx/v1/documents/doc-1",
		"/open-apis/docx/v1/documents/doc-1/raw_content",
		"/open-apis/docx/v1/documents/doc-1/blocks/block-1/children",
	} {
		if !policy.Allows("lark", larkegresspolicy.OpenAPIHost, allowed, "GET", digest) {
			t.Fatalf("compiled policy denied %q", allowed)
		}
	}
	for _, denied := range []string{
		"/open-apis/docx/v1/documents/doc-1/unknown",
		"/open-apis/docx/v1/documents-old/doc-1",
		"/open-apis/docx/v1/documents/doc-1/blocks/block-1/children/extra",
	} {
		if policy.Allows("lark", larkegresspolicy.OpenAPIHost, denied, "GET", digest) {
			t.Fatalf("compiled policy allowed unknown path %q", denied)
		}
	}
	if policy.Allows("lark", larkegresspolicy.OpenAPIHost, "/open-apis/docx/v1/documents/doc-1", "POST", digest) ||
		policy.Allows("github", larkegresspolicy.OpenAPIHost, "/open-apis/docx/v1/documents/doc-1", "GET", digest) ||
		policy.Allows("lark", larkegresspolicy.OpenAPIHost, "/open-apis/docx/v1/documents/doc-1", "GET", strings.Repeat("f", 64)) {
		t.Fatal("provider policy accepted an out-of-scope method, provider, or digest")
	}
}

func TestProductionZTIVerifierRequiresVerifiedExactROWSPIFFESubject(t *testing.T) {
	now := time.Date(2026, 8, 6, 23, 30, 0, 0, time.UTC)
	const allowedPSM = "prod.tae.agent-gateway"
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *common.ZeroTrustIdentity)
		wantOK bool
	}{
		{name: "exact ROW TAE workload", wantOK: true},
		{name: "signature downgrade", mutate: func(_ *testing.T, identity *common.ZeroTrustIdentity) {
			identity.Claims = nil
		}},
		{name: "wrong trust domain", mutate: func(t *testing.T, identity *common.ZeroTrustIdentity) {
			identity.SpiffeID = mustProductionSPIFFEID(t, "spiffe://prod-cn.byted.org/ns:tce/r:sg/vdc:sg1/id:"+allowedPSM)
			identity.Claims.Subject = identity.SpiffeID.String()
		}},
		{name: "wrong PSM", mutate: func(t *testing.T, identity *common.ZeroTrustIdentity) {
			identity.SpiffeID = mustProductionSPIFFEID(t, "spiffe://prod-row.byted.org/ns:tce/r:sg/vdc:sg1/id:prod.tae.other")
			identity.Claims.Subject = identity.SpiffeID.String()
		}},
		{name: "subject mismatch", mutate: func(_ *testing.T, identity *common.ZeroTrustIdentity) {
			identity.Claims.Subject = "spiffe://prod-row.byted.org/ns:tce/id:prod.tae.other"
		}},
		{name: "expired", mutate: func(_ *testing.T, identity *common.ZeroTrustIdentity) {
			identity.Expiry = now.Add(-time.Second)
			identity.Claims.Expiry = jwt.NewNumericDate(identity.Expiry)
		}},
		{name: "future not-before", mutate: func(_ *testing.T, identity *common.ZeroTrustIdentity) {
			identity.Claims.NotBefore = jwt.NewNumericDate(now.Add(time.Minute))
		}},
		{name: "delegated principal", mutate: func(t *testing.T, identity *common.ZeroTrustIdentity) {
			identity.DelegatedPrincipalSpiffeID = mustProductionSPIFFEID(t, "spiffe://prod-row.byted.org/ns:user/id:someone")
		}},
		{name: "legacy PSM mismatch", mutate: func(_ *testing.T, identity *common.ZeroTrustIdentity) {
			identity.LegacyID.PSM = "prod.tae.other"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			identity := productionTestZTI(t, now, allowedPSM)
			if test.mutate != nil {
				test.mutate(t, identity)
			}
			verifier, err := newProductionZTIVerifier(allowedPSM, func(raw, _, purpose string) (*common.ZeroTrustIdentity, error) {
				if raw != "signed-zti-token-value" || purpose != "agentserver-egress-authorizer" {
					t.Fatalf("verify arguments = %q / %q", raw, purpose)
				}
				return identity, nil
			}, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			principal, verifyError := verifier.VerifyZTI(t.Context(), "signed-zti-token-value")
			if test.wantOK {
				if verifyError != nil || principal.PSM != allowedPSM || principal.User != "tae-workload" {
					t.Fatalf("principal = %#v, error = %v", principal, verifyError)
				}
			} else if verifyError == nil {
				t.Fatalf("unsafe ZTI identity was accepted: %#v", principal)
			}
		})
	}
}

func TestProductionZTIVerifierHonorsCancelledWebhookContext(t *testing.T) {
	called := false
	verifier, err := newProductionZTIVerifier("prod.tae.agent-gateway", func(string, string, string) (*common.ZeroTrustIdentity, error) {
		called = true
		return nil, nil
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := verifier.VerifyZTI(ctx, "signed-zti-token-value"); err == nil || called {
		t.Fatalf("cancelled verification error = %v, SDK called = %t", err, called)
	}
}

func productionTestZTI(t *testing.T, now time.Time, psm string) *common.ZeroTrustIdentity {
	t.Helper()
	expiry := now.Add(5 * time.Minute)
	subject := "spiffe://prod-row.byted.org/ns:tce/r:sg/vdc:sg1/id:" + psm
	return &common.ZeroTrustIdentity{
		SpiffeID: mustProductionSPIFFEID(t, subject), Expiry: expiry,
		Claims: &jwt.Claims{
			Subject: subject, Expiry: jwt.NewNumericDate(expiry),
			IssuedAt: jwt.NewNumericDate(now.Add(-time.Second)),
		},
		LegacyID: &common.LegacyIdentity{PSM: psm, User: "tae-workload", ExpireTime: expiry.Unix()},
	}
}

func mustProductionSPIFFEID(t *testing.T, raw string) spiffeid.ID {
	t.Helper()
	identity, err := spiffeid.FromString(raw)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}
