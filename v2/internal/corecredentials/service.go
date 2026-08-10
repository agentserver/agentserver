package corecredentials

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

const (
	ReasonCredentialNotConfigured = "credential_not_configured"
	ReasonCredentialNotReady      = "credential_not_ready"
	ReasonCredentialRevoked       = "credential_revoked"
	ReasonCredentialUnauthorized  = "credential_unauthorized"
	ReasonCredentialInvalid       = "credential_invalid"
	ReasonProviderDenied          = "provider_denied"
	ReasonCoreUnavailable         = "core_credential_resolver_unavailable"
)

// ResolveError is deliberately terse. Its public message is safe for TAE,
// harness and Platform logs; the underlying provider/database error is kept
// private and is never serialized.
type ResolveError struct {
	Code      string
	Retryable bool
	Status    int
	message   string
	cause     error
}

func (err *ResolveError) Error() string {
	if err == nil {
		return "<nil>"
	}
	if err.message != "" {
		return err.Code + ": " + err.message
	}
	return err.Code
}

func (err *ResolveError) Unwrap() error { return err.cause }

func resolveError(code, message string, retryable bool, status int, cause error) *ResolveError {
	return &ResolveError{Code: code, Retryable: retryable, Status: status, message: message, cause: cause}
}

func ResolveErrorCode(err error) string {
	var resolved *ResolveError
	if errors.As(err, &resolved) {
		return resolved.Code
	}
	return ""
}

// CredentialUseAudit is the data-minimized event emitted after a materialize
// attempt. It intentionally has no request headers, sealed bytes, or provider
// response body.
type CredentialUseAudit struct {
	At                   time.Time
	EventID              string
	Stage                string
	CapabilityID         string
	WorkspaceID          string
	SessionID            string
	ActorID              string
	EnvironmentID        string
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	ExecutionID          string
	OperationID          string
	SandboxID            string
	TargetGeneration     int64
	ProviderKind         string
	BindingID            string
	AuthorityVersion     int64
	CredentialVersion    int64
	TAEPSM               string
	Host                 string
	Path                 string
	Method               string
	Decision             string
	ReasonCode           string
}

type AuditSink interface {
	RecordCredentialUse(context.Context, CredentialUseAudit) error
}

type ResolveResult struct {
	ProviderKind      string          `json:"providerKind"`
	Binding           BindingMetadata `json:"binding"`
	AuthorityVersion  int64           `json:"authorityVersion"`
	CredentialVersion int64           `json:"credentialVersion"`
	AccessExpiresAt   *time.Time      `json:"accessExpiresAt,omitempty"`
	ResolvedAt        time.Time       `json:"resolvedAt"`
}

type ServiceConfig struct {
	Registry       *ProviderRegistry
	Bindings       BindingStore
	LiveAuthorizer LiveAuthorizer
	Placeholders   PlaceholderVerifier
	Sealer         *Keyring
	Audit          AuditSink
	Now            func() time.Time
}

type Service struct {
	registry       *ProviderRegistry
	bindings       BindingStore
	liveAuthorizer LiveAuthorizer
	placeholders   PlaceholderVerifier
	sealer         *Keyring
	audit          AuditSink
	now            func() time.Time
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Registry == nil || config.Bindings == nil || config.LiveAuthorizer == nil ||
		config.Sealer == nil || config.Now == nil {
		return nil, errors.New("corecredentials registry, binding store, live authorizer, sealer, and clock are required")
	}
	if config.Audit == nil {
		config.Audit = noopAuditSink{}
	}
	return &Service{
		registry: config.Registry, bindings: config.Bindings, liveAuthorizer: config.LiveAuthorizer,
		placeholders: config.Placeholders, sealer: config.Sealer, audit: config.Audit, now: config.Now,
	}, nil
}

// ResolveInjection is the placeholder-authenticated method that turns a
// workspace binding into a provider header for webhook_swap. A
// workspace/binding lookup alone is never sufficient; the separate trusted
// method below still requires an exact live operation and authenticated Core
// workload boundary.
func (service *Service) ResolveInjection(ctx context.Context, request UseRequest) (HeaderMutation, ResolveResult, error) {
	if service == nil || ctx == nil || service.registry == nil || service.bindings == nil ||
		service.liveAuthorizer == nil || service.sealer == nil || service.now == nil || service.placeholders == nil {
		return HeaderMutation{}, ResolveResult{}, resolveError(ReasonCoreUnavailable, "Core workspace credential service is not configured", true, 503, nil)
	}
	if err := ctx.Err(); err != nil {
		return HeaderMutation{}, ResolveResult{}, resolveError(ReasonCoreUnavailable, "Core workspace credential resolution was cancelled", true, 503, err)
	}
	request.CredentialMode = managedcredential.ModeWebhookSwap
	request.ExpectedCredentialVersion = 0
	if err := request.Validate(); err != nil {
		return HeaderMutation{}, ResolveResult{}, resolveError(ReasonCredentialInvalid, "credential use request is invalid", false, 400, err)
	}
	now := service.now().UTC()
	claims, err := service.placeholders.Verify(request.Placeholder, now)
	if err != nil || !claimsMatchRequest(claims, request, now) {
		return service.fail(ctx, request, ResolveResult{}, "materialize", resolveError(ReasonCredentialUnauthorized, "credential placeholder is not authorized", false, 403, err))
	}
	request.CapabilityID = claims.CapabilityID
	return service.resolveAuthorized(ctx, request, now, "materialize")
}

