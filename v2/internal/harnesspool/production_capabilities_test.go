package harnesspool

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestProductionAttemptRuntimeCapabilitySourceRequestsExactCoreAuthority(t *testing.T) {
	prepared := developmentCapabilityPreparedLaunch(t)
	createdAt := time.Date(2026, 8, 2, 9, 10, 11, 987_654_000, time.UTC)
	prepared.Scheduled.Claim.RunAttempt.CreatedAt = createdAt
	wantIssuedAt := time.UnixMilli(createdAt.UnixMilli()).UTC()
	wantDeadline := wantIssuedAt.Add(time.Duration(prepared.Manifest.Limits.MaxRunDurationMS) * time.Millisecond)
	issuer := &recordingProductionCapabilityIssuer{results: []IssueRunCapabilitiesResult{{
		ExecutorMCP: IssuedRunCapability{
			CapabilityID: "91000000-0000-4000-8000-000000000001", Audience: "executor-mcp",
			Token: "asv2cap1.executor.claims.signature", IssuedAt: wantIssuedAt,
			RunDeadline: wantDeadline, ExpiresAt: wantDeadline.Add(30 * time.Second),
		},
		LLMProxy: IssuedRunCapability{
			CapabilityID: "92000000-0000-4000-8000-000000000002", Audience: "llmproxy",
			Token: "asv2cap1.model.claims.signature", IssuedAt: wantIssuedAt,
			RunDeadline: wantDeadline, ExpiresAt: wantDeadline.Add(30 * time.Second),
		},
	}}}
	executorID := "93000000-0000-4000-8000-000000000003"
	source, err := NewProductionAttemptRuntimeCapabilitySource(issuer, executorID)
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := source.IssueAttemptRuntimeCapabilities(t.Context(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.ExecutorMCP != issuer.results[0].ExecutorMCP.Token ||
		capabilities.LLMProxy != issuer.results[0].LLMProxy.Token {
		t.Fatalf("production capabilities = %+v", capabilities)
	}
	if len(issuer.calls) != 1 {
		t.Fatalf("Core issuance calls = %d", len(issuer.calls))
	}
	request := issuer.calls[0]
	claim := prepared.Scheduled.Claim
	if request.WorkspaceID != prepared.Manifest.WorkspaceID || request.SessionID != prepared.Manifest.SessionID ||
		request.RunID != prepared.Manifest.RunID || request.RunAttemptID != prepared.Manifest.RunAttemptID ||
		request.HolderID != prepared.Manifest.HolderID || request.RunAttemptGeneration != prepared.Manifest.RunAttemptGeneration ||
		request.ExpectedRunVersion != claim.Run.Version || request.ExpectedRunAttemptVersion != claim.RunAttempt.Version ||
		request.ExecutorID != executorID || request.BrainToolCatalogID != prepared.Manifest.ExecutorMCP.CatalogID ||
		request.ToolCatalogDigest != prepared.Manifest.ExecutorMCP.CatalogDigest ||
		request.Model != prepared.Manifest.Model.Model || request.Provider != prepared.Manifest.Model.Provider ||
		request.MaxRunDuration != time.Duration(prepared.Manifest.Limits.MaxRunDurationMS)*time.Millisecond ||
		request.MaxApprovalTTL != time.Duration(prepared.Manifest.Limits.MaxApprovalTTLMS)*time.Millisecond {
		t.Fatalf("Core issuance request = %+v", request)
	}
}

func TestProductionAttemptRuntimeCapabilitySourceRetriesOnlyAmbiguousIssuance(t *testing.T) {
	prepared := developmentCapabilityPreparedLaunch(t)
	prepared.Scheduled.Claim.RunAttempt.CreatedAt = time.UnixMilli(1_800_000_000_000).UTC()
	issuedAt := prepared.Scheduled.Claim.RunAttempt.CreatedAt
	deadline := issuedAt.Add(time.Duration(prepared.Manifest.Limits.MaxRunDurationMS) * time.Millisecond)
	result := validProductionCapabilityResult(issuedAt, deadline)
	issuer := &recordingProductionCapabilityIssuer{
		errors:  []error{errors.New("ambiguous transport failure"), nil},
		results: []IssueRunCapabilitiesResult{{}, result},
	}
	source, err := NewProductionAttemptRuntimeCapabilitySource(issuer, "93000000-0000-4000-8000-000000000003")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.IssueAttemptRuntimeCapabilities(t.Context(), prepared); err != nil {
		t.Fatal(err)
	}
	if len(issuer.calls) != 2 || issuer.calls[0] != issuer.calls[1] {
		t.Fatalf("exact retry calls = %+v", issuer.calls)
	}

	issuer = &recordingProductionCapabilityIssuer{errors: []error{&CoreCommandError{HTTPStatus: 403, Code: "forbidden"}}}
	source, _ = NewProductionAttemptRuntimeCapabilitySource(issuer, "93000000-0000-4000-8000-000000000003")
	if _, err := source.IssueAttemptRuntimeCapabilities(t.Context(), prepared); err == nil || len(issuer.calls) != 1 {
		t.Fatalf("authoritative denial error/calls = %v / %d", err, len(issuer.calls))
	}
}

func TestProductionAttemptRuntimeCapabilitySourceRejectsDriftedResponseWithoutLeakingTokens(t *testing.T) {
	prepared := developmentCapabilityPreparedLaunch(t)
	prepared.Scheduled.Claim.RunAttempt.CreatedAt = time.UnixMilli(1_800_000_000_000).UTC()
	issuedAt := prepared.Scheduled.Claim.RunAttempt.CreatedAt
	deadline := issuedAt.Add(time.Duration(prepared.Manifest.Limits.MaxRunDurationMS) * time.Millisecond)
	base := validProductionCapabilityResult(issuedAt, deadline)
	for name, mutate := range map[string]func(*IssueRunCapabilitiesResult){
		"audience": func(result *IssueRunCapabilitiesResult) { result.ExecutorMCP.Audience = "llmproxy" },
		"identity": func(result *IssueRunCapabilitiesResult) {
			result.LLMProxy.CapabilityID = result.ExecutorMCP.CapabilityID
		},
		"token": func(result *IssueRunCapabilitiesResult) { result.LLMProxy.Token = result.ExecutorMCP.Token },
		"deadline": func(result *IssueRunCapabilitiesResult) {
			result.LLMProxy.RunDeadline = result.LLMProxy.RunDeadline.Add(time.Millisecond)
		},
		"grace": func(result *IssueRunCapabilitiesResult) {
			result.LLMProxy.ExpiresAt = deadline.Add(11 * time.Minute)
			result.ExecutorMCP.ExpiresAt = result.LLMProxy.ExpiresAt
		},
	} {
		t.Run(name, func(t *testing.T) {
			result := base
			mutate(&result)
			issuer := &recordingProductionCapabilityIssuer{results: []IssueRunCapabilitiesResult{result}}
			source, err := NewProductionAttemptRuntimeCapabilitySource(issuer, "93000000-0000-4000-8000-000000000003")
			if err != nil {
				t.Fatal(err)
			}
			_, err = source.IssueAttemptRuntimeCapabilities(t.Context(), prepared)
			if err == nil {
				t.Fatal("drifted production capability response was accepted")
			}
			if strings.Contains(err.Error(), base.ExecutorMCP.Token) || strings.Contains(err.Error(), base.LLMProxy.Token) {
				t.Fatal("production capability token leaked through validation error")
			}
		})
	}

	missingTime := prepared
	missingTime.Scheduled.Claim.RunAttempt.CreatedAt = time.Time{}
	issuer := &recordingProductionCapabilityIssuer{results: []IssueRunCapabilitiesResult{base}}
	source, _ := NewProductionAttemptRuntimeCapabilitySource(issuer, "93000000-0000-4000-8000-000000000003")
	if _, err := source.IssueAttemptRuntimeCapabilities(t.Context(), missingTime); err == nil || len(issuer.calls) != 0 {
		t.Fatalf("missing attempt creation time error/calls = %v / %d", err, len(issuer.calls))
	}
}

func TestProductionAttemptRuntimeCapabilitySourceValidatesConstructionAndContext(t *testing.T) {
	if _, err := NewProductionAttemptRuntimeCapabilitySource(nil, "93000000-0000-4000-8000-000000000003"); err == nil {
		t.Fatal("nil production issuer was accepted")
	}
	issuer := &recordingProductionCapabilityIssuer{}
	if _, err := NewProductionAttemptRuntimeCapabilitySource(issuer, "not-a-uuid"); err == nil {
		t.Fatal("invalid production executor was accepted")
	}
	source, _ := NewProductionAttemptRuntimeCapabilitySource(issuer, "93000000-0000-4000-8000-000000000003")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.IssueAttemptRuntimeCapabilities(ctx, PreparedRunLaunch{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled production issuance = %v", err)
	}
}

func validProductionCapabilityResult(issuedAt, deadline time.Time) IssueRunCapabilitiesResult {
	expiresAt := deadline.Add(30 * time.Second)
	return IssueRunCapabilitiesResult{
		ExecutorMCP: IssuedRunCapability{
			CapabilityID: "91000000-0000-4000-8000-000000000001", Audience: "executor-mcp",
			Token: "asv2cap1.executor.claims.signature", IssuedAt: issuedAt,
			RunDeadline: deadline, ExpiresAt: expiresAt,
		},
		LLMProxy: IssuedRunCapability{
			CapabilityID: "92000000-0000-4000-8000-000000000002", Audience: "llmproxy",
			Token: "asv2cap1.model.claims.signature", IssuedAt: issuedAt,
			RunDeadline: deadline, ExpiresAt: expiresAt,
		},
	}
}

type recordingProductionCapabilityIssuer struct {
	calls   []IssueRunCapabilitiesRequest
	results []IssueRunCapabilitiesResult
	errors  []error
}

func (issuer *recordingProductionCapabilityIssuer) IssueRunCapabilities(
	_ context.Context,
	request IssueRunCapabilitiesRequest,
) (IssueRunCapabilitiesResult, error) {
	issuer.calls = append(issuer.calls, request)
	index := len(issuer.calls) - 1
	var result IssueRunCapabilitiesResult
	if index < len(issuer.results) {
		result = issuer.results[index]
	}
	var err error
	if index < len(issuer.errors) {
		err = issuer.errors[index]
	}
	return result, err
}
