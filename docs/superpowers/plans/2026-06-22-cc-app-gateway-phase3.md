# cc-app-gateway Phase 3 plan: env-MCP wiring

**Spec:** `docs/superpowers/specs/2026-06-22-cc-app-gateway-phase3-design.md` (v3 — audited + Phase 0 v2 verified)
**Branch:** `feat/cc-app-gateway-phase3` (worktree at `/root/agentserver/.claude/worktrees/cc-app-gateway-phase3`)
**Base:** main @ `41cee9b` (chart 0.69.8)
**Date:** 2026-06-22

## Global constraints

- Each task TDD: RED first (failing test) → GREEN (minimal impl) → commit
- Each commit message includes Co-Authored-By trailer
- Reviews: sonnet for non-trivial tasks, haiku for transcription tasks
- Final whole-branch review with opus before PR
- After all tasks: bump chart 0.69.8 → 0.69.9, tag `v0.69.9`, update /root/k8s pulumi

## Files added/modified

| Task | File | Op | Notes |
|---|---|---|---|
| 1 | `internal/ccappgateway/mcp/config.go` | NEW | Pure WriteConfig + tests |
| 2 | `internal/ccappgateway/config.go` | MOD | New env vars + struct fields + tests |
| 3 | `internal/ccappgateway/runner/options.go` | MOD | RunInput.MCPConfigPath + BuildArgs |
| 4 | `internal/ccappgateway/server.go` | MOD | NewServer validation + plumbing Cfg fields |
| 4 | `internal/ccappgateway/turn_api.go` | MOD | Mint cap-token + WriteConfig + pass to runner |
| 5 | `Dockerfile.cc-app-gateway` | MOD | Also build codex-app-gateway binary; COPY into final image |
| 6 | `deploy/helm/agentserver/templates/cc-app-gateway.yaml` | MOD | Conditional env block + fail gate |
| 6 | `deploy/helm/agentserver/templates/codex-gateway-secret.yaml` | MOD | Widen `if` to include cc env-mcp consumer |
| 6 | `deploy/helm/agentserver/values.yaml` | MOD | `ccAppGateway.envMcp.enabled: false` default |
| 7 | `internal/ccappgateway/integration_phase3_test.go` | NEW | End-to-end: cc-app-gateway → claude → env-mcp child → mock codex-exec-gateway returns env list |

7 tasks total. Task 7 = integration test (largest).

## Tasks

### Task 1: `mcp.WriteConfig` (pure function)

**Subject:** New `internal/ccappgateway/mcp/config.go` with `WriteConfig(dir string, in ConfigInput) (string, error)` returning the path of the written mcp.json.

