# cc-app-gateway Phase 3: env-MCP wiring

**Status:** Audited + Phase 0 verified (v3)
**Date:** 2026-06-22
**Author:** SDD-built
**Builds on:** Phase 1 (#279), Phase 2 (#280), Phase 4 (#281), chart fixes (#283, #285)
**Self-audit:** 7 critical + 7 important + 5 minor; v2 below incorporates all critical + important fixes.

## Goal

Wire env-MCP into cc-app-gateway so claude (via the managed_cc IM path) can call workspace env tools (shell, exec_command, read_file, apply_patch, copy_path, list_environments, scheduled tasks) — feature parity with the codex path for the same workspace.

After this lands, WeChat users on `routing_mode=managed_cc` channels can ask claude to do real work in their workspace executors, not just chat / write temp files in the ephemeral pod-local dir.

## Non-goals

- New env tools beyond what env-mcp already exposes (`apply_patch`, `read_file`, `shell`, etc.).
- A cc-specific env-mcp variant — Phase 3 reuses the existing `codex-app-gateway env-mcp` binary as-is.
- Bidirectional handoff between codex and managed_cc for an in-progress conversation (Phase 4 Open Risk #8 stands: switching routing_mode = new session).
- HTTP/SSE MCP (Phase 3 sticks with stdio per Phase 0 probe).

## Phase 0 probe results (v2, completed before code)

3 unknowns verified at `/tmp/cc-probe-v2/`:

| Probe | Result | Spec impact |
|---|---|---|
| P0-A: `--tools mcp__<server>__*` glob | ✅ Works | Use `--tools mcp__agentserver__*` — no enumeration needed |
| P0-B: mcp.json `env` block honored by claude | ✅ Yes; child ALSO inherits claude's parent env | Use env block for cap-token (explicit, auditable). NB: claude's full env passes through too |
| P0-C: `bypassPermissions` blanket-covers MCP tools | ✅ Yes | NO `--allowedTools`, NO per-tool list, NO readOnlyHint needed |

**Pinned claude flags for Phase 3**:
```
--permission-mode bypassPermissions
--dangerously-skip-permissions
--mcp-config <path>
--strict-mcp-config
--tools mcp__agentserver__*
```

**P0-B security note**: env-mcp child inherits claude's full parent env (in addition to mcp.json `env` block). cc-app-gateway's `envAllowlist` (runner/options.go:83) only lets PATH/HOME/USER/LANG/etc. + the 6 explicitly-set `ANTHROPIC_*` / `CLAUDE_*` / `IS_SANDBOX` vars through — `CXG_*` are NOT in the allowlist, so they won't leak into env-mcp accidentally. The mcp.json `env` block remains the SOLE intended path for `CXG_WORKSPACE_TOKEN` and `CXG_EXEC_GATEWAY_INTERNAL_SECRET`.

Env-mcp tool inventory (for reference; not used as allowlist since glob suffices):
- `list_environments`, `shell`, `unified_exec`, `write_stdin`, `read_output`, `terminate`, `read_file`, `apply_patch`, `copy_path`
- Scheduling: `schedule_task`, `list_tasks`, `update_task`, `cancel_task`, `pause_task`, `resume_task`

## Architecture

```
WeChat msg
   │
   ▼
imbridge ──POST──▶ agentserver /api/internal/imbridge/cc/turn
                       │
                       ▼ ccDispatcher → ccInboundHandler
                       │
                       ▼ POST cc-app-gateway /api/turns
                            │
                            ▼ TurnHandler.ServeHTTP
                              │
                              ├─ AcquireSessionLock
                              ├─ workspace.Setup (S3 download)
                              ├─ mint cap-token (HMAC, per-turn) ────────┐  NEW (Phase 3)
                              ├─ mcp.WriteConfig(<TempDir>/mcp.json)   │  NEW
                              ├─ runner.Run(claude --print --mcp-config) │
                              │                                          │
                              │  ┌──── claude subprocess ──────────┐    │
                              │  │                                 │    │
                              │  │ reads mcp.json                  │    │
                              │  │ forks env-mcp stdio child       │    │
                              │  │   │                             │    │
                              │  │   └─ codex-app-gateway env-mcp  │    │
                              │  │      WorkspaceID, ExecGwURL,    │◀───┘
                              │  │      CXG_WORKSPACE_TOKEN env    │
                              │  │      → ws://codex-exec-gateway  │
                              │  │      → http://agentserver-main  │
                              │  └─────────────────────────────────┘
                              │
                              └─ Teardown (goroutine, S3 upload + mutex release)
```

Key insight: env-mcp is **codex-agnostic** — it talks to `codex-exec-gateway` and `agentserver-main` over standard internal APIs. The "codex" in `codex-app-gateway env-mcp` is just where the binary lives, not who can use it. Phase 3 ships the binary into the cc-app-gateway container and invokes its `env-mcp` subcommand directly.

## Code shape

### New: `internal/ccappgateway/mcp/config.go`

```go
package mcp

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
)

// ConfigInput carries everything mcp.WriteConfig needs to render
// the per-turn mcp.json file claude reads via --mcp-config.
type ConfigInput struct {
    EnvMcpBinary              string // absolute path to codex-app-gateway binary
    WorkspaceID               string
    ExecGatewayBridgeURL      string // ws:// URL with "/bridge" suffix already appended
    ExecGatewayInternalURL    string // http:// base URL
    ExecGatewayInternalSecret string // optional; HTTP relay shared key
    AgentserverInternalURL    string // http:// base URL
    WorkspaceCapToken         string // per-turn HMAC cap-token (NOT the proxy token)
    LogFile                   string // optional; env-mcp --log-file (use "/dev/stderr" for kubectl-logs-visible)
}

// WriteConfig renders mcp.json into dir and returns the absolute path.
// Mode 0600 — the cap-token in env block is sensitive.
//
// JSON server key is "agentserver" (matches the env-mcp MCPServer self-name
// at internal/codexappgateway/envmcp/envmcp.go around `NewMCPServer("agentserver", ...)`).
// claude's --tools / --allowedTools allowlist uses prefix mcp__agentserver__<tool>;
// mismatching the JSON key would cause every tool call to be rejected.
func WriteConfig(dir string, in ConfigInput) (string, error) {
    if in.EnvMcpBinary == "" {
        return "", fmt.Errorf("mcp.WriteConfig: EnvMcpBinary required")
    }
    if in.WorkspaceID == "" {
        return "", fmt.Errorf("mcp.WriteConfig: WorkspaceID required")
    }
    if in.WorkspaceCapToken == "" {
        return "", fmt.Errorf("mcp.WriteConfig: WorkspaceCapToken required")
    }
    args := []string{
        "env-mcp",
        "--workspace-id", in.WorkspaceID,
        "--exec-gateway-url", in.ExecGatewayBridgeURL,  // must end with /bridge — caller's responsibility
        "--exec-gateway-internal-url", in.ExecGatewayInternalURL,
        "--agentserver-internal-url", in.AgentserverInternalURL,
        "--workspace-token-env", "CXG_WORKSPACE_TOKEN",
    }
    env := map[string]string{"CXG_WORKSPACE_TOKEN": in.WorkspaceCapToken}
    if in.ExecGatewayInternalSecret != "" {
        args = append(args, "--exec-gateway-internal-secret-env", "CXG_EXEC_GATEWAY_INTERNAL_SECRET")
        env["CXG_EXEC_GATEWAY_INTERNAL_SECRET"] = in.ExecGatewayInternalSecret
    }
    if in.LogFile != "" {
        args = append(args, "--log-file", in.LogFile)
    }
    payload := map[string]any{
        "mcpServers": map[string]any{
            "agentserver": map[string]any{
                "command": in.EnvMcpBinary,
                "args":    args,
                "env":     env,
            },
        },
    }
    b, err := json.Marshal(payload)
    if err != nil {
        return "", fmt.Errorf("marshal: %w", err)
    }
    path := filepath.Join(dir, "mcp.json")
    if err := os.WriteFile(path, b, 0o600); err != nil {
        return "", fmt.Errorf("write %s: %w", path, err)
    }
    return path, nil
}
```

### Cap-token: direct import from `internal/captoken`

cc-app-gateway imports `github.com/agentserver/agentserver/internal/captoken` directly (the package lives at top-level `internal/captoken`, NOT under `internal/codexappgateway/captoken`). codex-app-gateway already imports it from the same path (see `internal/codexappgateway/server.go` around `captoken.Mint(...)`). No wrapper needed.

`shortid` package: import path `github.com/agentserver/agentserver/internal/shortid` (matches codex's `cmd/codex-app-gateway/main.go`).

### Modify: `internal/ccappgateway/runner/options.go`

Append `RunInput` field:

```go
// MCPConfigPath, when non-empty, adds --mcp-config <path>, --strict-mcp-config,
// and --tools mcp__agentserver__* to the claude args. Phase 3.
// (Per Phase 0 v2: glob works, --allowedTools redundant, no per-tool list needed.)
MCPConfigPath string
```

`BuildArgs` appends `--mcp-config <path>`, `--strict-mcp-config`, `--tools mcp__agentserver__*` when `MCPConfigPath != ""`. Backward-compat: Phase 1/2 callers leave it empty → no change.

The MCP server name `agentserver` is hardcoded because env-mcp's `NewMCPServer("agentserver", ...)` self-name is hardcoded; the allowlist prefix MUST match.

### Modify: `internal/ccappgateway/config.go`

Add fields (env var → struct):

| env var | field | required |
|---|---|---|
| `CCAPPGW_ENV_MCP_BINARY` | `EnvMcpBinary` | Phase 3 only |
| `CCAPPGW_EXEC_GATEWAY_WS_URL` | `ExecGatewayWSURL` (ws:// **without** /bridge suffix; cc-app-gateway appends) | Phase 3 only |
| `CCAPPGW_EXEC_GATEWAY_INTERNAL_URL` | `ExecGatewayInternalURL` (http://) | Phase 3 only |
| `CCAPPGW_EXEC_GATEWAY_INTERNAL_SECRET` | `ExecGatewayInternalSecret` | optional |
| `CCAPPGW_AGENTSERVER_INTERNAL_URL` | `AgentserverInternalURL` | Phase 3 only |
| `CCAPPGW_CAPTOKEN_HMAC_SECRET` | `CapTokenHMACSecret` ([]byte) | Phase 3 only |
| `CCAPPGW_CAPTOKEN_TTL` | `CapTokenTTL` (time.Duration; default `time.Hour`, matching codex `server.go` cap default) | optional |

"Phase 3 only" = required when `Cfg.EnvMcpBinary != ""`. If all are empty, behavior is identical to Phase 2 (no MCP, no cap-token mint). Lets us ship the chart with env-mcp opt-in.

URL construction note: cc-app-gateway's `NewServer` (or `TurnHandler.ServeHTTP` per turn) computes `bridgeURL := strings.TrimRight(cfg.ExecGatewayWSURL, "/") + "/bridge"` and passes that to `mcp.WriteConfig`. Mirrors codex's `internal/codexappgateway/server.go` line ~187. Keeps the chart value matching codex's `codexAppGateway.execGatewayWsUrl` shape exactly so operators don't have to set two different URLs.

### Modify: `internal/ccappgateway/turn_api.go`

Between `workspace.Setup` and `runner.Run`:

```go
var (
    mcpConfigPath    string
    mcpToolAllowlist string
    mcpToolFlag      string
)
if h.Cfg.EnvMcpBinary != "" {
    // Mint per-turn cap-token from the SAME HMAC secret codex shares.
    capTok, err := captoken.Mint(h.Cfg.CapTokenHMACSecret, captoken.Payload{
        TurnID:      "trn_" + shortid.Generate(),
        WorkspaceID: req.WorkspaceID,
        UserID:      "", // cc-app-gateway is internal-secret authed; no enduser context.
                         // captoken.Mint accepts UserID=""; codex-exec-gateway's
                         // VerifyCapabilityToken does NOT require non-empty UserID
                         // (see codexexecgateway auth_test.go
                         // TestVerifyCapabilityToken_OldTokenHasNoUserID).
    }, h.Cfg.CapTokenTTL)
    if err != nil {
        log.Printf("[cc-app-gateway] mint_captoken_failed (session=%s workspace=%s): %v", req.SessionID, req.WorkspaceID, err)
        writeError(w, http.StatusInternalServerError, "captoken_failed", "internal authorization failure")
        return
    }
    bridgeURL := strings.TrimRight(h.Cfg.ExecGatewayWSURL, "/") + "/bridge"
    p, err := mcp.WriteConfig(ws.TempDir, mcp.ConfigInput{
        EnvMcpBinary:              h.Cfg.EnvMcpBinary,
        WorkspaceID:               req.WorkspaceID,
        ExecGatewayBridgeURL:      bridgeURL,
        ExecGatewayInternalURL:    h.Cfg.ExecGatewayInternalURL,
        ExecGatewayInternalSecret: h.Cfg.ExecGatewayInternalSecret,
        AgentserverInternalURL:    h.Cfg.AgentserverInternalURL,
        WorkspaceCapToken:         capTok,
        LogFile:                   "/dev/stderr", // pod-stderr → kubectl logs; survives Teardown
    })
    if err != nil {
        log.Printf("[cc-app-gateway] mcp_config_write_failed (session=%s): %v", req.SessionID, err)
        writeError(w, http.StatusInternalServerError, "mcp_config_failed", "internal MCP config failure")
        return
    }
    mcpConfigPath = p
}
```

Pass `MCPConfigPath: mcpConfigPath` to `runner.RunInput`. Tool allowlist hardcoded in `BuildArgs` per Phase 0 v2.

### Modify: `internal/ccappgateway/server.go` (NewServer)

Validate config when env-mcp is enabled. If `EnvMcpBinary != ""` AND any of `ExecGatewayWSURL` / `ExecGatewayInternalURL` / `AgentserverInternalURL` / `CapTokenHMACSecret` is empty → return error at startup (CrashLoopBackOff). Also validate `CapTokenTTL > TurnTimeout` (TTL=1h, TurnTimeout=10m by default — wide margin). Misconfig fails fast, not silently disabled.

### Modify: `Dockerfile.cc-app-gateway`

The repo currently builds the cc-app-gateway image from a Dockerfile under `Dockerfile.cc-app-gateway` (or similar — IMPLEMENTATION TASK: locate by `grep -l "cc-app-gateway" Dockerfile* deploy/`). To embed the env-mcp binary:

1. Add a build stage that compiles codex-app-gateway: `go build -o /out/codex-app-gateway ./cmd/codex-app-gateway`
2. In the final stage: `COPY --from=<codex-builder> /out/codex-app-gateway /usr/local/bin/codex-app-gateway`

Verify the cc-app-gateway image size delta (~30 MB for the codex binary; acceptable).

**Decision**: ship the existing codex-app-gateway binary as-is (no fork, no shim). Zero new build artifact. cc and codex must build from the same Git commit (already true: `.github/workflows/build.yml` runs all builds in one workflow run per push), so binary version drift is impossible within a release.

### Modify: `deploy/helm/agentserver/templates/cc-app-gateway.yaml`

Two changes: (1) hard-gate that codex-exec-gateway is enabled (since env-mcp's whole reason for existing is to dial it); (2) the env block itself.

```yaml
{{- if .Values.ccAppGateway.envMcp.enabled }}
{{- if not .Values.codexExecGateway.enabled }}{{- fail "ccAppGateway.envMcp.enabled=true requires codexExecGateway.enabled=true (env-mcp dials codex-exec-gateway)" }}{{- end }}
- name: CCAPPGW_ENV_MCP_BINARY
  value: "/usr/local/bin/codex-app-gateway"
- name: CCAPPGW_EXEC_GATEWAY_WS_URL
  value: "ws://{{ .Release.Name }}-codex-exec-gateway.{{ .Release.Namespace }}.svc:{{ .Values.codexExecGateway.port }}"
- name: CCAPPGW_EXEC_GATEWAY_INTERNAL_URL
  value: "http://{{ .Release.Name }}-codex-exec-gateway.{{ .Release.Namespace }}.svc:{{ .Values.codexExecGateway.port }}"
- name: CCAPPGW_AGENTSERVER_INTERNAL_URL
  value: "http://{{ .Release.Name }}.{{ .Release.Namespace }}.svc:{{ .Values.service.port }}"
- name: CCAPPGW_CAPTOKEN_HMAC_SECRET
  valueFrom:
    secretKeyRef:
      name: {{ .Release.Name }}-codex-gateway
      key: cap-token-hmac-secret
- name: CCAPPGW_EXEC_GATEWAY_INTERNAL_SECRET
  valueFrom:
    secretKeyRef:
      name: {{ .Release.Name }}-codex-gateway
      key: internal-shared-secret
{{- end }}
```

values.yaml default:

```yaml
ccAppGateway:
  envMcp:
    enabled: false  # opt-in; requires codexExecGateway.enabled=true
```

(No `toolFlag` / `toolAllowlist` — `BuildArgs` hardcodes `--tools mcp__agentserver__*` per Phase 0 v2.)

### Modify: `deploy/helm/agentserver/templates/codex-gateway-secret.yaml`

The current secret template wraps the entire resource in `{{- if or .Values.codexAppGateway.enabled .Values.codexExecGateway.enabled }}`. Widen to include the cc env-mcp case:

```yaml
{{- if or .Values.codexAppGateway.enabled .Values.codexExecGateway.enabled (and .Values.ccAppGateway.enabled .Values.ccAppGateway.envMcp.enabled) }}
```

Otherwise an operator running cc-app-gateway env-mcp WITHOUT codex pods would have the secret missing and the pod would CrashLoopBackOff on missing `secretKeyRef`. The helm `fail` gate above prevents the typical misconfig, but defense in depth keeps the secret resource consistent with consumers.

## Security model

- **Cap-token = same HMAC secret as codex.** cc-app-gateway shares the `cap-token-hmac-secret` k8s secret with codex-app-gateway and codex-exec-gateway. env-mcp's Authorization: Bearer to codex-exec-gateway verifies cap-token; same workspace_id payload semantics. No new HMAC, no new trust boundary.
- **Token TTL = 1h default.** Same as codex. Turn timeout is 10m; one cap-token per turn so token outlives all in-flight env-mcp work even if claude stalls.
- **Token persistence bounded by Teardown.** mcp.json (mode 0600) is written under per-session TempDir (`<tmpRoot>/<wid>/<sid>/`); Phase 2's `workspace.Teardown` does `os.RemoveAll(TempDir)` at end of turn (workspace.go ~L146). If Teardown fails (Phase 2 logs and continues), the cap-token sits on local pod disk until the next turn's Setup overwrites the layout. Worst-case stale-file exposure window = `CapTokenTTL` (1h default). NOT written into ClaudeDir (which gets tarred to S3 — would be a leak).
- **env-mcp subprocess inherits ONLY the env we set in mcp.json's `env` block.** Phase 0 probe P0-B validates this assumption before coding. Fallback if claude DOES pass parent env: cc-app-gateway's `envAllowlist` in runner/options.go already strips `CXG_*` from the parent env claude inherits, so even an unintended passthrough would deliver nothing. The `env` block in mcp.json is the SOLE intended source of `CXG_WORKSPACE_TOKEN`.
- **No new attack surface.** cc-app-gateway already trusts the workspace via WSToken; cap-token is a narrower authorization (per-turn TTL) over the same trust relationship.

## Concurrency

No new locks. Per-(workspace, session) mutex from Phase 2 already serializes turns within a pod. mcp.WriteConfig writes a different `mcp.json` under each turn's TempDir, so no contention.

## Teardown

mcp.json lives at `<TempDir>/mcp.json` where TempDir = `<tmpRoot>/<wid>/<sid>/` (per-session, NOT per-turn — but Phase 2's Teardown wipes it at end of every turn, restoring per-turn ephemerality). env-mcp.log goes to `/dev/stderr` so it's visible via `kubectl logs <pod>` (NOT in TempDir, so survives Teardown — which is what we want operationally). No teardown code changes needed.

## Failure modes

| Failure | User sees | Logs |
|---|---|---|
| cap-token mint fails (HMAC empty / clock skew) | "Claude 返回错误：internal authorization failure" | mint_captoken_failed |
| mcp.WriteConfig fails (disk full, perms) | "Claude 返回错误：internal MCP config failure" | mcp_config_write_failed |
| codex-exec-gateway unreachable from env-mcp | claude sees `tools/call` error; replies with text mentioning tool failure | env-mcp logs in pod stderr (via `--log-file /dev/stderr`); persist across Teardown |
| claude doesn't call any tools (LLM choice) | normal reply | no MCP activity |
| upstream agentserver `/internal/workspaces/<wid>/scheduled-tasks/*` returns 5xx | claude sees `tools/call` error | env-mcp logs |

**Operational mitigation**: when env-mcp fails consistently, operators flip `ccAppGateway.envMcp.enabled=false` and roll → falls back to Phase 1 behavior (no tools, but still chats).

## Open risks

1. **Cap-token TTL vs turn timeout**: TTL=1h is plenty for turnTimeout=10m. But if turnTimeout is bumped past 1h, expired token mid-turn → 401 from codex-exec-gateway → tools start failing. Mitigation: validate `CapTokenTTL >= TurnTimeout * 2` at startup, fail-fast otherwise.

2. **HMAC secret sharing across pods**: cc-app-gateway and codex-app-gateway must read the SAME `cap-token-hmac-secret`. Chart-side enforcement: both pods mount `{{ .Release.Name }}-codex-gateway` secret; values.yaml's `codexGateway.capTokenHmacSecret` is set ONCE and consumed by both. Operator must NOT override per-pod.

3. **env-mcp binary version drift**: cc-app-gateway image and codex-app-gateway image both ship the same `codex-app-gateway` binary. If they're built at different times from different commits, env-mcp behavior could differ. Mitigation: both images built in the same workflow run (`build.yml`), tagged identically (e.g., 0.69.9). No new build-time check needed since they share the same Git commit.

4. **No HTTP/SSE MCP fallback**: stdio-only. If claude's stdio MCP layer has a bug discovered post-deploy, no in-band workaround — must roll back to envMcp.enabled=false.

5. **List_environments returns empty when workspace has no executors**: pre-existing condition (same for codex). claude sees an empty list and proceeds with text-only response. UX: user gets a chat reply explaining no executors found, which is correct but might confuse a first-time user who hasn't set up executors.

6. **codex executor required**: `ccAppGateway.envMcp.enabled=true` requires `codexExecGateway.enabled=true` (helm `required` gate). If operator disables codex but leaves managed_cc env-mcp on, helm install/upgrade fails fast.

7. **No per-tool allowlist override**: `--tools mcp__env-mcp__*` lets claude call any env tool. If we later add a tool we don't want exposed (e.g., privileged "execute-as-root"), we'd need explicit allowlist OR env-mcp side filtering. Acceptable for Phase 3.

8. **MCP config has cap-token in plaintext inside the pod**: mcp.json mode 0600, lives in TempDir which is pod-local emptyDir. Other containers in the same pod could read it if compromised. cc-app-gateway has no sidecars by design; if that changes, revisit.

## Acceptance criteria

1. Build & ship a 0.69.9 chart with `ccAppGateway.envMcp.enabled=true` deployable on nj-prod.
2. WeChat user on managed_cc channel: "请帮我看下我 workspace 里有哪些 executor" → claude calls `list_environments` → reply naming connected executors.
3. WeChat user: "帮我在 executor X 上跑 `pwd`" → claude calls `shell` → reply with cwd path.
4. WeChat user: "读一下 executor 上 /etc/hostname 内容" → claude calls `read_file` → reply with file contents.
5. Failure injection: stop codex-exec-gateway → next claude tool call fails → claude responds with a text explanation, NOT 500 to WeChat.
6. `kubectl logs <cc-app-gateway-pod>` shows env-mcp tool invocations (because `LogFile=/dev/stderr` writes into pod stderr; survives Teardown).
7. Existing managed_cc path (no tool calls): unchanged behavior — still works.

## Out of scope / future phases

- **Per-workspace MCP server overrides** (user-defined MCP servers in addition to env-mcp). Phase 5+.
- **Streaming MCP server output to claude** for long-running shell commands (currently buffered; Phase 5+ or env-mcp-side enhancement).
- **MCP server health probes** at cc-app-gateway startup (env-mcp can be unhealthy without us knowing until first tool call).
- **HTTP/SSE MCP transport** (e.g., for cross-cluster MCP servers).

## Audit revisions (v1 → v2)

Self-audit (opus, fresh-eyes) found 7 critical + 7 important + 5 minor BEFORE any code was written. Critical + important resolved in v2:

**Critical:**
1. `captoken` package path was wrong (`internal/codexappgateway/captoken` → actual is `internal/captoken`). Fixed throughout.
2. ExecGatewayURL needs `/bridge` suffix appended; helm passes bare WS URL; cc-app-gateway appends in code. Mirrors codex's `internal/codexappgateway/server.go` pattern. Renamed env var to `CCAPPGW_EXEC_GATEWAY_WS_URL` to match codex's `ExecGatewayWSURL` field naming.
3. mcp.json server key changed from `"env-mcp"` to `"agentserver"` to match env-mcp's hardcoded MCPServer self-name. Tool allowlist becomes `mcp__agentserver__*` (NOT `mcp__env-mcp__*`).
4. `--tools` glob (`*`) is unverified by Phase 0 — added Phase 0 probe P0-A. If glob unsupported, fall back to enumerated list. `--allowedTools` vs `--tools` flag choice deferred to probe P0-C result.
5. Helm `required` doesn't error on bool `false`. Replaced with explicit `{{- if not .Values.codexExecGateway.enabled }}{{- fail "..." }}{{- end }}`.
6. claude env block passthrough for MCP child unverified — added Phase 0 probe P0-B. Fallback noted (envAllowlist already strips CXG_*).
7. Dockerfile change hand-waved — named the actual file (Dockerfile.cc-app-gateway) and the exact `COPY --from=...` line. Build atomicity (same Git commit) addressed via existing workflow design.

**Important:**
8. `codex-gateway-secret.yaml` `if` widened to include cc env-mcp case (otherwise pod CrashLoopBackOff on missing secret if codex pods are disabled).
9. shortid import path explicitly named (`internal/shortid`).
10. TempDir lifecycle wording fixed (per-session, NOT per-turn; ephemerality via Teardown).
11. CapTokenTTL > TurnTimeout validation pinned in NewServer.
12. Acceptance criterion #6 changed to `kubectl logs` (LogFile=/dev/stderr) since `kubectl exec` post-turn finds TempDir gone.
13. ProjectTrustedPaths not needed (env tools execute remotely on executor, not on cc-app-gateway local filesystem).
14. cap-token TTL default = `time.Hour` (codex's default).

**Minor (deferred):**
15. Stale PR reference (#285 vs #283/#284) — non-functional.
16. JSON map ordering nondeterminism — test design concern; addressed at test-writing time.
17. MCP handshake noise in jsonl — known Phase 2 issue; noted in Open Risks.
18. (this section).
19. (this section).

## Spec strengths (validated by audit)

1. `captoken.Mint` signature exactly matches (`Mint(secret []byte, p Payload, ttl time.Duration) (string, error)`); Payload fields TurnID/WorkspaceID/UserID match the struct; UserID empty is fine (covered by codexexecgateway's auth_test `TestVerifyCapabilityToken_OldTokenHasNoUserID`).
2. env-mcp arg flag names verified letter-for-letter against `cmd/codex-app-gateway/main.go` (`--workspace-id`, `--exec-gateway-url`, `--exec-gateway-internal-url`, `--agentserver-internal-url`, `--workspace-token-env`, `--exec-gateway-internal-secret-env`, `--log-file`).
3. Cap-token shared-secret strategy correct: `cap-token-hmac-secret` from auto-generated + `lookup()`-preserved `codex-gateway-secret.yaml`.
4. Concurrency: Phase 2's per-(workspace, session) mutex sufficient; mcp.json per-turn write has no contention.
5. Fail-fast posture: validate env-mcp config at NewServer, not first turn.
