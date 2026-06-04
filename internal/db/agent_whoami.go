package db

import (
	"database/sql"
	"fmt"
)

type AgentWhoamiLookupState string

const (
	AgentWhoamiOK        AgentWhoamiLookupState = "ok"
	AgentWhoamiUnknown   AgentWhoamiLookupState = "unknown"
	AgentWhoamiForbidden AgentWhoamiLookupState = "forbidden"
)

type AgentWhoami struct {
	UserID        string
	WorkspaceID   string
	WorkspaceName string
	SandboxID     string
	ShortID       string
	DisplayName   string
	Role          string
	SandboxStatus string
}

func (db *DB) GetAgentWhoamiByProxyToken(token string) (*AgentWhoami, AgentWhoamiLookupState, error) {
	pt, err := db.GetProxyToken(token)
	if err != nil {
		return nil, "", fmt.Errorf("get whoami proxy token: %w", err)
	}
	if pt == nil {
		return nil, AgentWhoamiUnknown, nil
	}
	if pt.TokenType != ProxyTokenSandbox || !pt.SandboxID.Valid {
		return nil, AgentWhoamiUnknown, nil
	}
	if !pt.UserID.Valid || pt.UserID.String == "" {
		return nil, AgentWhoamiForbidden, nil
	}

	out := &AgentWhoami{}
	err = db.QueryRow(
		`SELECT pt.user_id,
		        pt.workspace_id,
		        w.name,
		        s.id,
		        COALESCE(s.short_id, ''),
		        COALESCE(NULLIF(ac.display_name, ''), s.name, ''),
		        wm.role,
		        s.status
		   FROM proxy_tokens pt
		   JOIN sandboxes s
		     ON s.id = pt.sandbox_id
		    AND s.workspace_id = pt.workspace_id
		   JOIN workspaces w
		     ON w.id = pt.workspace_id
		   JOIN workspace_members wm
		     ON wm.workspace_id = pt.workspace_id
		    AND wm.user_id = pt.user_id
		   LEFT JOIN agent_cards ac
		     ON ac.sandbox_id = s.id
		  WHERE pt.token = $1
		    AND pt.token_type = 'sandbox'
		    AND pt.user_id IS NOT NULL`, token,
	).Scan(&out.UserID, &out.WorkspaceID, &out.WorkspaceName, &out.SandboxID, &out.ShortID, &out.DisplayName, &out.Role, &out.SandboxStatus)
	if err == sql.ErrNoRows {
		return nil, AgentWhoamiForbidden, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("get agent whoami: %w", err)
	}
	return out, AgentWhoamiOK, nil
}
