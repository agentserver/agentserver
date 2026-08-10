package corecontract

import "time"

const (
	WorkspaceCollectionRoutePattern     = "/v2/workspaces"
	WorkspaceResourceRoutePattern       = "/v2/workspaces/{workspaceId}"
	WorkspaceArchiveRoutePattern        = "/v2/workspaces/{workspaceId}/actions/archive"
	WorkspaceMembersCollectionPattern   = "/v2/workspaces/{workspaceId}/members"
	WorkspaceMemberResourceRoutePattern = "/v2/workspaces/{workspaceId}/members/{memberId}"
)

func WorkspacesPath() string { return "/v2/workspaces" }

func WorkspacePath(workspaceID string) string {
	return "/v2/workspaces/" + workspaceID
}

func ArchiveWorkspacePath(workspaceID string) string {
	return WorkspacePath(workspaceID) + "/actions/archive"
}

func WorkspaceMembersPath(workspaceID string) string {
	return WorkspacePath(workspaceID) + "/members"
}

func WorkspaceMemberPath(workspaceID, userID string) string {
	return WorkspaceMembersPath(workspaceID) + "/" + userID
}

type WorkspaceState struct {
	WorkspaceID               string    `json:"workspaceId"`
	Name                      string    `json:"name"`
	Status                    string    `json:"status"`
	CurrentUserRole           string    `json:"currentUserRole"`
	ManagedLarkCredentialMode string    `json:"managedLarkCredentialMode"`
	Version                   int64     `json:"version"`
	CreatedAt                 time.Time `json:"createdAt"`
	UpdatedAt                 time.Time `json:"updatedAt"`
}

type ListWorkspacesResponse struct {
	Workspaces []WorkspaceState `json:"workspaces"`
}

type CreateWorkspaceRequest struct {
	WorkspaceID               string `json:"workspaceId"`
	Name                      string `json:"name"`
	ManagedLarkCredentialMode string `json:"managedLarkCredentialMode"`
}

type CreateWorkspaceResponse struct {
	Workspace WorkspaceState `json:"workspace"`
	Created   bool           `json:"created"`
}

type UpdateWorkspaceRequest struct {
	Name                      string `json:"name"`
	ManagedLarkCredentialMode string `json:"managedLarkCredentialMode"`
	ExpectedVersion           int64  `json:"expectedVersion"`
}

type UpdateWorkspaceResponse struct {
	Workspace WorkspaceState `json:"workspace"`
	Changed   bool           `json:"changed"`
}

type ArchiveWorkspaceRequest struct {
	ExpectedVersion int64 `json:"expectedVersion"`
}

type ArchiveWorkspaceResponse struct {
	Workspace WorkspaceState `json:"workspace"`
	Changed   bool           `json:"changed"`
}

type WorkspaceMemberState struct {
	UserID    string    `json:"userId"`
	Role      string    `json:"role"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ListWorkspaceMembersResponse struct {
	Members []WorkspaceMemberState `json:"members"`
}

type AddWorkspaceMemberRequest struct {
	UserID string `json:"userId"`
	Role   string `json:"role"`
}

type AddWorkspaceMemberResponse struct {
	Member  WorkspaceMemberState `json:"member"`
	Created bool                 `json:"created"`
}

type UpdateWorkspaceMemberRequest struct {
	Role            string `json:"role"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

type UpdateWorkspaceMemberResponse struct {
	Member  WorkspaceMemberState `json:"member"`
	Changed bool                 `json:"changed"`
}

type RemoveWorkspaceMemberResponse struct {
	UserID  string `json:"userId"`
	Removed bool   `json:"removed"`
}
