# Agent Whoami Identity Design

**Date:** 2026-06-04
**Status:** Ready for review

## Context

Observer needs an authenticated public endpoint that maps an
agentserver-issued sandbox `proxy_token` to the identity tuple:

```json
{
  "user_id": "u_abc123",
  "workspace_id": "ws_xyz789",
  "workspace_name": "Alice's Workspace",
  "sandbox_id": "sbx_456",
  "short_id": "alice-driver-01",
  "display_name": "Alice Driver",
  "role": "developer"
}
```

`POST /internal/validate-proxy-token` is not suitable for this cross-trust-domain
consumer. Audit H-19 identifies it as unauthenticated and useful for token
probing / sandbox metadata enumeration. The new endpoint should live under the
public agent API surface:

```http
GET /api/agent/whoami
Authorization: Bearer <proxy_token>
```

## Current State

Agentserver already has public `/api/agent/*` routes authenticated by sandbox
tokens, but the current helper is too broad for this contract:

- `extractProxyTokenSandbox` calls `GetSandboxByAnyToken`.
- `GetSandboxByAnyToken` accepts both `proxy_token` and `tunnel_token`.
- Existing agent routes do not consistently gate on sandbox lifecycle status.
- `proxy_tokens` and `sandboxes` contain `workspace_id` and `sandbox_id`, but do
  not contain a reliable `user_id` lineage.

`/api/agent/register` has the OAuth subject at registration time and verifies
the user's workspace role, but the subject is not persisted when the sandbox and
proxy token are created. Web-created sandboxes have the session user in request
context, but that user is also not persisted into token lineage.

This means agentserver can currently resolve `workspace_id`, `sandbox_id`,
`short_id`, sandbox status, sandbox name, card display name, and workspace name
from a proxy token, but cannot reliably resolve `user_id` or
`workspace_members.role`.

## Goals

- Add `GET /api/agent/whoami` for sandbox-scoped `proxy_token` identity
  introspection.
- Make agentserver the source of truth for observer's user / workspace /
  sandbox identity mapping.
- Return exactly the seven documented fields.
- Do not return secrets or token expiry / TTL information.
- Do not extend `/internal/validate-proxy-token`.
- Avoid accepting `tunnel_token` or workspace-scoped tokens for this endpoint.
- Preserve existing `validate-proxy-token` behavior for llmproxy and
  credentialproxy callers.

## Non-Goals

- No observer-side user table.
- No cache TTL contract in the response body.
- No attempt to infer a user for historical tokens without recorded lineage.
- No broad refactor of all existing `/api/agent/*` handlers in the first
  implementation. The whoami endpoint gets strict auth semantics; broader
  unification can follow separately.

## Recommended Approach

Add user lineage to sandbox proxy tokens and implement a strict whoami resolver
against `proxy_tokens`.

### Schema

Add `user_id` to `proxy_tokens`:

```sql
ALTER TABLE proxy_tokens
  ADD COLUMN IF NOT EXISTS user_id TEXT REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_proxy_tokens_user
  ON proxy_tokens (user_id)
  WHERE user_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_proxy_tokens_workspace_user
  ON proxy_tokens (workspace_id, user_id)
  WHERE user_id IS NOT NULL;
```

`user_id` is nullable so existing rows remain valid. New sandbox-scoped tokens
must write it. Workspace-scoped tokens may keep it null because they are not a
sandbox identity and are out of scope for whoami.

### Token Issuance

Update sandbox token creation paths to persist the current user:

- `/api/agent/register`: use the OAuth introspection subject.
- `POST /api/workspaces/{wid}/sandboxes`: use `auth.UserIDFromContext`.
- Any direct DB/store helper that creates a sandbox proxy token should accept a
  `userID` parameter and write it to `proxy_tokens.user_id`.

The denormalized `sandboxes.proxy_token` column can remain unchanged. The
authoritative identity lineage for whoami is `proxy_tokens.user_id`.

### Legacy Tokens

Existing tokens with `proxy_tokens.user_id IS NULL` cannot be mapped to a user
without guesswork. The endpoint must return `403 forbidden` for those rows.
Operators can restore whoami eligibility by recreating or re-registering the
sandbox so a new token is minted with user lineage.

Do not backfill historical rows by choosing a workspace owner or first member.
That would produce plausible but false identity data.

### Strict Resolver

Add a new DB helper such as `GetAgentWhoamiByProxyToken(token string)`.

It should:

