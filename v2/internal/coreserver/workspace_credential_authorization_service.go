package coreserver

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

const credentialAuthorizationPollLease = 25 * time.Second

func (commands StateStoreWorkspaceCredentialCommands) BeginAuthorization(
	ctx context.Context,
	workspaceID, kind, actorID string,
	input corecontract.BeginWorkspaceCredentialAuthorizationRequest,
) (corecontract.BeginWorkspaceCredentialAuthorizationResponse, error) {
	if err := commands.readyForWrite(); err != nil {
		return corecontract.BeginWorkspaceCredentialAuthorizationResponse{}, err
	}
	if commands.Now == nil {
		return corecontract.BeginWorkspaceCredentialAuthorizationResponse{}, errors.New("workspace credential authorization clock is unavailable")
	}
	if err := commands.requireCredentialManager(ctx, workspaceID, actorID); err != nil {
		return corecontract.BeginWorkspaceCredentialAuthorizationResponse{}, err
	}
	provider, ok := commands.Registry.DeviceAuthorizationProvider(kind)
	if !ok {
		return corecontract.BeginWorkspaceCredentialAuthorizationResponse{}, credentialAuthorizationError(coredb.ErrorInvalidArgument, "BeginWorkspaceCredentialAuthorization", "", "provider does not support device authorization")
	}
	if input.DisplayName == "" || len(input.DisplayName) > 256 || strings.TrimSpace(input.DisplayName) != input.DisplayName || strings.ContainsAny(input.DisplayName, "\x00\r\n") {
		return corecontract.BeginWorkspaceCredentialAuthorizationResponse{}, credentialAuthorizationError(coredb.ErrorInvalidArgument, "BeginWorkspaceCredentialAuthorization", "", "credential display name is invalid")
	}
	ownerScope, ownerUserID := input.OwnerScope, input.OwnerUserID
	if ownerScope == "" {
		ownerScope = corecredentials.OwnerScopeWorkspace
	}
	if ownerScope == corecredentials.OwnerScopeUser && ownerUserID == "" {
		ownerUserID = actorID
	}
	if ownerScope != corecredentials.OwnerScopeWorkspace && ownerScope != corecredentials.OwnerScopeUser {
		return corecontract.BeginWorkspaceCredentialAuthorizationResponse{}, credentialAuthorizationError(coredb.ErrorInvalidArgument, "BeginWorkspaceCredentialAuthorization", "", "credential owner scope is invalid")
	}
	authorizationID, err := newCredentialID()
	if err != nil {
		return corecontract.BeginWorkspaceCredentialAuthorizationResponse{}, errors.New("allocate credential authorization ID")
	}
	targetBindingID := input.BindingID
	targetExists := targetBindingID != ""
	if !targetExists {
		targetBindingID, err = newCredentialID()
		if err != nil {
			return corecontract.BeginWorkspaceCredentialAuthorizationResponse{}, errors.New("allocate credential binding ID")
		}
	} else {
		binding, getErr := commands.Store.Get(ctx, workspaceID, kind, targetBindingID)
		if getErr != nil {
			return corecontract.BeginWorkspaceCredentialAuthorizationResponse{}, getErr
		}
		defer clearCredentialBytes(binding.SealedSecret)
		if binding.ID == "" {
			return corecontract.BeginWorkspaceCredentialAuthorizationResponse{}, credentialAuthorizationError(coredb.ErrorNotFound, "BeginWorkspaceCredentialAuthorization", authorizationID, "credential binding was not found")
		}
		if input.ExpectedAuthorityVersion != binding.AuthorityVersion || input.ExpectedCredentialVersion != binding.CredentialVersion {
			return corecontract.BeginWorkspaceCredentialAuthorizationResponse{}, credentialAuthorizationError(coredb.ErrorVersionConflict, "BeginWorkspaceCredentialAuthorization", authorizationID, "credential binding version changed")
		}
		input.DisplayName, ownerScope, ownerUserID, input.MakeDefault = binding.DisplayName, binding.OwnerScope, binding.OwnerUserID, binding.IsDefault
	}
	challenge, err := provider.BeginDeviceAuthorization(ctx, input.ProviderParameters)
	if err != nil {
		commands.logCredentialAuthorizationProviderFailure(ctx, kind, "begin", err)
		return corecontract.BeginWorkspaceCredentialAuthorizationResponse{}, credentialAuthorizationError(coredb.ErrorConflict, "BeginWorkspaceCredentialAuthorization", authorizationID, "provider could not begin device authorization")
	}
	defer clearCredentialBytes(challenge.ProviderState)
	now := commands.Now().UTC()
	if err := validateCredentialAuthorizationChallenge(challenge, now); err != nil {
		return corecontract.BeginWorkspaceCredentialAuthorizationResponse{}, errors.New("credential provider returned an invalid device authorization challenge")
	}
	sealed, err := commands.Sealer.SealAuthorization(corecredentials.AuthorizationSealScope{
		WorkspaceID: workspaceID, AuthorizationID: authorizationID, ProviderStateVersion: 1,
	}, challenge.ProviderState)
	if err != nil {
		return corecontract.BeginWorkspaceCredentialAuthorizationResponse{}, errors.New("seal credential authorization state")
	}
	defer clearCredentialBytes(sealed)
	record, err := commands.Store.CreateWorkspaceCredentialAuthorization(ctx, coredb.CreateWorkspaceCredentialAuthorizationCommand{
		Record: coredb.WorkspaceCredentialAuthorizationRecord{
			ID: authorizationID, WorkspaceID: workspaceID, Kind: kind, ActorID: actorID,
			TargetBindingID: targetBindingID, TargetExists: targetExists,
			ExpectedAuthorityVersion: input.ExpectedAuthorityVersion, ExpectedCredentialVersion: input.ExpectedCredentialVersion,
			DisplayName: input.DisplayName, OwnerScope: ownerScope, OwnerUserID: ownerUserID, MakeDefault: input.MakeDefault,
			ProviderPublic: choosePublicMetadata(nil, challenge.ProviderPublic), UserCode: challenge.UserCode,
			VerificationURI: challenge.VerificationURI, VerificationURIComplete: challenge.VerificationURIComplete,
			SealedProviderState: sealed, SealingKeyID: commands.Sealer.ActiveKeyID(), ProviderStateVersion: 1,
			Status:              coredb.WorkspaceCredentialAuthorizationPending,
			PollIntervalSeconds: int(challenge.Interval / time.Second), NextPollAt: now.Add(challenge.Interval), ExpiresAt: challenge.ExpiresAt.UTC(),
		},
	})
	if err != nil {
		return corecontract.BeginWorkspaceCredentialAuthorizationResponse{}, err
	}
	return corecontract.BeginWorkspaceCredentialAuthorizationResponse{Authorization: credentialAuthorization(record, nil)}, nil
}

