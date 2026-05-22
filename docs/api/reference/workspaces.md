# Workspaces

Endpoints under the `Workspaces` tag. Auto-generated from [`docs/api/openapi.yaml`](../openapi.yaml) — do not edit by hand.

> Run `make api-docs` after changing handler annotations to regenerate this file.

## Operations

| Method | Path | Summary |
|--------|------|---------|
| `GET` | [`/api/workspaces`](#op-get-api-workspaces) | List workspaces for the current user |
| `POST` | [`/api/workspaces`](#op-post-api-workspaces) | Create a new workspace |
| `GET` | [`/api/workspaces/quota`](#op-get-api-workspaces-quota) | Get per-user workspace quota |
| `GET` | [`/api/workspaces/{id}`](#op-get-api-workspaces-id) | Get a workspace by id |
| `PATCH` | [`/api/workspaces/{id}`](#op-patch-api-workspaces-id) | Rename a workspace |
| `DELETE` | [`/api/workspaces/{id}`](#op-delete-api-workspaces-id) | Delete a workspace (owner only; cascades to sandboxes + namespace) |
| `GET` | [`/api/workspaces/{id}/llm-config`](#op-get-api-workspaces-id-llm-config) | Get workspace LLM config (owner/maintainer) |
| `PUT` | [`/api/workspaces/{id}/llm-config`](#op-put-api-workspaces-id-llm-config) | Upsert workspace LLM config (owner/maintainer) |
| `DELETE` | [`/api/workspaces/{id}/llm-config`](#op-delete-api-workspaces-id-llm-config) | Delete workspace LLM config (owner/maintainer) |
| `GET` | [`/api/workspaces/{id}/llm-quota`](#op-get-api-workspaces-id-llm-quota) | Get the workspace's daily LLM request quota usage |
| `GET` | [`/api/workspaces/{id}/members`](#op-get-api-workspaces-id-members) | List members of a workspace |
| `POST` | [`/api/workspaces/{id}/members`](#op-post-api-workspaces-id-members) | Add a member to a workspace |
| `PUT` | [`/api/workspaces/{id}/members/{userId}`](#op-put-api-workspaces-id-members-userid) | Change a member's role (owner only) |
| `DELETE` | [`/api/workspaces/{id}/members/{userId}`](#op-delete-api-workspaces-id-members-userid) | Remove a member (owner only) |

### `GET /api/workspaces` {#op-get-api-workspaces}
List workspaces for the current user

**Auth:** `CookieAuth`


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | array of [`Workspace`](#schema-workspace) |
| `500` | internal error | `string` |


### `POST /api/workspaces` {#op-post-api-workspaces}
Create a new workspace
Creator is auto-added as owner. May fail with 403 if the per-user workspace quota is exceeded.

**Auth:** `CookieAuth`


**Request body**

Content-Type: `application/json`

Schema: [`WorkspaceCreateRequest`](#schema-workspacecreaterequest)

```yaml
{
  name: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `201` | Created | [`Workspace`](#schema-workspace) |
| `400` | bad request / empty name | `string` |
| `403` | workspace quota exceeded | `string` |
| `500` | internal error | `string` |


### `GET /api/workspaces/quota` {#op-get-api-workspaces-quota}
Get per-user workspace quota

**Auth:** `CookieAuth`


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`WorkspaceQuotaResponse`](#schema-workspacequotaresponse) |
| `500` | internal error | `string` |


### `GET /api/workspaces/{id}` {#op-get-api-workspaces-id}
Get a workspace by id

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`Workspace`](#schema-workspace) |
| `403` | not a member | `string` |
| `404` | workspace not found | `string` |
| `500` | internal error | `string` |


### `PATCH /api/workspaces/{id}` {#op-patch-api-workspaces-id}
Rename a workspace

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |


**Request body**

Content-Type: `application/json`

Schema: [`WorkspaceRenameRequest`](#schema-workspacerenamerequest)

```yaml
{
  name: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`Workspace`](#schema-workspace) |
| `400` | empty name | `string` |
| `403` | owner or maintainer required | `string` |
| `500` | internal error | `string` |


### `DELETE /api/workspaces/{id}` {#op-delete-api-workspaces-id}
Delete a workspace (owner only; cascades to sandboxes + namespace)

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `204` | No Content | — |
| `403` | owner only | `string` |
| `500` | internal error | `string` |


### `GET /api/workspaces/{id}/llm-config` {#op-get-api-workspaces-id-llm-config}
Get workspace LLM config (owner/maintainer)
The returned api_key is masked (first 3 + "..." + last 4). updated_at is null when no config is set.

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`LLMConfigResponse`](#schema-llmconfigresponse) |
| `403` | insufficient role | `string` |
| `500` | internal error | `string` |


### `PUT /api/workspaces/{id}/llm-config` {#op-put-api-workspaces-id-llm-config}
Upsert workspace LLM config (owner/maintainer)
On update, omitting api_key retains the existing key.

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |


**Request body**

Content-Type: `application/json`

Schema: [`LLMConfigUpsertRequest`](#schema-llmconfigupsertrequest)

```yaml
{
  api_key?: string
  base_url: string
  models: []LLMModel
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`LLMConfigUpsertResponse`](#schema-llmconfigupsertresponse) |
| `400` | validation error (invalid URL / missing field / too many models) | `string` |
| `403` | insufficient role | `string` |
| `500` | internal error | `string` |


### `DELETE /api/workspaces/{id}/llm-config` {#op-delete-api-workspaces-id-llm-config}
Delete workspace LLM config (owner/maintainer)

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `204` | No Content | — |
| `403` | insufficient role | `string` |
| `500` | internal error | `string` |


### `GET /api/workspaces/{id}/llm-quota` {#op-get-api-workspaces-id-llm-quota}
Get the workspace's daily LLM request quota usage

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`LLMQuotaResponse`](#schema-llmquotaresponse) |
| `403` | insufficient role | `string` |
| `500` | internal error | `string` |


### `GET /api/workspaces/{id}/members` {#op-get-api-workspaces-id-members}
List members of a workspace

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | array of [`WorkspaceMember`](#schema-workspacemember) |
| `403` | not a member | `string` |
| `500` | internal error | `string` |


### `POST /api/workspaces/{id}/members` {#op-post-api-workspaces-id-members}
Add a member to a workspace
Looks up the user by email. Default role is "developer" if omitted.

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |


**Request body**

Content-Type: `application/json`

Schema: [`MemberAddRequest`](#schema-memberaddrequest)

```yaml
{
  email: string
  role?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `201` | Created | [`WorkspaceMember`](#schema-workspacemember) |
| `400` | bad request | `string` |
| `403` | owner or maintainer required | `string` |
| `404` | user not found | `string` |
| `500` | internal error | `string` |


### `PUT /api/workspaces/{id}/members/{userId}` {#op-put-api-workspaces-id-members-userid}
Change a member's role (owner only)

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |
| `userId` | `string` | yes | User id |


**Request body**

Content-Type: `application/json`

Schema: [`MemberRoleUpdateRequest`](#schema-memberroleupdaterequest)

```yaml
{
  role: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `204` | No Content | — |
| `400` | empty role | `string` |
| `403` | owner only | `string` |
| `500` | internal error | `string` |


### `DELETE /api/workspaces/{id}/members/{userId}` {#op-delete-api-workspaces-id-members-userid}
Remove a member (owner only)

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |
| `userId` | `string` | yes | User id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `204` | No Content | — |
| `403` | owner only | `string` |
| `500` | internal error | `string` |


## Schemas

### `LLMConfigResponse` {#schema-llmconfigresponse}

```yaml
{
  api_key?: string
  base_url?: string
  configured: boolean
  models?: []LLMModel
  updated_at?: string
}
```

### `LLMConfigUpsertRequest` {#schema-llmconfigupsertrequest}

```yaml
{
  api_key?: string
  base_url: string
  models: []LLMModel
}
```

### `LLMConfigUpsertResponse` {#schema-llmconfigupsertresponse}

```yaml
{
  ok: boolean
}
```

### `LLMModel` {#schema-llmmodel}

```yaml
{
  id: string
  name: string
}
```

### `LLMQuotaResponse` {#schema-llmquotaresponse}

```yaml
{
  default_max_rpd: integer
  today_request_count: integer
  workspace_quota?: any
}
```

### `LLMWorkspaceQuotaPart` {#schema-llmworkspacequotapart}

```yaml
{
  max_rpd?: integer
  updated_at: string
  workspace_id: string
}
```

### `MemberAddRequest` {#schema-memberaddrequest}

```yaml
{
  email: string
  role?: string
}
```

### `MemberRoleUpdateRequest` {#schema-memberroleupdaterequest}

```yaml
{
  role: string
}
```

### `Workspace` {#schema-workspace}

```yaml
{
  created_at: string
  id: string
  name: string
  updated_at: string
}
```

### `WorkspaceCreateRequest` {#schema-workspacecreaterequest}

```yaml
{
  name: string
}
```

### `WorkspaceMember` {#schema-workspacemember}

```yaml
{
  email: string
  picture?: string
  role: string
  user_id: string
}
```

### `WorkspaceQuotaResponse` {#schema-workspacequotaresponse}

```yaml
{
  current: integer
  max: integer
}
```

### `WorkspaceRenameRequest` {#schema-workspacerenamerequest}

```yaml
{
  name: string
}
```
