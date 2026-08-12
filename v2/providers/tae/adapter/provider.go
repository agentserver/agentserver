package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/sandboxgateway"
	"github.com/agentserver/agentserver/v2/internal/taepolicy"
)

const (
	MetadataSandboxID       = "agentserver_sandbox_id"
	MetadataCreateID        = "agentserver_create_id"
	MetadataWorkspaceID     = "agentserver_workspace_id"
	MetadataSessionID       = "agentserver_session_id"
	MetadataEnvironmentID   = "agentserver_environment_id"
	MetadataRuntimeSHA256   = "agentserver_runtime_sha256"
	MetadataPackSetSHA256   = "agentserver_pack_sha256"
	MetadataTAEPolicySHA256 = "agentserver_tae_policy_sha256"
	defaultStreamGrace      = 30 * time.Second
	defaultReconnectDelay   = 100 * time.Millisecond
	defaultReconnectCount   = 2
	defaultSignalTimeout    = 3 * time.Second
	defaultMaxSourceBytes   = int64(executionbackend.MaxReadFileBytes)
	maxDeleteSearchMatches  = 100
	providerOperationPrefix = "tae-pid:"
)

type Config struct {
	Control            ControlPlane
	Data               DataPlane
	Region             string
	PSM                string
	Root               string
	Now                func() time.Time
	StreamGrace        time.Duration
	ReconnectAttempts  int
	ReconnectDelay     time.Duration
	SignalTimeout      time.Duration
	MaxReadSourceBytes int64
	Policy             taepolicy.Binding
}

type Provider struct {
	control            ControlPlane
	data               DataPlane
	region             string
	psm                string
	root               string
	now                func() time.Time
	streamGrace        time.Duration
	reconnectAttempts  int
	reconnectDelay     time.Duration
	signalTimeout      time.Duration
	maxReadSourceBytes int64
	policy             taepolicy.Binding
}

