package coreserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

type StateStoreWorkspaceCredentialCommands struct {
	Store    *coredb.StateStore
	Registry *corecredentials.ProviderRegistry
	Sealer   *corecredentials.Keyring
	Now      func() time.Time
}

func (commands StateStoreWorkspaceCredentialCommands) ListSchemas(context.Context) (corecontract.ListWorkspaceCredentialProviderSchemasResponse, error) {
	if commands.Registry == nil {
		return corecontract.ListWorkspaceCredentialProviderSchemasResponse{}, errors.New("credential provider registry is unavailable")
	}
	schemas := commands.Registry.Schemas()
	result := make([]corecontract.WorkspaceCredentialProviderSchema, len(schemas))
	for index, schema := range schemas {
		result[index] = corecontract.WorkspaceCredentialProviderSchema{
			Kind: schema.Kind, DisplayName: schema.DisplayName,
			AuthTypes:            append([]string(nil), schema.AuthTypes...),
			AllowedHosts:         append([]string(nil), schema.AllowedHosts...),
			AllowedHeaders:       append([]string(nil), schema.AllowedHeaders...),
			SecretFormat:         schema.SecretFormat,
			AuthorizationMethods: append([]string(nil), schema.AuthorizationMethods...),
		}
	}
	return corecontract.ListWorkspaceCredentialProviderSchemasResponse{Providers: result}, nil
}

func (commands StateStoreWorkspaceCredentialCommands) ListBindings(ctx context.Context, workspaceID, kind, actorID string) (corecontract.ListWorkspaceCredentialsResponse, error) {
	if commands.Store == nil {
		return corecontract.ListWorkspaceCredentialsResponse{}, errors.New("credential binding store is unavailable")
	}
	if err := commands.requireCredentialManager(ctx, workspaceID, actorID); err != nil {
		return corecontract.ListWorkspaceCredentialsResponse{}, err
	}
	items, err := commands.Store.List(ctx, workspaceID, kind)
	if err != nil {
		return corecontract.ListWorkspaceCredentialsResponse{}, err
	}
	return corecontract.ListWorkspaceCredentialsResponse{Bindings: credentialMetadataList(items)}, nil
}

