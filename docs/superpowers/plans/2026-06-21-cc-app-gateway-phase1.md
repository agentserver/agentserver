# cc-app-gateway Phase 1 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the Phase 1 contract of cc-app-gateway as defined in
`docs/superpowers/specs/2026-06-21-cc-app-gateway-design.md` § Phase 1 contract.
A developer should be able to `docker run cc-app-gateway:dev serve`, point a curl
at `POST /api/turns` with a workspace_id + session_id + userMessage, and receive
the final assistant text from a real `claude --print` subprocess driven by
stream-json IO and authenticated against agentserver via a per-workspace token.

**Out of scope for Phase 1** (explicitly deferred): session resume (`--resume`),
S3 workspace persistence, in-process MCP tools (env-mcp subcommand), IM intake
on agentserver side, `callbackUrl` async mode, SSE streaming. Each is its own
phase per the spec.

**Architecture:** A single Go program with one subcommand (`serve`). On each
`POST /api/turns`:

1. Verify `X-Internal-Secret` (or future `Bearer <workspace_token>`).
2. Fetch a workspace proxy token from `agentserver POST /internal/workspace-token`.
3. Create an ephemeral `/tmp/cc-app-gateway/<turn_uuid>/{claude-home,project}` dir.
4. Spawn `claude --print --output-format stream-json --input-format stream-json
   ...` with `ANTHROPIC_AUTH_TOKEN=<wsToken>` + `ANTHROPIC_BASE_URL=<llmproxy>`,
   write one SDKUserMessage to stdin, read SDKMessage frames from stdout.
5. Extract the final assistant text from the `result/success` frame.
6. Return `CcTurnResponse` synchronously (Phase 1 sync-only).
7. Background-cleanup the temp dir.

**Tech Stack:** Go 1.26, `github.com/go-chi/chi/v5` (router; matches codex-app-gateway),
stdlib `encoding/json` (stream-json codec), stdlib `os/exec` (subprocess), stdlib
`net/http` (server + wstoken client). **No SDK dependencies** for claude — runner
speaks stream-json directly per Phase 0 PoC. Reuse codex-app-gateway's
`wstoken_client.go` pattern (75 LOC, stdlib-only) by copying — both clients hit
the same agentserver endpoint with the same secret.

**Spec:** `/root/agentserver/docs/superpowers/specs/2026-06-21-cc-app-gateway-design.md`
(read § Architecture, § Subprocess driver, § Phase 1 contract, § Auth model,
§ Audit revisions before starting).

**Phase 0 artifacts (validated 2026-06-21):**

- `/tmp/cc-probe/probe.go` — Go driver that spawned claude 2.1.185 via stream-json.
- `/tmp/cc-probe/echo_mcp.py` — minimal stdio MCP server (not used in Phase 1
  since we skip MCP entirely, kept for Phase 3 reference).
- `/tmp/cc-probe/FINDINGS.md` — what works on 2.1.185 and what doesn't.
- `/tmp/cc-probe/transcript.jsonl` — 90 frames of real SDKMessage output;
  primary reference for runner/events.go frame-shape tests.

**Working directory:** All tasks operate in `/root/agentserver`. Use a worktree
per the superpowers:using-git-worktrees skill before starting (branch:
`feat/cc-app-gateway-phase1`).

**Module path:** `github.com/agentserver/agentserver`.

---

## File structure

| File | Responsibility |
|---|---|
| `cmd/cc-app-gateway/main.go` | Subcommand dispatch (`serve` only in Phase 1; placeholder for `env-mcp` returning "not implemented in phase 1") |
| `cmd/cc-app-gateway/serve_args.go` | `parseServeArgs` — parses `--listen-addr`, `--claude-bin` |
| `cmd/cc-app-gateway/serve_args_test.go` | Round-trip flag parsing tests |
| `internal/ccappgateway/config.go` | `ServeConfig` struct + `LoadServeConfigFromEnv()` for `CCAPPGW_*` env vars |
| `internal/ccappgateway/config_test.go` | Env-roundtrip tests |
| `internal/ccappgateway/auth/auth.go` | `InternalSecretMiddleware` (Phase 1 used); `BearerMiddleware` stub returning 501 (wired but unexercised; Phase 5+) |
| `internal/ccappgateway/auth/auth_test.go` | Middleware unit tests |
| `internal/ccappgateway/wstoken_client.go` | Mirror of `internal/codexappgateway/wstoken_client.go` — same agentserver endpoint, same secret, same response shape. Package decl changes; struct/method names may be renamed; the duplication is intentional (spec § Audit revisions) since the two gateways have different release cadences and may diverge. |
| `internal/ccappgateway/wstoken_client_test.go` | httptest-mock-server tests for happy path + auth failure + empty token |
| `internal/ccappgateway/workspace/workspace.go` | Phase 1 stub: `Workspace { TempDir, ClaudeDir, ProjectDir }`; `Setup(ctx) (*Workspace, error)` (mkdir only — no S3); `(*Workspace) Teardown() error` (rm -rf) |
| `internal/ccappgateway/workspace/workspace_test.go` | Setup creates dirs, Teardown removes them, double-Teardown is safe |
| `internal/ccappgateway/runner/stream_json.go` | SDKMessage struct + `Decode(reader) (<-chan SDKMessage, <-chan error)` + `EncodeUserMessage(writer, text) error` |
| `internal/ccappgateway/runner/stream_json_test.go` | Golden tests against `/tmp/cc-probe/transcript.jsonl` frames |
| `internal/ccappgateway/runner/events.go` | Frame classification (keep/drop), `ExtractAssistantText(<-chan SDKMessage) (string, *ResultMeta, error)` |
| `internal/ccappgateway/runner/events_test.go` | Each frame type → expected keep/drop + extracted text |
| `internal/ccappgateway/runner/options.go` | `BuildArgs(cfg, ws, sessionID, model) []string`; `BuildEnv(cfg, ws, wsToken) []string` |
| `internal/ccappgateway/runner/options_test.go` | Asserts every required flag/env-var is present |
| `internal/ccappgateway/runner/runner.go` | `Run(ctx, cfg, ws, sessionID, userMsg, wsToken) (*RunResult, error)` — spawn claude, drive stdio, return result |
| `internal/ccappgateway/runner/runner_test.go` | Tests using a fake claude binary (small Go test helper that reads stdin + emits canned SDKMessage frames) |
| `internal/ccappgateway/turn_api.go` | `TurnHandler.ServeHTTP` — orchestrates wstoken → workspace → runner → response |
| `internal/ccappgateway/turn_api_test.go` | Integration test with httptest server, fake wstoken endpoint, fake claude |
| `internal/ccappgateway/server.go` | `Server`, `Routes()` (chi), `Start`, `Shutdown` — wires middleware + healthz/readyz/api/turns |
| `internal/ccappgateway/server_test.go` | Server-level integration (auth pass/fail, healthz, readyz when wstoken unreachable) |
| `Dockerfile.cc-app-gateway` | Multi-stage Go build + claude install via official script (mirrors `Dockerfile.claudecode`) |
| `deploy/helm/agentserver/templates/cc-app-gateway.yaml` | Deployment + Service (mirrors `codex-app-gateway.yaml`) |
| `deploy/helm/agentserver/values.yaml` | `ccAppGateway` block (mirrors `codexAppGateway` block; `enabled: false` default) |
| `.github/workflows/build.yml` | Add `build-cc-app-gateway` job (mirrors `build-codex-app-gateway`) |
| `internal/ccappgateway/integration_test.go` | `//go:build integration` — docker-compose harness driving real claude with fake llmproxy |

