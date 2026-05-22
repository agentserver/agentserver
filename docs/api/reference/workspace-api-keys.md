# Workspace API Keys

Endpoints under the `Workspace API Keys` tag. Auto-generated from [`docs/api/openapi.yaml`](../openapi.yaml) — do not edit by hand.

> Run `make api-docs` after changing handler annotations to regenerate this file.

## Operations

| Method | Path | Summary |
|--------|------|---------|
| `GET` | [`/api/workspaces/{wid}/api-keys`](#op-get-api-workspaces-wid-api-keys) | List workspace API keys |
| `POST` | [`/api/workspaces/{wid}/api-keys`](#op-post-api-workspaces-wid-api-keys) | Mint a workspace API key |
| `GET` | [`/api/workspaces/{wid}/api-keys/scopes`](#op-get-api-workspaces-wid-api-keys-scopes) | List available API key scopes |
| `DELETE` | [`/api/workspaces/{wid}/api-keys/{id}`](#op-delete-api-workspaces-wid-api-keys-id) | Revoke a workspace API key |

### `GET /api/workspaces/{wid}/api-keys` {#op-get-api-workspaces-wid-api-keys}
List workspace API keys
Returns prefix-only metadata. Secrets are never included.

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `wid` | `string` | yes | Workspace id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | array of [`WorkspaceAPIKey`](#schema-workspaceapikey) |
| `403` | not a member | `string` |
| `500` | internal error | `string` |


### `POST /api/workspaces/{wid}/api-keys` {#op-post-api-workspaces-wid-api-keys}
Mint a workspace API key
Returns the secret ONCE in the response body. At least one Available scope must be provided.

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `wid` | `string` | yes | Workspace id |


**Request body**

Content-Type: `application/json`

Schema: [`WorkspaceAPIKeyMintRequest`](#schema-workspaceapikeymintrequest)

```yaml
{
  expires_at?: string
  name: string
  scopes: []string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `201` | Created | [`WorkspaceAPIKeyMintResponse`](#schema-workspaceapikeymintresponse) |
| `400` | name required / scope not available / at least one scope required | `string` |
| `403` | owner or maintainer required | `string` |
| `422` | expires_at invalid (bad RFC3339 / in past / >365d in future) | `string` |
| `500` | internal error | `string` |


### `GET /api/workspaces/{wid}/api-keys/scopes` {#op-get-api-workspaces-wid-api-keys-scopes}
List available API key scopes
Returns the catalog of scope names + descriptions. Available=false entries are placeholders shown greyed-out in the UI.

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `wid` | `string` | yes | Workspace id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | array of [`APIKeyScopeDescriptor`](#schema-apikeyscopedescriptor) |
| `403` | not a member | `string` |


### `DELETE /api/workspaces/{wid}/api-keys/{id}` {#op-delete-api-workspaces-wid-api-keys-id}
Revoke a workspace API key

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `wid` | `string` | yes | Workspace id |
| `id` | `string` | yes | Key id (= prefix) |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `204` | No Content | — |
| `403` | owner or maintainer required | `string` |
| `500` | internal error | `string` |


## Schemas

### `APIKeyScopeDescriptor` {#schema-apikeyscopedescriptor}

```yaml
{
  available: boolean
  description: string
  name: string
}
```

### `WorkspaceAPIKey` {#schema-workspaceapikey}

```yaml
{
  created_at: string
  expires_at: string
  id: string
  last_used_at?: string
  name: string
  prefix: string
  revoked_at?: string
  scopes: []string
}
```

### `WorkspaceAPIKeyMintRequest` {#schema-workspaceapikeymintrequest}

```yaml
{
  expires_at?: string
  name: string
  scopes: []string
}
```

### `WorkspaceAPIKeyMintResponse` {#schema-workspaceapikeymintresponse}

```yaml
{
  created_at: string
  expires_at: string
  id: string
  name: string
  prefix: string
  scopes: []string
  secret: string
}
```
