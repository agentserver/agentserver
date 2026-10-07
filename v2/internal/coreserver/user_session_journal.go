package coreserver

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

type UserSessionJournalCommands interface {
	GetJournal(context.Context, string, string, string, string, int64) (corecontract.UserSessionJournalPage, error)
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
	page, err := commands.GetJournal(r.Context(), r.PathValue("workspaceId"), r.PathValue("sessionId"), actor, query.runID, query.after)
	if err != nil {
		handler.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

type journalQuery struct {
	runID string
	after int64
}

func parseJournalQuery(r *http.Request) (journalQuery, error) {
	var query journalQuery
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return query, errors.New("invalid journal query")
	}
	for key, value := range values {
		if len(value) != 1 || (key != "runId" && key != "after") {
			return query, errors.New("session journal accepts one runId and after")
		}
	}
	query.runID = values.Get("runId")
	if value, ok := values["after"]; ok {
		query.after, err = strconv.ParseInt(value[0], 10, 64)
	}
	if err != nil || query.after < 0 || (query.runID == "" && query.after != 0) {
		return query, errors.New("invalid journal cursor")
	}
	return query, nil
}

func (commands StateStoreUserSessionCommands) GetJournal(ctx context.Context, workspaceID, sessionID, actorID, runID string, after int64) (corecontract.UserSessionJournalPage, error) {
	if commands.Store == nil || commands.Prompts == nil {
		return corecontract.UserSessionJournalPage{}, errors.New("journal readers required")
	}
	source, err := commands.Store.ReadUserSessionJournal(ctx, workspaceID, sessionID, actorID, runID, after)
	if err != nil {
		return corecontract.UserSessionJournalPage{}, err
	}
	page := corecontract.UserSessionJournalPage{Session: contractUserSession(source.Session), RunID: source.Run.ID, AfterSeq: source.AfterSeq, HasMore: source.HasMore, Events: []runevent.Event{}}
	if source.IncludePrompt {
		page.RequestID = source.RequestID
		prompt, err := commands.Prompts.ReadUserPrompt(ctx, UserPromptReadRequest{WorkspaceID: workspaceID, Pointer: source.Run.Prompt})
		if err != nil {
			return page, err
		}
		page.Prompt = &corecontract.UserSessionTranscriptMessage{MessageID: "user-" + source.Run.ID, RunID: source.Run.ID, Role: "user", Content: prompt, Complete: true, CreatedAt: source.Run.CreatedAt}
	}
	for _, value := range source.Events {
		event, err := contractUserSessionTranscriptEvent(workspaceID, sessionID, value)
		if err != nil {
			return page, err
		}
		page.Events = append(page.Events, event)
	}
	return page, nil
}
