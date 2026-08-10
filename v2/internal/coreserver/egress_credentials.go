package coreserver

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"path"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

// EgressCredentialService is the v2 Core-owned credential resolver. It is a
// library used by the Core HTTP handler and egress-authorizer contract; it is
// not a separately deployed proxy process. Platform writes bindings through
// WorkspaceCredentialHandler, while this service performs the final
// operation-bound materialization.
type EgressCredentialService struct {
	resolver                 *corecredentials.Service
	store                    EgressCredentialStore
	processProofs            *egresscapability.Verifier
	processEnvironmentTAEPSM string
	now                      func() time.Time
}

// EgressCredentialStore is the narrow Core persistence surface required by
// credential resolution. StateStore implements it in production; keeping the
// boundary explicit lets the sensitive direct-delivery path be exercised
// without a networked database in unit tests.
type EgressCredentialStore interface {
	corecredentials.BindingStore
	corecredentials.LiveAuthorizer
	ResolveCredentialAuthority(context.Context, corecredentials.AuthorityRequest) (corecredentials.BindingReference, error)
	RecordWorkspaceCredentialUseEvent(context.Context, coredb.WorkspaceCredentialUseEvent) error
}

type EgressCredentialServiceConfig struct {
	Store                    EgressCredentialStore
	Registry                 *corecredentials.ProviderRegistry
	Sealer                   *corecredentials.Keyring
	Placeholders             corecredentials.PlaceholderVerifier
	ProcessProofs            *egresscapability.Verifier
	ProcessEnvironmentTAEPSM string
	Now                      func() time.Time
}

func (service *EgressCredentialService) ResolveAuthority(ctx context.Context, request corecontract.ResolveEgressCredentialAuthorityRequest) (corecontract.ResolveEgressCredentialAuthorityResponse, error) {
	if service == nil || service.store == nil || service.now == nil || ctx == nil {
		return corecontract.ResolveEgressCredentialAuthorityResponse{}, errors.New("v2 egress credential authority service is unavailable")
	}
	operation := request.Operation
	ref, err := service.store.ResolveCredentialAuthority(ctx, corecredentials.AuthorityRequest{
		WorkspaceID: operation.WorkspaceID, SessionID: operation.SessionID, ActorID: operation.ActorID,
		EnvironmentID: operation.EnvironmentID, RunID: operation.RunID, RunAttemptID: operation.RunAttemptID,
		RunAttemptGeneration: operation.RunAttemptGeneration, ExecutionID: operation.ExecutionID,
		OperationID: operation.OperationID, SandboxID: operation.SandboxID, TargetGeneration: operation.TargetGeneration,
		ProviderKind: request.ProviderKind, PolicySHA256: request.PolicySHA256,
	})
	if err != nil {
		return corecontract.ResolveEgressCredentialAuthorityResponse{}, err
	}
	if !managedcredential.ValidMode(ref.CredentialMode) {
		return corecontract.ResolveEgressCredentialAuthorityResponse{}, errors.New("workspace returned an invalid managed credential mode")
	}
	return corecontract.ResolveEgressCredentialAuthorityResponse{
		CredentialMode: ref.CredentialMode,
		ProviderKind:   request.ProviderKind, BindingID: ref.BindingID, AuthorityVersion: ref.AuthorityVersion,
		CredentialVersion: ref.CredentialVersion, PolicySHA256: request.PolicySHA256, AuthorizedAt: service.now().UTC(),
	}, nil
}

