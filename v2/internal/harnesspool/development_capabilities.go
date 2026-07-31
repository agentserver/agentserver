package harnesspool

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnessbootstrap"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
)

const (
	defaultDevelopmentCapabilityExpiryGrace = 2 * time.Minute
	maximumDevelopmentCapabilityExpiryGrace = 10 * time.Minute
)

// DevelopmentAttemptRuntimeCapabilitySourceConfig controls only the explicit
// insecure-development HMAC issuer. Production capability issuance is an
// asymmetric and online-revocable service boundary, not another configuration
// of this source.
type DevelopmentAttemptRuntimeCapabilitySourceConfig struct {
	ExecutorID  string
	ExpiryGrace time.Duration
	Now         func() time.Time
	IDGenerator IDGenerator
}

func DefaultDevelopmentAttemptRuntimeCapabilitySourceConfig(executorID string) DevelopmentAttemptRuntimeCapabilitySourceConfig {
	return DevelopmentAttemptRuntimeCapabilitySourceConfig{
		ExecutorID: executorID, ExpiryGrace: defaultDevelopmentCapabilityExpiryGrace,
		Now: time.Now, IDGenerator: newRandomUUID,
	}
}

// DevelopmentAttemptRuntimeCapabilitySource mints two distinct, audience-
// separated HMAC capabilities for a dynamically claimed local attempt. The
// executor capability is bound to the post-TurnAccepted run versions because
// no MCP tool call is legal before that transition succeeds.
type DevelopmentAttemptRuntimeCapabilitySource struct {
	codec  *runcapability.DevelopmentCodec
	config DevelopmentAttemptRuntimeCapabilitySourceConfig
}

func NewDevelopmentAttemptRuntimeCapabilitySource(
	codec *runcapability.DevelopmentCodec,
	config DevelopmentAttemptRuntimeCapabilitySourceConfig,
) (*DevelopmentAttemptRuntimeCapabilitySource, error) {
	if codec == nil {
		return nil, errors.New("development run capability codec is required")
	}
	if err := validateUUIDIdentity("development executor ID", config.ExecutorID); err != nil {
		return nil, err
	}
	if config.ExpiryGrace < time.Second || config.ExpiryGrace > maximumDevelopmentCapabilityExpiryGrace {
		return nil, fmt.Errorf("development capability expiry grace must be between 1s and %s", maximumDevelopmentCapabilityExpiryGrace)
	}
	if config.Now == nil {
		return nil, errors.New("development capability clock is required")
	}
	if config.IDGenerator == nil {
		return nil, errors.New("development capability identity generator is required")
	}
	return &DevelopmentAttemptRuntimeCapabilitySource{codec: codec, config: config}, nil
}

func (source *DevelopmentAttemptRuntimeCapabilitySource) IssueAttemptRuntimeCapabilities(
	ctx context.Context,
	prepared PreparedRunLaunch,
) (harnessbootstrap.RuntimeCapabilities, error) {
	if ctx == nil {
		return harnessbootstrap.RuntimeCapabilities{}, errors.New("development capability issuance context is required")
	}
	if err := ctx.Err(); err != nil {
		return harnessbootstrap.RuntimeCapabilities{}, err
	}
	if source == nil || source.codec == nil || source.config.Now == nil || source.config.IDGenerator == nil {
		return harnessbootstrap.RuntimeCapabilities{}, errors.New("development attempt capability source is required")
	}
	if err := validatePreparedSupervisionInput(prepared.Scheduled, prepared); err != nil {
		return harnessbootstrap.RuntimeCapabilities{}, fmt.Errorf("validate development capability launch authority: %w", err)
	}

	executorCapabilityID, err := source.allocateCapabilityID("executor")
	if err != nil {
		return harnessbootstrap.RuntimeCapabilities{}, err
	}
	modelCapabilityID, err := source.allocateCapabilityID("model")
	if err != nil {
		return harnessbootstrap.RuntimeCapabilities{}, err
	}
	if modelCapabilityID == executorCapabilityID {
		return harnessbootstrap.RuntimeCapabilities{}, errors.New("development runtime capability identities must be distinct")
	}

	now := source.config.Now().UTC()
	maximumDuration := time.Duration(prepared.Manifest.Limits.MaxRunDurationMS) * time.Millisecond
	expiresAt := now.Add(maximumDuration + source.config.ExpiryGrace)
	claim := prepared.Scheduled.Claim
	common := runcapability.Claims{
		Version:     runcapability.DevelopmentVersion,
		WorkspaceID: prepared.Manifest.WorkspaceID, SessionID: prepared.Manifest.SessionID,
		RunID: prepared.Manifest.RunID, RunAttemptID: prepared.Manifest.RunAttemptID,
		RunAttemptGeneration: prepared.Manifest.RunAttemptGeneration,
		ActorID:              claim.Run.ActorID, HolderID: prepared.Manifest.HolderID,
		IssuedAtUnixMS: now.UnixMilli(), ExpiresAtUnixMS: expiresAt.UnixMilli(),
	}
	executorClaims := common
	executorClaims.CapabilityID = executorCapabilityID
	executorClaims.Audience = runcapability.AudienceExecutorMCP
	executorClaims.ExecutorID = source.config.ExecutorID
	executorClaims.ToolCatalogDigest = prepared.Manifest.ExecutorMCP.CatalogDigest
	executorClaims.ExpectedRunVersion = claim.Run.Version + 1
	executorClaims.ExpectedRunAttemptVersion = claim.RunAttempt.Version + 1
	executorCapability, err := source.codec.Sign(executorClaims)
	if err != nil {
		return harnessbootstrap.RuntimeCapabilities{}, fmt.Errorf("sign development executor capability: %w", err)
	}

	modelClaims := common
	modelClaims.CapabilityID = modelCapabilityID
	modelClaims.Audience = runcapability.AudienceLLMProxy
	modelClaims.Model = prepared.Manifest.Model.Model
	modelClaims.Provider = prepared.Manifest.Model.Provider
	modelCapability, err := source.codec.Sign(modelClaims)
	if err != nil {
		return harnessbootstrap.RuntimeCapabilities{}, fmt.Errorf("sign development model capability: %w", err)
	}
	capabilities := harnessbootstrap.RuntimeCapabilities{ExecutorMCP: executorCapability, LLMProxy: modelCapability}
	if err := harnessbootstrap.ValidateRuntimeCapabilities(capabilities); err != nil {
		return harnessbootstrap.RuntimeCapabilities{}, fmt.Errorf("validate development runtime capabilities: %w", err)
	}
	return capabilities, nil
}

func (source *DevelopmentAttemptRuntimeCapabilitySource) allocateCapabilityID(label string) (string, error) {
	identity, err := source.config.IDGenerator()
	if err != nil {
		return "", fmt.Errorf("allocate development %s capability identity: %w", label, err)
	}
	if err := validateUUIDIdentity("development "+label+" capability ID", identity); err != nil {
		return "", err
	}
	return identity, nil
}
