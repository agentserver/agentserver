package corecontract

import "github.com/agentserver/agentserver/v2/internal/runevent"

const UserSessionJournalRoutePattern = "/v2/workspaces/{workspaceId}/sessions/{sessionId}/journal"

func UserSessionJournalPath(workspaceID, sessionID string) string {
	return UserSessionPath(workspaceID, sessionID) + "/journal"
}

// UserSessionJournalPage is an ordered source for replayable browser projections.
// RunID/AfterSeq identify committed Core events, not process-local DSH entries.
// A prompt occurs exactly once, before the first event of its run. Readers must
// drain HasMore before publishing an opening snapshot.
type UserSessionJournalPage struct {
	Session   UserSessionState              `json:"session"`
	RunID     string                        `json:"runId,omitempty"`
	RequestID string                        `json:"requestId,omitempty"`
	Prompt    *UserSessionTranscriptMessage `json:"prompt,omitempty"`
	Events    []runevent.Event              `json:"events"`
	AfterSeq  int64                         `json:"afterSeq"`
	HasMore   bool                          `json:"hasMore"`
}
