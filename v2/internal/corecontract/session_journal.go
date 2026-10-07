package corecontract

import (
	"github.com/agentserver/agentserver/v2/internal/runevent"
	"time"
)

const UserSessionJournalRoutePattern = "/v2/workspaces/{workspaceId}/sessions/{sessionId}/journal"

func UserSessionJournalPath(workspaceID, sessionID string) string {
	return UserSessionPath(workspaceID, sessionID) + "/journal"
}

// UserSessionJournalPage is an ordered source for replayable browser projections.
// Cursor addresses a commit-ordered, session-wide Core journal, not DSH event
// positions. Readers must drain HasMore before publishing an opening snapshot.
type UserSessionJournalPage struct {
	Session UserSessionState          `json:"session"`
	Entries []UserSessionJournalEntry `json:"entries"`
	Cursor  int64                     `json:"cursor"`
	HasMore bool                      `json:"hasMore"`
}

type UserSessionJournalEntry struct {
	Seq               int64                         `json:"seq"`
	Kind              string                        `json:"kind"`
	CreatedAt         time.Time                     `json:"createdAt"`
	RequestID         string                        `json:"requestId,omitempty"`
	Prompt            *UserSessionTranscriptMessage `json:"prompt,omitempty"`
	Event             *runevent.Event               `json:"event,omitempty"`
	PermissionMode    string                        `json:"permissionMode,omitempty"`
	PermissionVersion int64                         `json:"permissionVersion,omitempty"`
	Title             string                        `json:"title,omitempty"`
	TitleSource       string                        `json:"titleSource,omitempty"`
	TitleVersion      int64                         `json:"titleVersion,omitempty"`
}
