package db

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

var (
	testDBOnce sync.Once
	testDB     *DB
	testDBErr  error
)

// newTestDB returns a shared *DB connected to TEST_DATABASE_URL with all
// migrations applied. Calls t.Skip when TEST_DATABASE_URL is unset (so
// these tests are no-ops in environments without a Postgres for testing).
func newTestDB(t *testing.T) *DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	testDBOnce.Do(func() {
		testDB, testDBErr = Open(url)
	})
	if testDBErr != nil {
		t.Fatalf("test DB init: %v", testDBErr)
	}
	return testDB
}

func TestAgentSessionTUIFields(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()

	sid := "cse_test_" + t.Name()
	err := d.CreateAgentSessionTUI(ctx, CreateTUISessionParams{
		ID:                  sid,
		WorkspaceID:         "ws_test",
		ExternalID:          "tui:exe_a:1730000000",
		Title:               "TUI session",
		CreatorUserID:       "u_alice",
		PermissionMode:      "ask",
		PreferredExecutorID: "exe_a",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM agent_sessions WHERE id = $1`, sid) })

	s, err := d.GetAgentSession(sid)
	if err != nil || s == nil {
		t.Fatalf("get: %v %v", s, err)
	}
	if s.ChannelType != "tui" {
		t.Errorf("channel_type=%q, want tui", s.ChannelType)
	}
	if s.PermissionMode != "ask" {
		t.Errorf("permission_mode=%q, want ask", s.PermissionMode)
	}
	if s.PreferredExecutorID == nil || *s.PreferredExecutorID != "exe_a" {
		t.Errorf("preferred_executor_id=%v, want exe_a", s.PreferredExecutorID)
	}
}

func TestActiveTurnCAS(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	sid := "cse_cas_" + t.Name()
	if err := d.CreateAgentSessionTUI(ctx, CreateTUISessionParams{
		ID: sid, WorkspaceID: "ws_test", ExternalID: "tui:e:1", CreatorUserID: "u",
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM agent_sessions WHERE id = $1`, sid) })

	ok, err := d.ClaimActiveTurn(ctx, sid, "trn_a")
	if err != nil || !ok {
		t.Fatalf("first claim should succeed: ok=%v err=%v", ok, err)
	}
	ok, _ = d.ClaimActiveTurn(ctx, sid, "trn_b")
	if ok {
		t.Errorf("second claim should fail (turn in progress)")
	}
	cur, _ := d.GetActiveTurn(ctx, sid)
	if cur != "trn_a" {
		t.Errorf("active_turn_id=%q want trn_a", cur)
	}
	if err := d.ClearActiveTurn(ctx, sid, "trn_a"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	cur2, _ := d.GetActiveTurn(ctx, sid)
	if cur2 != "" {
		t.Errorf("after clear, active_turn_id=%q want empty", cur2)
	}
}

func TestAttachResponder(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	sid := "cse_att_" + t.Name()
	_ = d.CreateAgentSessionTUI(ctx, CreateTUISessionParams{
		ID: sid, WorkspaceID: "ws", ExternalID: "tui:e:1", CreatorUserID: "u",
	})
	t.Cleanup(func() { d.Exec(`DELETE FROM agent_sessions WHERE id = $1`, sid) })

	prev, err := d.AttachResponder(ctx, sid, "exe_laptop", true)
	if err != nil {
		t.Fatalf("first attach: %v", err)
	}
	if prev.PreviousResponder != "" || prev.PreviousPreferred != "" {
		t.Errorf("first attach should have no previous: %+v", prev)
	}

	prev2, _ := d.AttachResponder(ctx, sid, "exe_desktop", true)
	if prev2.PreviousResponder != "exe_laptop" {
		t.Errorf("second attach previous_responder=%q want exe_laptop", prev2.PreviousResponder)
	}
	if prev2.PreviousPreferred != "exe_laptop" {
		t.Errorf("second attach previous_preferred=%q want exe_laptop", prev2.PreviousPreferred)
	}
}

