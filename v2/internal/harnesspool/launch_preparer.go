package harnesspool

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

type LaunchPreparationCore interface {
	FreezeBrainToolCatalog(context.Context, FreezeBrainToolCatalogRequest) (FreezeBrainToolCatalogResult, error)
}

type BrainToolCatalogIDAllocator interface {
	AllocateBrainToolCatalogID() (string, error)
}

type RunLaunchInputResolver interface {
	ResolveRunLaunch(context.Context, ScheduledRunAttempt) (RunLaunchInputs, error)
}

type RunManifestSigner interface {
	SignRunManifest(runmanifest.Manifest) (runmanifest.SignedManifest, error)
}

type RunLaunchInputs struct {
	Prompt                     runmanifest.ObjectPointer
	PreviousCheckpoint         *runmanifest.PreviousCheckpoint
	PreviousBrainToolCatalog   *BrainToolCatalog
	CodexRuntimeManifestDigest string
	Model                      runmanifest.ModelRoute
	ExecutorCatalogPolicy      ExecutorCatalogPolicy
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
	ManagedSandbox             *ManagedSandboxLaunchSpec
}

type PreparedRunLaunch struct {
	Scheduled      ScheduledRunAttempt
	FrozenCatalog  BrainToolCatalog
	Manifest       runmanifest.Manifest
	SignedManifest runmanifest.SignedManifest
	ManagedSandbox *ManagedSandboxLaunchSpec
}

type LaunchPreparer struct {
	core       LaunchPreparationCore
	identities BrainToolCatalogIDAllocator
	resolver   RunLaunchInputResolver
	signer     RunManifestSigner
}

func NewLaunchPreparer(core LaunchPreparationCore, identities BrainToolCatalogIDAllocator, resolver RunLaunchInputResolver, signer RunManifestSigner) (*LaunchPreparer, error) {
	if core == nil {
		return nil, errors.New("launch preparation core client is required")
	}
	if identities == nil {
		return nil, errors.New("brain tool catalog identity allocator is required")
	}
	if resolver == nil {
		return nil, errors.New("run launch input resolver is required")
	}
	if signer == nil {
		return nil, errors.New("run manifest signer is required")
	}
	return &LaunchPreparer{core: core, identities: identities, resolver: resolver, signer: signer}, nil
}

