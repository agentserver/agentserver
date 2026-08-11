package corecontract

import (
	"encoding/json"
	"net/url"
	"time"
)

const (
	WorkspaceCredentialProviderSchemasPath                 = "/v2/credential-providers"
	WorkspaceCredentialCollectionRoutePattern              = "/v2/workspaces/{workspaceId}/credentials/{kind}"
	WorkspaceCredentialResourceRoutePattern                = "/v2/workspaces/{workspaceId}/credentials/{kind}/{bindingId}"
	WorkspaceCredentialAuthorizationCollectionRoutePattern = "/v2/workspaces/{workspaceId}/credential-authorizations/{kind}"
	WorkspaceCredentialAuthorizationResourceRoutePattern   = "/v2/workspaces/{workspaceId}/credential-authorizations/{kind}/{authorizationId}"
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

func WorkspaceCredentialAuthorizationCollectionPath(workspaceID, kind string) string {
	return "/v2/workspaces/" + url.PathEscape(workspaceID) + "/credential-authorizations/" + url.PathEscape(kind)
}

func WorkspaceCredentialAuthorizationPath(workspaceID, kind, authorizationID string) string {
	return WorkspaceCredentialAuthorizationCollectionPath(workspaceID, kind) + "/" + url.PathEscape(authorizationID)
}

func PollWorkspaceCredentialAuthorizationPath(workspaceID, kind, authorizationID string) string {
	return WorkspaceCredentialAuthorizationPath(workspaceID, kind, authorizationID) + ":poll"
}

func CancelWorkspaceCredentialAuthorizationPath(workspaceID, kind, authorizationID string) string {
	return WorkspaceCredentialAuthorizationPath(workspaceID, kind, authorizationID) + ":cancel"
}

type WorkspaceCredentialProviderSchema struct {
	Kind                 string   `json:"kind"`
	DisplayName          string   `json:"displayName"`
	AuthTypes            []string `json:"authTypes"`
	AllowedHosts         []string `json:"allowedHosts"`
	AllowedHeaders       []string `json:"allowedHeaders"`
	SecretFormat         string   `json:"secretFormat"`
	AuthorizationMethods []string `json:"authorizationMethods"`
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

type BeginWorkspaceCredentialAuthorizationRequest struct {
	DisplayName               string          `json:"displayName"`
	OwnerScope                string          `json:"ownerScope"`
	OwnerUserID               string          `json:"ownerUserId,omitempty"`
	MakeDefault               bool            `json:"makeDefault"`
	BindingID                 string          `json:"bindingId,omitempty"`
	ExpectedAuthorityVersion  int64           `json:"expectedAuthorityVersion,omitempty"`
	ExpectedCredentialVersion int64           `json:"expectedCredentialVersion,omitempty"`
	ProviderParameters        json.RawMessage `json:"providerParameters,omitempty"`
}

type WorkspaceCredentialAuthorization struct {
	ID                      string                       `json:"id"`
	WorkspaceID             string                       `json:"workspaceId"`
	Kind                    string                       `json:"kind"`
	TargetBindingID         string                       `json:"targetBindingId"`
	Status                  string                       `json:"status"`
	UserCode                string                       `json:"userCode"`
	VerificationURI         string                       `json:"verificationUri"`
	VerificationURIComplete string                       `json:"verificationUriComplete"`
	PollIntervalSeconds     int                          `json:"pollIntervalSeconds"`
	NextPollAt              time.Time                    `json:"nextPollAt"`
	ExpiresAt               time.Time                    `json:"expiresAt"`
	LastErrorCode           string                       `json:"lastErrorCode,omitempty"`
	Version                 int64                        `json:"version"`
	Binding                 *WorkspaceCredentialMetadata `json:"binding,omitempty"`
}

type BeginWorkspaceCredentialAuthorizationResponse struct {
	Authorization WorkspaceCredentialAuthorization `json:"authorization"`
}

type GetWorkspaceCredentialAuthorizationResponse struct {
	Authorization WorkspaceCredentialAuthorization `json:"authorization"`
}

type PollWorkspaceCredentialAuthorizationResponse struct {
	Authorization WorkspaceCredentialAuthorization `json:"authorization"`
}

type CancelWorkspaceCredentialAuthorizationRequest struct {
	ExpectedVersion int64 `json:"expectedVersion"`
}

type CancelWorkspaceCredentialAuthorizationResponse struct {
	Authorization WorkspaceCredentialAuthorization `json:"authorization"`
	Changed       bool                             `json:"changed"`
}
