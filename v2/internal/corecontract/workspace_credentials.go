package corecontract

import (
	"encoding/json"
	"net/url"
	"time"
)

const (
	WorkspaceCredentialProviderSchemasPath    = "/v2/credential-providers"
	WorkspaceCredentialCollectionRoutePattern = "/v2/workspaces/{workspaceId}/credentials/{kind}"
	WorkspaceCredentialResourceRoutePattern   = "/v2/workspaces/{workspaceId}/credentials/{kind}/{bindingId}"
)

func WorkspaceCredentialCollectionPath(workspaceID, kind string) string {
	return "/v2/workspaces/" + url.PathEscape(workspaceID) + "/credentials/" + url.PathEscape(kind)
}

func WorkspaceCredentialPath(workspaceID, kind, bindingID string) string {
	return WorkspaceCredentialCollectionPath(workspaceID, kind) + "/" + url.PathEscape(bindingID)
}

func RotateWorkspaceCredentialPath(workspaceID, kind, bindingID string) string {
	return WorkspaceCredentialPath(workspaceID, kind, bindingID) + ":rotate"
}

func RevokeWorkspaceCredentialPath(workspaceID, kind, bindingID string) string {
	return WorkspaceCredentialPath(workspaceID, kind, bindingID) + ":revoke"
}

func DeleteWorkspaceCredentialPath(workspaceID, kind, bindingID string) string {
	return WorkspaceCredentialPath(workspaceID, kind, bindingID) + ":delete"
}

func DefaultWorkspaceCredentialPath(workspaceID, kind, bindingID string) string {
	return WorkspaceCredentialPath(workspaceID, kind, bindingID) + ":setDefault"
}

type WorkspaceCredentialProviderSchema struct {
	Kind           string   `json:"kind"`
	DisplayName    string   `json:"displayName"`
	AuthTypes      []string `json:"authTypes"`
	AllowedHosts   []string `json:"allowedHosts"`
	AllowedHeaders []string `json:"allowedHeaders"`
	SecretFormat   string   `json:"secretFormat"`
}

type ListWorkspaceCredentialProviderSchemasResponse struct {
	Providers []WorkspaceCredentialProviderSchema `json:"providers"`
}

// WorkspaceCredentialMetadata never contains sealed_secret or a plaintext
// secret. Secret is accepted only by write requests below and is never echoed.
type WorkspaceCredentialMetadata struct {
	ID                string          `json:"id"`
	WorkspaceID       string          `json:"workspaceId"`
	Kind              string          `json:"kind"`
	DisplayName       string          `json:"displayName"`
	OwnerScope        string          `json:"ownerScope"`
	OwnerUserID       string          `json:"ownerUserId,omitempty"`
	PublicMetadata    json.RawMessage `json:"publicMetadata"`
	AuthType          string          `json:"authType"`
	AuthorityVersion  int64           `json:"authorityVersion"`
	CredentialVersion int64           `json:"credentialVersion"`
	Status            string          `json:"status"`
	IsDefault         bool            `json:"isDefault"`
	AccessExpiresAt   *time.Time      `json:"accessExpiresAt,omitempty"`
	RefreshExpiresAt  *time.Time      `json:"refreshExpiresAt,omitempty"`
}

type ListWorkspaceCredentialsResponse struct {
	Bindings []WorkspaceCredentialMetadata `json:"bindings"`
}

type CreateWorkspaceCredentialRequest struct {
	ID               string          `json:"id,omitempty"`
	DisplayName      string          `json:"displayName"`
	OwnerScope       string          `json:"ownerScope"`
	OwnerUserID      string          `json:"ownerUserId,omitempty"`
	AuthType         string          `json:"authType"`
	Secret           json.RawMessage `json:"secret"`
	PublicMetadata   json.RawMessage `json:"publicMetadata,omitempty"`
	AccessExpiresAt  *time.Time      `json:"accessExpiresAt,omitempty"`
	RefreshExpiresAt *time.Time      `json:"refreshExpiresAt,omitempty"`
	MakeDefault      bool            `json:"makeDefault"`
}

type CreateWorkspaceCredentialResponse struct {
	Binding WorkspaceCredentialMetadata `json:"binding"`
	Created bool                        `json:"created"`
}

type RotateWorkspaceCredentialRequest struct {
	ExpectedAuthorityVersion  int64           `json:"expectedAuthorityVersion"`
	ExpectedCredentialVersion int64           `json:"expectedCredentialVersion"`
	AuthType                  string          `json:"authType"`
	Secret                    json.RawMessage `json:"secret"`
	AccessExpiresAt           *time.Time      `json:"accessExpiresAt,omitempty"`
	RefreshExpiresAt          *time.Time      `json:"refreshExpiresAt,omitempty"`
}

type RotateWorkspaceCredentialResponse struct {
	Binding WorkspaceCredentialMetadata `json:"binding"`
	Changed bool                        `json:"changed"`
}

type RenameWorkspaceCredentialRequest struct {
	DisplayName              string `json:"displayName"`
	ExpectedAuthorityVersion int64  `json:"expectedAuthorityVersion"`
}

type RevokeWorkspaceCredentialRequest struct {
	ExpectedAuthorityVersion int64 `json:"expectedAuthorityVersion"`
}

type RenameWorkspaceCredentialResponse struct {
	Binding WorkspaceCredentialMetadata `json:"binding"`
	Changed bool                        `json:"changed"`
}

type RevokeWorkspaceCredentialResponse struct {
	Binding WorkspaceCredentialMetadata `json:"binding"`
	Changed bool                        `json:"changed"`
}

type DeleteWorkspaceCredentialRequest struct {
	ExpectedAuthorityVersion int64 `json:"expectedAuthorityVersion"`
}

type DeleteWorkspaceCredentialResponse struct {
	BindingID string `json:"bindingId"`
	Deleted   bool   `json:"deleted"`
}

type SetDefaultWorkspaceCredentialRequest struct {
	ExpectedAuthorityVersion int64 `json:"expectedAuthorityVersion"`
}

type SetDefaultWorkspaceCredentialResponse struct {
	Binding WorkspaceCredentialMetadata `json:"binding"`
	Changed bool                        `json:"changed"`
}