func NewEgressCredentialService(config EgressCredentialServiceConfig) (*EgressCredentialService, error) {
	if config.Store == nil || config.Registry == nil || config.Sealer == nil || config.Placeholders == nil ||
		config.ProcessProofs == nil || config.Now == nil || config.ProcessEnvironmentTAEPSM == "" ||
		len(config.ProcessEnvironmentTAEPSM) > 256 || strings.TrimSpace(config.ProcessEnvironmentTAEPSM) != config.ProcessEnvironmentTAEPSM ||
		strings.ContainsAny(config.ProcessEnvironmentTAEPSM, "\x00\r\n") {
		return nil, errors.New("v2 egress credential store, provider registry, sealer, both proof verifiers, TAE PSM, and clock are required")
	}
	resolver, err := corecredentials.NewService(corecredentials.ServiceConfig{
		Registry: config.Registry, Bindings: config.Store, LiveAuthorizer: config.Store,
		Placeholders: config.Placeholders, Sealer: config.Sealer, Audit: workspaceCredentialAuditSink{store: config.Store}, Now: config.Now,
	})
	if err != nil {
		return nil, err
	}
	return &EgressCredentialService{
		resolver: resolver, store: config.Store, processProofs: config.ProcessProofs,
		processEnvironmentTAEPSM: config.ProcessEnvironmentTAEPSM,
		now:                      config.Now,
	}, nil
}

// ResolveExecutionLarkCredential performs direct, operation-scoped Lark token
// delivery for process_env mode. The endpoint caller is authenticated
// separately as executor-gateway; this method still checks the live Core
// operation twice (default binding selection followed by exact versioned use)
// so revocation or rotation between the reads fails closed.
func (service *EgressCredentialService) ResolveExecutionLarkCredential(
	ctx context.Context,
	request corecontract.ResolveExecutionLarkCredentialRequest,
) (corecontract.ResolveExecutionLarkCredentialResponse, error) {
	if service == nil || service.resolver == nil || service.store == nil || service.now == nil || ctx == nil ||
		service.processEnvironmentTAEPSM == "" {
		return corecontract.ResolveExecutionLarkCredentialResponse{}, errors.New("v2 process environment credential resolver is unavailable")
	}
	policySHA256 := larkegresspolicy.SHA256Hex()
	if request.ToolName != "shell" || request.Executable != "lark-cli" || request.TAEPSM != service.processEnvironmentTAEPSM ||
		request.PolicySHA256 != policySHA256 {
		return corecontract.ResolveExecutionLarkCredentialResponse{}, &coredb.StateError{
			Code: coredb.ErrorInvalidArgument, Operation: "ResolveExecutionLarkCredential",
			Resource: "credential_use", ResourceID: request.Operation.OperationID,
			Message: "process environment credential request is outside the managed Lark contract",
		}
	}
	operation := request.Operation
	authorityRequest := corecontract.ResolveEgressCredentialAuthorityRequest{
		Operation: operation, ProviderKind: "lark", PolicySHA256: policySHA256,
	}
	authority, err := service.ResolveAuthority(ctx, authorityRequest)
	if err != nil {
		return corecontract.ResolveExecutionLarkCredentialResponse{}, err
	}
	base := corecontract.ResolveExecutionLarkCredentialResponse{
		Configured: false, CredentialMode: authority.CredentialMode,
		ProviderKind: "lark", PolicySHA256: policySHA256,
		TAEPSM: request.TAEPSM, ResolvedAt: service.now().UTC(),
	}
	if authority.CredentialMode != managedcredential.ModeProcessEnv ||
		request.BindingID != authority.BindingID || request.AuthorityVersion != authority.AuthorityVersion ||
		request.CredentialVersion != authority.CredentialVersion {
		return corecontract.ResolveExecutionLarkCredentialResponse{}, &coredb.StateError{
			Code: coredb.ErrorForbidden, Operation: "ResolveExecutionLarkCredential",
			Resource: "credential_use", ResourceID: request.Operation.OperationID,
			Message: "workspace credential delivery mode or version changed before process launch",
		}
	}
	if authority.BindingID == "" {
		return base, nil
	}
	auditID, err := newCredentialEventID()
	if err != nil {
		return corecontract.ResolveExecutionLarkCredentialResponse{}, err
	}
	mutation, result, err := service.resolver.ResolveTrustedInjection(ctx, corecredentials.UseRequest{
		WorkspaceID: operation.WorkspaceID, SessionID: operation.SessionID, ActorID: operation.ActorID,
		EnvironmentID: operation.EnvironmentID, RunID: operation.RunID, RunAttemptID: operation.RunAttemptID,
		RunAttemptGeneration: operation.RunAttemptGeneration, ExecutionID: operation.ExecutionID,
		OperationID: operation.OperationID, SandboxID: operation.SandboxID, TargetGeneration: operation.TargetGeneration,
		ProviderKind: "lark", BindingID: authority.BindingID, AuthorityVersion: authority.AuthorityVersion,
		ExpectedCredentialVersion: authority.CredentialVersion,
		CredentialMode:            managedcredential.ModeProcessEnv,
		PolicySHA256:              policySHA256, TAEPSM: request.TAEPSM,
		Host: larkegresspolicy.OpenAPIHost, Path: "/", Method: "PROCESS_ENV",
	}, auditID, "process_env")
	if err != nil {
		return corecontract.ResolveExecutionLarkCredentialResponse{}, err
	}
	token, err := exactBearerToken(mutation.Headers)
	if err != nil {
		return corecontract.ResolveExecutionLarkCredentialResponse{}, errors.New("v2 Lark provider returned an invalid process credential")
	}
	return corecontract.ResolveExecutionLarkCredentialResponse{
		Configured: true, AccessToken: token, CredentialMode: managedcredential.ModeProcessEnv,
		ProviderKind: result.ProviderKind,
		BindingID:    result.Binding.ID, AuthorityVersion: result.AuthorityVersion,
		CredentialVersion: result.CredentialVersion, PolicySHA256: policySHA256,
		TAEPSM: request.TAEPSM, ResolvedAt: result.ResolvedAt.UTC(),
		AccessExpiresAt: copyCredentialTime(result.AccessExpiresAt),
	}, nil
}