func (commands StateStoreWorkspaceCredentialCommands) GetAuthorization(ctx context.Context, workspaceID, kind, authorizationID, actorID string) (corecontract.GetWorkspaceCredentialAuthorizationResponse, error) {
	if commands.Store == nil {
		return corecontract.GetWorkspaceCredentialAuthorizationResponse{}, errors.New("credential authorization store is unavailable")
	}
	record, err := commands.Store.GetWorkspaceCredentialAuthorization(ctx, workspaceID, kind, authorizationID, actorID)
	if err != nil {
		return corecontract.GetWorkspaceCredentialAuthorizationResponse{}, err
	}
	authorization, err := commands.authorizationResponse(ctx, record)
	if err != nil {
		return corecontract.GetWorkspaceCredentialAuthorizationResponse{}, err
	}
	return corecontract.GetWorkspaceCredentialAuthorizationResponse{Authorization: authorization}, nil
}

func (commands StateStoreWorkspaceCredentialCommands) PollAuthorization(ctx context.Context, workspaceID, kind, authorizationID, actorID string) (corecontract.PollWorkspaceCredentialAuthorizationResponse, error) {
	if err := commands.readyForWrite(); err != nil || commands.Now == nil {
		if err != nil {
			return corecontract.PollWorkspaceCredentialAuthorizationResponse{}, err
		}
		return corecontract.PollWorkspaceCredentialAuthorizationResponse{}, errors.New("credential authorization clock is unavailable")
	}
	current, err := commands.Store.GetWorkspaceCredentialAuthorization(ctx, workspaceID, kind, authorizationID, actorID)
	if err != nil {
		return corecontract.PollWorkspaceCredentialAuthorizationResponse{}, err
	}
	if current.Status != coredb.WorkspaceCredentialAuthorizationPending || current.NextPollAt.After(commands.Now().UTC()) {
		authorization, responseErr := commands.authorizationResponse(ctx, current)
		if responseErr != nil {
			return corecontract.PollWorkspaceCredentialAuthorizationResponse{}, responseErr
		}
		return corecontract.PollWorkspaceCredentialAuthorizationResponse{Authorization: authorization}, nil
	}
	leaseToken, err := newCredentialID()
	if err != nil {
		return corecontract.PollWorkspaceCredentialAuthorizationResponse{}, errors.New("allocate credential authorization poll lease")
	}
	now := commands.Now().UTC()
	claimed, err := commands.Store.ClaimWorkspaceCredentialAuthorizationPoll(ctx, coredb.ClaimWorkspaceCredentialAuthorizationPollCommand{
		WorkspaceID: workspaceID, Kind: kind, AuthorizationID: authorizationID, ActorID: actorID,
		LeaseToken: leaseToken, LeaseExpiresAt: now.Add(credentialAuthorizationPollLease),
	})
	if err != nil {
		var stateErr *coredb.StateError
		if errors.As(err, &stateErr) && stateErr.Code == coredb.ErrorConflict {
			latest, getErr := commands.Store.GetWorkspaceCredentialAuthorization(ctx, workspaceID, kind, authorizationID, actorID)
			if getErr == nil {
				authorization, responseErr := commands.authorizationResponse(ctx, latest)
				if responseErr == nil {
					return corecontract.PollWorkspaceCredentialAuthorizationResponse{Authorization: authorization}, nil
				}
				return corecontract.PollWorkspaceCredentialAuthorizationResponse{}, responseErr
			}
		}
		return corecontract.PollWorkspaceCredentialAuthorizationResponse{}, err
	}
	defer clearCredentialBytes(claimed.SealedProviderState)
	provider, ok := commands.Registry.DeviceAuthorizationProvider(kind)
	if !ok {
		return commands.finishAuthorizationFailure(ctx, claimed, actorID, leaseToken, coredb.WorkspaceCredentialAuthorizationFailed, "provider_unavailable")
	}
	state, err := commands.Sealer.OpenAuthorization(corecredentials.AuthorizationSealScope{
		WorkspaceID: workspaceID, AuthorizationID: authorizationID, ProviderStateVersion: claimed.ProviderStateVersion,
	}, claimed.SealedProviderState)
	if err != nil {
		return commands.finishAuthorizationFailure(ctx, claimed, actorID, leaseToken, coredb.WorkspaceCredentialAuthorizationFailed, "provider_state_invalid")
	}
	defer clearCredentialBytes(state)
	poll, pollErr := provider.PollDeviceAuthorization(ctx, state)
	if pollErr != nil {
		commands.logCredentialAuthorizationProviderFailure(ctx, kind, "poll", pollErr)
		return commands.finishAuthorizationPending(ctx, claimed, actorID, leaseToken, "provider_unavailable", nil, 0)
	}
	if err := validateCredentialAuthorizationPoll(poll); err != nil {
		return commands.finishAuthorizationFailure(ctx, claimed, actorID, leaseToken, coredb.WorkspaceCredentialAuthorizationFailed, "provider_response_invalid")
	}
	switch poll.Status {
	case corecredentials.DeviceAuthorizationPending:
		return commands.finishAuthorizationPending(ctx, claimed, actorID, leaseToken, poll.ErrorCode, poll.ProviderState, poll.RetryAfter)
	case corecredentials.DeviceAuthorizationDenied:
		return commands.finishAuthorizationFailure(ctx, claimed, actorID, leaseToken, coredb.WorkspaceCredentialAuthorizationDenied, firstNonemptyCode(poll.ErrorCode, "access_denied"))
	case corecredentials.DeviceAuthorizationExpired:
		return commands.finishAuthorizationFailure(ctx, claimed, actorID, leaseToken, coredb.WorkspaceCredentialAuthorizationExpired, firstNonemptyCode(poll.ErrorCode, "expired_token"))
	case corecredentials.DeviceAuthorizationFailed:
		return commands.finishAuthorizationFailure(ctx, claimed, actorID, leaseToken, coredb.WorkspaceCredentialAuthorizationFailed, firstNonemptyCode(poll.ErrorCode, "provider_denied"))
	case corecredentials.DeviceAuthorizationSucceeded:
		return commands.finalizeAuthorization(ctx, claimed, actorID, leaseToken, provider, poll.Credential)
	default:
		return commands.finishAuthorizationFailure(ctx, claimed, actorID, leaseToken, coredb.WorkspaceCredentialAuthorizationFailed, "provider_response_invalid")
	}
}

