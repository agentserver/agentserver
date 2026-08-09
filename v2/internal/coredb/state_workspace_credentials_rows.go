package coredb

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecredentials"
)

func scanWorkspaceCredentialBinding(scanner rowScanner, operation string) (corecredentials.Binding, error) {
	var binding corecredentials.Binding
	var ownerUserID *string
	var publicMetadata []byte
	var sealedSecret []byte
	var accessExpiresAt *time.Time
	var refreshExpiresAt *time.Time
	if err := scanner.Scan(
		&binding.ID, &binding.WorkspaceID, &binding.Kind, &binding.DisplayName, &binding.OwnerScope,
		&ownerUserID, &publicMetadata, &binding.AuthType, &sealedSecret,
		&binding.AuthorityVersion, &binding.CredentialVersion, &binding.Status, &binding.IsDefault,
		&accessExpiresAt, &refreshExpiresAt,
	); err != nil {
		return corecredentials.Binding{}, databaseError(operation+" scan binding", err)
	}
	if ownerUserID != nil {
		binding.OwnerUserID = *ownerUserID
	}
	if len(publicMetadata) == 0 {
		binding.PublicMetadata = json.RawMessage("{}")
	} else {
		binding.PublicMetadata = append(json.RawMessage(nil), publicMetadata...)
	}
	binding.SealedSecret = append([]byte(nil), sealedSecret...)
	if accessExpiresAt != nil {
		binding.AccessExpiresAt = accessExpiresAt.UTC()
	}
	if refreshExpiresAt != nil {
		value := refreshExpiresAt.UTC()
		binding.RefreshExpiresAt = &value
	}
	if err := validateScannedCredentialBinding(binding); err != nil {
		return corecredentials.Binding{}, databaseError(operation+" validate binding", err)
	}
	return binding, nil
}

func scanWorkspaceCredentialMetadata(scanner rowScanner, operation string) (corecredentials.BindingMetadata, error) {
	var metadata corecredentials.BindingMetadata
	var ownerUserID *string
	var publicMetadata []byte
	var accessExpiresAt *time.Time
	var refreshExpiresAt *time.Time
	if err := scanner.Scan(
		&metadata.ID, &metadata.WorkspaceID, &metadata.Kind, &metadata.DisplayName, &metadata.OwnerScope,
		&ownerUserID, &publicMetadata, &metadata.AuthType,
		&metadata.AuthorityVersion, &metadata.CredentialVersion, &metadata.Status, &metadata.IsDefault,
		&accessExpiresAt, &refreshExpiresAt,
	); err != nil {
		return corecredentials.BindingMetadata{}, databaseError(operation+" scan metadata", err)
	}
	if ownerUserID != nil {
		metadata.OwnerUserID = *ownerUserID
	}
	if len(publicMetadata) == 0 {
		metadata.PublicMetadata = json.RawMessage("{}")
	} else {
		metadata.PublicMetadata = append(json.RawMessage(nil), publicMetadata...)
	}
	if accessExpiresAt != nil {
		value := accessExpiresAt.UTC()
		metadata.AccessExpiresAt = &value
	}
	if refreshExpiresAt != nil {
		value := refreshExpiresAt.UTC()
		metadata.RefreshExpiresAt = &value
	}
	return metadata, nil
}

func validateScannedCredentialBinding(binding corecredentials.Binding) error {
	if binding.ID == "" || binding.WorkspaceID == "" || binding.Kind == "" ||
		binding.AuthorityVersion < 1 || binding.CredentialVersion < 1 || binding.Status == "" {
		return fmt.Errorf("stored credential binding identity or version is invalid")
	}
	return nil
}
