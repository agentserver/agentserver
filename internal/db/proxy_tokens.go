package db

import (
	"database/sql"
	"fmt"

	"github.com/agentserver/agentserver/internal/secrets"
)

// ProxyTokenType discriminates the two kinds of tokens stored in
// proxy_tokens. Sandbox tokens are bound to a specific sandbox lifecycle
// (must be 'running' / 'creating' to authorize requests). Workspace tokens
// are bound only to a workspace and authorize cc-broker turn workers, which
// do not have a sandbox identity.
type ProxyTokenType string

const (
	ProxyTokenSandbox   ProxyTokenType = "sandbox"
	ProxyTokenWorkspace ProxyTokenType = "workspace"
)

// ProxyToken is a row from the proxy_tokens table.
type ProxyToken struct {
	Token       string
	TokenType   ProxyTokenType
	SandboxID   sql.NullString
	WorkspaceID string
	UserID      sql.NullString
}

// GetProxyToken looks up a token in the unified proxy_tokens table. Returns
// (nil, nil) if the token is not present. Callers (llmproxy validation)
// branch on TokenType to decide whether sandbox status checks apply.
func (db *DB) GetProxyToken(token string) (*ProxyToken, error) {
	pt := &ProxyToken{}
	err := db.QueryRow(
		`SELECT token, token_type, sandbox_id, workspace_id, user_id
		   FROM proxy_tokens WHERE token = $1`, token,
	).Scan(&pt.Token, &pt.TokenType, &pt.SandboxID, &pt.WorkspaceID, &pt.UserID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get proxy token: %w", err)
	}
	return pt, nil
}

// CreateSandboxProxyToken inserts a sandbox-scoped row into proxy_tokens.
// CreateSandbox / CreateLocalSandbox call this in the same DB session as the
// sandbox INSERT so the two tables stay in lockstep.
func (db *DB) CreateSandboxProxyToken(token, sandboxID, workspaceID, userID string) error {
	if token == "" {
		return nil
	}
	_, err := db.Exec(
		`INSERT INTO proxy_tokens (token, token_type, sandbox_id, workspace_id, user_id)
		 VALUES ($1, 'sandbox', $2, $3, NULLIF($4, ''))
		 ON CONFLICT (token) DO NOTHING`,
		token, sandboxID, workspaceID, userID,
	)
	if err != nil {
		return fmt.Errorf("create sandbox proxy token: %w", err)
	}
	return nil
}

// GetOrCreateWorkspaceToken returns the workspace's persistent proxy token,
// creating one if none exists. Idempotent: concurrent callers race-free
// thanks to the unique index on (workspace_id) WHERE token_type='workspace'.
func (db *DB) GetOrCreateWorkspaceToken(workspaceID string) (string, error) {
	var existing string
	err := db.QueryRow(
		`SELECT token FROM proxy_tokens
		   WHERE workspace_id = $1 AND token_type = 'workspace'`,
		workspaceID,
	).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("lookup workspace token: %w", err)
	}

	token, err := newProxyToken()
	if err != nil {
		return "", err
	}
	_, err = db.Exec(
		`INSERT INTO proxy_tokens (token, token_type, workspace_id)
		 VALUES ($1, 'workspace', $2)
		 ON CONFLICT (workspace_id) WHERE token_type = 'workspace' DO NOTHING`,
		token, workspaceID,
	)
	if err != nil {
		return "", fmt.Errorf("insert workspace token: %w", err)
	}

	// On conflict (concurrent insert from another caller), the INSERT was a
	// no-op and our token wasn't stored. Re-read to return the winner's.
	err = db.QueryRow(
		`SELECT token FROM proxy_tokens
		   WHERE workspace_id = $1 AND token_type = 'workspace'`,
		workspaceID,
	).Scan(&existing)
	if err != nil {
		return "", fmt.Errorf("re-read workspace token after insert: %w", err)
	}
	return existing, nil
}

// newProxyToken generates a 32-byte random token, hex-encoded. 64 hex chars,
// 256 bits of entropy — well above the bar for opaque API keys.
func newProxyToken() (string, error) {
	tok, err := secrets.RandomHex(32)
	if err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return tok, nil
}
