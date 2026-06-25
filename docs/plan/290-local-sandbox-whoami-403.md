# Plan: `whoami` returns identity unconditionally + exposes runtime status

Spec: `docs/spec/290-local-sandbox-whoami-403.md`
Issue: https://github.com/agentserver/agentserver/issues/290 (see
`@yzs15`'s second comment for the corrected analysis).

## Decision summary

`GET /api/agent/whoami` stops gating on `sandboxes.status`. Once the
identity verification passes (proxy_token + non-null `user_id` +
membership JOIN), the handler always returns **200** with the existing
identity fields **plus** a new `sandbox_status` field carrying the
current `sandboxes.status` value verbatim. Callers (observer-server,
admin scripts, future SDK consumers) decide for themselves whether to
gate on the runtime state.

Three other behaviors are deliberately preserved unchanged:

- 401 paths (missing/malformed/unknown/wrong-type bearer).
- 403 paths for the two real identity failures
  (`legacy_null_user`, `membership_removed`).
- `Cache-Control: no-store` on every 200 response.

The DB query (`internal/db/agent_whoami.go`) already returns
`s.status` into `AgentWhoami.SandboxStatus` (line 51, line 66) — no
SQL change, no new column read, no migration.

## Code changes

All paths relative to repo root.

### 1. `internal/server/agent_whoami.go` — drop the status gate

- Delete `activeWhoamiSandboxStatus` (lines 24-26).
- In `handleAgentWhoami` (lines 39-76), remove the
  `!activeWhoamiSandboxStatus(who.SandboxStatus)` branch (lines 60-63).
  The remaining `who == nil` guard is dead code by construction (when
  `state == AgentWhoamiOK`, `who` is non-nil) but the cheapest defense
  is to keep a `who == nil` → 500 check; the spec already maps "I
  couldn't tell" to 500, so this is consistent.
- Pass `who.SandboxStatus` into the response (new field below).
- Update the swaggo annotations on the handler doc-comment:
  - Keep `@Success 200 {object} AgentWhoamiResponse`.
  - Keep `@Failure 401 {string} string "unauthorized"`.
  - Tighten `@Failure 403` description from generic "forbidden" to
    something like
    `"identity not valid for this token (missing user binding or workspace membership)"`
    so the OpenAPI surface tells callers exactly what 403 means now.
  - Keep `@Failure 500 {string} string "internal error"`.

### 2. `internal/server/api_types.go` — add the new field

In `AgentWhoamiResponse` (lines 329-339), append one field:

```go
SandboxStatus string `json:"sandbox_status" validate:"required" example:"running"`
```

Decisions:

- **Name**: `sandbox_status` mirrors the existing `sandbox_id` /
  `workspace_id` snake_case naming and avoids the ambiguous `status`.
- **`validate:"required"`**: included so the OpenAPI surface marks the
  field as always-present. The field is always populated by the
  handler (every 200 response now carries it), so `required` is
  accurate. Old SDK consumers decoding into the old struct silently
  ignore unknown fields — they are unaffected.
