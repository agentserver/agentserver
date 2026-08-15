package coreserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
)

const (
	maximumProductionRunDuration     = 24 * time.Hour
	maximumProductionCapabilityGrace = 10 * time.Minute
	maximumProductionRouteTextBytes  = 256
	maximumCapabilitySafeJSONInteger = int64(1<<53 - 1)
	capabilityIdentityDomain         = "agentserver-v2/production-run-capability/id/sha256-v1\x00"
)

var productionCapabilityUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type ProductionRunCapabilityPolicy struct {
	ExecutorID     string
	MaxRunDuration time.Duration
	MaxApprovalTTL time.Duration
	ExpiryGrace    time.Duration
}

type RunCapabilityStateStore interface {
	ResolveRunCapabilityIssuance(context.Context, coredb.ResolveRunCapabilityIssuanceCommand) (coredb.RunCapabilityIssuanceAuthority, error)
	AuthorizeRunCapability(context.Context, coredb.AuthorizeRunCapabilityCommand) (coredb.AuthorizedRunCapability, error)
}

type RunCapabilityAuthority interface {
	IssueRunCapabilities(context.Context, corecontract.IssueRunCapabilitiesRequest) (corecontract.IssueRunCapabilitiesResponse, error)
	AuthorizeExecutorRunCapability(context.Context, string, corecontract.AuthorizeExecutorRunCapabilityRequest) (corecontract.AuthorizeRunCapabilityResponse, error)
	AuthorizeLLMProxyRunCapability(context.Context, string, corecontract.AuthorizeLLMProxyRunCapabilityRequest) (corecontract.AuthorizeLLMProxyRunCapabilityResponse, error)
}

type LLMGatewayUpstreamResolver interface {
	ResolveUpstream(context.Context, coredb.LLMGatewayLiveAuthority) (LLMGatewayUpstreamAuthorization, error)
}

type ProductionRunCapabilityServiceConfig struct {
	Store              RunCapabilityStateStore
	Signer             *runcapability.ProductionSigner
	Verifier           *runcapability.ProductionVerifier
	Policy             ProductionRunCapabilityPolicy
	LLMGatewayResolver LLMGatewayUpstreamResolver
	Now                func() time.Time
	Logger             *slog.Logger
}

// ProductionRunCapabilityService keeps signing and online authorization in
// Core. The asymmetric token is an input to this decision, never a replacement
// for current PostgreSQL lease, generation, membership, catalog and executor
// facts.
type ProductionRunCapabilityService struct {
	store              RunCapabilityStateStore
	signer             *runcapability.ProductionSigner
	verifier           *runcapability.ProductionVerifier
	policy             ProductionRunCapabilityPolicy
	llmGatewayResolver LLMGatewayUpstreamResolver
	now                func() time.Time
	logger             *slog.Logger
}

var _ RunCapabilityAuthority = (*ProductionRunCapabilityService)(nil)

