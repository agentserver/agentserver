# IM Channels

Endpoints under the `IM Channels` tag. Auto-generated from [`docs/api/openapi.yaml`](../openapi.yaml) — do not edit by hand.

> Run `make api-docs` after changing handler annotations to regenerate this file.

## Operations

| Method | Path | Summary |
|--------|------|---------|
| `POST` | [`/api/sandboxes/{id}/im/bind`](#op-post-api-sandboxes-id-im-bind) | Bind a sandbox to an IM channel |
| `DELETE` | [`/api/sandboxes/{id}/im/bind`](#op-delete-api-sandboxes-id-im-bind) | Unbind a sandbox from its IM channel |
| `GET` | [`/api/workspaces/{id}/im/channels`](#op-get-api-workspaces-id-im-channels) | List IM channels in a workspace |
| `PATCH` | [`/api/workspaces/{id}/im/channels/{channelId}`](#op-patch-api-workspaces-id-im-channels-channelid) | Update IM channel settings |
| `DELETE` | [`/api/workspaces/{id}/im/channels/{channelId}`](#op-delete-api-workspaces-id-im-channels-channelid) | Delete an IM channel |
| `POST` | [`/api/workspaces/{id}/im/matrix/configure`](#op-post-api-workspaces-id-im-matrix-configure) | Bind a Matrix account to a workspace |
| `POST` | [`/api/workspaces/{id}/im/telegram/configure`](#op-post-api-workspaces-id-im-telegram-configure) | Bind a Telegram bot to a workspace |
| `POST` | [`/api/workspaces/{id}/im/weixin/qr-start`](#op-post-api-workspaces-id-im-weixin-qr-start) | Start WeChat QR-code bind for a workspace |
| `POST` | [`/api/workspaces/{id}/im/weixin/qr-wait`](#op-post-api-workspaces-id-im-weixin-qr-wait) | Long-poll WeChat QR-code scan for a workspace |

### `POST /api/sandboxes/{id}/im/bind` {#op-post-api-sandboxes-id-im-bind}
Bind a sandbox to an IM channel

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Sandbox id |


**Request body**

Content-Type: `application/json`

Schema: [`IMSandboxBindRequest`](#schema-imsandboxbindrequest)

```yaml
{
  channel_id: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`IMSandboxBindResponse`](#schema-imsandboxbindresponse) |
| `400` | bad request | `string` |
| `403` | not a member | `string` |
| `404` | sandbox or channel not found | `string` |
| `500` | internal error | `string` |


### `DELETE /api/sandboxes/{id}/im/bind` {#op-delete-api-sandboxes-id-im-bind}
Unbind a sandbox from its IM channel

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Sandbox id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`IMSandboxUnbindResponse`](#schema-imsandboxunbindresponse) |
| `403` | not a member | `string` |
| `404` | sandbox not found | `string` |
| `500` | internal error | `string` |


### `GET /api/workspaces/{id}/im/channels` {#op-get-api-workspaces-id-im-channels}
List IM channels in a workspace

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`IMChannelListResponse`](#schema-imchannellistresponse) |
| `403` | not a member | `string` |
| `500` | internal error | `string` |


### `PATCH /api/workspaces/{id}/im/channels/{channelId}` {#op-patch-api-workspaces-id-im-channels-channelid}
Update IM channel settings

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |
| `channelId` | `string` | yes | Channel id |


**Request body**

Content-Type: `application/json`

Schema: [`IMChannelPatchRequest`](#schema-imchannelpatchrequest)

```yaml
{
  require_mention?: boolean
  routing_mode?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`IMChannelPatchResponse`](#schema-imchannelpatchresponse) |
| `400` | bad request | `string` |
| `403` | not a member | `string` |
| `404` | channel not found | `string` |
| `500` | internal error | `string` |


### `DELETE /api/workspaces/{id}/im/channels/{channelId}` {#op-delete-api-workspaces-id-im-channels-channelid}
Delete an IM channel

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |
| `channelId` | `string` | yes | Channel id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `204` | No Content | — |
| `403` | not a member | `string` |
| `404` | channel not found | `string` |
| `500` | internal error | `string` |


### `POST /api/workspaces/{id}/im/matrix/configure` {#op-post-api-workspaces-id-im-matrix-configure}
Bind a Matrix account to a workspace

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |


**Request body**

Content-Type: `application/json`

Schema: [`IMMatrixConfigureRequest`](#schema-immatrixconfigurerequest)

```yaml
{
  access_token: string
  homeserver_url: string
  recovery_key?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`IMMatrixConfigureResponse`](#schema-immatrixconfigureresponse) |
| `400` | invalid credentials | `string` |
| `403` | not a member | `string` |
| `500` | internal error | `string` |


### `POST /api/workspaces/{id}/im/telegram/configure` {#op-post-api-workspaces-id-im-telegram-configure}
Bind a Telegram bot to a workspace

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |


**Request body**

Content-Type: `application/json`

Schema: [`IMTelegramConfigureRequest`](#schema-imtelegramconfigurerequest)

```yaml
{
  bot_token: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`IMTelegramConfigureResponse`](#schema-imtelegramconfigureresponse) |
| `400` | invalid bot token | `string` |
| `403` | not a member | `string` |
| `500` | internal error | `string` |


### `POST /api/workspaces/{id}/im/weixin/qr-start` {#op-post-api-workspaces-id-im-weixin-qr-start}
Start WeChat QR-code bind for a workspace
Returns a QR code URL the user scans in WeChat. Client should then long-poll qr-wait until the channel is bound.

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`IMWeixinQRStartResponse`](#schema-imweixinqrstartresponse) |
| `403` | not a member | `string` |
| `502` | upstream error | `string` |


### `POST /api/workspaces/{id}/im/weixin/qr-wait` {#op-post-api-workspaces-id-im-weixin-qr-wait}
Long-poll WeChat QR-code scan for a workspace
Polls for QR scan progress. Returns status "wait", "scaned", "confirmed", "expired", or other terminal states. On "confirmed", bot_id is set. On "expired", qrcode_url contains a refreshed code.

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Workspace id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`IMWeixinQRWaitResponse`](#schema-imweixinqrwaitresponse) |
| `400` | no active session | `string` |
| `403` | not a member | `string` |
| `502` | upstream error | `string` |


## Schemas

### `IMChannel` {#schema-imchannel}

```yaml
{
  bot_id: string
  bound_at: string
  id: string
  provider: string
  require_mention?: boolean
  routing_mode: string
  user_id?: string
  workspace_id: string
}
```

### `IMChannelListResponse` {#schema-imchannellistresponse}

```yaml
{
  channels: []IMChannel
}
```

### `IMChannelPatchRequest` {#schema-imchannelpatchrequest}

```yaml
{
  require_mention?: boolean
  routing_mode?: string
}
```

### `IMChannelPatchResponse` {#schema-imchannelpatchresponse}

```yaml
{
  status: string
}
```

### `IMMatrixConfigureRequest` {#schema-immatrixconfigurerequest}

```yaml
{
  access_token: string
  homeserver_url: string
  recovery_key?: string
}
```

### `IMMatrixConfigureResponse` {#schema-immatrixconfigureresponse}

```yaml
{
  bot_id: string
  connected: boolean
}
```

### `IMSandboxBindRequest` {#schema-imsandboxbindrequest}

```yaml
{
  channel_id: string
}
```

### `IMSandboxBindResponse` {#schema-imsandboxbindresponse}

```yaml
{
  status: string
}
```

### `IMSandboxUnbindResponse` {#schema-imsandboxunbindresponse}

```yaml
{
  status: string
}
```

### `IMTelegramConfigureRequest` {#schema-imtelegramconfigurerequest}

```yaml
{
  bot_token: string
}
```

### `IMTelegramConfigureResponse` {#schema-imtelegramconfigureresponse}

```yaml
{
  bot_id: string
  connected: boolean
}
```

### `IMWeixinQRStartResponse` {#schema-imweixinqrstartresponse}

```yaml
{
  message: string
  qrcode_url: string
}
```

### `IMWeixinQRWaitResponse` {#schema-imweixinqrwaitresponse}

```yaml
{
  bot_id?: string
  connected: boolean
  message?: string
  qrcode_url?: string
  status: string
  user_id?: string
}
```
