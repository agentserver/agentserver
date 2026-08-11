package coreserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

func TestWorkspaceCredentialRefresherCompletesLeasedRefresh(t *testing.T) {
	now := time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC)
	keyring, binding := refreshTestBinding(t, now)
	access, refresh := now.Add(time.Hour), now.Add(24*time.Hour)
	provider := &refreshTestProvider{upload: corecredentials.UploadResult{
		AuthType: "device_oauth", PublicMetadata: json.RawMessage(`{"subject":"updated"}`),
		Secret: []byte("new-refresh-credential"), AccessExpiresAt: &access, RefreshExpiresAt: &refresh,
	}}
	registry, err := corecredentials.NewRegistry(provider)
	if err != nil {
		t.Fatal(err)
	}
	store := &refreshTestStore{binding: binding, ownsLease: true}
	refresher, err := NewWorkspaceCredentialRefresher(store, registry, keyring, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	reference := corecredentials.BindingReference{WorkspaceID: binding.WorkspaceID, Kind: binding.Kind, BindingID: binding.ID}
	if err := refresher.RefreshCredentialReference(t.Context(), reference); err != nil {
		t.Fatal(err)
	}
	if store.claims != 1 || store.completes != 1 || store.failures != 0 || provider.refreshes != 1 {
		t.Fatalf("refresh calls = claim:%d complete:%d fail:%d provider:%d", store.claims, store.completes, store.failures, provider.refreshes)
	}
	if !bytes.Equal(provider.seenSecret, []byte("old-refresh-credential")) {
		t.Fatalf("provider saw credential %q", provider.seenSecret)
	}
	command := store.complete
	if command.ExpectedAuthorityVersion != binding.AuthorityVersion || command.ExpectedCredentialVersion != binding.CredentialVersion ||
		command.LeaseToken == "" || command.AuthType != "device_oauth" || command.SealingKeyID != keyring.ActiveKeyID() ||
		command.AccessExpiresAt == nil || !command.AccessExpiresAt.Equal(access) || command.RefreshExpiresAt == nil || !command.RefreshExpiresAt.Equal(refresh) {
		t.Fatalf("complete command = %+v", command)
	}
	plaintext, err := keyring.Open(corecredentials.BindingSealScope{
		WorkspaceID: binding.WorkspaceID, BindingID: binding.ID, CredentialVersion: binding.CredentialVersion + 1,
	}, command.SealedSecret)
	if err != nil {
		t.Fatal(err)
	}
	defer clearCredentialBytes(plaintext)
	if !bytes.Equal(plaintext, []byte("new-refresh-credential")) {
		t.Fatalf("refreshed plaintext = %q", plaintext)
	}
}

func TestWorkspaceCredentialRefresherHonorsLeaseAndFailureClass(t *testing.T) {
	now := time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name          string
		ownsLease     bool
		accessExpiry  time.Time
		terminal      bool
		providerError error
		wantError     bool
		wantRefreshes int
		wantFailures  int
		wantTerminal  bool
	}{
		{
			name:      "another replica owns lease while old access token remains usable",
			ownsLease: false, accessExpiry: now.Add(2 * time.Minute), wantRefreshes: 0,
		},
		{
			name:      "transient provider failure releases lease while old token remains usable",
			ownsLease: true, accessExpiry: now.Add(2 * time.Minute), providerError: errors.New("temporary upstream failure"),
			wantRefreshes: 1, wantFailures: 1,
		},
		{
			name:      "terminal provider failure requires reauthorization",
			ownsLease: true, accessExpiry: now.Add(2 * time.Minute), terminal: true, providerError: errors.New("invalid refresh token"),
			wantError: true, wantRefreshes: 1, wantFailures: 1, wantTerminal: true,
		},
		{
			name:      "transient failure after access expiry fails closed",
			ownsLease: true, accessExpiry: now.Add(-time.Second), providerError: errors.New("temporary upstream failure"),
			wantError: true, wantRefreshes: 1, wantFailures: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			keyring, binding := refreshTestBinding(t, now)
			binding.AccessExpiresAt = test.accessExpiry
			provider := &refreshTestProvider{terminal: test.terminal, refreshErr: test.providerError}
			registry, err := corecredentials.NewRegistry(provider)
			if err != nil {
				t.Fatal(err)
			}
			store := &refreshTestStore{binding: binding, ownsLease: test.ownsLease}
			refresher, err := NewWorkspaceCredentialRefresher(store, registry, keyring, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			err = refresher.RefreshCredentialReference(t.Context(), corecredentials.BindingReference{
				WorkspaceID: binding.WorkspaceID, Kind: binding.Kind, BindingID: binding.ID,
			})
			if (err != nil) != test.wantError || provider.refreshes != test.wantRefreshes || store.failures != test.wantFailures {
				t.Fatalf("RefreshCredentialReference() error=%v refreshes=%d failures=%d", err, provider.refreshes, store.failures)
			}
			if test.wantFailures > 0 && store.failure.Terminal != test.wantTerminal {
				t.Fatalf("failure terminal = %v, want %v", store.failure.Terminal, test.wantTerminal)
			}
		})
	}
}