1. Look up the token in `proxy_tokens`.
2. Require `token_type = 'sandbox'`.
3. Require a non-null `sandbox_id` and `user_id`.
4. Join to `sandboxes`, `workspaces`, and `workspace_members`.
5. Left join `agent_cards` on `agent_cards.sandbox_id = sandboxes.id`.
6. Require `workspace_members.workspace_id = proxy_tokens.workspace_id`.
7. Require `workspace_members.user_id = proxy_tokens.user_id`.
8. Return the sandbox status for handler-side lifecycle gating.

Do not use `GetSandboxByAnyToken`, because that accepts `tunnel_token`.

### Endpoint Contract

```http
GET /api/agent/whoami
Authorization: Bearer <proxy_token>
```

Successful response:

```json
{
  "user_id": "u_abc123",
  "workspace_id": "ws_xyz789",
  "workspace_name": "Alice's Workspace",
  "sandbox_id": "sbx_456",
  "short_id": "alice-driver-01",
  "display_name": "Alice Driver",
  "role": "developer"
}
```

`workspace_name`, `short_id`, and `display_name` should serialize as empty
strings when the database value is empty or null. `display_name` is the
human-facing agent name and should be resolved as:

```sql
COALESCE(agent_cards.display_name, sandboxes.name, '')
```

This prefers the capability card's display name when present and falls back to
the sandbox name for agents that have not registered a card.

The response should set:

```http
Content-Type: application/json
Cache-Control: no-store
```

The endpoint is read-only and idempotent, but it is an authenticated identity
response. It should not be stored by shared HTTP caches. Observer can still
apply its own local TTL cache.

### Error Semantics

Return a constant 401 body for all unauthenticated token failures:

- Missing Authorization header.
- Malformed Authorization header.
- Unknown token.
- Token exists but is not a sandbox-scoped proxy token.

```http
401 Unauthorized

unauthorized
```

Return 403 when the token is known as a sandbox proxy token but the current
identity is not usable:

- Sandbox status is not `creating` or `running`.
- `proxy_tokens.user_id` is null.
- The recorded user is no longer a workspace member.
- The joined workspace or sandbox row is missing due to inconsistent state.

```http
403 Forbidden

forbidden
```

The original issue's "workspace removed returns 403" should be interpreted as
"workspace identity is no longer usable returns 403." With the current schema,
workspace deletion normally cascades `proxy_tokens`, so the token may instead
become unknown and return 401.

### OpenAPI And Docs

Add `AgentWhoamiResponse` to `internal/server/api_types.go` and annotate the
handler with swag comments under the `Agent` tag.

Regenerate:

```bash
make openapi
make api-docs
```

The generated docs should include the new operation in
`docs/api/reference/agent.md`.

## Alternatives Considered

### Reuse `GetSandboxByAnyToken` and infer user from workspace membership

Rejected. It would accept `tunnel_token` and cannot uniquely identify a user in
multi-member workspaces. Returning an owner or first member would create false
identity data.

### Add `created_by_user_id` to `sandboxes`

Viable, but less directly tied to the credential being introspected. The
contract asks what identity a `proxy_token` represents; storing lineage on
`proxy_tokens` keeps the credential as the source of truth and naturally
supports future token rotation.

### Extend `/internal/validate-proxy-token`

Rejected. That endpoint is unauthenticated internal surface and is already used
by llmproxy and credentialproxy. Adding auth plus expanding the response would
couple unrelated callers and risk breaking existing integrations.

## Testing

Add focused handler / DB integration tests:

- Valid sandbox proxy token with recorded user lineage returns 200 and exactly
  the seven response fields.
- Missing header returns `401 unauthorized`.
- Malformed bearer returns `401 unauthorized`.
- Unknown token returns `401 unauthorized`.
- Workspace-scoped token returns `401 unauthorized`.
- `tunnel_token` returns `401 unauthorized`.
- Suspended statuses such as `paused`, `offline`, `deleting`, or `pausing`
  return `403 forbidden`.
- Legacy sandbox proxy token with null `user_id` returns `403 forbidden`.
- Removing the recorded workspace member returns `403 forbidden`.
- Response does not include token, upstream credentials, expiry, or TTL fields.

Run at minimum:

```bash
go test ./internal/server ./internal/db
make openapi-check
make api-docs-check
```

## Rollout

1. Deploy schema migration with nullable `proxy_tokens.user_id`.
2. Deploy server changes that write `user_id` for new sandbox proxy tokens.
3. Deploy whoami endpoint.
4. Update observer to call whoami and treat 5xx as upstream unavailable.
5. Re-register or recreate any legacy agents that need observer integration.

No coordinated change is required for llmproxy or credentialproxy because
`/internal/validate-proxy-token` remains unchanged.