func (service *EgressCredentialService) AuthorizeProcessEnvironmentEgress(
	ctx context.Context,
	request corecontract.AuthorizeProcessEnvironmentEgressRequest,
) (corecontract.ResolveEgressCredentialResponse, error) {
	if service == nil || service.resolver == nil || service.processProofs == nil || service.now == nil || ctx == nil ||
		service.processEnvironmentTAEPSM == "" {
		return corecontract.ResolveEgressCredentialResponse{}, errors.New("v2 process environment egress resolver is unavailable")
	}
	now := service.now().UTC()
	claims, err := service.processProofs.VerifyProcessEnvironment(request.ProcessProof, now)
	if err != nil || !processEnvironmentClaimsMatchRequest(claims, request) {
		return corecontract.ResolveEgressCredentialResponse{}, processEnvironmentDenied(request.Operation.OperationID, "process environment egress proof is invalid")
	}
	if request.TAEPSM != service.processEnvironmentTAEPSM || request.ProviderKind != "lark" ||
		request.PolicySHA256 != larkegresspolicy.SHA256Hex() ||
		!larkegresspolicy.Allows(request.Host, request.Path, request.Method) {
		return corecontract.ResolveEgressCredentialResponse{}, &coredb.StateError{
			Code: coredb.ErrorForbidden, Operation: "AuthorizeProcessEnvironmentEgress",
			Resource: "credential_use", ResourceID: request.Operation.OperationID,
			Message: "process environment egress request is outside the managed Lark policy",
		}
	}
	authorization, ok := exactCredentialHeader(request.Headers, "Authorization")
	proofHeader, proofOK := exactCredentialHeader(request.Headers, managedcredential.LarkAgentTraceHeader)
	if !ok || !proofOK || proofHeader != request.ProcessProof || !strings.HasPrefix(authorization, "Bearer ") ||
		strings.Count(authorization, " ") != 1 {
		return corecontract.ResolveEgressCredentialResponse{}, &coredb.StateError{
			Code: coredb.ErrorForbidden, Operation: "AuthorizeProcessEnvironmentEgress",
			Resource: "credential_use", ResourceID: request.Operation.OperationID,
			Message: "process environment egress proof or bearer is missing",
		}
	}
	operation := request.Operation
	mutation, result, err := service.resolver.ResolveTrustedInjection(ctx, corecredentials.UseRequest{
		WorkspaceID: operation.WorkspaceID, SessionID: operation.SessionID, ActorID: operation.ActorID,
		EnvironmentID: operation.EnvironmentID, RunID: operation.RunID, RunAttemptID: operation.RunAttemptID,
		RunAttemptGeneration: operation.RunAttemptGeneration, ExecutionID: operation.ExecutionID,
		OperationID: operation.OperationID, SandboxID: operation.SandboxID, TargetGeneration: operation.TargetGeneration,
		ProviderKind: request.ProviderKind, BindingID: request.BindingID, AuthorityVersion: request.AuthorityVersion,
		ExpectedCredentialVersion: request.CredentialVersion, CredentialMode: managedcredential.ModeProcessEnv,
		PolicySHA256: request.PolicySHA256, TAEPSM: request.TAEPSM,
		Host: request.Host, Path: request.Path, Method: request.Method,
	}, claims.CapabilityID, "egress")
	if err != nil {
		return corecontract.ResolveEgressCredentialResponse{}, err
	}
	expectedAuthorization, ok := exactCredentialHeader(mutation.Headers, "Authorization")
	if !ok || !equalCredentialSecret(expectedAuthorization, authorization) || result.CredentialVersion != request.CredentialVersion {
		return corecontract.ResolveEgressCredentialResponse{}, processEnvironmentDenied(request.Operation.OperationID, "process environment bearer no longer matches the workspace binding")
	}
	return corecontract.ResolveEgressCredentialResponse{
		Headers: map[string]string{
			"Authorization":                        authorization,
			managedcredential.LarkAgentTraceHeader: managedcredential.LarkSanitizedAgentTrace,
		},
		ProviderKind: result.ProviderKind, BindingID: result.Binding.ID,
		AuthorityVersion: result.AuthorityVersion, CredentialVersion: result.CredentialVersion,
		ResolvedAt: result.ResolvedAt.UTC(), AccessExpiresAt: copyCredentialTime(result.AccessExpiresAt),
	}, nil
}