func TestWorkspaceCredentialRefresherSkipsFreshAndNonDeviceCredentials(t *testing.T) {
	now := time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name     string
		authType string
		expiry   time.Time
	}{
		{name: "fresh device credential", authType: "device_oauth", expiry: now.Add(6 * time.Minute)},
		{name: "static credential", authType: "static", expiry: now.Add(time.Minute)},
	} {
		t.Run(test.name, func(t *testing.T) {
			keyring, binding := refreshTestBinding(t, now)
			binding.AuthType, binding.AccessExpiresAt = test.authType, test.expiry
			provider := &refreshTestProvider{}
			registry, err := corecredentials.NewRegistry(provider)
			if err != nil {
				t.Fatal(err)
			}
			store := &refreshTestStore{binding: binding, ownsLease: true}
			refresher, err := NewWorkspaceCredentialRefresher(store, registry, keyring, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			if err := refresher.RefreshCredentialReference(t.Context(), corecredentials.BindingReference{
				WorkspaceID: binding.WorkspaceID, Kind: binding.Kind, BindingID: binding.ID,
			}); err != nil {
				t.Fatal(err)
			}
			if store.claims != 0 || provider.refreshes != 0 {
				t.Fatalf("unexpected refresh calls = claim:%d provider:%d", store.claims, provider.refreshes)
			}
		})
	}
}

func refreshTestBinding(t *testing.T, now time.Time) (*corecredentials.Keyring, corecredentials.Binding) {
	t.Helper()
	keyring, err := corecredentials.NewKeyring("test-key", map[string][]byte{"test-key": bytes.Repeat([]byte{7}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	binding := corecredentials.Binding{
		ID: "binding-1", WorkspaceID: "workspace-1", Kind: "refresh-test", DisplayName: "Refresh test",
		OwnerScope: corecredentials.OwnerScopeWorkspace, AuthType: "device_oauth",
		AuthorityVersion: 3, CredentialVersion: 4, Status: corecredentials.StatusActive,
		AccessExpiresAt: now.Add(2 * time.Minute),
	}
	refreshExpiry := now.Add(48 * time.Hour)
	binding.RefreshExpiresAt = &refreshExpiry
	binding.SealedSecret, err = keyring.Seal(corecredentials.BindingSealScope{
		WorkspaceID: binding.WorkspaceID, BindingID: binding.ID, CredentialVersion: binding.CredentialVersion,
	}, []byte("old-refresh-credential"))
	if err != nil {
		t.Fatal(err)
	}
	return keyring, binding
}

type refreshTestStore struct {
	binding   corecredentials.Binding
	ownsLease bool
	claims    int
	completes int
	failures  int
	claim     coredb.ClaimWorkspaceCredentialRefreshCommand
	complete  coredb.CompleteWorkspaceCredentialRefreshCommand
	failure   coredb.FailWorkspaceCredentialRefreshCommand
}

func (store *refreshTestStore) Get(context.Context, string, string, string) (corecredentials.Binding, error) {
	return cloneRefreshTestBinding(store.binding), nil
}

func (*refreshTestStore) List(context.Context, string, string) ([]corecredentials.BindingMetadata, error) {
	return nil, nil
}

func (store *refreshTestStore) ClaimWorkspaceCredentialRefresh(_ context.Context, command coredb.ClaimWorkspaceCredentialRefreshCommand) (corecredentials.Binding, bool, error) {
	store.claims++
	store.claim = command
	return cloneRefreshTestBinding(store.binding), store.ownsLease, nil
}

func (store *refreshTestStore) CompleteWorkspaceCredentialRefresh(_ context.Context, command coredb.CompleteWorkspaceCredentialRefreshCommand) (corecredentials.Binding, error) {
	store.completes++
	command.SealedSecret = append([]byte(nil), command.SealedSecret...)
	command.PublicMetadata = append(json.RawMessage(nil), command.PublicMetadata...)
	store.complete = command
	return cloneRefreshTestBinding(store.binding), nil
}

func (store *refreshTestStore) FailWorkspaceCredentialRefresh(_ context.Context, command coredb.FailWorkspaceCredentialRefreshCommand) (corecredentials.Binding, error) {
	store.failures++
	store.failure = command
	return cloneRefreshTestBinding(store.binding), nil
}

func cloneRefreshTestBinding(binding corecredentials.Binding) corecredentials.Binding {
	binding.SealedSecret = append([]byte(nil), binding.SealedSecret...)
	binding.PublicMetadata = append(json.RawMessage(nil), binding.PublicMetadata...)
	if binding.RefreshExpiresAt != nil {
		value := *binding.RefreshExpiresAt
		binding.RefreshExpiresAt = &value
	}
	return binding
}

type refreshTestProvider struct {
	upload     corecredentials.UploadResult
	terminal   bool
	refreshErr error
	refreshes  int
	seenSecret []byte
}

func (*refreshTestProvider) Kind() string { return "refresh-test" }

func (provider *refreshTestProvider) ValidateUpload(authType string, raw []byte) (corecredentials.UploadResult, error) {
	if authType != "device_oauth" || len(raw) == 0 {
		return corecredentials.UploadResult{}, errors.New("invalid refresh upload")
	}
	result := provider.upload
	result.AuthType = authType
	result.Secret = append([]byte(nil), raw...)
	result.PublicMetadata = append(json.RawMessage(nil), provider.upload.PublicMetadata...)
	return result, nil
}

func (*refreshTestProvider) Materialize(context.Context, corecredentials.Binding, []byte, corecredentials.UseRequest) (corecredentials.HeaderMutation, error) {
	return corecredentials.HeaderMutation{}, nil
}

func (*refreshTestProvider) AllowedHeaders() []string { return []string{"Authorization"} }

func (provider *refreshTestProvider) RefreshDeviceCredential(_ context.Context, _ corecredentials.Binding, raw []byte) (corecredentials.UploadResult, bool, error) {
	provider.refreshes++
	provider.seenSecret = append([]byte(nil), raw...)
	result := provider.upload
	result.Secret = append([]byte(nil), provider.upload.Secret...)
	result.PublicMetadata = append(json.RawMessage(nil), provider.upload.PublicMetadata...)
	return result, provider.terminal, provider.refreshErr
}

var _ WorkspaceCredentialRefreshStore = (*refreshTestStore)(nil)
var _ corecredentials.RefreshingProvider = (*refreshTestProvider)(nil)
