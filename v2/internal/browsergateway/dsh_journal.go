package browsergateway

import (
	"context"
	"errors"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

// Live and restored streams use the same committed source on every replica.
func (gateway *DSHGateway) refreshJournal(ctx context.Context, bearer string, state *dshSessionState) error {
	state.journalMu.Lock()
	defer state.journalMu.Unlock()
	state.mu.Lock()
	sessionID := state.session.SessionID
	previousActive := state.session.ActiveRunID
	state.mu.Unlock()
	if state.journalApprovals == nil {
		state.journalApprovals = map[string]runevent.Event{}
	}
	for {
		page, err := gateway.backend.GetJournal(ctx, bearer, gateway.config.WorkspaceID, sessionID, state.journalCursor)
		if err != nil {
			return err
		}
		if err = validateDSHJournalPage(page, gateway.config.WorkspaceID, sessionID, state.journalCursor); err != nil {
			return err
		}
		runID, runSeq, err := validateJournalRunContinuity(page, state.journalRunID, state.journalRunSeq)
		if err != nil {
			return err
		}
		for _, entry := range page.Entries {
			if entry.Kind == "title" {
				state.appendJournalTitle(entry)
				continue
			}
			if entry.Kind == "prompt" {
				state.appendJournalPrompt(*entry.Prompt, entry.RequestID)
				continue
			}
			if entry.Kind == "permission" {
				state.appendJournalPermission(entry)
				continue
			}
			event := *entry.Event
			state.mapCanonical(event)
			if event.Kind == runevent.KindApprovalRequested {
				payload, _ := runevent.DecodeSemanticPayload(event)
				state.journalApprovals[payload.(runevent.ApprovalPayload).ApprovalID] = event
			}
			if event.Kind == runevent.KindApprovalApproved || event.Kind == runevent.KindApprovalDenied || event.Kind == runevent.KindApprovalExpired || event.Kind == runevent.KindApprovalCancelled || event.Kind == runevent.KindApprovalConsumed {
				payload, _ := runevent.DecodeSemanticPayload(event)
				delete(state.journalApprovals, payload.(runevent.ApprovalPayload).ApprovalID)
				gateway.cancelApproval(event)
			}
		}
		state.journalCursor = page.Cursor
		state.journalRunID, state.journalRunSeq = runID, runSeq
		state.mu.Lock()
		state.session = page.Session
		state.mu.Unlock()
		if !page.HasMore {
			for id, event := range state.journalApprovals {
				if event.RunID == page.Session.ActiveRunID {
					gateway.publishApproval(event, bearer)
				} else {
					delete(state.journalApprovals, id)
				}
			}
			if previousActive != page.Session.ActiveRunID {
				gateway.emitRemoteEvent("api-session/status", sessionID, page.Session.ActiveRunID != "")
			}
			return nil
		}
	}
}

// The session cursor and the canonical run cursor are distinct. Enforce both,
// including across page boundaries and permission entries between model items.
func validateJournalRunContinuity(page corecontract.UserSessionJournalPage, runID string, runSeq int64) (string, int64, error) {
	for _, entry := range page.Entries {
		switch entry.Kind {
		case "prompt":
			if entry.Prompt.RunID == runID {
				return "", 0, errors.New("journal repeated committed prompt")
			}
			runID, runSeq = entry.Prompt.RunID, 0
		case "run_event":
			if entry.Event.RunID != runID || entry.Event.Seq != runSeq+1 {
				return "", 0, errors.New("journal canonical run continuity mismatch")
			}
			runSeq = entry.Event.Seq
		}
	}
	return runID, runSeq, nil
}

func validateDSHJournalPage(page corecontract.UserSessionJournalPage, workspaceID, sessionID string, after int64) error {
	if page.Session.SessionID != sessionID || page.Session.WorkspaceID != workspaceID {
		return errors.New("journal escaped requested session")
	}
	seq := after
	for _, entry := range page.Entries {
		if entry.Seq != seq+1 {
			return errors.New("journal entry continuity mismatch")
		}
		switch entry.Kind {
		case "title":
			if entry.Event != nil || entry.Prompt != nil || entry.TitleVersion < 1 || entry.Title == "" || len(entry.Title) > 256 {
				return errors.New("invalid journal title")
			}
			if entry.TitleSource != "placeholder" && entry.TitleSource != "fallback" && entry.TitleSource != "manual" && entry.TitleSource != "generated" {
				return errors.New("invalid journal title source")
			}
		case "prompt":
			if entry.Prompt == nil || entry.Event != nil || entry.Prompt.RunID == "" || !entry.Prompt.Complete || entry.Prompt.Role != "user" {
				return errors.New("invalid journal prompt")
			}
		case "permission":
			if entry.Event != nil || entry.Prompt != nil || entry.PermissionVersion < 1 || (entry.PermissionMode != "read-only" && entry.PermissionMode != "auto" && entry.PermissionMode != "full-access") {
				return errors.New("invalid journal permission")
			}
		case "run_event":
			if entry.Event == nil || entry.Prompt != nil {
				return errors.New("invalid journal event")
			}
			event := *entry.Event
			if event.WorkspaceID != workspaceID || event.SessionID != sessionID {
				return errors.New("journal event escaped requested session")
			}
			if err := event.Validate(); err != nil {
				return err
			}
			if runevent.IsKnownKind(event.Kind) {
				if _, err := runevent.DecodeSemanticPayload(event); err != nil {
					return err
				}
			}
		default:
			return errors.New("unknown journal entry kind")
		}
		seq = entry.Seq
	}
	if page.Cursor != seq || (page.HasMore && len(page.Entries) == 0) {
		return errors.New("journal cursor did not advance")
	}
	return nil
}