func TestListSessionsByChannel_EscapesLikeWildcards(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()

	// Create a session with executor "exe_a".
	sid := "cse_wild_" + t.Name()
	_ = d.CreateAgentSessionTUI(ctx, CreateTUISessionParams{
		ID: sid, WorkspaceID: "ws", ExternalID: "tui:exe_a:100", CreatorUserID: "u",
	})
	t.Cleanup(func() { d.Exec(`DELETE FROM agent_sessions WHERE id = $1`, sid) })

	// Query with a wildcard-laden executor ID that should NOT match exe_a.
	// Without escaping, "exe_%%" would match "exe_a" because _ is a LIKE wildcard.
	list, err := d.ListSessionsByChannel(ctx, "ws", "tui", "exe_%", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range list {
		if item.ID == sid {
			t.Errorf("wildcard injection: executor_id=exe_%% matched exe_a session %q", item.ID)
		}
	}
}

func TestListSessionsByChannel(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	_ = d.CreateAgentSessionTUI(ctx, CreateTUISessionParams{
		ID: "cse_lbc1_" + t.Name(), WorkspaceID: "ws", ExternalID: "tui:exe_a:100", CreatorUserID: "u",
	})
	time.Sleep(10 * time.Millisecond)
	_ = d.CreateAgentSessionTUI(ctx, CreateTUISessionParams{
		ID: "cse_lbc2_" + t.Name(), WorkspaceID: "ws", ExternalID: "tui:exe_a:200", CreatorUserID: "u",
	})
	t.Cleanup(func() {
		d.Exec(`DELETE FROM agent_sessions WHERE id LIKE $1`, "cse_lbc%_"+t.Name())
	})

	list, err := d.ListSessionsByChannel(ctx, "ws", "tui", "exe_a", 10)
	if err != nil {
		t.Fatal(err)
	}

	// Filter to just the sessions this test created (cleanup in case of past runs)
	ours := []SessionListItem{}
	for _, item := range list {
		if item.ID == "cse_lbc1_"+t.Name() || item.ID == "cse_lbc2_"+t.Name() {
			ours = append(ours, item)
		}
	}
	if len(ours) != 2 {
		t.Fatalf("expected exactly 2 of our sessions in result, got %d (full list: %v)", len(ours), list)
	}
	// Most recent (second created) should appear first when filtered preserving order
	if ours[0].ID != "cse_lbc2_"+t.Name() {
		t.Errorf("first of our sessions should be cse_lbc2 (most recent), got %q", ours[0].ID)
	}
}

func TestSetSessionClaudeSessionID(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Set up a row.
	sid := "cse_cc_" + t.Name()
	err := db.CreateAgentSessionTUI(ctx, CreateTUISessionParams{
		ID:              sid,
		WorkspaceID:     "ws_test",
		ExternalID:      "tui:exe:1",
		Title:           "test",
		CreatorUserID:   "u_test",
		PermissionMode:  "ask",
		PreferredModel:  "",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM agent_sessions WHERE id = $1`, sid) })

	// SetSessionClaudeSessionID populates the new column.
	cid := "11111111-1111-4111-8111-111111111111"
	if err := db.SetSessionClaudeSessionID(ctx, sid, cid); err != nil {
		t.Fatalf("SetSessionClaudeSessionID: %v", err)
	}

	// Read it back via existing query path.
	sess, err := db.GetAgentSession(sid)
	if err != nil {
		t.Fatal(err)
	}
	if sess == nil {
		t.Fatal("GetAgentSession returned nil")
	}
	if !sess.ClaudeSessionID.Valid || sess.ClaudeSessionID.String != cid {
		t.Errorf("ClaudeSessionID: got %v, want %q", sess.ClaudeSessionID, cid)
	}

	// Updating an existing claude_session_id (e.g. drift) overwrites.
	cid2 := "22222222-2222-4222-8222-222222222222"
	if err := db.SetSessionClaudeSessionID(ctx, sid, cid2); err != nil {
		t.Fatal(err)
	}
	sess, _ = db.GetAgentSession(sid)
	if sess.ClaudeSessionID.String != cid2 {
		t.Errorf("update didn't take: %q", sess.ClaudeSessionID.String)
	}

	// Setting on nonexistent session returns an error.
	if err := db.SetSessionClaudeSessionID(ctx, "cse_does_not_exist", "33333333-3333-4333-8333-333333333333"); err == nil {
		t.Error("SetSessionClaudeSessionID on missing row should error")
	}
}