Total new files: ~24. Modified: 2 (`.github/workflows/build.yml`,
`deploy/helm/agentserver/values.yaml`). Estimated total LOC including tests:
~2200 (~900 production, ~1300 tests).

---

## Task 1: `serve` subcommand wiring + config

**Files:**
- Create: `cmd/cc-app-gateway/main.go`
- Create: `cmd/cc-app-gateway/serve_args.go`
- Create: `cmd/cc-app-gateway/serve_args_test.go`
- Create: `internal/ccappgateway/config.go`
- Create: `internal/ccappgateway/config_test.go`

**CLI contract:**

```
cc-app-gateway serve \
    [--listen-addr <addr>]   default :8087, env CCAPPGW_LISTEN_ADDR
    [--claude-bin  <path>]   default /usr/local/bin/claude, env CCAPPGW_CLAUDE_BIN

cc-app-gateway env-mcp
    Phase 1: prints "env-mcp not implemented in phase 1" and exits 2.
    (Subcommand reserved for Phase 3.)

cc-app-gateway version
    Prints version + git sha.
```

Other knobs are env-only:

| Env var | Required | Default | Purpose |
|---|---|---|---|
| `CCAPPGW_LISTEN_ADDR` | n | `:8087` | HTTP listen address |
| `CCAPPGW_CLAUDE_BIN` | n | `/usr/local/bin/claude` | Path to claude binary |
| `INTERNAL_API_SECRET` | y | (none) | Shared secret w/ agentserver |
| `AGENTSERVER_INTERNAL_URL` | y | (none) | e.g. `http://agentserver:8080` |
| `CCAPPGW_LLMPROXY_URL` | y | (none) | e.g. `http://llmproxy:8081` |
| `CCAPPGW_DEFAULT_MODEL` | n | `haiku` | Used if request omits `model` |
| `CCAPPGW_TURN_TIMEOUT` | n | `10m` | Wall-clock max per turn |
| `CCAPPGW_TMP_ROOT` | n | `/tmp/cc-app-gateway` | Base dir for per-turn tmpdirs |
| `CCAPPGW_LOG_LEVEL` | n | `info` | `debug` / `info` / `warn` / `error` |

- [ ] **Step 1: Failing serve_args test**

  Create `cmd/cc-app-gateway/serve_args_test.go`. Test cases:
  - Empty args → defaults (`:8087`, `/usr/local/bin/claude`)
  - `--listen-addr :9000 --claude-bin /opt/claude` → both overridden
  - `--listen-addr=:9000` (=form) → parsed
  - Unknown flag → error mentioning the flag name

  Should fail to compile (no `serve_args.go` yet).

- [ ] **Step 2: Failing config test**

  Create `internal/ccappgateway/config_test.go`. Test cases:
  - All env vars set → `LoadServeConfigFromEnv()` returns populated struct
  - Required vars missing → error naming the missing one
  - Duration parse failure (`CCAPPGW_TURN_TIMEOUT=garbage`) → error
  - Invalid log level → error

  Should fail to compile (no `config.go` yet).

- [ ] **Step 3: Run tests to verify they fail**

  ```
  cd cmd/cc-app-gateway && go test ./...      # build error expected
  cd internal/ccappgateway && go test ./...   # build error expected
  ```

- [ ] **Step 4: Implement `serve_args.go`**

  Use stdlib `flag` (not pflag/cobra — match codex-app-gateway). Function
  signature:

  ```go
  func parseServeArgs(args []string) (serveFlags, error)
  type serveFlags struct {
      ListenAddr string
      ClaudeBin  string
  }
  ```

  Honor `CCAPPGW_LISTEN_ADDR` / `CCAPPGW_CLAUDE_BIN` env as defaults; CLI
  flags override env.