func (commands StateStoreWorkspaceCredentialCommands) CreateBinding(
	ctx context.Context,
	workspaceID, kind, actorID string,
	input corecontract.CreateWorkspaceCredentialRequest,
) (corecontract.CreateWorkspaceCredentialResponse, error) {
	if err := commands.readyForWrite(); err != nil {
		return corecontract.CreateWorkspaceCredentialResponse{}, err
	}
	// Authorize before parsing or sealing user-supplied secret material. The
	// StateStore repeats this check in the write transaction so a membership or
	// workspace-status change between the two checks still fails closed.
	if err := commands.requireCredentialManager(ctx, workspaceID, actorID); err != nil {
		return corecontract.CreateWorkspaceCredentialResponse{}, err
	}
	provider, ok := commands.Registry.Lookup(kind)
	if !ok {
		return corecontract.CreateWorkspaceCredentialResponse{}, credentialCommandError(coredb.ErrorInvalidArgument, "CreateWorkspaceCredentialBinding", "", "provider kind is not installed")
	}
	secret, err := rawCredentialSecret(input.Secret)
	if err != nil {
		return corecontract.CreateWorkspaceCredentialResponse{}, credentialCommandError(coredb.ErrorInvalidArgument, "CreateWorkspaceCredentialBinding", input.ID, err.Error())
	}
	defer clearCredentialBytes(secret)
	upload, err := provider.ValidateUpload(input.AuthType, secret)
	if err != nil {
		return corecontract.CreateWorkspaceCredentialResponse{}, credentialCommandError(coredb.ErrorInvalidArgument, "CreateWorkspaceCredentialBinding", input.ID, "credential secret does not match the provider schema")
	}
	defer clearCredentialBytes(upload.Secret)
	bindingID := input.ID
	if bindingID == "" {
		bindingID, err = newCredentialID()
		if err != nil {
			return corecontract.CreateWorkspaceCredentialResponse{}, errors.New("allocate credential binding ID")
		}
	}
	ownerScope := input.OwnerScope
	if ownerScope == "" {
		ownerScope = corecredentials.OwnerScopeWorkspace
	}
	ownerUserID := input.OwnerUserID
	if ownerScope == corecredentials.OwnerScopeUser && ownerUserID == "" {
		ownerUserID = actorID
	}
	sealed, err := commands.Sealer.Seal(corecredentials.BindingSealScope{
		WorkspaceID: workspaceID, BindingID: bindingID, CredentialVersion: 1,
	}, upload.Secret)
	if err != nil {
		return corecontract.CreateWorkspaceCredentialResponse{}, errors.New("seal workspace credential")
	}
	authType := upload.AuthType
	if authType == "" {
		return corecontract.CreateWorkspaceCredentialResponse{}, credentialCommandError(coredb.ErrorInvalidArgument, "CreateWorkspaceCredentialBinding", bindingID, "provider did not return an auth type")
	}
	accessExpiry := chooseTime(input.AccessExpiresAt, upload.AccessExpiresAt)
	refreshExpiry := chooseTime(input.RefreshExpiresAt, upload.RefreshExpiresAt)
	result, err := commands.Store.CreateWorkspaceCredentialBinding(ctx, coredb.CreateWorkspaceCredentialBindingCommand{
		ID: bindingID, WorkspaceID: workspaceID, ActorID: actorID, Kind: kind,
		DisplayName: input.DisplayName, OwnerScope: ownerScope, OwnerUserID: ownerUserID,
		PublicMetadata: choosePublicMetadata(input.PublicMetadata, upload.PublicMetadata),
		AuthType:       authType, SealedSecret: sealed, SealingKeyID: commands.Sealer.ActiveKeyID(),
		AccessExpiresAt: accessExpiry, RefreshExpiresAt: refreshExpiry, MakeDefault: input.MakeDefault,
	})
	if err != nil {
		return corecontract.CreateWorkspaceCredentialResponse{}, err
	}
	return corecontract.CreateWorkspaceCredentialResponse{Binding: credentialMetadata(result.Binding.Metadata()), Created: true}, nil
}

