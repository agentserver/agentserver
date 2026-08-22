package harnesspool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
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
	Prompt                 runmanifest.ObjectPointer
	PreviousCheckpoint     *RunLaunchCheckpoint
	ExecutorPolicy         ExecutorCatalogPolicy
	LLMGateway             *RunLLMGatewayBinding
	LarkEgress             *RunLarkEgressBinding
	ManagedSandbox         *RunManagedSandboxBinding
	PermissionMode         runmanifest.CodexPermissionMode
	PermissionModeVersion  int64
	PermissionModeExplicit bool
	PermissionModeLegacy   bool
}

type RunManagedSandboxBinding struct {
	SettingVersion int64
	Region         string
	EnvironmentID  string
}

type RunLLMGatewayBinding struct {
	GatewayID     string
	ConfigVersion int64
	GrantUserID   string
	Model         string
}

type RunLarkEgressBinding struct {
	GrantID      string
	GrantVersion int64
	GrantUserID  string
	PolicySHA256 string
}

type RunLaunchStateSource interface {
	ResolveRunLaunchState(context.Context, ScheduledRunAttempt) (RunLaunchState, error)
}

// RunLaunchProfile is deployment-owned configuration shared by attempts on
// one harness-pool holder. ControllerCallbackEndpoint must address that exact
// holder instance rather than a load-balanced Service.
type RunLaunchProfile struct {
	CodexRuntimeManifestDigest string
	// PermissionMode is deployment-owned Codex approval/sandbox authority.
	// Empty is normalized to the safe read-only preset for legacy profiles.
	PermissionMode             runmanifest.CodexPermissionMode
	Model                      runmanifest.ModelRoute
	ModelFromRun               bool
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
	// ManagedSandboxProfiles is the production launch catalog keyed by region.
	// ManagedSandbox is retained only
	// for insecure-development and legacy single-profile deployments.
	ManagedSandboxProfiles map[string]ManagedSandboxLaunchSpec
	ManagedSandbox         *ManagedSandboxLaunchSpec
}

// deploymentPermissionModeVersion is the epoch used when a launch source
// does not provide Core's per-session permission-mode authority.  New Core
// session rows always take the explicit branch below and carry their own CAS
// version; this value only makes a static development/legacy deployment
// profile's mode and version a valid, paired manifest projection.
const deploymentPermissionModeVersion int64 = 1

const maxPermissionModeVersion int64 = 1<<53 - 1

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
	return &ConfiguredRunLaunchInputResolver{source: source, profile: cloneRunLaunchProfile(profile)}, nil
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
	permissionMode, permissionModeVersion, err := resolveRunPermissionMode(profile.PermissionMode, state)
	if err != nil {
		return RunLaunchInputs{}, err
	}
	policy := state.ExecutorPolicy
	policy.AllowedTools = append([]string(nil), state.ExecutorPolicy.AllowedTools...)
	managedSandbox, err := profile.selectManagedSandbox(state.ManagedSandbox)
	if err != nil {
		return RunLaunchInputs{}, err
	}
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
	model := profile.Model
	if profile.ModelFromRun {
		if state.LLMGateway == nil {
			return RunLaunchInputs{}, errors.New("production run has no frozen workspace LLM gateway binding")
		}
		model.Model = state.LLMGateway.Model
		model.Provider = corecontract.WorkspaceLLMGatewayProvider
		model.LLMGatewayID = state.LLMGateway.GatewayID
		model.LLMGatewayVersion = state.LLMGateway.ConfigVersion
		model.LLMGatewayGrantUserID = state.LLMGateway.GrantUserID
	} else if state.LLMGateway != nil {
		return RunLaunchInputs{}, errors.New("static development model profile received production workspace LLM gateway authority")
	}
	return RunLaunchInputs{
		Prompt: state.Prompt, PreviousCheckpoint: previousCheckpoint, PreviousBrainToolCatalog: previousCatalog,
		CodexRuntimeManifestDigest: profile.CodexRuntimeManifestDigest, Model: model,
		PermissionMode: permissionMode, PermissionModeVersion: permissionModeVersion,
		ExecutorCatalogPolicy: policy, ExecutorMCPEndpoint: profile.ExecutorMCPEndpoint,
		ExecutorMCPTLSIdentity: profile.ExecutorMCPTLSIdentity, ExecutorMCPAudience: profile.ExecutorMCPAudience,
		Limits: profile.Limits, CheckpointAllowlistVersion: profile.CheckpointAllowlistVersion,
		WorkerImageDigest: profile.WorkerImageDigest, ExpectedServiceAccount: profile.ExpectedServiceAccount,
		ControllerCallbackEndpoint: profile.ControllerCallbackEndpoint,
		ControllerCallbackIdentity: profile.ControllerCallbackIdentity,
		ControllerCallbackAudience: profile.ControllerCallbackAudience,
		ManagedSandbox:             managedSandbox,
	}, nil
}