- [ ] **Step 5: Implement `config.go`**

  ```go
  type ServeConfig struct {
      ListenAddr             string
      ClaudeBin              string
      InternalSecret         string
      AgentserverInternalURL string
      LLMProxyURL            string
      DefaultModel           string
      TurnTimeout            time.Duration
      TmpRoot                string
      LogLevel               string
  }
  func LoadServeConfigFromEnv(flags serveFlags) (ServeConfig, error)
  ```

  Validation: required vars must be non-empty; durations must parse via
  `time.ParseDuration`; LogLevel must be one of {debug, info, warn, error}.

- [ ] **Step 6: Implement `main.go`**

  Subcommand dispatch (no cobra). Top-level args: first non-flag = subcommand.
  Subcommands: `serve`, `env-mcp`, `version`. Unknown → usage + exit 2.

  `serve` body for Task 1 (deliberately incomplete; Task 7 replaces it):
  1. `parseServeArgs(args[1:])`
  2. `LoadServeConfigFromEnv(flags)`
  3. Print `phase1 scaffold OK; listen=<cfg.ListenAddr> claudeBin=<cfg.ClaudeBin>` to stdout
  4. Exit 0

  Do NOT reference `ccappgateway.NewServer` yet — that package's
  `Server` type lands in Task 7. Task 1 is intentionally a working
  end-to-end build of the CLI scaffold (no HTTP listener) so it can
  commit independently. Task 7 replaces this 4-line body with the real
  `srv.Start(ctx)` flow.

  `env-mcp` body: print "env-mcp not implemented in phase 1" to stderr, exit 2.

  `version` body: print `cc-app-gateway version <BuildVersion> (<BuildSHA>)` and exit 0.

  Use `var BuildVersion, BuildSHA string` for `-ldflags` injection.

- [ ] **Step 7: Run tests + smoke-build**

  ```
  cd cmd/cc-app-gateway && go test ./...
  go build ./cmd/cc-app-gateway
  ./cc-app-gateway version
  ./cc-app-gateway serve --listen-addr :9999 --claude-bin /bin/true
  # expect "phase1 scaffold OK; listen=:9999 claudeBin=/bin/true" then exit 0
  # (Task 1 builds the CLI scaffold; Task 7 replaces this with the real
  # srv.Start(ctx) flow.)
  ./cc-app-gateway env-mcp        # expect "not implemented" exit 2
  ```

- [ ] **Step 8: Commit**

  `feat(cc-app-gateway): scaffold serve subcommand + config`

---

## Task 2: Auth middleware

**Files:**
- Create: `internal/ccappgateway/auth/auth.go`
- Create: `internal/ccappgateway/auth/auth_test.go`

**Scope:**
- `InternalSecretMiddleware(secret string) func(http.Handler) http.Handler` —
  validates `X-Internal-Secret` against `secret`. 401 on mismatch. If `secret`
  is empty, middleware is permissive (matches codex pattern; useful for local
  dev).
- `BearerMiddleware(...)` — Phase 1 stub that returns 501 Not Implemented
  if called with a `Bearer` Authorization header. Wired but unexercised; Phase
  5+ replaces this with real workspace-token validation.
- A composed middleware `Either(internal, bearer)` that tries internal first
  then bearer, used by `/api/turns`. Healthz/readyz are unauthenticated.

- [ ] **Step 1: Failing auth_test.go**

  Cases:
  - Missing `X-Internal-Secret` + secret configured → 401
  - Wrong `X-Internal-Secret` → 401
  - Matching `X-Internal-Secret` → next handler invoked
  - Empty `secret` config → permissive (next invoked regardless)
  - `Authorization: Bearer xyz` against BearerMiddleware → 501 with JSON `{"error":"bearer auth not implemented in phase 1"}`
  - `Either` falls through internal → bearer correctly

- [ ] **Step 2: Run test (expect fail)**

- [ ] **Step 3: Implement auth.go**

  Pattern: constant-time secret compare via `crypto/subtle.ConstantTimeCompare`.

- [ ] **Step 4: Run tests (expect pass)**

- [ ] **Step 5: Commit**

  `feat(cc-app-gateway): X-Internal-Secret auth middleware + Bearer stub`

---

## Task 3: wstoken_client (copy from codex-app-gateway)

**Files:**
- Create: `internal/ccappgateway/wstoken_client.go`
- Create: `internal/ccappgateway/wstoken_client_test.go`

**Scope:** Mirror `internal/codexappgateway/wstoken_client.go` (75 LOC).
The agentserver `POST /internal/workspace-token` endpoint and the
`proxy_tokens` table both serve codex and cc — same payload, same secret,
same response. Duplication is intentional per spec § Audit revisions: the
two gateways have separate release cadences and a single shared package
would couple their iteration loops. Adapt the package decl (and rename
struct/methods if the package context calls for it), but preserve the
HTTP contract bit-for-bit.

- [ ] **Step 1: Failing wstoken_client_test.go**

  Use `httptest.NewServer` returning canned JSON. Cases:
  - Happy path: `{token:"deadbeef"}` → GetOrCreate returns `"deadbeef"`
  - 401 → error mentions status code + body
  - 500 → error mentions status code
  - Empty token in response → error
  - Empty workspaceID → error before any HTTP call
  - Context cancellation → error wraps `context.Canceled`

- [ ] **Step 2: Run test (expect fail)**

- [ ] **Step 3: Copy + adapt wstoken_client.go**

  Verbatim copy except `package ccappgateway`. Re-read the spec's § Auth model
  to confirm no semantic drift from codex's pattern.

