package server

import (
	"context"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/db"
)

func TestRunAuditRetentionOnce_RemovesOldRows(t *testing.T) {
	srv, cleanup := newTestServerTUI(t)
	defer cleanup()
	ctx := context.Background()

	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	recent := time.Now().UTC().Add(-1 * time.Hour)

	oldSessID := "old-sess-" + uniqueIDSuffix(t)
	newSessID := "new-sess-" + uniqueIDSuffix(t)
	t.Cleanup(func() {
		_, _ = srv.DB.Exec(`DELETE FROM exec_audit_sessions WHERE id IN ($1, $2)`, oldSessID, newSessID)
	})
	if err := srv.DB.UpsertAuditSession(db.AuditSession{
		ID: oldSessID, WorkspaceID: "ws_retention", ExeID: "exe", StreamID: "s1", OpenedAt: old,
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.DB.UpsertAuditSession(db.AuditSession{
		ID: newSessID, WorkspaceID: "ws_retention", ExeID: "exe", StreamID: "s2", OpenedAt: recent,
	}); err != nil {
		t.Fatal(err)
	}
	// Backdate the old session so it precedes the cutoff
	if _, err := srv.DB.Exec(`UPDATE exec_audit_sessions SET opened_at=$1 WHERE id=$2`, old, oldSessID); err != nil {
		t.Fatal(err)
	}

	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	res, err := RunAuditRetentionOnce(ctx, srv, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sessions == 0 {
		t.Fatalf("expected at least 1 old session pruned, got %d", res.Sessions)
	}

	// Sanity: new session survives.
	if _, err := srv.DB.GetAuditSession(newSessID); err != nil {
		t.Fatalf("new session unexpectedly deleted: %v", err)
	}
}
