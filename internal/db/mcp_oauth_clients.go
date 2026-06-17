package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MCPOAuthClient mirrors a mcp_oauth_clients row — the agentserver-
// side ownership record for a Hydra-issued OAuth2 client_id. See
// migration 036 for column-level rationale.
type MCPOAuthClient struct {
	ID            string
	UserID        string
	HydraClientID string
	Name          string
	CreatedAt     time.Time
	LastUsedAt    *time.Time
}

// CreateMCPOAuthClient inserts an ownership row. The hydra_client_id
// must already exist in Hydra (call HydraClient.CreateOAuth2Client
// first); this table is purely the user→hydra-client mapping.
func (db *DB) CreateMCPOAuthClient(ctx context.Context, c MCPOAuthClient) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO mcp_oauth_clients (id, user_id, hydra_client_id, name, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, c.ID, c.UserID, c.HydraClientID, c.Name, c.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert mcp_oauth_clients: %w", err)
	}
	return nil
}

// ListMCPOAuthClientsByUser returns the caller's clients in
// reverse-chronological order. Empty list is a valid result (and
// the empty-state the UI / docs are designed around).
func (db *DB) ListMCPOAuthClientsByUser(ctx context.Context, userID string) ([]MCPOAuthClient, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, user_id, hydra_client_id, name, created_at, last_used_at
		  FROM mcp_oauth_clients
		 WHERE user_id = $1
		 ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list mcp_oauth_clients: %w", err)
	}
	defer rows.Close()

	var out []MCPOAuthClient
	for rows.Next() {
		var c MCPOAuthClient
		if err := rows.Scan(&c.ID, &c.UserID, &c.HydraClientID, &c.Name, &c.CreatedAt, &c.LastUsedAt); err != nil {
			return nil, fmt.Errorf("scan mcp_oauth_clients: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetMCPOAuthClient fetches one row by our opaque id, scoped to the
// owning user (so an attacker who guesses a foreign id still gets
// the same not-found response as a missing row). Returns
// sql.ErrNoRows if absent.
func (db *DB) GetMCPOAuthClient(ctx context.Context, id, userID string) (*MCPOAuthClient, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, user_id, hydra_client_id, name, created_at, last_used_at
		  FROM mcp_oauth_clients
		 WHERE id = $1 AND user_id = $2
	`, id, userID)
	var c MCPOAuthClient
	if err := row.Scan(&c.ID, &c.UserID, &c.HydraClientID, &c.Name, &c.CreatedAt, &c.LastUsedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("get mcp_oauth_client: %w", err)
	}
	return &c, nil
}

// DeleteMCPOAuthClient removes the ownership row. Caller is
// responsible for also calling HydraClient.DeleteOAuth2Client to
// purge the underlying Hydra row — keep them in sync. Idempotent:
// 0 rows affected is NOT an error so a stale UI delete still cleans
// up.
func (db *DB) DeleteMCPOAuthClient(ctx context.Context, id, userID string) error {
	_, err := db.ExecContext(ctx, `
		DELETE FROM mcp_oauth_clients
		 WHERE id = $1 AND user_id = $2
	`, id, userID)
	if err != nil {
		return fmt.Errorf("delete mcp_oauth_client: %w", err)
	}
	return nil
}

// TouchMCPOAuthClientLastUsed updates last_used_at to NOW(). Fire-
// and-forget from the OAuthResolver (mcppublic) hot path; the
// resolver looks the row up by hydra_client_id, not id, so this
// takes the same key.
func (db *DB) TouchMCPOAuthClientLastUsed(ctx context.Context, hydraClientID string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE mcp_oauth_clients
		   SET last_used_at = NOW()
		 WHERE hydra_client_id = $1
	`, hydraClientID)
	if err != nil {
		return fmt.Errorf("touch mcp_oauth_client: %w", err)
	}
	return nil
}