- [ ] **Step 4: Run tests (expect pass)**

- [ ] **Step 5: Commit**

  `feat(cc-app-gateway): wstoken_client mirrors codex pattern`

---

## Task 4: Workspace stub (mkdir + cleanup, no S3)

**Files:**
- Create: `internal/ccappgateway/workspace/workspace.go`
- Create: `internal/ccappgateway/workspace/workspace_test.go`

**Phase 1 scope (deliberately minimal):**
- `type Workspace struct { TempDir, ClaudeDir, ProjectDir string }`
- `func Setup(ctx context.Context, tmpRoot string) (*Workspace, error)` —
  mkdir `<tmpRoot>/<uuid>/{claude-home,project}` with 0700 perms; returns
  paths. UUID is a fresh v4. No S3 download.
- `func (*Workspace) Teardown() error` — `os.RemoveAll(w.TempDir)`. Idempotent
  (no error if already gone). Safe to call twice.
- Defer-able pattern: `ws, err := workspace.Setup(...); defer ws.Teardown()`.

Phase 2 will add S3 round-trip + snapshot/diff. Phase 1 keeps this skeleton so
the runner has somewhere to point `cmd.Dir`.

- [ ] **Step 1: Failing workspace_test.go**

  Cases:
  - Setup creates both subdirs with the right perms
  - Setup with non-existent tmpRoot creates it (or errors cleanly)
  - Teardown removes the tmpdir
  - Double Teardown is a no-op (no error)
  - Two concurrent Setup calls don't collide (UUIDs differ)

- [ ] **Step 2: Run test (expect fail)**

- [ ] **Step 3: Implement workspace.go**

  Use `crypto/rand` for UUID v4 (no third-party dep). `os.MkdirAll` for nested
  dirs.

- [ ] **Step 4: Run tests (expect pass)**

- [ ] **Step 5: Commit**

  `feat(cc-app-gateway): workspace stub (mkdir + teardown, no S3)`

---

## Task 5a: stream-json codec (Decode + EncodeUserMessage)

**Files:**
- Create: `internal/ccappgateway/runner/stream_json.go`
- Create: `internal/ccappgateway/runner/stream_json_test.go`
- Create: `internal/ccappgateway/runner/testdata/sample_transcript.jsonl` — copy of `/tmp/cc-probe/transcript.jsonl` (or a representative subset)

**Scope:**

```go
// SDKMessage is the wire shape claude --print --output-format stream-json emits.
// We keep raw json.RawMessage for the body so we don't have to mirror every
// content-block shape; runner/events.go does the keep/drop classification.
type SDKMessage struct {
    Type        string          `json:"type"`
    Subtype     string          `json:"subtype,omitempty"`
    SessionID   string          `json:"session_id,omitempty"`
    UUID        string          `json:"uuid,omitempty"`
    Message     json.RawMessage `json:"message,omitempty"`     // assistant/user frames
    Result      json.RawMessage `json:"result,omitempty"`      // result/* frames
    Raw         json.RawMessage `json:"-"`                     // verbatim line for logging
}

// Decode wraps a reader and returns a channel of SDKMessages. The error
// channel emits at most one error (EOF or parse failure), then closes.
// Caller should range the messages channel; on close, check the error
// channel for non-EOF errors.
func Decode(r io.Reader) (<-chan SDKMessage, <-chan error)

// EncodeUserMessage writes a single SDKUserMessage line to w (terminated
// with \n) for the user message text. Format from Phase 0 PoC:
//   {"type":"user","message":{"role":"user","content":[{"type":"text","text":"..."}]}}
func EncodeUserMessage(w io.Writer, text string) error
```

- [ ] **Step 1: Copy probe transcript into testdata**

  ```bash
  mkdir -p internal/ccappgateway/runner/testdata
  cp /tmp/cc-probe/transcript.jsonl internal/ccappgateway/runner/testdata/sample_transcript.jsonl
  ```

  If `/tmp/cc-probe/` was cleaned up, re-run probe (see Phase 0 FINDINGS.md) or hand-craft a subset covering: `system/init`, `assistant`, `user`(tool_result), `result/success`. ~10 frames is enough.

- [ ] **Step 2: Failing stream_json_test.go**

  Cases:
  - `EncodeUserMessage` produces exactly one line with the expected JSON shape (parseable + content[0].text round-trips)
  - `Decode` reading sample_transcript.jsonl yields N messages where N matches `wc -l`
  - Decoded messages have the right `Type` field for the first system/init and the last result/success
  - Decoder handles trailing newline + missing newline gracefully
  - Decoder error channel emits a parse-error for a malformed line (test with intentional `{not json}` line)

- [ ] **Step 3: Run test (expect fail)**

- [ ] **Step 4: Implement stream_json.go**

  Use `bufio.Scanner` with a 1MB buffer (claude frames are usually <10KB but
  stream_event with full message_start can be larger). Channel pattern: send
  to messages channel until error or EOF; then close messages, send error to
  errors channel (or nil for EOF), close errors.

- [ ] **Step 5: Run tests (expect pass)**

- [ ] **Step 6: Commit**

  `feat(cc-app-gateway): stream-json codec (encode/decode SDKMessage)`

---

## Task 5b: events classification + assistant-text extraction

**Files:**
- Create: `internal/ccappgateway/runner/events.go`
- Create: `internal/ccappgateway/runner/events_test.go`

**Scope:**

