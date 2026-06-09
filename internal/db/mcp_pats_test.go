package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

// setupMCPPATFixtures inserts a user + workspace + membership row
// needed by the FK constraints and returns their ids. Cleanup is
// registered on t.
//
// 2026-06-15: mcp_pats grew a workspace_id NOT NULL column. Every
// fixture row now needs a workspace to bind to; for tests we mint a
// throwaway one per test-name suffix.
func setupMCPPATFixtures(t *testing.T, d *DB) (userID, workspaceID string) {
	t.Helper()
	suffix := fmt.Sprintf("%x", sha256.Sum256([]byte(t.Name())))[:8]
	userID = "u_mpat_" + suffix
	workspaceID = "ws_mpat_" + suffix

	if _, err := d.Exec(
		`INSERT INTO users (id, username, email) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		userID, "mpat_user_"+suffix, "mpat_user_"+suffix+"@example.com",
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := d.Exec(
		`INSERT INTO workspaces (id, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		workspaceID, "mpat-ws-"+suffix,
	); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}

	t.Cleanup(func() {
		// CASCADE deletes mcp_pats too (FKs on both user_id and workspace_id).
		d.Exec(`DELETE FROM mcp_pats WHERE user_id = $1`, userID)
		d.Exec(`DELETE FROM workspaces WHERE id = $1`, workspaceID)
		d.Exec(`DELETE FROM users WHERE id = $1`, userID)
	})
	return
}

