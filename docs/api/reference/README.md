# agentserver API — Developer Reference

Spec version: `0.1.0`

This reference covers the **external developer surface** of agentserver: the endpoints an app or custom agent integrator will use. Admin and internal endpoints are intentionally excluded — see the raw [`openapi.yaml`](../openapi.yaml) for the full surface.

## Conventions

- **Base URL.** All paths are relative to your agentserver host, e.g. `https://agent.example.com`.
- **Auth schemes.** Three schemes are used across the API:
  - `CookieAuth` — browser session cookie set by `POST /api/auth/login` or the OIDC callbacks.
  - `BearerAuth` — `Authorization: Bearer <token>` with one of: OAuth access token (Device Flow), `proxy_token` returned from `POST /api/agent/register`, or a workspace API key (`wak_*`).
  - The auth column on each endpoint shows what is accepted; many endpoints accept either cookie or bearer.
- **Errors.** Non-2xx responses are plain JSON strings unless documented otherwise. The status code is the source of truth — `400` validation, `401` not authenticated, `403` not authorized, `404` not found, `409` conflict, `500` unexpected.
- **IDs.** UUIDs are returned as canonical 36-char hex with dashes. Sandbox `short_id`s are 16 chars, used in proxy subdomains.
- **Timestamps.** ISO-8601 UTC (RFC 3339).

## Sections

| Tag | Endpoints | Page |
|-----|-----------|------|
| Auth | 5 | [`auth.md`](auth.md) |
| Workspaces | 14 | [`workspaces.md`](workspaces.md) |
| Workspace API Keys | 4 | [`workspace-api-keys.md`](workspace-api-keys.md) |
| Sandboxes | 8 | [`sandboxes.md`](sandboxes.md) |
| Agent | 15 | [`agent.md`](agent.md) |
| Codex Tokens | 3 | [`codex-tokens.md`](codex-tokens.md) |
| Codex Browser Sessions | 1 | [`codex-browser-sessions.md`](codex-browser-sessions.md) |
| IM Channels | 9 | [`im-channels.md`](im-channels.md) |

## Related docs

- [`../../developer/quickstart.md`](../../developer/quickstart.md) — build a custom agent in 5 minutes.
- [`../../developer/protocol.md`](../../developer/protocol.md) — full custom-agent tunnel protocol (WebSocket + yamux).
- [`../mobile-integration.md`](../mobile-integration.md) — mobile/IM integration notes.
- [`../openapi.yaml`](../openapi.yaml) — machine-readable source of truth.