func NewProductionRunCapabilityService(config ProductionRunCapabilityServiceConfig) (*ProductionRunCapabilityService, error) {
	if config.Store == nil {
		return nil, errors.New("production run capability state store is required")
	}
	if config.Signer == nil || config.Signer.Issuer() == "" || config.Signer.KeyID() == "" {
		return nil, errors.New("production run capability signer is required")
	}
	if config.Verifier == nil || config.Verifier.Issuer() != config.Signer.Issuer() {
		return nil, errors.New("production run capability verifier must trust the signer issuer")
	}
	if config.LLMGatewayResolver == nil {
		return nil, errors.New("workspace LLM gateway upstream resolver is required")
	}
	if !slices.Contains(config.Verifier.KeyIDs(), config.Signer.KeyID()) {
		return nil, errors.New("production run capability verifier does not contain the active signing key")
	}
	if err := ValidateProductionRunCapabilityPolicy(config.Policy); err != nil {
		return nil, fmt.Errorf("production run capability policy: %w", err)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &ProductionRunCapabilityService{
		store: config.Store, signer: config.Signer, verifier: config.Verifier,
		policy: config.Policy, llmGatewayResolver: config.LLMGatewayResolver, now: config.Now,
		logger: config.Logger,
	}, nil
}

func (service *ProductionRunCapabilityService) IssueRunCapabilities(
	ctx context.Context,
	request corecontract.IssueRunCapabilitiesRequest,
) (corecontract.IssueRunCapabilitiesResponse, error) {
	if ctx == nil {
		return corecontract.IssueRunCapabilitiesResponse{}, errors.New("run capability issuance context is required")
	}
	if service == nil || service.store == nil || service.signer == nil || service.verifier == nil {
		return corecontract.IssueRunCapabilitiesResponse{}, errors.New("production run capability service is unavailable")
	}
	catalogDigest, err := decodeCapabilityDigest("toolCatalogDigest", request.ToolCatalogDigest)
	if err != nil {
		return corecontract.IssueRunCapabilitiesResponse{}, capabilityServiceStateError(
			coredb.ErrorInvalidArgument, "IssueRunCapabilities", request.RunAttemptID, err.Error(),
		)
	}
	managedSandbox, err := decodeRunManagedSandboxBinding(request.ManagedSandbox)
	if err != nil {
		return corecontract.IssueRunCapabilitiesResponse{}, capabilityServiceStateError(
			coredb.ErrorInvalidArgument, "IssueRunCapabilities", request.RunAttemptID, err.Error(),
		)
	}
	if err := validateIssueRunCapabilitiesRequest(request); err != nil {
		return corecontract.IssueRunCapabilitiesResponse{}, capabilityServiceStateError(
			coredb.ErrorInvalidArgument, "IssueRunCapabilities", request.RunAttemptID, err.Error(),
		)
	}
	if err := service.requireIssuePolicy(request); err != nil {
		return corecontract.IssueRunCapabilitiesResponse{}, err
	}
	authority, err := service.store.ResolveRunCapabilityIssuance(ctx, coredb.ResolveRunCapabilityIssuanceCommand{
		WorkspaceID: request.WorkspaceID, SessionID: request.SessionID, RunID: request.RunID,
		AttemptID: request.RunAttemptID, HolderID: request.HolderID,
		Generation: request.RunAttemptGeneration, ExpectedRunVersion: request.ExpectedRunVersion,
		ExpectedAttemptVersion: request.ExpectedRunAttemptVersion,
		ExecutorID:             request.ExecutorID, BrainToolCatalogID: request.BrainToolCatalogID,
		ToolCatalogDigest: catalogDigest,
		LLMGateway: coredb.RunLLMGatewayBinding{
			GatewayID: request.LLMGatewayID, ConfigVersion: request.LLMGatewayVersion,
			GrantUserID: request.LLMGatewayGrantUserID, Model: request.Model,
		},
		ManagedSandbox: managedSandbox,
	})
	if err != nil {
		return corecontract.IssueRunCapabilitiesResponse{}, err
	}
	if err := validateRunCapabilityIssuanceProjection(request, catalogDigest, authority); err != nil {
		return corecontract.IssueRunCapabilitiesResponse{}, err
	}
	issuedAt := time.UnixMilli(authority.AttemptCreatedAt.UnixMilli()).UTC()
	runDeadline := issuedAt.Add(service.policy.MaxRunDuration)
	expiresAt := runDeadline.Add(service.policy.ExpiryGrace)
	if err := validateCapabilityTimes(authority.DatabaseTime, issuedAt, runDeadline, expiresAt); err != nil {
		return corecontract.IssueRunCapabilitiesResponse{}, capabilityServiceStateError(
			coredb.ErrorForbidden, "IssueRunCapabilities", authority.AttemptID, err.Error(),
		)
	}
	if err := validateCapabilityTimes(service.now().UTC(), issuedAt, runDeadline, expiresAt); err != nil {
		return corecontract.IssueRunCapabilitiesResponse{}, capabilityServiceStateError(
			coredb.ErrorForbidden, "IssueRunCapabilities", authority.AttemptID, err.Error(),
		)
	}
	if authority.RunVersion >= maximumCapabilitySafeJSONInteger || authority.AttemptVersion >= maximumCapabilitySafeJSONInteger {
		return corecontract.IssueRunCapabilitiesResponse{}, capabilityServiceStateError(
			coredb.ErrorInvalidState, "IssueRunCapabilities", authority.AttemptID,
			"run versions cannot cross the turn-acceptance boundary safely",
		)
	}

	common := runcapability.Claims{
		Version: runcapability.ProductionVersion, Issuer: service.signer.Issuer(),
		WorkspaceID: authority.WorkspaceID, SessionID: authority.SessionID,
		RunID: authority.RunID, RunAttemptID: authority.AttemptID,
		RunAttemptGeneration: authority.Generation, ActorID: authority.ActorID,
		HolderID: authority.HolderID, IssuedAtUnixMS: issuedAt.UnixMilli(),
		RunDeadlineUnixMS: runDeadline.UnixMilli(), ExpiresAtUnixMS: expiresAt.UnixMilli(),
	}
	executorClaims := common
	executorClaims.Audience = runcapability.AudienceExecutorMCP
	executorClaims.ExecutorID = authority.ExecutorID
	executorClaims.ToolCatalogDigest = hex.EncodeToString(authority.ToolCatalogDigest[:])
	executorClaims.ExpectedRunVersion = authority.RunVersion + 1
	executorClaims.ExpectedRunAttemptVersion = authority.AttemptVersion + 1
	executorClaims.MaxApprovalTTLMillis = service.policy.MaxApprovalTTL.Milliseconds()
	if authority.ManagedSandbox != (coredb.RunManagedSandboxBinding{}) {
		executorClaims.ManagedSandboxSettingVersion = authority.ManagedSandbox.SettingVersion
		executorClaims.ManagedSandboxRegion = authority.ManagedSandbox.Region
		executorClaims.ManagedSandboxProfileID = authority.ManagedSandbox.ProfileID
		executorClaims.ManagedSandboxBindingSHA256 = hex.EncodeToString(authority.ManagedSandbox.BindingSHA256[:])
		executorClaims.ManagedSandboxEnvironmentID = authority.ManagedSandbox.EnvironmentID
	}
	executorCapability, err := service.issueOne(executorClaims)
	if err != nil {
		return corecontract.IssueRunCapabilitiesResponse{}, fmt.Errorf("issue executor MCP run capability: %w", err)
	}

	modelClaims := common
	modelClaims.Audience = runcapability.AudienceLLMProxy
	modelClaims.Model = authority.LLMGateway.Model
	modelClaims.Provider = corecontract.WorkspaceLLMGatewayProvider
	modelClaims.LLMGatewayID = authority.LLMGateway.GatewayID
	modelClaims.LLMGatewayVersion = authority.LLMGateway.ConfigVersion
	modelClaims.LLMGatewayGrantUserID = authority.LLMGateway.GrantUserID
	modelCapability, err := service.issueOne(modelClaims)
	if err != nil {
		return corecontract.IssueRunCapabilitiesResponse{}, fmt.Errorf("issue llmproxy run capability: %w", err)
	}
	if executorCapability.CapabilityID == modelCapability.CapabilityID {
		return corecontract.IssueRunCapabilitiesResponse{}, errors.New("production run capability audiences received the same identity")
	}
	return corecontract.IssueRunCapabilitiesResponse{
		ExecutorMCP: executorCapability, LLMProxy: modelCapability,
	}, nil
}

func (service *ProductionRunCapabilityService) AuthorizeExecutorRunCapability(
	ctx context.Context,
	token string,
	request corecontract.AuthorizeExecutorRunCapabilityRequest,
) (corecontract.AuthorizeRunCapabilityResponse, error) {
	claims, err := service.verifyForLiveAuthorization(token, runcapability.AudienceExecutorMCP)
	if err != nil {
		return corecontract.AuthorizeRunCapabilityResponse{}, err
	}
	if request.ExecutorID != claims.ExecutorID || request.ToolCatalogDigest != claims.ToolCatalogDigest ||
		claims.ExecutorID != service.policy.ExecutorID {
		return corecontract.AuthorizeRunCapabilityResponse{}, deniedRunCapability(claims.CapabilityID)
	}
	catalogDigest, err := decodeCapabilityDigest("toolCatalogDigest", claims.ToolCatalogDigest)
	if err != nil {
		return corecontract.AuthorizeRunCapabilityResponse{}, deniedRunCapability(claims.CapabilityID)
	}
	managedSandbox, err := managedSandboxBindingFromClaims(claims)
	if err != nil {
		return corecontract.AuthorizeRunCapabilityResponse{}, deniedRunCapability(claims.CapabilityID)
	}
	return service.authorizeClaims(ctx, claims, coredb.AuthorizeRunCapabilityCommand{
		Audience: coredb.RunCapabilityAudienceExecutorMCP, CapabilityID: claims.CapabilityID,
		WorkspaceID: claims.WorkspaceID, SessionID: claims.SessionID, RunID: claims.RunID,
		AttemptID: claims.RunAttemptID, ActorID: claims.ActorID, HolderID: claims.HolderID,
		Generation: claims.RunAttemptGeneration, ExecutorID: claims.ExecutorID,
		ToolCatalogDigest: catalogDigest, ExpectedRunVersion: claims.ExpectedRunVersion,
		ExpectedAttemptVersion: claims.ExpectedRunAttemptVersion,
		ManagedSandbox:         managedSandbox,
	})
}

func (service *ProductionRunCapabilityService) AuthorizeLLMProxyRunCapability(
	ctx context.Context,
	token string,
	request corecontract.AuthorizeLLMProxyRunCapabilityRequest,
) (corecontract.AuthorizeLLMProxyRunCapabilityResponse, error) {
	claims, err := service.verifyForLiveAuthorization(token, runcapability.AudienceLLMProxy)
	if err != nil {
		service.logLLMAuthorizationFailure("capability_verification", err)
		return corecontract.AuthorizeLLMProxyRunCapabilityResponse{}, err
	}
	if request.Model != claims.Model || request.Provider != claims.Provider ||
		request.LLMGatewayID != claims.LLMGatewayID ||
		request.LLMGatewayVersion != claims.LLMGatewayVersion ||
		request.LLMGatewayGrantUserID != claims.LLMGatewayGrantUserID ||
		claims.Provider != corecontract.WorkspaceLLMGatewayProvider {
		service.logLLMAuthorizationFailure("route_claim_mismatch", nil)
		return corecontract.AuthorizeLLMProxyRunCapabilityResponse{}, deniedRunCapability(claims.CapabilityID)
	}
	if ctx == nil {
		return corecontract.AuthorizeLLMProxyRunCapabilityResponse{}, errors.New("run capability authorization context is required")
	}
	result, err := service.store.AuthorizeRunCapability(ctx, coredb.AuthorizeRunCapabilityCommand{
		Audience: coredb.RunCapabilityAudienceLLMProxy, CapabilityID: claims.CapabilityID,
		WorkspaceID: claims.WorkspaceID, SessionID: claims.SessionID, RunID: claims.RunID,
		AttemptID: claims.RunAttemptID, ActorID: claims.ActorID, HolderID: claims.HolderID,
		Generation: claims.RunAttemptGeneration,
		LLMGateway: coredb.RunLLMGatewayBinding{
			GatewayID: claims.LLMGatewayID, ConfigVersion: claims.LLMGatewayVersion,
			GrantUserID: claims.LLMGatewayGrantUserID, Model: claims.Model,
		},
	})
	if err != nil {
		service.logLLMAuthorizationFailure("live_state_authority", err)
		return corecontract.AuthorizeLLMProxyRunCapabilityResponse{}, err
	}
	issuedAt := time.UnixMilli(claims.IssuedAtUnixMS).UTC()
	deadline := time.UnixMilli(claims.RunDeadlineUnixMS).UTC()
	expiresAt := time.UnixMilli(claims.ExpiresAtUnixMS).UTC()
	if err := validateCapabilityTimes(result.DatabaseTime, issuedAt, deadline, expiresAt); err != nil || result.LLMGateway == nil {
		service.logLLMAuthorizationFailure("capability_time_or_gateway_projection", err)
		return corecontract.AuthorizeLLMProxyRunCapabilityResponse{}, deniedRunCapability(claims.CapabilityID)
	}
	upstream, err := service.llmGatewayResolver.ResolveUpstream(ctx, *result.LLMGateway)
	if err != nil {
		service.logLLMAuthorizationFailure("gateway_upstream_resolution", err)
		return corecontract.AuthorizeLLMProxyRunCapabilityResponse{}, deniedRunCapability(claims.CapabilityID)
	}
	if upstream.GatewayID != claims.LLMGatewayID || upstream.GatewayConfigVersion != claims.LLMGatewayVersion ||
		upstream.GrantUserID != claims.LLMGatewayGrantUserID || upstream.Model != claims.Model ||
		upstream.ResponsesURL == "" || upstream.Authorization == "" ||
		!upstream.BearerExpiresAt.After(result.DatabaseTime.UTC()) {
		service.logLLMAuthorizationFailure("resolved_upstream_projection", nil)
		return corecontract.AuthorizeLLMProxyRunCapabilityResponse{}, deniedRunCapability(claims.CapabilityID)
	}
	return corecontract.AuthorizeLLMProxyRunCapabilityResponse{
		CapabilityID: claims.CapabilityID, Audience: claims.Audience,
		RunID: claims.RunID, RunAttemptID: claims.RunAttemptID,
		RunAttemptGeneration: claims.RunAttemptGeneration,
		RunVersion:           result.RunVersion, RunAttemptVersion: result.AttemptVersion,
		AuthorizedAt: result.DatabaseTime.UTC(), Model: claims.Model,
		Provider:     corecontract.WorkspaceLLMGatewayProvider,
		LLMGatewayID: upstream.GatewayID, LLMGatewayVersion: upstream.GatewayConfigVersion,
		LLMGatewayGrantUserID: upstream.GrantUserID, ResponsesURL: upstream.ResponsesURL,
		UpstreamAuthorization: upstream.Authorization, BearerExpiresAt: upstream.BearerExpiresAt.UTC(),
	}, nil
}

// logLLMAuthorizationFailure retains only a fixed decision stage and, when
// available, the bounded database error code. Capabilities, model routes,
// provider errors, URLs, credentials, and user content are intentionally
// excluded from this boundary log.
func (service *ProductionRunCapabilityService) logLLMAuthorizationFailure(stage string, err error) {
	if service == nil || service.logger == nil {
		return
	}
	code := "none"
	var stateError *coredb.StateError
	if errors.As(err, &stateError) {
		code = string(stateError.Code)
	}
	service.logger.Warn("llmproxy capability authorization did not complete", "stage", stage, "state_code", code)
}

func (service *ProductionRunCapabilityService) authorizeClaims(
	ctx context.Context,
	claims runcapability.Claims,
	command coredb.AuthorizeRunCapabilityCommand,
) (corecontract.AuthorizeRunCapabilityResponse, error) {
	if ctx == nil {
		return corecontract.AuthorizeRunCapabilityResponse{}, errors.New("run capability authorization context is required")
	}
	result, err := service.store.AuthorizeRunCapability(ctx, command)
	if err != nil {
		return corecontract.AuthorizeRunCapabilityResponse{}, err
	}
	issuedAt := time.UnixMilli(claims.IssuedAtUnixMS).UTC()
	deadline := time.UnixMilli(claims.RunDeadlineUnixMS).UTC()
	expiresAt := time.UnixMilli(claims.ExpiresAtUnixMS).UTC()
	if err := validateCapabilityTimes(result.DatabaseTime, issuedAt, deadline, expiresAt); err != nil {
		return corecontract.AuthorizeRunCapabilityResponse{}, deniedRunCapability(claims.CapabilityID)
	}
	return corecontract.AuthorizeRunCapabilityResponse{
		CapabilityID: claims.CapabilityID, Audience: claims.Audience,
		RunID: claims.RunID, RunAttemptID: claims.RunAttemptID,
		RunAttemptGeneration: claims.RunAttemptGeneration,
		RunVersion:           result.RunVersion, RunAttemptVersion: result.AttemptVersion,
		AuthorizedAt: result.DatabaseTime.UTC(),
	}, nil
}

func (service *ProductionRunCapabilityService) issueOne(claims runcapability.Claims) (corecontract.IssuedRunCapability, error) {
	identity, err := stableProductionCapabilityID(service.signer.KeyID(), claims)
	if err != nil {
		return corecontract.IssuedRunCapability{}, err
	}
	claims.CapabilityID = identity
	token, err := service.signer.Sign(claims)
	if err != nil {
		return corecontract.IssuedRunCapability{}, err
	}
	return corecontract.IssuedRunCapability{
		CapabilityID: identity, Audience: claims.Audience, Token: token,
		IssuedAt:    time.UnixMilli(claims.IssuedAtUnixMS).UTC(),
		RunDeadline: time.UnixMilli(claims.RunDeadlineUnixMS).UTC(),
		ExpiresAt:   time.UnixMilli(claims.ExpiresAtUnixMS).UTC(),
	}, nil
}

func (service *ProductionRunCapabilityService) verifyForLiveAuthorization(token, audience string) (runcapability.Claims, error) {
	if service == nil || service.store == nil || service.verifier == nil || service.now == nil {
		return runcapability.Claims{}, errors.New("production run capability service is unavailable")
	}
	now := service.now().UTC()
	claims, err := service.verifier.Verify(token, audience, now)
	if err != nil {
		return runcapability.Claims{}, deniedRunCapability("")
	}
	if err := validateCapabilityTimes(
		now,
		time.UnixMilli(claims.IssuedAtUnixMS).UTC(),
		time.UnixMilli(claims.RunDeadlineUnixMS).UTC(),
		time.UnixMilli(claims.ExpiresAtUnixMS).UTC(),
	); err != nil {
		return runcapability.Claims{}, deniedRunCapability(claims.CapabilityID)
	}
	return claims, nil
}

func validateIssueRunCapabilitiesRequest(request corecontract.IssueRunCapabilitiesRequest) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "workspaceId", value: request.WorkspaceID},
		{name: "sessionId", value: request.SessionID},
		{name: "runId", value: request.RunID},
		{name: "runAttemptId", value: request.RunAttemptID},
		{name: "executorId", value: request.ExecutorID},
		{name: "brainToolCatalogId", value: request.BrainToolCatalogID},
	} {
		if field.value == "00000000-0000-0000-0000-000000000000" || !productionCapabilityUUIDPattern.MatchString(field.value) {
			return fmt.Errorf("%s must be a non-zero canonical lowercase UUID", field.name)
		}
	}
	if !validCapabilityRouteText(request.HolderID) {
		return errors.New("holderId must be canonical bounded text")
	}
	if request.RunAttemptGeneration < 1 || request.RunAttemptGeneration > maximumCapabilitySafeJSONInteger ||
		request.ExpectedRunVersion < 1 || request.ExpectedRunVersion >= maximumCapabilitySafeJSONInteger ||
		request.ExpectedRunAttemptVersion < 1 || request.ExpectedRunAttemptVersion >= maximumCapabilitySafeJSONInteger {
		return errors.New("generation and expected versions must be positive JSON-safe integers with room for turn acceptance")
	}
	if !validCapabilityRouteText(request.Model) || !validCapabilityRouteText(request.Provider) {
		return errors.New("model and provider must be canonical bounded text")
	}
	if request.Provider != corecontract.WorkspaceLLMGatewayProvider ||
		!productionCapabilityUUIDPattern.MatchString(request.LLMGatewayID) ||
		!productionCapabilityUUIDPattern.MatchString(request.LLMGatewayGrantUserID) ||
		request.LLMGatewayVersion < 1 || request.LLMGatewayVersion > maximumCapabilitySafeJSONInteger {
		return errors.New("workspace LLM gateway route must contain an exact provider, gateway, version, and grant user")
	}
	if request.MaxRunDurationMillis < int64(time.Second/time.Millisecond) ||
		request.MaxRunDurationMillis > int64(maximumProductionRunDuration/time.Millisecond) {
		return errors.New("maxRunDurationMs is outside the production bound")
	}
	if request.MaxApprovalTTLMillis < int64(time.Second/time.Millisecond) ||
		request.MaxApprovalTTLMillis > request.MaxRunDurationMillis {
		return errors.New("maxApprovalTtlMs must be at least one second and not exceed maxRunDurationMs")
	}
	return nil
}

