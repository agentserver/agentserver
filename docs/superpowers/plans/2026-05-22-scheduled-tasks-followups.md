# Scheduled Tasks — Open Follow-ups

> Companion to `2026-05-22-scheduled-tasks.md`. These are issues surfaced by the final whole-feature code review (post-Task 12) that were **deferred to follow-up work** because they require architectural decisions or non-trivial wiring not covered by the original plan.

## C2 — Spawned `codex exec` has no credentials and no PATH

**Status**: deferred. Every scheduled fire in production will fail until this is wired.

**Where**:
- `internal/codexappgateway/scheduler/dispatcher.go` — `codexEnv(t)` returns only `{"TZ=..."}`.
- `internal/codexappgateway/scheduler/spawn.go` — `cmd.Env = append([]string{}, in.Env...)` does not inherit anything from the parent process.
- `internal/codexappgateway/scheduler/loop.go` — `Loop.New` does not receive any credential fetcher.

**Why deferred**: requires deciding how scheduler.Loop fetches per-workspace credentials. The live-spawn path uses `wsTokenClient.GetOrCreate(ctx, workspaceID)` (see `internal/codexappgateway/server.go:makeBuildConfig`); the scheduler should do the same, but that means passing `*WorkspaceTokenClient` (or an interface mirror) into `scheduler.Config` and using it in `dispatcher.Fire` before spawning. Also need to decide which env vars to inherit (PATH, HOME, CODEX_HOME) vs. set explicitly per spawn.

**Sketch of fix**:
1. Add `WorkspaceTokens` field (interface with `GetOrCreate(ctx, wsID) (string, error)`) to `scheduler.Config`.
2. In `dispatcher.Fire`, before `SpawnExec`, fetch the workspace token and build env.
3. In `Spawner.Run`, optionally inherit a whitelist of parent env vars (PATH, HOME) — or set them explicitly via Loop config.
4. Test with a fake `WorkspaceTokens` that returns a static string.

**Spec reference**: §"Fire pipeline" step 3 — "env: CODEX_HOME=<per-spawn>, ANTHROPIC_API_KEY=<workspace cred>, TZ=<task.timezone>". The plan task 12 wired CodexBin but not creds; the spec had this in the §"Open questions for implementation" section.

## I2 — ~~`scheduling.instructions.md` ships but is never bundled into MCP tool registration~~ ✅ Resolved

✅ Resolved — content folded into `schedule_task` Description() in commit for "fix(scheduling): inline script guidance into schedule_task description; drop unused TZ header reference".

## Acknowledged gaps (NOT regressions, but worth noting)

- **No multi-pod loop integration test** — would have caught issue C1 (lease bug) earlier. Adding one against pgtest is a good next step: spin up 3 in-process Loops, seed 50 due tasks, assert each fires exactly once.
- **No graceful shutdown drain** — `Run(ctx)` returns immediately on ctx cancel; inflight `Fire` goroutines are abandoned. With LeaseSeconds default 1800s, the dispatcher recovery path (now fixed in commit 937b8aa) handles this, but a `sync.WaitGroup` with a short drain timeout would be cleaner.
- **Cost / num_turns parsing left nil** — codex-version-specific; deferred per original spec.
- **No Web Console UI** — out of scope per spec.