- **No `omitempty`**: we want the field present even when
  `SandboxStatus == ""` (which can happen for some legacy rows; that
  empty string is meaningful — it says "the row exists but has no
  status set" — and hiding it loses information).
- **Position**: at the end of the struct, after `Role`, so the
  generated OpenAPI diff is minimal (no field re-ordering churn).
- We do **not** add `is_local` or `last_heartbeat_at` in this PR.
  Rationale: keep the contract change minimal. `last_heartbeat_at` is
  seeded by `CreateLocalSandbox` at registration
  (`internal/db/sandboxes.go:284`) and then only refreshed by the
  tunnel keepalive at `internal/sandboxproxy/tunnel.go:102` —
  meaning a non-tunneled `is_local` row carries its registration
  timestamp forever and exposing it would advertise a misleading
  half-truth. A future PR can add liveness fields if needed; this
  one is just unblocking observer-server.

### 3. `internal/server/agent_whoami_test.go` — rewrite assertions

Existing tests and their new shape:

- **`TestStrictBearerToken`** (lines 70-95) — unchanged. Pure helper
  test, no whoami contract dependency.

- **`TestActiveWhoamiSandboxStatus`** (lines 97-108) — **delete** the
  whole function. The helper it tests is removed.

- **`TestAgentWhoami_HappyPath`** (lines 110-130) — extend: after
  decoding the response, assert
  `out.SandboxStatus == "running"` (matches the seeded value).

- **`TestAgentWhoami_UnauthorizedCases`** (lines 132-165) — unchanged.
  The 401 paths are preserved exactly.

- **`TestAgentWhoami_ForbiddenCases`** (lines 167-199) —
  **split** into two table-driven tests:
  - `TestAgentWhoami_ForbiddenIdentity` — keeps the two genuine
    identity-failure sub-cases:
    - `legacy_null_user` (seed with `withUser=false`) → 403, body
      `"forbidden\n"`.
    - `membership_removed` (seed normal, then delete the
      `workspace_members` row) → 403, body `"forbidden\n"`.
  - `TestAgentWhoami_RuntimeStatusReportedInBody` — the previously-403
    statuses now flip to 200, table-driven over the full set defined
    in `internal/sbxstore/state.go:5-12` (`creating`, `running`,
    `pausing`, `paused`, `resuming`, `offline`, `deleting`) **plus**
    the empty string `""` for completeness. For each seeded status,
    run **two variants**: `is_local=FALSE` (cloud) and `is_local=TRUE`
    (local). Both must return:
    - HTTP 200.
    - Decoded body with `out.SandboxStatus == <seeded>`.
    - All existing identity fields populated.

    The `is_local=TRUE, status='offline'` row is the spec's
    headline scenario (success criterion #1) and must be covered
    explicitly, not just implied by the table sweep.

- **`TestAgentWhoami_DisplayNameFallsBackToSandboxName`** (lines
  201-215) — extend: also assert `out.SandboxStatus == "running"`.

**Helper change** — `seedWhoamiSandbox` (lines 24-57) currently does
not expose `is_local`, so every seeded row is `is_local=FALSE`
(default per `internal/db/migrations/001_initial.sql:81`). Extend its
signature with a trailing `isLocal bool` parameter and pass it
through the `INSERT INTO sandboxes` statement (add `is_local` to the
column list at line 28 and to the value list at line 29). Update the
six existing call sites (lines 112, 134, 171, 181, 190, 203) to pass
`false` to preserve their current intent. New tests pass `true` or
`false` per case.

### 4. OpenAPI / docs regeneration

After the Go changes, run:

```bash
make openapi    # regenerates docs/api/openapi.{yaml,json}
make api-docs   # regenerates docs/api/reference/*.md from openapi.yaml
```

Both targets are wired to CI drift checks (`openapi-check`,
`api-docs-check` — `Makefile:101, 119`); committing the Go change
without re-running them fails CI. The diff in `docs/api/openapi.yaml`
should be a one-field addition to the `AgentWhoami` schema definition
plus the tightened 403 description on `/api/agent/whoami`. The diff in
`docs/api/reference/agent.md` (or whichever per-tag file the whoami
endpoint lands in) mirrors the same.

No other files in `docs/` need manual editing.

## Files touched

- **Modified**: `internal/server/agent_whoami.go` — drop the
  status-gate branch + helper; update swaggo `@Failure 403` text;
  pass `SandboxStatus` into the response.
- **Modified**: `internal/server/api_types.go` — add
  `SandboxStatus` field to `AgentWhoamiResponse`.
- **Modified**: `internal/server/agent_whoami_test.go` — delete
  `TestActiveWhoamiSandboxStatus`; extend happy-path / display-name
  tests; split forbidden tests as described.
- **Regenerated** (via `make openapi` + `make api-docs`):
  `docs/api/openapi.yaml`, `docs/api/openapi.json`,
  `docs/api/reference/*.md` (one file at most).
- **No new file**. No migration. No agentsdk SDK change in
  `pkg/agentsdk/` (consumers there don't deserialize whoami
  responses).

## Reuse / pattern conformance

- Use existing helpers (`newWhoamiTestServer`, `seedWorkspaceMember`,
  `seedWhoamiSandbox`, `callWhoami`) — they already take all
  parameters the new tests need.
- Status constants come from `internal/sbxstore/state.go:5-12`. In
  test code we can keep them as literal strings (matches the existing
  test style in `agent_whoami_test.go:103, 168`) — no need to
  import `sbxstore` from this test file.
- The `Cache-Control: no-store` header pattern (existing line 66)
  stays; we just emit it before `json.NewEncoder(w).Encode(...)`
  exactly as today.
- swaggo annotation style matches the existing handler doc-comment
  format (no schema changes; just a string update).

## Verification

End-to-end, in order:

1. **Unit tests**:
   ```bash
   go test ./internal/server -run AgentWhoami -count=1 -v
   ```
   Every new subtest must pass; no existing assertion regresses.
   Then the broader package:
   ```bash
   go test ./internal/server -count=1
   ```

2. **Whole-module sweep** (matches CI):
   ```bash
   go test ./...
   ```
   No regressions anywhere.

3. **OpenAPI drift check** (matches CI):
   ```bash
   make openapi
   make api-docs
   make openapi-check
   make api-docs-check
   ```
   Both `-check` targets must report no drift after the regen step.

4. **Live reproduction against `https://agent.cs.ac.cn`** using the
   existing `/tmp/repro290/repro290` harness:
   ```bash
   /tmp/repro290/repro290              # register fresh non-tunneled sandbox
   # then EITHER manually flip status to 'offline' via SQL,
   # OR start serve-mcp and SIGTERM it (we already verified this
   # path writes status=offline via tunnel.go:127).
   /tmp/repro290/repro290 -resume <proxy_token> -count 3
   ```
   Before the fix: probes return 403.
   After the fix: probes return 200 with `"sandbox_status":"offline"`
   in the body.

5. **Negative tests** (no DB writes, just curl-level):
   - Bearer with unknown token → 401 (unchanged).
   - Bearer with the workspace-token type → 401 (unchanged).
   - Tunnel-token instead of proxy-token → 401 (unchanged).

## Risk and rollback

- **External callers** assuming a 403 for offline sandboxes now get
  200. observer-server is the only known caller we care about;
  reporter has already requested this behavior change. Other callers
  in the wild (admin scripts, monitoring) that interpret 403 as
  "sandbox unhealthy" are wrong by spec but may exist; the new
  `sandbox_status` field gives them the exact value to gate on, so
  they can migrate without ambiguity.
- **Rollback** is a single revert of the three Go files plus the
  regenerated docs.

## Out of scope (deliberately not in this PR)

- Changing `tunnel.go:127`'s behavior, the tunnel reconnect logic, or
  any other status writer.
- Adding `POST /api/agent/heartbeat` or any non-whoami liveness
  endpoint.
- Changing `commander.WSClient`'s 401-is-terminal handling in loom
  (separate repo, separate PR).
- Adding `is_local`, `last_heartbeat_at`, or other runtime
  introspection to the whoami response.
