# Codex Tokens

Endpoints under the `Codex Tokens` tag. Auto-generated from [`docs/api/openapi.yaml`](../openapi.yaml) — do not edit by hand.

> Run `make api-docs` after changing handler annotations to regenerate this file.

## Operations

| Method | Path | Summary |
|--------|------|---------|
| `GET` | [`/api/codex/tokens`](#op-get-api-codex-tokens) | List Codex tokens for a workspace |
| `POST` | [`/api/codex/tokens`](#op-post-api-codex-tokens) | Mint a Codex access token |
| `DELETE` | [`/api/codex/tokens/{id}`](#op-delete-api-codex-tokens-id) | Revoke a Codex token |

### `GET /api/codex/tokens` {#op-get-api-codex-tokens}
List Codex tokens for a workspace

**Auth:** `CookieAuth`


**Query parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `workspace_id` | `string` | yes | Workspace id |
| `include_revoked` | `boolean` | no | Include revoked tokens (default false) |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | array of [`CodexTokenListItem`](#schema-codextokenlistitem) |
| `400` | workspace_id required | `string` |
| `401` | unauthorized | `string` |
| `403` | not a member | `string` |
| `500` | internal error | `string` |


### `POST /api/codex/tokens` {#op-post-api-codex-tokens}
Mint a Codex access token

**Auth:** `CookieAuth`


**Request body**

Content-Type: `application/json`

Schema: [`CodexTokenMintRequest`](#schema-codextokenmintrequest)

```yaml
{
  expires_at?: string
  name: string
  workspace_id: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `201` | Created | [`CodexTokenMintResponse`](#schema-codextokenmintresponse) |
| `400` | invalid JSON | `string` |
| `401` | unauthorized | `string` |
| `403` | not a member of this workspace | `string` |
| `422` | workspace_id and name are required / expires_at invalid | `string` |
| `500` | internal error | `string` |


### `DELETE /api/codex/tokens/{id}` {#op-delete-api-codex-tokens-id}
Revoke a Codex token

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Token id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `204` | No Content | — |
| `401` | unauthorized | `string` |
| `403` | forbidden | `string` |
| `500` | internal error | `string` |


## Schemas

### `CodexTokenListItem` {#schema-codextokenlistitem}

```yaml
{
  created_at: string
  expires_at: string
  id: string
  last_used_at?: string
  name: string
  revoked?: boolean
  revoked_at?: string
  workspace_id: string
}
```

### `CodexTokenMintRequest` {#schema-codextokenmintrequest}

```yaml
{
  expires_at?: string
  name: string
  workspace_id: string
}
```

### `CodexTokenMintResponse` {#schema-codextokenmintresponse}

```yaml
{
  created_at: string
  expires_at: string
  id: string
  name: string
  token: string
  workspace_id: string
}
```