func processEnvironmentDenied(operationID, message string) error {
	return &coredb.StateError{
		Code: coredb.ErrorForbidden, Operation: "AuthorizeProcessEnvironmentEgress",
		Resource: "credential_use", ResourceID: operationID, Message: message,
	}
}

func processEnvironmentClaimsMatchRequest(
	claims egresscapability.ProcessEnvironmentClaims,
	request corecontract.AuthorizeProcessEnvironmentEgressRequest,
) bool {
	operation := request.Operation
	return claims.WorkspaceID == operation.WorkspaceID && claims.SessionID == operation.SessionID &&
		claims.ActorID == operation.ActorID && claims.EnvironmentID == operation.EnvironmentID &&
		claims.RunID == operation.RunID && claims.RunAttemptID == operation.RunAttemptID &&
		claims.RunAttemptGeneration == operation.RunAttemptGeneration && claims.ExecutionID == operation.ExecutionID &&
		claims.OperationID == operation.OperationID && claims.SandboxID == operation.SandboxID &&
		claims.TargetGeneration == operation.TargetGeneration && claims.ProviderKind == request.ProviderKind &&
		claims.BindingID == request.BindingID && claims.AuthorityVersion == request.AuthorityVersion &&
		claims.CredentialVersion == request.CredentialVersion && claims.PolicySHA256 == request.PolicySHA256
}

func exactCredentialHeader(headers map[string]string, wanted string) (string, bool) {
	var result string
	found := false
	for name, value := range headers {
		if !strings.EqualFold(name, wanted) {
			continue
		}
		if found || value == "" || len(value) > 32*1024 || strings.ContainsAny(value, "\x00\r\n") {
			return "", false
		}
		result, found = value, true
	}
	return result, found
}

