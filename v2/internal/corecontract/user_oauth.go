package corecontract

import "sort"

const (
	OAuthOpenIDScope          = "openid"
	UserOAuthAuthorityVersion = 1

	UserOAuthPlatformAuthority  = "platform"
	UserOAuthBrowserAuthority   = "browser"
	UserOAuthWorkspaceURNPrefix = "urn:agentserver:workspace:"

	PlatformOAuthClientID = "agentserver-platform"
	PlatformOAuthAudience = "agentserver-platform-api"

	PlatformOAuthWorkspacesReadScope     = "workspaces:read"
	PlatformOAuthWorkspacesCreateScope   = "workspaces:create"
	PlatformOAuthWorkspacesUpdateScope   = "workspaces:update"
	PlatformOAuthWorkspacesArchiveScope  = "workspaces:archive"
	PlatformOAuthMembersReadScope        = "members:read"
	PlatformOAuthMembersAddScope         = "members:add"
	PlatformOAuthMembersUpdateScope      = "members:update"
	PlatformOAuthMembersRemoveScope      = "members:remove"
	PlatformOAuthExecutorsReadScope      = "executors:read"
	PlatformOAuthExecutorsCreateScope    = "executors:create"
	PlatformOAuthExecutorsEnrollScope    = "executors:enroll"
	PlatformOAuthExecutorsUpdateScope    = "executors:update"
	PlatformOAuthExecutorsArchiveScope   = "executors:archive"
	PlatformOAuthLLMGatewaysReadScope    = "llm-gateways:read"
	PlatformOAuthLLMGatewaysCreateScope  = "llm-gateways:create"
	PlatformOAuthLLMGatewaysUpdateScope  = "llm-gateways:update"
	PlatformOAuthLLMGatewaysDisableScope = "llm-gateways:disable"
	PlatformOAuthLLMGrantsAuthorizeScope = "llm-gateway-grants:authorize"
	PlatformOAuthLLMGrantsRevokeScope    = "llm-gateway-grants:revoke"
	PlatformOAuthCredentialsReadScope    = "credentials:read"
	PlatformOAuthCredentialsManageScope  = "credentials:manage"

	BrowserOAuthClientID = "agentserver-browser"
	BrowserOAuthAudience = "agentserver-browser-api"

	BrowserOAuthSessionsReadScope    = "sessions:read"
	BrowserOAuthSessionsCreateScope  = "sessions:create"
	BrowserOAuthSessionsUpdateScope  = "sessions:update"
	BrowserOAuthSessionsArchiveScope = "sessions:archive"
	BrowserOAuthRunsReadScope        = "runs:read"
	BrowserOAuthRunsCreateScope      = "runs:create"
	BrowserOAuthRunsCancelScope      = "runs:cancel"
	BrowserOAuthApprovalsDecideScope = "approvals:decide"
)

type UserOAuthResource string

const (
	UserOAuthGlobalResource    UserOAuthResource = "global"
	UserOAuthWorkspaceResource UserOAuthResource = "workspace"
)

type UserOAuthActionAuthority struct {
	Resource    UserOAuthResource
	Permissions []string
}

type UserOAuthWorkspaceGrant struct {
	WorkspaceID string   `json:"workspace_id"`
	Generation  int64    `json:"generation"`
	Permissions []string `json:"permissions"`
}

type UserOAuthAuthority struct {
	Version           int                       `json:"version"`
	Authority         string                    `json:"authority"`
	GlobalPermissions []string                  `json:"global_permissions"`
	WorkspaceGrants   []UserOAuthWorkspaceGrant `json:"workspace_grants"`
}

// PlatformOAuthScopes returns the complete, canonical maximum scope registry
// for the platform client. A concrete token is granted only the subset resolved
// by the Hydra consent policy.
func PlatformOAuthScopes() []string {
	return []string{
		OAuthOpenIDScope,
		PlatformOAuthWorkspacesReadScope,
		PlatformOAuthWorkspacesCreateScope,
		PlatformOAuthWorkspacesUpdateScope,
		PlatformOAuthWorkspacesArchiveScope,
		PlatformOAuthMembersReadScope,
		PlatformOAuthMembersAddScope,
		PlatformOAuthMembersUpdateScope,
		PlatformOAuthMembersRemoveScope,
		PlatformOAuthExecutorsReadScope,
		PlatformOAuthExecutorsCreateScope,
		PlatformOAuthExecutorsEnrollScope,
		PlatformOAuthExecutorsUpdateScope,
		PlatformOAuthExecutorsArchiveScope,
		PlatformOAuthLLMGatewaysReadScope,
		PlatformOAuthLLMGatewaysCreateScope,
		PlatformOAuthLLMGatewaysUpdateScope,
		PlatformOAuthLLMGatewaysDisableScope,
		PlatformOAuthLLMGrantsAuthorizeScope,
		PlatformOAuthLLMGrantsRevokeScope,
		PlatformOAuthCredentialsReadScope,
		PlatformOAuthCredentialsManageScope,
	}
}

