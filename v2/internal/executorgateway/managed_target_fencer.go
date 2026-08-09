package executorgateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/sandboxclient"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

type managedTargetDeleteClient interface {
	Delete(context.Context, sandboxcontract.DeleteSandboxRequest, sandboxclient.TokenRequest) (sandboxcontract.SandboxResponse, error)
}

// GatewayManagedTargetFencer deletes exactly the Core-frozen managed target
// generation after an ambiguous dispatch. It never resolves or creates a
// replacement generation.
type GatewayManagedTargetFencer struct {
	client      managedTargetDeleteClient
	idGenerator IDGenerator
}

func NewGatewayManagedTargetFencer(client managedTargetDeleteClient, idGenerator IDGenerator) (*GatewayManagedTargetFencer, error) {
	if client == nil || idGenerator == nil {
		return nil, errors.New("managed target fencer client and identity generator are required")
	}
	return &GatewayManagedTargetFencer{client: client, idGenerator: idGenerator}, nil
}

func NewDefaultGatewayManagedTargetFencer(client managedTargetDeleteClient) (*GatewayManagedTargetFencer, error) {
	return NewGatewayManagedTargetFencer(client, newRandomUUID)
}

func (fencer *GatewayManagedTargetFencer) FenceManagedTarget(
	ctx context.Context,
	principal ExecutorMCPPrincipal,
	target executionbackend.Target,
	reason string,
) error {
	if fencer == nil || fencer.client == nil || fencer.idGenerator == nil || ctx == nil {
		return errors.New("managed target fencer and context are required")
	}
	if err := validateExecutorMCPPrincipal(principal); err != nil {
		return err
	}
	if err := target.Validate(); err != nil {
		return err
	}
	if target.Kind != executionbackend.KindTAE {
		return errors.New("managed target fencer requires a TAE target")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 1024 || !utf8.ValidString(reason) || strings.ContainsRune(reason, '\x00') {
		return errors.New("managed target fence reason is invalid")
	}
	requestID, err := fencer.idGenerator()
	if err != nil {
		return fmt.Errorf("allocate managed target fence request ID: %w", err)
	}
	if err := validateRegistryIdentity("managed target fence request ID", requestID); err != nil {
		return err
	}
	session := sandboxcontract.SessionIdentity{
		WorkspaceID: principal.WorkspaceID, SessionID: principal.SessionID, EnvironmentID: target.EnvironmentID,
	}
	ref := sandboxcontract.SandboxRef{SandboxID: target.ID, TargetGeneration: target.Generation}
	response, err := fencer.client.Delete(ctx, sandboxcontract.DeleteSandboxRequest{
		Profile: sandboxcontract.ProfileV1, RequestID: requestID, Session: session, Ref: ref, Reason: reason,
	}, sandboxclient.TokenRequest{
		Action: sandboxclient.ActionDelete, Session: session, Ref: ref,
		RunID: principal.Run.RunID, RunAttemptID: principal.Run.RunAttemptID,
		RunAttemptGeneration: principal.Run.RunAttemptGeneration, HolderID: principal.Run.HolderID,
	})
	if err != nil {
		return err
	}
	if response.Sandbox.Ref != ref || (response.Sandbox.State != sandboxcontract.SandboxDeleting && response.Sandbox.State != sandboxcontract.SandboxDeleted) {
		return errors.New("sandbox-gateway fenced a different generation or returned a non-deleting state")
	}
	return nil
}

var _ ManagedTargetFencer = (*GatewayManagedTargetFencer)(nil)
