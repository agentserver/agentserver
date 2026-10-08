package coredb

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func normalizedLLMGatewayAuthType(value string) string {
	if value == "" {
		return LLMGatewayAuthOIDC
	}
	return value
}

// AuthorizedUserID is the request actor, not the owner who supplied the key.
func (a LLMGatewayLiveAuthority) AuthorizedUserID() string {
	if a.Gateway.AuthType == LLMGatewayAuthAPIKey {
		return a.APIKeyUserID
	}
	return a.Grant.UserID
}

func (s *StateStore) readAPIKeyLLMGatewayAuthority(ctx context.Context, tx pgx.Tx, operation, workspaceID string, binding RunLLMGatewayBinding, lock bool) (LLMGatewayLiveAuthority, bool, error) {
	lockClause := ""
	if lock {
		lockClause = " FOR SHARE OF gateway, workspace, member, local_user"
	}
	query := fmt.Sprintf(`SELECT %s, gateway.sealed_api_key
FROM %s AS gateway
JOIN %s AS workspace ON workspace.id=gateway.workspace_id AND workspace.status='active'
JOIN %s AS member ON member.workspace_id=gateway.workspace_id AND member.user_id=$4 AND member.role IN ('owner','developer')
JOIN %s AS local_user ON local_user.id=member.user_id AND local_user.status='active'
WHERE gateway.id=$1 AND gateway.workspace_id=$2 AND gateway.version=$3
AND gateway.default_model=$5 AND gateway.status='active' AND gateway.auth_type='api_key'
AND gateway.sealed_api_key IS NOT NULL%s`, workspaceLLMGatewayColumns("gateway"), s.table("workspace_llm_gateways"), s.table("workspaces"), s.table("workspace_members"), s.table("users"), lockClause)
	var a LLMGatewayLiveAuthority
	g := &a.Gateway
	err := tx.QueryRow(ctx, query, binding.GatewayID, workspaceID, binding.ConfigVersion, binding.GrantUserID, binding.Model).Scan(
		&g.ID, &g.WorkspaceID, &g.Name, &g.ResponsesURL, &g.OIDCIssuer, &g.OIDCClientID, &g.OIDCScopes, &g.BearerTokenType, &g.DefaultModel, &g.Status, &g.Default, &g.Version, &g.CreatedBy, &g.CreatedAt, &g.UpdatedAt, &g.AuthType, &g.APIKeyConfigured, &a.SealedAPIKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return LLMGatewayLiveAuthority{}, false, nil
	}
	if err != nil {
		return LLMGatewayLiveAuthority{}, false, databaseError(operation+" read workspace API key authority", err)
	}
	a.APIKeyUserID = binding.GrantUserID
	a.Model = binding.Model
	a.SealedAPIKey = append([]byte(nil), a.SealedAPIKey...)
	return a, true, nil
}