```go
// KeepFrame reports whether an SDKMessage should be retained for downstream
// processing or dropped as noise. Per Phase 0:
//   keep: system/init, assistant, user(tool_result), result/*
//   drop: stream_event, system/status, system/thinking_tokens
func KeepFrame(m SDKMessage) bool

// ResultMeta captures the closing-frame metadata we surface in the HTTP response.
type ResultMeta struct {
    Subtype       string             // "success" | "error" | etc
    IsError       bool
    DurationMs    int64
    TotalCostUSD  float64
    ModelUsage    map[string]ModelUsage
    ErrorMessage  string             // when IsError
}

// ExtractAssistantText drains the channel and returns the final assistant
// text plus result metadata. The "final assistant text" is the last
// non-empty text content in any assistant frame (NOT the result.result —
// see Phase 0 transcript: the assistant frame has cleaner content, and
// result.result occasionally summarizes/truncates).
// Returns an error if the channel closes without a result frame.
func ExtractAssistantText(in <-chan SDKMessage) (string, *ResultMeta, error)
```

- [ ] **Step 1: Failing events_test.go**

  Cases for KeepFrame:
  - Every frame type from sample_transcript classifies correctly
  - Unknown type defaults to keep + log warning (we'd rather over-log than drop new data; warning is observable via test capture)

  Cases for ExtractAssistantText:
  - Sample transcript → extracts the assistant text matching `result.result` (close enough — assert one or the other works)
  - Channel with only a result/error → returns error + populated ResultMeta with IsError=true
  - Channel closing without any result frame → error wrapping `io.ErrUnexpectedEOF`
  - Empty channel → error

- [ ] **Step 2: Run test (expect fail)**

- [ ] **Step 3: Implement events.go**

- [ ] **Step 4: Run tests (expect pass)**

- [ ] **Step 5: Commit**

  `feat(cc-app-gateway): SDKMessage classification + assistant text extraction`

---

## Task 6: runner (args/env builder + spawn + drive)

**Files:**
- Create: `internal/ccappgateway/runner/options.go`
- Create: `internal/ccappgateway/runner/options_test.go`
- Create: `internal/ccappgateway/runner/runner.go`
- Create: `internal/ccappgateway/runner/runner_test.go`
- Create: `internal/ccappgateway/runner/testdata/fake_claude.go` — Go test helper that mimics claude --print

**Scope of options.go:**

```go
type RunInput struct {
    ClaudeBin   string        // from config
    ClaudeDir   string        // from workspace (becomes CLAUDE_CONFIG_DIR)
    ProjectDir  string        // from workspace (becomes cmd.Dir)
    SessionID   string
    Model       string
    UserMessage string
    WSToken     string
    LLMProxyURL string
    Timeout     time.Duration
}

func BuildArgs(in RunInput) []string  // returns the exact flag list
func BuildEnv(in RunInput, parentEnv []string) []string  // returns env vars
```

`BuildArgs` (Phase 1, no MCP, no resume):
```
--print
--input-format stream-json
--output-format stream-json
--verbose
--permission-mode bypassPermissions
--dangerously-skip-permissions
--model <Model>
--session-id <SessionID>
```

`BuildEnv` (inherits parentEnv minus any ANTHROPIC_* / CLAUDE_* keys to avoid
leakage; then sets):
```
CLAUDE_CONFIG_DIR=<ClaudeDir>
IS_SANDBOX=1
CLAUDE_CODE_AUTO_COMPACT_WINDOW=165000
CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING=1
ANTHROPIC_AUTH_TOKEN=<WSToken>
ANTHROPIC_BASE_URL=<LLMProxyURL>
```

**Scope of runner.go:**

```go
type RunResult struct {
    AssistantText string
    Meta          *ResultMeta
    DurationMs    int64
    ExitCode      int
}

// Run spawns claude with BuildArgs/BuildEnv, writes one SDKUserMessage to
// stdin, closes stdin, reads SDKMessage frames from stdout, extracts the
// final assistant text. stderr is logged.  Wall-clock timeout enforced via
// context; SIGTERM on timeout, SIGKILL after 5s grace.
func Run(ctx context.Context, in RunInput) (*RunResult, error)
```

**Fake claude binary:** test-only helper. A small Go program built with
`go test -c` into a binary that:
- Reads one line from stdin (the SDKUserMessage)
- Echoes a hardcoded canned stream-json transcript to stdout (loaded from
  testdata/canned_transcript.jsonl)
- Exits 0

Tests reference this binary via `os.Args[0]` trick + `TestHelperProcess`
pattern (stdlib `os/exec` docs example).

- [ ] **Step 1: Failing options_test.go**

  Cases:
  - BuildArgs has every required flag in the right order
  - BuildArgs does NOT include `--mcp-config` (Phase 1 skips MCP)
  - BuildArgs does NOT include `--resume` (Phase 1 first-turn-only)
  - BuildEnv has all required vars
  - BuildEnv strips parent's `ANTHROPIC_AUTH_TOKEN` (no leakage from gateway env)
  - BuildEnv strips parent's `CLAUDE_CODE_REMOTE*` (those are for managed-harness mode, not what we want here)

- [ ] **Step 2: Failing runner_test.go**

  Cases (using fake_claude helper):
  - Happy path: returns RunResult with extracted text matching the canned transcript
  - Subprocess exits non-zero → error wraps exit code
  - Timeout: 1s timeout vs canned transcript with a sleep → SIGTERM, error
    mentions context.DeadlineExceeded
  - stdin write fails (subprocess exits before reading) → error doesn't panic
  - Malformed stream-json from subprocess → returned in error, not silently
    dropped

- [ ] **Step 3: Run tests (expect fail)**

- [ ] **Step 4: Implement options.go**

- [ ] **Step 5: Implement runner.go + fake_claude helper**

- [ ] **Step 6: Run tests (expect pass)**

- [ ] **Step 7: Manual smoke against real claude**

  ```bash
  go build -o /tmp/cc-runner-smoke ./internal/ccappgateway/runner/cmd/smoke
  # (write a tiny smoke main.go calling runner.Run with hardcoded inputs +
  #  CCAPPGW_LLMPROXY_URL pointing at the dev llmproxy)
  # Verify it gets a real "pong" back from claude.
  ```

  This is paranoia for the integration boundary; tests use a fake but at least
  one human-driven real-claude call before moving on is worth ten test cases.

- [ ] **Step 8: Commit**

  `feat(cc-app-gateway): runner spawns claude + drives stream-json IO`

---

## Task 7: turn_api + server (chi router + healthz + /api/turns)

**Files:**
- Create: `internal/ccappgateway/turn_api.go`
- Create: `internal/ccappgateway/turn_api_test.go`
- Create: `internal/ccappgateway/server.go`
- Create: `internal/ccappgateway/server_test.go`
- Modify: `cmd/cc-app-gateway/main.go` (replace "phase1 scaffold OK" stub
  with `srv.Start(ctx)`)

**turn_api.go scope:**

```go
type TurnHandler struct {
    cfg           ServeConfig
    wstoken       *WorkspaceTokenClient
    runner        runner.Func  // injected for testability
}

type CcTurnRequest struct {
    WorkspaceID string `json:"workspaceId"`
    SessionID   string `json:"sessionId"`
    UserMessage string `json:"userMessage"`
    Model       string `json:"model,omitempty"`
    TimeoutMs   int    `json:"timeoutMs,omitempty"`
    CallbackURL string `json:"callbackUrl,omitempty"`  // Phase 4; Phase 1 returns 501 if set
}

type CcTurnResponse struct {
    SessionID     string                       `json:"sessionId"`
    AssistantText string                       `json:"assistantText"`
    IsError       bool                         `json:"isError"`
    DurationMs    int64                        `json:"durationMs"`
    TotalCostUSD  float64                      `json:"totalCostUsd"`
    ModelUsage    map[string]runner.ModelUsage `json:"modelUsage,omitempty"`
}

func (h *TurnHandler) ServeHTTP(w http.ResponseWriter, r *http.Request)
```

Flow:
1. Decode request; validate (workspaceID + sessionID + userMessage required;
   sessionID must be valid UUID; userMessage <100KB)
2. If `CallbackURL != ""` → 501 (Phase 4)
3. Fetch workspace token (5s deadline)
4. workspace.Setup
5. Build RunInput, call runner.Run (with per-turn ctx + timeout)
6. defer workspace.Teardown (synchronous in Phase 1; Phase 2 makes it async)
7. Marshal CcTurnResponse

Error mapping:
- wstoken fetch failure → 502 (upstream agentserver down)
- workspace setup failure → 500
- runner error (subprocess crashed) → 500 with code "runner_failed"
- runner timeout → 504 with code "runner_timeout"
- ResultMeta.IsError true → 200 with isError=true (Anthropic-side error; we
  succeeded in running the turn)

**server.go scope:**

```go
type Server struct { /* cfg, http.Server, chi.Router */ }
func NewServer(cfg ServeConfig) (*Server, error)
func (s *Server) Start(ctx context.Context) error
func (s *Server) Shutdown(ctx context.Context) error
func (s *Server) Routes() chi.Router  // exposed for tests
```

Routes:
- `GET /healthz` → 200 "ok"
- `GET /readyz` → 200 if (a) claude binary present + executable, AND (b) wstoken
  endpoint responds. Else 503 with a small JSON body listing which check failed.
- `POST /api/turns` → wrapped in `Either(Internal, Bearer)` middleware,
  calls TurnHandler.ServeHTTP

- [ ] **Step 1: Failing turn_api_test.go**

  Use a fake runner.Func that returns a canned RunResult; httptest for the
  wstoken endpoint. Cases:
  - Happy path: valid request → 200 CcTurnResponse with expected fields
  - Missing workspaceID → 400
  - Invalid sessionID (not a UUID) → 400
  - User message empty → 400
  - User message >100KB → 413
  - CallbackURL set → 501 with JSON body
  - wstoken endpoint returns 500 → 502 from /api/turns
  - Fake runner returns error → 500 with code "runner_failed"
  - Fake runner returns ResultMeta.IsError=true → 200 with isError=true

- [ ] **Step 2: Failing server_test.go**

  Cases:
  - Healthz returns 200 unauthenticated
  - Readyz returns 503 if wstoken endpoint is down (httptest server stopped)
  - Readyz returns 503 if claude binary path doesn't exist
  - Readyz returns 200 in happy case
  - /api/turns without X-Internal-Secret → 401
  - Shutdown drains in-flight turns within grace period

- [ ] **Step 3: Run tests (expect fail)**

- [ ] **Step 4: Implement turn_api.go**

- [ ] **Step 5: Implement server.go**

- [ ] **Step 6: Wire main.go properly**

  Replace "phase1 scaffold OK" stub:
  ```go
  srv, err := ccappgateway.NewServer(cfg)
  if err != nil { ... fatal ... }
  if err := srv.Start(ctx); err != nil { ... fatal ... }
  ```
  Add SIGTERM handler that calls `srv.Shutdown(ctx-with-30s-deadline)`.

- [ ] **Step 7: Run tests + manual smoke**

  ```
  go test ./internal/ccappgateway/...
  go build ./cmd/cc-app-gateway
  CCAPPGW_LLMPROXY_URL=... INTERNAL_API_SECRET=... AGENTSERVER_INTERNAL_URL=... ./cc-app-gateway serve
  # In another shell:
  curl -sX POST http://localhost:8087/api/turns -H "X-Internal-Secret: ..." \
       -d '{"workspaceId":"...","sessionId":"...","userMessage":"Say only the word: pong"}'
  ```

- [ ] **Step 8: Commit**

  `feat(cc-app-gateway): /api/turns synchronous handler + chi server`

---

## Task 8: Dockerfile + helm chart + CI

**Files:**
- Create: `Dockerfile.cc-app-gateway`
- Create: `deploy/helm/agentserver/templates/cc-app-gateway.yaml`
- Modify: `deploy/helm/agentserver/values.yaml` (add `ccAppGateway` block)
- Modify: `.github/workflows/build.yml` (add `build-cc-app-gateway` job; add
  to `publish-helm` needs list)

**Dockerfile pattern** (mirrors `Dockerfile.claudecode` + codex-app-gateway):

```dockerfile
# syntax=docker/dockerfile:1.7

FROM golang:1.26-trixie AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG SHA=unknown
RUN CGO_ENABLED=0 go build \
    -ldflags "-X main.BuildVersion=${VERSION} -X main.BuildSHA=${SHA}" \
    -o /out/cc-app-gateway ./cmd/cc-app-gateway

FROM debian:bookworm-slim
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates curl && \
    curl -fsSL https://claude.ai/install.sh | bash -s -- 2.1.185 && \
    cp /root/.local/bin/claude /usr/local/bin/claude && \
    rm -rf /root/.local /var/lib/apt/lists/* && \
    apt-get purge -y curl && apt-get autoremove -y
COPY --from=build /out/cc-app-gateway /usr/local/bin/cc-app-gateway
EXPOSE 8087
ENTRYPOINT ["/usr/local/bin/cc-app-gateway"]
CMD ["serve"]
```

> ⚠️ The `claude.ai/install.sh` script doesn't support pinning to an exact
> version via `-s -- 2.1.185` per its current contract. **Verify in Task 8
> Step 1** before assuming. If unsupported, options:
> (a) bake an explicit version pin by downloading the binary directly from the
>     versioned URL the install script uses internally
> (b) commit a `claude-install-pinned.sh` that wraps it
> Pick whichever Dockerfile.claudecode itself currently uses for version pinning.

**Helm values.yaml block** (add):

```yaml
ccAppGateway:
  enabled: false                      # Feature flag; off by default
  image:
    repository: ghcr.io/agentserver/cc-app-gateway
    tag: latest
    pullPolicy: Always
  replicaCount: 1
  port: 8087
  logLevel: info
  defaultModel: haiku
  turnTimeout: 10m
  resources:
    requests:
      memory: "512Mi"
      cpu: "500m"
    limits:
      memory: "2Gi"
      cpu: "2000m"
```

**Helm template** (`cc-app-gateway.yaml`): Service + Deployment, mirror
codex-app-gateway.yaml structure. Env vars from values plus references to the
existing INTERNAL_API_SECRET secret and the chart-computed agentserver URL.
Probes: `/healthz` liveness, `/readyz` readiness with 5s period.

**CI job** (in `.github/workflows/build.yml`):

```yaml
build-cc-app-gateway:
  runs-on: ubuntu-latest
  needs: [test]
  steps:
    - uses: actions/checkout@v6
    - uses: docker/setup-buildx-action@v4
    - uses: docker/login-action@v4
      with: { registry: ghcr.io, username: ${{ github.actor }}, password: ${{ secrets.GITHUB_TOKEN }} }
    - uses: docker/metadata-action@v6
      id: meta
      with:
        images: ghcr.io/agentserver/cc-app-gateway
        tags: |
          type=sha,prefix=
          type=ref,event=branch
          type=semver,pattern={{version}}
          type=semver,pattern={{major}}.{{minor}}
          type=raw,value=latest,enable={{is_default_branch}}
    - uses: docker/build-push-action@v7
      with:
        context: .
        file: ./Dockerfile.cc-app-gateway
        push: true
        tags: ${{ steps.meta.outputs.tags }}
        labels: ${{ steps.meta.outputs.labels }}
        cache-from: type=gha
        cache-to: type=gha,mode=max
```

Also add `build-cc-app-gateway` to `publish-helm`'s `needs:` list (so chart
bump can wait on the image).