func NewProvider(config Config) (*Provider, error) {
	if config.Control == nil || config.Data == nil {
		return nil, errors.New("TAE control plane and data plane are required")
	}
	// The first production rollout is deliberately SG-only. Do not silently
	// infer a region from the host process: a wrong trust/network domain must
	// fail during startup rather than creating a sandbox elsewhere.
	if config.Region != "sg" {
		return nil, errors.New("TAE provider region must be exactly sg")
	}
	if strings.TrimSpace(config.PSM) != config.PSM || config.PSM == "" || len(config.PSM) > 256 {
		return nil, errors.New("TAE provider PSM is invalid")
	}
	if err := config.Policy.Validate(config.Region, config.PSM, larkegresspolicy.SHA256Hex()); err != nil {
		return nil, fmt.Errorf("TAE policy binding is invalid: %w", err)
	}
	if config.Root == "" {
		config.Root = "/workspace"
	}
	if !cleanAbsolutePath(config.Root) {
		return nil, errors.New("TAE provider root must be a clean absolute path")
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.StreamGrace == 0 {
		config.StreamGrace = defaultStreamGrace
	}
	if config.StreamGrace < time.Second || config.StreamGrace > 5*time.Minute {
		return nil, errors.New("TAE stream grace must be between one second and five minutes")
	}
	if config.ReconnectAttempts == 0 {
		config.ReconnectAttempts = defaultReconnectCount
	}
	if config.ReconnectAttempts < 0 || config.ReconnectAttempts > 5 {
		return nil, errors.New("TAE reconnect attempts must be between zero and five")
	}
	if config.ReconnectDelay == 0 {
		config.ReconnectDelay = defaultReconnectDelay
	}
	if config.ReconnectDelay < 10*time.Millisecond || config.ReconnectDelay > 5*time.Second {
		return nil, errors.New("TAE reconnect delay must be between 10ms and five seconds")
	}
	if config.SignalTimeout == 0 {
		config.SignalTimeout = defaultSignalTimeout
	}
	if config.SignalTimeout < 100*time.Millisecond || config.SignalTimeout > 30*time.Second {
		return nil, errors.New("TAE signal timeout must be between 100ms and 30s")
	}
	if config.MaxReadSourceBytes == 0 {
		config.MaxReadSourceBytes = defaultMaxSourceBytes
	}
	if config.MaxReadSourceBytes < 1 || config.MaxReadSourceBytes > executionbackend.MaxReadFileBytes {
		return nil, fmt.Errorf("TAE source-file limit must be between 1 and %d bytes", executionbackend.MaxReadFileBytes)
	}
	return &Provider{
		control: config.Control, data: config.Data, region: config.Region, psm: config.PSM,
		root: config.Root, now: config.Now, streamGrace: config.StreamGrace,
		reconnectAttempts: config.ReconnectAttempts, reconnectDelay: config.ReconnectDelay,
		signalTimeout: config.SignalTimeout, maxReadSourceBytes: config.MaxReadSourceBytes, policy: config.Policy,
	}, nil
}

func (provider *Provider) CreateSandbox(ctx context.Context, request sandboxgateway.CreateSandboxRequest) (sandboxgateway.ProviderSandbox, error) {
	if err := provider.validateCreate(request.Region, request.PSM, request.TTL); err != nil {
		return sandboxgateway.ProviderSandbox{}, &sandboxgateway.ProviderError{Code: "invalid_create_request", Cause: err}
	}
	metadata := provider.createMetadata(request.SandboxID, request.IdempotencyKey, request.WorkspaceID, request.SessionID,
		request.EnvironmentID, request.RuntimeProfileSHA256, request.PackSetSHA256)
	if err := validateMetadata(metadata); err != nil {
		return sandboxgateway.ProviderSandbox{}, &sandboxgateway.ProviderError{Code: "invalid_create_metadata", Cause: err}
	}
	if request.SessionRef != "" {
		existing, err := provider.control.Get(ctx, request.SessionRef)
		if err != nil {
			return sandboxgateway.ProviderSandbox{}, provider.lifecycleError("provider_get_failed", err)
		}
		if !metadataContainsIdentity(existing.Metadata, metadata) {
			return sandboxgateway.ProviderSandbox{}, &sandboxgateway.ProviderError{
				Code: "provider_identity_mismatch", Cause: errors.New("TAE session metadata does not match the managed sandbox create identity"),
			}
		}
		return provider.providerSandbox(existing)
	}
	// Terminal Sandbox images and startup commands are fixed by the pinned TAE
	// Sandbox revision. The Session API must not receive image or command
	// overrides; those fields are not the release boundary for Terminal.
	session, err := provider.control.Create(ctx, CreateInput{
		TTL: request.TTL, Metadata: metadata,
	})
	if err != nil {
		return sandboxgateway.ProviderSandbox{}, provider.createError(err)
	}
	if !metadataContainsIdentity(session.Metadata, metadata) {
		return sandboxgateway.ProviderSandbox{}, &sandboxgateway.ProviderError{
			Code: "provider_metadata_mismatch", Ambiguous: true,
			Cause: errors.New("TAE create returned a session without the complete managed sandbox identity metadata"),
		}
	}
	return provider.providerSandbox(session)
}

func (provider *Provider) FindSandbox(ctx context.Context, request sandboxgateway.FindSandboxRequest) (sandboxgateway.ProviderSandbox, error) {
	if request.Region != provider.region || request.PSM != provider.psm {
		return sandboxgateway.ProviderSandbox{}, &sandboxgateway.ProviderError{Code: "provider_scope_mismatch", Cause: errors.New("TAE lookup differs from configured region or PSM")}
	}
	metadata := provider.createMetadata(request.SandboxID, request.IdempotencyKey, request.WorkspaceID, request.SessionID,
		request.EnvironmentID, request.RuntimeProfileSHA256, request.PackSetSHA256)
	if err := validateMetadata(metadata); err != nil {
		return sandboxgateway.ProviderSandbox{}, &sandboxgateway.ProviderError{Code: "invalid_find_metadata", Cause: err}
	}
	result, err := provider.control.Search(ctx, SearchInput{Metadata: metadata, Limit: 2})
	if err != nil {
		return sandboxgateway.ProviderSandbox{}, provider.lifecycleError("provider_search_failed", err)
	}
	if result.Total > len(result.Sessions) {
		return sandboxgateway.ProviderSandbox{}, &sandboxgateway.ProviderError{
			Code: "provider_search_incomplete", Ambiguous: true,
			Cause: errors.New("TAE search has more exact candidates than the bounded response returned"),
		}
	}
	matched := make([]ControlSession, 0, 2)
	for _, session := range result.Sessions {
		if metadataContainsIdentity(session.Metadata, metadata) && !session.Deleted {
			matched = append(matched, session)
		}
	}
	switch len(matched) {
	case 0:
		return sandboxgateway.ProviderSandbox{}, sandboxgateway.ErrProviderSandboxNotFound
	case 1:
		return provider.providerSandbox(matched[0])
	default:
		return sandboxgateway.ProviderSandbox{}, &sandboxgateway.ProviderError{
			Code: "provider_create_ambiguous", Ambiguous: true,
			Cause: errors.New("more than one live TAE session has the exact managed sandbox create metadata"),
		}
	}
}

func (provider *Provider) GetSandbox(ctx context.Context, sessionRef string) (sandboxgateway.ProviderSandbox, error) {
	if sessionRef == "" {
		return sandboxgateway.ProviderSandbox{}, sandboxgateway.ErrProviderSandboxNotFound
	}
	session, err := provider.control.Get(ctx, sessionRef)
	if err != nil {
		return sandboxgateway.ProviderSandbox{}, provider.lifecycleError("provider_get_failed", err)
	}
	return provider.providerSandbox(session)
}

func (provider *Provider) SetSandboxTimeout(ctx context.Context, request sandboxgateway.SetSandboxTimeoutProviderRequest) (sandboxgateway.ProviderSandbox, error) {
	if request.SessionRef == "" || request.TTL <= 0 || request.TTL%time.Second != 0 {
		return sandboxgateway.ProviderSandbox{}, &sandboxgateway.ProviderError{Code: "invalid_ttl_request", Cause: errors.New("TAE session and whole-second positive TTL are required")}
	}
	if err := provider.control.UpdateTTL(ctx, request.SessionRef, request.TTL); err != nil {
		return sandboxgateway.ProviderSandbox{}, provider.lifecycleError("provider_ttl_failed", err)
	}
	return provider.GetSandbox(ctx, request.SessionRef)
}

func (provider *Provider) DeleteSandbox(ctx context.Context, request sandboxgateway.DeleteSandboxProviderRequest) error {
	identity := request.Identity
	if identity.Region != provider.region || identity.PSM != provider.psm {
		return &sandboxgateway.ProviderError{Code: "provider_scope_mismatch", Cause: errors.New("TAE delete differs from configured region or PSM")}
	}
	metadata := provider.createMetadata(identity.SandboxID, identity.IdempotencyKey, identity.WorkspaceID, identity.SessionID,
		identity.EnvironmentID, identity.RuntimeProfileSHA256, identity.PackSetSHA256)
	if err := validateMetadata(metadata); err != nil {
		return &sandboxgateway.ProviderError{Code: "invalid_delete_metadata", Cause: err}
	}
	if request.SessionRef != "" {
		session, err := provider.control.Get(ctx, request.SessionRef)
		if err != nil {
			return provider.lifecycleError("provider_delete_get_failed", err)
		}
		if session.Deleted {
			return sandboxgateway.ErrProviderSandboxNotFound
		}
		if session.ID != request.SessionRef || !metadataContainsIdentity(session.Metadata, metadata) {
			return &sandboxgateway.ProviderError{
				Code:  "provider_identity_mismatch",
				Cause: errors.New("TAE delete reference does not have the complete managed sandbox create identity"),
			}
		}
		if err := provider.control.Delete(ctx, request.SessionRef); err != nil {
			return provider.lifecycleError("provider_delete_failed", err)
		}
		return nil
	}

	result, err := provider.control.Search(ctx, SearchInput{Metadata: metadata, Limit: maxDeleteSearchMatches})
	if err != nil {
		return provider.lifecycleError("provider_delete_search_failed", err)
	}
	if result.Total > len(result.Sessions) {
		return &sandboxgateway.ProviderError{
			Code: "provider_delete_search_incomplete", Ambiguous: true,
			Cause: errors.New("TAE delete recovery could not enumerate every exact candidate"),
		}
	}
	matched := make([]string, 0, len(result.Sessions))
	seen := make(map[string]struct{}, len(result.Sessions))
	for _, session := range result.Sessions {
		if session.Deleted || !metadataContainsIdentity(session.Metadata, metadata) {
			continue
		}
		if session.ID == "" || len(session.ID) > 1024 {
			return &sandboxgateway.ProviderError{Code: "invalid_provider_identity", Cause: errors.New("TAE delete recovery returned an invalid session ID")}
		}
		if _, duplicate := seen[session.ID]; duplicate {
			return &sandboxgateway.ProviderError{Code: "provider_delete_search_invalid", Cause: errors.New("TAE delete recovery returned a duplicate session ID")}
		}
		seen[session.ID] = struct{}{}
		matched = append(matched, session.ID)
	}
	if len(matched) == 0 {
		return sandboxgateway.ErrProviderSandboxNotFound
	}
	for _, sessionID := range matched {
		if err := provider.control.Delete(ctx, sessionID); err != nil && !errors.Is(err, ErrSessionNotFound) {
			return &sandboxgateway.ProviderError{
				Code: "provider_delete_partial", Ambiguous: true,
				Cause: errors.New("TAE delete recovery did not confirm deletion of every exact candidate"),
			}
		}
	}
	return nil
}

func (provider *Provider) StartProcess(ctx context.Context, request sandboxgateway.StartProcessProviderRequest) (executionbackend.Exchange, error) {
	if err := request.Request.Validate(); err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_request", err)
	}
	if request.Request.Target.Kind != executionbackend.KindTAE {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "target_kind_mismatch", errors.New("TAE provider received a non-TAE target"))
	}
	if request.Request.TTY {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "tty_unsupported", errors.New("TAE managed executor does not enable TTY mode"))
	}
	if request.SessionRef == "" || !provider.pathAllowed(request.Request.WorkingDirectory) || request.Request.WorkspaceRoot != provider.root {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_sandbox_path", errors.New("TAE process path is outside the configured workspace root"))
	}
	operationContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), request.Request.Timeout+provider.streamGrace)
	stream, err := provider.data.StartProcess(operationContext, request.SessionRef, StartProcessInput{
		RequestID: request.Request.RequestID, Executable: request.Request.Executable,
		Arguments: append([]string(nil), request.Request.Arguments...), WorkingDirectory: request.Request.WorkingDirectory,
		Environment: cloneStrings(request.Request.Environment), Timeout: request.Request.Timeout,
	})
	if err != nil {
		cancel()
		return nil, dispatchError(err, false, "provider_start_failed")
	}
	exchange := newProcessExchange(provider, operationContext, cancel, request, stream)
	go exchange.consume(stream)
	return exchange, nil
}