func equalCredentialSecret(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func exactBearerToken(headers map[string]string) (string, error) {
	if len(headers) != 1 {
		return "", errors.New("Lark process credential mutation must contain exactly one header")
	}
	for name, value := range headers {
		if !strings.EqualFold(name, "Authorization") || !strings.HasPrefix(value, "Bearer ") {
			return "", errors.New("Lark process credential mutation is not a bearer token")
		}
		token := strings.TrimPrefix(value, "Bearer ")
		if token == "" || len(token) > 32*1024 || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\x00\r\n") {
			return "", errors.New("Lark process credential token is invalid")
		}
		return token, nil
	}
	return "", errors.New("Lark process credential mutation is empty")
}

func (service *EgressCredentialService) Resolve(ctx context.Context, request corecontract.ResolveEgressCredentialRequest) (corecontract.ResolveEgressCredentialResponse, error) {
	if service == nil || service.resolver == nil || service.now == nil || ctx == nil {
		return corecontract.ResolveEgressCredentialResponse{}, errors.New("v2 egress credential resolver is unavailable")
	}
	use := corecredentials.UseRequest{
		Placeholder: request.Placeholder,
		WorkspaceID: request.Operation.WorkspaceID, SessionID: request.Operation.SessionID,
		ActorID: request.Operation.ActorID, EnvironmentID: request.Operation.EnvironmentID,
		RunID: request.Operation.RunID, RunAttemptID: request.Operation.RunAttemptID,
		RunAttemptGeneration: request.Operation.RunAttemptGeneration, ExecutionID: request.Operation.ExecutionID,
		OperationID: request.Operation.OperationID, SandboxID: request.Operation.SandboxID,
		TargetGeneration: request.Operation.TargetGeneration, ProviderKind: request.ProviderKind,
		BindingID: request.BindingID, AuthorityVersion: request.AuthorityVersion,
		PolicySHA256: request.PolicySHA256, TAEPSM: request.TAEPSM,
		Host: request.Host, Path: request.Path, Method: request.Method,
		Headers: cloneCredentialHeaders(request.Headers),
	}
	mutation, result, err := service.resolver.ResolveInjection(ctx, use)
	if err != nil {
		return corecontract.ResolveEgressCredentialResponse{}, err
	}
	if result.ProviderKind != request.ProviderKind || result.Binding.ID != request.BindingID ||
		result.AuthorityVersion != request.AuthorityVersion || result.CredentialVersion < 1 {
		return corecontract.ResolveEgressCredentialResponse{}, errors.New("v2 egress credential resolver returned an out-of-scope result")
	}
	return corecontract.ResolveEgressCredentialResponse{
		Headers: cloneCredentialHeaders(mutation.Headers), ProviderKind: result.ProviderKind,
		BindingID: result.Binding.ID, AuthorityVersion: result.AuthorityVersion,
		CredentialVersion: result.CredentialVersion, ResolvedAt: result.ResolvedAt.UTC(),
		AccessExpiresAt: copyCredentialTime(result.AccessExpiresAt),
	}, nil
}