func (commands StateStoreWorkspaceCredentialCommands) CancelAuthorization(ctx context.Context, workspaceID, kind, authorizationID, actorID string, input corecontract.CancelWorkspaceCredentialAuthorizationRequest) (corecontract.CancelWorkspaceCredentialAuthorizationResponse, error) {
	if commands.Store == nil {
		return corecontract.CancelWorkspaceCredentialAuthorizationResponse{}, errors.New("credential authorization store is unavailable")
	}
	record, changed, err := commands.Store.CancelWorkspaceCredentialAuthorization(ctx, coredb.CancelWorkspaceCredentialAuthorizationCommand{
		WorkspaceID: workspaceID, Kind: kind, AuthorizationID: authorizationID,
		ActorID: actorID, ExpectedVersion: input.ExpectedVersion,
	})
	if err != nil {
		return corecontract.CancelWorkspaceCredentialAuthorizationResponse{}, err
	}
	authorization, err := commands.authorizationResponse(ctx, record)
	if err != nil {
		return corecontract.CancelWorkspaceCredentialAuthorizationResponse{}, err
	}
	return corecontract.CancelWorkspaceCredentialAuthorizationResponse{Authorization: authorization, Changed: changed}, nil
}

func (commands StateStoreWorkspaceCredentialCommands) finalizeAuthorization(ctx context.Context, claimed coredb.WorkspaceCredentialAuthorizationRecord, actorID, leaseToken string, provider corecredentials.DeviceAuthorizationProvider, upload corecredentials.UploadResult) (corecontract.PollWorkspaceCredentialAuthorizationResponse, error) {
	defer clearCredentialBytes(upload.Secret)
	validated, err := provider.ValidateUpload(upload.AuthType, upload.Secret)
	if err != nil {
		return commands.finishAuthorizationFailure(ctx, claimed, actorID, leaseToken, coredb.WorkspaceCredentialAuthorizationFailed, "credential_invalid")
	}
	defer clearCredentialBytes(validated.Secret)
	credentialVersion := int64(1)
	if claimed.TargetExists {
		credentialVersion = claimed.ExpectedCredentialVersion + 1
	}
	sealed, err := commands.Sealer.Seal(corecredentials.BindingSealScope{
		WorkspaceID: claimed.WorkspaceID, BindingID: claimed.TargetBindingID, CredentialVersion: credentialVersion,
	}, validated.Secret)
	if err != nil {
		return corecontract.PollWorkspaceCredentialAuthorizationResponse{}, errors.New("seal authorized workspace credential")
	}
	defer clearCredentialBytes(sealed)
	result, err := commands.Store.FinalizeWorkspaceCredentialAuthorization(ctx, coredb.FinalizeWorkspaceCredentialAuthorizationCommand{
		WorkspaceID: claimed.WorkspaceID, Kind: claimed.Kind, AuthorizationID: claimed.ID,
		ActorID: actorID, LeaseToken: leaseToken, AuthType: validated.AuthType,
		PublicMetadata: choosePublicMetadata(upload.PublicMetadata, validated.PublicMetadata),
		SealedSecret:   sealed, SealingKeyID: commands.Sealer.ActiveKeyID(),
		AccessExpiresAt:  chooseTime(upload.AccessExpiresAt, validated.AccessExpiresAt),
		RefreshExpiresAt: chooseTime(upload.RefreshExpiresAt, validated.RefreshExpiresAt),
	})
	if err != nil {
		// Provider polling can outlive the database lease. If another replica
		// acquired the expired lease and completed (or otherwise advanced) the
		// same transaction first, return the durable state instead of surfacing a
		// spurious 409 after the user has already authorized the application.
		if coredb.HasStateErrorCode(err, coredb.ErrorConflict) || coredb.HasStateErrorCode(err, coredb.ErrorVersionConflict) {
			latest, getErr := commands.GetAuthorization(ctx, claimed.WorkspaceID, claimed.Kind, claimed.ID, actorID)
			if getErr == nil {
				return corecontract.PollWorkspaceCredentialAuthorizationResponse{Authorization: latest.Authorization}, nil
			}
		}
		return corecontract.PollWorkspaceCredentialAuthorizationResponse{}, err
	}
	metadata := credentialMetadata(result.Binding.Metadata())
	return corecontract.PollWorkspaceCredentialAuthorizationResponse{Authorization: credentialAuthorization(result.Authorization, &metadata)}, nil
}

