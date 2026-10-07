package coreserver

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

type UserSessionJournalCommands interface {
	GetJournal(context.Context, string, string, string, int64) (corecontract.UserSessionJournalPage, error)
}

func (handler *UserSessionHandler) journal(w http.ResponseWriter, r *http.Request) {
	userSessionNoStore(w)
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writePublicRunError(w, http.StatusMethodNotAllowed, "method_not_allowed", "session journal requires GET", "")
		return
	}
	query, err := parseJournalQuery(r)
	if err != nil {
		writePublicRunError(w, http.StatusBadRequest, "invalid_argument", err.Error(), "")
		return
	}
	// Same combined session/run read authority as transcript; this is a
	// paginated projection source, not an internal-auth-only user API.
	actor, ok := handler.authorize(w, r, "sessions.transcript")
	if !ok || !requireEmptyUserSessionBody(w, r, "session journal") {
		return
	}
	commands, ok := handler.commands.(UserSessionJournalCommands)
	if !ok {
		writePublicRunError(w, http.StatusServiceUnavailable, "unavailable", "session journal is unavailable", "")
		return
	}
	page, err := commands.GetJournal(r.Context(), r.PathValue("workspaceId"), r.PathValue("sessionId"), actor, query.cursor)
	if err != nil {
		handler.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

type journalQuery struct {
	cursor int64
}

func parseJournalQuery(r *http.Request) (journalQuery, error) {
	var query journalQuery
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return query, errors.New("invalid journal query")
	}
	for key, value := range values {
		if len(value) != 1 || key != "cursor" {
			return query, errors.New("session journal accepts one cursor")
		}
	}
	if value, ok := values["cursor"]; ok {
		query.cursor, err = strconv.ParseInt(value[0], 10, 64)
	}
	if err != nil || query.cursor < 0 || query.cursor >= 9007199254740991 {
		return query, errors.New("invalid journal cursor")
	}
	return query, nil
}

func (commands StateStoreUserSessionCommands) GetJournal(ctx context.Context, workspaceID, sessionID, actorID string, cursor int64) (corecontract.UserSessionJournalPage, error) {
	if commands.Store == nil || commands.Prompts == nil {
		return corecontract.UserSessionJournalPage{}, errors.New("journal readers required")
	}
	source, err := commands.Store.ReadUserSessionJournal(ctx, workspaceID, sessionID, actorID, cursor)
	if err != nil {
		return corecontract.UserSessionJournalPage{}, err
	}
	page := corecontract.UserSessionJournalPage{Session: contractUserSession(source.Session), Cursor: source.Cursor, HasMore: source.HasMore, Entries: []corecontract.UserSessionJournalEntry{}}
	for _, value := range source.Entries {
		entry := corecontract.UserSessionJournalEntry{Seq: value.Seq, Kind: value.Kind, CreatedAt: value.CreatedAt, PermissionMode: value.PermissionMode, PermissionVersion: value.PermissionVersion, RequestID: value.RequestID, Title: value.Title, TitleSource: value.TitleSource, TitleVersion: value.TitleVersion}
		switch value.Kind {
		case "prompt":
			prompt, err := commands.Prompts.ReadUserPrompt(ctx, UserPromptReadRequest{WorkspaceID: workspaceID, Pointer: value.Run.Prompt})
			if err != nil {
				return page, err
			}
			entry.Prompt = &corecontract.UserSessionTranscriptMessage{MessageID: "user-" + value.Run.ID, RunID: value.Run.ID, Role: "user", Content: prompt, Complete: true, CreatedAt: value.Run.CreatedAt}
		case "run_event":
			event, err := contractUserSessionTranscriptEvent(workspaceID, sessionID, value.Event)
			if err != nil {
				return page, err
			}
			entry.Event = &event
		}
		page.Entries = append(page.Entries, entry)
	}
	return page, nil
}