func (commands StateStoreWorkspaceCredentialCommands) RotateBinding(
	ctx context.Context,
	workspaceID, kind, bindingID, actorID string,
	input corecontract.RotateWorkspaceCredentialRequest,
) (corecontract.RotateWorkspaceCredentialResponse, error) {
	if err := commands.readyForWrite(); err != nil {
		return corecontract.RotateWorkspaceCredentialResponse{}, err
	}
	if err := commands.requireCredentialManager(ctx, workspaceID, actorID); err != nil {
		return corecontract.RotateWorkspaceCredentialResponse{}, err
	}
	binding, err := commands.Store.Get(ctx, workspaceID, kind, bindingID)
	if err != nil {
		return corecontract.RotateWorkspaceCredentialResponse{}, err
	}
	if binding.ID == "" {
		return corecontract.RotateWorkspaceCredentialResponse{}, credentialCommandError(coredb.ErrorNotFound, "RotateWorkspaceCredentialBinding", bindingID, "credential binding was not found")
	}
	defer clearCredentialBytes(binding.SealedSecret)
	if binding.AuthorityVersion != input.ExpectedAuthorityVersion || binding.CredentialVersion != input.ExpectedCredentialVersion {
		return corecontract.RotateWorkspaceCredentialResponse{}, credentialCommandError(coredb.ErrorVersionConflict, "RotateWorkspaceCredentialBinding", bindingID, "credential binding version changed")
	}
	provider, ok := commands.Registry.Lookup(kind)
	if !ok {
		return corecontract.RotateWorkspaceCredentialResponse{}, credentialCommandError(coredb.ErrorInvalidArgument, "RotateWorkspaceCredentialBinding", bindingID, "provider kind is not installed")
	}
	secret, err := rawCredentialSecret(input.Secret)
	if err != nil {
		return corecontract.RotateWorkspaceCredentialResponse{}, credentialCommandError(coredb.ErrorInvalidArgument, "RotateWorkspaceCredentialBinding", bindingID, err.Error())
	}
	defer clearCredentialBytes(secret)
	upload, err := provider.ValidateUpload(input.AuthType, secret)
	if err != nil {
		return corecontract.RotateWorkspaceCredentialResponse{}, credentialCommandError(coredb.ErrorInvalidArgument, "RotateWorkspaceCredentialBinding", bindingID, "credential secret does not match the provider schema")
	}
	defer clearCredentialBytes(upload.Secret)
	nextCredentialVersion := binding.CredentialVersion + 1
	sealed, err := commands.Sealer.Seal(corecredentials.BindingSealScope{
		WorkspaceID: workspaceID, BindingID: bindingID, CredentialVersion: nextCredentialVersion,
	}, upload.Secret)
	if err != nil {
		return corecontract.RotateWorkspaceCredentialResponse{}, errors.New("seal rotated workspace credential")
	}
	authType := upload.AuthType
	if authType == "" {
		return corecontract.RotateWorkspaceCredentialResponse{}, credentialCommandError(coredb.ErrorInvalidArgument, "RotateWorkspaceCredentialBinding", bindingID, "provider did not return an auth type")
	}
	result, err := commands.Store.RotateWorkspaceCredentialBinding(ctx, coredb.RotateWorkspaceCredentialBindingCommand{
		WorkspaceID: workspaceID, BindingID: bindingID, ActorID: actorID,
		ExpectedAuthorityVersion:  input.ExpectedAuthorityVersion,
		ExpectedCredentialVersion: input.ExpectedCredentialVersion,
		SealedSecret:              sealed, SealingKeyID: commands.Sealer.ActiveKeyID(), AuthType: authType,
		AccessExpiresAt:  chooseTime(input.AccessExpiresAt, upload.AccessExpiresAt),
		RefreshExpiresAt: chooseTime(input.RefreshExpiresAt, upload.RefreshExpiresAt),
	})
	if err != nil {
		return corecontract.RotateWorkspaceCredentialResponse{}, err
	}
	return corecontract.RotateWorkspaceCredentialResponse{Binding: credentialMetadata(result.Binding.Metadata()), Changed: result.Changed}, nil
}

func (commands StateStoreWorkspaceCredentialCommands) RenameBinding(
	ctx context.Context,
	workspaceID, kind, bindingID, actorID string,
	input corecontract.RenameWorkspaceCredentialRequest,
) (corecontract.RenameWorkspaceCredentialResponse, error) {
	if commands.Store == nil {
		return corecontract.RenameWorkspaceCredentialResponse{}, errors.New("credential binding store is unavailable")
	}
	if err := commands.requireCredentialManager(ctx, workspaceID, actorID); err != nil {
		return corecontract.RenameWorkspaceCredentialResponse{}, err
	}
	binding, err := commands.Store.Get(ctx, workspaceID, kind, bindingID)
	if err != nil {
		return corecontract.RenameWorkspaceCredentialResponse{}, err
	}
	if binding.ID == "" {
		return corecontract.RenameWorkspaceCredentialResponse{}, credentialCommandError(coredb.ErrorNotFound, "UpdateWorkspaceCredentialBinding", bindingID, "credential binding was not found")
	}
	defer clearCredentialBytes(binding.SealedSecret)
	result, err := commands.Store.UpdateWorkspaceCredentialBinding(ctx, coredb.UpdateWorkspaceCredentialBindingCommand{
		WorkspaceID: workspaceID, BindingID: bindingID, ActorID: actorID,
		ExpectedVersion: input.ExpectedAuthorityVersion, DisplayName: input.DisplayName,
		PublicMetadata: binding.PublicMetadata,
	})
	if err != nil {
		return corecontract.RenameWorkspaceCredentialResponse{}, err
	}
	return corecontract.RenameWorkspaceCredentialResponse{Binding: credentialMetadata(result.Binding.Metadata()), Changed: result.Changed}, nil
}

