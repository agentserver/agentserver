package coredb

import (
	"fmt"
	"testing"
)

func TestPostgreSQLWorkspaceLLMGatewayUpdateFencesAllActiveGrantsAtomically(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	workspaceID := stateTestUUID(220_000)
	ownerID := stateTestUUID(220_001)
	developerID := stateTestUUID(220_002)
	gatewayID := stateTestUUID(220_003)
	quotedSchema := quoteIdentifier(schema)
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.workspaces (id, status, managed_lark_credential_mode) VALUES ($1, 'active', 'webhook_swap')", quotedSchema), workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.users (id, status) VALUES ($1, 'active'), ($2, 'active')", quotedSchema), ownerID, developerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'owner'), ($1, $3, 'developer')", quotedSchema), workspaceID, ownerID, developerID); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateWorkspaceLLMGateway(t.Context(), CreateWorkspaceLLMGatewayCommand{
		ID: gatewayID, WorkspaceID: workspaceID, ActorID: ownerID, Name: "original",
		ResponsesURL: "https://llm.example.com/v1/responses", OIDCIssuer: "https://id.example.com",
		OIDCClientID: "original-client", OIDCScopes: "offline_access openid",
		BearerTokenType: LLMGatewayBearerIDToken, DefaultModel: "model-1", MakeDefault: true,
	})
	if err != nil || !created.Created || created.Gateway.Version != 1 {
		t.Fatalf("create Gateway = %+v, %v", created, err)
	}
	insertGrant := fmt.Sprintf(`INSERT INTO %s.workspace_llm_gateway_grants
    (id, gateway_id, workspace_id, user_id, oidc_issuer, oidc_subject, status,
     sealed_token_set, bearer_expires_at)
VALUES ($1, $2, $3, $4, 'https://id.example.com', $5, $6, $7,
        pg_catalog.clock_timestamp() + interval '30 minutes')`, quotedSchema)
	for index, grant := range []struct {
		userID string
		status string
	}{
		{userID: ownerID, status: LLMGatewayGrantStatusActive},
		{userID: developerID, status: LLMGatewayGrantStatusActive},
	} {
		if _, err := pool.Exec(t.Context(), insertGrant, stateTestUUID(220_010+index), gatewayID, workspaceID,
			grant.userID, fmt.Sprintf("subject-%d", index), grant.status, make([]byte, 64)); err != nil {
			t.Fatal(err)
		}
	}
	command := UpdateWorkspaceLLMGatewayCommand{
		ID: gatewayID, WorkspaceID: workspaceID, ActorID: ownerID, Name: "updated",
		ResponsesURL: "https://new-llm.example.com/v1/responses", OIDCIssuer: "https://new-id.example.com",
		OIDCClientID: "updated-client", OIDCScopes: "offline_access openid project:inference",
		BearerTokenType: LLMGatewayBearerAccessToken, DefaultModel: "model-2",
		MakeDefault: true, ExpectedVersion: 1,
	}
	updated, err := store.UpdateWorkspaceLLMGateway(t.Context(), command)
	if err != nil || !updated.Changed || updated.Gateway.Version != 2 || updated.Gateway.Name != "updated" ||
		updated.Gateway.GrantStatus != LLMGatewayGrantStatusReauthRequired {
		t.Fatalf("update Gateway = %+v, %v", updated, err)
	}
	var active, reauth int
	statusQuery := fmt.Sprintf(`SELECT
    pg_catalog.count(*) FILTER (WHERE status = $2),
    pg_catalog.count(*) FILTER (WHERE status = $3)
FROM %s.workspace_llm_gateway_grants
WHERE gateway_id = $1`, quotedSchema)
	if err := pool.QueryRow(t.Context(), statusQuery, gatewayID, LLMGatewayGrantStatusActive, LLMGatewayGrantStatusReauthRequired).Scan(&active, &reauth); err != nil {
		t.Fatal(err)
	}
	if active != 0 || reauth != 2 {
		t.Fatalf("grant fence counts active=%d reauth=%d", active, reauth)
	}
	command.ExpectedVersion = updated.Gateway.Version
	repeated, err := store.UpdateWorkspaceLLMGateway(t.Context(), command)
	if err != nil || repeated.Changed || repeated.Gateway.Version != updated.Gateway.Version {
		t.Fatalf("no-op update = %+v, %v", repeated, err)
	}
	command.ExpectedVersion = 1
	if _, err := store.UpdateWorkspaceLLMGateway(t.Context(), command); !HasStateErrorCode(err, ErrorVersionConflict) {
		t.Fatalf("stale update error = %v", err)
	}
	command.ExpectedVersion = updated.Gateway.Version
	command.ActorID = developerID
	if _, err := store.UpdateWorkspaceLLMGateway(t.Context(), command); !HasStateErrorCode(err, ErrorForbidden) {
		t.Fatalf("developer update error = %v", err)
	}
}