// ResolveTrustedInjection materializes a credential for an exact live
// operation after the caller has been authenticated as a trusted Core
// workload. Unlike ResolveInjection it does not accept or verify a sandbox
// placeholder; callers must supply a bounded audit identity and an explicit
// audit stage. The database live-authority check is still mandatory.
func (service *Service) ResolveTrustedInjection(
	ctx context.Context,
	request UseRequest,
	capabilityID, auditStage string,
) (HeaderMutation, ResolveResult, error) {
	if service == nil || ctx == nil || service.registry == nil || service.bindings == nil ||
		service.liveAuthorizer == nil || service.sealer == nil || service.now == nil {
		return HeaderMutation{}, ResolveResult{}, resolveError(ReasonCoreUnavailable, "Core workspace credential service is not configured", true, 503, nil)
	}
	if err := ctx.Err(); err != nil {
		return HeaderMutation{}, ResolveResult{}, resolveError(ReasonCoreUnavailable, "Core workspace credential resolution was cancelled", true, 503, err)
	}
	if err := request.ValidateLiveAuthorityScope(); err != nil {
		return HeaderMutation{}, ResolveResult{}, resolveError(ReasonCredentialInvalid, "credential use request is invalid", false, 400, err)
	}
	if capabilityID == "" || len(capabilityID) > 256 || strings.TrimSpace(capabilityID) != capabilityID || strings.ContainsAny(capabilityID, "\x00\r\n") {
		return HeaderMutation{}, ResolveResult{}, resolveError(ReasonCredentialInvalid, "credential audit identity is invalid", false, 400, nil)
	}
	if auditStage != "process_env" && auditStage != "egress" {
		return HeaderMutation{}, ResolveResult{}, resolveError(ReasonCredentialInvalid, "credential audit stage is invalid", false, 400, nil)
	}
	request.CapabilityID = capabilityID
	return service.resolveAuthorized(ctx, request, service.now().UTC(), auditStage)
}

func (service *Service) resolveAuthorized(
	ctx context.Context,
	request UseRequest,
	now time.Time,
	auditStage string,
) (HeaderMutation, ResolveResult, error) {
	ref, err := service.liveAuthorizer.AuthorizeCredentialUse(ctx, request)
	if err != nil {
		return service.fail(ctx, request, ResolveResult{}, auditStage, resolveError(ReasonCredentialUnauthorized, "credential operation is not authorized", true, 403, err))
	}
	if ref.WorkspaceID != request.WorkspaceID || ref.Kind != request.ProviderKind || ref.BindingID != request.BindingID ||
		ref.AuthorityVersion != request.AuthorityVersion || ref.CredentialMode != request.CredentialMode ||
		(request.ExpectedCredentialVersion > 0 && ref.CredentialVersion != request.ExpectedCredentialVersion) {
		return service.fail(ctx, request, ResolveResult{}, auditStage, resolveError(ReasonCredentialUnauthorized, "credential authority version is stale", false, 409, nil))
	}
	binding, err := service.bindings.Get(ctx, request.WorkspaceID, request.ProviderKind, request.BindingID)
	if err != nil {
		return service.fail(ctx, request, ResolveResult{}, auditStage, resolveError(ReasonCoreUnavailable, "Core credential binding store is unavailable", true, 503, err))
	}
	if binding.ID == "" {
		return service.fail(ctx, request, ResolveResult{}, auditStage, resolveError(ReasonCredentialNotConfigured, "workspace credential is not configured", false, 404, nil))
	}
	if err := validateBindingForUse(binding, request, now); err != nil {
		code := ReasonCredentialNotReady
		status := 409
		if binding.Status == StatusRevoked {
			code, status = ReasonCredentialRevoked, 403
		}
		return service.fail(ctx, request, bindingResult(binding, now), auditStage, resolveError(code, "workspace credential is not ready", false, status, err))
	}
	provider, ok := service.registry.Lookup(request.ProviderKind)
	if !ok {
		return service.fail(ctx, request, bindingResult(binding, now), auditStage, resolveError(ReasonCredentialInvalid, "credential provider is not installed", false, 400, nil))
	}
	plaintext, err := service.sealer.Open(BindingSealScope{
		WorkspaceID: request.WorkspaceID, BindingID: binding.ID, CredentialVersion: binding.CredentialVersion,
	}, binding.SealedSecret)
	if err != nil {
		return service.fail(ctx, request, bindingResult(binding, now), auditStage, resolveError(ReasonCredentialNotReady, "workspace credential cannot be opened", true, 503, err))
	}
	defer clear(plaintext)
	mutation, providerErr := provider.Materialize(ctx, binding, plaintext, request)
	if providerErr != nil {
		return service.fail(ctx, request, bindingResult(binding, now), auditStage, resolveError(ReasonProviderDenied, "provider credential could not be materialized", true, 502, providerErr))
	}
	if err := mutation.Validate(provider); err != nil {
		return service.fail(ctx, request, bindingResult(binding, now), auditStage, resolveError(ReasonProviderDenied, "provider returned an invalid credential mutation", false, 502, err))
	}
	result := bindingResult(binding, now)
	if resolveErr := service.finish(ctx, request, result, auditStage, nil); resolveErr != nil {
		return HeaderMutation{}, result, resolveErr
	}
	return mutation, result, nil
}