func (commands StateStoreWorkspaceCredentialCommands) RevokeBinding(ctx context.Context, workspaceID, kind, bindingID, actorID string, expectedVersion int64) (corecontract.RevokeWorkspaceCredentialResponse, error) {
	if commands.Store == nil {
		return corecontract.RevokeWorkspaceCredentialResponse{}, errors.New("credential binding store is unavailable")
	}
	if err := commands.requireCredentialManager(ctx, workspaceID, actorID); err != nil {
		return corecontract.RevokeWorkspaceCredentialResponse{}, err
	}
	if err := commands.requireKind(ctx, workspaceID, kind, bindingID); err != nil {
		return corecontract.RevokeWorkspaceCredentialResponse{}, err
	}
	result, err := commands.Store.RevokeWorkspaceCredentialBinding(ctx, coredb.RevokeWorkspaceCredentialBindingCommand{
		WorkspaceID: workspaceID, BindingID: bindingID, ActorID: actorID, ExpectedVersion: expectedVersion,
	})
	if err != nil {
		return corecontract.RevokeWorkspaceCredentialResponse{}, err
	}
	return corecontract.RevokeWorkspaceCredentialResponse{Binding: credentialMetadata(result.Binding.Metadata()), Changed: result.Changed}, nil
}

func (commands StateStoreWorkspaceCredentialCommands) DeleteBinding(ctx context.Context, workspaceID, kind, bindingID, actorID string, expectedVersion int64) (corecontract.DeleteWorkspaceCredentialResponse, error) {
	if commands.Store == nil {
		return corecontract.DeleteWorkspaceCredentialResponse{}, errors.New("credential binding store is unavailable")
	}
	if err := commands.requireCredentialManager(ctx, workspaceID, actorID); err != nil {
		return corecontract.DeleteWorkspaceCredentialResponse{}, err
	}
	if err := commands.requireKind(ctx, workspaceID, kind, bindingID); err != nil {
		return corecontract.DeleteWorkspaceCredentialResponse{}, err
	}
	id, deleted, err := commands.Store.DeleteWorkspaceCredentialBinding(ctx, coredb.DeleteWorkspaceCredentialBindingCommand{
		WorkspaceID: workspaceID, BindingID: bindingID, ActorID: actorID, ExpectedVersion: expectedVersion,
	})
	return corecontract.DeleteWorkspaceCredentialResponse{BindingID: id, Deleted: deleted}, err
}

func (commands StateStoreWorkspaceCredentialCommands) SetDefaultBinding(ctx context.Context, workspaceID, kind, bindingID, actorID string, expectedVersion int64) (corecontract.SetDefaultWorkspaceCredentialResponse, error) {
	if commands.Store == nil {
		return corecontract.SetDefaultWorkspaceCredentialResponse{}, errors.New("credential binding store is unavailable")
	}
	if err := commands.requireCredentialManager(ctx, workspaceID, actorID); err != nil {
		return corecontract.SetDefaultWorkspaceCredentialResponse{}, err
	}
	if err := commands.requireKind(ctx, workspaceID, kind, bindingID); err != nil {
		return corecontract.SetDefaultWorkspaceCredentialResponse{}, err
	}
	result, err := commands.Store.SetDefaultWorkspaceCredentialBinding(ctx, workspaceID, bindingID, actorID, expectedVersion)
	if err != nil {
		return corecontract.SetDefaultWorkspaceCredentialResponse{}, err
	}
	return corecontract.SetDefaultWorkspaceCredentialResponse{Binding: credentialMetadata(result.Binding.Metadata()), Changed: result.Changed}, nil
}

func (commands StateStoreWorkspaceCredentialCommands) readyForWrite() error {
	if commands.Store == nil || commands.Registry == nil || commands.Sealer == nil {
		return errors.New("workspace credential write path is not configured")
	}
	return nil
}