func (commands StateStoreWorkspaceCredentialCommands) finishAuthorizationPending(ctx context.Context, claimed coredb.WorkspaceCredentialAuthorizationRecord, actorID, leaseToken, errorCode string, providerState []byte, retryAfter time.Duration) (corecontract.PollWorkspaceCredentialAuthorizationResponse, error) {
	interval := claimed.PollIntervalSeconds
	if retryAfter > 0 {
		interval += int(retryAfter / time.Second)
		if interval > 60 {
			interval = 60
		}
	}
	var sealed []byte
	if len(providerState) > 0 {
		defer clearCredentialBytes(providerState)
		var err error
		sealed, err = commands.Sealer.SealAuthorization(corecredentials.AuthorizationSealScope{
			WorkspaceID: claimed.WorkspaceID, AuthorizationID: claimed.ID, ProviderStateVersion: claimed.ProviderStateVersion,
		}, providerState)
		if err != nil {
			return commands.finishAuthorizationFailure(ctx, claimed, actorID, leaseToken, coredb.WorkspaceCredentialAuthorizationFailed, "provider_state_invalid")
		}
		defer clearCredentialBytes(sealed)
	}
	next := commands.Now().UTC().Add(time.Duration(interval) * time.Second)
	if next.After(claimed.ExpiresAt) {
		return commands.finishAuthorizationFailure(ctx, claimed, actorID, leaseToken, coredb.WorkspaceCredentialAuthorizationExpired, "expired_token")
	}
	record, err := commands.Store.FinishWorkspaceCredentialAuthorizationPoll(ctx, coredb.FinishWorkspaceCredentialAuthorizationPollCommand{
		WorkspaceID: claimed.WorkspaceID, Kind: claimed.Kind, AuthorizationID: claimed.ID, ActorID: actorID,
		LeaseToken: leaseToken, Status: coredb.WorkspaceCredentialAuthorizationPending,
		NextPollAt: next, PollInterval: interval, LastErrorCode: boundedAuthorizationErrorCode(errorCode),
		SealedProviderState: sealed,
	})
	if err != nil {
		return corecontract.PollWorkspaceCredentialAuthorizationResponse{}, err
	}
	return corecontract.PollWorkspaceCredentialAuthorizationResponse{Authorization: credentialAuthorization(record, nil)}, nil
}

