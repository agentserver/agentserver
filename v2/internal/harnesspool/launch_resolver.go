package harnesspool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

// RunLaunchCheckpoint carries both the signed-manifest projection and the
// compatibility facts that must match the selected deployment profile.
type RunLaunchCheckpoint struct {
	Checkpoint runmanifest.PreviousCheckpoint
	Catalog    BrainToolCatalog
}

// RunLaunchState is the per-run, authority-derived portion of a launch. A
// production source obtains this projection from core/policy services after
// the exact attempt has been claimed; it must not read model-controlled
// endpoint, image, callback, or runtime configuration.
type RunLaunchState struct {
	Prompt             runmanifest.ObjectPointer
	PreviousCheckpoint *RunLaunchCheckpoint
	ExecutorPolicy     ExecutorCatalogPolicy
}

type RunLaunchStateSource interface {
	ResolveRunLaunchState(context.Context, ScheduledRunAttempt) (RunLaunchState, error)
}

// RunLaunchProfile is deployment-owned configuration shared by attempts on
// one harness-pool holder. ControllerCallbackEndpoint must address that exact
// holder instance rather than a load-balanced Service.
type RunLaunchProfile struct {
	CodexRuntimeManifestDigest string
	Model                      runmanifest.ModelRoute
	ExecutorMCPEndpoint        string
	ExecutorMCPTLSIdentity     string
	ExecutorMCPAudience        string
	Limits                     runmanifest.RunLimits
	CheckpointAllowlistVersion int
	WorkerImageDigest          string
	ExpectedServiceAccount     string
	ControllerCallbackEndpoint string
	ControllerCallbackIdentity string
	ControllerCallbackAudience string
}

// ConfiguredRunLaunchInputResolver combines authority-derived mutable state
// with an immutable deployment profile. It validates the complete result
// before the signer or core freeze command can observe it.
type ConfiguredRunLaunchInputResolver struct {
	source  RunLaunchStateSource
	profile RunLaunchProfile
}

func NewConfiguredRunLaunchInputResolver(source RunLaunchStateSource, profile RunLaunchProfile) (*ConfiguredRunLaunchInputResolver, error) {
	if source == nil {
		return nil, errors.New("run launch state source is required")
	}
	if err := validateRunLaunchProfile(profile); err != nil {
		return nil, fmt.Errorf("run launch profile: %w", err)
	}
	return &ConfiguredRunLaunchInputResolver{source: source, profile: profile}, nil
}

func (resolver *ConfiguredRunLaunchInputResolver) ResolveRunLaunch(ctx context.Context, scheduled ScheduledRunAttempt) (RunLaunchInputs, error) {
	if resolver == nil || resolver.source == nil {
		return RunLaunchInputs{}, errors.New("configured run launch resolver is required")
	}
	if ctx == nil {
		return RunLaunchInputs{}, errors.New("run launch resolution context is required")
	}
	if err := validateScheduledLaunchAuthority(scheduled); err != nil {
		return RunLaunchInputs{}, err
	}
	state, err := resolver.source.ResolveRunLaunchState(ctx, scheduled)
	if err != nil {
		return RunLaunchInputs{}, fmt.Errorf("resolve authority-derived run launch state: %w", err)
	}
	inputs, err := resolver.profile.inputs(state)
	if err != nil {
		return RunLaunchInputs{}, fmt.Errorf("validate checkpoint compatibility: %w", err)
	}
	if err := validateResolvedRunLaunchInputs(scheduled, inputs); err != nil {
		return RunLaunchInputs{}, fmt.Errorf("validate resolved run launch inputs: %w", err)
	}
	return inputs, nil
}

func (profile RunLaunchProfile) inputs(state RunLaunchState) (RunLaunchInputs, error) {
	policy := state.ExecutorPolicy
	policy.AllowedTools = append([]string(nil), state.ExecutorPolicy.AllowedTools...)
	var previousCheckpoint *runmanifest.PreviousCheckpoint
	var previousCatalog *BrainToolCatalog
	if state.PreviousCheckpoint != nil {
		if state.PreviousCheckpoint.Checkpoint.CodexRuntimeManifestDigest != profile.CodexRuntimeManifestDigest ||
			state.PreviousCheckpoint.Checkpoint.CheckpointAllowlistVersion != int64(profile.CheckpointAllowlistVersion) {
			return RunLaunchInputs{}, errors.New("previous checkpoint runtime manifest or allowlist version does not match the deployment profile")
		}
		proposal, err := BuildExecutorCatalog(policy)
		if err != nil {
			return RunLaunchInputs{}, err
		}
		catalog := state.PreviousCheckpoint.Catalog
		if err := validateUUIDIdentity("previous checkpoint catalog ID", catalog.CatalogID); err != nil {
			return RunLaunchInputs{}, err
		}
		if catalog.ThreadID != state.PreviousCheckpoint.Checkpoint.ThreadID ||
			catalog.ContractVersion != proposal.ContractVersion || catalog.CanonicalizerVersion != proposal.CanonicalizerVersion ||
			!bytes.Equal(catalog.CanonicalCatalog, proposal.CanonicalCatalog) || catalog.CatalogDigest != proposal.CatalogDigest ||
			catalog.PolicyVersion != proposal.PolicyVersion || catalog.PolicyContextDigest != proposal.PolicyContextDigest ||
			state.PreviousCheckpoint.Checkpoint.CatalogDigest != hex.EncodeToString(catalog.CatalogDigest[:]) {
			return RunLaunchInputs{}, errors.New("previous checkpoint catalog authority does not match its checkpoint and executor policy")
		}
		previousCheckpoint = clonePreviousCheckpoint(&state.PreviousCheckpoint.Checkpoint)
		catalogCopy := cloneBrainToolCatalog(catalog)
		previousCatalog = &catalogCopy
	}
	return RunLaunchInputs{
		Prompt: state.Prompt, PreviousCheckpoint: previousCheckpoint, PreviousBrainToolCatalog: previousCatalog,
		CodexRuntimeManifestDigest: profile.CodexRuntimeManifestDigest, Model: profile.Model,
		ExecutorCatalogPolicy: policy, ExecutorMCPEndpoint: profile.ExecutorMCPEndpoint,
		ExecutorMCPTLSIdentity: profile.ExecutorMCPTLSIdentity, ExecutorMCPAudience: profile.ExecutorMCPAudience,
		Limits: profile.Limits, CheckpointAllowlistVersion: profile.CheckpointAllowlistVersion,
		WorkerImageDigest: profile.WorkerImageDigest, ExpectedServiceAccount: profile.ExpectedServiceAccount,
		ControllerCallbackEndpoint: profile.ControllerCallbackEndpoint,
		ControllerCallbackIdentity: profile.ControllerCallbackIdentity,
		ControllerCallbackAudience: profile.ControllerCallbackAudience,
	}, nil
}

