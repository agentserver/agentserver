package corecontract

import "time"

const (
	UserSessionCollectionRoutePattern = "/v2/workspaces/{workspaceId}/sessions"
	UserSessionResourceRoutePattern   = "/v2/workspaces/{workspaceId}/sessions/{sessionId}"
	UserSessionArchiveRoutePattern    = "/v2/workspaces/{workspaceId}/sessions/{sessionId}/actions/archive"
)

func UserSessionsPath(workspaceID string) string {
	return "/v2/workspaces/" + workspaceID + "/sessions"
}

func UserSessionPath(workspaceID, sessionID string) string {
	return UserSessionsPath(workspaceID) + "/" + sessionID
}

func ArchiveUserSessionPath(workspaceID, sessionID string) string {
	return UserSessionPath(workspaceID, sessionID) + "/actions/archive"
}

type UserSessionState struct {
	SessionID   string    `json:"sessionId"`
	WorkspaceID string    `json:"workspaceId"`
	Title       string    `json:"title"`
	Status      string    `json:"status"`
	ActiveRunID string    `json:"activeRunId,omitempty"`
	Version     int64     `json:"version"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type ListUserSessionsResponse struct {
	Sessions []UserSessionState `json:"sessions"`
}

type CreateUserSessionRequest struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
}

type CreateUserSessionResponse struct {
	Session UserSessionState `json:"session"`
	Created bool             `json:"created"`
}

type UpdateUserSessionRequest struct {
	Title           string `json:"title"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

type UpdateUserSessionResponse struct {
	Session UserSessionState `json:"session"`
	Changed bool             `json:"changed"`
}

type ArchiveUserSessionRequest struct {
	ExpectedVersion int64 `json:"expectedVersion"`
}

type ArchiveUserSessionResponse struct {
	Session UserSessionState `json:"session"`
	Changed bool             `json:"changed"`
}