func (commands StateStoreWorkspaceCredentialCommands) requireCredentialManager(ctx context.Context, workspaceID, actorID string) error {
	if commands.Store == nil {
		return errors.New("credential binding store is unavailable")
	}
	workspace, err := commands.Store.GetPlatformWorkspace(ctx, workspaceID, actorID)
	if err != nil {
		return err
	}
	if workspace.Status != coredb.WorkspaceStatusActive || workspace.Role != coredb.WorkspaceRoleOwner {
		return credentialCommandError(
			coredb.ErrorForbidden,
			"AuthorizeWorkspaceCredentialManager",
			workspaceID,
			"active workspace owner membership is required",
		)
	}
	return nil
}

func (commands StateStoreWorkspaceCredentialCommands) requireKind(ctx context.Context, workspaceID, kind, bindingID string) error {
	binding, err := commands.Store.Get(ctx, workspaceID, kind, bindingID)
	if err != nil {
		return err
	}
	defer clearCredentialBytes(binding.SealedSecret)
	if binding.ID == "" {
		return credentialCommandError(coredb.ErrorNotFound, "WorkspaceCredential", bindingID, "credential binding was not found")
	}
	return nil
}

func credentialMetadataList(items []corecredentials.BindingMetadata) []corecontract.WorkspaceCredentialMetadata {
	result := make([]corecontract.WorkspaceCredentialMetadata, len(items))
	for index, item := range items {
		result[index] = credentialMetadata(item)
	}
	return result
}

func credentialMetadata(item corecredentials.BindingMetadata) corecontract.WorkspaceCredentialMetadata {
	return corecontract.WorkspaceCredentialMetadata{
		ID: item.ID, WorkspaceID: item.WorkspaceID, Kind: item.Kind, DisplayName: item.DisplayName,
		OwnerScope: item.OwnerScope, OwnerUserID: item.OwnerUserID,
		PublicMetadata: append(json.RawMessage(nil), item.PublicMetadata...),
		AuthType:       item.AuthType, AuthorityVersion: item.AuthorityVersion, CredentialVersion: item.CredentialVersion,
		Status: item.Status, IsDefault: item.IsDefault,
		AccessExpiresAt: copyTime(item.AccessExpiresAt), RefreshExpiresAt: copyTime(item.RefreshExpiresAt),
	}
}

func rawCredentialSecret(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, errors.New("secret is required")
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || len(trimmed) > 512*1024 {
		return nil, errors.New("secret is empty or exceeds its size limit")
	}
	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil || value == "" {
			return nil, errors.New("secret string is invalid")
		}
		return []byte(value), nil
	}
	return append([]byte(nil), trimmed...), nil
}

func choosePublicMetadata(requested, parsed json.RawMessage) json.RawMessage {
	if len(parsed) > 0 {
		return append(json.RawMessage(nil), parsed...)
	}
	if len(requested) > 0 {
		return append(json.RawMessage(nil), requested...)
	}
	return json.RawMessage("{}")
}

func chooseTime(requested, parsed *time.Time) *time.Time {
	if parsed != nil {
		value := parsed.UTC()
		return &value
	}
	if requested != nil {
		value := requested.UTC()
		return &value
	}
	return nil
}

func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func credentialCommandError(code coredb.StateErrorCode, operation, bindingID, message string) error {
	return &coredb.StateError{Code: code, Operation: operation, Resource: "credential", ResourceID: bindingID, Message: message}
}

func newCredentialID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return hex.EncodeToString(raw[0:4]) + "-" + hex.EncodeToString(raw[4:6]) + "-" +
		hex.EncodeToString(raw[6:8]) + "-" + hex.EncodeToString(raw[8:10]) + "-" + hex.EncodeToString(raw[10:16]), nil
}

func clearCredentialBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

var _ WorkspaceCredentialCommands = StateStoreWorkspaceCredentialCommands{}
