package corecontract

import (
	"encoding/json"
	"time"

	"github.com/agentserver/agentserver/v2/internal/runevent"
)

const PublicRunPathPrefix = "/v2/workspaces/"

type CreateUserRunRequest struct {
	ClientRunID                   string `json:"clientRunId,omitempty"`
	Prompt                        string `json:"prompt"`
	ExpectedPermissionModeVersion int64  `json:"expectedPermissionModeVersion,omitempty"`
	// ExpectedWorkingDirectoryVersion is an optional CAS token for the
	// session's next-run executor workspace binding. It is a precondition for
	// creating a new run, not part of the idempotency identity.
	ExpectedWorkingDirectoryVersion int64 `json:"expectedWorkingDirectoryVersion,omitempty"`
}

type CreateUserRunResponse struct {
	WorkspaceID       string    `json:"workspaceId"`
	SessionID         string    `json:"sessionId"`
	RunID             string    `json:"runId"`
	CreatedAt         time.Time `json:"createdAt"`
	Cursor            string    `json:"cursor"`
	LastEventSequence int64     `json:"lastEventSequence"`
	Created           bool      `json:"created"`
}

// CancelUserRunResponse reports the authoritative run state after an explicit
// user cancel command. Terminal is true only after Core has made the session
// available for another run; a cancelling response means the live holder must
// still interrupt and fence its attempt.
type CancelUserRunResponse struct {
	WorkspaceID string `json:"workspaceId"`
	SessionID   string `json:"sessionId"`
	RunID       string `json:"runId"`
	Status      string `json:"status"`
	RunVersion  int64  `json:"runVersion"`
	Terminal    bool   `json:"terminal"`
	Changed     bool   `json:"changed"`
}

type ReadUserRunEventsResponse struct {
	Events            []runevent.Event `json:"events"`
	EventCursors      []string         `json:"eventCursors"`
	NextCursor        string           `json:"nextCursor"`
	LastEventSequence int64            `json:"lastEventSequence"`
}

type UserRunSnapshot struct {
	WorkspaceID       string          `json:"workspaceId"`
	SessionID         string          `json:"sessionId"`
	RunID             string          `json:"runId"`
	Status            string          `json:"status"`
	RunVersion        int64           `json:"runVersion"`
	LastEventSequence int64           `json:"lastEventSequence"`
	State             json.RawMessage `json:"state"`
	UpdatedAt         time.Time       `json:"updatedAt"`
}

type UserRunCursorExpiredResponse struct {
	Code              string          `json:"code"`
	Message           string          `json:"message"`
	Snapshot          UserRunSnapshot `json:"snapshot"`
	RebaseCursor      string          `json:"rebaseCursor"`
	LastEventSequence int64           `json:"lastEventSequence"`
}

type PublicErrorResponse struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	CurrentRunID string `json:"currentRunId,omitempty"`
}

func CreateUserRunPath(workspaceID, sessionID string) string {
	return PublicRunPathPrefix + workspaceID + "/sessions/" + sessionID + "/runs"
}

func ReadUserRunEventsPath(workspaceID, runID string) string {
	return PublicRunPathPrefix + workspaceID + "/runs/" + runID + "/events"
}

func CancelUserRunPath(workspaceID, runID string) string {
	return PublicRunPathPrefix + workspaceID + "/runs/" + runID + ":cancel"
}