func (provider *Provider) SignalProcess(ctx context.Context, request sandboxgateway.SignalProcessProviderRequest) (executionbackend.Exchange, error) {
	if err := request.Request.Validate(); err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_request", err)
	}
	if request.Request.Target.Kind != executionbackend.KindTAE || request.SessionRef == "" {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "target_kind_mismatch", errors.New("TAE signal target is invalid"))
	}
	pid, err := parseProviderHandle(request.Request.ProviderHandle)
	if err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_provider_handle", err)
	}
	signal, err := providerSignal(request.Request.Signal)
	if err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_signal", err)
	}
	operationContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), provider.signalTimeout)
	defer cancel()
	providerRequestID, err := provider.data.SignalProcess(operationContext, request.SessionRef, pid, signal)
	if err != nil {
		return nil, dispatchError(err, false, "provider_signal_failed")
	}
	return newCompletedExchange(request.Request.Target, request.Request.Operation,
		executionbackend.Acknowledgement{ProviderOperationID: request.Request.ProviderHandle, ProviderRequestID: providerRequestID, AcceptedAt: provider.now()},
		nil, executionbackend.TerminalResult{Status: executionbackend.TerminalSucceeded, ReasonCode: "signal_delivered", OutputComplete: true, CompletedAt: provider.now()}), nil
}

