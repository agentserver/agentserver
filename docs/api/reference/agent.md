# Agent

Endpoints under the `Agent` tag. Auto-generated from [`docs/api/openapi.yaml`](../openapi.yaml) — do not edit by hand.

> Run `make api-docs` after changing handler annotations to regenerate this file.

## Operations

| Method | Path | Summary |
|--------|------|---------|
| `GET` | [`/api/agent/discovery/agents`](#op-get-api-agent-discovery-agents) | Discover agents in the calling agent's workspace (proxy_token auth) |
| `POST` | [`/api/agent/discovery/cards`](#op-post-api-agent-discovery-cards) | Register or update an agent capability card |
| `GET` | [`/api/agent/mailbox/inbox`](#op-get-api-agent-mailbox-inbox) | Read messages from the calling agent's inbox |
| `POST` | [`/api/agent/mailbox/send`](#op-post-api-agent-mailbox-send) | Send a message to another agent's mailbox |
| `POST` | [`/api/agent/register`](#op-post-api-agent-register) | Register an agent (obtain sandbox credentials) |
| `POST` | [`/api/agent/tasks`](#op-post-api-agent-tasks) | Create a delegated task (proxy_token auth) |
| `GET` | [`/api/agent/tasks/poll`](#op-get-api-agent-tasks-poll) | Poll for pending tasks (proxy_token auth) |
| `GET` | [`/api/agent/tasks/{id}`](#op-get-api-agent-tasks-id) | Get a task by ID (proxy_token auth) |
| `PUT` | [`/api/agent/tasks/{id}/status`](#op-put-api-agent-tasks-id-status) | Update task status (proxy_token auth) |
| `GET` | [`/api/agents/{sandboxId}`](#op-get-api-agents-sandboxid) | Get a single agent card by sandbox ID |
| `GET` | [`/api/tasks/{id}`](#op-get-api-tasks-id) | Get a task by ID |
| `POST` | [`/api/tasks/{id}/cancel`](#op-post-api-tasks-id-cancel) | Cancel a task |
| `GET` | [`/api/workspaces/{wid}/agents`](#op-get-api-workspaces-wid-agents) | List agent cards in a workspace |
| `GET` | [`/api/workspaces/{wid}/tasks`](#op-get-api-workspaces-wid-tasks) | List delegated tasks for a workspace |
| `POST` | [`/api/workspaces/{wid}/tasks`](#op-post-api-workspaces-wid-tasks) | Create a delegated task in a workspace |

### `GET /api/agent/discovery/agents` {#op-get-api-agent-discovery-agents}
Discover agents in the calling agent's workspace (proxy_token auth)

**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | array of [`AgentCardItem`](#schema-agentcarditem) |
| `401` | unauthorized | `string` |
| `500` | internal error | `string` |


### `POST /api/agent/discovery/cards` {#op-post-api-agent-discovery-cards}
Register or update an agent capability card

**Request body**

Content-Type: `application/json`

Schema: [`AgentCardRegisterRequest`](#schema-agentcardregisterrequest)

```yaml
{
  agent_type?: string
  card?: any
  description?: string
  display_name?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`AgentCardRegisterResponse`](#schema-agentcardregisterresponse) |
| `400` | bad request | `string` |
| `401` | unauthorized | `string` |
| `500` | internal error | `string` |


### `GET /api/agent/mailbox/inbox` {#op-get-api-agent-mailbox-inbox}
Read messages from the calling agent's inbox

**Query parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `limit` | `integer` | no | Max messages to return (default 10) |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | array of [`AgentMailboxMessage`](#schema-agentmailboxmessage) |
| `401` | unauthorized | `string` |
| `500` | internal error | `string` |


### `POST /api/agent/mailbox/send` {#op-post-api-agent-mailbox-send}
Send a message to another agent's mailbox

**Request body**

Content-Type: `application/json`

Schema: [`AgentMailboxSendRequest`](#schema-agentmailboxsendrequest)

```yaml
{
  msg_type?: string
  text: string
  to: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `201` | Created | [`AgentMailboxSendResponse`](#schema-agentmailboxsendresponse) |
| `400` | bad request | `string` |
| `401` | unauthorized | `string` |
| `403` | target not in same workspace | `string` |
| `404` | target agent not found | `string` |
| `500` | internal error | `string` |


### `POST /api/agent/register` {#op-post-api-agent-register}
Register an agent (obtain sandbox credentials)

**Request body**

Content-Type: `application/json`

Schema: [`AgentRegisterRequest`](#schema-agentregisterrequest)

```yaml
{
  name?: string
  type?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `201` | Created | [`AgentRegisterResponse`](#schema-agentregisterresponse) |
| `400` | bad request | `string` |
| `401` | unauthorized | `string` |
| `403` | no permission | `string` |
| `500` | internal error | `string` |


### `POST /api/agent/tasks` {#op-post-api-agent-tasks}
Create a delegated task (proxy_token auth)

**Request body**

Content-Type: `application/json`

Schema: [`AgentTaskCreateRequest`](#schema-agenttaskcreaterequest)

```yaml
{
  delegation_chain?: []string
  max_budget_usd?: number
  max_turns?: integer
  prompt: string
  requester_id?: string
  skill?: string
  system_context?: string
  target_id: string
  timeout_seconds?: integer
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `201` | Created | [`AgentTaskCreateResponse`](#schema-agenttaskcreateresponse) |
| `400` | bad request | `string` |
| `401` | unauthorized | `string` |
| `403` | forbidden | `string` |
| `404` | target agent not found | `string` |
| `500` | internal error | `string` |


### `GET /api/agent/tasks/poll` {#op-get-api-agent-tasks-poll}
Poll for pending tasks (proxy_token auth)

**Query parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `sandbox_id` | `string` | no | Sandbox ID (defaults to token's sandbox) |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | array of [`AgentTaskPollItem`](#schema-agenttaskpollitem) |
| `401` | unauthorized | `string` |
| `403` | forbidden | `string` |
| `500` | internal error | `string` |


### `GET /api/agent/tasks/{id}` {#op-get-api-agent-tasks-id}
Get a task by ID (proxy_token auth)

**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Task ID |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`AgentTaskDetail`](#schema-agenttaskdetail) |
| `401` | unauthorized | `string` |
| `404` | not found | `string` |
| `500` | internal error | `string` |


### `PUT /api/agent/tasks/{id}/status` {#op-put-api-agent-tasks-id-status}
Update task status (proxy_token auth)

**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Task ID |


**Request body**

Content-Type: `application/json`

Schema: [`AgentTaskStatusRequest`](#schema-agenttaskstatusrequest)

```yaml
{
  failure_reason?: string
  num_turns?: integer
  result?: any
  status: string
  total_cost_usd?: number
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | — |
| `400` | bad request | `string` |
| `401` | unauthorized | `string` |
| `500` | internal error | `string` |


### `GET /api/agents/{sandboxId}` {#op-get-api-agents-sandboxid}
Get a single agent card by sandbox ID

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `sandboxId` | `string` | yes | Sandbox ID |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`AgentCardItem`](#schema-agentcarditem) |
| `401` | unauthorized | `string` |
| `403` | not a member | `string` |
| `404` | not found | `string` |
| `500` | internal error | `string` |


### `GET /api/tasks/{id}` {#op-get-api-tasks-id}
Get a task by ID

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Task ID |

**Query parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `include_output` | `boolean` | no | Include output events |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`AgentTaskDetail`](#schema-agenttaskdetail) |
| `401` | unauthorized | `string` |
| `404` | not found | `string` |
| `500` | internal error | `string` |


### `POST /api/tasks/{id}/cancel` {#op-post-api-tasks-id-cancel}
Cancel a task

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Task ID |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`AgentTaskCancelResponse`](#schema-agenttaskcancelresponse) |
| `401` | unauthorized | `string` |
| `404` | not found | `string` |
| `409` | task already finished | `string` |
| `500` | internal error | `string` |


### `GET /api/workspaces/{wid}/agents` {#op-get-api-workspaces-wid-agents}
List agent cards in a workspace

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `wid` | `string` | yes | Workspace ID |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | array of [`AgentCardItem`](#schema-agentcarditem) |
| `401` | unauthorized | `string` |
| `403` | not a member | `string` |
| `500` | internal error | `string` |


### `GET /api/workspaces/{wid}/tasks` {#op-get-api-workspaces-wid-tasks}
List delegated tasks for a workspace

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `wid` | `string` | yes | Workspace ID |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | array of [`AgentTaskItem`](#schema-agenttaskitem) |
| `401` | unauthorized | `string` |
| `403` | not a member | `string` |
| `500` | internal error | `string` |


### `POST /api/workspaces/{wid}/tasks` {#op-post-api-workspaces-wid-tasks}
Create a delegated task in a workspace

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `wid` | `string` | yes | Workspace ID |


**Request body**

Content-Type: `application/json`

Schema: [`AgentTaskCreateRequest`](#schema-agenttaskcreaterequest)

```yaml
{
  delegation_chain?: []string
  max_budget_usd?: number
  max_turns?: integer
  prompt: string
  requester_id?: string
  skill?: string
  system_context?: string
  target_id: string
  timeout_seconds?: integer
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `201` | Created | [`AgentTaskCreateResponse`](#schema-agenttaskcreateresponse) |
| `400` | bad request | `string` |
| `401` | unauthorized | `string` |
| `403` | forbidden | `string` |
| `404` | target agent not found | `string` |
| `500` | internal error | `string` |


## Schemas

### `AgentCardItem` {#schema-agentcarditem}

```yaml
{
  agent_id: string
  agent_type: string
  card: any
  description?: string
  display_name: string
  status: string
  version: integer
}
```

### `AgentCardRegisterRequest` {#schema-agentcardregisterrequest}

```yaml
{
  agent_type?: string
  card?: any
  description?: string
  display_name?: string
}
```

### `AgentCardRegisterResponse` {#schema-agentcardregisterresponse}

```yaml
{
  status: string
}
```

### `AgentMailboxMessage` {#schema-agentmailboxmessage}

```yaml
{
  created_at: string
  from: string
  id: string
  msg_type: string
  text: string
}
```

### `AgentMailboxSendRequest` {#schema-agentmailboxsendrequest}

```yaml
{
  msg_type?: string
  text: string
  to: string
}
```

### `AgentMailboxSendResponse` {#schema-agentmailboxsendresponse}

```yaml
{
  message_id: string
  status: string
}
```

### `AgentRegisterRequest` {#schema-agentregisterrequest}

```yaml
{
  name?: string
  type?: string
}
```

### `AgentRegisterResponse` {#schema-agentregisterresponse}

```yaml
{
  proxy_token: string
  sandbox_id: string
  short_id: string
  tunnel_token: string
  workspace_id: string
}
```

### `AgentTaskCancelResponse` {#schema-agenttaskcancelresponse}

```yaml
{
  status: string
}
```

### `AgentTaskCreateRequest` {#schema-agenttaskcreaterequest}

```yaml
{
  delegation_chain?: []string
  max_budget_usd?: number
  max_turns?: integer
  prompt: string
  requester_id?: string
  skill?: string
  system_context?: string
  target_id: string
  timeout_seconds?: integer
}
```

### `AgentTaskCreateResponse` {#schema-agenttaskcreateresponse}

```yaml
{
  session_id?: string
  status: string
  task_id: string
}
```

### `AgentTaskDetail` {#schema-agenttaskdetail}

```yaml
{
  completed_at?: string
  created_at: string
  failure_reason?: string
  num_turns?: integer
  prompt: string
  requester_id?: string
  result?: any
  session_id?: string
  skill?: string
  status: string
  target_id: string
  task_id: string
  total_cost_usd?: number
  workspace_id: string
}
```

### `AgentTaskItem` {#schema-agenttaskitem}

```yaml
{
  completed_at?: string
  created_at: string
  num_turns?: integer
  prompt: string
  requester_id?: string
  skill?: string
  status: string
  target_id: string
  task_id: string
  total_cost_usd?: number
}
```

### `AgentTaskPollItem` {#schema-agenttaskpollitem}

```yaml
{
  max_budget_usd?: number
  max_turns?: integer
  prompt: string
  session_id?: string
  system_context?: string
  task_id: string
}
```

### `AgentTaskStatusRequest` {#schema-agenttaskstatusrequest}

```yaml
{
  failure_reason?: string
  num_turns?: integer
  result?: any
  status: string
  total_cost_usd?: number
}
```