func (service *EgressCredentialService) RecordAudit(ctx context.Context, request corecontract.RecordEgressCredentialAuditRequest) (corecontract.RecordEgressCredentialAuditResponse, error) {
	if service == nil || service.store == nil || ctx == nil {
		return corecontract.RecordEgressCredentialAuditResponse{}, errors.New("v2 egress credential audit store is unavailable")
	}
	if err := validateEgressCredentialAuditRequest(request); err != nil {
		return corecontract.RecordEgressCredentialAuditResponse{}, &coredb.StateError{
			Code: coredb.ErrorInvalidArgument, Operation: "RecordEgressCredentialAudit", Resource: "credential_use",
			ResourceID: request.EventID, Message: err.Error(),
		}
	}
	eventID := request.EventID
	if eventID == "" {
		var err error
		eventID, err = newCredentialEventID()
		if err != nil {
			return corecontract.RecordEgressCredentialAuditResponse{}, err
		}
	}
	err := service.store.RecordWorkspaceCredentialUseEvent(ctx, coredb.WorkspaceCredentialUseEvent{
		EventID: eventID, At: request.At.UTC(), Stage: "egress", CapabilityID: request.CapabilityID,
		WorkspaceID: request.Operation.WorkspaceID, ActorID: request.Operation.ActorID,
		EnvironmentID: request.Operation.EnvironmentID,
		SessionID:     request.Operation.SessionID, RunID: request.Operation.RunID,
		RunAttemptID: request.Operation.RunAttemptID, RunAttemptGeneration: request.Operation.RunAttemptGeneration,
		ExecutionID: request.Operation.ExecutionID, OperationID: request.Operation.OperationID,
		SandboxID: request.Operation.SandboxID, TargetGeneration: request.Operation.TargetGeneration,
		ProviderKind: request.ProviderKind, BindingID: request.BindingID,
		AuthorityVersion: request.AuthorityVersion, CredentialVersion: request.CredentialVersion,
		TAEPSM: request.TAEPSM,
		Host:   request.Host, Path: request.Path, Method: request.Method,
		Decision: request.Decision, ReasonCode: request.ReasonCode,
	})
	if err != nil {
		return corecontract.RecordEgressCredentialAuditResponse{}, err
	}
	return corecontract.RecordEgressCredentialAuditResponse{Recorded: true}, nil
}

// validateEgressCredentialAuditRequest is the Core-side counterpart to the
// webhook's decision checks. It prevents a compromised caller with the
// egress-authorizer workload identity from writing a misleading allow event,
// while still allowing a pre-placeholder deny to carry only partial scope.
func validateEgressCredentialAuditRequest(request corecontract.RecordEgressCredentialAuditRequest) error {
	if request.At.IsZero() || request.At.After(time.Now().UTC().Add(5*time.Second)) {
		return errors.New("credential audit timestamp is invalid")
	}
	if request.Decision != "allow" && request.Decision != "deny" {
		return errors.New("credential audit decision is invalid")
	}
	if !boundedAuditText(request.ReasonCode, 128) {
		return errors.New("credential audit reason code is invalid")
	}
	if request.CapabilityID != "" && !boundedAuditText(request.CapabilityID, 256) {
		return errors.New("credential audit capability ID is invalid")
	}
	operation := request.Operation
	for name, value := range map[string]string{
		"workspaceId": operation.WorkspaceID, "sessionId": operation.SessionID, "actorId": operation.ActorID,
		"environmentId": operation.EnvironmentID, "runId": operation.RunID, "runAttemptId": operation.RunAttemptID,
		"executionId": operation.ExecutionID, "operationId": operation.OperationID, "sandboxId": operation.SandboxID,
	} {
		if value != "" && !boundedAuditText(value, 256) {
			return fmt.Errorf("credential audit %s is invalid", name)
		}
	}
	if operation.RunAttemptGeneration < 0 || operation.TargetGeneration < 0 {
		return errors.New("credential audit generations are invalid")
	}
	if request.ProviderKind != "" && !boundedAuditText(request.ProviderKind, 128) {
		return errors.New("credential audit provider kind is invalid")
	}
	if request.BindingID != "" && !boundedAuditText(request.BindingID, 256) {
		return errors.New("credential audit binding ID is invalid")
	}
	if request.TAEPSM != "" && !boundedAuditText(request.TAEPSM, 256) {
		return errors.New("credential audit TAE PSM is invalid")
	}
	if request.AuthorityVersion < 0 || request.CredentialVersion < 0 {
		return errors.New("credential audit versions are invalid")
	}
	if (request.BindingID == "") != (request.AuthorityVersion == 0) {
		return errors.New("credential audit binding scope is partial")
	}
	if (operation.RunAttemptID == "") != (operation.RunAttemptGeneration == 0) ||
		(operation.SandboxID == "") != (operation.TargetGeneration == 0) {
		return errors.New("credential audit operation scope is partial")
	}
	if request.Host != "" || request.Path != "" || request.Method != "" {
		if request.Decision == "allow" {
			if !canonicalAuditTarget(request.Host, request.Path, request.Method) {
				return errors.New("credential audit request target is invalid")
			}
		} else if !optionalAuditText(request.Host, 512) || !optionalAuditText(request.Path, 4096) || !optionalAuditText(request.Method, 16) {
			return errors.New("credential audit deny target is invalid")
		}
	}
	if request.Decision == "allow" {
		if request.CapabilityID == "" || operation.WorkspaceID == "" || operation.SessionID == "" ||
			operation.ActorID == "" || operation.EnvironmentID == "" || operation.RunID == "" ||
			operation.RunAttemptID == "" || operation.RunAttemptGeneration < 1 || operation.ExecutionID == "" ||
			operation.OperationID == "" || operation.SandboxID == "" || operation.TargetGeneration < 1 ||
			request.ProviderKind == "" || request.BindingID == "" || request.AuthorityVersion < 1 ||
			request.CredentialVersion < 1 || request.TAEPSM == "" || !canonicalAuditTarget(request.Host, request.Path, request.Method) {
			return errors.New("allowed credential audit requires complete operation and provider scope")
		}
	}
	return nil
}