func (provider *Provider) ReadFile(ctx context.Context, request sandboxgateway.ReadFileProviderRequest) (executionbackend.Exchange, error) {
	if err := request.Request.Validate(); err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_request", err)
	}
	if request.Request.Target.Kind != executionbackend.KindTAE || request.SessionRef == "" {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "target_kind_mismatch", errors.New("TAE read-file target is invalid"))
	}
	if !provider.pathAllowed(request.Request.Path) {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_sandbox_path", errors.New("TAE read-file path is outside the configured workspace root"))
	}
	operationContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), provider.streamGrace)
	defer cancel()
	info, statRequestID, err := provider.data.Stat(operationContext, request.SessionRef, request.Request.Path)
	if err != nil {
		return nil, dispatchError(err, false, "provider_stat_failed")
	}
	if info.Type != "file" || info.SymlinkTarget != "" {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeRejected, "file_type_rejected", errors.New("TAE read-file only permits regular non-symlink files"))
	}
	if info.Size < 0 || info.Size > provider.maxReadSourceBytes {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeRejected, "source_file_too_large", errors.New("TAE source file exceeds the configured download bound"))
	}
	if request.Request.Offset >= uint64(info.Size) {
		return newCompletedExchange(request.Request.Target, request.Request.Operation,
			executionbackend.Acknowledgement{ProviderOperationID: "tae-read:" + request.Request.Operation.OperationID, ProviderRequestID: statRequestID, AcceptedAt: provider.now()},
			nil, executionbackend.TerminalResult{Status: executionbackend.TerminalSucceeded, OutputComplete: true, CompletedAt: provider.now()}), nil
	}
	download, err := provider.data.Download(operationContext, request.SessionRef, request.Request.Path)
	if err != nil {
		return nil, dispatchError(err, true, "provider_download_failed")
	}
	if download.Body == nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeUnknown, "provider_download_invalid", errors.New("TAE download returned no body"))
	}
	defer download.Body.Close()
	if download.ContentLength > provider.maxReadSourceBytes {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeRejected, "source_file_too_large", errors.New("TAE download content length exceeds the configured bound"))
	}
	if _, err := io.CopyN(io.Discard, download.Body, int64(request.Request.Offset)); err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeUnknown, "provider_download_short", errors.New("TAE download ended before the approved offset"))
	}
	limit := request.Request.Limit
	remainingFile := uint64(info.Size) - request.Request.Offset
	if limit > remainingFile {
		limit = remainingFile
	}
	content, err := io.ReadAll(io.LimitReader(download.Body, int64(limit)))
	if err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeUnknown, "provider_download_failed", errors.New("TAE download stream failed"))
	}
	if uint64(len(content)) != limit {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeUnknown, "provider_download_short", errors.New("TAE download changed or ended before the approved range"))
	}
	var events []executionbackend.Event
	if len(content) > 0 {
		events = []executionbackend.Event{{Sequence: 1, Kind: executionbackend.EventFileBytes, Data: content}}
	}
	return newCompletedExchange(request.Request.Target, request.Request.Operation,
		executionbackend.Acknowledgement{ProviderOperationID: "tae-read:" + request.Request.Operation.OperationID, ProviderRequestID: download.RequestID, AcceptedAt: provider.now()},
		events, executionbackend.TerminalResult{Status: executionbackend.TerminalSucceeded, OutputComplete: true, CompletedAt: provider.now()}), nil
}

