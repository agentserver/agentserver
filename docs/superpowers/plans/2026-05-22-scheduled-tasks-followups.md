# Scheduled Tasks — Open Follow-ups

> Companion to `2026-05-22-scheduled-tasks.md`. These are issues surfaced by the final whole-feature code review (post-Task 12) that were **deferred to follow-up work** because they require architectural decisions or non-trivial wiring not covered by the original plan.

## C2 — ~~Spawned `codex exec` has no credentials and no PATH~~ ✅ Resolved

Fixed in commit for "fix(scheduler): inject workspace Bearer + PATH/HOME into spawned codex env (resolves C2)". `Dispatcher.codexEnv(ctx, t)` now:
- Inherits PATH, HOME, CODEX_HOME from the dispatcher process env (selective whitelist).
- Fetches per-workspace Bearer via `WorkspaceTokenFetcher.GetOrCreate(ctx, workspaceID)` and sets it under `ModelProviderEnvKey` (e.g. `CODEX_API_KEY=<token>`), same mechanism as the live-spawn path uses.
- On token-fetch failure, fires `status='failed'` with the error in summary, instead of spawning a credential-less codex.

`scriptEnv(t)` deliberately does NOT include the token — pre-task scripts shouldn't see provider credentials.

## I2 — ~~`scheduling.instructions.md` ships but is never bundled into MCP tool registration~~ ✅ Resolved

✅ Resolved — content folded into `schedule_task` Description() in commit for "fix(scheduling): inline script guidance into schedule_task description; drop unused TZ header reference".

## Acknowledged gaps (NOT regressions, but worth noting)

- **No multi-pod loop integration test** — would have caught issue C1 (lease bug) earlier. Adding one against pgtest is a good next step: spin up 3 in-process Loops, seed 50 due tasks, assert each fires exactly once.
- **No graceful shutdown drain** — `Run(ctx)` returns immediately on ctx cancel; inflight `Fire` goroutines are abandoned. With LeaseSeconds default 1800s, the dispatcher recovery path (now fixed in commit 937b8aa) handles this, but a `sync.WaitGroup` with a short drain timeout would be cleaner.
- **Cost / num_turns parsing left nil** — codex-version-specific; deferred per original spec.
- **No Web Console UI** — out of scope per spec.