func TestMCPPAT_CreateAndList(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	uID, wID := setupMCPPATFixtures(t, d)

	pat := MCPPAT{
		ID:          "agpat_testcreate",
		UserID:      uID,
		WorkspaceID: wID,
		Name:        "test-pat",
		Prefix:      "agpat_testcreate",
		SecretHash:  makeHash("agpat_testcreate_secretvalue"),
		Scopes:      []string{"mcp:read"},
		ExpiresAt:   time.Now().Add(90 * 24 * time.Hour),
	}
	if err := d.CreateMCPPAT(ctx, pat); err != nil {
		t.Fatalf("CreateMCPPAT: %v", err)
	}

	rows, err := d.ListMCPPATsByWorkspace(ctx, wID)
	if err != nil {
		t.Fatalf("ListMCPPATsByWorkspace: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	got := rows[0]
	if got.ID != pat.ID {
		t.Errorf("ID: got %q, want %q", got.ID, pat.ID)
	}
	if got.WorkspaceID != wID {
		t.Errorf("WorkspaceID: got %q, want %q", got.WorkspaceID, wID)
	}
	if got.Name != pat.Name {
		t.Errorf("Name: got %q, want %q", got.Name, pat.Name)
	}
	if got.SecretHash != "" {
		t.Errorf("List must not return SecretHash, got %q", got.SecretHash)
	}
	if len(got.Scopes) != 1 || got.Scopes[0] != "mcp:read" {
		t.Errorf("Scopes: got %v, want [mcp:read]", got.Scopes)
	}

	// Also reachable via ListMCPPATsByUser.
	byUser, err := d.ListMCPPATsByUser(ctx, uID)
	if err != nil {
		t.Fatalf("ListMCPPATsByUser: %v", err)
	}
	if len(byUser) != 1 {
		t.Fatalf("ListMCPPATsByUser: want 1 row, got %d", len(byUser))
	}
}

func TestMCPPAT_RejectsMissingWorkspace(t *testing.T) {
	// FK on workspace_id should reject inserts referencing a non-existent
	// workspace. Documents the NOT NULL + FK semantic the 2026-06-15
	// design amendment introduced.
	d := newTestDB(t)
	ctx := context.Background()
	uID, _ := setupMCPPATFixtures(t, d)

	pat := MCPPAT{
		ID:          "agpat_nows",
		UserID:      uID,
		WorkspaceID: "ws_does_not_exist",
		Name:        "missing-ws",
		Prefix:      "agpat_nows",
		SecretHash:  makeHash("agpat_nows_x"),
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	if err := d.CreateMCPPAT(ctx, pat); err == nil {
		t.Fatal("CreateMCPPAT should FK-fail on unknown workspace")
	}
}

func TestMCPPAT_ValidateHashMatch(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	uID, wID := setupMCPPATFixtures(t, d)

	secret := "agpat_hashtest1_mysecretvalue00000000000000"
	pat := MCPPAT{
		ID:          "agpat_hashtest1",
		UserID:      uID,
		WorkspaceID: wID,
		Name:        "hash-test",
		Prefix:      "agpat_hashtest1",
		SecretHash:  makeHash(secret),
		Scopes:      []string{"mcp:read", "mcp:exec"},
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	if err := d.CreateMCPPAT(ctx, pat); err != nil {
		t.Fatalf("CreateMCPPAT: %v", err)
	}

	got, err := d.ValidateMCPPATSecret(ctx, "agpat_hashtest1", secret)
	if err != nil {
		t.Fatalf("ValidateMCPPATSecret: %v", err)
	}
	if got.UserID != uID {
		t.Errorf("UserID: got %q, want %q", got.UserID, uID)
	}
	if got.WorkspaceID != wID {
		t.Errorf("WorkspaceID: got %q, want %q", got.WorkspaceID, wID)
	}
	if got.SecretHash != "" {
		t.Errorf("SecretHash should be cleared on return, got %q", got.SecretHash)
	}
	if len(got.Scopes) != 2 {
		t.Errorf("Scopes: got %v, want [mcp:read mcp:exec]", got.Scopes)
	}
}

func TestMCPPAT_ValidateHashMismatch(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	uID, wID := setupMCPPATFixtures(t, d)

	secret := "agpat_mismatch_correctsecretvalue00000000"
	pat := MCPPAT{
		ID:          "agpat_mismatch",
		UserID:      uID,
		WorkspaceID: wID,
		Name:        "mismatch-test",
		Prefix:      "agpat_mismatch",
		SecretHash:  makeHash(secret),
		Scopes:      []string{"mcp:read"},
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	if err := d.CreateMCPPAT(ctx, pat); err != nil {
		t.Fatalf("CreateMCPPAT: %v", err)
	}

	_, err := d.ValidateMCPPATSecret(ctx, "agpat_mismatch", "agpat_mismatch_wrongsecret")
	if err != sql.ErrNoRows {
		t.Fatalf("want sql.ErrNoRows on mismatch, got %v", err)
	}
}

func TestMCPPAT_ValidateRevoked(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	uID, wID := setupMCPPATFixtures(t, d)

	secret := "agpat_revoktest_secretvalue000000000000000"
	pat := MCPPAT{
		ID:          "agpat_revoktest",
		UserID:      uID,
		WorkspaceID: wID,
		Name:        "revoke-test",
		Prefix:      "agpat_revoktest",
		SecretHash:  makeHash(secret),
		Scopes:      []string{"mcp:read"},
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	if err := d.CreateMCPPAT(ctx, pat); err != nil {
		t.Fatalf("CreateMCPPAT: %v", err)
	}
	if err := d.RevokeMCPPAT(ctx, wID, "agpat_revoktest"); err != nil {
		t.Fatalf("RevokeMCPPAT: %v", err)
	}

	_, err := d.ValidateMCPPATSecret(ctx, "agpat_revoktest", secret)
	if err != sql.ErrNoRows {
		t.Fatalf("want sql.ErrNoRows for revoked PAT, got %v", err)
	}
}

func TestMCPPAT_ValidateExpired(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	uID, wID := setupMCPPATFixtures(t, d)

	secret := "agpat_expirtest_secretvalue000000000000000"
	pat := MCPPAT{
		ID:          "agpat_expirtest",
		UserID:      uID,
		WorkspaceID: wID,
		Name:        "expire-test",
		Prefix:      "agpat_expirtest",
		SecretHash:  makeHash(secret),
		Scopes:      []string{"mcp:read"},
		ExpiresAt:   time.Now().Add(-time.Hour), // already expired
	}
	if err := d.CreateMCPPAT(ctx, pat); err != nil {
		t.Fatalf("CreateMCPPAT: %v", err)
	}

	_, err := d.ValidateMCPPATSecret(ctx, "agpat_expirtest", secret)
	if err != sql.ErrNoRows {
		t.Fatalf("want sql.ErrNoRows for expired PAT, got %v", err)
	}
}

func TestMCPPAT_RevokeScopedToWorkspace(t *testing.T) {
	// One workspace must not be able to revoke another workspace's PAT
	// by guessing the id (defense in depth — the CRUD API already
	// gates on user membership). Mirrors the workspace_id check in
	// RevokeMCPPAT.
	d := newTestDB(t)
	ctx := context.Background()
	uAlice, wAlice := setupMCPPATFixtures(t, d)

	// Fresh workspace owned by no one in particular; we only need its
	// id to be a valid alternative target for the bogus revoke attempt.
	wOther := "ws_mpat_other_" + fmt.Sprintf("%x", sha256.Sum256([]byte(t.Name()+"-other")))[:6]
	if _, err := d.Exec(
		`INSERT INTO workspaces (id, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		wOther, "other-ws",
	); err != nil {
		t.Fatalf("insert other workspace: %v", err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM workspaces WHERE id = $1`, wOther) })

	secret := "agpat_alicespat_secretvalue000000000000000"
	pat := MCPPAT{
		ID:          "agpat_alicespat",
		UserID:      uAlice,
		WorkspaceID: wAlice,
		Name:        "alice's pat",
		Prefix:      "agpat_alicespat",
		SecretHash:  makeHash(secret),
		Scopes:      []string{"mcp:read"},
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	if err := d.CreateMCPPAT(ctx, pat); err != nil {
		t.Fatalf("CreateMCPPAT: %v", err)
	}

	// Attempt revoke against the wrong workspace: must no-op.
	if err := d.RevokeMCPPAT(ctx, wOther, "agpat_alicespat"); err != nil {
		t.Fatalf("RevokeMCPPAT (wrong workspace): %v", err)
	}

	// PAT still validates.
	got, err := d.ValidateMCPPATSecret(ctx, "agpat_alicespat", secret)
	if err != nil {
		t.Fatalf("ValidateMCPPATSecret: %v (wrong-workspace revoke should have been a no-op)", err)
	}
	if got.WorkspaceID != wAlice {
		t.Errorf("PAT now reports WorkspaceID=%q, want %q", got.WorkspaceID, wAlice)
	}
}

func TestMCPPAT_TouchLastUsed(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	uID, wID := setupMCPPATFixtures(t, d)

	pat := MCPPAT{
		ID:          "agpat_touchtest",
		UserID:      uID,
		WorkspaceID: wID,
		Name:        "touch-test",
		Prefix:      "agpat_touchtest",
		SecretHash:  makeHash("agpat_touchtest_val"),
		Scopes:      []string{"mcp:read"},
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	if err := d.CreateMCPPAT(ctx, pat); err != nil {
		t.Fatalf("CreateMCPPAT: %v", err)
	}

	before := time.Now().Add(-time.Second)
	if err := d.TouchMCPPATLastUsed(ctx, "agpat_touchtest"); err != nil {
		t.Fatalf("TouchMCPPATLastUsed: %v", err)
	}

	rows, err := d.ListMCPPATsByWorkspace(ctx, wID)
	if err != nil {
		t.Fatalf("ListMCPPATsByWorkspace: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected rows after touch")
	}
	var touched *MCPPAT
	for i := range rows {
		if rows[i].ID == "agpat_touchtest" {
			touched = &rows[i]
			break
		}
	}
	if touched == nil {
		t.Fatal("PAT not found after touch")
	}
	if touched.LastUsedAt == nil {
		t.Fatal("LastUsedAt should be set after touch")
	}
	if touched.LastUsedAt.Before(before) {
		t.Errorf("LastUsedAt %v is before %v — not updated", touched.LastUsedAt, before)
	}
	if time.Since(*touched.LastUsedAt) > 5*time.Second {
		t.Errorf("LastUsedAt %v is too far in the past", touched.LastUsedAt)
	}
}

func TestMCPPAT_ListExcludesSecretHash(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	uID, wID := setupMCPPATFixtures(t, d)

	pat := MCPPAT{
		ID:          "agpat_listsecret",
		UserID:      uID,
		WorkspaceID: wID,
		Name:        "list-secret-test",
		Prefix:      "agpat_listsecret",
		SecretHash:  makeHash("agpat_listsecret_somevalue"),
		Scopes:      []string{"mcp:read"},
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	if err := d.CreateMCPPAT(ctx, pat); err != nil {
		t.Fatalf("CreateMCPPAT: %v", err)
	}

	rows, err := d.ListMCPPATsByWorkspace(ctx, wID)
	if err != nil {
		t.Fatalf("ListMCPPATsByWorkspace: %v", err)
	}
	for _, r := range rows {
		if r.SecretHash != "" {
			t.Errorf("List returned SecretHash=%q for PAT %q — must be empty", r.SecretHash, r.ID)
		}
		if r.Scopes == nil {
			t.Errorf("Scopes should be non-nil (empty slice acceptable), got nil for PAT %q", r.ID)
		}
	}
}