// resolveRunPermissionMode validates the marker/value relationship carried by
// a launch-state source before any deployment fallback is applied.  A source
// that accidentally combines legacy and explicit authority, or supplies mode
// values without declaring which branch they belong to, must fail closed;
// otherwise a malformed response could silently select a different preset.
func resolveRunPermissionMode(profileMode runmanifest.CodexPermissionMode, state RunLaunchState) (runmanifest.CodexPermissionMode, int64, error) {
	if state.PermissionModeLegacy && state.PermissionModeExplicit {
		return "", 0, errors.New("permission mode authority cannot be both legacy and explicit")
	}
	if state.PermissionModeLegacy {
		if state.PermissionMode != "" || state.PermissionModeVersion != 0 {
			return "", 0, errors.New("legacy permission mode authority must omit mode and version")
		}
		// Preserve the pre-mode manifest semantics for old launch rows.
		return "", 0, nil
	}
	if state.PermissionModeExplicit {
		if state.PermissionMode == "" {
			return "", 0, errors.New("explicit permission mode authority requires a mode")
		}
		mode, err := state.PermissionMode.Effective()
		if err != nil {
			return "", 0, err
		}
		if state.PermissionModeVersion < 1 || state.PermissionModeVersion > maxPermissionModeVersion {
			return "", 0, errors.New("explicit permission mode authority requires a positive JSON-safe version")
		}
		return mode, state.PermissionModeVersion, nil
	}
	if state.PermissionMode != "" || state.PermissionModeVersion != 0 {
		return "", 0, errors.New("permission mode authority values require an explicit or legacy marker")
	}
	mode, err := profileMode.Effective()
	if err != nil {
		return "", 0, err
	}
	return mode, deploymentPermissionModeVersion, nil
}

func (profile RunLaunchProfile) selectManagedSandbox(binding *RunManagedSandboxBinding) (*ManagedSandboxLaunchSpec, error) {
	if binding == nil {
		if profile.ModelFromRun && (profile.ManagedSandbox != nil || len(profile.ManagedSandboxProfiles) != 0) {
			return nil, errors.New("production run has no frozen managed sandbox profile")
		}
		if len(profile.ManagedSandboxProfiles) != 0 {
			return nil, errors.New("managed sandbox profile catalog requires frozen run authority")
		}
		return cloneManagedSandboxLaunchSpec(profile.ManagedSandbox), nil
	}

	var selected *ManagedSandboxLaunchSpec
	if len(profile.ManagedSandboxProfiles) != 0 {
		spec, ok := profile.ManagedSandboxProfiles[binding.Region]
		if !ok {
			return nil, fmt.Errorf("run selected managed sandbox region %q which is not installed on this harness", binding.Region)
		}
		selected = cloneManagedSandboxLaunchSpec(&spec)
	} else {
		selected = cloneManagedSandboxLaunchSpec(profile.ManagedSandbox)
		if selected == nil {
			return nil, errors.New("run selected a managed sandbox profile but this harness deployment has none")
		}
		selected.Region = binding.Region
	}
	selected.SettingVersion = binding.SettingVersion
	return selected, nil
}

