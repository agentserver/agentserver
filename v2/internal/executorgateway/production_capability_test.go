package executorgateway

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/runcapability"
)

const (
	testProductionCapabilityIssuer   = "https://agentserver.example.test/core"
	testProductionCapabilityKeyID    = "production-executor-test-key"
	testProductionCapabilityExecutor = "96000000-0000-4000-8000-000000000001"
)

func TestProductionExecutorMCPAuthenticatorVerifiesAndLiveAuthorizesEveryRequest(t *testing.T) {
	now := time.Date(2026, 8, 2, 11, 0, 0, 0, time.UTC)
	claims := productionExecutorClaims(now, testProductionCapabilityExecutor)
	token, verifier := signProductionExecutorClaims(t, claims)
	authorizer := &recordingExecutorRunCapabilityAuthorizer{result: productionExecutorAuthorization(claims, now, true)}
	authenticator, err := NewProductionExecutorMCPAuthenticator(
		verifier, authorizer, testProductionCapabilityExecutor, func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "https://executor-gateway.test/mcp", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	for range 2 {
		principal, err := authenticator.AuthenticateExecutorMCP(request)
		if err != nil {
			t.Fatal(err)
		}
		if !principal.Production || principal.CapabilityID != claims.CapabilityID ||
			principal.WorkspaceID != claims.WorkspaceID || principal.ActorID != claims.ActorID ||
			principal.ExecutorID != claims.ExecutorID || principal.ToolCatalogDigest != claims.ToolCatalogDigest ||
			principal.MaxApprovalTTL != time.Duration(claims.MaxApprovalTTLMillis)*time.Millisecond ||
			!principal.RunDeadline.Equal(time.UnixMilli(claims.RunDeadlineUnixMS)) ||
			!principal.CapabilityExpiresAt.Equal(time.UnixMilli(claims.ExpiresAtUnixMS)) ||
			principal.Run.RunID != claims.RunID || principal.Run.RunAttemptID != claims.RunAttemptID ||
			principal.Run.RunAttemptGeneration != claims.RunAttemptGeneration ||
			principal.Run.HolderID != claims.HolderID || principal.Run.ExpectedRunVersion != claims.ExpectedRunVersion ||
			principal.Run.ExpectedRunAttemptVersion != claims.ExpectedRunAttemptVersion {
			t.Fatalf("production executor principal = %+v", principal)
		}
	}
	if len(authorizer.calls) != 2 {
		t.Fatalf("live authorization calls = %d", len(authorizer.calls))
	}
	for _, call := range authorizer.calls {
		if call.Token != token || call.CapabilityID != claims.CapabilityID ||
			call.ExecutorID != claims.ExecutorID || call.ToolCatalogDigest != claims.ToolCatalogDigest ||
			call.RunID != claims.RunID || call.RunAttemptID != claims.RunAttemptID ||
			call.RunAttemptGeneration != claims.RunAttemptGeneration ||
			call.ExpectedRunVersion != claims.ExpectedRunVersion ||
			call.ExpectedRunAttemptVersion != claims.ExpectedRunAttemptVersion {
			t.Fatalf("live authorization request = %+v", call)
		}
	}
	workspaceClaims := claims
	workspaceClaims.WorkspaceEnvironmentID = testEnvironmentID
	workspaceClaims.WorkspaceEnvironmentVersion = 2
	workspaceClaims.WorkspaceRootSHA256 = strings.Repeat("b", 64)
	workspaceClaims.WorkspaceWorkingDirectory = "rtm-aihub"
	workspaceClaims.WorkspaceWorkingDirectoryVersion = 3
	workspaceToken, workspaceVerifier := signProductionExecutorClaims(t, workspaceClaims)
	workspaceAuthorizer := &recordingExecutorRunCapabilityAuthorizer{result: productionExecutorAuthorization(workspaceClaims, now, true)}
	workspaceAuthenticator, err := NewProductionExecutorMCPAuthenticator(
		workspaceVerifier, workspaceAuthorizer, testProductionCapabilityExecutor, func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}
	workspaceRequest := httptest.NewRequest("POST", "https://executor-gateway.test/mcp", nil)
	workspaceRequest.Header.Set("Authorization", "Bearer "+workspaceToken)
	principal, err := workspaceAuthenticator.AuthenticateExecutorMCP(workspaceRequest)
	if err != nil || principal.Workspace == nil || principal.Workspace.EnvironmentID != testEnvironmentID ||
		principal.Workspace.WorkingDirectory != "rtm-aihub" || principal.Workspace.WorkingDirectoryVersion != 3 {
		t.Fatalf("production workspace principal = %+v, %v", principal.Workspace, err)
	}
}

func TestProductionExecutorMCPAuthenticatorFailsClosedBeforeAndAfterCore(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	validClaims := productionExecutorClaims(now, testProductionCapabilityExecutor)
	validToken, verifier := signProductionExecutorClaims(t, validClaims)

	for name, claims := range map[string]runcapability.Claims{
		"wrong executor": productionExecutorClaims(now, "96000000-0000-4000-8000-000000000099"),
		"deadline grace": func() runcapability.Claims {
			claims := productionExecutorClaims(now, testProductionCapabilityExecutor)
			claims.RunDeadlineUnixMS = now.UnixMilli()
			return claims
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			token, localVerifier := signProductionExecutorClaims(t, claims)
			authorizer := &recordingExecutorRunCapabilityAuthorizer{}
			authenticator, err := NewProductionExecutorMCPAuthenticator(
				localVerifier, authorizer, testProductionCapabilityExecutor, func() time.Time { return now },
			)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("POST", "https://executor-gateway.test/mcp", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			if _, err := authenticator.AuthenticateExecutorMCP(request); err == nil || len(authorizer.calls) != 0 {
				t.Fatalf("local denial error/calls = %v / %d", err, len(authorizer.calls))
			}
		})
	}

	modelClaims := validClaims
	modelClaims.Audience = runcapability.AudienceLLMProxy
	modelClaims.ExecutorID = ""
	modelClaims.ToolCatalogDigest = ""
	modelClaims.ExpectedRunVersion = 0
	modelClaims.ExpectedRunAttemptVersion = 0
	modelClaims.MaxApprovalTTLMillis = 0
	modelClaims.Model = "gpt-5.6-codex"
	modelClaims.Provider = "openai"
	modelToken, _ := signProductionExecutorClaims(t, modelClaims)
	for name, authorization := range map[string][]string{
		"wrong audience": {"Bearer " + modelToken},
		"duplicate":      {"Bearer " + validToken, "Bearer " + validToken},
		"padded":         {"Bearer  " + validToken},
	} {
		t.Run(name, func(t *testing.T) {
			authorizer := &recordingExecutorRunCapabilityAuthorizer{}
			authenticator, _ := NewProductionExecutorMCPAuthenticator(
				verifier, authorizer, testProductionCapabilityExecutor, func() time.Time { return now },
			)
			request := httptest.NewRequest("POST", "https://executor-gateway.test/mcp", nil)
			request.Header["Authorization"] = authorization
			if _, err := authenticator.AuthenticateExecutorMCP(request); err == nil || len(authorizer.calls) != 0 {
				t.Fatalf("framing/audience denial error/calls = %v / %d", err, len(authorizer.calls))
			}
		})
	}

	coreFailure := &recordingExecutorRunCapabilityAuthorizer{err: errors.New("Core unavailable")}
	authenticator, _ := NewProductionExecutorMCPAuthenticator(
		verifier, coreFailure, testProductionCapabilityExecutor, func() time.Time { return now },
	)
	request := httptest.NewRequest("POST", "https://executor-gateway.test/mcp", nil)
	request.Header.Set("Authorization", "Bearer "+validToken)
	if _, err := authenticator.AuthenticateExecutorMCP(request); err == nil || strings.Contains(err.Error(), validToken) {
		t.Fatalf("Core failure error = %v", err)
	}

	drift := &recordingExecutorRunCapabilityAuthorizer{result: productionExecutorAuthorization(validClaims, now, true)}
	drift.result.RunID = "96000000-0000-4000-8000-000000000099"
	authenticator, _ = NewProductionExecutorMCPAuthenticator(
		verifier, drift, testProductionCapabilityExecutor, func() time.Time { return now },
	)
	if _, err := authenticator.AuthenticateExecutorMCP(request); err == nil {
		t.Fatal("inconsistent Core live authorization was accepted")
	}
}

func TestProductionExecutorMCPAuthenticatorValidatesConstruction(t *testing.T) {
	claims := productionExecutorClaims(time.Now().UTC(), testProductionCapabilityExecutor)
	_, verifier := signProductionExecutorClaims(t, claims)
	authorizer := &recordingExecutorRunCapabilityAuthorizer{}
	if _, err := NewProductionExecutorMCPAuthenticator(nil, authorizer, testProductionCapabilityExecutor, time.Now); err == nil {
		t.Fatal("nil production verifier was accepted")
	}
	if _, err := NewProductionExecutorMCPAuthenticator(verifier, nil, testProductionCapabilityExecutor, time.Now); err == nil {
		t.Fatal("nil production live authorizer was accepted")
	}
	if _, err := NewProductionExecutorMCPAuthenticator(verifier, authorizer, "not-a-uuid", time.Now); err == nil {
		t.Fatal("invalid production executor was accepted")
	}
	if _, err := NewProductionExecutorMCPAuthenticator(verifier, authorizer, testProductionCapabilityExecutor, nil); err == nil {
		t.Fatal("nil production clock was accepted")
	}
}

func productionExecutorClaims(now time.Time, executorID string) runcapability.Claims {
	return runcapability.Claims{
		Version: runcapability.ProductionVersion, Issuer: testProductionCapabilityIssuer,
		CapabilityID:         "97000000-0000-4000-8000-000000000001",
		Audience:             runcapability.AudienceExecutorMCP,
		WorkspaceID:          "97000000-0000-4000-8000-000000000002",
		SessionID:            "97000000-0000-4000-8000-000000000003",
		RunID:                "97000000-0000-4000-8000-000000000004",
		RunAttemptID:         "97000000-0000-4000-8000-000000000005",
		RunAttemptGeneration: 3, ActorID: "97000000-0000-4000-8000-000000000006",
		HolderID: "pool/holder", IssuedAtUnixMS: now.Add(-time.Minute).UnixMilli(),
		RunDeadlineUnixMS: now.Add(30 * time.Minute).UnixMilli(),
		ExpiresAtUnixMS:   now.Add(31 * time.Minute).UnixMilli(),
		ExecutorID:        executorID, ToolCatalogDigest: strings.Repeat("a", 64),
		ExpectedRunVersion: 5, ExpectedRunAttemptVersion: 6, MaxApprovalTTLMillis: 10_000,
	}
}

func signProductionExecutorClaims(t *testing.T, claims runcapability.Claims) (string, *runcapability.ProductionVerifier) {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed([]byte(strings.Repeat("q", ed25519.SeedSize)))
	signer, err := runcapability.NewProductionSigner(testProductionCapabilityIssuer, testProductionCapabilityKeyID, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := runcapability.NewProductionVerifier(testProductionCapabilityIssuer, map[string]ed25519.PublicKey{
		testProductionCapabilityKeyID: privateKey.Public().(ed25519.PublicKey),
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

func productionExecutorAuthorization(claims runcapability.Claims, now time.Time, accepted bool) ExecutorRunCapabilityAuthorization {
	runVersion := claims.ExpectedRunVersion - 1
	attemptVersion := claims.ExpectedRunAttemptVersion - 1
	if accepted {
		runVersion = claims.ExpectedRunVersion
		attemptVersion = claims.ExpectedRunAttemptVersion
	}
	return ExecutorRunCapabilityAuthorization{
		CapabilityID: claims.CapabilityID, Audience: runcapability.AudienceExecutorMCP,
		RunID: claims.RunID, RunAttemptID: claims.RunAttemptID,
		RunAttemptGeneration: claims.RunAttemptGeneration,
		RunVersion:           runVersion, RunAttemptVersion: attemptVersion, AuthorizedAt: now,
	}
}

type recordingExecutorRunCapabilityAuthorizer struct {
	calls  []ExecutorRunCapabilityAuthorizationRequest
	result ExecutorRunCapabilityAuthorization
	err    error
}

func (authorizer *recordingExecutorRunCapabilityAuthorizer) AuthorizeExecutorRunCapability(
	_ context.Context,
	request ExecutorRunCapabilityAuthorizationRequest,
) (ExecutorRunCapabilityAuthorization, error) {
	authorizer.calls = append(authorizer.calls, request)
	return authorizer.result, authorizer.err
}