func (commands StateStoreWorkspaceCredentialCommands) finishAuthorizationFailure(ctx context.Context, claimed coredb.WorkspaceCredentialAuthorizationRecord, actorID, leaseToken, status, errorCode string) (corecontract.PollWorkspaceCredentialAuthorizationResponse, error) {
	record, err := commands.Store.FinishWorkspaceCredentialAuthorizationPoll(ctx, coredb.FinishWorkspaceCredentialAuthorizationPollCommand{
		WorkspaceID: claimed.WorkspaceID, Kind: claimed.Kind, AuthorizationID: claimed.ID, ActorID: actorID,
		LeaseToken: leaseToken, Status: status, NextPollAt: claimed.NextPollAt,
		PollInterval: claimed.PollIntervalSeconds, LastErrorCode: boundedAuthorizationErrorCode(errorCode),
	})
	if err != nil {
		return corecontract.PollWorkspaceCredentialAuthorizationResponse{}, err
	}
	return corecontract.PollWorkspaceCredentialAuthorizationResponse{Authorization: credentialAuthorization(record, nil)}, nil
}

func (commands StateStoreWorkspaceCredentialCommands) authorizationBinding(ctx context.Context, record coredb.WorkspaceCredentialAuthorizationRecord) (*corecontract.WorkspaceCredentialMetadata, error) {
	if record.Status != coredb.WorkspaceCredentialAuthorizationSucceeded || record.BindingID == "" {
		return nil, nil
	}
	binding, err := commands.Store.Get(ctx, record.WorkspaceID, record.Kind, record.BindingID)
	if err != nil {
		return nil, err
	}
	defer clearCredentialBytes(binding.SealedSecret)
	if binding.ID == "" {
		return nil, nil
	}
	metadata := credentialMetadata(binding.Metadata())
	return &metadata, nil
}

func (commands StateStoreWorkspaceCredentialCommands) authorizationResponse(ctx context.Context, record coredb.WorkspaceCredentialAuthorizationRecord) (corecontract.WorkspaceCredentialAuthorization, error) {
	binding, err := commands.authorizationBinding(ctx, record)
	if err != nil {
		return corecontract.WorkspaceCredentialAuthorization{}, err
	}
	return credentialAuthorization(record, binding), nil
}

