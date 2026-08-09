package harnesspool

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/agentserver/agentserver/v2/internal/sandboxclient"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

var managedSandboxDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var managedPackIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}@v[1-9][0-9]{0,8}$`)

type ManagedSandboxLaunchSpec struct {
	EnvironmentID        string
	RuntimeProfileDigest string
	PackID               string
	PackSetDigest        string
	SkillSHA256          string
	SandboxTTL           time.Duration
	ActivityTTL          time.Duration
}

type ManagedSandboxBinding struct {
	SandboxID        string
	TargetGeneration int64
	Root             string
	ExpiresAt        time.Time
}

type ManagedSandboxLifecycle interface {
	Ensure(context.Context, ScheduledRunAttempt, ManagedSandboxLaunchSpec) (ManagedSandboxBinding, error)
	Renew(context.Context, ScheduledRunAttempt, ManagedSandboxLaunchSpec, ManagedSandboxBinding) error
	Release(context.Context, ScheduledRunAttempt, ManagedSandboxLaunchSpec, ManagedSandboxBinding) error
}

type managedSandboxGatewayClient interface {
	Ensure(context.Context, sandboxcontract.EnsureSandboxRequest, sandboxclient.TokenRequest) (sandboxcontract.EnsureSandboxResponse, error)
	RenewActivity(context.Context, sandboxcontract.RenewSandboxActivityRequest, sandboxclient.TokenRequest) (sandboxcontract.SandboxResponse, error)
	ReleaseActivity(context.Context, sandboxcontract.ReleaseSandboxActivityRequest, sandboxclient.TokenRequest) (sandboxcontract.SandboxResponse, error)
}

type GatewayManagedSandboxLifecycle struct {
	client      managedSandboxGatewayClient
	idGenerator IDGenerator
}

func NewGatewayManagedSandboxLifecycle(client managedSandboxGatewayClient, idGenerator IDGenerator) (*GatewayManagedSandboxLifecycle, error) {
	if client == nil || idGenerator == nil {
		return nil, errors.New("managed sandbox gateway client and request ID generator are required")
	}
	return &GatewayManagedSandboxLifecycle{client: client, idGenerator: idGenerator}, nil
}

func NewDefaultGatewayManagedSandboxLifecycle(client managedSandboxGatewayClient) (*GatewayManagedSandboxLifecycle, error) {
	return NewGatewayManagedSandboxLifecycle(client, newRandomUUID)
}

func (lifecycle *GatewayManagedSandboxLifecycle) Ensure(ctx context.Context, scheduled ScheduledRunAttempt, spec ManagedSandboxLaunchSpec) (ManagedSandboxBinding, error) {
	if err := validateManagedSandboxLaunch(scheduled, spec); err != nil {
		return ManagedSandboxBinding{}, err
	}
	requestID, err := lifecycle.requestID()
	if err != nil {
		return ManagedSandboxBinding{}, err
	}
	session := managedSandboxSession(scheduled, spec)
	request := sandboxcontract.EnsureSandboxRequest{
		Profile: sandboxcontract.ProfileV1, RequestID: requestID, Session: session,
		RequestedTTLSeconds:  int64(spec.SandboxTTL / time.Second),
		RuntimeProfileDigest: spec.RuntimeProfileDigest, PackSetDigest: spec.PackSetDigest,
	}
	response, err := lifecycle.client.Ensure(ctx, request, managedSandboxTokenRequest(sandboxclient.ActionEnsure, scheduled, session, sandboxcontract.SandboxRef{}))
	if err != nil {
		return ManagedSandboxBinding{}, err
	}
	if response.Sandbox.State != sandboxcontract.SandboxReady || response.Sandbox.Root == "" || response.Sandbox.ExpiresAt.IsZero() {
		return ManagedSandboxBinding{}, errors.New("sandbox-gateway ensure did not return a ready managed sandbox")
	}
	binding := ManagedSandboxBinding{
		SandboxID: response.Sandbox.Ref.SandboxID, TargetGeneration: response.Sandbox.Ref.TargetGeneration,
		Root: response.Sandbox.Root, ExpiresAt: response.Sandbox.ExpiresAt,
	}
	if err := validateManagedSandboxBinding(binding); err != nil {
		return ManagedSandboxBinding{}, err
	}
	return binding, nil
}

func (lifecycle *GatewayManagedSandboxLifecycle) Renew(ctx context.Context, scheduled ScheduledRunAttempt, spec ManagedSandboxLaunchSpec, binding ManagedSandboxBinding) error {
	if err := validateManagedSandboxLaunch(scheduled, spec); err != nil {
		return err
	}
	if err := validateManagedSandboxBinding(binding); err != nil {
		return err
	}
	requestID, err := lifecycle.requestID()
	if err != nil {
		return err
	}
	session := managedSandboxSession(scheduled, spec)
	ref := sandboxcontract.SandboxRef{SandboxID: binding.SandboxID, TargetGeneration: binding.TargetGeneration}
	request := sandboxcontract.RenewSandboxActivityRequest{
		Profile: sandboxcontract.ProfileV1, RequestID: requestID, Session: session, Ref: ref,
		RunAttemptID:         scheduled.Claim.RunAttempt.RunAttemptID,
		RunAttemptGeneration: scheduled.Claim.RunAttempt.Generation,
		ActivityTTLSeconds:   int64(spec.ActivityTTL / time.Second),
	}
	response, err := lifecycle.client.RenewActivity(ctx, request, managedSandboxTokenRequest(sandboxclient.ActionRenewActivity, scheduled, session, ref))
	if err != nil {
		return err
	}
	if response.Sandbox.Ref != ref || response.Sandbox.State != sandboxcontract.SandboxReady {
		return errors.New("sandbox-gateway renewed a different or non-ready managed sandbox")
	}
	return nil
}

func (lifecycle *GatewayManagedSandboxLifecycle) Release(ctx context.Context, scheduled ScheduledRunAttempt, spec ManagedSandboxLaunchSpec, binding ManagedSandboxBinding) error {
	if err := validateManagedSandboxLaunch(scheduled, spec); err != nil {
		return err
	}
	if err := validateManagedSandboxBinding(binding); err != nil {
		return err
	}
	requestID, err := lifecycle.requestID()
	if err != nil {
		return err
	}
	session := managedSandboxSession(scheduled, spec)
	ref := sandboxcontract.SandboxRef{SandboxID: binding.SandboxID, TargetGeneration: binding.TargetGeneration}
	request := sandboxcontract.ReleaseSandboxActivityRequest{
		Profile: sandboxcontract.ProfileV1, RequestID: requestID, Session: session, Ref: ref,
		RunAttemptID:         scheduled.Claim.RunAttempt.RunAttemptID,
		RunAttemptGeneration: scheduled.Claim.RunAttempt.Generation,
	}
	response, err := lifecycle.client.ReleaseActivity(ctx, request, managedSandboxTokenRequest(sandboxclient.ActionReleaseActivity, scheduled, session, ref))
	if err != nil {
		return err
	}
	if response.Sandbox.Ref != ref {
		return errors.New("sandbox-gateway released a different managed sandbox generation")
	}
	return nil
}

func (lifecycle *GatewayManagedSandboxLifecycle) requestID() (string, error) {
	if lifecycle == nil || lifecycle.client == nil || lifecycle.idGenerator == nil {
		return "", errors.New("managed sandbox lifecycle is not configured")
	}
	requestID, err := lifecycle.idGenerator()
	if err != nil {
		return "", fmt.Errorf("allocate managed sandbox request ID: %w", err)
	}
	if err := validateUUIDIdentity("managed sandbox request ID", requestID); err != nil {
		return "", err
	}
	return requestID, nil
}

func validateManagedSandboxLaunch(scheduled ScheduledRunAttempt, spec ManagedSandboxLaunchSpec) error {
	if err := validateScheduledLaunchAuthority(scheduled); err != nil {
		return err
	}
	if err := validateUUIDIdentity("managed environment ID", spec.EnvironmentID); err != nil {
		return err
	}
	if !managedPackIDPattern.MatchString(spec.PackID) {
		return errors.New("managed sandbox pack ID must be canonical and versioned")
	}
	if !managedSandboxDigestPattern.MatchString(spec.RuntimeProfileDigest) ||
		!managedSandboxDigestPattern.MatchString(spec.PackSetDigest) ||
		!managedSandboxDigestPattern.MatchString(spec.SkillSHA256) {
		return errors.New("managed sandbox runtime, pack-set, and skill digests must be lowercase SHA-256")
	}
	if spec.SandboxTTL < 30*time.Second || spec.SandboxTTL > 24*time.Hour || spec.SandboxTTL%time.Second != 0 {
		return errors.New("managed sandbox TTL must be whole seconds between 30 seconds and 24 hours")
	}
	if spec.ActivityTTL < time.Second || spec.ActivityTTL > 24*time.Hour || spec.ActivityTTL%time.Second != 0 {
		return errors.New("managed sandbox activity TTL must be whole seconds between 1 second and 24 hours")
	}
	if spec.ActivityTTL > spec.SandboxTTL {
		return errors.New("managed sandbox activity TTL must not exceed the sandbox TTL")
	}
	return nil
}

func validateManagedSandboxBinding(binding ManagedSandboxBinding) error {
	if err := validateUUIDIdentity("managed sandbox ID", binding.SandboxID); err != nil {
		return err
	}
	if binding.TargetGeneration < 1 || binding.Root == "" || binding.ExpiresAt.IsZero() {
		return errors.New("managed sandbox binding generation, root, and expiry are required")
	}
	return nil
}

func managedSandboxSession(scheduled ScheduledRunAttempt, spec ManagedSandboxLaunchSpec) sandboxcontract.SessionIdentity {
	return sandboxcontract.SessionIdentity{
		WorkspaceID:   scheduled.Claim.Run.WorkspaceID,
		SessionID:     scheduled.Claim.Run.SessionID,
		EnvironmentID: spec.EnvironmentID,
	}
}

func managedSandboxTokenRequest(action string, scheduled ScheduledRunAttempt, session sandboxcontract.SessionIdentity, ref sandboxcontract.SandboxRef) sandboxclient.TokenRequest {
	return sandboxclient.TokenRequest{
		Action: action, Session: session, Ref: ref,
		RunID:                scheduled.Claim.Run.RunID,
		RunAttemptID:         scheduled.Claim.RunAttempt.RunAttemptID,
		RunAttemptGeneration: scheduled.Claim.RunAttempt.Generation,
		HolderID:             scheduled.Claim.RunAttempt.HolderID,
	}
}