func validateRunCapabilityIssuanceProjection(
	request corecontract.IssueRunCapabilitiesRequest,
	catalogDigest [sha256.Size]byte,
	authority coredb.RunCapabilityIssuanceAuthority,
) error {
	if authority.WorkspaceID != request.WorkspaceID || authority.SessionID != request.SessionID ||
		authority.RunID != request.RunID || authority.AttemptID != request.RunAttemptID ||
		authority.HolderID != request.HolderID || authority.Generation != request.RunAttemptGeneration ||
		authority.RunVersion != request.ExpectedRunVersion || authority.AttemptVersion != request.ExpectedRunAttemptVersion ||
		authority.ExecutorID != request.ExecutorID || authority.BrainToolCatalogID != request.BrainToolCatalogID ||
		authority.ToolCatalogDigest != catalogDigest ||
		authority.LLMGateway != (coredb.RunLLMGatewayBinding{
			GatewayID: request.LLMGatewayID, ConfigVersion: request.LLMGatewayVersion,
			GrantUserID: request.LLMGatewayGrantUserID, Model: request.Model,
		}) {
		return errors.New("production run capability store returned an inconsistent issuance projection")
	}
	requestManagedSandbox, err := decodeRunManagedSandboxBinding(request.ManagedSandbox)
	if err != nil || authority.ManagedSandbox != requestManagedSandbox {
		return errors.New("production run capability store returned inconsistent managed sandbox authority")
	}
	if authority.ActorID == "00000000-0000-0000-0000-000000000000" ||
		!productionCapabilityUUIDPattern.MatchString(authority.ActorID) ||
		authority.AttemptCreatedAt.IsZero() || authority.DatabaseTime.IsZero() {
		return errors.New("production run capability store returned an invalid issuance projection")
	}
	return nil
}

