package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/internal/secrets"
	"github.com/lib/pq"
)

// MCPPAT mirrors mcp_pats rows. SecretHash is only populated by the
// validate path that just matched; List / Get omit it.
//
// 2026-06-15 design amendment: WorkspaceID is a first-class column,
// NOT NULL, FK to workspaces. One PAT = one workspace. See the
// migration file (035_mcp_pats.sql) for the rationale.
//
// Scopes are catalog strings — only mcp:read and mcp:exec are
// meaningful now; the workspace:<id> scope from the earlier draft
// has been removed since workspace is intrinsic to the PAT row.
// Validation of the catalog is the server layer's job.
type MCPPAT struct {
	ID          string
	UserID      string
	WorkspaceID string
	Name        string
	Prefix      string
	SecretHash  string // populated only by Validate, never by List/Get
	Scopes      []string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	LastUsedAt  *time.Time
	RevokedAt   *time.Time
}

// CreateMCPPAT inserts a new PAT row. Caller is responsible for
// generating id (= "agpat_<id>"), prefix, secret_hash (via
// secrets.Mint + secrets.Hash) and a non-empty expires_at, plus
// supplying a valid WorkspaceID (FK violation surfaces here on miss).
func (db *DB) CreateMCPPAT(ctx context.Context, p MCPPAT) error {
	scopes := p.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO mcp_pats
		    (id, user_id, workspace_id, name, prefix, secret_hash, scopes, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		p.ID, p.UserID, p.WorkspaceID, p.Name, p.Prefix, p.SecretHash, pq.Array(scopes), p.ExpiresAt)
	if err != nil {
		return fmt.Errorf("insert mcp_pats: %w", err)
	}
	return nil
}

// ListMCPPATsByWorkspace returns all PATs bound to a workspace,
// active + revoked, sorted newest-first. The workspace-scoped settings
// UI calls this; for a user-wide "all my PATs" listing across their
// workspaces, see ListMCPPATsByUser.
func (db *DB) ListMCPPATsByWorkspace(ctx context.Context, workspaceID string) ([]MCPPAT, error) {
	return db.listMCPPATs(ctx, "workspace_id", workspaceID)
}

// ListMCPPATsByUser returns all PATs owned by a user across every
// workspace, sorted newest-first. Useful for an account-level audit
// view.
func (db *DB) ListMCPPATsByUser(ctx context.Context, userID string) ([]MCPPAT, error) {
	return db.listMCPPATs(ctx, "user_id", userID)
}

func (db *DB) listMCPPATs(ctx context.Context, scopeCol, scopeVal string) ([]MCPPAT, error) {
	// scopeCol is one of "user_id" or "workspace_id" — chosen by the
	// public wrappers above, never interpolated from user input.
	q := `SELECT id, user_id, workspace_id, name, prefix, scopes,
		       created_at, expires_at, last_used_at, revoked_at
		  FROM mcp_pats
		 WHERE ` + scopeCol + ` = $1
		 ORDER BY created_at DESC`
	rows, err := db.QueryContext(ctx, q, scopeVal)
	if err != nil {
		return nil, fmt.Errorf("list mcp_pats: %w", err)
	}
	defer rows.Close()

	var out []MCPPAT
	for rows.Next() {
		var p MCPPAT
		var lastUsed, revoked sql.NullTime
		var scopes pq.StringArray
		if err := rows.Scan(&p.ID, &p.UserID, &p.WorkspaceID, &p.Name, &p.Prefix, &scopes,
			&p.CreatedAt, &p.ExpiresAt, &lastUsed, &revoked); err != nil {
			return nil, fmt.Errorf("scan mcp_pats: %w", err)
		}
		p.Scopes = []string(scopes)
		if lastUsed.Valid {
			t := lastUsed.Time
			p.LastUsedAt = &t
		}
		if revoked.Valid {
			t := revoked.Time
			p.RevokedAt = &t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RevokeMCPPAT soft-deletes by stamping revoked_at. Scoped to
// (workspaceID, patID) so callers can't revoke another workspace's
// PAT by guessing the id (the CRUD API also already gates on user
// membership in workspaceID before reaching here). Idempotent:
// re-revoking a revoked row is a no-op.
func (db *DB) RevokeMCPPAT(ctx context.Context, workspaceID, patID string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE mcp_pats
		   SET revoked_at = NOW()
		 WHERE id = $1 AND workspace_id = $2 AND revoked_at IS NULL`,
		patID, workspaceID)
	if err != nil {
		return fmt.Errorf("revoke mcp_pats: %w", err)
	}
	return nil
}

// ValidateMCPPATSecret looks up the PAT by prefix, constant-time compares
// the hash, and returns the active row (including scopes) on match.
// Returns sql.ErrNoRows on any mismatch (wrong prefix, wrong secret,
// revoked, expired).
//
// On match, callers should fire TouchMCPPATLastUsed (best-effort, off the
// hot path).
func (db *DB) ValidateMCPPATSecret(ctx context.Context, prefix, secret string) (*MCPPAT, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, user_id, workspace_id, name, prefix, secret_hash, scopes,
		       created_at, expires_at, last_used_at, revoked_at
		  FROM mcp_pats
		 WHERE prefix = $1 AND revoked_at IS NULL AND expires_at > NOW()`, prefix)
	var p MCPPAT
	var lastUsed, revoked sql.NullTime
	var scopes pq.StringArray
	if err := row.Scan(&p.ID, &p.UserID, &p.WorkspaceID, &p.Name, &p.Prefix, &p.SecretHash, &scopes,
		&p.CreatedAt, &p.ExpiresAt, &lastUsed, &revoked); err != nil {
		return nil, err // includes sql.ErrNoRows
	}
	if !secrets.ConstantTimeMatch(secret, p.SecretHash) {
		return nil, sql.ErrNoRows
	}
	p.Scopes = []string(scopes)
	if lastUsed.Valid {
		t := lastUsed.Time
		p.LastUsedAt = &t
	}
	// Defensive: should never happen given WHERE clause, but if a
	// concurrent revoke landed between our query and now, treat as miss.
	if revoked.Valid {
		return nil, sql.ErrNoRows
	}
	p.SecretHash = "" // do not leak hash to callers
	return &p, nil
}

// TouchMCPPATLastUsed bumps last_used_at to NOW(). Fire-and-forget; errors
// are logged by caller, not surfaced.
func (db *DB) TouchMCPPATLastUsed(ctx context.Context, patID string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE mcp_pats SET last_used_at = NOW() WHERE id = $1`, patID)
	if err != nil {
		return fmt.Errorf("touch mcp_pats last_used_at: %w", err)
	}
	return nil
}
