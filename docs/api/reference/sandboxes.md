# Sandboxes

Endpoints under the `Sandboxes` tag. Auto-generated from [`docs/api/openapi.yaml`](../openapi.yaml) — do not edit by hand.

> Run `make api-docs` after changing handler annotations to regenerate this file.

## Operations

| Method | Path | Summary |
|--------|------|---------|
| `GET` | [`/api/sandboxes/{id}`](#op-get-api-sandboxes-id) | Get a sandbox by id |
| `PATCH` | [`/api/sandboxes/{id}`](#op-patch-api-sandboxes-id) | Rename a sandbox |
| `DELETE` | [`/api/sandboxes/{id}`](#op-delete-api-sandboxes-id) | Delete a sandbox |
| `POST` | [`/api/sandboxes/{id}/pause`](#op-post-api-sandboxes-id-pause) | Pause a sandbox (cloud sandboxes only) |
| `POST` | [`/api/sandboxes/{id}/resume`](#op-post-api-sandboxes-id-resume) | Resume a paused sandbox (cloud sandboxes only) |
| `GET` | [`/api/sandboxes/{id}/usage`](#op-get-api-sandboxes-id-usage) | Get sandbox usage stats |
| `GET` | [`/api/workspaces/{wid}/sandboxes`](#op-get-api-workspaces-wid-sandboxes) | List sandboxes in a workspace |
| `POST` | [`/api/workspaces/{wid}/sandboxes`](#op-post-api-workspaces-wid-sandboxes) | Create a sandbox in a workspace |

### `GET /api/sandboxes/{id}` {#op-get-api-sandboxes-id}
Get a sandbox by id

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Sandbox id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`Sandbox`](#schema-sandbox) |
| `403` | not a member | `string` |
| `404` | sandbox not found | `string` |
| `500` | internal error | `string` |


### `PATCH /api/sandboxes/{id}` {#op-patch-api-sandboxes-id}
Rename a sandbox

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Sandbox id |


**Request body**

Content-Type: `application/json`

Schema: [`SandboxRenameRequest`](#schema-sandboxrenamerequest)

```yaml
{
  name: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`Sandbox`](#schema-sandbox) |
| `400` | name required | `string` |
| `403` | not a member | `string` |
| `404` | sandbox not found | `string` |
| `500` | internal error | `string` |


### `DELETE /api/sandboxes/{id}` {#op-delete-api-sandboxes-id}
Delete a sandbox

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Sandbox id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `204` | No Content | — |
| `403` | not a member | `string` |
| `404` | sandbox not found | `string` |
| `500` | internal error | `string` |


### `POST /api/sandboxes/{id}/pause` {#op-post-api-sandboxes-id-pause}
Pause a sandbox (cloud sandboxes only)
Initiates pause transition; returns {"status":"pausing"}. Final state lands asynchronously.

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Sandbox id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`SandboxLifecycleStatusResponse`](#schema-sandboxlifecyclestatusresponse) |
| `400` | local sandbox cannot be paused | `string` |
| `403` | not a member | `string` |
| `404` | sandbox not found | `string` |
| `409` | invalid state for pause | `string` |
| `500` | internal error | `string` |


### `POST /api/sandboxes/{id}/resume` {#op-post-api-sandboxes-id-resume}
Resume a paused sandbox (cloud sandboxes only)
Initiates resume transition; returns {"status":"resuming"}. Final state lands asynchronously.

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Sandbox id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`SandboxLifecycleStatusResponse`](#schema-sandboxlifecyclestatusresponse) |
| `400` | local sandbox cannot be resumed | `string` |
| `403` | not a member | `string` |
| `404` | sandbox not found | `string` |
| `409` | invalid state for resume | `string` |
| `500` | internal error | `string` |


### `GET /api/sandboxes/{id}/usage` {#op-get-api-sandboxes-id-usage}
Get sandbox usage stats

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Sandbox id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`SandboxUsage`](#schema-sandboxusage) |
| `403` | not a member | `string` |
| `404` | sandbox not found | `string` |
| `500` | internal error | `string` |


### `GET /api/workspaces/{wid}/sandboxes` {#op-get-api-workspaces-wid-sandboxes}
List sandboxes in a workspace

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `wid` | `string` | yes | Workspace id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | array of [`Sandbox`](#schema-sandbox) |
| `403` | not a member | `string` |
| `500` | internal error | `string` |


### `POST /api/workspaces/{wid}/sandboxes` {#op-post-api-workspaces-wid-sandboxes}
Create a sandbox in a workspace
Validates type / CPU / memory / idle_timeout / quota / budget. Returns 201 immediately with status="provisioning"; container starts asynchronously.

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `wid` | `string` | yes | Workspace id |


**Request body**

Content-Type: `application/json`

Schema: [`SandboxCreateRequest`](#schema-sandboxcreaterequest)

```yaml
{
  cpu?: integer
  idle_timeout?: integer
  memory?: integer
  metadata?: object
  name: string
  type?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `201` | Created | [`Sandbox`](#schema-sandbox) |
| `400` | validation error (type/cpu/memory/idle_timeout) | `string` |
| `403` | insufficient role / quota / budget | `string` |
| `500` | internal error | `string` |


## Schemas

### `AgentInfo` {#schema-agentinfo}

```yaml
{
  agent_version: string
  cpu_count_logical: integer
  cpu_model_name: string
  disk_free: integer
  disk_total: integer
  hostname: string
  kernel_arch: string
  memory_total: integer
  opencode_version: string
  os: string
  platform: string
  platform_version: string
  updated_at: string
  workdir: string
}
```

### `IMBinding` {#schema-imbinding}

```yaml
{
  bot_id: string
  bound_at: string
  provider: string
  user_id?: string
}
```

### `Sandbox` {#schema-sandbox}

```yaml
{
  agent_info?: AgentInfo
  claudecode_url?: string
  cpu?: integer
  created_at: string
  custom_url?: string
  id: string
  idle_timeout?: integer
  im_bindings?: []IMBinding
  is_local: boolean
  jupyter_url?: string
  last_activity_at?: string
  last_heartbeat_at?: string
  memory?: integer
  metadata?: object
  name: string
  openclaw_url?: string
  opencode_url?: string
  paused_at?: string
  short_id?: string
  status: string
  type: string
  weixin_bindings?: []IMBinding
  workspace_id: string
}
```

### `SandboxCreateRequest` {#schema-sandboxcreaterequest}

```yaml
{
  cpu?: integer
  idle_timeout?: integer
  memory?: integer
  metadata?: object
  name: string
  type?: string
}
```

### `SandboxLifecycleStatusResponse` {#schema-sandboxlifecyclestatusresponse}

```yaml
{
  status: string
}
```

### `SandboxRenameRequest` {#schema-sandboxrenamerequest}

```yaml
{
  name: string
}
```

### `SandboxUsage` {#schema-sandboxusage}

```yaml
{
  since?: string
  usage: []SandboxUsageSummary
}
```

### `SandboxUsageSummary` {#schema-sandboxusagesummary}

```yaml
{
  cache_creation_input_tokens: integer
  cache_read_input_tokens: integer
  input_tokens: integer
  model: string
  output_tokens: integer
  provider: string
  request_count: integer
}
```