- [ ] **Step 1: Investigate claude install version pinning**

  ```bash
  # On a clean throwaway container:
  docker run --rm debian:bookworm-slim bash -c "
    apt-get update && apt-get install -y --no-install-recommends ca-certificates curl &&
    curl -fsSL https://claude.ai/install.sh | bash -s -- 2.1.185
  "
  ```
  Confirm whether 2.1.185 lands. If not, look at how `Dockerfile.claudecode`
  pins (read it). Document the chosen pinning approach in a comment in
  `Dockerfile.cc-app-gateway`.

- [ ] **Step 2: Write Dockerfile.cc-app-gateway**

  Build it locally:
  ```bash
  docker build -f Dockerfile.cc-app-gateway -t cc-app-gateway:dev .
  docker run --rm cc-app-gateway:dev version
  docker run --rm cc-app-gateway:dev serve --help
  ```

- [ ] **Step 3: Write helm template**

  Lint:
  ```bash
  cd deploy/helm/agentserver
  helm lint .
  helm template . --set ccAppGateway.enabled=true | grep -A 30 "name: .*cc-app-gateway"
  ```
  Verify the Deployment / Service render with the right ports + env vars.

- [ ] **Step 4: Add values.yaml block + CI job**

- [ ] **Step 5: Smoke-build CI locally**

  ```bash
  # If you have act installed:
  act -j build-cc-app-gateway -n  # dry-run
  ```
  Otherwise just visually diff against `build-codex-app-gateway`.

