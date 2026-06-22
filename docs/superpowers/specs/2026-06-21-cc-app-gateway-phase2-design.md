# cc-app-gateway Phase 2 — S3 workspace persistence + session resume

**Status:** draft v2 (self-audit revisions applied 2026-06-21, see § Audit revisions)
**Date:** 2026-06-21 (same day as Phase 1; written after Phase 1 PR #279 shipped)
**Owner:** agentserver / cc integration
**Builds on:** [`2026-06-21-cc-app-gateway-design.md`](2026-06-21-cc-app-gateway-design.md) — Phase 1 spec.
**Resolves spec §Phase 1 vs deferred entries:**
- S3 claude-home tarball round-trip on every turn (workspace.Setup/Teardown)
- Session resume across turns (`--resume`)
- `agent_sessions.claude_session_id` plumbing on agentserver side

## Goal

Make cc-app-gateway turns **stateful across calls** for the same `(workspaceID, sessionID)` tuple:

1. **First turn** for a session: workspace.Setup creates an empty
   `<tmp>/<turnUUID>/{claude-home,project}` (Phase 1 behavior unchanged).
2. **Subsequent turn** for the same session: workspace.Setup downloads the
   prior turn's `claude-home.tar.gz` from S3 into the fresh tmpdir, so claude
   sees the session jsonl + any other state from the previous turn.
3. **Every turn's tail**: workspace.Teardown tars + gzips claude-home and uploads
   to `s3://<bucket>/cc-app-gateway/<workspaceID>/<sessionID>.tar.gz`. Overwrites
   previous turn's tarball.
4. **Runner picks the right flag**: `--session-id <UUID>` on first turn (S3 miss),
   `--resume <UUID>` on subsequent turns (S3 hit). Decided by S3 Get result, NOT
   by an agentserver schema field — Phase 2 keeps state of truth in S3.

Why this matters for the longer roadmap:

- Phase 4 (IM intake) is **not viable without resume** — every WeChat message would
  be a memoryless single turn. The deferred-Minor table in Phase 1's progress
  ledger flagged "user can't keep talking" as the blocker.
- Without S3 persistence, the gateway pod's `/tmp` would either grow unbounded or
  the per-turn discard pattern would block resume.

## Non-goals (deferred to later phases)

- In-process MCP tools (Phase 3).
- `claude_session_id` column on agentserver side. Phase 2 does NOT touch
  `agent_sessions` schema — agentserver doesn't know cc sessions exist yet;
  the only caller is direct `curl /api/turns` (and Phase 4's IM bridge, which
  hasn't landed). Adding the column now would be premature.
- S3 garbage collection of orphan tarballs (Phase 5+).
- Per-workspace S3 quota / size limits (Phase 5+).
- **Cross-pod** concurrent-turn safety for the same `(workspace, session)`
  tuple. The in-gateway mutex (see § Concurrency) serializes turns within a
  single pod, but if two pods get traffic for the same session concurrently
  (Service load-balancing during rolling restart, or sticky session not
  honored), they can both Get-Run-Put racily. Mitigation deferred to Phase 5+:
  S3 object versioning + conditional Put with If-Match. Phase 2 accepts
  last-write-wins for this rare case and documents it as a known limitation.

- Single-pod concurrent-turn safety: NOT deferred — see § Concurrency.

## Architecture

```
POST /api/turns
   │  (sessionID, workspaceID, userMessage, ...)
   ▼
TurnHandler.ServeHTTP
   ├─ wstoken fetch          (Phase 1, unchanged)
   ├─ workspace.Setup        (Phase 2 — NEW behavior)
   │     ├─ mkdir tmp dirs
   │     ├─ S3 Get key=cc-app-gateway/<workspaceID>/<sessionID>.tar.gz
   │     │     ├─ hit  → untar into ClaudeDir; remember "resume mode"
   │     │     └─ miss → leave ClaudeDir empty; remember "first-turn mode"
   │     └─ return *Workspace with `IsResume bool`
   │
   ├─ runner.Run             (Phase 2 — NEW arg)
   │     in.SessionMode = workspace.IsResume ? "resume" : "fresh"
   │     ├─ fresh    → --session-id <UUID>
   │     └─ resume   → --resume    <UUID>
   │
   ├─ workspace.Teardown     (Phase 2 — NEW behavior; runs in goroutine)
   │     ├─ tar + gzip ClaudeDir
   │     ├─ S3 Put key=cc-app-gateway/<workspaceID>/<sessionID>.tar.gz
   │     └─ os.RemoveAll(TempDir)
   │
   └─ return CcTurnResponse  (Phase 1 shape unchanged)
```

### Critical correctness invariants

1. **ProjectDir determinism**: the spawned claude's session jsonl path is
   `${CLAUDE_CONFIG_DIR}/projects/${sanitize(cwd)}/${UUID}.jsonl` where
   `sanitize` replaces `/` with `-`. For resume to find the prior jsonl, the
   subprocess's `cmd.Dir` must produce the **same sanitized** path across turns.
   - Phase 1 used `/tmp/cc-app-gateway/<turnUUID>/project/` — `turnUUID` is a
     new UUID per turn, so the sanitized cwd would differ across turns.
   - Phase 2 must use a **per-session** project dir name, not per-turn.
     Proposed: ProjectDir = `<tmpRoot>/<turnUUID>/project` BUT the jsonl path
     is keyed by sanitized cwd → we need to either (a) symlink so cwd is stable,
     or (b) use a stable per-session relative path inside the claude-home tar.

   **Decision (per Phase 0 finding):** the tar archive captures
   `claude-home/projects/<sanitized-prev-cwd>/<sessionID>.jsonl`. When we
   untar into the new turn's `ClaudeDir`, the jsonl lands at
   `<new-ClaudeDir>/projects/<sanitized-prev-cwd>/<sessionID>.jsonl`. For
   `--resume <sessionID>` to find it, the new turn's process cwd MUST sanitize
   to the SAME `<sanitized-prev-cwd>` as the previous turn.

   **Implementation:** ProjectDir = `<tmpRoot>/<turnUUID>/project` does NOT
   work (turnUUID changes). Use **`<tmpRoot>/<workspaceID>/<sessionID>/project`**
   for the project dir — stable per (workspace, session). The tmpRoot/workspace
   prefix is constant; sessionID is the unique per-session segment.
   tearDown still removes the entire `<tmpRoot>/<workspaceID>/<sessionID>/`
   subtree, so two concurrent turns of different sessions don't collide.

   ProjectDir naming changes the workspace.Workspace contract — see
   §Component changes below.

2. **CLAUDE_CONFIG_DIR persists session jsonl correctly**: claude writes
   `<CLAUDE_CONFIG_DIR>/projects/<sanitized-cwd>/<sessionID>.jsonl`. The
   tarball must capture the full `projects/` subtree, not just the jsonl file
   — claude may write `projects/<sanitized-cwd>/memory/` and other state. Tar
   the whole `ClaudeDir` tree as-is (codex's S3Backend does this).

3. **`--session-id <existing>` errors**: Phase 0 found
   `--session-id <UUID-already-in-jsonl>` errors with "session already exists".
   So if we accidentally use `--session-id` on a resume, the run fails. The
   resume-mode-detection MUST be reliable: if S3 Get succeeds AND the untarred
   jsonl contains rows for this sessionID, use `--resume`; else use `--session-id`.

   **Simplification**: trust S3 Get as the signal. If Get returns the tarball,
   it was put by a successful Teardown, which means a prior turn for this
   sessionID completed → use `--resume`. Don't inspect jsonl rows.

## Concurrency

Two race conditions are real even in Phase 2's simplest workflow (sequential
direct curls for the same sessionID):

**Race 1: Teardown-Setup race within a pod.** Spec backgrounds Teardown (S3
Put runs in goroutine after response). A scripted `curl ; curl` for the same
sessionID will routinely fire turn 2's Setup while turn 1's Put is in flight.
Turn 2's Get returns the previous-previous turn's tarball → silent resume of
stale state → claude may emit confused output or hit `--session-id already
exists` if it sees half-written jsonl.

**Race 2: RemoveAll-Setup race within a pod.** Teardown's `os.RemoveAll(<tmp>/<workspace>/<session>/)`
is mid-execution when Setup mkdirs the same path → either RemoveAll wipes the
new ClaudeDir or Setup's mkdir lands into a half-deleted tree.

### Fix: per-session mutex + graceful shutdown

```go
// In Server:
type Server struct {
    // ... Phase 1 fields ...
    sessionLocks sync.Map  // map[sessionKey]*sync.Mutex
    teardownWG   sync.WaitGroup
}

func sessionKey(workspaceID, sessionID string) string {
    return workspaceID + "/" + sessionID
}

func (s *Server) acquireSessionLock(wid, sid string) *sync.Mutex {
    key := sessionKey(wid, sid)
    actual, _ := s.sessionLocks.LoadOrStore(key, &sync.Mutex{})
    mu := actual.(*sync.Mutex)
    mu.Lock()
    return mu
}
```

`TurnHandler.ServeHTTP` flow:
```go
mu := h.Server.acquireSessionLock(req.WorkspaceID, req.SessionID)
ws, err := workspace.Setup(...)
defer func() {
    h.Server.teardownWG.Add(1)
    go func() {
        defer h.Server.teardownWG.Done()
        defer mu.Unlock()  // released only after Teardown completes
        ws.Teardown(bctx, h.Store)
    }()
}()
result, err := h.Runner(...)
// return response immediately; goroutine completes Teardown in background;
// next turn for this session blocks at acquireSessionLock until released
```

`Server.Shutdown` extended to wait for pending Teardowns:
```go
func (s *Server) Shutdown(ctx context.Context) error {
    httpErr := s.http.Shutdown(ctx)
    // Wait for in-flight Teardown goroutines with the same deadline.
    done := make(chan struct{})
    go func() { s.teardownWG.Wait(); close(done) }()
    select {
    case <-done: // all Teardowns complete
    case <-ctx.Done():
        log.Printf("[cc-app-gateway] %d teardowns still pending at shutdown", /* count */)
    }
    return httpErr
}
```

**Properties:**
- Sequential turns for the same session: turn N+1's Setup blocks at the lock
  until turn N's Teardown (including S3 Put) finishes — no Get-after-Put race.
- Concurrent different sessions: different keys → different mutexes → no
  contention.
- Pod SIGTERM: HTTP server drains in-flight requests; Server.Shutdown then
  waits up to the 30s deadline for backgrounded Teardowns. After deadline,
  remaining Teardowns are dropped (logged) — better than blocking shutdown
  indefinitely. Lost tarballs mean the next turn's Setup will Get the
  previous tarball (turn N-1 state, not turn N) — visible to users as
  "lost the last reply" but not corruption.
- sync.Map prevents unbounded growth: keys are workspace+session strings, but
  if a sessionID is never used again the entry sits idle forever. Phase 2
  accepts this — typical workload is bounded by active workspaces. A Phase 5
  cleanup ticker could prune locks unused for >24h if needed.

**What this does NOT cover:**
- Cross-pod races. Two pods serving the same sessionID concurrently
  (rolling-restart traffic shift, broken sticky-session) still race.
  Deferred to Phase 5+ via S3 versioning + conditional Put.
- Client-side concurrent calls from a single client. The client should
  serialize per sessionID; if they don't, second call blocks on the mutex
  rather than 409-erroring. Acceptable.

## Component changes

### `internal/ccappgateway/workspace/`

`workspace.Workspace`:

```go
type Workspace struct {
    TempDir     string
    ClaudeDir   string
    ProjectDir  string
    IsResume    bool   // NEW: true if S3 found prior tarball for (workspace, session)
    sessionID   string // private: used by Teardown to compute the S3 key
    workspaceID string // private
}
```

`workspace.Setup` signature changes:

```go
// Phase 1:
//   func Setup(ctx, tmpRoot string) (*Workspace, error)
// Phase 2:
//   func Setup(ctx, tmpRoot, workspaceID, sessionID string, store ObjectStore) (*Workspace, error)
```

Setup flow:
1. mkdir `<tmpRoot>/<workspaceID>/<sessionID>/{claude-home,project}` (mode 0700,
   parent 0755 if missing; Phase 1 perms preserved).
2. Try `store.Get(ctx, key)` where `key = cc-app-gateway/<workspaceID>/<sessionID>.tar.gz`.
   - On `ErrObjectNotFound`: `IsResume = false`, return.
   - On other error: return error (don't silently proceed without context).
   - On success: untar into ClaudeDir, set `IsResume = true`.

`workspace.Teardown`:
```go
// Phase 1:
//   func (w *Workspace) Teardown() error
// Phase 2:
//   func (w *Workspace) Teardown(ctx context.Context, store ObjectStore) error
```

Teardown flow:
1. **Prune `<ClaudeDir>/backups/`** before tarring. claude itself writes
   `.claude.json.backup.<timestamp>` per spawn (1 file/turn — verified Phase 0
   probe artifact `/tmp/cc-probe/claude-home/backups/`). It's a disaster-recovery
   safety net for the live config; we don't need it across turns (resume
   reads from `projects/<cwd>/<sid>.jsonl`, not from `backups/`). Without
   pruning, the tarball's file count grows linearly with turn count → 1000
   turns = 1000 small backup files → tar+gz wall-clock degrades to seconds.
   `os.RemoveAll(filepath.Join(w.ClaudeDir, "backups"))` — claude will
   re-create the directory next spawn.
2. tar + gzip the **entire `w.ClaudeDir` tree** (root = ClaudeDir, NOT
   ClaudeDir/projects). Captures: `.claude.json` (config), `projects/<sanitized-cwd>/`
   (jsonl + memory subtree), `sessions/` (empty dir but claude expects it).
   Codex's WalkDir does this correctly; mirror.
3. `store.Put(ctx, key, tarball)` — same key as Setup tried (overwrites).
4. `os.RemoveAll(w.TempDir)` — Phase 1 cleanup behavior preserved.

Error handling: if tar fails or S3 Put fails, log the error but **STILL** RemoveAll
the TempDir (don't leak disk). Return the first error. Caller logs and continues
serving — Phase 2 prefers fail-open (lose a turn's history) over fail-closed
(every subsequent turn fails because of stale tmpdir).

**Permission preservation:** codex's untar masks file mode to 0o600 and dir
mode to 0o700 (`s3.go` lines 122, 132). claude writes 0o600 files / 0o700 dirs
in ClaudeDir, so this masking is lossy but correct. cc-app-gateway mirrors
this exactly.

**`.claude.json` capture is load-bearing:** claude reads/writes this root file
for `firstStartTime`, `machineID`, `migrationVersion`. If the tarball omits
it, the next spawn re-runs first-time migrations every turn (slow + emits
warning frames). The `WalkDir` from `w.ClaudeDir` (NOT `w.ClaudeDir/projects`)
captures it.

### `internal/ccappgateway/workspace/s3.go` (NEW)

Mirror `internal/codexappgateway/codexhome/s3.go` almost verbatim:

```go
// ObjectStore is the seam between workspace and the S3 client.
// Real callers wire a thin wrapper around aws-sdk-go-v2/service/s3.
// Tests use a map-backed fake.
type ObjectStore interface {
    Put(ctx context.Context, key string, data []byte) error
    Get(ctx context.Context, key string) ([]byte, error)
    Delete(ctx context.Context, key string) error
}

var ErrObjectNotFound = errors.New("workspace: object not found")

// TarUpload tars+gzips src dir and writes to store under key.
func TarUpload(ctx context.Context, store ObjectStore, key, src string) error

// TarDownload fetches key from store and untars into dst.
// Returns ErrObjectNotFound if the key is absent.
func TarDownload(ctx context.Context, store ObjectStore, key, dst string) error
```

(Free functions, not bound to a type — there's no per-instance state. Phase 1's
`workspace.Workspace` carries the metadata; the tar+gzip plumbing is stateless.)

Tar/untar code: 1:1 copy from `internal/codexappgateway/codexhome/s3.go` lines
44-151. The skipped types (symlink/fifo/device) and the `..` path safety check
are the same.

### `internal/ccappgateway/s3client.go` (NEW)

Wires the real aws-sdk-go-v2 client to ObjectStore. ~100 LOC including
config struct, `NewS3Client(cfg)`, the 3 methods (Put/Get/Delete), and
`ErrObjectNotFound` translation from `*types.NoSuchKey`.

**Credentials: AWS default chain, NOT codex's static pattern.** Codex's
`internal/codexappgateway/s3_store.go` uses `credentials.NewStaticCredentialsProvider`
with explicit access key + secret key. That breaks on rotation. cc-app-gateway
uses `config.LoadDefaultConfig(ctx)` which honors (in order): env vars,
shared config files, ECS task role, EC2 instance metadata, **IRSA token**
(IAM Roles for Service Accounts on EKS). IRSA tokens auto-refresh; static
creds don't. For production (EKS deployment), this is the right choice —
worth the divergence from codex.

**MinIO / local dev** also works with the default chain via env vars
(`AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY`) — see integration test
docker-compose config.

Config sources:
- `CCAPPGW_S3_ENDPOINT` (optional, for MinIO/dev)
- `CCAPPGW_S3_REGION` (required)
- `CCAPPGW_S3_BUCKET` (required)
- `CCAPPGW_S3_PATH_STYLE` (bool, optional, for MinIO)
- AWS credentials from default chain (env / IRSA / shared config — no
  explicit Provider construction in this code).

### `internal/ccappgateway/runner/options.go`

`RunInput`:

```go
type RunInput struct {
    // ... Phase 1 fields ...

    // SessionMode controls which flag carries the SessionID:
    //   "fresh"  → --session-id <UUID> (first turn for this session)
    //   "resume" → --resume <UUID>     (subsequent turn)
    // Set by TurnHandler based on workspace.Workspace.IsResume.
    SessionMode string
}
```

`BuildArgs` change: replace the hardcoded `--session-id` with a switch:

```go
switch in.SessionMode {
case "resume":
    args = append(args, "--resume", in.SessionID)
default: // "fresh" or ""
    args = append(args, "--session-id", in.SessionID)
}
```

Zero-value `""` defaults to fresh — Phase 1 callers (none exist outside this
repo) still work. Production callers in turn_api always set it explicitly.

### `internal/ccappgateway/turn_api.go`

Wire S3 client into `TurnHandler`:

```go
type TurnHandler struct {
    // ... Phase 1 fields ...
    Store workspace.ObjectStore // NEW; constructed by NewServer
}
```

`ServeHTTP` flow change:

```go
ws, err := workspace.Setup(r.Context(), h.TmpRoot, req.WorkspaceID, req.SessionID, h.Store)
// ...
defer func() {
    // Background — Phase 1 was synchronous; Phase 2 backgrounds to avoid
    // adding S3 upload latency to the HTTP response.
    go func() {
        // Use a fresh context with a sensible upload deadline (not the
        // request's r.Context() which is cancelled after response write).
        bctx, bcancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer bcancel()
        if err := ws.Teardown(bctx, h.Store); err != nil {
            log.Printf("[cc-app-gateway] workspace teardown failed (session=%s): %v", req.SessionID, err)
        }
    }()
}()
// ...
result, err := h.Runner(runCtx, runner.RunInput{
    // ... Phase 1 fields ...
    SessionMode: sessionModeFromIsResume(ws.IsResume),
})
```

Where `sessionModeFromIsResume(b bool) string` returns "resume" if true, "fresh" if false.

### `internal/ccappgateway/server.go`

`NewServer` constructs the S3 client at startup:

```go
store, err := NewS3Client(S3Config{
    Endpoint:  cfg.S3Endpoint,
    Region:    cfg.S3Region,
    Bucket:    cfg.S3Bucket,
    PathStyle: cfg.S3PathStyle,
})
if err != nil {
    return nil, fmt.Errorf("s3 client: %w", err)
}
turnHandler.Store = store
```

`/readyz` adds a third check: call `store.Get(ctx, "cc-app-gateway/__readyz__/probe")`
and treat `ErrObjectNotFound` as success (proves we can reach S3 + auth works).
Any other error → readyz 503 with "s3 unreachable: ...".

**The probe key is inside `cc-app-gateway/` prefix on purpose.** If operators
tighten IAM to `s3:GetObject` only on `arn:aws:s3:::<bucket>/cc-app-gateway/*`
(least privilege), a probe key outside that prefix would 403 → permanent
readyz 503 → all pods un-ready → outage. Keep the probe inside the prefix
the gateway already has perms for.

### `config.go`

Add S3 fields to `ServeConfig`:

```go
type ServeConfig struct {
    // ... Phase 1 fields ...
    S3Endpoint  string
    S3Region    string
    S3Bucket    string
    S3PathStyle bool
}
```

All required EXCEPT S3Endpoint and S3PathStyle (optional, for MinIO). New env vars:

| Env var | Required | Default | Purpose |
|---|---|---|---|
| `CCAPPGW_S3_ENDPOINT` | n | "" | MinIO endpoint (omit for real AWS) |
| `CCAPPGW_S3_REGION` | y | — | e.g. us-east-1 |
| `CCAPPGW_S3_BUCKET` | y | — | bucket name |
| `CCAPPGW_S3_PATH_STYLE` | n | false | path-style addressing (MinIO) |

AWS credentials sourced from the default SDK chain (env vars / IRSA / shared config).
Helm chart adds `extraEnvFrom` to wire k8s Secret keys to `AWS_ACCESS_KEY_ID`
+ `AWS_SECRET_ACCESS_KEY` (mirror codex pattern).

### helm chart + values.yaml

`values.yaml` `ccAppGateway` block adds:

```yaml
ccAppGateway:
  # ... Phase 1 fields ...
  s3:
    endpoint: ""           # MinIO endpoint; empty for real AWS
    region: "us-east-1"
    bucket: ""             # required when enabled=true
    pathStyle: false
    existingSecret: ""     # k8s secret with keys: access_key_id, secret_access_key
```

Template adds the env-var wiring + envFrom secret ref (1:1 mirror of codex's
`codex-app-gateway.yaml`).

## Tests

### Unit tests

- `workspace_test.go`: cover Setup with both hit and miss paths; cover Teardown
  upload + cleanup; cover error paths (S3 Put fails → tmpdir still removed,
  error returned).
- `s3_test.go`: round-trip a small tar through TarUpload + TarDownload via a
  map-backed fake ObjectStore (mirror codex's pattern).
- `runner/options_test.go`: cover BuildArgs SessionMode switch — fresh emits
  `--session-id`, resume emits `--resume`, default (empty) emits `--session-id`.
- `turn_api_test.go`: cover ws.IsResume → SessionMode wiring; cover S3 client
  injection via the test's `NewServerWithRunner` extended to accept a fake store.

### Integration test

Extend `internal/ccappgateway/integration_test.go`:

```go
func TestIntegration_ResumeAcrossTurns(t *testing.T) {
    // ... bring up stack with a minio sidecar ...

    // Turn 1: "My favorite color is blue."
    // Turn 2 same sessionID: "What's my favorite color?"
    // Assert turn 2's assistantText contains "blue".
}
```

Add minio container to `docker-compose.yml`:

```yaml
  minio:
    image: minio/minio:latest
    command: server /data
    environment:
      MINIO_ROOT_USER: minioadmin
      MINIO_ROOT_PASSWORD: minioadmin
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:9000/minio/health/live"]
      interval: 2s
      timeout: 1s
      retries: 10
```

cc-app-gateway env vars in compose extended to point at minio:
```yaml
      CCAPPGW_S3_ENDPOINT: http://minio:9000
      CCAPPGW_S3_REGION: us-east-1
      CCAPPGW_S3_BUCKET: cc-app-gateway-test
      CCAPPGW_S3_PATH_STYLE: "true"
      AWS_ACCESS_KEY_ID: minioadmin
      AWS_SECRET_ACCESS_KEY: minioadmin
```

A one-shot `init-bucket` container (or `make up` step) creates the test bucket
before cc-app-gateway starts. Use minio-mc image: `mc mb local/cc-app-gateway-test`.

Per-turn fake-llmproxy upgrade: the fake's canned reply needs to vary by user
message content (so turn 2 has different output than turn 1). Simplest: instead
of a fixed `--canned-reply`, the fake parses the incoming `messages[]` and
returns `messages[-1].content` reversed (or some deterministic function). This
way turn 1 with "blue" → assistant says "eulb"; turn 2 with "what's my color?"
plus claude's session context → claude (talking to fake llmproxy which echoes)
includes the earlier "blue" in its response.

**However** the fake-llmproxy doesn't actually run a real LLM — it just echoes.
For testing resume, the assertion has to be that the **conversation history is
present in the second call's request to llmproxy**, not that the model "remembers".
Pivot:

- Add fake-llmproxy log mode that dumps every inbound request body to a file.
- Integration test reads the second turn's request body, asserts it contains
  the user message from turn 1 (proving claude resumed and sent the full
  conversation history).

This is a cleaner test of the resume mechanism (request shape) without
depending on LLM behavior. Spec the test this way.

## Phase 2 vs Phase 1 contract

**Wire-level breaking changes from Phase 1:** None. `POST /api/turns` request
and response shapes unchanged. Callers that worked in Phase 1 work in Phase 2.

**Behavioral changes:**
- Same sessionID with same workspaceID NOW resumes (Phase 1 returned an error
  on duplicate session ID).
- Per-turn latency adds 1 S3 Get (Setup) + 1 S3 Put (Teardown, backgrounded
  so doesn't affect response time).
- Cold-cache S3 client init at startup (negligible).

**New required env vars:** if `ccAppGateway.enabled=true`, `S3_BUCKET` + `S3_REGION`
must be set. NewServer fails fast at startup if missing — better than 500s on
every turn.

## Migration

cc-app-gateway is shipped Phase 1 with `enabled=false` default. Phase 2 keeps
that default. Existing users (none external as of 2026-06-21) have nothing to
migrate. The `ccAppGateway.s3.*` helm values are new and have empty defaults;
setting `enabled=true` requires also setting `s3.bucket` + `s3.region` —
helm template renders an error via `{{ required }}` if either missing.

**Before bumping Chart.yaml on Phase 2 merge:** verify no flux HelmRelease has
`ccAppGateway.enabled=true`:

```bash
grep -rn "ccAppGateway:" /root/nanoclaw/ 2>/dev/null | grep -v -E "(enabled: false|^\s*#)"
```

If any hit shows `enabled: true`, that deployment needs `s3.bucket` + `s3.region`
added in the same chart-bump PR or the helm upgrade will fail. (Phase 1 left
the default `false`, so this is unlikely but worth checking.)

**Phase 4 prerequisite:** Phase 4's IM intake must allocate a sessionID at
IM-thread creation time and persist it in a new `agent_sessions.claude_session_id`
column. Phase 2 deliberately does NOT add this column — direct curl callers in
Phase 2 supply their own sessionID — but the column will be added in Phase 4's
schema migration. Phase 2 contains no Phase-4-incompatible decisions; the only
coupling is that Phase 4's ccDispatcher must pass the workspaceID + sessionID
it allocated through to cc-app-gateway, which is already supported by Phase 2's
unchanged request schema.

Phase 1 PR #279 still in review at time of writing; Phase 2 PR will be a
**stacked PR** with base = `feat/cc-app-gateway-phase1` so it can land
sequentially with Phase 1.

## Open risks

1. **Tarball growth (jsonl is linear; backups/ is bounded).**

   **`CLAUDE_CODE_AUTO_COMPACT_WINDOW` does NOT mitigate growth in `--print`
   mode.** Spec v1 claimed it did; this was wrong. Phase 0 FINDINGS.md
   surprise #7 documents that auto-compaction only fires inside interactive
   sessions — `claude --print` exits before compaction triggers. The env var
   we set in BuildEnv is effectively dead for our use case.

   Two sources of growth, handled separately:

   a) **`backups/.claude.json.backup.<timestamp>`** — claude writes 1 file
      per spawn (verified Phase 0 probe artifact). Without action, 1000 turns
      = 1000 small files in the tarball → tar+gz wall-clock degrades to
      seconds. **Fix in Phase 2:** Teardown prunes `<ClaudeDir>/backups/`
      before tarring (see § Component changes / workspace.Teardown).
      claude re-creates the directory next spawn. Safe — backups/ is not
      involved in resume.

   b) **`projects/<sanitized-cwd>/<sid>.jsonl`** — claude appends 5-10 lines
      per turn (verified Phase 0). 1000 turns ≈ 5MB jsonl ≈ ~500KB-1MB
      gzip'd tarball ≈ 500-800ms S3 Get/Put per turn. **Phase 2 accepts
      this as a known limitation.** Practical impact: typical IM users
      (50-200 messages/day) reach this in months; heavy daily users
      (~100 messages/day in one session) reach this in 1-2 weeks.

      Phase 5+ mitigation: "session rotation" — after N turns, allocate
      a new sessionID and seed it with a summary of the previous session
      as system prompt. Requires Phase 4's `agent_sessions.claude_session_id`
      column plus a `prev_session_id` column to chain rotations. Defer.

2. **Cross-pod S3 Put race.** Two pods serving the same `(workspace, session)`
   racing each other: last-write-wins → loses history. The in-gateway
   per-session mutex (see § Concurrency) prevents this within a single pod
   but doesn't help across pods. Acceptable for Phase 2 — typical workload is
   one pod replica, and Service load-balancing with `sessionAffinity: ClientIP`
   keeps the same client (and therefore same sessionID) on the same pod.
   Phase 5+ fix: S3 object versioning + conditional Put with `If-Match`.

3. **CLAUDE_CONFIG_DIR sanitized-cwd path drift.** If we change `ProjectDir`
   path naming OR if claude changes its sanitize algorithm (currently
   `/` → `-` + strip leading `-`, per Phase 0 FINDINGS.md #1) between
   versions, prior tarballs become un-resumable. Lock the path as
   `<tmpRoot>/<workspaceID>/<sessionID>/project` FOREVER in the spec.
   Pin it in a comment in workspace.go. On every claude binary version
   bump, re-run a Phase-0-style probe to confirm the sanitize algorithm
   hasn't drifted; add the probe assertion to the binary-version-bump
   PR checklist.

4. **fake-llmproxy doesn't actually exercise the LLM-level resume.** The
   integration test asserts request-body content includes prior turn's
   message, not that an LLM remembers. This is a test of the runner/workspace
   resume mechanism, NOT a test that claude actually does the right thing.
   Phase 5 (or earlier dev smoke testing) should add a probe against the real
   Anthropic endpoint with an OAuth token to confirm end-to-end resume works.

5. **S3 outage = all turns fail.** Phase 2 makes S3 a hard dependency
   (readyz fails, every Setup attempts a Get). Mitigation: graceful Setup
   degradation — if S3 Get fails with a network error (not NotFound), log the
   error but proceed as `IsResume = false` so the user gets a "fresh
   conversation" rather than a 500. Document this as "best-effort resume"
   behavior in spec § Error handling.

6. **Chart-upgrade rolling-restart cross-pod race.** During `kubectl rollout`
   the old pod may have a Teardown goroutine in flight when the new pod starts
   serving the next turn for the same session. Old pod's S3 Put can land
   AFTER new pod's Get → new pod resumes from turn N-1 state, then old pod's
   Put overwrites turn N's tarball with the older state. The in-gateway mutex
   doesn't span pods. Phase 2 mitigations: (a) `Server.Shutdown` waits up to
   30s for pending Teardowns (see § Concurrency); (b) helm preStop + grace
   period give the wait a real chance. (c) Phase 5+ adds S3 versioning +
   If-Match for a hard guarantee. Phase 2 ships with (a) + (b); rare in
   practice with single-replica deployment.

7. **Encryption at rest / KMS / object versioning / lifecycle policy** all
   deferred to Phase 5+. Phase 2 relies on bucket-default SSE-S3 (AWS default)
   or MinIO's bucket-level encryption settings. No bucket policy enforced by
   the gateway.

8. **Cross-region latency.** If bucket region ≠ pod region, every turn adds
   50-100ms each direction. Operator responsibility: bucket and gateway pod
   should be co-region. Document in helm values comment.

## Audit revisions (2026-06-21, post-self-review)

Spec v1 was self-audited via two parallel adversarial readers. Three real
bugs were found and patched (Critical), plus two important corrections.

### Revision 1 (Critical) — `CLAUDE_CODE_AUTO_COMPACT_WINDOW` mitigation was fictional

**v1 claimed:** `CLAUDE_CODE_AUTO_COMPACT_WINDOW=165000` (set in BuildEnv)
mitigates jsonl growth across turns.

**Reality:** Phase 0 FINDINGS.md surprise #7 explicitly says "Session jsonl
is append-only and grows linearly with conversation. **No auto-compaction in
print mode.**" The env var only does anything for interactive (long-lived)
claude sessions; `claude --print` exits before compaction triggers. The env
var is effectively dead code in cc-app-gateway's use case.

**Patched in:** Open Risks #1 rewritten to honestly document linear jsonl
growth as a known limitation. Added the Teardown `backups/` prune as the
only growth fix Phase 2 ships. Phase 5+ session-rotation path documented.

### Revision 2 (Critical) — `backups/` was an unmentioned growth source

**v1 omitted:** the fact that claude itself writes `.claude.json.backup.<ts>`
to `<ClaudeDir>/backups/` per spawn — 1 file per turn. After 1000 turns,
1000 small files in the tarball → tar+gz wall-clock degrades to seconds
per turn even though total bytes are small.

**Reality (verified Phase 0 probe artifact `/tmp/cc-probe/claude-home/backups/`):**
the file exists; claude binary strings confirm `backups/ may still contain ...`
is claude's own comment text. Not adversary-controlled, but unbounded.

**Patched in:** workspace.Teardown step 1 now prunes
`<ClaudeDir>/backups/` before tarring. claude re-creates the directory on
next spawn. backups/ is NOT involved in resume (resume reads jsonl only),
so pruning is safe.

### Revision 3 (Critical) — Teardown-Setup race within a pod was undocumented

**v1 said:** "concurrent-turn safety deferred to Phase 4 ccDispatcher" —
treating same-pod sequential `curl ; curl` as out of scope.

**Reality:** Phase 2 backgrounds Teardown (S3 Put is the slow part). A
sequential client's turn 2 fires Setup while turn 1's Put is still in flight
→ Get returns previous-previous turn's tarball → resume silently corrupts
state. This is the DEFAULT behavior, not a concurrent-call edge case.

**Patched in:** new § Concurrency section adds per-session mutex
(`sync.Map[sessionKey]*sync.Mutex`) + `Server.Shutdown` drains pending
Teardown goroutines via sync.WaitGroup. ~80 LOC. Cross-pod race remains a
known limitation (Open Risk #6) deferred to Phase 5+.

### Revision 4 (Important) — `/readyz` probe key outside `cc-app-gateway/` prefix

**v1 used:** `__readyz__/probe` as the readyz S3 probe key.

**Reality:** operators tighten IAM to least-privilege `s3:GetObject` on
`arn:aws:s3:::<bucket>/cc-app-gateway/*`. Any key outside that prefix would
403 AccessDenied → permanent readyz 503 → all pods un-ready → outage.

**Patched in:** probe key changed to `cc-app-gateway/__readyz__/probe`
(inside the prefix the gateway has perms for).

### Revision 5 (Important) — Credentials story was inconsistent with code

**v1 claimed:** "AWS credentials sourced from the default SDK chain (env
vars / IRSA / shared config)" while citing codex as the reference pattern.

**Reality:** codex's `internal/codexappgateway/s3_store.go:27` uses
`credentials.NewStaticCredentialsProvider(accessKey, secret, "")` — explicit
static creds from env. NOT the default chain. Static creds break on IRSA
token rotation (EKS production).

**Patched in:** cc-app-gateway s3client.go § calls out the divergence
explicitly: cc-app-gateway uses `config.LoadDefaultConfig(ctx)` (real
default chain incl. IRSA), NOT codex's static pattern. The right answer
for prod; worth the divergence. Local-dev / MinIO still works via env vars
which the default chain reads first.

### Other audit findings (no changes needed)

- Tar safety (only `..` path check, symlinks/fifo/devices skipped): codex
  pattern is sufficient since claude-home content is gateway-generated,
  not user-controlled. Spec § Component changes already specifies
  1:1 copy of codex's s3.go.
- Path-length: workspace+session UUIDs + tmpRoot prefix is ~120 bytes,
  well under PATH_MAX (4096) and ext4's 255-byte filename cap.
- `--resume` + token refresh: jsonl rows don't cache auth tokens
  (Phase 0 init frame: `apiKeySource: "none"`); token is read fresh from
  `ANTHROPIC_AUTH_TOKEN` env on every spawn. No interaction issue.
- Empty directories survive tar round-trip (codex's WalkDir writes dir
  headers even for empty dirs).
- File permissions: codex's untar masks to 0o600 / 0o700 — matches what
  claude writes, lossy but correct.
- aws-sdk-go-v2 deps already in go.mod (v1.100.1 for s3). No new direct deps.

## Acceptance

A developer can:

1. Bring up the Phase 2 docker-compose harness (now includes minio):
   ```bash
   cd internal/ccappgateway/testdata/integration && make up
   ```

2. Send turn 1:
   ```bash
   curl -sX POST http://localhost:8087/api/turns \
     -H "X-Internal-Secret: secret123" \
     -H "Content-Type: application/json" \
     -d '{
       "workspaceId": "ws_test",
       "sessionId":   "00000000-0000-4000-8000-000000000001",
       "userMessage": "My favorite color is blue."
     }'
   ```

3. Verify minio has the tarball:
   ```bash
   docker compose exec minio mc ls local/cc-app-gateway-test/cc-app-gateway/ws_test/
   # → 00000000-0000-4000-8000-000000000001.tar.gz
   ```

4. Send turn 2 with the SAME sessionID:
   ```bash
   curl -sX POST http://localhost:8087/api/turns ... \
     -d '{"workspaceId":"ws_test","sessionId":"00000000-0000-4000-8000-000000000001",
          "userMessage":"What did I just tell you?"}'
   ```

5. Inspect fake-llmproxy's request log: turn 2's `messages[]` array contains
   2+ entries (the prior user message + the prior assistant message + the new
   user message), proving the resume worked.

6. Send turn 1 to a NEW sessionID: independent — minio shows a separate
   tarball, conversation does NOT cross-contaminate.

7. `docker compose restart cc-app-gateway`: turn 2 to the original sessionID
   still resumes (state in S3, not in-memory).

Integration test `TestIntegration_ResumeAcrossTurns` is the automated version
of items 2, 4, and 5.