**RED test** (`internal/ccappgateway/mcp/config_test.go`):
```go
func TestWriteConfig_HappyPath(t *testing.T) {
    tmp := t.TempDir()
    path, err := WriteConfig(tmp, ConfigInput{
        EnvMcpBinary:           "/usr/local/bin/codex-app-gateway",
        WorkspaceID:            "wsp_abc",
        ExecGatewayBridgeURL:   "ws://codex-exec-gateway:6060/bridge",
        ExecGatewayInternalURL: "http://codex-exec-gateway:6060",
        AgentserverInternalURL: "http://agentserver:8080",
        WorkspaceCapToken:      "cap-token-abc.def.ghi",
        LogFile:                "/dev/stderr",
    })
    if err != nil {
        t.Fatalf("WriteConfig: %v", err)
    }
    if path != filepath.Join(tmp, "mcp.json") {
        t.Errorf("path = %q, want %q", path, filepath.Join(tmp, "mcp.json"))
    }
    info, err := os.Stat(path)
    if err != nil { t.Fatal(err) }
    if info.Mode().Perm() != 0o600 {
        t.Errorf("mode = %v, want 0600 (secret in env block)", info.Mode().Perm())
    }
    raw, _ := os.ReadFile(path)
    var got map[string]any
    if err := json.Unmarshal(raw, &got); err != nil { t.Fatal(err) }
    servers := got["mcpServers"].(map[string]any)
    srv := servers["agentserver"].(map[string]any)  // key MUST be "agentserver" — env-mcp's self-name
    if srv["command"] != "/usr/local/bin/codex-app-gateway" {
        t.Errorf("command = %v", srv["command"])
    }
    args := srv["args"].([]any)
    wantSubstrs := []string{"env-mcp", "--workspace-id", "wsp_abc",
        "--exec-gateway-url", "ws://codex-exec-gateway:6060/bridge",
        "--exec-gateway-internal-url", "http://codex-exec-gateway:6060",
        "--agentserver-internal-url", "http://agentserver:8080",
        "--workspace-token-env", "CXG_WORKSPACE_TOKEN",
        "--log-file", "/dev/stderr"}
    var got_args []string
    for _, a := range args { got_args = append(got_args, a.(string)) }
    for _, want := range wantSubstrs {
        if !contains(got_args, want) {
            t.Errorf("args missing %q; got %v", want, got_args)
        }
    }
    env := srv["env"].(map[string]any)
    if env["CXG_WORKSPACE_TOKEN"] != "cap-token-abc.def.ghi" {
        t.Errorf("env CXG_WORKSPACE_TOKEN = %v", env["CXG_WORKSPACE_TOKEN"])
    }
}

func TestWriteConfig_OptionalExecGatewayInternalSecret(t *testing.T) {
    tmp := t.TempDir()
    _, err := WriteConfig(tmp, ConfigInput{
        EnvMcpBinary: "/x", WorkspaceID: "w", WorkspaceCapToken: "t",
        ExecGatewayInternalSecret: "secret-xxx",
    })
    if err != nil { t.Fatal(err) }
    raw, _ := os.ReadFile(filepath.Join(tmp, "mcp.json"))
    if !bytes.Contains(raw, []byte("CXG_EXEC_GATEWAY_INTERNAL_SECRET")) {
        t.Errorf("expected --exec-gateway-internal-secret-env flag + env var")
    }
    if !bytes.Contains(raw, []byte("secret-xxx")) {
        t.Errorf("expected secret value in env block")
    }
}

func TestWriteConfig_ValidatesRequired(t *testing.T) {
    cases := []struct{ name string; in ConfigInput }{
        {"empty binary",    ConfigInput{WorkspaceID: "w", WorkspaceCapToken: "t"}},
        {"empty workspace", ConfigInput{EnvMcpBinary: "/x", WorkspaceCapToken: "t"}},
        {"empty captoken",  ConfigInput{EnvMcpBinary: "/x", WorkspaceID: "w"}},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            _, err := WriteConfig(t.TempDir(), tc.in)
            if err == nil { t.Errorf("want error, got nil") }
        })
    }
}
```

**Commit:** `feat(ccappgateway/mcp): WriteConfig writes per-turn mcp.json for env-mcp stdio child`

**Test command:** `go test ./internal/ccappgateway/mcp/...`

**Review:** sonnet — verify JSON structure matches Phase 0 v2 findings (key="agentserver", env block honored).

---

### Task 2: config.go env vars + struct fields

**Subject:** Add Phase 3 fields to `Config` struct and `Load()`.

New struct fields + env loading (in `internal/ccappgateway/config.go`):

```go
type Config struct {
    // ... existing Phase 1/2 fields ...

    // Phase 3: env-MCP wiring. All required when EnvMcpBinary != "".
    EnvMcpBinary              string
    ExecGatewayWSURL          string // ws://...; cc-app-gateway appends /bridge
    ExecGatewayInternalURL    string
    ExecGatewayInternalSecret string // optional
    AgentserverInternalURL    string
    CapTokenHMACSecret        []byte
    CapTokenTTL               time.Duration // default time.Hour
}
```

