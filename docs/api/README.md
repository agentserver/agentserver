# agentserver API

This directory holds the generated OpenAPI 3.0 spec for agentserver's
public REST API.

## Files

- `openapi.yaml` / `openapi.json` — the spec. Generated from swaggo
  annotations on Go handler functions, then upconverted from Swagger 2.0
  to OpenAPI 3.0 via `swagger2openapi`. Do not edit by hand — any
  manual edit is overwritten by the next `make openapi`.

## Regenerating

After changing a handler signature, request/response struct, or
swaggo annotation:

```bash
make openapi          # regenerate docs/api/openapi.{yaml,json}
git add docs/api/
git commit
```

CI runs `make openapi-check` and fails if the committed spec is
out of date.

## Viewing

Any OpenAPI 3.0 viewer works. Two zero-install options:

```bash
# Redoc, served at http://localhost:8080
npx @redocly/cli preview-docs docs/api/openapi.yaml

# SwaggerUI
npx swagger-ui-watcher docs/api/openapi.yaml
```

Or upload the file to https://editor.swagger.io/.

## Frontend codegen

The web app generates `web/src/lib/api-generated/schema.d.ts` from
this spec at build time (gitignored, never commit it):

```bash
cd web && pnpm openapi:gen
```

The generated types feed into `web/src/lib/apiClient.ts`, which is
what the rest of the frontend uses.

## Scope

This directory covers the **public REST CRUD** subset of agentserver's
API (Phase 1, see `docs/superpowers/specs/2026-05-21-openapi-organization-design.md`).
OAuth flow / Hydra proxy / OIDC SSO / WebSocket / SSE / `/api/internal/*`
are intentionally not in this spec — each has its own follow-up.

Currently annotated: **Auth** (5 endpoints). Remaining tags
(Workspaces, Sandboxes, IM Channels, Codex Tokens, Codex Browser
Sessions, Agent Discovery / Tasks / Mailbox, Misc) land in Phase 1.b,
one PR per tag.
