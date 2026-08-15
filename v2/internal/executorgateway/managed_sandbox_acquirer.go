package executorgateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
	"github.com/agentserver/agentserver/v2/internal/sandboxclient"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

var managedSandboxSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ManagedSandboxProvisioningSpec is deployment-owned authority for the one
// managed environment advertised by this executor-gateway. No provider
// session identity is stored here; the sandbox-gateway allocates and fences
// that identity when the first executor tool is actually called.
type ManagedSandboxProvisioningSpec struct {
	Region               string
	ProfileID            string
	ProfileBindingSHA256 string
	EnvironmentID        string
	RuntimeProfileDigest string
	PackSetDigest        string
	SandboxTTL           time.Duration
	ActivityTTL          time.Duration
}

// ManagedSandboxSessionLease keeps one attempt's managed sandbox activity
// live while its authenticated MCP session can dispatch executor tools.
type ManagedSandboxSessionLease interface {
	Done() <-chan struct{}
	Err() error
	Release(context.Context) error
}

type ManagedSandboxSessionAcquirer interface {
	Acquire(context.Context, ExecutorMCPPrincipal) (ManagedSandboxSessionLease, error)
}

type managedSandboxLifecycleClient interface {
	Ensure(context.Context, sandboxcontract.EnsureSandboxRequest, sandboxclient.TokenRequest) (sandboxcontract.EnsureSandboxResponse, error)
	RenewActivity(context.Context, sandboxcontract.RenewSandboxActivityRequest, sandboxclient.TokenRequest) (sandboxcontract.SandboxResponse, error)
	ReleaseActivity(context.Context, sandboxcontract.ReleaseSandboxActivityRequest, sandboxclient.TokenRequest) (sandboxcontract.SandboxResponse, error)
}

// GatewayManagedSandboxSessionAcquirer implements on-demand sandbox
// acquisition. Merely starting a model turn or opening an MCP transport does
// not call Ensure; ExecutorMCPHandler invokes Acquire only for an actual
// list_environments, shell, or read_file tool call.
type GatewayManagedSandboxSessionAcquirer struct {
	client      managedSandboxLifecycleClient
	spec        ManagedSandboxProvisioningSpec
	idGenerator IDGenerator
	now         func() time.Time
	logger      *slog.Logger
}

func NewGatewayManagedSandboxSessionAcquirer(
	client managedSandboxLifecycleClient,
	spec ManagedSandboxProvisioningSpec,
	idGenerator IDGenerator,
	now func() time.Time,
	logger *slog.Logger,
) (*GatewayManagedSandboxSessionAcquirer, error) {
	if client == nil || idGenerator == nil || now == nil {
		return nil, errors.New("managed sandbox lifecycle client, identity generator, and clock are required")
	}
	if err := validateManagedSandboxProvisioningSpec(spec); err != nil {
		return nil, err
	}
	return &GatewayManagedSandboxSessionAcquirer{
		client: client, spec: spec, idGenerator: idGenerator, now: now, logger: logger,
	}, nil
}

func NewDefaultGatewayManagedSandboxSessionAcquirer(
	client managedSandboxLifecycleClient,
	spec ManagedSandboxProvisioningSpec,
	logger *slog.Logger,
) (*GatewayManagedSandboxSessionAcquirer, error) {
	return NewGatewayManagedSandboxSessionAcquirer(client, spec, newRandomUUID, time.Now, logger)
}

