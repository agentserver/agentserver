package coreserver

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
)

func TestLLMCapabilityDiagnosticLogExcludesUnderlyingError(t *testing.T) {
	var logs bytes.Buffer
	service := &ProductionRunCapabilityService{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	service.logLLMAuthorizationFailure("gateway_upstream_resolution", errors.New("Bearer secret-must-not-enter-logs"))
	if !strings.Contains(logs.String(), `"stage":"gateway_upstream_resolution"`) ||
		!strings.Contains(logs.String(), `"state_code":"none"`) || strings.Contains(logs.String(), "secret-must-not-enter-logs") {
		t.Fatalf("unsafe or incomplete Core capability diagnostic log = %q", logs.String())
	}
}

const (
	testCapabilityIssuer    = "https://agentserver.example.test/core"
	testCapabilityKeyID     = "run-capability-2026-08"
	testCapabilityExecutor  = "61000000-0000-4000-8000-000000000001"
	testCapabilityWorkspace = "61000000-0000-4000-8000-000000000002"
	testCapabilitySession   = "61000000-0000-4000-8000-000000000003"
	testCapabilityRun       = "61000000-0000-4000-8000-000000000004"
	testCapabilityAttempt   = "61000000-0000-4000-8000-000000000005"
	testCapabilityActor     = "61000000-0000-4000-8000-000000000006"
	testCapabilityCatalog   = "61000000-0000-4000-8000-000000000007"
	testCapabilityGateway   = "61000000-0000-4000-8000-000000000008"
)

func TestProductionRunCapabilityServiceIssuesStableSeparatedCapabilitiesAndAuthorizesLiveState(t *testing.T) {
	createdAt := time.Date(2026, 8, 2, 4, 5, 6, 789_456_000, time.UTC)
	now := createdAt.Add(time.Minute)
	request, authority := productionCapabilityIssuanceFixture(createdAt)
	store := &recordingRunCapabilityStore{
		issuance: authority,
		authorized: coredb.AuthorizedRunCapability{
			RunVersion: authority.RunVersion + 1, AttemptVersion: authority.AttemptVersion + 1,
			RunStatus: coredb.RunStatusRunning, AttemptStatus: coredb.AttemptStatusRunning,
			DatabaseTime: now,
		},
	}
	service, verifier := newProductionRunCapabilityTestService(t, store, func() time.Time { return now })

	first, err := service.IssueRunCapabilities(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.IssueRunCapabilities(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("exact issuance retry changed response:\nfirst  = %+v\nsecond = %+v", first, second)
	}
	if first.ExecutorMCP.CapabilityID == first.LLMProxy.CapabilityID ||
		first.ExecutorMCP.Token == first.LLMProxy.Token {
		t.Fatal("executor and llmproxy capabilities were not audience-separated")
	}
	wantIssuedAt := time.UnixMilli(createdAt.UnixMilli()).UTC()
	wantDeadline := wantIssuedAt.Add(30 * time.Minute)
	wantExpiry := wantDeadline.Add(45 * time.Second)
	for name, issued := range map[string]corecontract.IssuedRunCapability{
		"executor": first.ExecutorMCP, "llmproxy": first.LLMProxy,
	} {
		if issued.IssuedAt != wantIssuedAt || issued.RunDeadline != wantDeadline || issued.ExpiresAt != wantExpiry {
			t.Fatalf("%s capability times = %s / %s / %s", name, issued.IssuedAt, issued.RunDeadline, issued.ExpiresAt)
		}
	}
	executorClaims, err := verifier.Verify(first.ExecutorMCP.Token, runcapability.AudienceExecutorMCP, now)
	if err != nil {
		t.Fatal(err)
	}
	if executorClaims.CapabilityID != first.ExecutorMCP.CapabilityID ||
		executorClaims.ExecutorID != request.ExecutorID || executorClaims.ToolCatalogDigest != request.ToolCatalogDigest ||
		executorClaims.ExpectedRunVersion != request.ExpectedRunVersion+1 ||
		executorClaims.ExpectedRunAttemptVersion != request.ExpectedRunAttemptVersion+1 ||
		executorClaims.MaxApprovalTTLMillis != request.MaxApprovalTTLMillis ||
		executorClaims.Model != "" || executorClaims.Provider != "" {
		t.Fatalf("executor claims = %+v", executorClaims)
	}
	modelClaims, err := verifier.Verify(first.LLMProxy.Token, runcapability.AudienceLLMProxy, now)
	if err != nil {
		t.Fatal(err)
	}
	if modelClaims.CapabilityID != first.LLMProxy.CapabilityID || modelClaims.Model != request.Model ||
		modelClaims.Provider != request.Provider || modelClaims.ExecutorID != "" ||
		modelClaims.LLMGatewayID != request.LLMGatewayID || modelClaims.LLMGatewayVersion != request.LLMGatewayVersion ||
		modelClaims.LLMGatewayGrantUserID != request.LLMGatewayGrantUserID ||
		modelClaims.ToolCatalogDigest != "" || modelClaims.ExpectedRunVersion != 0 ||
		modelClaims.ExpectedRunAttemptVersion != 0 || modelClaims.MaxApprovalTTLMillis != 0 {
		t.Fatalf("llmproxy claims = %+v", modelClaims)
	}
	if len(store.issuanceCalls) != 2 || store.issuanceCalls[0] != store.issuanceCalls[1] {
		t.Fatalf("issuance calls = %+v", store.issuanceCalls)
	}

	executorAuthorization, err := service.AuthorizeExecutorRunCapability(
		t.Context(), first.ExecutorMCP.Token,
		corecontract.AuthorizeExecutorRunCapabilityRequest{
			ExecutorID: request.ExecutorID, ToolCatalogDigest: request.ToolCatalogDigest,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if executorAuthorization.CapabilityID != first.ExecutorMCP.CapabilityID ||
		executorAuthorization.Audience != runcapability.AudienceExecutorMCP ||
		executorAuthorization.RunVersion != authority.RunVersion+1 ||
		executorAuthorization.RunAttemptVersion != authority.AttemptVersion+1 ||
		executorAuthorization.AuthorizedAt != now {
		t.Fatalf("executor authorization = %+v", executorAuthorization)
	}
	executorCommand := store.authorizationCalls[len(store.authorizationCalls)-1]
	if executorCommand.Audience != coredb.RunCapabilityAudienceExecutorMCP ||
		executorCommand.CapabilityID != first.ExecutorMCP.CapabilityID ||
		executorCommand.ExpectedRunVersion != authority.RunVersion+1 ||
		executorCommand.ExpectedAttemptVersion != authority.AttemptVersion+1 ||
		executorCommand.ExecutorID != request.ExecutorID || executorCommand.ToolCatalogDigest != authority.ToolCatalogDigest {
		t.Fatalf("executor authorization command = %+v", executorCommand)
	}

	modelAuthorization, err := service.AuthorizeLLMProxyRunCapability(
		t.Context(), first.LLMProxy.Token,
		corecontract.AuthorizeLLMProxyRunCapabilityRequest{
			Model: request.Model, Provider: request.Provider, LLMGatewayID: request.LLMGatewayID,
			LLMGatewayVersion: request.LLMGatewayVersion, LLMGatewayGrantUserID: request.LLMGatewayGrantUserID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if modelAuthorization.CapabilityID != first.LLMProxy.CapabilityID ||
		modelAuthorization.Audience != runcapability.AudienceLLMProxy ||
		modelAuthorization.ResponsesURL != "https://gateway.example.com/v1/responses" ||
		modelAuthorization.UpstreamAuthorization != "Bearer workspace-token" {
		t.Fatalf("llmproxy authorization = %+v", modelAuthorization)
	}
	modelCommand := store.authorizationCalls[len(store.authorizationCalls)-1]
	if modelCommand.Audience != coredb.RunCapabilityAudienceLLMProxy || modelCommand.ExecutorID != "" ||
		modelCommand.ToolCatalogDigest != ([sha256.Size]byte{}) || modelCommand.ExpectedRunVersion != 0 ||
		modelCommand.ExpectedAttemptVersion != 0 || modelCommand.LLMGateway != authority.LLMGateway {
		t.Fatalf("llmproxy authorization command = %+v", modelCommand)
	}
}

func TestProductionRunCapabilityServiceRejectsInvalidPolicyProjectionAndAudience(t *testing.T) {
	createdAt := time.Date(2026, 8, 2, 5, 0, 0, 0, time.UTC)
	now := createdAt.Add(time.Minute)
	request, authority := productionCapabilityIssuanceFixture(createdAt)
	store := &recordingRunCapabilityStore{issuance: authority, authorized: coredb.AuthorizedRunCapability{DatabaseTime: now}}
	service, _ := newProductionRunCapabilityTestService(t, store, func() time.Time { return now })

	invalid := request
	invalid.WorkspaceID = ""
	invalid.Provider = "different"
	if _, err := service.IssueRunCapabilities(t.Context(), invalid); !coredb.HasStateErrorCode(err, coredb.ErrorInvalidArgument) {
		t.Fatalf("structurally invalid issuance error = %v, want invalid_argument", err)
	}
	invalid = request
	invalid.MaxRunDurationMillis++
	if _, err := service.IssueRunCapabilities(t.Context(), invalid); !coredb.HasStateErrorCode(err, coredb.ErrorForbidden) {
		t.Fatalf("policy mismatch issuance error = %v, want forbidden", err)
	}
	invalid = request
	invalid.ToolCatalogDigest = "ABC"
	if _, err := service.IssueRunCapabilities(t.Context(), invalid); !coredb.HasStateErrorCode(err, coredb.ErrorInvalidArgument) {
		t.Fatalf("malformed digest issuance error = %v, want invalid_argument", err)
	}
	if len(store.issuanceCalls) != 0 {
		t.Fatalf("invalid issuance reached store: %+v", store.issuanceCalls)
	}

	drifted := authority
	drifted.RunID = "61000000-0000-4000-8000-000000000099"
	store.issuance = drifted
	if _, err := service.IssueRunCapabilities(t.Context(), request); err == nil || coredb.HasStateErrorCode(err, coredb.ErrorForbidden) {
		t.Fatalf("drifted issuance projection error = %v", err)
	}
	store.issuance = authority
	issued, err := service.IssueRunCapabilities(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	before := len(store.authorizationCalls)
	if _, err := service.AuthorizeExecutorRunCapability(
		t.Context(), issued.LLMProxy.Token,
		corecontract.AuthorizeExecutorRunCapabilityRequest{ExecutorID: request.ExecutorID, ToolCatalogDigest: request.ToolCatalogDigest},
	); !coredb.HasStateErrorCode(err, coredb.ErrorForbidden) {
		t.Fatalf("cross-audience executor authorization error = %v", err)
	}
	if _, err := service.AuthorizeLLMProxyRunCapability(
		t.Context(), issued.LLMProxy.Token,
		corecontract.AuthorizeLLMProxyRunCapabilityRequest{Model: "other", Provider: request.Provider},
	); !coredb.HasStateErrorCode(err, coredb.ErrorForbidden) {
		t.Fatalf("wrong-route model authorization error = %v", err)
	}
	if len(store.authorizationCalls) != before {
		t.Fatalf("locally denied token reached store: %+v", store.authorizationCalls[before:])
	}
}

func TestProductionRunCapabilityServiceEnforcesLocalAndDatabaseDeadline(t *testing.T) {
	createdAt := time.Date(2026, 8, 2, 6, 0, 0, 0, time.UTC)
	request, authority := productionCapabilityIssuanceFixture(createdAt)
	now := createdAt.Add(time.Minute)
	store := &recordingRunCapabilityStore{
		issuance: authority,
		authorized: coredb.AuthorizedRunCapability{
			RunVersion: authority.RunVersion, AttemptVersion: authority.AttemptVersion,
			RunStatus: coredb.RunStatusStarting, AttemptStatus: coredb.AttemptStatusLeased,
			DatabaseTime: now,
		},
	}
	service, _ := newProductionRunCapabilityTestService(t, store, func() time.Time { return now })
	issued, err := service.IssueRunCapabilities(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}

	now = issued.ExecutorMCP.RunDeadline
	before := len(store.authorizationCalls)
	if _, err := service.AuthorizeExecutorRunCapability(
		t.Context(), issued.ExecutorMCP.Token,
		corecontract.AuthorizeExecutorRunCapabilityRequest{ExecutorID: request.ExecutorID, ToolCatalogDigest: request.ToolCatalogDigest},
	); !coredb.HasStateErrorCode(err, coredb.ErrorForbidden) {
		t.Fatalf("local deadline authorization error = %v", err)
	}
	if len(store.authorizationCalls) != before {
		t.Fatal("local deadline denial reached the state store")
	}

	now = createdAt.Add(time.Minute)
	store.authorized.DatabaseTime = issued.ExecutorMCP.RunDeadline
	if _, err := service.AuthorizeExecutorRunCapability(
		t.Context(), issued.ExecutorMCP.Token,
		corecontract.AuthorizeExecutorRunCapabilityRequest{ExecutorID: request.ExecutorID, ToolCatalogDigest: request.ToolCatalogDigest},
	); !coredb.HasStateErrorCode(err, coredb.ErrorForbidden) {
		t.Fatalf("database deadline authorization error = %v", err)
	}

	store.authorized.DatabaseTime = now
	store.issuance.DatabaseTime = issued.ExecutorMCP.RunDeadline
	if _, err := service.IssueRunCapabilities(t.Context(), request); !coredb.HasStateErrorCode(err, coredb.ErrorForbidden) {
		t.Fatalf("database deadline issuance error = %v", err)
	}
	store.issuance.DatabaseTime = now
	now = issued.ExecutorMCP.RunDeadline
	if _, err := service.IssueRunCapabilities(t.Context(), request); !coredb.HasStateErrorCode(err, coredb.ErrorForbidden) {
		t.Fatalf("local deadline issuance error = %v", err)
	}
}

func TestNewProductionRunCapabilityServiceRequiresMatchingActiveKeyAndPolicy(t *testing.T) {
	privateKey := ed25519.NewKeyFromSeed(bytesOf(0x31, ed25519.SeedSize))
	signer, err := runcapability.NewProductionSigner(testCapabilityIssuer, testCapabilityKeyID, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := runcapability.NewProductionVerifier(testCapabilityIssuer, map[string]ed25519.PublicKey{
		testCapabilityKeyID: privateKey.Public().(ed25519.PublicKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	valid := ProductionRunCapabilityServiceConfig{
		Store: &recordingRunCapabilityStore{}, Signer: signer, Verifier: verifier,
		Policy: productionCapabilityTestPolicy(), LLMGatewayResolver: recordingLLMGatewayResolver{}, Now: time.Now,
	}
	if _, err := NewProductionRunCapabilityService(valid); err != nil {
		t.Fatal(err)
	}

	missingStore := valid
	missingStore.Store = nil
	if _, err := NewProductionRunCapabilityService(missingStore); err == nil {
		t.Fatal("nil state store was accepted")
	}
	otherPrivateKey := ed25519.NewKeyFromSeed(bytesOf(0x32, ed25519.SeedSize))
	missingActiveVerifier, err := runcapability.NewProductionVerifier(testCapabilityIssuer, map[string]ed25519.PublicKey{
		"other-key": otherPrivateKey.Public().(ed25519.PublicKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	missingActive := valid
	missingActive.Verifier = missingActiveVerifier
	if _, err := NewProductionRunCapabilityService(missingActive); err == nil {
		t.Fatal("keyring without active signing key was accepted")
	}
	wrongIssuerVerifier, err := runcapability.NewProductionVerifier("https://other.example.test/core", map[string]ed25519.PublicKey{
		testCapabilityKeyID: privateKey.Public().(ed25519.PublicKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	wrongIssuer := valid
	wrongIssuer.Verifier = wrongIssuerVerifier
	if _, err := NewProductionRunCapabilityService(wrongIssuer); err == nil {
		t.Fatal("mismatched verifier issuer was accepted")
	}
	invalidPolicy := valid
	invalidPolicy.Policy.MaxApprovalTTL = invalidPolicy.Policy.MaxRunDuration + time.Second
	if _, err := NewProductionRunCapabilityService(invalidPolicy); err == nil {
		t.Fatal("invalid production policy was accepted")
	}
}

type recordingRunCapabilityStore struct {
	issuance           coredb.RunCapabilityIssuanceAuthority
	issuanceErr        error
	authorized         coredb.AuthorizedRunCapability
	authorizationErr   error
	issuanceCalls      []coredb.ResolveRunCapabilityIssuanceCommand
	authorizationCalls []coredb.AuthorizeRunCapabilityCommand
}

func (store *recordingRunCapabilityStore) ResolveRunCapabilityIssuance(
	_ context.Context,
	command coredb.ResolveRunCapabilityIssuanceCommand,
) (coredb.RunCapabilityIssuanceAuthority, error) {
	store.issuanceCalls = append(store.issuanceCalls, command)
	return store.issuance, store.issuanceErr
}

func (store *recordingRunCapabilityStore) AuthorizeRunCapability(
	_ context.Context,
	command coredb.AuthorizeRunCapabilityCommand,
) (coredb.AuthorizedRunCapability, error) {
	store.authorizationCalls = append(store.authorizationCalls, command)
	result := store.authorized
	if command.Audience == coredb.RunCapabilityAudienceLLMProxy && result.LLMGateway == nil {
		result.LLMGateway = &coredb.LLMGatewayLiveAuthority{
			Gateway: coredb.WorkspaceLLMGateway{
				ID: command.LLMGateway.GatewayID, WorkspaceID: command.WorkspaceID,
				Version: command.LLMGateway.ConfigVersion, DefaultModel: command.LLMGateway.Model,
				Status: coredb.LLMGatewayStatusActive, ResponsesURL: "https://gateway.example.com/v1/responses",
			},
			Grant: coredb.WorkspaceLLMGatewayGrant{
				GatewayID: command.LLMGateway.GatewayID, WorkspaceID: command.WorkspaceID,
				UserID: command.LLMGateway.GrantUserID, Status: coredb.LLMGatewayGrantStatusActive,
			},
			Model: command.LLMGateway.Model,
		}
	}
	return result, store.authorizationErr
}

func productionCapabilityIssuanceFixture(createdAt time.Time) (
	corecontract.IssueRunCapabilitiesRequest,
	coredb.RunCapabilityIssuanceAuthority,
) {
	digest := sha256.Sum256([]byte("production tool catalog"))
	request := corecontract.IssueRunCapabilitiesRequest{
		WorkspaceID: testCapabilityWorkspace, SessionID: testCapabilitySession,
		RunID: testCapabilityRun, RunAttemptID: testCapabilityAttempt,
		HolderID: "pool-instance/attempt-holder", RunAttemptGeneration: 3,
		ExpectedRunVersion: 4, ExpectedRunAttemptVersion: 5,
		ExecutorID: testCapabilityExecutor, BrainToolCatalogID: testCapabilityCatalog,
		ToolCatalogDigest: fmtSHA256(digest), Model: "gpt-5.6-codex",
		Provider:     corecontract.WorkspaceLLMGatewayProvider,
		LLMGatewayID: testCapabilityGateway, LLMGatewayVersion: 2,
		LLMGatewayGrantUserID: testCapabilityActor,
		MaxRunDurationMillis:  int64(30 * time.Minute / time.Millisecond),
		MaxApprovalTTLMillis:  int64(10 * time.Second / time.Millisecond),
	}
	authority := coredb.RunCapabilityIssuanceAuthority{
		WorkspaceID: request.WorkspaceID, SessionID: request.SessionID, RunID: request.RunID,
		AttemptID: request.RunAttemptID, ActorID: testCapabilityActor, HolderID: request.HolderID,
		Generation: request.RunAttemptGeneration, RunVersion: request.ExpectedRunVersion,
		AttemptVersion: request.ExpectedRunAttemptVersion, AttemptCreatedAt: createdAt,
		DatabaseTime: createdAt.Add(time.Minute), ExecutorID: request.ExecutorID,
		BrainToolCatalogID: request.BrainToolCatalogID, ToolCatalogDigest: digest,
		LLMGateway: coredb.RunLLMGatewayBinding{
			GatewayID: request.LLMGatewayID, ConfigVersion: request.LLMGatewayVersion,
			GrantUserID: request.LLMGatewayGrantUserID, Model: request.Model,
		},
	}
	return request, authority
}

func newProductionRunCapabilityTestService(
	t *testing.T,
	store RunCapabilityStateStore,
	now func() time.Time,
) (*ProductionRunCapabilityService, *runcapability.ProductionVerifier) {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed(bytesOf(0x41, ed25519.SeedSize))
	signer, err := runcapability.NewProductionSigner(testCapabilityIssuer, testCapabilityKeyID, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := runcapability.NewProductionVerifier(testCapabilityIssuer, map[string]ed25519.PublicKey{
		testCapabilityKeyID: privateKey.Public().(ed25519.PublicKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewProductionRunCapabilityService(ProductionRunCapabilityServiceConfig{
		Store: store, Signer: signer, Verifier: verifier, Policy: productionCapabilityTestPolicy(),
		LLMGatewayResolver: recordingLLMGatewayResolver{}, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, verifier
}

func productionCapabilityTestPolicy() ProductionRunCapabilityPolicy {
	return ProductionRunCapabilityPolicy{
		ExecutorID:     testCapabilityExecutor,
		MaxRunDuration: 30 * time.Minute, MaxApprovalTTL: 10 * time.Second, ExpiryGrace: 45 * time.Second,
	}
}

type recordingLLMGatewayResolver struct{}

func (recordingLLMGatewayResolver) ResolveUpstream(
	_ context.Context,
	authority coredb.LLMGatewayLiveAuthority,
) (LLMGatewayUpstreamAuthorization, error) {
	return LLMGatewayUpstreamAuthorization{
		GatewayID: authority.Gateway.ID, GatewayConfigVersion: authority.Gateway.Version,
		GrantUserID: authority.Grant.UserID, Model: authority.Model,
		ResponsesURL:  "https://gateway.example.com/v1/responses",
		Authorization: "Bearer workspace-token", BearerExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	}, nil
}

func bytesOf(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}

func fmtSHA256(value [sha256.Size]byte) string {
	const digits = "0123456789abcdef"
	result := make([]byte, sha256.Size*2)
	for index, octet := range value {
		result[index*2] = digits[octet>>4]
		result[index*2+1] = digits[octet&0x0f]
	}
	return string(result)
}
