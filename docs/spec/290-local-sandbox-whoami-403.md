# Spec: `whoami` should not gate identity on sandbox runtime status

GitHub issue: https://github.com/agentserver/agentserver/issues/290 (see the
**second** comment by `@yzs15` for the corrected root-cause analysis; the
issue body itself is superseded).

## Problem (corrected after reporter's second comment)

`GET /api/agent/whoami` currently conflates two unrelated questions:

1. **Identity** — "is this `proxy_token` a valid identity for some
   (user, workspace, sandbox) tuple?"
2. **Runtime liveness** — "is this sandbox's yamux tunnel currently
   connected?"

Today, whoami returns HTTP 403 unless **both** are true
(`internal/server/agent_whoami.go:60`, `activeWhoamiSandboxStatus` only
permits `creating`/`running`). The only writer of `sandboxes.status =
'offline'` is `internal/sandboxproxy/tunnel.go:127`, which fires the
**moment** the active tunnel's context is cancelled — i.e. the moment the
agent process holding that tunnel exits or its WebSocket dies.

This makes the contract leak into normal operation. The deadlock the
reporter hits is reachable purely through the everyday `codex` lifecycle:

| T | Action | `sandboxes.status` | `whoami` |
|---|--------|--------------------|----------|
| 0 | `driver-agent register` | `running` | 200 |
| +Δ | `driver-agent serve-mcp` starts → `agentsdk.Client.Connect` opens tunnel → `tunnel.go:82` writes `running` | `running` | 200 |
| +Δ | `serve-mcp` exits cleanly (codex CLI session ends, EOF on stdin) → `tunnel.go:127` writes `offline` within ms | `offline` | **403** within ms |
| +Δ | `driver-agent serve-daemon` starts; observer-server validates the bearer via whoami → 403 → observer returns 401 to daemon → `commander.WSClient` treats 401 as non-retryable → daemon process exits within ~200 ms | `offline` | 403 |

The asymmetry the reporter originally described — "tunneled agents
self-heal, non-tunneled don't" — is in fact "agents whose long-lived
process owns the tunnel self-heal (slave-agent's poller maintains its own
tunnel forever); agents whose tunnel is owned by a short-lived child
process (driver-agent serve-mcp under codex) do not, because the
long-lived sibling (`serve-daemon`) cannot reach any agentserver endpoint
that depends on whoami once the child has exited."

`MarkStaleAgentCardsOffline` only touches `agent_cards.agent_status`
(`internal/db/agent_cards.go:103-115`) and is unrelated. `last_heartbeat_at`
staleness does **not** flip `sandboxes.status` anywhere in v0.69.9.

## Root cause

`whoami` is the only proxy_token-authed endpoint in the codebase that
checks `sandboxes.status`. All other proxy_token-authed endpoints —
`internal/server/agent_proxy_routes.go:21`,
`internal/server/agent_discovery.go:127`,
`internal/server/agent_mailbox.go:37, 119`,
`internal/server/agent_tasks.go:272, 346` — authenticate via
`GetSandboxByAnyToken` and do **not** gate on `sandboxes.status`. The
`status` check inside whoami is therefore not enforcing a system-wide
authorization rule; it is whoami-specific behavior that has become a
de-facto liveness probe for callers (observer-server in particular).