func (preparer *LaunchPreparer) Prepare(ctx context.Context, scheduled ScheduledRunAttempt) (PreparedRunLaunch, error) {
	if ctx == nil {
		return PreparedRunLaunch{}, errors.New("launch preparation context is required")
	}
	if err := validateScheduledLaunchAuthority(scheduled); err != nil {
		return PreparedRunLaunch{}, err
	}
	inputs, err := preparer.resolver.ResolveRunLaunch(ctx, scheduled)
	if err != nil {
		return PreparedRunLaunch{}, fmt.Errorf("resolve run launch inputs: %w", err)
	}
	proposal, err := BuildExecutorCatalog(inputs.ExecutorCatalogPolicy)
	if err != nil {
		return PreparedRunLaunch{}, err
	}
	var catalogID string
	var resumedCatalog *BrainToolCatalog
	if inputs.PreviousCheckpoint != nil {
		if inputs.PreviousBrainToolCatalog == nil {
			return PreparedRunLaunch{}, errors.New("previous checkpoint requires its bound brain tool catalog authority")
		}
		if err := validateResumeBrainToolCatalog(scheduled, inputs.PreviousCheckpoint, *inputs.PreviousBrainToolCatalog, proposal); err != nil {
			return PreparedRunLaunch{}, err
		}
		catalog := cloneBrainToolCatalog(*inputs.PreviousBrainToolCatalog)
		resumedCatalog = &catalog
		catalogID = catalog.CatalogID
	} else {
		if inputs.PreviousBrainToolCatalog != nil {
			return PreparedRunLaunch{}, errors.New("brain tool catalog authority cannot be supplied without a previous checkpoint")
		}
		catalogID, err = preparer.identities.AllocateBrainToolCatalogID()
		if err != nil {
			return PreparedRunLaunch{}, fmt.Errorf("allocate brain tool catalog identity: %w", err)
		}
		if err := validateUUIDIdentity("brain tool catalog ID", catalogID); err != nil {
			return PreparedRunLaunch{}, err
		}
	}
	executorMCP, err := runmanifest.ExecutorMCPFromCatalog(
		inputs.ExecutorMCPEndpoint,
		inputs.ExecutorMCPTLSIdentity,
		inputs.ExecutorMCPAudience,
		proposal.ContractVersion,
		catalogID,
		proposal.Catalog,
	)
	if err != nil {
		return PreparedRunLaunch{}, err
	}
	claim := scheduled.Claim
	previousCheckpoint := clonePreviousCheckpoint(inputs.PreviousCheckpoint)
	toolPack := managedToolPackAuthority(inputs.ManagedSandbox)
	manifest := runmanifest.Manifest{
		ManifestVersion: runmanifest.CurrentVersion, CanonicalizerVersion: runmanifest.Canonicalizer,
		WorkspaceID: claim.Run.WorkspaceID, SessionID: claim.Run.SessionID, RunID: claim.Run.RunID,
		RunAttemptID: claim.RunAttempt.RunAttemptID, RunAttemptGeneration: claim.RunAttempt.Generation,
		HolderID: claim.RunAttempt.HolderID, Prompt: inputs.Prompt,
		PreviousCheckpoint: previousCheckpoint, CodexRuntimeManifestDigest: inputs.CodexRuntimeManifestDigest,
		Model: inputs.Model, ExecutorMCP: executorMCP,
		ExecutorPolicy: runmanifest.ExecutorPolicy{
			Version: proposal.PolicyVersion, ContextDigest: hex.EncodeToString(proposal.PolicyContextDigest[:]),
		},
		ToolPack:       toolPack,
		ManagedSandbox: managedSandboxAuthority(inputs.ManagedSandbox),
		Limits:         inputs.Limits, CheckpointAllowlistVersion: inputs.CheckpointAllowlistVersion,
		WorkerImageDigest: inputs.WorkerImageDigest, ExpectedServiceAccount: inputs.ExpectedServiceAccount,
		ControllerCallback: runmanifest.ControllerCallback{
			Endpoint: inputs.ControllerCallbackEndpoint, TLSIdentity: inputs.ControllerCallbackIdentity,
			Audience: inputs.ControllerCallbackAudience, HolderID: claim.RunAttempt.HolderID,
		},
	}
	signed, err := preparer.signer.SignRunManifest(manifest)
	if err != nil {
		return PreparedRunLaunch{}, fmt.Errorf("sign run manifest: %w", err)
	}
	if resumedCatalog != nil {
		return PreparedRunLaunch{
			Scheduled: scheduled, FrozenCatalog: *resumedCatalog, Manifest: manifest, SignedManifest: signed,
			ManagedSandbox: cloneManagedSandboxLaunchSpec(inputs.ManagedSandbox),
		}, nil
	}
	freezeRequest := FreezeBrainToolCatalogRequest{
		CatalogID: catalogID, WorkspaceID: claim.Run.WorkspaceID, SessionID: claim.Run.SessionID,
		RunID: claim.Run.RunID, RunAttemptID: claim.RunAttempt.RunAttemptID,
		HolderID: claim.RunAttempt.HolderID, RunAttemptGeneration: claim.RunAttempt.Generation,
		ExpectedRunVersion: claim.Run.Version, ExpectedRunAttemptVersion: claim.RunAttempt.Version,
		ContractVersion: proposal.ContractVersion, CanonicalizerVersion: proposal.CanonicalizerVersion,
		CanonicalCatalog: proposal.CanonicalCatalog, CatalogDigest: proposal.CatalogDigest,
		PolicyVersion: proposal.PolicyVersion, PolicyContextDigest: proposal.PolicyContextDigest,
	}
	frozen, err := preparer.freezeExactly(ctx, freezeRequest)
	if err != nil {
		return PreparedRunLaunch{}, fmt.Errorf("freeze brain tool catalog: %w", err)
	}
	if err := validatePreparedFrozenCatalog(frozen.Catalog, freezeRequest); err != nil {
		return PreparedRunLaunch{}, err
	}
	return PreparedRunLaunch{
		Scheduled: scheduled, FrozenCatalog: frozen.Catalog, Manifest: manifest, SignedManifest: signed,
		ManagedSandbox: cloneManagedSandboxLaunchSpec(inputs.ManagedSandbox),
	}, nil
}