func (provider *Provider) validateCreate(region, psm string, ttl time.Duration) error {
	if region != provider.region || psm != provider.psm {
		return errors.New("TAE create differs from configured region or PSM")
	}
	if ttl <= 0 || ttl%time.Second != 0 || ttl > 24*time.Hour {
		return errors.New("TAE create TTL must be whole seconds between one second and 24 hours")
	}
	return nil
}

func (provider *Provider) providerSandbox(session ControlSession) (sandboxgateway.ProviderSandbox, error) {
	if session.ID == "" || len(session.ID) > 1024 {
		return sandboxgateway.ProviderSandbox{}, &sandboxgateway.ProviderError{Code: "invalid_provider_identity", Cause: errors.New("TAE returned an invalid session ID")}
	}
	state := providerState(session)
	result := sandboxgateway.ProviderSandbox{SessionRef: session.ID, State: state, ExpiresAt: session.ExpiresAt, RequestID: session.RequestID}
	if state == sandboxgateway.ProviderSandboxReady {
		if !session.SandboxdEnabled {
			return sandboxgateway.ProviderSandbox{}, &sandboxgateway.ProviderError{Code: "sandboxd_not_enabled", Cause: errors.New("TAE Terminal session does not expose sandboxd")}
		}
		if RuntimeCommandConflicts(session.Command) {
			return sandboxgateway.ProviderSandbox{}, &sandboxgateway.ProviderError{Code: "runtime_command_mismatch", Cause: errors.New("TAE Terminal session did not retain the managed runtime command")}
		}
		result.Root = provider.root
	}
	return result, nil
}

