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
		page, err := gateway.backend.GetJournal(ctx, bearer, gateway.config.WorkspaceID, sessionID, state.journalRunID, state.journalSeq)
		if err != nil {
			return err
		}
		if err = validateDSHJournalPage(page, gateway.config.WorkspaceID, sessionID, state.journalRunID, state.journalSeq); err != nil {
			return err
		}
		if page.Prompt != nil {
			state.appendJournalPrompt(*page.Prompt, page.RequestID)
		}
		for _, event := range page.Events {
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
		state.journalRunID, state.journalSeq = page.RunID, page.AfterSeq
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

func validateDSHJournalPage(page corecontract.UserSessionJournalPage, workspaceID, sessionID, runID string, after int64) error {
	if page.Session.SessionID != sessionID || page.Session.WorkspaceID != workspaceID {
		return errors.New("journal escaped requested session")
	}
	seq := after
	if page.RunID != runID {
		if page.RunID == "" || page.Prompt == nil {
			return errors.New("journal changed run without committed prompt")
		}
		seq = 0
	} else if page.Prompt != nil {
		return errors.New("journal repeated committed prompt")
	}
	if page.Prompt != nil && (page.Prompt.RunID != page.RunID || !page.Prompt.Complete || page.Prompt.Role != "user") {
		return errors.New("invalid journal prompt")
	}
	for _, event := range page.Events {
		if event.WorkspaceID != workspaceID || event.SessionID != sessionID || event.RunID != page.RunID || event.Seq != seq+1 {
			return errors.New("journal event scope or continuity mismatch")
		}
		if err := event.Validate(); err != nil {
			return err
		}
		if runevent.IsKnownKind(event.Kind) {
			if _, err := runevent.DecodeSemanticPayload(event); err != nil {
				return err
			}
		}
		seq = event.Seq
	}
	if page.AfterSeq != seq || (page.HasMore && page.Prompt == nil && len(page.Events) == 0) {
		return errors.New("journal cursor did not advance")
	}
	return nil
}