// PlatformOAuthActionPermissions is the closed action-to-permission registry
// used by user-facing Core handlers. New Platform actions must be added here
// before an endpoint can authorize them.
func PlatformOAuthActionPermissions() map[string]UserOAuthActionAuthority {
	return map[string]UserOAuthActionAuthority{
		"workspaces.list":                     {Resource: UserOAuthGlobalResource, Permissions: []string{PlatformOAuthWorkspacesReadScope}},
		"workspaces.create":                   {Resource: UserOAuthGlobalResource, Permissions: []string{PlatformOAuthWorkspacesCreateScope}},
		"workspaces.get":                      {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthWorkspacesReadScope}},
		"workspaces.update":                   {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthWorkspacesUpdateScope}},
		"workspaces.archive":                  {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthWorkspacesArchiveScope}},
		"members.list":                        {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthMembersReadScope}},
		"members.add":                         {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthMembersAddScope}},
		"members.update":                      {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthMembersUpdateScope}},
		"members.remove":                      {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthMembersRemoveScope}},
		"executors.list":                      {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthExecutorsReadScope}},
		"executors.create":                    {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthExecutorsCreateScope}},
		"executors.enrollment-token.issue":    {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthExecutorsEnrollScope}},
		"executors.update":                    {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthExecutorsUpdateScope}},
		"executors.archive":                   {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthExecutorsArchiveScope}},
		"llm-gateways.list":                   {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthLLMGatewaysReadScope}},
		"llm-gateways.create":                 {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthLLMGatewaysCreateScope}},
		"llm-gateways.update":                 {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthLLMGatewaysUpdateScope}},
		"llm-gateways.disable":                {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthLLMGatewaysDisableScope}},
		"llm-gateways.authorize":              {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthLLMGrantsAuthorizeScope}},
		"llm-gateways.complete-authorization": {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthLLMGrantsAuthorizeScope}},
		"llm-gateways.revoke":                 {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthLLMGrantsRevokeScope}},
		"credentials.schemas":                 {Resource: UserOAuthGlobalResource, Permissions: []string{PlatformOAuthCredentialsReadScope}},
		"credentials.list":                    {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthCredentialsReadScope}},
		"credentials.create":                  {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthCredentialsManageScope}},
		"credentials.update":                  {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthCredentialsManageScope}},
		"credentials.rotate":                  {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthCredentialsManageScope}},
		"credentials.revoke":                  {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthCredentialsManageScope}},
		"credentials.delete":                  {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthCredentialsManageScope}},
		"credentials.set-default":             {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthCredentialsManageScope}},
		"credentials.authorizations.begin":    {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthCredentialsManageScope}},
		"credentials.authorizations.get":      {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthCredentialsReadScope}},
		"credentials.authorizations.poll":     {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthCredentialsManageScope}},
		"credentials.authorizations.cancel":   {Resource: UserOAuthWorkspaceResource, Permissions: []string{PlatformOAuthCredentialsManageScope}},
	}
}

