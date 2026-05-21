# Phase 1.b — Misc tag implementation plan

**Date:** 2026-05-22
**Branch:** `feat/openapi-phase-1b-misc`
**Stacks on:** PR #158 (Agent tag)

## Scope

This is the final Phase 1.b PR — the Misc catchall for all public REST
endpoints not covered by the preceding 7 PRs (Auth, Workspaces, Sandboxes,
IM Channels, Codex Tokens, Codex Browser Sessions, Agent).

## IN-scope endpoints

### Credential Bindings (6)
| Method | Path | Handler |
|--------|------|---------|
| GET    | /api/workspaces/{id}/credentials/{kind} | handleListCredentialBindings |
| POST   | /api/workspaces/{id}/credentials/{kind} | handleCreateCredentialBinding |
| PATCH  | /api/workspaces/{id}/credentials/{kind}/{bindingId} | handlePatchCredentialBinding |
| DELETE | /api/workspaces/{id}/credentials/{kind}/{bindingId} | handleDeleteCredentialBinding |
| POST   | /api/workspaces/{id}/credentials/{kind}/{bindingId}/set-default | handleSetDefaultCredentialBinding |
| POST   | /api/workspaces/{id}/credentials/{kind}/{bindingId}/device-complete | handleDeviceCodeComplete |

### Operations Log (1)
| Method | Path | Handler |
|--------|------|---------|
| GET    | /api/workspaces/{id}/operations | getWorkspaceOperations |

### Workspace Defaults (1)
| Method | Path | Handler |
|--------|------|---------|
| GET    | /api/workspaces/{wid}/defaults | handleGetWorkspaceDefaults |

### LLM Traces (4)
| Method | Path | Handler |
|--------|------|---------|
| GET    | /api/sandboxes/{id}/traces | handleSandboxTraces |
| GET    | /api/sandboxes/{id}/traces/{traceId} | handleTraceDetail |
| GET    | /api/workspaces/{wid}/traces | handleWorkspaceTraces |
| GET    | /api/workspaces/{wid}/traces/{traceId} | handleWorkspaceTraceDetail |

### Codex Executors (3)
| Method | Path | Handler |
|--------|------|---------|
| POST   | /api/workspaces/{wid}/executors | handleRegisterExecutor |
| GET    | /api/workspaces/{wid}/executors | handleListExecutors |
| DELETE | /api/workspaces/{wid}/executors/{exe_id} | handleUnbindExecutor |

### ModelServer (3)
| Method | Path | Handler |
|--------|------|---------|
| GET    | /api/workspaces/{id}/modelserver/connect | handleModelserverConnect |
| DELETE | /api/workspaces/{id}/modelserver/disconnect | handleModelserverDisconnect |
| GET    | /api/workspaces/{id}/modelserver/status | handleModelserverStatus |

### Agent Interactions (1)
| Method | Path | Handler |
|--------|------|---------|
| GET    | /api/workspaces/{wid}/agent-interactions | handleListInteractions |

### Admin (11)
| Method | Path | Handler |
|--------|------|---------|
| GET    | /api/admin/users | handleAdminListUsers |
| GET    | /api/admin/workspaces | handleAdminListWorkspaces |
| GET    | /api/admin/sandboxes | handleAdminListSandboxes |
| PUT    | /api/admin/users/{id}/role | handleAdminUpdateUserRole |
| GET    | /api/admin/quotas/defaults | handleAdminGetQuotaDefaults |
| PUT    | /api/admin/quotas/defaults | handleAdminSetQuotaDefaults |
| GET    | /api/admin/users/{id}/quota | handleAdminGetUserQuota |
| PUT    | /api/admin/users/{id}/quota | handleAdminSetUserQuota |
| DELETE | /api/admin/users/{id}/quota | handleAdminDeleteUserQuota |
| GET    | /api/admin/workspaces/{id}/quota | handleAdminGetWorkspaceQuota |
| PUT    | /api/admin/workspaces/{id}/quota | handleAdminSetWorkspaceQuota |
| DELETE | /api/admin/workspaces/{id}/quota | handleAdminDeleteWorkspaceQuota |
| GET    | /api/admin/workspaces/{id}/llm-quota | handleAdminGetWorkspaceLLMQuota |
| PUT    | /api/admin/workspaces/{id}/llm-quota | handleAdminSetWorkspaceLLMQuota |
| DELETE | /api/admin/workspaces/{id}/llm-quota | handleAdminDeleteWorkspaceLLMQuota |

**Total IN-scope: 34 endpoints** (using two tags: `Misc` and `Admin`)

## OUT of scope (explicitly excluded)
- `/api/oauth2/*` — Hydra login/consent/device (Phase 2)
- `/api/auth/oidc/*` — OIDC flows (Phase 2)
- `GET /api/auth/modelserver/callback` — OAuth redirect, browser-facing, Phase 2
- `/healthz` — operational probe, not REST CRUD
- All `/internal/*` and `/api/internal/*` — server-internal auth
- All WebSocket/SSE/proxy passthrough routes

## Tasks

1. **DTOs** — Add types under `// --- Misc ---` and `// --- Admin ---` in
   `api_types.go`. Inline structs in credential_bindings.go, operations.go,
   admin.go, agent_interactions.go need named counterparts.

2. **Annotate handlers** — Add `@Tags Misc` (or `@Tags Admin`) + full swag
   annotations to all 34 handlers. Regen spec with `make swag`, verify no
   `server.` prefix drift.

3. **Frontend migration** — Check `web/src/` for local types covering
   credential bindings, operations, executors, modelserver, admin; alias to
   `components['schemas']` via the generated client.

4. **Verify + PR** — `make swag && go build ./...`, push, open PR titled
   `feat(openapi): Phase 1.b — Misc tag (34 endpoints)` stacked on #158.
   PR body notes this is the FINAL Phase 1.b PR completing the 8-PR stack
   (#152–#159).