- [ ] **Step 6: Commit**

  `feat(cc-app-gateway): Dockerfile + helm chart + CI build job`

---

## Task 9: docker-compose integration test (fakes + real claude)

**Files:**
- Create: `internal/ccappgateway/integration_test.go` (build tag `integration`)
- Create: `internal/ccappgateway/testdata/integration/docker-compose.yml`
- Create: `internal/ccappgateway/testdata/integration/Makefile` (helper to bring up + tear down stack)
- Optional: a small `cmd/cc-app-gateway-test-tools/main.go` providing
  fake-agentserver + fake-llmproxy subcommands (single binary, two faces;
  ~250 LOC)

**Scope:** Spin up the docker-compose harness from the spec's § Acceptance
section. Drive `/api/turns` from Go; assert real claude returns "pong".

The integration test is `//go:build integration` so it doesn't run by default
(needs docker + a built image). Make CI run it on PRs that touch
`internal/ccappgateway/` or `Dockerfile.cc-app-gateway`.

- [ ] **Step 1: Build fake-agentserver + fake-llmproxy**

  Single binary `cc-app-gateway-test-tools` with two subcommands. Tiny —
  fake-agentserver only needs to serve `POST /internal/workspace-token`;
  fake-llmproxy only needs to accept the Anthropic-protocol POST and return a
  canned `{content: [{type:"text", text:"pong"}]}` response (mimicking real
  Anthropic API; claude needs the proper message_start/delta/stop streaming
  shape too if we use --include-partial-messages, but Phase 1 doesn't).

  ```bash
  go build -o /tmp/test-tools ./cmd/cc-app-gateway-test-tools
  /tmp/test-tools fake-agentserver --listen :8080 --workspace-token deadbeef
  /tmp/test-tools fake-llmproxy --listen :8081 --accept-token deadbeef --canned-reply "pong"
  ```

- [ ] **Step 2: Write docker-compose.yml**

  Per spec § Acceptance. Three services. Use `image: cc-app-gateway:dev` (
  build via `docker build` in the Makefile before bringing up).

- [ ] **Step 3: Write integration_test.go**

  Test body:
  1. `exec.Command("make", "-C", testdataPath, "up")` (with timeout)
  2. Wait for cc-app-gateway readyz (HTTP poll with 30s deadline)
  3. POST /api/turns
  4. Assert response shape matches expected; `assistantText == "pong"`
  5. `defer exec.Command("make", "-C", testdataPath, "down")`

  Build tag: `//go:build integration`. Run with `go test -tags integration ./internal/ccappgateway/...`.

- [ ] **Step 4: Run integration test locally**

  ```bash
  docker build -f Dockerfile.cc-app-gateway -t cc-app-gateway:dev .
  go test -tags integration -v -run TestIntegration ./internal/ccappgateway/...
  ```

- [ ] **Step 5: Wire CI**

  Add an `integration-cc-app-gateway` job in `.github/workflows/build.yml`
  that depends on `build-cc-app-gateway` (or rebuilds locally), runs the
  test, only on PRs that change relevant paths.

- [ ] **Step 6: Document**

  Add a paragraph to `docs/superpowers/specs/2026-06-21-cc-app-gateway-design.md`
  § Acceptance pointing at this test harness.

- [ ] **Step 7: Commit**

  `test(cc-app-gateway): docker-compose integration harness with fakes + real claude`

---

## Final pass

- [ ] **Run full test suite from repo root**

  ```bash
  go test ./...                                # all unit tests
  go test -tags integration ./internal/ccappgateway/...  # integration
  go vet ./...
  ```

- [ ] **Update memory**

  Add a Phase-1-complete note to `/root/.claude/projects/-root-agentserver/memory/`:
  cross-link with `cc_v2_1_185_gateway_feasibility.md` and
  `cc_print_stream_json_probe.md`; mark this plan as the implementation of
  the spec.

- [ ] **Open PR**

  PR description must:
  - Mention this is the spec's Phase 1
  - Link to spec + plan
  - Call out that this *reverses PR #135 in part* (per spec § Header) — the
    purge commit's "the Anthropic Claude Code public-binary path was
    abandoned" claim is being narrowed to "abandoned as a TUI thin-client
    target", not "abandoned as a managed harness target"
  - Show the `curl` output from the integration test as evidence
  - Note Phase 2-5 plans are deferred

- [ ] **Bump chart (post-merge)**

  Per `agentserver_release_flow` memory: bump `Chart.yaml` version, merge,
  push `v<version>` git tag so pulumi can pull images.

---

## Out-of-band: rerun Phase 0 probe before starting

The probe transcript at `/tmp/cc-probe/transcript.jsonl` was captured on
2026-06-21 against claude 2.1.185. If a newer claude has shipped by the
time Phase 1 starts, **rerun the probe first** to capture a current
transcript (testdata for Task 5):

```bash
cd /tmp/cc-probe && go build -o probe ./probe.go
./probe fresh 'Say only the word: pong'
ls -la /tmp/cc-probe/transcript.jsonl
```

If the new transcript reveals schema changes (new frame types, removed fields,
flag-name changes), STOP. Update the spec's § Phase 0 PoC log to record the
delta and decide whether to (a) accommodate in runner/events.go, (b) pin to
the older claude version in Dockerfile, or (c) extend Phase 0 to revalidate
all four PoC assumptions on the new version. Don't push forward on Phase 1
against a spec written for an older binary.
