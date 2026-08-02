package llmproxy

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
)

const (
	testIssuer   = "https://agentserver.example.test/core"
	testKeyID    = "production-llmproxy-test-key"
	testModel    = "gpt-5.6-codex"
	testProvider = corecontract.WorkspaceLLMGatewayProvider
)

func TestProductionAuthenticatorVerifiesAndLiveAuthorizesEveryModelRequest(t *testing.T) {
	now := time.Date(2026, 8, 2, 15, 0, 0, 0, time.UTC)
	claims := productionModelClaims(now)
	token, verifier := signProductionClaims(t, claims)
	authorizer := &recordingAuthorizer{result: productionModelAuthorization(claims, now)}
	authenticator, err := NewProductionAuthenticator(verifier, authorizer, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "https://llmproxy.test/v1/responses", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	for range 2 {
		principal, err := authenticator.AuthenticateModelRequest(request, testModel)
		if err != nil {
			t.Fatal(err)
		}
		if principal.CapabilityID != claims.CapabilityID || principal.WorkspaceID != claims.WorkspaceID ||
			principal.SessionID != claims.SessionID || principal.RunID != claims.RunID ||
			principal.RunAttemptID != claims.RunAttemptID || principal.RunAttemptGeneration != claims.RunAttemptGeneration ||
			principal.ActorID != claims.ActorID || principal.HolderID != claims.HolderID ||
			principal.Model != testModel || principal.Provider != testProvider ||
			!principal.RunDeadline.Equal(time.UnixMilli(claims.RunDeadlineUnixMS)) ||
			!principal.CapabilityExpiresAt.Equal(time.UnixMilli(claims.ExpiresAtUnixMS)) || !principal.AuthorizedAt.Equal(now) {
			t.Fatalf("production llmproxy principal = %+v", principal)
		}
	}
	if len(authorizer.calls) != 2 {
		t.Fatalf("Core live authorization calls = %d", len(authorizer.calls))
	}
	for _, call := range authorizer.calls {
		if call.Token != token || call.CapabilityID != claims.CapabilityID || call.Model != testModel ||
			call.Provider != testProvider || call.RunID != claims.RunID || call.RunAttemptID != claims.RunAttemptID ||
			call.RunAttemptGeneration != claims.RunAttemptGeneration || call.LLMGatewayID != claims.LLMGatewayID ||
			call.LLMGatewayVersion != claims.LLMGatewayVersion || call.LLMGatewayGrantUserID != claims.LLMGatewayGrantUserID {
			t.Fatalf("Core live authorization request = %+v", call)
		}
	}
}

func TestProductionAuthenticatorFailsClosedBeforeAndAfterCore(t *testing.T) {
	now := time.Date(2026, 8, 2, 15, 30, 0, 0, time.UTC)
	claims := productionModelClaims(now)
	validToken, verifier := signProductionClaims(t, claims)

	deadlineClaims := claims
	deadlineClaims.RunDeadlineUnixMS = now.UnixMilli()
	deadlineToken, deadlineVerifier := signProductionClaims(t, deadlineClaims)
	for _, test := range []struct {
		name     string
		token    string
		verifier *runcapability.ProductionVerifier
		model    string
	}{
		{name: "hard deadline", token: deadlineToken, verifier: deadlineVerifier, model: testModel},
		{name: "request model", token: validToken, verifier: verifier, model: "gpt-other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			authorizer := &recordingAuthorizer{}
			authenticator, err := NewProductionAuthenticator(test.verifier, authorizer, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("POST", "https://llmproxy.test/v1/responses", nil)
			request.Header.Set("Authorization", "Bearer "+test.token)
			if _, err := authenticator.AuthenticateModelRequest(request, test.model); err == nil || len(authorizer.calls) != 0 {
				t.Fatalf("local route denial error/calls = %v / %d", err, len(authorizer.calls))
			}
		})
	}

	executorClaims := claims
	executorClaims.Audience = runcapability.AudienceExecutorMCP
	executorClaims.Model = ""
	executorClaims.Provider = ""
	executorClaims.LLMGatewayID = ""
	executorClaims.LLMGatewayVersion = 0
	executorClaims.LLMGatewayGrantUserID = ""
	executorClaims.ExecutorID = "96000000-0000-4000-8000-000000000001"
	executorClaims.ToolCatalogDigest = strings.Repeat("a", 64)
	executorClaims.ExpectedRunVersion = 5
	executorClaims.ExpectedRunAttemptVersion = 6
	executorClaims.MaxApprovalTTLMillis = 10_000
	executorToken, _ := signProductionClaims(t, executorClaims)
	for name, authorization := range map[string][]string{
		"wrong audience": {"Bearer " + executorToken},
		"duplicate":      {"Bearer " + validToken, "Bearer " + validToken},
		"padded":         {"Bearer  " + validToken},
	} {
		t.Run(name, func(t *testing.T) {
			authorizer := &recordingAuthorizer{}
			authenticator, _ := NewProductionAuthenticator(verifier, authorizer, func() time.Time { return now })
			request := httptest.NewRequest("POST", "https://llmproxy.test/v1/responses", nil)
			request.Header["Authorization"] = authorization
			if _, err := authenticator.AuthenticateModelRequest(request, testModel); err == nil || len(authorizer.calls) != 0 {
				t.Fatalf("framing/audience denial error/calls = %v / %d", err, len(authorizer.calls))
			}
		})
	}

	coreFailure := &recordingAuthorizer{err: errors.New("Core unavailable " + validToken)}
	authenticator, _ := NewProductionAuthenticator(verifier, coreFailure, func() time.Time { return now })
	request := httptest.NewRequest("POST", "https://llmproxy.test/v1/responses", nil)
	request.Header.Set("Authorization", "Bearer "+validToken)
	if _, err := authenticator.AuthenticateModelRequest(request, testModel); err == nil || strings.Contains(err.Error(), validToken) {
		t.Fatalf("Core failure error = %v", err)
	}

	drift := &recordingAuthorizer{result: productionModelAuthorization(claims, now)}
	drift.result.RunID = "97000000-0000-4000-8000-000000000099"
	authenticator, _ = NewProductionAuthenticator(verifier, drift, func() time.Time { return now })
	if _, err := authenticator.AuthenticateModelRequest(request, testModel); err == nil {
		t.Fatal("inconsistent Core live authorization was accepted")
	}
}

func TestProductionAuthenticatorValidatesConstruction(t *testing.T) {
	claims := productionModelClaims(time.Now().UTC())
	_, verifier := signProductionClaims(t, claims)
	authorizer := &recordingAuthorizer{}
	for name, build := range map[string]func() (*ProductionAuthenticator, error){
		"verifier": func() (*ProductionAuthenticator, error) {
			return NewProductionAuthenticator(nil, authorizer, time.Now)
		},
		"authorizer": func() (*ProductionAuthenticator, error) {
			return NewProductionAuthenticator(verifier, nil, time.Now)
		},
		"clock": func() (*ProductionAuthenticator, error) {
			return NewProductionAuthenticator(verifier, authorizer, nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := build(); err == nil {
				t.Fatal("invalid production llmproxy authenticator was accepted")
			}
		})
	}
}

func productionModelClaims(now time.Time) runcapability.Claims {
	return runcapability.Claims{
		Version: runcapability.ProductionVersion, Issuer: testIssuer,
		CapabilityID:         "97000000-0000-4000-8000-000000000001",
		Audience:             runcapability.AudienceLLMProxy,
		WorkspaceID:          "97000000-0000-4000-8000-000000000002",
		SessionID:            "97000000-0000-4000-8000-000000000003",
		RunID:                "97000000-0000-4000-8000-000000000004",
		RunAttemptID:         "97000000-0000-4000-8000-000000000005",
		RunAttemptGeneration: 3, ActorID: "97000000-0000-4000-8000-000000000006",
		HolderID: "pool/holder", IssuedAtUnixMS: now.Add(-time.Minute).UnixMilli(),
		RunDeadlineUnixMS: now.Add(30 * time.Minute).UnixMilli(),
		ExpiresAtUnixMS:   now.Add(31 * time.Minute).UnixMilli(),
		Model:             testModel, Provider: testProvider,
		LLMGatewayID: "97000000-0000-4000-8000-000000000007", LLMGatewayVersion: 2,
		LLMGatewayGrantUserID: "97000000-0000-4000-8000-000000000006",
	}
}

func signProductionClaims(t *testing.T, claims runcapability.Claims) (string, *runcapability.ProductionVerifier) {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed([]byte(strings.Repeat("m", ed25519.SeedSize)))
	signer, err := runcapability.NewProductionSigner(testIssuer, testKeyID, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := runcapability.NewProductionVerifier(testIssuer, map[string]ed25519.PublicKey{
		testKeyID: privateKey.Public().(ed25519.PublicKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	return token, verifier
}

func productionModelAuthorization(claims runcapability.Claims, now time.Time) RunCapabilityAuthorization {
	return RunCapabilityAuthorization{
		CapabilityID: claims.CapabilityID, Audience: runcapability.AudienceLLMProxy,
		RunID: claims.RunID, RunAttemptID: claims.RunAttemptID,
		RunAttemptGeneration: claims.RunAttemptGeneration,
		RunVersion:           5, RunAttemptVersion: 6, AuthorizedAt: now,
		Model: claims.Model, Provider: claims.Provider,
		LLMGatewayID: claims.LLMGatewayID, LLMGatewayVersion: claims.LLMGatewayVersion,
		LLMGatewayGrantUserID: claims.LLMGatewayGrantUserID,
		ResponsesURL:          "https://gateway.example.com/v1/responses",
		UpstreamAuthorization: "Bearer upstream-secret", BearerExpiresAt: now.Add(20 * time.Minute),
	}
}

type recordingAuthorizer struct {
	calls  []RunCapabilityAuthorizationRequest
	result RunCapabilityAuthorization
	err    error
}

func (authorizer *recordingAuthorizer) AuthorizeLLMProxyRunCapability(
	_ context.Context,
	request RunCapabilityAuthorizationRequest,
) (RunCapabilityAuthorization, error) {
	authorizer.calls = append(authorizer.calls, request)
	return authorizer.result, authorizer.err
}
