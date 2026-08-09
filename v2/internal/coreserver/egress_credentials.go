package coreserver

import (
	"context"
	"crypto/rand"
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
)

// EgressCredentialService is the v2 Core-owned credential resolver. It is a
// library used by the Core HTTP handler and egress-authorizer contract; it is
// not a separately deployed proxy process. Platform writes bindings through
// WorkspaceCredentialHandler, while this service performs the final
// operation-bound materialization.
type EgressCredentialService struct {
	resolver *corecredentials.Service
	store    *coredb.StateStore
	now      func() time.Time
}

type EgressCredentialServiceConfig struct {
	Store        *coredb.StateStore
	Registry     *corecredentials.ProviderRegistry
	Sealer       *corecredentials.Keyring
	Placeholders corecredentials.PlaceholderVerifier
	Now          func() time.Time
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
	return corecontract.ResolveEgressCredentialAuthorityResponse{
		ProviderKind: request.ProviderKind, BindingID: ref.BindingID, AuthorityVersion: ref.AuthorityVersion,
		CredentialVersion: ref.CredentialVersion, PolicySHA256: request.PolicySHA256, AuthorizedAt: service.now().UTC(),
	}, nil
}

func NewEgressCredentialService(config EgressCredentialServiceConfig) (*EgressCredentialService, error) {
	if config.Store == nil || config.Registry == nil || config.Sealer == nil || config.Placeholders == nil || config.Now == nil {
		return nil, errors.New("v2 egress credential store, provider registry, sealer, placeholder verifier, and clock are required")
	}
	resolver, err := corecredentials.NewService(corecredentials.ServiceConfig{
		Registry: config.Registry, Bindings: config.Store, LiveAuthorizer: config.Store,
		Placeholders: config.Placeholders, Sealer: config.Sealer, Audit: workspaceCredentialAuditSink{store: config.Store}, Now: config.Now,
	})
	if err != nil {
		return nil, err
	}
	return &EgressCredentialService{resolver: resolver, store: config.Store, now: config.Now}, nil
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

type workspaceCredentialAuditSink struct{ store *coredb.StateStore }

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
