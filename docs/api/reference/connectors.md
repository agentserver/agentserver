# Connectors

Endpoints under the `Connectors` tag. Auto-generated from [`docs/api/openapi.yaml`](../openapi.yaml) — do not edit by hand.

> Run `make api-docs` after changing handler annotations to regenerate this file.

## Operations

| Method | Path | Summary |
|--------|------|---------|
| `POST` | [`/api/connectors/envs/list`](#op-post-api-connectors-envs-list) | List connected executors for the calling workspace |
| `POST` | [`/api/connectors/envs/{name}/tool/call`](#op-post-api-connectors-envs-name-tool-call) | Call an MCP tool on a connected executor |
| `GET` | [`/api/connectors/processes/{sid}/output`](#op-get-api-connectors-processes-sid-output) | Poll a running process's stdout/stderr |
| `POST` | [`/api/connectors/processes/{sid}/stdin`](#op-post-api-connectors-processes-sid-stdin) | Write to a running process's stdin |
| `POST` | [`/api/connectors/processes/{sid}/terminate`](#op-post-api-connectors-processes-sid-terminate) | Terminate a running process |

### `POST /api/connectors/envs/list` {#op-post-api-connectors-envs-list}
List connected executors for the calling workspace

**Auth:** `BearerAuth`


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`sdk.ConnectorEnvsListResponse`](#schema-sdk-connectorenvslistresponse) |
| `401` | missing or invalid bearer token | [`sdk.ConnectorErrorResponse`](#schema-sdk-connectorerrorresponse) |
| `500` | registry_error | [`sdk.ConnectorErrorResponse`](#schema-sdk-connectorerrorresponse) |


### `POST /api/connectors/envs/{name}/tool/call` {#op-post-api-connectors-envs-name-tool-call}
Call an MCP tool on a connected executor

**Auth:** `BearerAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `name` | `string` | yes | Connected environment name (from /envs/list) |


**Request body**

Content-Type: `application/json`

Schema: [`sdk.ConnectorToolCallRequest`](#schema-sdk-connectortoolcallrequest)

```yaml
{
  arguments?: object
  tool?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`tools.MCPCallToolResult`](#schema-tools-mcpcalltoolresult) |
| `400` | bad_request \| unknown_tool \| bad_arguments | [`sdk.ConnectorErrorResponse`](#schema-sdk-connectorerrorresponse) |
| `401` | missing or invalid bearer token | [`sdk.ConnectorErrorResponse`](#schema-sdk-connectorerrorresponse) |
| `500` | workspace_init \| tool_error | [`sdk.ConnectorErrorResponse`](#schema-sdk-connectorerrorresponse) |


### `GET /api/connectors/processes/{sid}/output` {#op-get-api-connectors-processes-sid-output}
Poll a running process's stdout/stderr

**Auth:** `BearerAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `sid` | `string` | yes | Session id returned by exec_command |

**Query parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `since` | `integer` | no | Highest seq already seen; only newer chunks are returned |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`sdk.ConnectorOutputResponse`](#schema-sdk-connectoroutputresponse) |
| `401` | missing or invalid bearer token | [`sdk.ConnectorErrorResponse`](#schema-sdk-connectorerrorresponse) |
| `403` | session belongs to a different workspace | [`sdk.ConnectorErrorResponse`](#schema-sdk-connectorerrorresponse) |
| `404` | session_not_found | [`sdk.ConnectorErrorResponse`](#schema-sdk-connectorerrorresponse) |


### `POST /api/connectors/processes/{sid}/stdin` {#op-post-api-connectors-processes-sid-stdin}
Write to a running process's stdin

**Auth:** `BearerAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `sid` | `string` | yes | Session id returned by exec_command |


**Request body**

Content-Type: `application/json`

Schema: [`sdk.ConnectorStdinRequest`](#schema-sdk-connectorstdinrequest)

```yaml
{
  data_b64?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`sdk.ConnectorOKResponse`](#schema-sdk-connectorokresponse) |
| `400` | bad_request \| bad_base64 | [`sdk.ConnectorErrorResponse`](#schema-sdk-connectorerrorresponse) |
| `401` | missing or invalid bearer token | [`sdk.ConnectorErrorResponse`](#schema-sdk-connectorerrorresponse) |
| `403` | session belongs to a different workspace | [`sdk.ConnectorErrorResponse`](#schema-sdk-connectorerrorresponse) |
| `404` | session_not_found | [`sdk.ConnectorErrorResponse`](#schema-sdk-connectorerrorresponse) |


### `POST /api/connectors/processes/{sid}/terminate` {#op-post-api-connectors-processes-sid-terminate}
Terminate a running process

**Auth:** `BearerAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `sid` | `string` | yes | Session id returned by exec_command |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`sdk.ConnectorOKResponse`](#schema-sdk-connectorokresponse) |
| `401` | missing or invalid bearer token | [`sdk.ConnectorErrorResponse`](#schema-sdk-connectorerrorresponse) |
| `403` | session belongs to a different workspace | [`sdk.ConnectorErrorResponse`](#schema-sdk-connectorerrorresponse) |
| `404` | session_not_found | [`sdk.ConnectorErrorResponse`](#schema-sdk-connectorerrorresponse) |


## Schemas

### `sdk.ConnectorEnv` {#schema-sdk-connectorenv}

```yaml
{
  is_default?: boolean
  last_seen?: string
  name?: string
  tools?: []sdk.ConnectorTool
  type?: string
}
```

### `sdk.ConnectorEnvsListResponse` {#schema-sdk-connectorenvslistresponse}

```yaml
{
  envs?: []sdk.ConnectorEnv
}
```

### `sdk.ConnectorErrorBody` {#schema-sdk-connectorerrorbody}

```yaml
{
  code?: string
  message?: string
}
```

### `sdk.ConnectorErrorResponse` {#schema-sdk-connectorerrorresponse}

```yaml
{
  error?: sdk.ConnectorErrorBody
}
```

### `sdk.ConnectorOKResponse` {#schema-sdk-connectorokresponse}

```yaml
{
  ok?: boolean
}
```

### `sdk.ConnectorOutputChunk` {#schema-sdk-connectoroutputchunk}

```yaml
{
  data_b64?: string
  seq?: integer
  stream?: enum(stdout|stderr)
}
```

### `sdk.ConnectorOutputResponse` {#schema-sdk-connectoroutputresponse}

```yaml
{
  chunks?: []sdk.ConnectorOutputChunk
  exit_code?: integer
  lost_bytes?: integer
  session_alive?: boolean
  truncated?: boolean
}
```

### `sdk.ConnectorStdinRequest` {#schema-sdk-connectorstdinrequest}

```yaml
{
  data_b64?: string
}
```

### `sdk.ConnectorTool` {#schema-sdk-connectortool}

```yaml
{
  description?: string
  kind?: enum(core|custom)
  name?: string
}
```

### `sdk.ConnectorToolCallRequest` {#schema-sdk-connectortoolcallrequest}

```yaml
{
  arguments?: object
  tool?: string
}
```

### `tools.MCPCallToolResult` {#schema-tools-mcpcalltoolresult}

```yaml
{
  content?: []tools.MCPToolContent
  isError?: boolean
  structuredContent?: object
}
```

### `tools.MCPToolContent` {#schema-tools-mcptoolcontent}

```yaml
{
  text?: string
  type?: string
}
```