func (acquirer *GatewayManagedSandboxSessionAcquirer) Acquire(
	ctx context.Context,
	principal ExecutorMCPPrincipal,
) (ManagedSandboxSessionLease, error) {
	if acquirer == nil || acquirer.client == nil || acquirer.idGenerator == nil || acquirer.now == nil || ctx == nil {
		return nil, errors.New("managed sandbox session acquirer and context are required")
	}
	if err := validateExecutorMCPPrincipal(principal); err != nil {
		return nil, err
	}
	if acquirer.spec.ProfileID != "" {
		authority := principal.ManagedSandbox
		if authority == nil || authority.Region != acquirer.spec.Region ||
			authority.ProfileID != acquirer.spec.ProfileID ||
			authority.BindingSHA256 != acquirer.spec.ProfileBindingSHA256 ||
			authority.EnvironmentID != acquirer.spec.EnvironmentID {
			return nil, errors.New("managed sandbox run authority does not match the provisioning profile")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	requestID, err := acquirer.requestID()
	if err != nil {
		return nil, err
	}
	session := sandboxcontract.SessionIdentity{
		WorkspaceID:   principal.WorkspaceID,
		SessionID:     principal.SessionID,
		EnvironmentID: acquirer.spec.EnvironmentID,
	}
	authority := sandboxclient.TokenRequest{
		Action: sandboxclient.ActionEnsure, Session: session,
		RunID: principal.Run.RunID, RunAttemptID: principal.Run.RunAttemptID,
		RunAttemptGeneration: principal.Run.RunAttemptGeneration, HolderID: principal.Run.HolderID,
	}
	startedAt := acquirer.now()
	response, err := acquirer.client.Ensure(ctx, sandboxcontract.EnsureSandboxRequest{
		Profile: sandboxcontract.ProfileV1, RequestID: requestID, Session: session,
		RequestedTTLSeconds:  int64(acquirer.spec.SandboxTTL / time.Second),
		RuntimeProfileDigest: acquirer.spec.RuntimeProfileDigest,
		PackSetDigest:        acquirer.spec.PackSetDigest,
	}, authority)
	if err != nil {
		acquirer.logAcquire(ctx, principal, "ensure_failed", "", 0, startedAt, err)
		return nil, fmt.Errorf("ensure managed sandbox on first executor use: %w", err)
	}
	ref := response.Sandbox.Ref
	if response.Sandbox.State != sandboxcontract.SandboxReady || response.Sandbox.Root == "" || response.Sandbox.ExpiresAt.IsZero() {
		err := errors.New("sandbox-gateway ensure did not return a ready managed sandbox")
		acquirer.logAcquire(ctx, principal, "invalid_ready_response", ref.SandboxID, ref.TargetGeneration, startedAt, err)
		return nil, err
	}
	if err := acquirer.renew(ctx, principal, session, ref); err != nil {
		acquirer.logAcquire(ctx, principal, "initial_activity_failed", ref.SandboxID, ref.TargetGeneration, startedAt, err)
		return nil, fmt.Errorf("acquire managed sandbox activity on first executor use: %w", err)
	}
	lease := &gatewayManagedSandboxSessionLease{
		client: acquirer.client, spec: acquirer.spec, principal: principal, session: session, ref: ref,
		idGenerator: acquirer.idGenerator, logger: acquirer.logger,
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	go lease.keepAlive()
	acquirer.logAcquire(ctx, principal, "ready", ref.SandboxID, ref.TargetGeneration, startedAt, nil)
	return lease, nil
}

// ManagedSandboxSessionAcquirerRouter selects the one immutable provisioning
// profile carried by the Core-issued run authority. It never reads a tool
// argument or request header to choose a region.
type ManagedSandboxSessionAcquirerRouter struct {
	byProfile map[string]ManagedSandboxSessionAcquirer
}

func NewManagedSandboxSessionAcquirerRouter(
	profiles map[string]ManagedSandboxSessionAcquirer,
) (*ManagedSandboxSessionAcquirerRouter, error) {
	if len(profiles) < 1 || len(profiles) > 32 {
		return nil, errors.New("managed sandbox acquirer router requires between 1 and 32 profiles")
	}
	copy := make(map[string]ManagedSandboxSessionAcquirer, len(profiles))
	for profileID, acquirer := range profiles {
		if acquirer == nil {
			return nil, errors.New("managed sandbox acquirer router contains a nil profile")
		}
		if !managedsandboxprofile.ValidProfileID(profileID) {
			return nil, errors.New("managed sandbox acquirer profile ID must be canonical bounded text")
		}
		if _, duplicate := copy[profileID]; duplicate {
			return nil, errors.New("managed sandbox acquirer profile is repeated")
		}
		copy[profileID] = acquirer
	}
	return &ManagedSandboxSessionAcquirerRouter{byProfile: copy}, nil
}

func (router *ManagedSandboxSessionAcquirerRouter) Acquire(
	ctx context.Context,
	principal ExecutorMCPPrincipal,
) (ManagedSandboxSessionLease, error) {
	if router == nil || principal.ManagedSandbox == nil {
		return nil, errors.New("managed sandbox run authority is required")
	}
	acquirer := router.byProfile[principal.ManagedSandbox.ProfileID]
	if acquirer == nil {
		return nil, errors.New("managed sandbox run profile is not configured")
	}
	return acquirer.Acquire(ctx, principal)
}

func (acquirer *GatewayManagedSandboxSessionAcquirer) requestID() (string, error) {
	requestID, err := acquirer.idGenerator()
	if err != nil {
		return "", fmt.Errorf("allocate managed sandbox request ID: %w", err)
	}
	if err := validateRegistryIdentity("managed sandbox request ID", requestID); err != nil {
		return "", err
	}
	return requestID, nil
}

func (acquirer *GatewayManagedSandboxSessionAcquirer) renew(
	ctx context.Context,
	principal ExecutorMCPPrincipal,
	session sandboxcontract.SessionIdentity,
	ref sandboxcontract.SandboxRef,
) error {
	requestID, err := acquirer.requestID()
	if err != nil {
		return err
	}
	response, err := acquirer.client.RenewActivity(ctx, sandboxcontract.RenewSandboxActivityRequest{
		Profile: sandboxcontract.ProfileV1, RequestID: requestID, Session: session, Ref: ref,
		RunAttemptID:         principal.Run.RunAttemptID,
		RunAttemptGeneration: principal.Run.RunAttemptGeneration,
		ActivityTTLSeconds:   int64(acquirer.spec.ActivityTTL / time.Second),
	}, sandboxclient.TokenRequest{
		Action: sandboxclient.ActionRenewActivity, Session: session, Ref: ref,
		RunID: principal.Run.RunID, RunAttemptID: principal.Run.RunAttemptID,
		RunAttemptGeneration: principal.Run.RunAttemptGeneration, HolderID: principal.Run.HolderID,
	})
	if err != nil {
		return err
	}
	if response.Sandbox.Ref != ref || response.Sandbox.State != sandboxcontract.SandboxReady {
		return errors.New("sandbox-gateway renewed a different or non-ready managed sandbox")
	}
	return nil
}

func (acquirer *GatewayManagedSandboxSessionAcquirer) logAcquire(
	ctx context.Context,
	principal ExecutorMCPPrincipal,
	stage, sandboxID string,
	generation int64,
	startedAt time.Time,
	err error,
) {
	if acquirer.logger == nil {
		return
	}
	elapsed := acquirer.now().Sub(startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	attributes := []any{
		"stage", stage,
		"workspace_id", principal.WorkspaceID,
		"session_id", principal.SessionID,
		"run_id", principal.Run.RunID,
		"run_attempt_id", principal.Run.RunAttemptID,
		"run_attempt_generation", principal.Run.RunAttemptGeneration,
		"environment_id", acquirer.spec.EnvironmentID,
		"sandbox_id", sandboxID,
		"sandbox_generation", generation,
		"elapsed_ms", elapsed.Milliseconds(),
	}
	if err != nil {
		attributes = append(attributes, "error", err)
		acquirer.logger.ErrorContext(ctx, "lazy managed sandbox acquire", attributes...)
		return
	}
	acquirer.logger.InfoContext(ctx, "lazy managed sandbox acquire", attributes...)
}

type gatewayManagedSandboxSessionLease struct {
	client      managedSandboxLifecycleClient
	spec        ManagedSandboxProvisioningSpec
	principal   ExecutorMCPPrincipal
	session     sandboxcontract.SessionIdentity
	ref         sandboxcontract.SandboxRef
	idGenerator IDGenerator
	logger      *slog.Logger

	stop        chan struct{}
	done        chan struct{}
	stopOnce    sync.Once
	releaseOnce sync.Once
	mu          sync.Mutex
	err         error
	releaseErr  error
}

func (lease *gatewayManagedSandboxSessionLease) Done() <-chan struct{} { return lease.done }

func (lease *gatewayManagedSandboxSessionLease) Err() error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	return lease.err
}

func (lease *gatewayManagedSandboxSessionLease) keepAlive() {
	defer close(lease.done)
	interval := lease.spec.ActivityTTL / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	deadline := lease.principal.RunDeadline
	if lease.principal.CapabilityExpiresAt.Before(deadline) {
		deadline = lease.principal.CapabilityExpiresAt
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		select {
		case <-lease.stop:
			return
		case <-timer.C:
			lease.setError(errors.New("managed sandbox activity authority expired"))
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), interval)
			err := lease.renew(ctx)
			cancel()
			if err != nil {
				lease.setError(fmt.Errorf("renew lazy managed sandbox activity: %w", err))
				if lease.logger != nil {
					lease.logger.Error("lazy managed sandbox activity renewal failed",
						"workspace_id", lease.principal.WorkspaceID,
						"session_id", lease.principal.SessionID,
						"run_id", lease.principal.Run.RunID,
						"run_attempt_id", lease.principal.Run.RunAttemptID,
						"sandbox_id", lease.ref.SandboxID,
						"sandbox_generation", lease.ref.TargetGeneration,
						"error", err,
					)
				}
				return
			}
		}
	}
}

func (lease *gatewayManagedSandboxSessionLease) renew(ctx context.Context) error {
	requestID, err := lease.requestID()
	if err != nil {
		return err
	}
	response, err := lease.client.RenewActivity(ctx, sandboxcontract.RenewSandboxActivityRequest{
		Profile: sandboxcontract.ProfileV1, RequestID: requestID, Session: lease.session, Ref: lease.ref,
		RunAttemptID:         lease.principal.Run.RunAttemptID,
		RunAttemptGeneration: lease.principal.Run.RunAttemptGeneration,
		ActivityTTLSeconds:   int64(lease.spec.ActivityTTL / time.Second),
	}, lease.tokenRequest(sandboxclient.ActionRenewActivity))
	if err != nil {
		return err
	}
	if response.Sandbox.Ref != lease.ref || response.Sandbox.State != sandboxcontract.SandboxReady {
		return errors.New("sandbox-gateway renewed a different or non-ready managed sandbox")
	}
	return nil
}

func (lease *gatewayManagedSandboxSessionLease) Release(ctx context.Context) error {
	if ctx == nil {
		return errors.New("managed sandbox release context is required")
	}
	lease.releaseOnce.Do(func() {
		lease.stopOnce.Do(func() { close(lease.stop) })
		select {
		case <-lease.done:
		case <-ctx.Done():
			lease.releaseErr = ctx.Err()
			return
		}
		requestID, err := lease.requestID()
		if err != nil {
			lease.releaseErr = err
			return
		}
		response, err := lease.client.ReleaseActivity(ctx, sandboxcontract.ReleaseSandboxActivityRequest{
			Profile: sandboxcontract.ProfileV1, RequestID: requestID, Session: lease.session, Ref: lease.ref,
			RunAttemptID:         lease.principal.Run.RunAttemptID,
			RunAttemptGeneration: lease.principal.Run.RunAttemptGeneration,
		}, lease.tokenRequest(sandboxclient.ActionReleaseActivity))
		if err != nil {
			lease.releaseErr = err
			return
		}
		if response.Sandbox.Ref != lease.ref {
			lease.releaseErr = errors.New("sandbox-gateway released a different managed sandbox generation")
		}
	})
	return lease.releaseErr
}

func (lease *gatewayManagedSandboxSessionLease) tokenRequest(action string) sandboxclient.TokenRequest {
	return sandboxclient.TokenRequest{
		Action: action, Session: lease.session, Ref: lease.ref,
		RunID: lease.principal.Run.RunID, RunAttemptID: lease.principal.Run.RunAttemptID,
		RunAttemptGeneration: lease.principal.Run.RunAttemptGeneration, HolderID: lease.principal.Run.HolderID,
	}
}

func (lease *gatewayManagedSandboxSessionLease) requestID() (string, error) {
	requestID, err := lease.idGenerator()
	if err != nil {
		return "", fmt.Errorf("allocate managed sandbox request ID: %w", err)
	}
	if err := validateRegistryIdentity("managed sandbox request ID", requestID); err != nil {
		return "", err
	}
	return requestID, nil
}

func (lease *gatewayManagedSandboxSessionLease) setError(err error) {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.err == nil {
		lease.err = err
	}
}

func validateManagedSandboxProvisioningSpec(spec ManagedSandboxProvisioningSpec) error {
	if err := (managedsandboxprofile.Binding{
		Region: spec.Region, ProfileID: spec.ProfileID,
		BindingSHA256: spec.ProfileBindingSHA256, EnvironmentID: spec.EnvironmentID,
	}).Validate(); err != nil {
		return err
	}
	if !managedSandboxSHA256Pattern.MatchString(spec.RuntimeProfileDigest) ||
		!managedSandboxSHA256Pattern.MatchString(spec.PackSetDigest) {
		return errors.New("managed sandbox runtime profile and pack-set digests must be lowercase SHA-256")
	}
	if spec.SandboxTTL < 30*time.Second || spec.SandboxTTL > 24*time.Hour || spec.SandboxTTL%time.Second != 0 {
		return errors.New("managed sandbox TTL must be whole seconds between 30 seconds and 24 hours")
	}
	if spec.ActivityTTL < 3*time.Second || spec.ActivityTTL > spec.SandboxTTL || spec.ActivityTTL%time.Second != 0 {
		return errors.New("managed sandbox activity TTL must be whole seconds between 3 seconds and the sandbox TTL")
	}
	return nil
}

// ValidateManagedSandboxProvisioningSpec validates deployment-owned routing
// and runtime authority without constructing a lifecycle client.
func ValidateManagedSandboxProvisioningSpec(spec ManagedSandboxProvisioningSpec) error {
	return validateManagedSandboxProvisioningSpec(spec)
}

var _ ManagedSandboxSessionAcquirer = (*GatewayManagedSandboxSessionAcquirer)(nil)
var _ ManagedSandboxSessionLease = (*gatewayManagedSandboxSessionLease)(nil)