func cloneManagedSandboxLaunchSpec(source *ManagedSandboxLaunchSpec) *ManagedSandboxLaunchSpec {
	if source == nil {
		return nil
	}
	copy := *source
	return &copy
}

func managedToolPackAuthority(source *ManagedSandboxLaunchSpec) *runmanifest.ToolPackAuthority {
	if source == nil {
		return nil
	}
	return &runmanifest.ToolPackAuthority{
		PackID: source.PackID, PackSetDigest: source.PackSetDigest,
		SkillSHA256: source.SkillSHA256,
	}
}

func validateResumeBrainToolCatalog(scheduled ScheduledRunAttempt, checkpoint *runmanifest.PreviousCheckpoint, catalog BrainToolCatalog, proposal ExecutorCatalogProposal) error {
	if checkpoint == nil {
		return errors.New("previous checkpoint is required for a resume catalog")
	}
	for field, value := range map[string]string{
		"brain tool catalog ID": catalog.CatalogID, "catalog workspace ID": catalog.WorkspaceID,
		"catalog session ID": catalog.SessionID, "catalog created run ID": catalog.CreatedRunID,
		"catalog created attempt ID": catalog.CreatedRunAttemptID,
	} {
		if err := validateUUIDIdentity(field, value); err != nil {
			return err
		}
	}
	claim := scheduled.Claim
	if catalog.WorkspaceID != claim.Run.WorkspaceID || catalog.SessionID != claim.Run.SessionID ||
		catalog.ThreadID != checkpoint.ThreadID || catalog.ThreadID == "" ||
		catalog.ContractVersion != proposal.ContractVersion || catalog.CanonicalizerVersion != proposal.CanonicalizerVersion ||
		!bytes.Equal(catalog.CanonicalCatalog, proposal.CanonicalCatalog) || catalog.CatalogDigest != proposal.CatalogDigest ||
		catalog.PolicyVersion != proposal.PolicyVersion || catalog.PolicyContextDigest != proposal.PolicyContextDigest ||
		checkpoint.CatalogDigest != hex.EncodeToString(catalog.CatalogDigest[:]) ||
		catalog.CreatedAttemptGeneration < 1 || catalog.CreatedHolderID == "" || catalog.CreatedRunVersion < 1 ||
		catalog.CreatedAttemptVersion < 1 || catalog.Version < 1 || catalog.CreatedAt.IsZero() || catalog.UpdatedAt.IsZero() {
		return errors.New("previous checkpoint brain tool catalog is not the exact frozen authority for this session and policy")
	}
	return nil
}

func cloneBrainToolCatalog(source BrainToolCatalog) BrainToolCatalog {
	copy := source
	copy.CanonicalCatalog = append(json.RawMessage(nil), source.CanonicalCatalog...)
	return copy
}

