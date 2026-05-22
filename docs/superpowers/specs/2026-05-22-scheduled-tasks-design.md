# Scheduled Tasks for codex-{app,exec}-gateway

**Status**: Design, awaiting implementation
**Author**: Claude (paired with @mryao)
**Date**: 2026-05-22

## Problem

Users want to schedule prompts that fire on a wall-clock — one-shot ("9pm tonight") or recurring ("weekdays at 9am") — without keeping their local codex running. When a scheduled prompt fires, the result must reach the user wherever they are, which today means every IM channel bound to their workspace (WeChat, Telegram, …).

agentserver has `agent_tasks` for immediate delegated execution, but no scheduling, no recurrence, no fire-and-broadcast. nanoclaw's `src/modules/scheduling/` solves the same problem for sandboxed Claude Code with a 6-tool MCP surface. We replicate that surface for codex on agentserver while adapting the storage and execution layers to our Postgres-first, gateway-spawned-codex world.

## Goals

1. Codex users create / list / pause / resume / update / cancel scheduled prompts via MCP, with the **exact tool names, arg names, and behaviors as nanoclaw**.
2. The same operations are reachable over REST for Web Console and CI scripts; REST JSON shape mirrors MCP camelCase 1:1.
3. At fire time, the server spawns a one-shot `codex exec` process (no dependency on the user's local codex being online) and broadcasts the result to **all** `workspace_im_channels` of the originating workspace.
4. Optional `script` pre-gate (nanoclaw's `{wakeAgent, data}` protocol) to avoid wasting API credits on frequent polling tasks.
5. Multiple codex-app-gateway replicas safe by default — no leader election.

## Non-goals

- Sub-second precision. ≥15 s tick is fine.
- Cross-workspace tasks; a series belongs to one workspace.
- Per-task target channel selection. Broadcast-to-all is sufficient per product decision (2026-05-22).
- Web Console UI in this spec — REST is enough; UI is a separate effort.
- IM-side `/schedule` slash commands. Out of scope (user did not select this entry surface).

## Approach: Centralized state, distributed dispatch

Three layers, each with a single responsibility:

| Layer | Where | Responsibility |
|---|---|---|
| **State** | agentserver-main Postgres | `scheduled_tasks` + `scheduled_task_runs` tables; REST API; internal lease/result API |
| **Dispatch + exec** | codex-app-gateway pod | `scheduler.Loop` goroutine; leases due rows; calls `supervisor.SpawnExec`; reports result |
| **Broadcast** | imbridgesvc | existing `/api/internal/imbridge/send` per `workspace_im_channels` row |

Rationale: the codex binary and per-spawn supervisor already live in app-gateway; workspace + IM-channel rows already live in agentserver Postgres; imbridge already knows how to push to providers. Each new piece slots into the layer that already owns the closest concern. Alternatives considered:

- **B: state in app-gateway's own DB** — diverges from PG-first norm; multi-replica state divergence; Web Console can't query alongside `workspaces`.
- **C: overload existing `agent_tasks`** — different lifecycle (no sandbox target, broadcast semantics), different polling model; tangles two concepts.

## Architecture

```
        ┌─────────── 本地 codex（用户机器）─────────────┐
        │  通过 env-mcp 看到的工具：                    │
        │   • schedule_task / cancel_task / pause /     │
        │     resume / update_task / list_tasks         │
        └──────────────────┬────────────────────────────┘
                           │ MCP (stdin/stdout)
                           ▼
   ┌────────── codex-app-gateway pod ──────────────────┐
   │  envmcp 子进程  ──(loopback + X-Loopback-Token)──▶ │
   │  internal_api.go  ─(/internal/scheduled-tasks/*)─▶ │
   │                                                    │
   │  ┌──── scheduler.Loop (goroutine in main) ───────┐ │
   │  │  每 N 秒：                                     │ │
   │  │   1. POST /api/internal/scheduled-tasks/lease │ │
   │  │      ↳ agentserver 用 FOR UPDATE SKIP LOCKED  │ │
   │  │   2. supervisor.SpawnExec(prompt, env, creds) │ │
   │  │   3. PATCH .../{id}/result  +  fan-out IM     │ │
   │  └────────────────────────────────────────────────┘ │
   └──┬─────────────────────────────────┬───────────────┘
      │ shared-secret HTTPS              │ shared-secret HTTPS
      ▼                                  ▼
 ┌─ agentserver-main ──────────┐   ┌─ imbridgesvc ──────────┐
 │  scheduled_tasks (Postgres) │   │ /api/internal/imbridge │
 │  +  /api/workspaces/{wid}/  │   │      /send             │
 │     scheduled-tasks (REST)  │   └────────────────────────┘
 │  +  /api/internal/          │
 │     scheduled-tasks/{lease, │
 │      result, list...}       │
 └─────────────────────────────┘
```

## Data Model

New migration `internal/db/migrations/029_scheduled_tasks.sql`:

```sql
CREATE TABLE scheduled_tasks (
    id                TEXT        PRIMARY KEY,                  -- "sch_" + uuid
    workspace_id      TEXT        NOT NULL
                                  REFERENCES workspaces(id) ON DELETE CASCADE,
    series_id         TEXT        NOT NULL,                     -- = id of the first occurrence
    created_by        TEXT,                                     -- user_id (NULL = system)
    creator_kind      TEXT        NOT NULL DEFAULT 'mcp'        -- 'mcp' | 'rest' | 'system'
                                  CHECK (creator_kind IN ('mcp','rest','system')),

    -- Payload (camelCase mirrors MCP/REST surface)
    prompt            TEXT        NOT NULL,
    script            TEXT,                                     -- optional bash, nanoclaw {wakeAgent,data} protocol

    -- Timing
    timezone          TEXT        NOT NULL DEFAULT 'UTC',       -- IANA name; per-task so multi-TZ users coexist
    recurrence        TEXT,                                     -- 5-field cron in `timezone`; NULL = one-shot
    process_after     TIMESTAMPTZ NOT NULL,                     -- next fire time, stored in UTC

    -- Lifecycle
    status            TEXT        NOT NULL DEFAULT 'pending'    -- pending|paused|running|completed|failed|cancelled
                                  CHECK (status IN ('pending','paused','running','completed','failed','cancelled')),
    tries             INT         NOT NULL DEFAULT 0,
    timeout_seconds   INT         NOT NULL DEFAULT 600,
    lease_until       TIMESTAMPTZ,                              -- non-NULL while a dispatcher holds it
    lease_owner       TEXT,                                     -- "<pod>/<pid>" for debugging

    last_run_id       TEXT,                                     -- → scheduled_task_runs.id
    last_error        TEXT,

    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Hot path: dispatcher pulls due rows. Partial index keeps it tiny even with many completed series.
CREATE INDEX idx_scheduled_tasks_due
    ON scheduled_tasks (process_after)
    WHERE status = 'pending';

CREATE INDEX idx_scheduled_tasks_workspace ON scheduled_tasks (workspace_id, created_at DESC);
CREATE INDEX idx_scheduled_tasks_series    ON scheduled_tasks (series_id);

-- One run per fire. Summary + transcript pointer; full transcript lives off-row.
CREATE TABLE scheduled_task_runs (
    id                TEXT        PRIMARY KEY,                  -- "run_" + uuid
    task_id           TEXT        NOT NULL REFERENCES scheduled_tasks(id) ON DELETE CASCADE,
    series_id         TEXT        NOT NULL,
    started_at        TIMESTAMPTZ NOT NULL,
    finished_at       TIMESTAMPTZ,
    duration_ms       BIGINT,
    exit_code         INT,
    status            TEXT        NOT NULL                       -- 'succeeded' | 'failed' | 'timeout' | 'skipped'
                                  CHECK (status IN ('succeeded','failed','timeout','skipped')),
    summary           TEXT,                                     -- ≤4KB; what gets shipped to IM
    transcript_uri    TEXT,                                     -- s3://… / file:// full transcript
    cost_usd          NUMERIC(10,4),
    num_turns         INT,
    broadcast_to      TEXT[]      NOT NULL DEFAULT '{}',        -- channel ids actually attempted
    broadcast_errors  JSONB                                     -- {channel_id: err_str, …}
);
CREATE INDEX idx_scheduled_task_runs_task ON scheduled_task_runs (task_id, started_at DESC);
```

Design notes:

- **One-shot and recurring share the table.** Recurring = `recurrence IS NOT NULL`. After fire, dispatcher writes a fresh row for the next occurrence (new `id`, same `series_id`), and marks the old row `completed` with `recurrence=NULL`. This matches nanoclaw's "clone forward, clear original" pattern but keeps history (no row reuse).
- **Lease over `assigned` status.** `UPDATE … SET lease_until=NOW()+interval, lease_owner=? WHERE … FOR UPDATE SKIP LOCKED`. Multi-replica safe; dead pods leak orphan rows for at most `LeaseSeconds`, then the next tick claims them.
- **Per-task `timezone`.** Multi-tenant platform → can't use a global TZ like nanoclaw does. MCP path defaults to the caller's `TZ` env (codex injects it into env-mcp; env-mcp reads `os.Getenv("TZ")` and forwards it as a `timezone` field in the POST to app-gateway loopback, which forwards it through to agentserver). REST path defaults to `UTC` when the field is omitted.
- **`timeout_seconds` is NOT exposed via MCP or REST in v1.** Column exists for an eventual admin-set policy override. Default 600 matches nanoclaw's container-side default. Exposing it later is purely additive.
- **No `target_channels`.** Broadcast-to-all is the single behavior (per product decision). If selective broadcast is ever needed it becomes an additive column + tool arg.
- **Operational columns (`creator_kind`, `created_by`, `lease_*`, `last_*`) are not exposed via MCP** — they exist for audit and dispatch, not the agent-facing surface. Keeping them off MCP is what lets us claim "1:1 with nanoclaw".

## MCP Tool Surface (must match nanoclaw exactly)

New package `internal/codexappgateway/envmcp/tools/scheduling.go`. Six tools, registered alongside the existing fixed tool list in `envmcp.Run`:

| Tool | Args (camelCase) | Returns |
|---|---|---|
| `schedule_task` | `prompt: string`, `processAfter: string`, `recurrence?: string`, `script?: string` | `{taskId, runsAt, recurrence?}` |
| `list_tasks` | `status?: "pending" \| "paused"` | One line per series; id field IS `series_id` |
| `cancel_task` | `taskId: string` (series id) | matches `(id = ? OR series_id = ?) AND status IN ('pending','paused')` |
| `pause_task` | `taskId: string` | pending → paused (no-op on running/completed) |
| `resume_task` | `taskId: string` | paused → pending (immediately due if `process_after < now()`) |
| `update_task` | `taskId: string`, any of `prompt? \| recurrence? \| processAfter? \| script?` | `""` clears `recurrence`/`script`; omitting leaves unchanged |

nanoclaw behaviors that we **must** replicate verbatim:

1. **Series-id is the public handle.** `schedule_task` returns `taskId = id = series_id` for the first occurrence. `list_tasks` returns one row per series (the live `pending`/`paused` one), with `id` field populated from `series_id`. All mutating ops match `(id = ? OR series_id = ?)`.
2. **`processAfter` parsing**: accepts UTC (`Z` / `+00:00`) **or** naive local timestamp interpreted in the task's `timezone`. Go-side `parseZonedToUtc(s, tz string) (time.Time, error)` mirrors nanoclaw's TS function.
3. **`recurrence` evaluated in `timezone`**: use `github.com/robfig/cron/v3` `cron.ParseStandard` + `cron.WithLocation(loc)` when computing the next fire.
4. **`update_task` clear semantics**: `recurrence: ""` / `script: ""` → DB NULL. Field absent → leave as-is. Empty `update_task` (only `taskId`) returns error "at least one field to update is required".
5. **Sync-to-store**: tool handlers POST to loopback `/internal/scheduled-tasks/*` and wait for the agentserver-main write to land before returning. The user sees the real state.
6. **Instructions doc**: ship `scheduling.instructions.md` next to the tool, copying nanoclaw's content on script usage / API credit cost / when not to use scripts. The doc is bundled into the env-mcp tool registration so codex includes it in tool documentation.

Tool-list golden file (`testdata/scheduling.golden.json`) locks the MCP tool schemas; any drift fails CI.

### MCP → server transport

env-mcp child is short-lived and stateless. It forwards each tool call:

```
env-mcp tool handler
  ── POST http://127.0.0.1:<gw>/internal/scheduled-tasks/<action>
     Header: X-Loopback-Token: <per-spawn token>     # already maps to workspace_id
     Body:   { ...tool args, normalized... }
app-gateway internal_api.go (new handler)
  ── POST https://agentserver.<svc>:8080/api/internal/scheduled-tasks/<action>
     Header: X-Internal-Secret: <AGENTSERVER_INTERNAL_SECRET>
     Body:   { workspace_id, ... }
```

This mirrors the existing `/internal/connected` plumbing.

## REST API (mirrors MCP 1:1)

Implemented in `internal/server/scheduled_tasks.go`, mounted under `/api/workspaces/{wid}/scheduled-tasks`. Auth via existing `requireWorkspaceMember`. JSON field names match MCP camelCase exactly (`prompt`, `processAfter`, `recurrence`, `script`, `timezone`, `taskId`).

| Verb | Path | Maps to |
|---|---|---|
| POST | `/api/workspaces/{wid}/scheduled-tasks` | `schedule_task` |
| GET | `/api/workspaces/{wid}/scheduled-tasks?status=pending\|paused` | `list_tasks` |
| GET | `/api/workspaces/{wid}/scheduled-tasks/{seriesId}` | Single series detail + recent runs |
| PATCH | `/api/workspaces/{wid}/scheduled-tasks/{seriesId}` | `update_task` |
| POST | `/api/workspaces/{wid}/scheduled-tasks/{seriesId}/cancel` | `cancel_task` |
| POST | `/api/workspaces/{wid}/scheduled-tasks/{seriesId}/pause` | `pause_task` |
| POST | `/api/workspaces/{wid}/scheduled-tasks/{seriesId}/resume` | `resume_task` |
| GET | `/api/workspaces/{wid}/scheduled-tasks/{seriesId}/runs` | Run history |

Two internal endpoints (shared-secret + loopback or peer-IP allowlist), used only by the dispatcher:

| Verb | Path | Purpose |
|---|---|---|
| POST | `/api/internal/scheduled-tasks/lease` | Body `{limit, leaseSeconds, owner}` → array of leased tasks |
| POST | `/api/internal/scheduled-tasks/result` | Body `{taskId, runId, status, summary, transcriptURI, costUsd, numTurns, broadcastTo, broadcastErrors}` |

## Scheduler Loop (codex-app-gateway)

New package `internal/codexappgateway/scheduler/`:

```
scheduler/
  loop.go         // Loop.Run(ctx) — ticker + lease + dispatch fan-out
  dispatcher.go   // dispatcher.Fire(task) — script gate → spawn codex → collect → POST result + broadcast
  spawn.go        // supervisor adapter: SpawnExec(prompt, env, timeout) → (exitCode, transcript, costUSD, numTurns)
  script.go       // runPreScript(script, env) → (wakeAgent bool, dataJSON []byte, err)
  broadcast.go    // fanout to imbridgesvc /api/internal/imbridge/send
```

Wired in `cmd/codex-app-gateway/...` startup (analog to `cmd/serve.go`'s `StartRetentionLoop`):

```go
schedCtx, schedCancel := context.WithCancel(context.Background())
sched := scheduler.New(scheduler.Config{
    AgentserverBase:   cfg.AgentserverBaseURL,
    InternalSecret:    cfg.AgentserverInternalSecret,
    ImbridgeBase:      cfg.ImbridgeBaseURL,
    ImbridgeSecret:    cfg.ImbridgeInternalSecret,
    Supervisor:        sup,
    TickInterval:      15 * time.Second,
    LeaseSeconds:      30 * 60,    // > 2× longest task timeout
    Concurrency:       4,          // max parallel fires per pod
    PodID:             os.Getenv("POD_NAME"),
})
go sched.Run(schedCtx)
```

### Tick

```go
func (l *Loop) tick(ctx context.Context) {
    batch, err := l.agent.LeaseDue(ctx, LeaseRequest{
        Limit:        l.cfg.Concurrency - int(l.inflight.Load()),
        LeaseSeconds: l.cfg.LeaseSeconds,
        Owner:        l.cfg.PodID + "/" + strconv.Itoa(os.Getpid()),
    })
    if err != nil { log.Printf("lease: %v", err); return }
    for _, t := range batch {
        l.inflight.Add(1)
        go func(t Task) {
            defer l.inflight.Add(-1)
            l.dispatcher.Fire(ctx, t)
        }(t)
    }
}
```

### LeaseDue SQL (agentserver-main)

```sql
WITH due AS (
  SELECT id FROM scheduled_tasks
   WHERE status = 'pending'
     AND process_after <= NOW()
     AND (lease_until IS NULL OR lease_until < NOW())
   ORDER BY process_after ASC
   LIMIT $1
   FOR UPDATE SKIP LOCKED
)
UPDATE scheduled_tasks t
   SET lease_until = NOW() + ($2 || ' seconds')::interval,
       lease_owner = $3,
       updated_at  = NOW()
  FROM due
 WHERE t.id = due.id
RETURNING t.*;
```

`FOR UPDATE SKIP LOCKED` makes any number of app-gateway replicas safe; lease expiry rescues tasks from dead pods.

## Fire pipeline

For each leased task, `dispatcher.Fire`:

```
1. INSERT scheduled_task_runs (run_id, task_id, started_at, status='running')

2. if task.script != NULL:
     runPreScript(script) → {wakeAgent, data}
       ├─ wakeAgent == false:
       │    run.status = 'skipped', run.summary = "script gated (wakeAgent=false)"
       │    → go to step 4 (no broadcast, not a failure)
       ├─ error / non-zero exit / parse failure:
       │    run.status = 'failed', run.summary = stderr (truncated)
       │    → go to step 4 (broadcast failure summary)
       └─ wakeAgent == true:
            prepend data as "## script_data:\n{json}\n\n" to prompt

3. supervisor.SpawnExec(prompt, env, timeout=task.timeout_seconds)
     command:  codex exec --json -
     stdin:    final prompt
     env:      CODEX_HOME=<per-spawn>, ANTHROPIC_API_KEY=<workspace cred>, TZ=<task.timezone>
     timeout:  context.WithTimeout(timeout_seconds); SIGTERM → 2s → SIGKILL
     capture:  parse stdout JSON stream → accumulate transcript;
               last "assistant" event message becomes summary candidate;
               token-usage events populate cost_usd / num_turns

4. PATCH /api/internal/scheduled-tasks/result
     server:  - updates runs row (finished_at, status, summary, cost_usd, ...)
              - increments task.tries, sets task.last_run_id, last_error
              - if recurrence != NULL: advance() (see below)
              - else (one-shot):
                  - run.status = 'succeeded' or 'skipped' → task.status = 'completed'
                  - run.status = 'failed' or 'timeout'   → task.status = 'failed'
                (a script-gated one-shot is treated as "done for this firing", and since there
                 is no next firing, the task is complete. No retry.)
              - always clears lease_until, lease_owner

5. Broadcast: for each row in workspace_im_channels WHERE workspace_id = task.workspace_id:
     POST imbridgesvc /api/internal/imbridge/send
       { workspace_id, provider, bot_id, user_id, text: renderSummary(run, task) }
     renderSummary shape (text/plain, IM-friendly):
       [Scheduled task fired — sch_abcd1234]
       at: 2026-05-22 09:00 CST  (took 12.4s)
       <run.summary truncated to 1500 chars>
       — full transcript: <transcript_uri or "(not stored)">
     Per-channel errors collected → run.broadcast_errors JSONB.
     One channel failing does NOT abort fan-out to others.
     A run with status='skipped' SKIPS broadcast entirely (no IM noise from gated runs).
```

### `advance` (recurring → next occurrence)

In the agentserver `/result` handler, inside the same transaction that finalizes the run:

```go
if oldTask.Recurrence != "" {
    loc, err := time.LoadLocation(oldTask.Timezone)
    if err != nil {
        // unknown TZ — fall back to UTC and record last_error
        loc = time.UTC
    }
    sched, err := cron.ParseStandard(oldTask.Recurrence)
    if err != nil {
        // bad cron: treat as one-shot completion + record last_error
    } else {
        next := sched.Next(time.Now().In(loc)).UTC()
        newID := "sch_" + uuid.NewString()
        db.Exec(`INSERT INTO scheduled_tasks
                 (id, workspace_id, series_id, created_by, creator_kind,
                  prompt, script, recurrence, timezone, process_after,
                  status, timeout_seconds, created_at, updated_at)
                 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'pending',$11,NOW(),NOW())`,
            newID, oldTask.WorkspaceID, oldTask.SeriesID,
            oldTask.CreatedBy, oldTask.CreatorKind,
            oldTask.Prompt, oldTask.Script, oldTask.Recurrence,
            oldTask.Timezone, next, oldTask.TimeoutSeconds)
    }
    db.Exec(`UPDATE scheduled_tasks
                SET status='completed', recurrence=NULL, lease_until=NULL, lease_owner=NULL
              WHERE id=$1`, oldTask.ID)
}
```

### `script` sub-protocol (1:1 with nanoclaw)

```go
func runPreScript(ctx context.Context, script string, env []string) (wake bool, data json.RawMessage, err error) {
    sctx, cancel := context.WithTimeout(ctx, 60*time.Second) // hard cap
    defer cancel()
    cmd := exec.CommandContext(sctx, "bash", "-c", script)
    cmd.Env = env
    out, err := cmd.Output()
    if err != nil { return false, nil, err }
    var parsed struct {
        WakeAgent bool            `json:"wakeAgent"`
        Data      json.RawMessage `json:"data"`
    }
    if err := json.Unmarshal(bytes.TrimSpace(out), &parsed); err != nil {
        return false, nil, fmt.Errorf("script must print JSON {wakeAgent,data}: %w", err)
    }
    return parsed.WakeAgent, parsed.Data, nil
}
```

Constraints: script process is separate from codex; `env` is a whitelist that **does not include `ANTHROPIC_API_KEY`** so a script can't accidentally exfiltrate it; output capped at 1 MiB; wall-clock 60 s.

## Boundary cases

| Scenario | Behavior |
|---|---|
| Single pod | Tick claims its own; normal. |
| Multiple pods | `FOR UPDATE SKIP LOCKED` partitions claims; no leader needed. |
| Pod dies mid-fire | Lease expires → next tick on another pod re-claims; same `task_id` may yield multiple `runs`; UI shows the last by `started_at`. |
| Task timeout | SIGTERM/SIGKILL; run.status = `'timeout'`; recurring still advances per cron. |
| Bad `recurrence` | After run finalize, `advance` fails → task.status = `'failed'`, `last_error` records the parse error. No infinite retry. |
| Timezone changed via `update_task` | Affects only the **next** advance; pending rows already have UTC `process_after` and are not back-filled. |
| Paused past due | On `resume`, if `process_after < now()`, immediately eligible for lease. |
| Workspace deleted | `ON DELETE CASCADE` drops `scheduled_tasks` and `scheduled_task_runs`. |
| `script` env leakage | Script env is a whitelist that explicitly excludes provider credentials. |

## Testing

**Unit**

- `scheduler/script_test.go` — happy path, bad JSON, timeout, non-zero exit, oversized output.
- `scheduler/dispatcher_test.go` — fake supervisor + fake agentserver client; assert state-machine transitions (script gate / timeout / success → result POST shape).
- `internal/db/scheduled_tasks_test.go` — CRUD, lease SQL under concurrency (N goroutines racing → each row leased exactly once), cancel/pause/resume hitting by series_id.
- `parseZonedToUtc_test.go` — port nanoclaw's test set (UTC / naive local / DST boundary).
- recurrence advance: `"0 9 * * 1-5"` + `Asia/Shanghai` → next `process_after` is next weekday 09:00 CST converted to UTC.

**Integration**

- `internal/codexappgateway/scheduler/loop_integ_test.go` — start 3 in-process `Loop` instances against pgtest, 50 tasks; assert each fires exactly once, every recurring task advances by exactly 1 step.
- `internal/server/scheduled_tasks_integ_test.go` — REST CRUD + cancel/pause/resume end-to-end; assert PATCH changes next lease behavior.

**MCP compatibility snapshot**

- `envmcp/tools/scheduling_test.go` — snapshot the 6 tools' `tools/list` output against `testdata/scheduling.golden.json`. Any drift from the nanoclaw schema fails CI. This is the hard contract.

**Manual smoke (not CI)**

- Local app-gateway + agentserver-main + mock imbridge. Call `schedule_task(processAfter="<5 min from now>", recurrence="*/2 * * * *")` from local codex. Observe ≥2 fires within 5 minutes; mock imbridge logs N `send` calls = N workspace channels × 2 fires.

## Open questions for implementation

1. **Cost / token-usage parsing** depends on codex's JSON event schema; the spec assumes there's a `usage`-style event but the exact field names need confirming against `codex 0.132`. Implementation should keep the parser tolerant of missing fields.
2. **Transcript storage** — `transcript_uri` could be file:// (local PVC) or s3:// reusing `internal/codexappgateway/s3_store.go`. Defer to whatever oplog/turn-recording is already standardizing on; this spec just reserves the column.
3. **`creator_kind = 'system'`** is reserved for an eventual scheduling-from-IM entry point or admin-created jobs; not used in v1.

## Out of scope (future work)

- Web Console UI for managing scheduled tasks.
- Per-task target channel subset (`target_channels TEXT[]`) — additive.
- IM slash commands (`/schedule`, `/tasks`, `/cancel`).
- Cross-workspace shared schedules.
- Retry policy beyond "fail then stop"; recurring tasks naturally re-attempt on next cron tick.