func (service *ProductionRunCapabilityService) requireIssuePolicy(request corecontract.IssueRunCapabilitiesRequest) error {
	if request.ExecutorID != service.policy.ExecutorID ||
		request.MaxRunDurationMillis != service.policy.MaxRunDuration.Milliseconds() ||
		request.MaxApprovalTTLMillis != service.policy.MaxApprovalTTL.Milliseconds() {
		return capabilityServiceStateError(
			coredb.ErrorForbidden, "IssueRunCapabilities", request.RunAttemptID,
			"requested runtime authority does not match Core production policy",
		)
	}
	return nil
}

// ValidateProductionRunCapabilityPolicy validates the complete static policy
// before a command opens network listeners or begins issuing authority.
func ValidateProductionRunCapabilityPolicy(policy ProductionRunCapabilityPolicy) error {
	if policy.ExecutorID == "00000000-0000-0000-0000-000000000000" ||
		!productionCapabilityUUIDPattern.MatchString(policy.ExecutorID) {
		return errors.New("executor ID must be a non-zero canonical lowercase UUID")
	}
	if policy.MaxRunDuration < time.Second || policy.MaxRunDuration > maximumProductionRunDuration {
		return fmt.Errorf("maximum run duration must be between 1s and %s", maximumProductionRunDuration)
	}
	if policy.MaxApprovalTTL < time.Second || policy.MaxApprovalTTL > policy.MaxRunDuration {
		return errors.New("maximum approval TTL must be at least 1s and not exceed maximum run duration")
	}
	if policy.ExpiryGrace < time.Second || policy.ExpiryGrace > maximumProductionCapabilityGrace {
		return fmt.Errorf("capability expiry grace must be between 1s and %s", maximumProductionCapabilityGrace)
	}
	return nil
}