func providerState(session ControlSession) sandboxgateway.ProviderSandboxState {
	if session.Deleted {
		return sandboxgateway.ProviderSandboxDeleted
	}
	switch strings.ToLower(strings.TrimSpace(session.Status)) {
	case "running", "ready":
		return sandboxgateway.ProviderSandboxReady
	case "", "pending", "creating", "initializing", "starting":
		return sandboxgateway.ProviderSandboxCreating
	case "deleting", "terminating", "stopping":
		return sandboxgateway.ProviderSandboxDeleting
	case "deleted", "terminated", "stopped":
		return sandboxgateway.ProviderSandboxDeleted
	case "failed", "error":
		return sandboxgateway.ProviderSandboxFailed
	default:
		return sandboxgateway.ProviderSandboxUnknown
	}
}

func (provider *Provider) createError(err error) error {
	var requestError *RequestError
	if errors.As(err, &requestError) {
		ambiguous := requestError.WroteRequest
		if requestError.StatusCode >= 400 && requestError.StatusCode < 500 &&
			requestError.StatusCode != 408 && requestError.StatusCode != 425 && requestError.StatusCode != 429 {
			ambiguous = false
		}
		return &sandboxgateway.ProviderError{Code: safeRequestCode(requestError, "provider_create_failed"), Ambiguous: ambiguous, Cause: safeRequestCause(requestError)}
	}
	return &sandboxgateway.ProviderError{Code: "provider_create_failed", Ambiguous: true, Cause: errors.New("TAE create failed without a definitive provider response")}
}

func (provider *Provider) lifecycleError(fallback string, err error) error {
	if errors.Is(err, ErrSessionNotFound) {
		return sandboxgateway.ErrProviderSandboxNotFound
	}
	var requestError *RequestError
	if errors.As(err, &requestError) {
		if requestError.StatusCode == 404 {
			return sandboxgateway.ErrProviderSandboxNotFound
		}
		return &sandboxgateway.ProviderError{Code: safeRequestCode(requestError, fallback), Cause: safeRequestCause(requestError)}
	}
	return &sandboxgateway.ProviderError{Code: fallback, Cause: errors.New("TAE lifecycle request failed")}
}