func (service *Service) fail(ctx context.Context, request UseRequest, result ResolveResult, auditStage string, err *ResolveError) (HeaderMutation, ResolveResult, error) {
	return HeaderMutation{}, result, service.finish(ctx, request, result, auditStage, err)
}

func validateBindingForUse(binding Binding, request UseRequest, now time.Time) error {
	if binding.WorkspaceID != request.WorkspaceID || binding.Kind != request.ProviderKind || binding.ID != request.BindingID {
		return errors.New("binding identity does not match the operation")
	}
	if binding.CredentialVersion < 1 || binding.AuthorityVersion < 1 || binding.AuthorityVersion != request.AuthorityVersion {
		return errors.New("binding version is stale")
	}
	switch binding.Status {
	case StatusActive:
	case StatusReauthRequired, StatusRevoked, StatusDisabled:
		return errors.New("binding status is not active")
	default:
		return errors.New("binding status is unknown")
	}
	if !binding.AccessExpiresAt.IsZero() && !binding.AccessExpiresAt.After(now.Add(time.Second)) {
		return errors.New("binding access credential is expired")
	}
	if binding.RefreshExpiresAt != nil && !binding.RefreshExpiresAt.IsZero() && !binding.RefreshExpiresAt.After(now) {
		return errors.New("binding refresh authority is expired")
	}
	if len(binding.SealedSecret) == 0 {
		return errors.New("binding has no sealed secret")
	}
	return nil
}

func claimsMatchRequest(claims PlaceholderClaims, request UseRequest, now time.Time) bool {
	return claims.WorkspaceID == request.WorkspaceID && claims.SessionID == request.SessionID &&
		claims.ActorID == request.ActorID && claims.EnvironmentID == request.EnvironmentID &&
		claims.RunID == request.RunID && claims.RunAttemptID == request.RunAttemptID &&
		claims.RunAttemptGeneration == request.RunAttemptGeneration && claims.ExecutionID == request.ExecutionID &&
		claims.OperationID == request.OperationID && claims.SandboxID == request.SandboxID &&
		claims.TargetGeneration == request.TargetGeneration && claims.ProviderKind == request.ProviderKind &&
		claims.BindingID == request.BindingID && claims.AuthorityVersion == request.AuthorityVersion &&
		claims.PolicySHA256 == request.PolicySHA256 && claims.ExpiresAt.After(now)
}

func bindingResult(binding Binding, now time.Time) ResolveResult {
	result := ResolveResult{
		ProviderKind: binding.Kind, Binding: binding.Metadata(), AuthorityVersion: binding.AuthorityVersion,
		CredentialVersion: binding.CredentialVersion, ResolvedAt: now,
	}
	if !binding.AccessExpiresAt.IsZero() {
		value := binding.AccessExpiresAt.UTC()
		result.AccessExpiresAt = &value
	}
	return result
}

func (service *Service) finish(ctx context.Context, request UseRequest, result ResolveResult, auditStage string, resolveErr *ResolveError) *ResolveError {
	if service == nil || service.audit == nil {
		return resolveErr
	}
	record := CredentialUseAudit{
		At: service.now().UTC(), Stage: auditStage, CapabilityID: request.CapabilityID,
		WorkspaceID: request.WorkspaceID, SessionID: request.SessionID,
		ActorID: request.ActorID, EnvironmentID: request.EnvironmentID,
		RunID: request.RunID, RunAttemptID: request.RunAttemptID, RunAttemptGeneration: request.RunAttemptGeneration,
		ExecutionID: request.ExecutionID, OperationID: request.OperationID, SandboxID: request.SandboxID,
		TargetGeneration: request.TargetGeneration, ProviderKind: request.ProviderKind, BindingID: request.BindingID,
		AuthorityVersion: request.AuthorityVersion, TAEPSM: request.TAEPSM,
		Host: request.Host, Path: request.Path, Method: request.Method,
		Decision: "allow", ReasonCode: "allowed",
	}
	if resolveErr != nil {
		record.Decision = "deny"
		record.ReasonCode = resolveErr.Code
	} else {
		record.CredentialVersion = result.CredentialVersion
	}
	if err := service.audit.RecordCredentialUse(ctx, record); err != nil {
		return resolveError(ReasonCoreUnavailable, "credential use audit is unavailable", true, 503, err)
	}
	return resolveErr
}

type noopAuditSink struct{}

func (noopAuditSink) RecordCredentialUse(context.Context, CredentialUseAudit) error { return nil }