func validateRunLaunchProfile(profile RunLaunchProfile) error {
	if _, err := profile.PermissionMode.Effective(); err != nil {
		return err
	}
	if profile.ManagedSandbox != nil && len(profile.ManagedSandboxProfiles) != 0 {
		return errors.New("managed sandbox single profile and profile catalog cannot both be configured")
	}
	if len(profile.ManagedSandboxProfiles) > 4 {
		return errors.New("managed sandbox profile catalog cannot contain more than four profiles")
	}
	seenRegions := make(map[string]struct{}, len(profile.ManagedSandboxProfiles))
	seenEnvironments := make(map[string]struct{}, len(profile.ManagedSandboxProfiles))
	for region, spec := range profile.ManagedSandboxProfiles {
		if !managedsandboxprofile.ValidRegion(region) || region != spec.Region {
			return errors.New("managed sandbox catalog key must equal its region")
		}
		if spec.SettingVersion != 0 {
			return errors.New("deployment managed sandbox profile must not pin a workspace setting version")
		}
		if _, duplicate := seenRegions[spec.Region]; duplicate {
			return errors.New("managed sandbox profile catalog repeats a region")
		}
		if _, duplicate := seenEnvironments[spec.EnvironmentID]; duplicate {
			return errors.New("managed sandbox profile catalog repeats an environment")
		}
		seenRegions[spec.Region] = struct{}{}
		seenEnvironments[spec.EnvironmentID] = struct{}{}
		validationSpec := spec
		validationSpec.SettingVersion = 1
		if err := validateManagedSandboxLaunch(profileValidationScheduledRun(), validationSpec); err != nil {
			return fmt.Errorf("managed sandbox region %q: %w", region, err)
		}
	}
	policyDigest := sha256.Sum256([]byte("agentserver-v2/run-launch-profile-validation"))
	scheduled := profileValidationScheduledRun()
	state := RunLaunchState{
		Prompt: runmanifest.ObjectPointer{
			ObjectID: "35000000-0000-4000-8000-000000000003", SHA256: strings.Repeat("a", 64),
			SizeBytes: 1, MediaType: "application/json",
		},
		ExecutorPolicy: ExecutorCatalogPolicy{Version: "profile-validation-policy", ContextDigest: policyDigest},
	}
	if profile.ModelFromRun {
		state.LLMGateway = &RunLLMGatewayBinding{
			GatewayID: "37000000-0000-4000-8000-000000000003", ConfigVersion: 1,
			GrantUserID: "38000000-0000-4000-8000-000000000003", Model: "profile-validation-model",
		}
		if len(profile.ManagedSandboxProfiles) != 0 {
			for _, spec := range profile.ManagedSandboxProfiles {
				state.ManagedSandbox = &RunManagedSandboxBinding{
					SettingVersion: 1, Region: spec.Region, EnvironmentID: spec.EnvironmentID,
				}
				break
			}
		} else if profile.ManagedSandbox != nil {
			spec := profile.ManagedSandbox
			state.ManagedSandbox = &RunManagedSandboxBinding{
				SettingVersion: 1, Region: spec.Region, EnvironmentID: spec.EnvironmentID,
			}
			if state.ManagedSandbox.Region == "" {
				// Legacy production configuration receives its routing region from
				// Core. Use the default region at construction time.
				state.ManagedSandbox.Region = managedsandboxprofile.DefaultRegion
			}
		}
	}
	inputs, err := profile.inputs(state)
	if err != nil {
		return err
	}
	return validateResolvedRunLaunchInputs(scheduled, inputs)
}

func profileValidationScheduledRun() ScheduledRunAttempt {
	return ScheduledRunAttempt{
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
}

func cloneRunLaunchProfile(source RunLaunchProfile) RunLaunchProfile {
	copy := source
	copy.ManagedSandbox = cloneManagedSandboxLaunchSpec(source.ManagedSandbox)
	if source.ManagedSandboxProfiles != nil {
		copy.ManagedSandboxProfiles = make(map[string]ManagedSandboxLaunchSpec, len(source.ManagedSandboxProfiles))
		for region, spec := range source.ManagedSandboxProfiles {
			copy.ManagedSandboxProfiles[region] = spec
		}
	}
	return copy
}

func validateResolvedRunLaunchInputs(scheduled ScheduledRunAttempt, inputs RunLaunchInputs) error {
	permissionMode := inputs.PermissionMode
	if permissionMode != "" {
		var err error
		permissionMode, err = permissionMode.Effective()
		if err != nil {
			return err
		}
		if inputs.PermissionModeVersion < 1 {
			return errors.New("explicit permission mode requires a positive version")
		}
	} else if inputs.PermissionModeVersion != 0 {
		return errors.New("permission mode version cannot be set without a mode")
	}
	if (inputs.PreviousCheckpoint == nil) != (inputs.PreviousBrainToolCatalog == nil) {
		return errors.New("previous checkpoint and brain tool catalog authority must be supplied together")
	}
	if inputs.ManagedSandbox != nil {
		if err := validateManagedSandboxLaunch(scheduled, *inputs.ManagedSandbox); err != nil {
			return err
		}
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
		PermissionMode: permissionMode, PermissionModeVersion: inputs.PermissionModeVersion,
		ExecutorPolicy: runmanifest.ExecutorPolicy{
			Version: proposal.PolicyVersion, ContextDigest: hex.EncodeToString(proposal.PolicyContextDigest[:]),
		},
		ToolPack:       managedToolPackAuthority(inputs.ManagedSandbox),
		ManagedSandbox: managedSandboxAuthority(inputs.ManagedSandbox),
		Limits:         inputs.Limits, CheckpointAllowlistVersion: inputs.CheckpointAllowlistVersion,
		WorkerImageDigest: inputs.WorkerImageDigest, ExpectedServiceAccount: inputs.ExpectedServiceAccount,
		ControllerCallback: runmanifest.ControllerCallback{
			Endpoint: inputs.ControllerCallbackEndpoint, TLSIdentity: inputs.ControllerCallbackIdentity,
			Audience: inputs.ControllerCallbackAudience, HolderID: claim.RunAttempt.HolderID,
		},
	}
	return manifest.Validate()
}