func dispatchError(err error, afterAccepted bool, fallback string) error {
	outcome := executionbackend.OutcomeUnknown
	code := fallback
	providerRequestID := ""
	cause := errors.New("TAE data-plane request failed")
	var requestError *RequestError
	if errors.As(err, &requestError) {
		code = safeRequestCode(requestError, fallback)
		providerRequestID = requestError.RequestID
		cause = safeRequestCause(requestError)
		switch {
		case requestError.StatusCode >= 400 && requestError.StatusCode < 500 && requestError.StatusCode != 408 && requestError.StatusCode != 425 && requestError.StatusCode != 429:
			outcome = executionbackend.OutcomeRejected
		case !afterAccepted && !requestError.WroteRequest:
			outcome = executionbackend.OutcomeNotSent
		}
	}
	dispatchError := executionbackend.NewDispatchError(outcome, code, cause)
	dispatchError.ProviderRequestID = providerRequestID
	return dispatchError
}

func safeRequestCode(requestError *RequestError, fallback string) string {
	if requestError != nil {
		switch requestError.Code {
		case "bad_request", "unauthorized", "forbidden", "not_found", "conflict", "rate_limited", "request_timeout", "provider_unavailable", "invalid_response", "stream_lost", "identity_unavailable":
			return requestError.Code
		}
	}
	return fallback
}

func safeRequestCause(requestError *RequestError) error {
	if requestError == nil {
		return errors.New("TAE request failed")
	}
	if requestError.StatusCode != 0 {
		return fmt.Errorf("TAE returned HTTP status %s", statusText(requestError.StatusCode))
	}
	return errors.New("TAE transport failed without a provider response")
}

func (provider *Provider) createMetadata(sandboxID, createID, workspaceID, sessionID, environmentID, runtimeDigest, packDigest string) map[string]string {
	return map[string]string{
		MetadataSandboxID: sandboxID, MetadataCreateID: createID, MetadataWorkspaceID: workspaceID,
		MetadataSessionID: sessionID, MetadataEnvironmentID: environmentID,
		MetadataRuntimeSHA256: runtimeDigest, MetadataPackSetSHA256: packDigest,
		MetadataTAEPolicySHA256: provider.policy.BindingSHA256,
	}
}

func validateMetadata(metadata map[string]string) error {
	if len(metadata) != 8 {
		return errors.New("managed sandbox metadata must contain exactly eight identity fields")
	}
	for name, value := range metadata {
		if value == "" || len(value) > 512 || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("managed sandbox metadata field %s is invalid", name)
		}
	}
	return nil
}

// metadataContainsIdentity verifies the complete agentserver-owned identity
// projection while permitting TAE to append provider-owned metadata. The
// request map is validated separately and always contains exactly the eight
// immutable identity fields. Missing or conflicting identity fields remain a
// hard mismatch; provider-added fields do not weaken resource ownership.
func metadataContainsIdentity(actual, expected map[string]string) bool {
	for name, value := range expected {
		if actual[name] != value {
			return false
		}
	}
	return true
}

func cloneStrings(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	clone := make(map[string]string, len(values))
	for name, value := range values {
		clone[name] = value
	}
	return clone
}

func (provider *Provider) pathAllowed(candidate string) bool {
	if !cleanAbsolutePath(candidate) {
		return false
	}
	return candidate == provider.root || strings.HasPrefix(candidate, provider.root+"/")
}

func cleanAbsolutePath(candidate string) bool {
	return strings.HasPrefix(candidate, "/") && candidate == path.Clean(candidate) && !strings.ContainsRune(candidate, '\x00')
}

func parseProviderHandle(handle string) (int, error) {
	if !strings.HasPrefix(handle, providerOperationPrefix) {
		return 0, errors.New("TAE provider handle has an invalid prefix")
	}
	pid, err := strconv.Atoi(strings.TrimPrefix(handle, providerOperationPrefix))
	if err != nil || pid < 1 {
		return 0, errors.New("TAE provider handle has an invalid PID")
	}
	return pid, nil
}

func providerSignal(signal executionbackend.Signal) (int, error) {
	switch signal {
	case executionbackend.SignalTerminate:
		return 15, nil
	case executionbackend.SignalInterrupt:
		return 2, nil
	case executionbackend.SignalKill:
		return 9, nil
	default:
		return 0, fmt.Errorf("unsupported TAE signal %q", signal)
	}
}

var _ sandboxgateway.Provider = (*Provider)(nil)