Load() additions:
```go
cfg.EnvMcpBinary = os.Getenv("CCAPPGW_ENV_MCP_BINARY")
cfg.ExecGatewayWSURL = os.Getenv("CCAPPGW_EXEC_GATEWAY_WS_URL")
cfg.ExecGatewayInternalURL = os.Getenv("CCAPPGW_EXEC_GATEWAY_INTERNAL_URL")
cfg.ExecGatewayInternalSecret = os.Getenv("CCAPPGW_EXEC_GATEWAY_INTERNAL_SECRET")
cfg.AgentserverInternalURL = os.Getenv("CCAPPGW_AGENTSERVER_INTERNAL_URL")
cfg.CapTokenHMACSecret = []byte(os.Getenv("CCAPPGW_CAPTOKEN_HMAC_SECRET"))
if v := os.Getenv("CCAPPGW_CAPTOKEN_TTL"); v != "" {
    d, err := time.ParseDuration(v)
    if err != nil { return Config{}, fmt.Errorf("CCAPPGW_CAPTOKEN_TTL: %w", err) }
    cfg.CapTokenTTL = d
} else {
    cfg.CapTokenTTL = time.Hour
}
```

**RED tests** (in `internal/ccappgateway/config_test.go`):
- All Phase 3 env vars empty → Phase 1/2 behavior, no MCP fields populated.
- Each env var set → corresponding field populated.
- `CCAPPGW_CAPTOKEN_TTL=invalid` → Load returns error.
- `CCAPPGW_CAPTOKEN_TTL` empty → field = `time.Hour`.

**Commit:** `feat(ccappgateway): add env-MCP config env vars (Phase 3)`

**Review:** haiku — straightforward env parsing.

---

### Task 3: runner BuildArgs with --mcp-config

**Subject:** Append MCP flags to BuildArgs when MCPConfigPath != "".

`internal/ccappgateway/runner/options.go`:

```go
type RunInput struct {
    // ... existing fields ...
    MCPConfigPath string  // when set: --mcp-config + --strict-mcp-config + --tools
}

// BuildArgs ...
func BuildArgs(in RunInput) []string {
    args := []string{
        "--print",
        "--input-format", "stream-json",
        "--output-format", "stream-json",
        "--verbose",
        "--permission-mode", "bypassPermissions",
        "--dangerously-skip-permissions",
        "--model", in.Model,
    }
    if in.MCPConfigPath != "" {
        args = append(args, "--mcp-config", in.MCPConfigPath, "--strict-mcp-config", "--tools", "mcp__agentserver__*")
    }
    switch in.SessionMode {
    case "resume":
        args = append(args, "--resume", in.SessionID)
    default:
        args = append(args, "--session-id", in.SessionID)
    }
    return args
}
```