The runtime liveness signal — "is the tunnel up right now?" — is
legitimate state, but it is **not** what whoami's name or its existing
docstring promise (`internal/server/agent_whoami.go:28-38`: "returns the
identity represented by a sandbox proxy_token"). Callers that need
liveness should be able to ask for it; callers that need identity (e.g.
auth gates that decide "is this caller anyone real?") should be able to
get identity without runtime status getting in the way.

## Goal

Make `GET /api/agent/whoami` answer the identity question alone, and
expose the runtime sandbox status as additional data so callers that
want to gate on it still can:

1. Whoami returns **200** for any `proxy_token` that resolves to a valid
   `(user, workspace, sandbox)` tuple where the user has workspace
   membership — regardless of the sandbox's current `status` — and the
   response body includes the current `sandboxes.status` as a new
   field so observer-server (and any other caller) can decide for
   itself whether to treat `offline` as actionable.
2. Whoami continues to return:
   - **401** for unknown / malformed bearer (no proxy_token row found,
     wrong token_type, no sandbox_id binding).
   - **403** for the existing identity failures: NULL/empty `user_id`
     (`internal/db/agent_whoami.go:38-39`,
     `TestAgentWhoami_ForbiddenCases/legacy_null_user`) and JOIN-miss
     (membership removed, etc.;
     `TestAgentWhoami_ForbiddenCases/membership_removed`).
3. The fix changes the contract of one endpoint (whoami) and one
   response struct (`AgentWhoamiResponse`). No other server behavior
   changes. observer-server is free to upgrade at its own pace; until
   it does, it will simply stop receiving 403 for offline sandboxes
   and instead receive 200 with the offline status in the body — which
   it can ignore, log, or gate on as it wishes.

## Non-goals

- Not changing `sbxstore.StatusOffline`, `tunnel.go:127`, the tunnel
  reconnect path, or any other status writer. `sandboxes.status='offline'`
  is the correct signal when the active tunnel drops; we are not
  hiding it, just stopping whoami from interpreting it as a 403.
- Not changing observer-server (it's in a separate repo). The fix
  must work whether observer-server is ever updated or not.
- Not changing `commander.WSClient`'s 401-is-terminal behavior (also
  out-of-tree).
- Not changing other proxy_token-authed endpoints. They already don't
  gate on status; they stay as-is.
- Not changing `MarkStaleAgentCardsOffline` or `agent_cards.agent_status`.
- Not adding a `POST /api/agent/heartbeat` endpoint or otherwise giving
  agents a way to mark themselves running — that's the original
  (now-superseded) direction and is unnecessary once whoami stops
  gating on status.
- Not introducing a new schema migration; no new columns on
  `proxy_tokens` or `sandboxes`.

## Constraints

- **Identity gating must not weaken.** The current 401 paths
  (`TestAgentWhoami_UnauthorizedCases`: missing/malformed bearer, empty
  bearer, unknown token, workspace token, tunnel token) and the
  remaining 403 paths (`legacy_null_user`, `membership_removed`) must
  still trip the same status codes with the same response bodies.
- **Response contract change must be additive.** Existing fields
  (`UserID`, `WorkspaceID`, `WorkspaceName`, `SandboxID`, `ShortID`,
  `DisplayName`, `Role` in
  `internal/server/api_types.go:329-339`) must keep their names,
  types, JSON tags, and `validate:"required"` shape. The new field is
  added at the end of the struct.
- **No-cache header preserved.** `Cache-Control: no-store` on the
  response (`internal/server/agent_whoami.go:66`) must stay; status
  can change instantaneously when a tunnel connects/disconnects, so
  the response is never cacheable.
- **Swagger / generated docs must update.** The struct is annotated
  `// @name AgentWhoamiResponse`; the OpenAPI surface (consumed by
  `docs/` swagger output) must reflect the new field, including
  example value. The Failure 403 doc annotation
  (`internal/server/agent_whoami.go:36`) must drop "forbidden" from
  the "sandbox status not active" implication — only the
  identity-failure cases (`legacy_null_user`, `membership_removed`)
  remain.
- **Backward-compatible JSON.** A caller that decodes the response
  into the old struct (without the new field) continues to work — the
  new field is just ignored. A caller that decodes into the new
  struct against an old server (without the new field) sees the
  Go zero-value (`""`). If that caller chooses to enforce
  validation (the field is tagged `validate:"required"` because the
  new server always populates it), it will reject the old-server
  response — that's the desired signal that they're talking to a
  server that predates this change.

## Success criteria

1. **A token whose sandbox is `offline` returns 200**, with body
   containing the existing identity fields **plus** the new
   `sandbox_status: "offline"` field. Specifically: seed an
   `is_local=TRUE` sandbox with `status='offline'` and a valid
   `proxy_token` whose `user_id` is set; call whoami; observe 200 and
   `sandbox_status == "offline"` in the body. Same for
   `is_local=FALSE`.
2. **A token whose sandbox is in any other non-active state returns
   200 too**, with `sandbox_status` set to whatever the row says. The
   table-driven `TestAgentWhoami_ForbiddenCases` "status_*" sub-cases
   that previously asserted 403 for `paused`, `pausing`, `deleting`
   (and the additional `resuming` state defined in
   `internal/sbxstore/state.go:25` but not previously covered) now
   assert 200 with `sandbox_status` matching the seeded value.
   Rationale: identity is identity; runtime state is runtime state.
   The caller decides what to do with `paused`/`deleting`/etc.
   The helper `activeWhoamiSandboxStatus` and its dedicated test
   `TestActiveWhoamiSandboxStatus`
   (`internal/server/agent_whoami_test.go:97`) encode the old
   contract; both are removed when the handler stops gating on
   status.
3. **Identity failures still return 403.**
   `TestAgentWhoami_ForbiddenCases/legacy_null_user` and
   `/membership_removed` continue to return 403 with body
   `"forbidden\n"`, unchanged.
4. **Unauthorized failures still return 401.**
   `TestAgentWhoami_UnauthorizedCases` continues to pass unchanged.
5. **Happy-path responses include the new field.**
   `TestAgentWhoami_HappyPath` and
   `TestAgentWhoami_DisplayNameFallsBackToSandboxName` both observe a
   non-empty `sandbox_status` field whose value matches the seeded
   row (`"running"` in both cases).
6. **`Cache-Control: no-store` still present** on every 200 response.
7. **End-to-end deadlock is gone**: replaying the reporter's exact
   sequence (`register` → `serve-mcp` → SIGTERM → curl whoami) returns
   HTTP 200 with `sandbox_status: "offline"` instead of 403. Verified
   by the reproduction harness (the `repro290` script we already have
   on disk works; we just re-run it after the fix and observe the
   different result).
8. The OpenAPI output regenerated from the struct annotations shows
   the new field. Manual: run `make openapi` (which updates
   `docs/api/openapi.yaml` and `docs/api/openapi.json` —
   `Makefile:91`) and `make api-docs` (which updates the reference
   markdown — `Makefile:114`), then grep both for `sandbox_status`.

## Open design questions (resolved in the plan)

- Exact JSON name for the new field. Candidates: `sandbox_status`
  (mirrors existing `sandbox_id` style — recommended) vs `status`
  (shorter but collides with the more general "this whole response's
  status"). Plan picks one and applies it consistently.
- Whether to also expose `is_local` and `last_heartbeat_at` in the
  same response, to give callers richer liveness signal without
  another round trip. Plan picks (yes/no) and justifies.
- Where the `status` value should come from when the JOIN's
  `s.status` column is read: today `GetAgentWhoamiByProxyToken`
  already returns it as `out.SandboxStatus`
  (`internal/db/agent_whoami.go:51, 66`), so the wiring is trivial —
  the plan just confirms no new DB query is needed.
