package harnesspool

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnessbootstrap"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
)

const maximumPoolProductionCapabilityExpiryGrace = 10 * time.Minute

// IssueRunCapabilitiesRequest is the exact deployment and attempt authority
// the pool projects to Core. Core re-derives every mutable fact; none of these
// caller-provided fields is authority by itself.
type IssueRunCapabilitiesRequest struct {
	WorkspaceID               string
	SessionID                 string
	RunID                     string
	RunAttemptID              string
	HolderID                  string
	RunAttemptGeneration      int64
	ExpectedRunVersion        int64
	ExpectedRunAttemptVersion int64
	ExecutorID                string
	BrainToolCatalogID        string
	ToolCatalogDigest         string
	Model                     string
	Provider                  string
	LLMGatewayID              string
	LLMGatewayVersion         int64
	LLMGatewayGrantUserID     string
	MaxRunDuration            time.Duration
	MaxApprovalTTL            time.Duration
}

type IssuedRunCapability struct {
	CapabilityID string
	Audience     string
	Token        string
	IssuedAt     time.Time
	RunDeadline  time.Time
	ExpiresAt    time.Time
}

type IssueRunCapabilitiesResult struct {
	ExecutorMCP IssuedRunCapability
	LLMProxy    IssuedRunCapability
}

type RunCapabilityIssuanceClient interface {
	IssueRunCapabilities(context.Context, IssueRunCapabilitiesRequest) (IssueRunCapabilitiesResult, error)
}

// ProductionAttemptRuntimeCapabilitySource asks Core, the sole private-key
// holder, for audience-separated tokens. It never signs, verifies, caches, or
// persists a token locally; the returned opaque values flow directly into the
// one-shot worker bootstrap pipe.
type ProductionAttemptRuntimeCapabilitySource struct {
	core       RunCapabilityIssuanceClient
	executorID string
}

func NewProductionAttemptRuntimeCapabilitySource(
	core RunCapabilityIssuanceClient,
	executorID string,
) (*ProductionAttemptRuntimeCapabilitySource, error) {
	if core == nil {
		return nil, errors.New("production run capability issuance client is required")
	}
	if err := validateUUIDIdentity("production executor ID", executorID); err != nil {
		return nil, err
	}
	return &ProductionAttemptRuntimeCapabilitySource{core: core, executorID: executorID}, nil
}

func (source *ProductionAttemptRuntimeCapabilitySource) IssueAttemptRuntimeCapabilities(
	ctx context.Context,
	prepared PreparedRunLaunch,
) (harnessbootstrap.RuntimeCapabilities, error) {
	if ctx == nil {
		return harnessbootstrap.RuntimeCapabilities{}, errors.New("production capability issuance context is required")
	}
	if err := ctx.Err(); err != nil {
		return harnessbootstrap.RuntimeCapabilities{}, err
	}
	if source == nil || source.core == nil || source.executorID == "" {
		return harnessbootstrap.RuntimeCapabilities{}, errors.New("production attempt capability source is required")
	}
	if err := validatePreparedSupervisionInput(prepared.Scheduled, prepared); err != nil {
		return harnessbootstrap.RuntimeCapabilities{}, fmt.Errorf("validate production capability launch authority: %w", err)
	}
	claim := prepared.Scheduled.Claim
	if claim.RunAttempt.CreatedAt.IsZero() {
		return harnessbootstrap.RuntimeCapabilities{}, errors.New("production capability attempt creation time is required")
	}
	request := IssueRunCapabilitiesRequest{
		WorkspaceID: prepared.Manifest.WorkspaceID, SessionID: prepared.Manifest.SessionID,
		RunID: prepared.Manifest.RunID, RunAttemptID: prepared.Manifest.RunAttemptID,
		HolderID: prepared.Manifest.HolderID, RunAttemptGeneration: prepared.Manifest.RunAttemptGeneration,
		ExpectedRunVersion: claim.Run.Version, ExpectedRunAttemptVersion: claim.RunAttempt.Version,
		ExecutorID: source.executorID, BrainToolCatalogID: prepared.Manifest.ExecutorMCP.CatalogID,
		ToolCatalogDigest: prepared.Manifest.ExecutorMCP.CatalogDigest,
		Model:             prepared.Manifest.Model.Model, Provider: prepared.Manifest.Model.Provider,
		LLMGatewayID:          prepared.Manifest.Model.LLMGatewayID,
		LLMGatewayVersion:     prepared.Manifest.Model.LLMGatewayVersion,
		LLMGatewayGrantUserID: prepared.Manifest.Model.LLMGatewayGrantUserID,
		MaxRunDuration:        time.Duration(prepared.Manifest.Limits.MaxRunDurationMS) * time.Millisecond,
		MaxApprovalTTL:        time.Duration(prepared.Manifest.Limits.MaxApprovalTTLMS) * time.Millisecond,
	}
	result, err := source.issueExactly(ctx, request)
	if err != nil {
		return harnessbootstrap.RuntimeCapabilities{}, err
	}
	if err := validateProductionCapabilityIssuanceResult(request, claim.RunAttempt.CreatedAt, result); err != nil {
		return harnessbootstrap.RuntimeCapabilities{}, err
	}
	capabilities := harnessbootstrap.RuntimeCapabilities{
		ExecutorMCP: result.ExecutorMCP.Token,
		LLMProxy:    result.LLMProxy.Token,
	}
	if err := harnessbootstrap.ValidateRuntimeCapabilities(capabilities); err != nil {
		return harnessbootstrap.RuntimeCapabilities{}, fmt.Errorf("validate production runtime capabilities: %w", err)
	}
	return capabilities, nil
}