func validatePreparedFrozenCatalog(catalog BrainToolCatalog, request FreezeBrainToolCatalogRequest) error {
	if catalog.CatalogID != request.CatalogID || catalog.WorkspaceID != request.WorkspaceID || catalog.SessionID != request.SessionID ||
		catalog.CreatedRunID != request.RunID || catalog.CreatedRunAttemptID != request.RunAttemptID ||
		catalog.CreatedAttemptGeneration != request.RunAttemptGeneration || catalog.CreatedHolderID != request.HolderID ||
		catalog.CreatedRunVersion != request.ExpectedRunVersion || catalog.CreatedAttemptVersion != request.ExpectedRunAttemptVersion ||
		catalog.ThreadID != "" || catalog.ContractVersion != request.ContractVersion ||
		catalog.CanonicalizerVersion != request.CanonicalizerVersion || !bytes.Equal(catalog.CanonicalCatalog, request.CanonicalCatalog) ||
		catalog.CatalogDigest != request.CatalogDigest || catalog.PolicyVersion != request.PolicyVersion ||
		catalog.PolicyContextDigest != request.PolicyContextDigest || catalog.Version < 1 ||
		catalog.CreatedAt.IsZero() || catalog.UpdatedAt.IsZero() {
		return errors.New("frozen catalog response is not the exact unbound catalog for the scheduled attempt")
	}
	return nil
}

func clonePreviousCheckpoint(source *runmanifest.PreviousCheckpoint) *runmanifest.PreviousCheckpoint {
	if source == nil {
		return nil
	}
	copy := *source
	return &copy
}

func (preparer *LaunchPreparer) freezeExactly(ctx context.Context, request FreezeBrainToolCatalogRequest) (FreezeBrainToolCatalogResult, error) {
	result, err := preparer.core.FreezeBrainToolCatalog(ctx, request)
	if err == nil {
		return result, nil
	}
	var commandError *CoreCommandError
	if errors.As(err, &commandError) || ctx.Err() != nil {
		return FreezeBrainToolCatalogResult{}, err
	}
	return preparer.core.FreezeBrainToolCatalog(ctx, request)
}

func validateScheduledLaunchAuthority(scheduled ScheduledRunAttempt) error {
	dispatch := scheduled.Dispatch
	claim := scheduled.Claim
	if dispatch.RunID == "" || dispatch.WorkspaceID == "" || dispatch.SessionID == "" ||
		claim.Run.RunID != dispatch.RunID || claim.Run.WorkspaceID != dispatch.WorkspaceID || claim.Run.SessionID != dispatch.SessionID {
		return errors.New("scheduled run claim does not match its dispatch identity")
	}
	if claim.Run.Status != "starting" || claim.Run.Version < 1 || claim.Run.CurrentAttemptGeneration < 1 ||
		claim.RunAttempt.RunAttemptID == "" || claim.RunAttempt.RunID != claim.Run.RunID ||
		claim.RunAttempt.Generation != claim.Run.CurrentAttemptGeneration || claim.RunAttempt.Status != "leased" ||
		claim.RunAttempt.HolderID == "" || claim.RunAttempt.Version < 1 || claim.RunAttempt.TurnStartedAt != nil {
		return errors.New("scheduled run claim is not a live pre-turn attempt authority tuple")
	}
	return nil
}

type Ed25519ManifestSigner struct {
	keyID      string
	privateKey ed25519.PrivateKey
}

func NewEd25519ManifestSigner(keyID string, privateKey ed25519.PrivateKey) (*Ed25519ManifestSigner, error) {
	if keyID == "" || len(keyID) > 256 || !utf8.ValidString(keyID) || strings.ContainsRune(keyID, 0) {
		return nil, errors.New("run manifest signing key ID must contain between 1 and 256 valid UTF-8 bytes without NUL")
	}
	canonical, err := validateCanonicalEd25519PrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	return &Ed25519ManifestSigner{keyID: keyID, privateKey: canonical}, nil
}

func (signer *Ed25519ManifestSigner) SignRunManifest(manifest runmanifest.Manifest) (runmanifest.SignedManifest, error) {
	if signer == nil {
		return runmanifest.SignedManifest{}, errors.New("run manifest signer is required")
	}
	return runmanifest.Sign(manifest, signer.keyID, signer.privateKey)
}
