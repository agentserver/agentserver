package coredb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/agentserver/agentserver/v2/internal/sessiontitle"
	"github.com/jackc/pgx/v5"
)

// Called only after AppendAttemptEvents validates current attempt generation,
// holder and leases. Lock the session before any journal-head allocation in
// the batch, preserving the run -> session -> journal lock order.
func (s *StateStore) lockTitleProposalSession(ctx context.Context, tx pgx.Tx, run Run, events []AttemptEvent) error {
	for _, event := range events {
		if event.Kind != sessiontitle.EventKind {
			continue
		}
		var id string
		return tx.QueryRow(ctx, fmt.Sprintf(`SELECT id::text FROM %s WHERE id=$1 FOR UPDATE`, s.table("sessions")), run.SessionID).Scan(&id)
	}
	return nil
}

func (s *StateStore) applySessionTitleProposal(ctx context.Context, tx pgx.Tx, run Run, event AttemptEvent) error {
	if event.Kind != sessiontitle.EventKind {
		return nil
	}
	var proposal sessiontitle.Proposal
	if event.SchemaVersion != 1 || event.Object != nil || json.Unmarshal(event.Payload, &proposal) != nil || proposal.Validate() != nil {
		return commandError(ErrorInvalidArgument, "AppendAttemptEvents", "session_title", run.SessionID, "invalid title proposal")
	}
	// Only the first committed run may supply automatic titles. Manual renames
	// (even to the same displayed string) pin the title. A fallback never
	// replaces a generated result, and generated results are accepted once.
	_, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s s
SET title=$2,title_source=$3,title_version=title_version+1,updated_at=pg_catalog.clock_timestamp()
WHERE s.id=$1 AND s.creator_id=$4 AND s.status='active'
  AND (s.title_source='placeholder' OR (s.title_source='fallback' AND $3='generated'))
  AND $5::uuid=(SELECT r.id FROM %s r WHERE r.session_id=s.id ORDER BY r.created_at,r.id LIMIT 1)`, s.table("sessions"), s.table("runs")), run.SessionID, proposal.Title, proposal.Source, run.ActorID, run.ID)
	return err
}