func (source *ProductionAttemptRuntimeCapabilitySource) issueExactly(
	ctx context.Context,
	request IssueRunCapabilitiesRequest,
) (IssueRunCapabilitiesResult, error) {
	result, err := source.core.IssueRunCapabilities(ctx, request)
	if err == nil || !ambiguousPoolCommand(err, ctx) {
		return result, err
	}
	return source.core.IssueRunCapabilities(ctx, request)
}

func validateProductionCapabilityIssuanceResult(
	request IssueRunCapabilitiesRequest,
	attemptCreatedAt time.Time,
	result IssueRunCapabilitiesResult,
) error {
	executor := result.ExecutorMCP
	model := result.LLMProxy
	if err := validateUUIDIdentity("issued executor capability ID", executor.CapabilityID); err != nil {
		return errors.New("Core returned an invalid production executor capability identity")
	}
	if err := validateUUIDIdentity("issued llmproxy capability ID", model.CapabilityID); err != nil {
		return errors.New("Core returned an invalid production llmproxy capability identity")
	}
	if executor.CapabilityID == model.CapabilityID || executor.Token == model.Token ||
		executor.Audience != runcapability.AudienceExecutorMCP || model.Audience != runcapability.AudienceLLMProxy {
		return errors.New("Core returned capabilities without exact audience separation")
	}
	if err := harnessbootstrap.ValidateRuntimeCapabilities(harnessbootstrap.RuntimeCapabilities{
		ExecutorMCP: executor.Token, LLMProxy: model.Token,
	}); err != nil {
		return errors.New("Core returned an invalid production capability token envelope")
	}
	wantIssuedAt := time.UnixMilli(attemptCreatedAt.UnixMilli()).UTC()
	wantDeadline := wantIssuedAt.Add(request.MaxRunDuration)
	if !executor.IssuedAt.Equal(wantIssuedAt) || !model.IssuedAt.Equal(wantIssuedAt) ||
		!executor.RunDeadline.Equal(wantDeadline) || !model.RunDeadline.Equal(wantDeadline) ||
		!executor.ExpiresAt.Equal(model.ExpiresAt) || !executor.ExpiresAt.After(wantDeadline) ||
		executor.ExpiresAt.After(wantDeadline.Add(maximumPoolProductionCapabilityExpiryGrace)) {
		return errors.New("Core returned production capability times outside the attempt and manifest authority")
	}
	return nil
}
