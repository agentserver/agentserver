package corecontract

import "time"

const (
	UserSessionCollectionRoutePattern     = "/v2/workspaces/{workspaceId}/sessions"
	UserSessionResourceRoutePattern       = "/v2/workspaces/{workspaceId}/sessions/{sessionId}"
	UserSessionPermissionModeRoutePattern = "/v2/workspaces/{workspaceId}/sessions/{sessionId}/permission-mode"
	UserSessionTranscriptRoutePattern     = "/v2/workspaces/{workspaceId}/sessions/{sessionId}/transcript"
	UserSessionArchiveRoutePattern        = "/v2/workspaces/{workspaceId}/sessions/{sessionId}/actions/archive"
)

func UserSessionsPath(workspaceID string) string {
	return "/v2/workspaces/" + workspaceID + "/sessions"
}

func UserSessionPath(workspaceID, sessionID string) string {
	return UserSessionsPath(workspaceID) + "/" + sessionID
}

func UserSessionTranscriptPath(workspaceID, sessionID string) string {
	return UserSessionPath(workspaceID, sessionID) + "/transcript"
}

func UserSessionPermissionModePath(workspaceID, sessionID string) string {
	return UserSessionPath(workspaceID, sessionID) + "/permission-mode"
}

func ArchiveUserSessionPath(workspaceID, sessionID string) string {
	return UserSessionPath(workspaceID, sessionID) + "/actions/archive"
}

type UserSessionState struct {
	SessionID             string    `json:"sessionId"`
	WorkspaceID           string    `json:"workspaceId"`
	Title                 string    `json:"title"`
	Status                string    `json:"status"`
	ActiveRunID           string    `json:"activeRunId,omitempty"`
	Version               int64     `json:"version"`
	PermissionMode        string    `json:"permissionMode"`
	PermissionModeVersion int64     `json:"permissionModeVersion"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
}

type ListUserSessionsResponse struct {
	Sessions []UserSessionState `json:"sessions"`
}

type UserSessionTranscriptMessage struct {
	MessageID string    `json:"messageId"`
	RunID     string    `json:"runId"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Complete  bool      `json:"complete"`
	CreatedAt time.Time `json:"createdAt"`
}

type GetUserSessionTranscriptResponse struct {
	WorkspaceID string                         `json:"workspaceId"`
	SessionID   string                         `json:"sessionId"`
	Messages    []UserSessionTranscriptMessage `json:"messages"`
	Truncated   bool                           `json:"truncated"`
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

// UpdateUserSessionPermissionModeRequest changes the mode used by the next
// run/turn. The independent version is a CAS token and is intentionally not
// the general session version, which also advances for run lifecycle events.
type UpdateUserSessionPermissionModeRequest struct {
	PermissionMode                string `json:"permissionMode"`
	ExpectedPermissionModeVersion int64  `json:"expectedPermissionModeVersion"`
}

type UpdateUserSessionPermissionModeResponse struct {
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