// BrowserOAuthActionPermissions is the closed action-to-permission registry
// for one workspace-bound conversation token.
func BrowserOAuthActionPermissions() map[string]UserOAuthActionAuthority {
	return map[string]UserOAuthActionAuthority{
		"sessions.list":       {Resource: UserOAuthWorkspaceResource, Permissions: []string{BrowserOAuthSessionsReadScope}},
		"sessions.create":     {Resource: UserOAuthWorkspaceResource, Permissions: []string{BrowserOAuthSessionsCreateScope}},
		"sessions.get":        {Resource: UserOAuthWorkspaceResource, Permissions: []string{BrowserOAuthSessionsReadScope}},
		"sessions.update":     {Resource: UserOAuthWorkspaceResource, Permissions: []string{BrowserOAuthSessionsUpdateScope}},
		"sessions.archive":    {Resource: UserOAuthWorkspaceResource, Permissions: []string{BrowserOAuthSessionsArchiveScope}},
		"sessions.transcript": {Resource: UserOAuthWorkspaceResource, Permissions: []string{BrowserOAuthSessionsReadScope, BrowserOAuthRunsReadScope}},
		"runs.read":           {Resource: UserOAuthWorkspaceResource, Permissions: []string{BrowserOAuthRunsReadScope}},
		"runs.create":         {Resource: UserOAuthWorkspaceResource, Permissions: []string{BrowserOAuthRunsCreateScope}},
		"runs.cancel":         {Resource: UserOAuthWorkspaceResource, Permissions: []string{BrowserOAuthRunsCancelScope}},
		"runs.events.read":    {Resource: UserOAuthWorkspaceResource, Permissions: []string{BrowserOAuthRunsReadScope}},
		"approvals.decide":    {Resource: UserOAuthWorkspaceResource, Permissions: []string{BrowserOAuthApprovalsDecideScope}},
	}
}

// BrowserOAuthScopes returns the complete, canonical maximum scope registry
// for the workspace-bound conversation client.
func BrowserOAuthScopes() []string {
	return []string{
		OAuthOpenIDScope,
		BrowserOAuthSessionsReadScope,
		BrowserOAuthSessionsCreateScope,
		BrowserOAuthSessionsUpdateScope,
		BrowserOAuthSessionsArchiveScope,
		BrowserOAuthRunsReadScope,
		BrowserOAuthRunsCreateScope,
		BrowserOAuthRunsCancelScope,
		BrowserOAuthApprovalsDecideScope,
	}
}

// PlatformOAuthGlobalPermissions returns the permissions every active user may
// receive independently of a workspace membership. The consent policy still
// intersects this registry with the scopes requested by the OAuth client.
func PlatformOAuthGlobalPermissions() []string {
	return []string{
		PlatformOAuthWorkspacesCreateScope,
		PlatformOAuthWorkspacesReadScope,
		PlatformOAuthCredentialsReadScope,
	}
}

// PlatformOAuthWorkspacePermissions expands one persisted workspace role into
// the canonical permission set that may be embedded in a Platform token. Roles
// are deliberately absent from the token itself.
func PlatformOAuthWorkspacePermissions(role string) ([]string, bool) {
	var permissions []string
	switch role {
	case "owner":
		permissions = permissionsForResource(PlatformOAuthActionPermissions(), UserOAuthWorkspaceResource)
	case "developer":
		permissions = []string{
			PlatformOAuthExecutorsReadScope,
			PlatformOAuthLLMGatewaysReadScope,
			PlatformOAuthLLMGrantsAuthorizeScope,
			PlatformOAuthLLMGrantsRevokeScope,
			PlatformOAuthMembersReadScope,
			PlatformOAuthWorkspacesReadScope,
		}
	case "viewer":
		permissions = []string{PlatformOAuthWorkspacesReadScope}
	default:
		return nil, false
	}
	sort.Strings(permissions)
	return permissions, true
}

// BrowserOAuthWorkspacePermissions expands one persisted workspace role into
// the canonical permission set that may be embedded in a workspace-bound
// Browser token.
func BrowserOAuthWorkspacePermissions(role string) ([]string, bool) {
	var permissions []string
	switch role {
	case "owner", "developer":
		permissions = permissionsForResource(BrowserOAuthActionPermissions(), UserOAuthWorkspaceResource)
	case "viewer":
		permissions = []string{
			BrowserOAuthRunsReadScope,
			BrowserOAuthSessionsReadScope,
		}
	default:
		return nil, false
	}
	sort.Strings(permissions)
	return permissions, true
}

func permissionsForResource(actions map[string]UserOAuthActionAuthority, resource UserOAuthResource) []string {
	set := make(map[string]struct{})
	for _, authority := range actions {
		if authority.Resource != resource {
			continue
		}
		for _, permission := range authority.Permissions {
			set[permission] = struct{}{}
		}
	}
	permissions := make([]string, 0, len(set))
	for permission := range set {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)
	return permissions
}
