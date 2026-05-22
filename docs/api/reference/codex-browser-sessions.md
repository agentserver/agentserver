# Codex Browser Sessions

Endpoints under the `Codex Browser Sessions` tag. Auto-generated from [`docs/api/openapi.yaml`](../openapi.yaml) — do not edit by hand.

> Run `make api-docs` after changing handler annotations to regenerate this file.

## Operations

| Method | Path | Summary |
|--------|------|---------|
| `GET` | [`/api/workspaces/{wid}/browsers`](#op-get-api-workspaces-wid-browsers) | List Codex browser sessions for a workspace |

### `GET /api/workspaces/{wid}/browsers` {#op-get-api-workspaces-wid-browsers}
List Codex browser sessions for a workspace

**Auth:** `CookieAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `wid` | `string` | yes | Workspace id |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | array of [`CodexBrowserItem`](#schema-codexbrowseritem) |
| `401` | unauthorized | `string` |
| `403` | not a member | `string` |
| `500` | internal error | `string` |


## Schemas

### `CodexBrowserItem` {#schema-codexbrowseritem}

```yaml
{
  client_ip?: string
  client_ua?: string
  codex_version?: string
  connected_at?: string
  created_at: string
  disconnected_at?: string
  expires_at: string
  id: string
  is_online: boolean
  last_used_at?: string
  name: string
  os?: string
  workspace_id: string
}
```