func validCapabilityRouteText(value string) bool {
	if value == "" || len(value) > maximumProductionRouteTextBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validateCapabilityTimes(databaseTime, issuedAt, deadline, expiresAt time.Time) error {
	if databaseTime.IsZero() || issuedAt.IsZero() || deadline.IsZero() || expiresAt.IsZero() ||
		!deadline.After(issuedAt) || expiresAt.Before(deadline) || databaseTime.Before(issuedAt) ||
		!databaseTime.Before(deadline) || !databaseTime.Before(expiresAt) {
		return errors.New("run capability is outside its database-time execution window")
	}
	for _, value := range []time.Time{issuedAt, deadline, expiresAt} {
		unixMillis := value.UnixMilli()
		if unixMillis < 1 || unixMillis > maximumCapabilitySafeJSONInteger {
			return errors.New("run capability time is outside the JSON-safe range")
		}
	}
	return nil
}

func decodeCapabilityDigest(field, value string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != value {
		return digest, fmt.Errorf("%s must be lowercase 64-character SHA-256 hex", field)
	}
	copy(digest[:], decoded)
	if digest == ([sha256.Size]byte{}) {
		return [sha256.Size]byte{}, fmt.Errorf("%s must not be all zero", field)
	}
	return digest, nil
}

func decodeRunManagedSandboxBinding(source *corecontract.RunLaunchManagedSandboxState) (coredb.RunManagedSandboxBinding, error) {
	if source == nil {
		return coredb.RunManagedSandboxBinding{}, nil
	}
	digest, err := decodeCapabilityDigest("managedSandbox.bindingSha256", source.BindingSHA256)
	if err != nil {
		return coredb.RunManagedSandboxBinding{}, err
	}
	if source.SettingVersion < 1 || source.SettingVersion > maximumCapabilitySafeJSONInteger {
		return coredb.RunManagedSandboxBinding{}, errors.New("managedSandbox.settingVersion must be a positive safe integer")
	}
	profile := managedsandboxprofile.Binding{
		Region: source.Region, ProfileID: source.ProfileID,
		BindingSHA256: source.BindingSHA256, EnvironmentID: source.EnvironmentID,
	}
	if err := profile.Validate(); err != nil {
		return coredb.RunManagedSandboxBinding{}, err
	}
	return coredb.RunManagedSandboxBinding{
		SettingVersion: source.SettingVersion, Region: source.Region, ProfileID: source.ProfileID,
		BindingSHA256: digest, EnvironmentID: source.EnvironmentID,
	}, nil
}

func managedSandboxBindingFromClaims(claims runcapability.Claims) (coredb.RunManagedSandboxBinding, error) {
	configured := claims.ManagedSandboxSettingVersion != 0 || claims.ManagedSandboxRegion != "" ||
		claims.ManagedSandboxProfileID != "" || claims.ManagedSandboxBindingSHA256 != "" ||
		claims.ManagedSandboxEnvironmentID != ""
	if !configured {
		return coredb.RunManagedSandboxBinding{}, nil
	}
	return decodeRunManagedSandboxBinding(&corecontract.RunLaunchManagedSandboxState{
		SettingVersion: claims.ManagedSandboxSettingVersion, Region: claims.ManagedSandboxRegion,
		ProfileID: claims.ManagedSandboxProfileID, BindingSHA256: claims.ManagedSandboxBindingSHA256,
		EnvironmentID: claims.ManagedSandboxEnvironmentID,
	})
}

func stableProductionCapabilityID(keyID string, claims runcapability.Claims) (string, error) {
	claims.CapabilityID = ""
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode production capability identity authority: %w", err)
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(capabilityIdentityDomain))
	_, _ = hasher.Write([]byte(keyID))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(raw)
	sum := hasher.Sum(nil)
	identity := append([]byte(nil), sum[:16]...)
	// RFC 9562 UUIDv8 marks this as an application-defined SHA-256 identity.
	identity[6] = (identity[6] & 0x0f) | 0x80
	identity[8] = (identity[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		identity[0:4], identity[4:6], identity[6:8], identity[8:10], identity[10:16],
	), nil
}

func deniedRunCapability(capabilityID string) error {
	return capabilityServiceStateError(
		coredb.ErrorForbidden, "AuthorizeRunCapability", capabilityID,
		"run capability is not currently authorized",
	)
}

func capabilityServiceStateError(code coredb.StateErrorCode, operation, resourceID, message string) error {
	return &coredb.StateError{
		Code: code, Operation: operation, Resource: "run_capability",
		ResourceID: resourceID, Message: message,
	}
}