func validateRunLaunchProfile(profile RunLaunchProfile) error {
	policyDigest := sha256.Sum256([]byte("agentserver-v2/run-launch-profile-validation"))
	scheduled := ScheduledRunAttempt{
		Dispatch: RunDispatch{
			RunDispatchID: "30000000-0000-4000-8000-000000000003",
			WorkspaceID:   "31000000-0000-4000-8000-000000000003",
			SessionID:     "32000000-0000-4000-8000-000000000003",
			RunID:         "33000000-0000-4000-8000-000000000003",
		},
		Claim: ClaimRunAttemptResult{
			Run: Run{
				RunID:       "33000000-0000-4000-8000-000000000003",
				WorkspaceID: "31000000-0000-4000-8000-000000000003",
				SessionID:   "32000000-0000-4000-8000-000000000003",
				Status:      "starting", CurrentAttemptGeneration: 1, Version: 1,
			},
			RunAttempt: RunAttempt{
				RunAttemptID: "34000000-0000-4000-8000-000000000003",
				RunID:        "33000000-0000-4000-8000-000000000003", Generation: 1,
				Status: "leased", HolderID: "profile-validation-holder", Version: 1,
			},
		},
	}
	state := RunLaunchState{
		Prompt: runmanifest.ObjectPointer{
			ObjectID: "35000000-0000-4000-8000-000000000003", SHA256: strings.Repeat("a", 64),
			SizeBytes: 1, MediaType: "application/json",
		},
		ExecutorPolicy: ExecutorCatalogPolicy{Version: "profile-validation-policy", ContextDigest: policyDigest},
	}
	inputs, err := profile.inputs(state)
	if err != nil {
		return err
	}
	return validateResolvedRunLaunchInputs(scheduled, inputs)
}

func validateResolvedRunLaunchInputs(scheduled ScheduledRunAttempt, inputs RunLaunchInputs) error {
	if (inputs.PreviousCheckpoint == nil) != (inputs.PreviousBrainToolCatalog == nil) {
		return errors.New("previous checkpoint and brain tool catalog authority must be supplied together")
	}
	proposal, err := BuildExecutorCatalog(inputs.ExecutorCatalogPolicy)
	if err != nil {
		return err
	}
	executorMCP, err := runmanifest.ExecutorMCPFromCatalog(
		inputs.ExecutorMCPEndpoint,
		inputs.ExecutorMCPTLSIdentity,
		inputs.ExecutorMCPAudience,
		proposal.ContractVersion,
		"36000000-0000-4000-8000-000000000003",
		proposal.Catalog,
	)
	if err != nil {
		return err
	}
	claim := scheduled.Claim
	manifest := runmanifest.Manifest{
		ManifestVersion: runmanifest.CurrentVersion, CanonicalizerVersion: runmanifest.Canonicalizer,
		WorkspaceID: claim.Run.WorkspaceID, SessionID: claim.Run.SessionID, RunID: claim.Run.RunID,
		RunAttemptID: claim.RunAttempt.RunAttemptID, RunAttemptGeneration: claim.RunAttempt.Generation,
		HolderID: claim.RunAttempt.HolderID, Prompt: inputs.Prompt,
		PreviousCheckpoint:         clonePreviousCheckpoint(inputs.PreviousCheckpoint),
		CodexRuntimeManifestDigest: inputs.CodexRuntimeManifestDigest, Model: inputs.Model, ExecutorMCP: executorMCP,
		ExecutorPolicy: runmanifest.ExecutorPolicy{
			Version: proposal.PolicyVersion, ContextDigest: hex.EncodeToString(proposal.PolicyContextDigest[:]),
		},
		Limits: inputs.Limits, CheckpointAllowlistVersion: inputs.CheckpointAllowlistVersion,
		WorkerImageDigest: inputs.WorkerImageDigest, ExpectedServiceAccount: inputs.ExpectedServiceAccount,
		ControllerCallback: runmanifest.ControllerCallback{
			Endpoint: inputs.ControllerCallbackEndpoint, TLSIdentity: inputs.ControllerCallbackIdentity,
			Audience: inputs.ControllerCallbackAudience, HolderID: claim.RunAttempt.HolderID,
		},
	}
	return manifest.Validate()
}