func boundedAuditText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func optionalAuditText(value string, maximum int) bool {
	return value == "" || boundedAuditText(value, maximum)
}

func canonicalAuditTarget(host, requestPath, method string) bool {
	return boundedAuditText(host, 512) && strings.ToLower(host) == host && net.ParseIP(host) == nil &&
		!strings.ContainsAny(host, "/:@[]") && !strings.HasPrefix(host, ".") && !strings.HasSuffix(host, ".") &&
		!strings.Contains(host, "..") && boundedAuditText(requestPath, 4096) && strings.HasPrefix(requestPath, "/") &&
		!strings.ContainsAny(requestPath, "\\%?#\x00") && !strings.Contains(requestPath, "//") && path.Clean(requestPath) == requestPath &&
		(len(requestPath) == 1 || !strings.HasSuffix(requestPath, "/")) && boundedAuditText(method, 16) && strings.ToUpper(method) == method
}

type workspaceCredentialAuditSink struct{ store EgressCredentialStore }

func (sink workspaceCredentialAuditSink) RecordCredentialUse(ctx context.Context, audit corecredentials.CredentialUseAudit) error {
	eventID, err := newCredentialEventID()
	if err != nil {
		return err
	}
	return sink.store.RecordWorkspaceCredentialUseEvent(ctx, coredb.WorkspaceCredentialUseEvent{
		EventID: eventID, At: audit.At.UTC(), Stage: audit.Stage, CapabilityID: audit.CapabilityID,
		WorkspaceID: audit.WorkspaceID, SessionID: audit.SessionID, ActorID: audit.ActorID,
		EnvironmentID: audit.EnvironmentID,
		RunID:         audit.RunID, RunAttemptID: audit.RunAttemptID, RunAttemptGeneration: audit.RunAttemptGeneration,
		ExecutionID: audit.ExecutionID, OperationID: audit.OperationID, SandboxID: audit.SandboxID,
		TargetGeneration: audit.TargetGeneration, ProviderKind: audit.ProviderKind, BindingID: audit.BindingID,
		AuthorityVersion: audit.AuthorityVersion, CredentialVersion: audit.CredentialVersion,
		TAEPSM: audit.TAEPSM,
		Host:   audit.Host, Path: audit.Path, Method: audit.Method, Decision: audit.Decision, ReasonCode: audit.ReasonCode,
	})
}

func newCredentialEventID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("allocate credential audit event ID: %w", err)
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], raw[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], raw[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], raw[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], raw[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], raw[10:16])
	return string(encoded), nil
}

func cloneCredentialHeaders(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func copyCredentialTime(input *time.Time) *time.Time {
	if input == nil {
		return nil
	}
	value := input.UTC()
	return &value
}
