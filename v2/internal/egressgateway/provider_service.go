package egressgateway

import (
	"context"
	"errors"
	"net"
	"path"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

type ProviderEgressPolicy interface {
	Allows(providerKind, host, requestPath, method, policySHA256 string) bool
}

// ProviderPolicyFunc is useful for policies whose path language is richer
// than a segment prefix (for example a versioned path-template pack). It keeps
// the egress gateway provider-neutral while allowing a pack to perform exact
// matching instead of widening a prefix accidentally.
type ProviderPolicyFunc func(providerKind, host, requestPath, method, policySHA256 string) bool

func (policy ProviderPolicyFunc) Allows(providerKind, host, requestPath, method, policySHA256 string) bool {
	return policy != nil && policy(providerKind, host, requestPath, method, policySHA256)
}

type ProviderServiceConfig struct {
	Placeholders *egresscapability.Verifier
	ZTI          ZTIVerifier
	Resolver     CredentialInjectionResolver
	Policy       ProviderEgressPolicy
	Audit        AuditSink
	AllowedPSM   string
	Now          func() time.Time
}

// ProviderService is the provider-neutral TAE Policy Webhook decision path.
// It owns network policy and asks the v2 Core workspace credential service for the
// final one-hop header mutation. Core is the only component that can open a
// workspace credential.
type ProviderService struct {
	placeholders *egresscapability.Verifier
	zti          ZTIVerifier
	resolver     CredentialInjectionResolver
	policy       ProviderEgressPolicy
	audit        AuditSink
	allowedPSM   string
	now          func() time.Time
}

func NewProviderService(config ProviderServiceConfig) (*ProviderService, error) {
	if config.Placeholders == nil || config.ZTI == nil || config.Resolver == nil ||
		config.Policy == nil || config.Audit == nil || config.Now == nil {
		return nil, errors.New("provider egress verifier, ZTI, credential resolver, policy, audit, and clock are required")
	}
	if !validBoundedText(config.AllowedPSM, 256) {
		return nil, errors.New("provider egress allowed TAE PSM is invalid")
	}
	return &ProviderService{
		placeholders: config.Placeholders, zti: config.ZTI, resolver: config.Resolver,
		policy: config.Policy, audit: config.Audit, allowedPSM: config.AllowedPSM, now: config.Now,
	}, nil
}

func (service *ProviderService) Authorize(ctx context.Context, request OriginalRequest, outerZTIToken string) Decision {
	if service == nil || service.placeholders == nil || service.zti == nil || service.resolver == nil ||
		service.policy == nil || service.audit == nil || service.now == nil || ctx == nil {
		return Decision{ReasonCode: "service_unavailable"}
	}
	now := service.now().UTC()
	baseAudit := AuditRecord{At: now, Host: request.Host, Path: request.Path, Method: request.Method, Decision: "deny"}
	if err := validateProviderOriginalRequest(request); err != nil {
		return service.deny(ctx, baseAudit, "invalid_request")
	}
	bodyZTI, ok := exactHeader(request.Headers, ZTIHeader)
	if !ok || !validBoundedText(outerZTIToken, 32*1024) || !equalSecret(outerZTIToken, bodyZTI) {
		return service.deny(ctx, baseAudit, "zti_mismatch")
	}
	principal, err := service.zti.VerifyZTI(ctx, outerZTIToken)
	if err != nil || principal.PSM != service.allowedPSM || !validBoundedText(principal.PSM, 256) {
		return service.deny(ctx, baseAudit, "zti_denied")
	}
	baseAudit.PSM = principal.PSM
	authorization, ok := exactHeader(request.Headers, AuthorizationHeader)
	if !ok || !strings.HasPrefix(authorization, "Bearer ") || strings.Count(authorization, " ") != 1 {
		return service.deny(ctx, baseAudit, "placeholder_missing")
	}
	placeholder := strings.TrimPrefix(authorization, "Bearer ")
	if !egresscapability.IsPlaceholderToken(placeholder) {
		return service.authorizeProcessEnvironment(ctx, request, principal, baseAudit, authorization, now)
	}
	claims, err := service.placeholders.Verify(placeholder, now)
	if err != nil || claims.ProviderKind == "" || claims.BindingID == "" || claims.AuthorityVersion < 1 {
		return service.deny(ctx, baseAudit, "placeholder_invalid")
	}
	baseAudit = bindProviderClaimsAudit(baseAudit, claims)
	if !service.policy.Allows(claims.ProviderKind, request.Host, request.Path, request.Method, claims.PolicySHA256) {
		return service.deny(ctx, baseAudit, "provider_policy_denied")
	}
	// ZTI authenticates the TAE hop only. Core deliberately rejects this
	// header, so strip every case variant before sending the use request while
	// retaining it in the original request used for policy/audit decisions.
	credentialHeaders := cloneOriginalHeaders(request.Headers)
	for name := range credentialHeaders {
		if strings.EqualFold(name, ZTIHeader) {
			delete(credentialHeaders, name)
		}
	}
	mutation, resolved, err := service.resolver.ResolveInjection(ctx, corecredentials.UseRequest{
		Placeholder: placeholder, WorkspaceID: claims.WorkspaceID, SessionID: claims.SessionID,
		ActorID: claims.ActorID, EnvironmentID: claims.EnvironmentID, RunID: claims.RunID,
		RunAttemptID: claims.RunAttemptID, RunAttemptGeneration: claims.RunAttemptGeneration,
		ExecutionID: claims.ExecutionID, OperationID: claims.OperationID, SandboxID: claims.SandboxID,
		TargetGeneration: claims.TargetGeneration, ProviderKind: claims.ProviderKind,
		BindingID: claims.BindingID, AuthorityVersion: claims.AuthorityVersion,
		PolicySHA256: claims.PolicySHA256, TAEPSM: principal.PSM,
		Host: request.Host, Path: request.Path,
		Method: request.Method, Headers: credentialHeaders,
	})
	if err != nil || corecredentials.ValidateClosedHeaderMutation(mutation) != nil {
		return service.deny(ctx, baseAudit, "core_credential_denied")
	}
	if resolved.ProviderKind != claims.ProviderKind || resolved.Binding.ID != claims.BindingID ||
		resolved.AuthorityVersion != claims.AuthorityVersion || resolved.CredentialVersion < 1 {
		return service.deny(ctx, baseAudit, "core_credential_denied")
	}
	allowAudit := baseAudit
	allowAudit.Decision = "allow"
	allowAudit.ReasonCode = "allowed"
	allowAudit.CredentialVersion = resolved.CredentialVersion
	if err := service.audit.RecordEgressDecision(ctx, allowAudit); err != nil {
		return Decision{ReasonCode: "audit_unavailable"}
	}
	return Decision{Allow: true, ReasonCode: "allowed", Headers: mutation.Headers}
}

func (service *ProviderService) authorizeProcessEnvironment(
	ctx context.Context,
	request OriginalRequest,
	principal ZTIPrincipal,
	baseAudit AuditRecord,
	authorization string,
	now time.Time,
) Decision {
	proof, ok := exactHeader(request.Headers, managedcredential.LarkAgentTraceHeader)
	if !ok || !egresscapability.IsProcessEnvironmentProof(proof) {
		return service.deny(ctx, baseAudit, "process_environment_proof_missing")
	}
	claims, err := service.placeholders.VerifyProcessEnvironment(proof, now)
	if err != nil {
		return service.deny(ctx, baseAudit, "process_environment_proof_invalid")
	}
	baseAudit = bindProcessEnvironmentClaimsAudit(baseAudit, claims)
	if !service.policy.Allows(claims.ProviderKind, request.Host, request.Path, request.Method, claims.PolicySHA256) {
		return service.deny(ctx, baseAudit, "provider_policy_denied")
	}
	credentialHeaders := cloneOriginalHeaders(request.Headers)
	for name := range credentialHeaders {
		if strings.EqualFold(name, ZTIHeader) {
			delete(credentialHeaders, name)
		}
	}
	mutation, resolved, err := service.resolver.AuthorizeProcessEnvironmentEgress(ctx, corecontract.AuthorizeProcessEnvironmentEgressRequest{
		ProcessProof: proof,
		Operation: corecontract.EgressCredentialOperation{
			WorkspaceID: claims.WorkspaceID, SessionID: claims.SessionID, ActorID: claims.ActorID,
			EnvironmentID: claims.EnvironmentID, RunID: claims.RunID, RunAttemptID: claims.RunAttemptID,
			RunAttemptGeneration: claims.RunAttemptGeneration, ExecutionID: claims.ExecutionID,
			OperationID: claims.OperationID, SandboxID: claims.SandboxID, TargetGeneration: claims.TargetGeneration,
		},
		ProviderKind: claims.ProviderKind, BindingID: claims.BindingID,
		AuthorityVersion: claims.AuthorityVersion, CredentialVersion: claims.CredentialVersion,
		PolicySHA256: claims.PolicySHA256, TAEPSM: principal.PSM,
		Host: request.Host, Path: request.Path, Method: request.Method, Headers: credentialHeaders,
	})
	if err != nil || corecredentials.ValidateClosedHeaderMutation(mutation) != nil || len(mutation.Headers) != 2 {
		return service.deny(ctx, baseAudit, "core_process_environment_denied")
	}
	mutatedAuthorization, authorizationOK := exactHeader(mutation.Headers, AuthorizationHeader)
	mutatedTrace, traceOK := exactHeader(mutation.Headers, managedcredential.LarkAgentTraceHeader)
	if !authorizationOK || !traceOK || !equalSecret(mutatedAuthorization, authorization) ||
		mutatedTrace != managedcredential.LarkSanitizedAgentTrace || resolved.ProviderKind != claims.ProviderKind ||
		resolved.Binding.ID != claims.BindingID || resolved.AuthorityVersion != claims.AuthorityVersion ||
		resolved.CredentialVersion != claims.CredentialVersion {
		return service.deny(ctx, baseAudit, "core_process_environment_denied")
	}
	allowAudit := baseAudit
	allowAudit.Decision = "allow"
	allowAudit.ReasonCode = "allowed"
	allowAudit.CredentialVersion = resolved.CredentialVersion
	if err := service.audit.RecordEgressDecision(ctx, allowAudit); err != nil {
		return Decision{ReasonCode: "audit_unavailable"}
	}
	return Decision{Allow: true, ReasonCode: "allowed", Headers: mutation.Headers}
}

func (service *ProviderService) deny(ctx context.Context, record AuditRecord, reason string) Decision {
	record.Decision = "deny"
	record.ReasonCode = reason
	if err := service.audit.RecordEgressDecision(ctx, record); err != nil {
		reason = "audit_unavailable"
	}
	return Decision{ReasonCode: reason}
}

func bindProviderClaimsAudit(record AuditRecord, claims egresscapability.Claims) AuditRecord {
	record = bindClaimsAudit(record, claims)
	record.ProviderKind = claims.ProviderKind
	record.BindingID = claims.BindingID
	record.AuthorityVersion = claims.AuthorityVersion
	return record
}

func bindProcessEnvironmentClaimsAudit(record AuditRecord, claims egresscapability.ProcessEnvironmentClaims) AuditRecord {
	record.CapabilityID = claims.CapabilityID
	record.WorkspaceID = claims.WorkspaceID
	record.SessionID = claims.SessionID
	record.ActorID = claims.ActorID
	record.EnvironmentID = claims.EnvironmentID
	record.RunID = claims.RunID
	record.RunAttemptID = claims.RunAttemptID
	record.RunAttemptGeneration = claims.RunAttemptGeneration
	record.ExecutionID = claims.ExecutionID
	record.OperationID = claims.OperationID
	record.SandboxID = claims.SandboxID
	record.TargetGeneration = claims.TargetGeneration
	record.ProviderKind = claims.ProviderKind
	record.BindingID = claims.BindingID
	record.AuthorityVersion = claims.AuthorityVersion
	return record
}

func validateProviderOriginalRequest(request OriginalRequest) error {
	if !validBoundedText(request.Host, 512) || request.Host != strings.ToLower(request.Host) ||
		net.ParseIP(request.Host) != nil || strings.ContainsAny(request.Host, "/:@[]") ||
		strings.HasPrefix(request.Host, ".") || strings.HasSuffix(request.Host, ".") || strings.Contains(request.Host, "..") {
		return errors.New("host is not a canonical provider DNS name")
	}
	if !validBoundedText(request.Method, 16) || strings.ToUpper(request.Method) != request.Method {
		return errors.New("method is not canonical uppercase")
	}
	if !validBoundedText(request.Path, 4096) || !strings.HasPrefix(request.Path, "/") ||
		strings.ContainsAny(request.Path, "\\%?#\x00") || strings.Contains(request.Path, "//") ||
		path.Clean(request.Path) != request.Path || (len(request.Path) > 1 && strings.HasSuffix(request.Path, "/")) {
		return errors.New("path is not canonical")
	}
	if len(request.Headers) < 1 || len(request.Headers) > 128 {
		return errors.New("headers are missing or excessive")
	}
	names := make(map[string]struct{}, len(request.Headers))
	for name, value := range request.Headers {
		if !validHTTPHeaderName(name) || !validBoundedText(value, 32*1024) {
			return errors.New("header is invalid")
		}
		canonical := strings.ToLower(name)
		if _, duplicate := names[canonical]; duplicate || forbiddenOriginalHeader(canonical) {
			return errors.New("header is ambiguous or unsafe")
		}
		names[canonical] = struct{}{}
	}
	return nil
}

func cloneOriginalHeaders(headers map[string]string) map[string]string {
	result := make(map[string]string, len(headers))
	for name, value := range headers {
		result[name] = value
	}
	return result
}