**RED tests:**
- MCPConfigPath empty → BuildArgs returns Phase 1/2 args (no `--mcp-config`).
- MCPConfigPath set → BuildArgs contains `--mcp-config /path/mcp.json`, `--strict-mcp-config`, `--tools mcp__agentserver__*`.
- Phase 0 v2 finding: glob `*` is correct (don't enumerate tools).

**Commit:** `feat(ccappgateway/runner): pass --mcp-config + --tools when MCPConfigPath set (Phase 3)`

**Review:** haiku — single conditional append.

---

### Task 4: turn_api.go integration + server.go startup validation

**Subject:** TurnHandler mints cap-token, writes mcp.json, passes to runner. NewServer validates Phase 3 config consistency.

`internal/ccappgateway/server.go` — in `NewServer` after existing checks:

```go
if cfg.EnvMcpBinary != "" {
    if cfg.ExecGatewayWSURL == "" { return nil, fmt.Errorf("CCAPPGW_EXEC_GATEWAY_WS_URL required when CCAPPGW_ENV_MCP_BINARY set") }
    if cfg.ExecGatewayInternalURL == "" { return nil, fmt.Errorf("CCAPPGW_EXEC_GATEWAY_INTERNAL_URL required when CCAPPGW_ENV_MCP_BINARY set") }
    if cfg.AgentserverInternalURL == "" { return nil, fmt.Errorf("CCAPPGW_AGENTSERVER_INTERNAL_URL required when CCAPPGW_ENV_MCP_BINARY set") }
    if len(cfg.CapTokenHMACSecret) == 0 { return nil, fmt.Errorf("CCAPPGW_CAPTOKEN_HMAC_SECRET required when CCAPPGW_ENV_MCP_BINARY set") }
    if cfg.CapTokenTTL <= cfg.TurnTimeout {
        return nil, fmt.Errorf("CapTokenTTL (%v) must exceed TurnTimeout (%v)", cfg.CapTokenTTL, cfg.TurnTimeout)
    }
}
```

`internal/ccappgateway/turn_api.go` — between `workspace.Setup` and `runner.Run`:

```go
var mcpConfigPath string
if h.Cfg.EnvMcpBinary != "" {
    capTok, err := captoken.Mint(h.Cfg.CapTokenHMACSecret, captoken.Payload{
        TurnID:      "trn_" + shortid.Generate(),
        WorkspaceID: req.WorkspaceID,
        UserID:      "",
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
        LogFile:                   "/dev/stderr",
    })
    if err != nil {
        log.Printf("[cc-app-gateway] mcp_config_write_failed (session=%s): %v", req.SessionID, err)
        writeError(w, http.StatusInternalServerError, "mcp_config_failed", "internal MCP config failure")
        return
    }
    mcpConfigPath = p
}

result, err := h.Runner(runCtx, runner.RunInput{
    // ... existing fields ...
    MCPConfigPath: mcpConfigPath,
})
```

Imports added: `"github.com/agentserver/agentserver/internal/captoken"`, `"github.com/agentserver/agentserver/internal/shortid"`, `"github.com/agentserver/agentserver/internal/ccappgateway/mcp"`, `"strings"`.

**RED tests** (`turn_api_test.go`):
- With `Cfg.EnvMcpBinary != ""` + valid config: ServeHTTP calls runner with non-empty `MCPConfigPath`, and the file at that path is a valid mcp.json with the cap-token in env block.
- With `Cfg.EnvMcpBinary == ""`: ServeHTTP calls runner with empty `MCPConfigPath` (Phase 2 behavior unchanged).
- Empty `CapTokenHMACSecret` at runtime (despite startup check) → captoken_failed response (defense in depth).

Plus (`server_test.go`):
- `NewServer` with `EnvMcpBinary` set but missing `ExecGatewayWSURL` → returns error.
- `NewServer` with `CapTokenTTL <= TurnTimeout` → returns error.

**Commit:** `feat(ccappgateway): mint cap-token + write mcp.json per turn (Phase 3)`

**Review:** sonnet — main integration point, multiple failure paths.

---

### Task 5: Dockerfile.cc-app-gateway adds codex-app-gateway binary

**Subject:** Build stage compiles both `cmd/cc-app-gateway` and `cmd/codex-app-gateway`; runtime stage COPYs both into final image.

Edit `Dockerfile.cc-app-gateway`:

```dockerfile
# --- build stage -------------------------------------------------------------
FROM golang:1.26-trixie AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG SHA=unknown
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-s -w -X main.BuildVersion=${VERSION} -X main.BuildSHA=${SHA}" \
    -o /out/cc-app-gateway ./cmd/cc-app-gateway && \
    CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-s -w -X main.BuildVersion=${VERSION} -X main.BuildSHA=${SHA}" \
    -o /out/codex-app-gateway ./cmd/codex-app-gateway
```

And in runtime stage:

```dockerfile
COPY --from=build /out/cc-app-gateway      /usr/local/bin/cc-app-gateway
COPY --from=build /out/codex-app-gateway   /usr/local/bin/codex-app-gateway
```

**Verification:**
- `docker build -f Dockerfile.cc-app-gateway .` succeeds locally
- Final image has both binaries: `docker run --rm <image> ls /usr/local/bin/ | grep gateway`
- `docker run --rm --entrypoint /usr/local/bin/codex-app-gateway <image> env-mcp --help` returns usage (sanity check the codex binary is runnable)

**Commit:** `feat(Dockerfile.cc-app-gateway): embed codex-app-gateway binary for env-mcp child`

**Review:** haiku — Dockerfile change only, no logic.

**Risk:** image size grows ~30 MB (one extra Go binary). Acceptable.

---

### Task 6: Helm chart + values.yaml

**Subject:** Conditional env block, fail-fast gate, widen secret consumer if.

`deploy/helm/agentserver/templates/cc-app-gateway.yaml` — append to existing env list:

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

`deploy/helm/agentserver/values.yaml`:

```yaml
ccAppGateway:
  # ... existing fields ...
  envMcp:
    enabled: false  # opt-in; requires codexExecGateway.enabled=true
```

`deploy/helm/agentserver/templates/codex-gateway-secret.yaml` — widen the gate:

```yaml
{{- if or .Values.codexAppGateway.enabled .Values.codexExecGateway.enabled (and .Values.ccAppGateway.enabled .Values.ccAppGateway.envMcp.enabled) }}
```

**Verification:**
- `helm template deploy/helm/agentserver --set ccAppGateway.enabled=true --set ccAppGateway.envMcp.enabled=true --set codexExecGateway.enabled=true` renders without errors and shows the env block.
- `helm template ... --set ccAppGateway.envMcp.enabled=true --set codexExecGateway.enabled=false` fails with the explicit message.
- `helm template ... --set ccAppGateway.envMcp.enabled=false` renders without the env block (Phase 2 behavior preserved).
- `helm template ... --set ccAppGateway.envMcp.enabled=true --set codexExecGateway.enabled=true --set codexAppGateway.enabled=false` STILL includes the `codex-gateway` Secret (verifying the widened gate).

**Commit:** `feat(helm/cc-app-gateway): conditional env-MCP env block + codex-gateway-secret consumer widen`

**Review:** sonnet — fail-gate logic + secret-consumer wiring.

---

### Task 7: Integration test — end-to-end env tool call

**Subject:** Real cc-app-gateway server in a sub-test; real claude --print subprocess; real env-mcp child; mock codex-exec-gateway returns an env list. Asserts the model called `list_environments` and saw the mock's response.

`//go:build integration` (NOT default test pass — opt-in like Phase 2 + Phase 4).

`internal/ccappgateway/integration_phase3_test.go`:

```go
//go:build integration
// +build integration

package ccappgateway_test

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"
    "time"
    // ...
)

func TestIntegration_EnvMcp_ListEnvironments(t *testing.T) {
    // 1. Build codex-app-gateway binary into testdata/.
    binDir := t.TempDir()
    out := filepath.Join(binDir, "codex-app-gateway")
    cmd := exec.Command("go", "build", "-o", out, "./cmd/codex-app-gateway")
    cmd.Dir = repoRoot(t)
    if b, err := cmd.CombinedOutput(); err != nil { t.Fatalf("build codex-app-gateway: %v: %s", err, b) }

    // 2. Mock codex-exec-gateway: GET /api/exec-gateway/connected returns a fake env.
    mockExecGw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path == "/api/exec-gateway/connected" {
            json.NewEncoder(w).Encode(map[string]any{
                "environments": []map[string]any{
                    {"env_id": "env_test123", "name": "test-env", "status": "connected"},
                },
            })
            return
        }
        http.NotFound(w, r)
    }))
    defer mockExecGw.Close()

    // 3. Mock agentserver-main for /internal/workspace-token (returns dummy WS token).
    mockAgsv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path == "/internal/workspace-token" {
            json.NewEncoder(w).Encode(map[string]string{"token": "fake-ws-token"})
            return
        }
        http.NotFound(w, r)
    }))
    defer mockAgsv.Close()

    // 4. Compute a valid HMAC secret + matching helm-style env.
    hmacSecret := []byte("integration-test-hmac-secret-32b!")
    t.Setenv("CCAPPGW_ENV_MCP_BINARY", out)
    t.Setenv("CCAPPGW_EXEC_GATEWAY_WS_URL", strings.Replace(mockExecGw.URL, "http://", "ws://", 1))
    t.Setenv("CCAPPGW_EXEC_GATEWAY_INTERNAL_URL", mockExecGw.URL)
    t.Setenv("CCAPPGW_AGENTSERVER_INTERNAL_URL", mockAgsv.URL)
    t.Setenv("CCAPPGW_CAPTOKEN_HMAC_SECRET", string(hmacSecret))
    t.Setenv("CCAPPGW_CAPTOKEN_TTL", "1h")
    t.Setenv("AGENTSERVER_INTERNAL_URL", mockAgsv.URL)
    t.Setenv("INTERNAL_API_SECRET", "test-secret")
    // Plus all the Phase 1/2 base env vars (claude bin, llmproxy URL, etc.).

    cfg, err := ccappgateway.Load()
    if err != nil { t.Fatalf("config load: %v", err) }
    srv, err := ccappgateway.NewServer(cfg)  // validates Phase 3 config
    if err != nil { t.Fatalf("server init: %v", err) }
    defer srv.Shutdown(context.Background())

    // 5. POST /api/turns asking claude to call list_environments.
    body := `{
        "workspaceId":"wsp_test",
        "sessionId":"00000000-0000-0000-0000-000000000001",
        "userMessage":"Please call the list_environments tool and tell me what envs exist.",
        "model":"claude-haiku-4-5"
    }`
    req := httptest.NewRequest(http.MethodPost, "/api/turns", strings.NewReader(body))
    req.Header.Set("X-Internal-Secret", "test-secret")
    req.Header.Set("Content-Type", "application/json")
    rec := httptest.NewRecorder()
    srv.Routes().ServeHTTP(rec, req)

    // 6. Assertions:
    if rec.Code != http.StatusOK { t.Fatalf("status %d: %s", rec.Code, rec.Body.String()) }
    var resp struct{
        AssistantText string `json:"assistantText"`
        IsError       bool   `json:"isError"`
    }
    json.Unmarshal(rec.Body.Bytes(), &resp)
    if resp.IsError { t.Errorf("turn returned IsError; assistant text: %s", resp.AssistantText) }
    if !strings.Contains(strings.ToLower(resp.AssistantText), "test-env") &&
       !strings.Contains(resp.AssistantText, "env_test123") {
        t.Errorf("expected env name or id in response; got: %q", resp.AssistantText)
    }
}
```

**Required prerequisites for the test to pass:**
- `claude` binary in PATH (cc-app-gateway image has it; CI/dev must too)
- `ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN` set (test reads from env; CI env or skipped)
- The codex-app-gateway binary builds clean (test builds it)

**Test command:** `go test -tags integration -timeout 90s ./internal/ccappgateway/... -run TestIntegration_EnvMcp_ListEnvironments`

**Commit:** `test(ccappgateway): integration test asserts env-mcp child returns list_environments through claude (Phase 3)`

**Review:** sonnet — complex test scaffolding; verify mocks faithfully represent codex-exec-gateway's contract.

**Skip-if-no-claude-binary**: integration tests already gate on `claude` in PATH (Phase 2 pattern); reuse.

---

## After all tasks pass

1. Final whole-branch review (opus model, code-reviewer agent) over `.superpowers/sdd/review-41cee9b..HEAD.diff`.
2. Open PR (this is `feat/cc-app-gateway-phase3` against `main`; not stacked).
3. After PR merges + CI green: bump `Chart.yaml` 0.69.8 → 0.69.9, push `v0.69.9` tag.
4. Update `/root/k8s/stacks/agentserver.ts`:
   - All `"0.69.8"` → `"0.69.9"`
   - Add `envMcp: { enabled: true }` to the `ccAppGateway` block
5. `pulumi up` and smoke test: send WeChat message asking claude to list executors.

## Out of scope

- HTTP/SSE MCP (Phase 5+)
- Per-workspace user-defined MCP servers (Phase 5+)
- MCP server health probes at startup (Phase 5+)
- Streaming long-running tool output (Phase 5+)
