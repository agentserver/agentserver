package coreserver

import (
	"context"
	"errors"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

const (
	workspaceCredentialRefreshAhead = 5 * time.Minute
	workspaceCredentialRefreshLease = 30 * time.Second
)

type WorkspaceCredentialRefreshStore interface {
	corecredentials.BindingStore
	ClaimWorkspaceCredentialRefresh(context.Context, coredb.ClaimWorkspaceCredentialRefreshCommand) (corecredentials.Binding, bool, error)
	CompleteWorkspaceCredentialRefresh(context.Context, coredb.CompleteWorkspaceCredentialRefreshCommand) (corecredentials.Binding, error)
	FailWorkspaceCredentialRefresh(context.Context, coredb.FailWorkspaceCredentialRefreshCommand) (corecredentials.Binding, error)
}

type CredentialReferenceRefresher interface {
	RefreshCredentialReference(context.Context, corecredentials.BindingReference) error
}

type WorkspaceCredentialRefresher struct {
	store    WorkspaceCredentialRefreshStore
	registry *corecredentials.ProviderRegistry
	sealer   *corecredentials.Keyring
	now      func() time.Time
}

func NewWorkspaceCredentialRefresher(store WorkspaceCredentialRefreshStore, registry *corecredentials.ProviderRegistry, sealer *corecredentials.Keyring, now func() time.Time) (*WorkspaceCredentialRefresher, error) {
	if store == nil || registry == nil || sealer == nil || now == nil {
		return nil, errors.New("workspace credential refresh store, registry, sealer, and clock are required")
	}
	return &WorkspaceCredentialRefresher{store: store, registry: registry, sealer: sealer, now: now}, nil
}

func (refresher *WorkspaceCredentialRefresher) RefreshCredentialReference(ctx context.Context, reference corecredentials.BindingReference) error {
	if refresher == nil || refresher.store == nil || refresher.registry == nil || refresher.sealer == nil || refresher.now == nil {
		return errors.New("workspace credential refresher is unavailable")
	}
	if reference.BindingID == "" {
		return nil
	}
	now := refresher.now().UTC()
	binding, err := refresher.store.Get(ctx, reference.WorkspaceID, reference.Kind, reference.BindingID)
	if err != nil {
		return err
	}
	defer clearCredentialBytes(binding.SealedSecret)
	if binding.ID == "" || binding.AuthType != "device_oauth" || binding.AccessExpiresAt.After(now.Add(workspaceCredentialRefreshAhead)) {
		return nil
	}
	leaseToken, err := newCredentialID()
	if err != nil {
		return errors.New("allocate workspace credential refresh lease")
	}
	claimed, ownsLease, err := refresher.store.ClaimWorkspaceCredentialRefresh(ctx, coredb.ClaimWorkspaceCredentialRefreshCommand{
		WorkspaceID: reference.WorkspaceID, Kind: reference.Kind, BindingID: reference.BindingID,
		Before: now.Add(workspaceCredentialRefreshAhead), LeaseToken: leaseToken,
		LeaseExpiresAt: now.Add(workspaceCredentialRefreshLease),
	})
	if err != nil {
		return err
	}
	defer clearCredentialBytes(claimed.SealedSecret)
	if !ownsLease {
		if claimed.Status == corecredentials.StatusActive && claimed.AccessExpiresAt.After(now.Add(time.Second)) {
			return nil
		}
		return errors.New("workspace credential refresh is already in progress")
	}
	provider, ok := refresher.registry.Lookup(reference.Kind)
	if !ok {
		return refresher.fail(ctx, claimed, leaseToken, "provider_unavailable", true, errors.New("credential refresh provider is unavailable"))
	}
	refreshing, ok := provider.(corecredentials.RefreshingProvider)
	if !ok {
		return refresher.fail(ctx, claimed, leaseToken, "refresh_unsupported", true, errors.New("credential provider does not support refresh"))
	}
	plaintext, err := refresher.sealer.Open(corecredentials.BindingSealScope{
		WorkspaceID: claimed.WorkspaceID, BindingID: claimed.ID, CredentialVersion: claimed.CredentialVersion,
	}, claimed.SealedSecret)
	if err != nil {
		return refresher.fail(ctx, claimed, leaseToken, "credential_invalid", true, err)
	}
	defer clearCredentialBytes(plaintext)
	upload, terminal, refreshErr := refreshing.RefreshDeviceCredential(ctx, claimed, plaintext)
	if refreshErr != nil {
		if !terminal && claimed.AccessExpiresAt.After(now.Add(time.Second)) {
			_ = refresher.fail(ctx, claimed, leaseToken, "provider_unavailable", false, nil)
			return nil
		}
		return refresher.fail(ctx, claimed, leaseToken, "refresh_failed", terminal, refreshErr)
	}
	defer clearCredentialBytes(upload.Secret)
	validated, err := provider.ValidateUpload(upload.AuthType, upload.Secret)
	if err != nil {
		return refresher.fail(ctx, claimed, leaseToken, "credential_invalid", true, err)
	}
	defer clearCredentialBytes(validated.Secret)
	sealed, err := refresher.sealer.Seal(corecredentials.BindingSealScope{
		WorkspaceID: claimed.WorkspaceID, BindingID: claimed.ID, CredentialVersion: claimed.CredentialVersion + 1,
	}, validated.Secret)
	if err != nil {
		return refresher.fail(ctx, claimed, leaseToken, "sealing_unavailable", false, err)
	}
	defer clearCredentialBytes(sealed)
	_, err = refresher.store.CompleteWorkspaceCredentialRefresh(ctx, coredb.CompleteWorkspaceCredentialRefreshCommand{
		WorkspaceID: claimed.WorkspaceID, Kind: claimed.Kind, BindingID: claimed.ID,
		ExpectedAuthorityVersion: claimed.AuthorityVersion, ExpectedCredentialVersion: claimed.CredentialVersion,
		LeaseToken: leaseToken, AuthType: validated.AuthType,
		PublicMetadata: choosePublicMetadata(upload.PublicMetadata, validated.PublicMetadata),
		SealedSecret:   sealed, SealingKeyID: refresher.sealer.ActiveKeyID(),
		AccessExpiresAt:  chooseTime(upload.AccessExpiresAt, validated.AccessExpiresAt),
		RefreshExpiresAt: chooseTime(upload.RefreshExpiresAt, validated.RefreshExpiresAt),
	})
	return err
}

func (refresher *WorkspaceCredentialRefresher) fail(ctx context.Context, binding corecredentials.Binding, leaseToken, code string, terminal bool, cause error) error {
	_, err := refresher.store.FailWorkspaceCredentialRefresh(ctx, coredb.FailWorkspaceCredentialRefreshCommand{
		WorkspaceID: binding.WorkspaceID, Kind: binding.Kind, BindingID: binding.ID,
		ExpectedAuthorityVersion: binding.AuthorityVersion, ExpectedCredentialVersion: binding.CredentialVersion,
		LeaseToken: leaseToken, ErrorCode: code, Terminal: terminal,
	})
	if err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

var _ CredentialReferenceRefresher = (*WorkspaceCredentialRefresher)(nil)