func credentialAuthorization(record coredb.WorkspaceCredentialAuthorizationRecord, binding *corecontract.WorkspaceCredentialMetadata) corecontract.WorkspaceCredentialAuthorization {
	return corecontract.WorkspaceCredentialAuthorization{
		ID: record.ID, WorkspaceID: record.WorkspaceID, Kind: record.Kind,
		TargetBindingID: record.TargetBindingID, Status: record.Status,
		UserCode: record.UserCode, VerificationURI: record.VerificationURI,
		VerificationURIComplete: record.VerificationURIComplete,
		PollIntervalSeconds:     record.PollIntervalSeconds, NextPollAt: record.NextPollAt.UTC(),
		ExpiresAt: record.ExpiresAt.UTC(), LastErrorCode: record.LastErrorCode,
		Version: record.Version, Binding: binding,
	}
}

func validateCredentialAuthorizationChallenge(challenge corecredentials.DeviceAuthorizationChallenge, now time.Time) error {
	if len(challenge.ProviderState) == 0 || len(challenge.ProviderState) > 256*1024 ||
		challenge.Interval < time.Second || challenge.Interval > time.Minute ||
		!challenge.ExpiresAt.After(now.Add(30*time.Second)) || challenge.ExpiresAt.After(now.Add(24*time.Hour)) ||
		len(challenge.UserCode) > 1024 || strings.ContainsAny(challenge.UserCode, "\x00\r\n") {
		return errors.New("device authorization challenge fields are invalid")
	}
	for _, raw := range []string{challenge.VerificationURI, challenge.VerificationURIComplete} {
		parsed, err := url.Parse(raw)
		if err != nil || len(raw) < 8 || len(raw) > 8192 || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || strings.ContainsAny(raw, "\x00\r\n") {
			return errors.New("device authorization verification URL is invalid")
		}
	}
	var metadata map[string]any
	if len(challenge.ProviderPublic) > 64*1024 || (len(challenge.ProviderPublic) > 0 && json.Unmarshal(challenge.ProviderPublic, &metadata) != nil) {
		return errors.New("device authorization public metadata is invalid")
	}
	return nil
}

func validateCredentialAuthorizationPoll(poll corecredentials.DeviceAuthorizationPollResult) error {
	switch poll.Status {
	case corecredentials.DeviceAuthorizationPending:
		if poll.RetryAfter < 0 || poll.RetryAfter > time.Minute || len(poll.ProviderState) > 256*1024 {
			return errors.New("pending device authorization result is invalid")
		}
	case corecredentials.DeviceAuthorizationSucceeded:
		if poll.Credential.AuthType == "" || len(poll.Credential.Secret) == 0 {
			return errors.New("successful device authorization result has no credential")
		}
	case corecredentials.DeviceAuthorizationDenied, corecredentials.DeviceAuthorizationExpired, corecredentials.DeviceAuthorizationFailed:
	default:
		return errors.New("device authorization status is invalid")
	}
	if len(poll.ErrorCode) > 128 || strings.ContainsAny(poll.ErrorCode, "\x00\r\n") {
		return errors.New("device authorization error code is invalid")
	}
	return nil
}

func boundedAuthorizationErrorCode(value string) string {
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "\x00\r\n") {
		return "provider_unavailable"
	}
	return value
}

func firstNonemptyCode(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func credentialAuthorizationError(code coredb.StateErrorCode, operation, authorizationID, message string) error {
	return &coredb.StateError{Code: code, Operation: operation, Resource: "credential_authorization", ResourceID: authorizationID, Message: message}
}

// logCredentialAuthorizationProviderFailure deliberately records only fixed
// classifications. Provider errors can contain URLs or upstream response
// details, and authorization state can contain tickets, device codes, or
// credentials, so none of those values cross this logging boundary.
func (commands StateStoreWorkspaceCredentialCommands) logCredentialAuthorizationProviderFailure(
	ctx context.Context,
	kind, stage string,
	err error,
) {
	if commands.Logger == nil || err == nil {
		return
	}
	commands.Logger.WarnContext(ctx, "workspace credential provider authorization did not complete",
		"provider_kind", kind,
		"stage", stage,
		"failure_class", credentialAuthorizationProviderFailureClass(err),
	)
}

func credentialAuthorizationProviderFailureClass(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		if networkError.Timeout() {
			return "timeout"
		}
		return "network"
	}
	var providerURL *url.Error
	if errors.As(err, &providerURL) {
		return "network"
	}
	return "rejected_or_invalid_response"
}
