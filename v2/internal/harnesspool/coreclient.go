// Package harnesspool contains the controller-side boundaries for claiming
// and supervising one run attempt. It never links coredb or writes PostgreSQL.
package harnesspool

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	checkpointartifact "github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

const (
	maxCoreCommandRequestBytes  = 18 * 1024 * 1024
	maxCoreCommandResponseBytes = 2 * 1024 * 1024
)

type CoreClient struct {
	baseURL    *url.URL
	httpClient *http.Client
}

var _ RunLaunchStateSource = (*CoreClient)(nil)

type CoreCommandError struct {
	HTTPStatus        int
	Code              string
	Message           string
	CurrentVersion    int64
	CurrentGeneration int64
}

func (err *CoreCommandError) Error() string {
	if err == nil {
		return "<nil>"
	}
	message := strings.TrimSpace(err.Message)
	if message == "" {
		message = err.Code
	}
	return fmt.Sprintf("core command %s: %s", err.Code, message)
}

type TransitionRecord struct {
	EventID            string
	ProducerInstanceID string
	ProducerSeq        int64
	OutboxID           string
}

type Run struct {
	RunID                    string
	WorkspaceID              string
	SessionID                string
	ActorID                  string
	Status                   string
	CurrentAttemptGeneration int64
	NextEventSeq             int64
	Version                  int64
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

type RunAttempt struct {
	RunAttemptID     string
	RunID            string
	Generation       int64
	Status           string
	TurnStartedAt    *time.Time
	TerminalThreadID string
	TerminalTurnID   string
	HolderID         string
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Lease struct {
	HolderID   string
	Generation int64
	ExpiresAt  time.Time
	AcquiredAt time.Time
	RenewedAt  time.Time
}

type ClaimRunAttemptRequest struct {
	RunID              string
	RunAttemptID       string
	HolderID           string
	ExpectedRunVersion int64
	LeaseTTL           time.Duration
	Record             TransitionRecord
}

type ClaimRunAttemptResult struct {
	Run          Run
	RunAttempt   RunAttempt
	SessionLease Lease
	AttemptLease Lease
	Created      bool
	Reclaimed    bool
}

type RenewRunAttemptRequest struct {
	SessionID            string
	RunID                string
	RunAttemptID         string
	HolderID             string
	RunAttemptGeneration int64
	LeaseTTL             time.Duration
}

type RenewRunAttemptResult struct {
	SessionLease Lease
	AttemptLease Lease
}

type MarkTurnAcceptedRequest struct {
	RunID                     string
	RunAttemptID              string
	HolderID                  string
	RunAttemptGeneration      int64
	ExpectedRunVersion        int64
	ExpectedRunAttemptVersion int64
	Record                    TransitionRecord
}

type MarkTurnAcceptedResult struct {
	Run        Run
	RunAttempt RunAttempt
	Changed    bool
}

type BeginRunFinalizationRequest struct {
	RunID                     string
	RunAttemptID              string
	HolderID                  string
	RunAttemptGeneration      int64
	ExpectedRunVersion        int64
	ExpectedRunAttemptVersion int64
	ThreadID                  string
	TurnID                    string
	Record                    TransitionRecord
}

type BeginRunFinalizationResult struct {
	Run        Run
	RunAttempt RunAttempt
	Changed    bool
}

type CheckpointCommit struct {
	CheckpointID               string
	BrainToolCatalogID         string
	ThreadID                   string
	TurnID                     string
	ManifestDigest             [32]byte
	CatalogDigest              [32]byte
	Object                     EventObjectPointer
	CodexRuntimeManifestDigest [32]byte
	CheckpointAllowlistVersion int64
}

type CommitCheckpointRequest struct {
	RunID                     string
	RunAttemptID              string
	HolderID                  string
	RunAttemptGeneration      int64
	ExpectedRunVersion        int64
	ExpectedRunAttemptVersion int64
	Checkpoint                CheckpointCommit
	Record                    TransitionRecord
}

type CommittedCheckpoint struct {
	CheckpointID               string
	WorkspaceID                string
	SessionID                  string
	RunID                      string
	RunAttemptID               string
	RunAttemptGeneration       int64
	BrainToolCatalogID         string
	ThreadID                   string
	TurnID                     string
	ManifestDigest             [32]byte
	CatalogDigest              [32]byte
	Object                     EventObjectPointer
	CodexRuntimeManifestDigest [32]byte
	CheckpointAllowlistVersion int64
	CreatedAt                  time.Time
}

type CommitCheckpointResult struct {
	Run            Run
	RunAttempt     RunAttempt
	Checkpoint     CommittedCheckpoint
	SessionVersion int64
	Created        bool
}

type EventObjectPointer struct {
	ObjectID  string
	SHA256    [32]byte
	Size      int64
	MediaType string
}

type AttemptEvent struct {
	EventID            string
	ProducerInstanceID string
	ProducerSeq        int64
	Source             string
	Kind               string
	SchemaVersion      int
	Payload            json.RawMessage
	Object             *EventObjectPointer
}

type AppendAttemptEventsRequest struct {
	RunID                string
	RunAttemptID         string
	HolderID             string
	RunAttemptGeneration int64
	OutboxID             string
	Events               []AttemptEvent
}

type AppendedAttemptEvent struct {
	EventID            string
	ProducerInstanceID string
	ProducerSeq        int64
	RunSeq             int64
	Duplicate          bool
}

type AppendAttemptEventsResult struct {
	Events   []AppendedAttemptEvent
	NewCount int
}

type BrainToolCatalog struct {
	CatalogID                string
	WorkspaceID              string
	SessionID                string
	CreatedRunID             string
	CreatedRunAttemptID      string
	CreatedAttemptGeneration int64
	CreatedHolderID          string
	CreatedRunVersion        int64
	CreatedAttemptVersion    int64
	ThreadID                 string
	ContractVersion          string
	CanonicalizerVersion     string
	CanonicalCatalog         json.RawMessage
	CatalogDigest            [32]byte
	PolicyVersion            string
	PolicyContextDigest      [32]byte
	Version                  int64
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

type FreezeBrainToolCatalogRequest struct {
	CatalogID                 string
	WorkspaceID               string
	SessionID                 string
	RunID                     string
	RunAttemptID              string
	HolderID                  string
	RunAttemptGeneration      int64
	ExpectedRunVersion        int64
	ExpectedRunAttemptVersion int64
	ContractVersion           string
	CanonicalizerVersion      string
	CanonicalCatalog          json.RawMessage
	CatalogDigest             [32]byte
	PolicyVersion             string
	PolicyContextDigest       [32]byte
}

type FreezeBrainToolCatalogResult struct {
	Catalog BrainToolCatalog
	Created bool
}

type BindBrainThreadCatalogRequest struct {
	CatalogID                 string
	RunID                     string
	RunAttemptID              string
	HolderID                  string
	RunAttemptGeneration      int64
	ExpectedRunVersion        int64
	ExpectedRunAttemptVersion int64
	ExpectedCatalogVersion    int64
	ThreadID                  string
}

type BindBrainThreadCatalogResult struct {
	Catalog BrainToolCatalog
	Changed bool
}

func NewCoreClient(baseURL string, httpClient *http.Client) (*CoreClient, error) {
	if httpClient == nil {
		return nil, errors.New("core HTTP client is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse core base URL: %w", err)
	}
	if (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("core base URL must be an absolute HTTP(S) origin without userinfo, query, or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, errors.New("core base URL must not contain a path")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("cleartext core base URL is allowed only on loopback")
	}
	parsed.Path = ""
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &CoreClient{baseURL: parsed, httpClient: &clientCopy}, nil
}

func (client *CoreClient) ClaimRunAttempt(ctx context.Context, request ClaimRunAttemptRequest) (ClaimRunAttemptResult, error) {
	contractRequest := corecontract.ClaimRunAttemptRequest{
		RunID: request.RunID, RunAttemptID: request.RunAttemptID, HolderID: request.HolderID,
		ExpectedRunVersion: request.ExpectedRunVersion, LeaseTTLMillis: request.LeaseTTL.Milliseconds(),
		Record: contractTransitionRecord(request.Record),
	}
	var response corecontract.ClaimRunAttemptResponse
	if err := client.post(ctx, corecontract.ClaimRunAttemptPath, contractRequest, &response); err != nil {
		return ClaimRunAttemptResult{}, err
	}
	result := ClaimRunAttemptResult{
		Run: contractRun(response.Run), RunAttempt: contractRunAttempt(response.RunAttempt),
		SessionLease: contractLease(response.SessionLease), AttemptLease: contractLease(response.AttemptLease),
		Created: response.Created, Reclaimed: response.Reclaimed,
	}
	if err := validateClaimResult(request, result); err != nil {
		return ClaimRunAttemptResult{}, fmt.Errorf("validate core claim response: %w", err)
	}
	return result, nil
}

func (client *CoreClient) ResolveRunLaunchState(ctx context.Context, scheduled ScheduledRunAttempt) (RunLaunchState, error) {
	if err := validateScheduledLaunchAuthority(scheduled); err != nil {
		return RunLaunchState{}, err
	}
	claim := scheduled.Claim
	request := corecontract.ResolveRunLaunchStateRequest{
		WorkspaceID: claim.Run.WorkspaceID, SessionID: claim.Run.SessionID, RunID: claim.Run.RunID,
		RunAttemptID: claim.RunAttempt.RunAttemptID, HolderID: claim.RunAttempt.HolderID,
		RunAttemptGeneration: claim.RunAttempt.Generation, ExpectedRunVersion: claim.Run.Version,
		ExpectedRunAttemptVersion: claim.RunAttempt.Version,
	}
	var response corecontract.ResolveRunLaunchStateResponse
	if err := client.post(ctx, corecontract.ResolveRunLaunchStatePath, request, &response); err != nil {
		return RunLaunchState{}, err
	}
	if response.WorkspaceID != request.WorkspaceID || response.SessionID != request.SessionID || response.RunID != request.RunID ||
		response.RunAttemptID != request.RunAttemptID || response.HolderID != request.HolderID ||
		response.RunAttemptGeneration != request.RunAttemptGeneration || response.RunVersion != request.ExpectedRunVersion ||
		response.RunAttemptVersion != request.ExpectedRunAttemptVersion {
		return RunLaunchState{}, errors.New("core launch-state response does not match the requested attempt authority tuple")
	}
	prompt, err := clientRunLaunchObjectPointer("prompt", response.Prompt)
	if err != nil {
		return RunLaunchState{}, fmt.Errorf("validate core launch-state response: %w", err)
	}
	policyDigest, err := decodeClientSHA256(response.ExecutorPolicy.ContextDigest)
	if err != nil {
		return RunLaunchState{}, fmt.Errorf("validate core launch-state response policy digest: %w", err)
	}
	policy := ExecutorCatalogPolicy{
		Version: response.ExecutorPolicy.Version, ContextDigest: policyDigest,
		AllowedTools: append([]string(nil), response.ExecutorPolicy.AllowedTools...),
	}
	if !slices.IsSorted(policy.AllowedTools) {
		return RunLaunchState{}, errors.New("validate core launch-state response: allowed tools are not sorted")
	}
	if _, err := BuildExecutorCatalog(policy); err != nil {
		return RunLaunchState{}, fmt.Errorf("validate core launch-state response policy: %w", err)
	}

	state := RunLaunchState{Prompt: prompt, ExecutorPolicy: policy}
	if response.PreviousCheckpoint == nil {
		return state, nil
	}
	checkpoint := response.PreviousCheckpoint
	if err := validateUUIDIdentity("checkpoint ID", checkpoint.CheckpointID); err != nil {
		return RunLaunchState{}, fmt.Errorf("validate core launch-state response: %w", err)
	}
	if err := validateUUIDIdentity("checkpoint run ID", checkpoint.RunID); err != nil {
		return RunLaunchState{}, fmt.Errorf("validate core launch-state response: %w", err)
	}
	if err := validateUUIDIdentity("checkpoint run attempt ID", checkpoint.RunAttemptID); err != nil {
		return RunLaunchState{}, fmt.Errorf("validate core launch-state response: %w", err)
	}
	if checkpoint.RunAttemptGeneration < 1 || checkpoint.RunAttemptGeneration > 1<<53-1 {
		return RunLaunchState{}, errors.New("validate core launch-state response: checkpoint attempt generation is not a positive safe integer")
	}
	if !validClientProtocolText(checkpoint.ThreadID, 256) {
		return RunLaunchState{}, errors.New("validate core launch-state response: checkpoint thread ID is invalid")
	}
	if !validClientProtocolText(checkpoint.TurnID, 256) {
		return RunLaunchState{}, errors.New("validate core launch-state response: checkpoint turn ID is invalid")
	}
	manifestDigest, err := decodeClientSHA256(checkpoint.ManifestDigest)
	if err != nil {
		return RunLaunchState{}, fmt.Errorf("validate core launch-state response manifest digest: %w", err)
	}
	catalogDigest, err := decodeClientSHA256(checkpoint.CatalogDigest)
	if err != nil {
		return RunLaunchState{}, fmt.Errorf("validate core launch-state response catalog digest: %w", err)
	}
	catalog, err := clientBrainToolCatalog(checkpoint.Catalog)
	if err != nil {
		return RunLaunchState{}, fmt.Errorf("validate core launch-state response checkpoint catalog: %w", err)
	}
	if catalog.CatalogDigest != catalogDigest || catalog.WorkspaceID != claim.Run.WorkspaceID ||
		catalog.SessionID != claim.Run.SessionID || catalog.ThreadID != checkpoint.ThreadID {
		return RunLaunchState{}, errors.New("validate core launch-state response: checkpoint catalog authority is inconsistent")
	}
	runtimeDigest, err := decodeClientSHA256(checkpoint.CodexRuntimeManifestDigest)
	if err != nil {
		return RunLaunchState{}, fmt.Errorf("validate core launch-state response runtime digest: %w", err)
	}
	object, err := clientRunLaunchObjectPointer("previous checkpoint object", checkpoint.Object)
	if err != nil {
		return RunLaunchState{}, fmt.Errorf("validate core launch-state response: %w", err)
	}
	if object.MediaType != checkpointartifact.ArtifactMediaType || object.SizeBytes > checkpointartifact.MaximumArtifactBytes {
		return RunLaunchState{}, errors.New("validate core launch-state response: checkpoint object does not use artifact v1")
	}
	if checkpoint.CheckpointAllowlistVersion < 1 || checkpoint.CheckpointAllowlistVersion > 1<<53-1 {
		return RunLaunchState{}, errors.New("validate core launch-state response: checkpoint allowlist version is not a positive safe integer")
	}
	state.PreviousCheckpoint = &RunLaunchCheckpoint{
		Checkpoint: runmanifest.PreviousCheckpoint{
			CheckpointID: checkpoint.CheckpointID, RunID: checkpoint.RunID,
			RunAttemptID: checkpoint.RunAttemptID, RunAttemptGeneration: checkpoint.RunAttemptGeneration,
			ThreadID: checkpoint.ThreadID, TurnID: checkpoint.TurnID,
			ManifestDigest: hex.EncodeToString(manifestDigest[:]), CatalogDigest: hex.EncodeToString(catalogDigest[:]),
			CodexRuntimeManifestDigest: hex.EncodeToString(runtimeDigest[:]),
			CheckpointAllowlistVersion: checkpoint.CheckpointAllowlistVersion,
			Object:                     object,
		},
		Catalog: catalog,
	}
	return state, nil
}

func (client *CoreClient) RenewRunAttempt(ctx context.Context, request RenewRunAttemptRequest) (RenewRunAttemptResult, error) {
	contractRequest := corecontract.RenewRunAttemptRequest{
		SessionID: request.SessionID, RunID: request.RunID, RunAttemptID: request.RunAttemptID,
		HolderID: request.HolderID, RunAttemptGeneration: request.RunAttemptGeneration, LeaseTTLMillis: request.LeaseTTL.Milliseconds(),
	}
	var response corecontract.RenewRunAttemptResponse
	if err := client.post(ctx, corecontract.RenewRunAttemptPath(request.RunAttemptID), contractRequest, &response); err != nil {
		return RenewRunAttemptResult{}, err
	}
	result := RenewRunAttemptResult{SessionLease: contractLease(response.SessionLease), AttemptLease: contractLease(response.AttemptLease)}
	if !sameLeaseHolder(result.SessionLease, request.HolderID, request.RunAttemptGeneration) || !sameLeaseHolder(result.AttemptLease, request.HolderID, request.RunAttemptGeneration) {
		return RenewRunAttemptResult{}, errors.New("core renew response does not match the requested holder generation")
	}
	return result, nil
}

func (client *CoreClient) MarkTurnAccepted(ctx context.Context, request MarkTurnAcceptedRequest) (MarkTurnAcceptedResult, error) {
	contractRequest := corecontract.MarkTurnAcceptedRequest{
		RunID: request.RunID, RunAttemptID: request.RunAttemptID, HolderID: request.HolderID,
		RunAttemptGeneration: request.RunAttemptGeneration, ExpectedRunVersion: request.ExpectedRunVersion,
		ExpectedRunAttemptVersion: request.ExpectedRunAttemptVersion, Record: contractTransitionRecord(request.Record),
	}
	var response corecontract.MarkTurnAcceptedResponse
	if err := client.post(ctx, corecontract.MarkTurnAcceptedPath(request.RunAttemptID), contractRequest, &response); err != nil {
		return MarkTurnAcceptedResult{}, err
	}
	result := MarkTurnAcceptedResult{Run: contractRun(response.Run), RunAttempt: contractRunAttempt(response.RunAttempt), Changed: response.Changed}
	if result.Run.RunID != request.RunID || result.RunAttempt.RunID != request.RunID || result.RunAttempt.RunAttemptID != request.RunAttemptID ||
		result.Run.CurrentAttemptGeneration != request.RunAttemptGeneration || result.RunAttempt.Generation != request.RunAttemptGeneration || result.RunAttempt.HolderID != request.HolderID {
		return MarkTurnAcceptedResult{}, errors.New("core turn-accepted response does not match the requested attempt identity")
	}
	return result, nil
}

func (client *CoreClient) BeginRunFinalization(ctx context.Context, request BeginRunFinalizationRequest) (BeginRunFinalizationResult, error) {
	contractRequest := corecontract.BeginRunFinalizationRequest{
		RunID: request.RunID, RunAttemptID: request.RunAttemptID, HolderID: request.HolderID,
		RunAttemptGeneration: request.RunAttemptGeneration, ExpectedRunVersion: request.ExpectedRunVersion,
		ExpectedRunAttemptVersion: request.ExpectedRunAttemptVersion, ThreadID: request.ThreadID,
		TurnID: request.TurnID, Record: contractTransitionRecord(request.Record),
	}
	var response corecontract.BeginRunFinalizationResponse
	if err := client.post(ctx, corecontract.BeginRunFinalizationPath(request.RunAttemptID), contractRequest, &response); err != nil {
		return BeginRunFinalizationResult{}, err
	}
	result := BeginRunFinalizationResult{
		Run: contractRun(response.Run), RunAttempt: contractRunAttempt(response.RunAttempt), Changed: response.Changed,
	}
	if result.Run.RunID != request.RunID || result.RunAttempt.RunID != request.RunID ||
		result.RunAttempt.RunAttemptID != request.RunAttemptID ||
		result.Run.CurrentAttemptGeneration != request.RunAttemptGeneration ||
		result.RunAttempt.Generation != request.RunAttemptGeneration || result.RunAttempt.HolderID != request.HolderID ||
		result.RunAttempt.TerminalThreadID != request.ThreadID || result.RunAttempt.TerminalTurnID != request.TurnID ||
		!((result.Run.Status == "finalizing" && result.RunAttempt.Status == "finalizing") ||
			(result.Run.Status == "completed" && result.RunAttempt.Status == "succeeded")) {
		return BeginRunFinalizationResult{}, errors.New("core begin-finalization response does not match the requested terminal attempt identity")
	}
	if result.Changed && (result.Run.Status != "finalizing" || result.RunAttempt.Status != "finalizing") {
		return BeginRunFinalizationResult{}, errors.New("core begin-finalization changed response is not finalizing")
	}
	return result, nil
}

func (client *CoreClient) CommitCheckpoint(ctx context.Context, request CommitCheckpointRequest) (CommitCheckpointResult, error) {
	checkpoint := request.Checkpoint
	contractRequest := corecontract.CommitCheckpointRequest{
		RunID: request.RunID, RunAttemptID: request.RunAttemptID, HolderID: request.HolderID,
		RunAttemptGeneration: request.RunAttemptGeneration, ExpectedRunVersion: request.ExpectedRunVersion,
		ExpectedRunAttemptVersion: request.ExpectedRunAttemptVersion,
		Checkpoint: corecontract.CheckpointCommit{
			CheckpointID: checkpoint.CheckpointID, BrainToolCatalogID: checkpoint.BrainToolCatalogID,
			ThreadID: checkpoint.ThreadID, TurnID: checkpoint.TurnID,
			ManifestDigest: hex.EncodeToString(checkpoint.ManifestDigest[:]),
			CatalogDigest:  hex.EncodeToString(checkpoint.CatalogDigest[:]),
			Object: corecontract.EventObjectPointer{
				ObjectID: checkpoint.Object.ObjectID, SHA256: hex.EncodeToString(checkpoint.Object.SHA256[:]),
				Size: checkpoint.Object.Size, MediaType: checkpoint.Object.MediaType,
			},
			CodexRuntimeManifestDigest: hex.EncodeToString(checkpoint.CodexRuntimeManifestDigest[:]),
			CheckpointAllowlistVersion: checkpoint.CheckpointAllowlistVersion,
		},
		Record: contractTransitionRecord(request.Record),
	}
	var response corecontract.CommitCheckpointResponse
	if err := client.post(ctx, corecontract.CommitCheckpointPath(request.RunAttemptID), contractRequest, &response); err != nil {
		return CommitCheckpointResult{}, err
	}
	committed, err := clientCommittedCheckpoint(response.Checkpoint)
	if err != nil {
		return CommitCheckpointResult{}, fmt.Errorf("validate core commit-checkpoint response: %w", err)
	}
	result := CommitCheckpointResult{
		Run: contractRun(response.Run), RunAttempt: contractRunAttempt(response.RunAttempt),
		Checkpoint: committed, SessionVersion: response.SessionVersion, Created: response.Created,
	}
	if result.Run.RunID != request.RunID || result.Run.Status != "completed" ||
		result.Run.CurrentAttemptGeneration != request.RunAttemptGeneration ||
		result.RunAttempt.RunAttemptID != request.RunAttemptID || result.RunAttempt.RunID != request.RunID ||
		result.RunAttempt.Generation != request.RunAttemptGeneration || result.RunAttempt.HolderID != request.HolderID ||
		result.RunAttempt.Status != "succeeded" || result.RunAttempt.TerminalThreadID != checkpoint.ThreadID ||
		result.RunAttempt.TerminalTurnID != checkpoint.TurnID || committed.WorkspaceID != result.Run.WorkspaceID ||
		committed.SessionID != result.Run.SessionID || result.SessionVersion < 1 ||
		!committedCheckpointMatchesRequest(committed, request) {
		return CommitCheckpointResult{}, errors.New("core commit-checkpoint response does not match the requested checkpoint and terminal attempt identity")
	}
	return result, nil
}

func (client *CoreClient) AppendAttemptEvents(ctx context.Context, request AppendAttemptEventsRequest) (AppendAttemptEventsResult, error) {
	contractEvents := make([]corecontract.AttemptEvent, len(request.Events))
	for index, event := range request.Events {
		contractEvents[index] = corecontract.AttemptEvent{
			EventID: event.EventID, ProducerInstanceID: event.ProducerInstanceID, ProducerSeq: event.ProducerSeq,
			Source: event.Source, Kind: event.Kind, SchemaVersion: event.SchemaVersion,
			Payload: append(json.RawMessage(nil), event.Payload...),
		}
		if event.Object != nil {
			contractEvents[index].Object = &corecontract.EventObjectPointer{
				ObjectID: event.Object.ObjectID, SHA256: hex.EncodeToString(event.Object.SHA256[:]),
				Size: event.Object.Size, MediaType: event.Object.MediaType,
			}
		}
	}
	contractRequest := corecontract.AppendAttemptEventsRequest{
		RunID: request.RunID, RunAttemptID: request.RunAttemptID, HolderID: request.HolderID,
		RunAttemptGeneration: request.RunAttemptGeneration, OutboxID: request.OutboxID, Events: contractEvents,
	}
	var response corecontract.AppendAttemptEventsResponse
	if err := client.post(ctx, corecontract.AppendAttemptEventsPath(request.RunAttemptID), contractRequest, &response); err != nil {
		return AppendAttemptEventsResult{}, err
	}
	if len(response.Events) != len(request.Events) {
		return AppendAttemptEventsResult{}, errors.New("core append-events response length differs from the request")
	}
	result := AppendAttemptEventsResult{Events: make([]AppendedAttemptEvent, len(response.Events)), NewCount: response.NewCount}
	newCount := 0
	for index, event := range response.Events {
		requested := request.Events[index]
		if event.EventID != requested.EventID || event.ProducerInstanceID != requested.ProducerInstanceID || event.ProducerSeq != requested.ProducerSeq || event.RunSeq < 1 {
			return AppendAttemptEventsResult{}, errors.New("core append-events response does not preserve producer identities")
		}
		if !event.Duplicate {
			newCount++
		}
		result.Events[index] = AppendedAttemptEvent{
			EventID: event.EventID, ProducerInstanceID: event.ProducerInstanceID, ProducerSeq: event.ProducerSeq,
			RunSeq: event.RunSeq, Duplicate: event.Duplicate,
		}
	}
	if response.NewCount != newCount {
		return AppendAttemptEventsResult{}, errors.New("core append-events newCount is inconsistent with duplicate flags")
	}
	return result, nil
}

func (client *CoreClient) FreezeBrainToolCatalog(ctx context.Context, request FreezeBrainToolCatalogRequest) (FreezeBrainToolCatalogResult, error) {
	contractRequest := corecontract.FreezeBrainToolCatalogRequest{
		CatalogID: request.CatalogID, WorkspaceID: request.WorkspaceID, SessionID: request.SessionID,
		RunID: request.RunID, RunAttemptID: request.RunAttemptID, HolderID: request.HolderID,
		RunAttemptGeneration: request.RunAttemptGeneration, ExpectedRunVersion: request.ExpectedRunVersion,
		ExpectedRunAttemptVersion: request.ExpectedRunAttemptVersion, ContractVersion: request.ContractVersion,
		CanonicalizerVersion: request.CanonicalizerVersion, CanonicalCatalog: append(json.RawMessage(nil), request.CanonicalCatalog...),
		CatalogDigest: hex.EncodeToString(request.CatalogDigest[:]), PolicyVersion: request.PolicyVersion,
		PolicyContextDigest: hex.EncodeToString(request.PolicyContextDigest[:]),
	}
	var response corecontract.FreezeBrainToolCatalogResponse
	if err := client.post(ctx, corecontract.FreezeBrainToolCatalogPath, contractRequest, &response); err != nil {
		return FreezeBrainToolCatalogResult{}, err
	}
	catalog, err := clientBrainToolCatalog(response.Catalog)
	if err != nil {
		return FreezeBrainToolCatalogResult{}, fmt.Errorf("validate core freeze-catalog response: %w", err)
	}
	if err := validateFrozenCatalogResponse(request, catalog); err != nil {
		return FreezeBrainToolCatalogResult{}, fmt.Errorf("validate core freeze-catalog response: %w", err)
	}
	return FreezeBrainToolCatalogResult{Catalog: catalog, Created: response.Created}, nil
}

func (client *CoreClient) BindBrainThreadCatalog(ctx context.Context, request BindBrainThreadCatalogRequest) (BindBrainThreadCatalogResult, error) {
	contractRequest := corecontract.BindBrainThreadCatalogRequest{
		CatalogID: request.CatalogID, RunID: request.RunID, RunAttemptID: request.RunAttemptID,
		HolderID: request.HolderID, RunAttemptGeneration: request.RunAttemptGeneration,
		ExpectedRunVersion: request.ExpectedRunVersion, ExpectedRunAttemptVersion: request.ExpectedRunAttemptVersion,
		ExpectedCatalogVersion: request.ExpectedCatalogVersion, ThreadID: request.ThreadID,
	}
	var response corecontract.BindBrainThreadCatalogResponse
	if err := client.post(ctx, corecontract.BindBrainThreadCatalogPath(request.CatalogID), contractRequest, &response); err != nil {
		return BindBrainThreadCatalogResult{}, err
	}
	catalog, err := clientBrainToolCatalog(response.Catalog)
	if err != nil {
		return BindBrainThreadCatalogResult{}, fmt.Errorf("validate core bind-catalog response: %w", err)
	}
	if catalog.CatalogID != request.CatalogID || catalog.CreatedRunID != request.RunID ||
		catalog.CreatedRunAttemptID != request.RunAttemptID || catalog.CreatedAttemptGeneration != request.RunAttemptGeneration ||
		catalog.CreatedHolderID != request.HolderID || catalog.CreatedRunVersion != request.ExpectedRunVersion ||
		catalog.CreatedAttemptVersion != request.ExpectedRunAttemptVersion || catalog.ThreadID != request.ThreadID || catalog.Version < 1 ||
		(response.Changed && (request.ExpectedCatalogVersion == math.MaxInt64 || catalog.Version != request.ExpectedCatalogVersion+1)) {
		return BindBrainThreadCatalogResult{}, errors.New("core bind-catalog response does not match the requested catalog and attempt identity")
	}
	return BindBrainThreadCatalogResult{Catalog: catalog, Changed: response.Changed}, nil
}

func (client *CoreClient) post(ctx context.Context, path string, command, destination any) error {
	if ctx == nil {
		return errors.New("core command context is required")
	}
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	// Preserve RFC 8785 documents embedded as json.RawMessage. The core
	// command transport is JSON, not HTML, so HTML-safe rewriting would only
	// corrupt the authority bytes that core is expected to freeze exactly.
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(command); err != nil {
		return fmt.Errorf("encode core command: %w", err)
	}
	if raw.Len() > maxCoreCommandRequestBytes {
		return errors.New("core command request exceeds size limit")
	}
	endpoint := *client.baseURL
	endpoint.Path = path
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(raw.Bytes()))
	if err != nil {
		return fmt.Errorf("construct core command: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	response, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("execute core command: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxCoreCommandResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read core command response: %w", err)
	}
	if len(body) > maxCoreCommandResponseBytes {
		return errors.New("core command response exceeds size limit")
	}
	if response.StatusCode != http.StatusOK {
		return decodeCoreCommandError(response.StatusCode, body)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode core command response: %w", err)
	}
	if err := finishJSON(decoder); err != nil {
		return fmt.Errorf("finish core command response: %w", err)
	}
	return nil
}

func decodeCoreCommandError(status int, body []byte) error {
	var response corecontract.ErrorResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil || finishJSON(decoder) != nil || response.Code == "" {
		return fmt.Errorf("core command returned HTTP %d with an invalid error envelope", status)
	}
	return &CoreCommandError{
		HTTPStatus: status, Code: response.Code, Message: response.Message,
		CurrentVersion: response.CurrentVersion, CurrentGeneration: response.CurrentGeneration,
	}
}

func finishJSON(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("additional JSON value")
	}
	return err
}

func validateClaimResult(request ClaimRunAttemptRequest, result ClaimRunAttemptResult) error {
	if result.Run.RunID != request.RunID || result.RunAttempt.RunID != request.RunID || result.RunAttempt.RunAttemptID != request.RunAttemptID || result.RunAttempt.HolderID != request.HolderID {
		return errors.New("claim identity differs from request")
	}
	generation := result.Run.CurrentAttemptGeneration
	if generation < 1 || result.RunAttempt.Generation != generation || !sameLeaseHolder(result.SessionLease, request.HolderID, generation) || !sameLeaseHolder(result.AttemptLease, request.HolderID, generation) {
		return errors.New("claim generation or lease holder tuple is inconsistent")
	}
	if result.Reclaimed && !result.Created {
		return errors.New("reclaimed claim must also be newly created")
	}
	return nil
}

func sameLeaseHolder(lease Lease, holderID string, generation int64) bool {
	return lease.HolderID == holderID && lease.Generation == generation && generation > 0 &&
		!lease.ExpiresAt.IsZero() && !lease.AcquiredAt.IsZero() && !lease.RenewedAt.IsZero()
}

func contractTransitionRecord(record TransitionRecord) corecontract.TransitionRecord {
	return corecontract.TransitionRecord{
		EventID: record.EventID, ProducerInstanceID: record.ProducerInstanceID,
		ProducerSeq: record.ProducerSeq, OutboxID: record.OutboxID,
	}
}

func contractRun(source corecontract.RunState) Run {
	return Run{
		RunID: source.RunID, WorkspaceID: source.WorkspaceID, SessionID: source.SessionID, ActorID: source.ActorID,
		Status: source.Status, CurrentAttemptGeneration: source.CurrentAttemptGeneration, NextEventSeq: source.NextEventSeq,
		Version: source.Version, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt,
	}
}

func contractRunAttempt(source corecontract.RunAttemptState) RunAttempt {
	return RunAttempt{
		RunAttemptID: source.RunAttemptID, RunID: source.RunID, Generation: source.Generation, Status: source.Status,
		TurnStartedAt: source.TurnStartedAt, TerminalThreadID: source.TerminalThreadID, TerminalTurnID: source.TerminalTurnID,
		HolderID: source.HolderID, Version: source.Version,
		CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt,
	}
}

func clientCommittedCheckpoint(source corecontract.CheckpointState) (CommittedCheckpoint, error) {
	manifestDigest, err := decodeClientSHA256(source.ManifestDigest)
	if err != nil {
		return CommittedCheckpoint{}, fmt.Errorf("manifest digest: %w", err)
	}
	catalogDigest, err := decodeClientSHA256(source.CatalogDigest)
	if err != nil {
		return CommittedCheckpoint{}, fmt.Errorf("catalog digest: %w", err)
	}
	objectDigest, err := decodeClientSHA256(source.Object.SHA256)
	if err != nil {
		return CommittedCheckpoint{}, fmt.Errorf("object digest: %w", err)
	}
	runtimeDigest, err := decodeClientSHA256(source.CodexRuntimeManifestDigest)
	if err != nil {
		return CommittedCheckpoint{}, fmt.Errorf("runtime manifest digest: %w", err)
	}
	for field, value := range map[string]string{
		"checkpoint ID": source.CheckpointID, "workspace ID": source.WorkspaceID,
		"session ID": source.SessionID, "run ID": source.RunID,
		"run attempt ID": source.RunAttemptID, "brain tool catalog ID": source.BrainToolCatalogID,
		"checkpoint object ID": source.Object.ObjectID,
	} {
		if err := validateUUIDIdentity(field, value); err != nil {
			return CommittedCheckpoint{}, err
		}
	}
	if source.RunAttemptGeneration < 1 || source.RunAttemptGeneration > 1<<53-1 ||
		!validClientProtocolText(source.ThreadID, 256) || !validClientProtocolText(source.TurnID, 256) ||
		source.Object.Size < 1 || source.Object.Size > checkpointartifact.MaximumArtifactBytes ||
		source.Object.MediaType != checkpointartifact.ArtifactMediaType ||
		source.CheckpointAllowlistVersion < 1 || source.CheckpointAllowlistVersion > 1<<53-1 || source.CreatedAt.IsZero() {
		return CommittedCheckpoint{}, errors.New("committed checkpoint response contains an invalid bounded authority field")
	}
	return CommittedCheckpoint{
		CheckpointID: source.CheckpointID, WorkspaceID: source.WorkspaceID, SessionID: source.SessionID,
		RunID: source.RunID, RunAttemptID: source.RunAttemptID, RunAttemptGeneration: source.RunAttemptGeneration,
		BrainToolCatalogID: source.BrainToolCatalogID, ThreadID: source.ThreadID, TurnID: source.TurnID,
		ManifestDigest: manifestDigest, CatalogDigest: catalogDigest,
		Object: EventObjectPointer{
			ObjectID: source.Object.ObjectID, SHA256: objectDigest,
			Size: source.Object.Size, MediaType: source.Object.MediaType,
		},
		CodexRuntimeManifestDigest: runtimeDigest,
		CheckpointAllowlistVersion: source.CheckpointAllowlistVersion, CreatedAt: source.CreatedAt,
	}, nil
}

func committedCheckpointMatchesRequest(checkpoint CommittedCheckpoint, request CommitCheckpointRequest) bool {
	want := request.Checkpoint
	return checkpoint.CheckpointID == want.CheckpointID && checkpoint.RunID == request.RunID && checkpoint.RunAttemptID == request.RunAttemptID &&
		checkpoint.RunAttemptGeneration == request.RunAttemptGeneration && checkpoint.BrainToolCatalogID == want.BrainToolCatalogID &&
		checkpoint.ThreadID == want.ThreadID && checkpoint.TurnID == want.TurnID &&
		checkpoint.ManifestDigest == want.ManifestDigest && checkpoint.CatalogDigest == want.CatalogDigest &&
		checkpoint.Object == want.Object && checkpoint.CodexRuntimeManifestDigest == want.CodexRuntimeManifestDigest &&
		checkpoint.CheckpointAllowlistVersion == want.CheckpointAllowlistVersion
}

func contractLease(source corecontract.LeaseState) Lease {
	return Lease{
		HolderID: source.HolderID, Generation: source.Generation, ExpiresAt: source.ExpiresAt,
		AcquiredAt: source.AcquiredAt, RenewedAt: source.RenewedAt,
	}
}

func clientBrainToolCatalog(source corecontract.BrainToolCatalogState) (BrainToolCatalog, error) {
	catalogDigest, err := decodeClientSHA256(source.CatalogDigest)
	if err != nil {
		return BrainToolCatalog{}, fmt.Errorf("catalog digest: %w", err)
	}
	policyContextDigest, err := decodeClientSHA256(source.PolicyContextDigest)
	if err != nil {
		return BrainToolCatalog{}, fmt.Errorf("policy context digest: %w", err)
	}
	parsed, err := braincatalog.ParseCanonical(source.CanonicalCatalog, braincatalog.DefaultLimits())
	if err != nil {
		return BrainToolCatalog{}, fmt.Errorf("canonical catalog: %w", err)
	}
	if source.CanonicalizerVersion != braincatalog.CatalogCanonicalizer || parsed.DigestSHA256() != catalogDigest {
		return BrainToolCatalog{}, errors.New("canonical catalog does not match its canonicalizer and digest")
	}
	for field, value := range map[string]string{
		"catalog ID": source.CatalogID, "workspace ID": source.WorkspaceID, "session ID": source.SessionID,
		"created run ID": source.CreatedRunID, "created run attempt ID": source.CreatedRunAttemptID,
	} {
		if err := validateUUIDIdentity(field, value); err != nil {
			return BrainToolCatalog{}, err
		}
	}
	if source.CreatedAttemptGeneration < 1 || source.CreatedRunVersion < 1 || source.CreatedAttemptVersion < 1 ||
		source.Version < 1 || source.CreatedHolderID == "" || source.ContractVersion == "" || source.PolicyVersion == "" ||
		source.CreatedAt.IsZero() || source.UpdatedAt.IsZero() || policyContextDigest == ([32]byte{}) {
		return BrainToolCatalog{}, errors.New("brain tool catalog response contains an incomplete authority fingerprint")
	}
	return BrainToolCatalog{
		CatalogID: source.CatalogID, WorkspaceID: source.WorkspaceID, SessionID: source.SessionID,
		CreatedRunID: source.CreatedRunID, CreatedRunAttemptID: source.CreatedRunAttemptID,
		CreatedAttemptGeneration: source.CreatedAttemptGeneration, CreatedHolderID: source.CreatedHolderID,
		CreatedRunVersion: source.CreatedRunVersion, CreatedAttemptVersion: source.CreatedAttemptVersion,
		ThreadID: source.ThreadID, ContractVersion: source.ContractVersion,
		CanonicalizerVersion: source.CanonicalizerVersion, CanonicalCatalog: append(json.RawMessage(nil), source.CanonicalCatalog...),
		CatalogDigest: catalogDigest, PolicyVersion: source.PolicyVersion, PolicyContextDigest: policyContextDigest,
		Version: source.Version, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt,
	}, nil
}

func validateFrozenCatalogResponse(request FreezeBrainToolCatalogRequest, catalog BrainToolCatalog) error {
	if catalog.CatalogID != request.CatalogID || catalog.WorkspaceID != request.WorkspaceID || catalog.SessionID != request.SessionID ||
		catalog.CreatedRunID != request.RunID || catalog.CreatedRunAttemptID != request.RunAttemptID ||
		catalog.CreatedAttemptGeneration != request.RunAttemptGeneration || catalog.CreatedHolderID != request.HolderID ||
		catalog.CreatedRunVersion != request.ExpectedRunVersion || catalog.CreatedAttemptVersion != request.ExpectedRunAttemptVersion ||
		catalog.ContractVersion != request.ContractVersion || catalog.CanonicalizerVersion != request.CanonicalizerVersion ||
		!bytes.Equal(catalog.CanonicalCatalog, request.CanonicalCatalog) || catalog.CatalogDigest != request.CatalogDigest ||
		catalog.PolicyVersion != request.PolicyVersion || catalog.PolicyContextDigest != request.PolicyContextDigest || catalog.Version < 1 {
		return errors.New("catalog fingerprint or attempt identity differs from request")
	}
	return nil
}

func decodeClientSHA256(value string) ([32]byte, error) {
	var digest [32]byte
	if len(value) != hex.EncodedLen(len(digest)) {
		return digest, errors.New("must contain 64 lowercase hexadecimal characters")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || hex.EncodeToString(decoded) != value {
		return digest, errors.New("must contain 64 lowercase hexadecimal characters")
	}
	copy(digest[:], decoded)
	return digest, nil
}

func clientRunLaunchObjectPointer(field string, source corecontract.RunLaunchObjectPointer) (runmanifest.ObjectPointer, error) {
	if err := validateUUIDIdentity(field+" object ID", source.ObjectID); err != nil {
		return runmanifest.ObjectPointer{}, err
	}
	digest, err := decodeClientSHA256(source.SHA256)
	if err != nil {
		return runmanifest.ObjectPointer{}, fmt.Errorf("%s SHA-256: %w", field, err)
	}
	if source.SizeBytes < 1 || source.SizeBytes > 1<<40 {
		return runmanifest.ObjectPointer{}, fmt.Errorf("%s size is outside the run-manifest bound", field)
	}
	if !validClientProtocolText(source.MediaType, 255) || strings.ContainsAny(source.MediaType, "\r\n") {
		return runmanifest.ObjectPointer{}, fmt.Errorf("%s media type is invalid", field)
	}
	return runmanifest.ObjectPointer{
		ObjectID: source.ObjectID, SHA256: hex.EncodeToString(digest[:]),
		SizeBytes: source.SizeBytes, MediaType: source.MediaType,
	}, nil
}

func validClientProtocolText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
