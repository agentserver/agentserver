# SDK → REST via codex-exec-gateway Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Python SDK's WS-through-codex transport with direct HTTP REST to codex-exec-gateway.

**Architecture:** Extract env-mcp tool implementations from `internal/codexappgateway/envmcp/` into a shared `internal/envtools/` package; add SDK REST handlers under `/api/sdk/*` on codex-exec-gateway that authenticate sandbox proxyTokens via agentserver's `/internal/validate-proxy-token` and dispatch tool calls through the same BridgePool the env-mcp subprocess uses today. Rewrite the Python SDK to httpx. Delete the codex-app-gateway `/notebook/ws` path.

**Tech Stack:** Go (chi, websocket, hashicorp/golang-lru), Python (httpx, pytest), Helm chart, Pulumi (TypeScript).

**Source spec:** `docs/superpowers/specs/2026-05-20-sdk-rest-via-exec-gateway-design.md` (commit `b9942cd`).

**Repo layout:** Tasks A1–E1 touch `/root/agentserver`. Task F1 touches `/root/k8s`.

**Order matters:** Task A1 (extraction) is the riskiest task and gates everything else. Do it first as one atomic commit, verify build + tests stay green, then fan out.

---

## Phase A — Extract envmcp into shared `internal/envtools/`

### Task A1: Move envmcp tool implementations + bridge + nameresolver into `internal/envtools/`

**Files (everything via `git mv` in one commit so builds stay green):**

- Move `internal/codexappgateway/envmcp/bridge.go` → `internal/envtools/bridge/bridge.go` (rename `package envmcp` → `package bridge`)
- Move `internal/codexappgateway/envmcp/bridge_test.go` → `internal/envtools/bridge/bridge_test.go`
- Move `internal/codexappgateway/envmcp/pool.go` → `internal/envtools/bridge/pool.go`
- Move `internal/codexappgateway/envmcp/pool_test.go` → `internal/envtools/bridge/pool_test.go`
- Move `internal/codexappgateway/envmcp/relay_client.go` → `internal/envtools/bridge/relay_client.go`
- Move `internal/codexappgateway/envmcp/relay_client_test.go` → `internal/envtools/bridge/relay_client_test.go`
- Move `internal/codexappgateway/envmcp/name_resolver.go` → `internal/envtools/nameresolver/resolver.go` (rename `package envmcp` → `package nameresolver`)
- Move `internal/codexappgateway/envmcp/tool_shell.go` → `internal/envtools/tools/shell.go` (rename `package envmcp` → `package tools`)
- Move `internal/codexappgateway/envmcp/tool_fs.go` → `internal/envtools/tools/fs.go`
- Move `internal/codexappgateway/envmcp/tool_apply_patch.go` → `internal/envtools/tools/apply_patch.go`
- Move `internal/codexappgateway/envmcp/apply_patch_test.go` → `internal/envtools/tools/apply_patch_test.go`
- Move `internal/codexappgateway/envmcp/tool_copy_path.go` → `internal/envtools/tools/copy_path.go`
- Move `internal/codexappgateway/envmcp/tool_copy_path_test.go` → `internal/envtools/tools/copy_path_test.go`
- Move `internal/codexappgateway/envmcp/tool_unified_exec.go` → `internal/envtools/tools/unified_exec.go`
- Move `internal/codexappgateway/envmcp/tool_list_envs.go` → `internal/envtools/tools/list_envs.go`
- Move `internal/codexappgateway/envmcp/types.go` → `internal/envtools/tools/types.go`
- Move `internal/codexappgateway/envmcp/types_test.go` → `internal/envtools/tools/types_test.go`
- **Keep** `internal/codexappgateway/envmcp/mcp_server.go` (MCP protocol layer, codex-app-gateway-specific)
- **Keep** `internal/codexappgateway/envmcp/envmcp.go` (Run() entrypoint, codex-app-gateway-specific)
- Within all moved files: change `package envmcp` to the new sub-package name and update intra-package symbols (e.g., `BridgePool` stays unqualified within `package bridge`).
- Within `mcp_server.go` and `envmcp.go`: update imports to reference the new envtools paths (`bridge.Pool`, `tools.ShellTool`, `nameresolver.Resolver`, etc.). These files now construct types from the new packages.

- [ ] **Step 1: Read the current `mcp_server.go` and `envmcp.go` end to end**

```bash
cat internal/codexappgateway/envmcp/mcp_server.go
cat internal/codexappgateway/envmcp/envmcp.go
```

You need to see every internal reference these two files make so you know what to re-import. In particular note: `NewBridgePool`, `NewNameResolver`, `NewShellTool`, `NewFSTool`, `NewApplyPatchTool`, `NewCopyPathTool`, `NewUnifiedExecTool`, `NewListEnvironmentsTool`, `MCPCallToolResult`, `MCPToolContent`, `Tool` interface, `errResult` helper.

- [ ] **Step 2: Decide which symbols stay in which sub-package**

Final structure:

```
internal/envtools/
├── bridge/
│   ├── bridge.go            (BridgeClient — was envmcp.BridgeClient)
│   ├── bridge_test.go
│   ├── pool.go              (Pool — was envmcp.BridgePool; rename Pool to keep package qualifier sensible)
│   ├── pool_test.go
│   ├── relay_client.go      (RelayClient)
│   └── relay_client_test.go
├── nameresolver/
│   └── resolver.go          (Resolver — was envmcp.NameResolver)
└── tools/
    ├── types.go             (Tool interface, MCPCallToolResult, MCPToolContent, errResult)
    ├── types_test.go
    ├── shell.go             (ShellTool, NewShellTool)
    ├── fs.go                (FSReadFileTool, FSWriteFileTool, NewFSTool — split tool ctors as-is)
    ├── apply_patch.go       (ApplyPatchTool, NewApplyPatchTool)
    ├── apply_patch_test.go
    ├── copy_path.go         (CopyPathTool, NewCopyPathTool)
    ├── copy_path_test.go
    ├── unified_exec.go      (UnifiedExecTool incl. ExecCommand/WriteStdin/ReadOutput/Terminate subtools)
    └── list_envs.go         (ListEnvironmentsTool, NewListEnvironmentsTool)
```

Renames you'll make inside the package code:
- `envmcp.BridgePool` → `bridge.Pool` (so callers say `bridge.NewPool(...)`)
- `envmcp.NameResolver` → `nameresolver.Resolver`
- All `*Tool` types and constructors keep their names but move to `tools` package
- `MCPCallToolResult`, `MCPToolContent`, `Tool` interface, `errResult` helper move to `tools/types.go`

- [ ] **Step 3: Create the destination directories**

```bash
mkdir -p internal/envtools/bridge internal/envtools/nameresolver internal/envtools/tools
```

- [ ] **Step 4: git mv each file**

```bash
git mv internal/codexappgateway/envmcp/bridge.go             internal/envtools/bridge/bridge.go
git mv internal/codexappgateway/envmcp/bridge_test.go        internal/envtools/bridge/bridge_test.go
git mv internal/codexappgateway/envmcp/pool.go               internal/envtools/bridge/pool.go
git mv internal/codexappgateway/envmcp/pool_test.go          internal/envtools/bridge/pool_test.go
git mv internal/codexappgateway/envmcp/relay_client.go       internal/envtools/bridge/relay_client.go
git mv internal/codexappgateway/envmcp/relay_client_test.go  internal/envtools/bridge/relay_client_test.go
git mv internal/codexappgateway/envmcp/name_resolver.go      internal/envtools/nameresolver/resolver.go
git mv internal/codexappgateway/envmcp/types.go              internal/envtools/tools/types.go
git mv internal/codexappgateway/envmcp/types_test.go         internal/envtools/tools/types_test.go
git mv internal/codexappgateway/envmcp/tool_shell.go         internal/envtools/tools/shell.go
git mv internal/codexappgateway/envmcp/tool_fs.go            internal/envtools/tools/fs.go
git mv internal/codexappgateway/envmcp/tool_apply_patch.go   internal/envtools/tools/apply_patch.go
git mv internal/codexappgateway/envmcp/apply_patch_test.go   internal/envtools/tools/apply_patch_test.go
git mv internal/codexappgateway/envmcp/tool_copy_path.go     internal/envtools/tools/copy_path.go
git mv internal/codexappgateway/envmcp/tool_copy_path_test.go internal/envtools/tools/copy_path_test.go
git mv internal/codexappgateway/envmcp/tool_unified_exec.go  internal/envtools/tools/unified_exec.go
git mv internal/codexappgateway/envmcp/tool_list_envs.go     internal/envtools/tools/list_envs.go
```

- [ ] **Step 5: Update package declarations in the moved files**

In every file under `internal/envtools/bridge/`: replace top-of-file `package envmcp` with `package bridge`.
In `internal/envtools/nameresolver/resolver.go`: replace with `package nameresolver`.
In every file under `internal/envtools/tools/`: replace with `package tools`.

```bash
sed -i 's/^package envmcp$/package bridge/' internal/envtools/bridge/*.go
sed -i 's/^package envmcp$/package nameresolver/' internal/envtools/nameresolver/*.go
sed -i 's/^package envmcp$/package tools/' internal/envtools/tools/*.go
```

- [ ] **Step 6: Rename `BridgePool` to `Pool` inside `bridge/` package**

```bash
sed -i 's/\bBridgePool\b/Pool/g' internal/envtools/bridge/*.go
sed -i 's/\bNewBridgePool\b/NewPool/g' internal/envtools/bridge/*.go
```

Verify: `grep -n "BridgePool\|NewBridgePool" internal/envtools/bridge/` returns nothing.

- [ ] **Step 7: Rename `NameResolver` to `Resolver` inside `nameresolver/` package**

```bash
sed -i 's/\bNameResolver\b/Resolver/g' internal/envtools/nameresolver/*.go
sed -i 's/\bNewNameResolver\b/NewResolver/g' internal/envtools/nameresolver/*.go
```

- [ ] **Step 8: Update cross-package references inside `tools/` files**

The `tools/` files referenced `BridgePool` and `NameResolver` from the same `envmcp` package. After the move, they need to import the new packages:

```bash
# Add imports + qualify references.
for f in internal/envtools/tools/*.go; do
    # Add import group if not present (idempotent via goimports later).
    :
done
# This step is best done by hand or `goimports -w` (next step).
```

Specifically: open `internal/envtools/tools/shell.go`, `apply_patch.go`, `copy_path.go`, `unified_exec.go`, `list_envs.go`, `fs.go`. Wherever `BridgePool` appears, change to `bridge.Pool`. Wherever `NameResolver` appears, change to `nameresolver.Resolver`. Add the corresponding imports:

```go
import (
    "github.com/agentserver/agentserver/internal/envtools/bridge"
    "github.com/agentserver/agentserver/internal/envtools/nameresolver"
)
```

- [ ] **Step 9: Update `internal/codexappgateway/envmcp/envmcp.go` and `mcp_server.go`**

These two files stay in `package envmcp`. They construct the types now in envtools sub-packages. Update imports:

```go
import (
    "github.com/agentserver/agentserver/internal/envtools/bridge"
    "github.com/agentserver/agentserver/internal/envtools/nameresolver"
    "github.com/agentserver/agentserver/internal/envtools/tools"
)
```

Inside, replace:
- `NewBridgePool(...)` → `bridge.NewPool(...)`
- `NewNameResolver(...)` → `nameresolver.NewResolver(...)`
- `NewShellTool(...)` → `tools.NewShellTool(...)`
- `NewFSTool(...)` → `tools.NewFSTool(...)` (preserve original symbol names — fs.go has its own ctor; check actual name on disk)
- `NewApplyPatchTool(...)` → `tools.NewApplyPatchTool(...)`
- `NewCopyPathTool(...)` → `tools.NewCopyPathTool(...)`
- `NewUnifiedExecTool(...)` → `tools.NewUnifiedExecTool(...)`
- `NewListEnvironmentsTool(...)` → `tools.NewListEnvironmentsTool(...)`
- Interface uses (`Tool`, `MCPCallToolResult`, etc.): `tools.Tool`, `tools.MCPCallToolResult`, etc.

- [ ] **Step 10: Run `goimports` to fix imports across the changed packages**

```bash
goimports -w internal/envtools/ internal/codexappgateway/envmcp/
```

If `goimports` isn't installed: `go install golang.org/x/tools/cmd/goimports@latest` then re-run.

- [ ] **Step 11: Build the entire repo**

```bash
go build ./...
```

Expected: succeeds with no errors. If it fails, the error message will name the missing import or type — fix and re-run.

- [ ] **Step 12: Run all tests**

```bash
go test ./internal/envtools/... ./internal/codexappgateway/... -count=1
```

Expected: all pass. The tests moved with their code; only package-name and qualifier changes affect them, no behavior changed.

- [ ] **Step 13: Verify no stale references elsewhere**

```bash
grep -rn "internal/codexappgateway/envmcp\"" --include="*.go" .
```

Should only match `mcp_server.go` and `envmcp.go` itself (which is the new "thin shim" + adapter). Anything else means a caller was overlooked.

- [ ] **Step 14: Commit**

```bash
git add -A
git commit -m "refactor: extract envmcp tools/bridge/nameresolver to internal/envtools/

Pure refactor — no behavior change. Splits the envmcp package so its
tool implementations + bridge + name resolver can be reused by
codex-exec-gateway's new SDK REST handlers. The mcp_server.go +
envmcp.go stay in codexappgateway/envmcp as the codex-app-gateway-
specific MCP protocol adapter and Run() entrypoint.
"
```

---

## Phase B — Add SDK REST handlers on codex-exec-gateway

### Task B1: Add `internal/codexexecgateway/sdk/auth.go` — ProxyTokenAuth

**Files:**
- Create: `internal/codexexecgateway/sdk/auth.go`
- Create: `internal/codexexecgateway/sdk/auth_test.go`

- [ ] **Step 1: Add `hashicorp/golang-lru/v2` to go.mod**

```bash
go get github.com/hashicorp/golang-lru/v2@latest
```

- [ ] **Step 2: Write the failing test**

`internal/codexexecgateway/sdk/auth_test.go`:

```go
package sdk

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"
)

func TestProxyTokenAuth_VerifySuccess(t *testing.T) {
    upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Header.Get("X-Internal-Secret") != "test-secret" {
            t.Errorf("missing X-Internal-Secret header")
        }
        _ = json.NewEncoder(w).Encode(map[string]string{
            "workspace_id": "ws-1", "user_id": "u-1",
        })
    }))
    defer upstream.Close()
    a := NewProxyTokenAuth(upstream.URL, "test-secret", time.Minute, time.Second)
    wsID, userID, err := a.Verify(context.Background(), "tok-1")
    if err != nil { t.Fatal(err) }
    if wsID != "ws-1" || userID != "u-1" {
        t.Errorf("got wsID=%q userID=%q", wsID, userID)
    }
}

func TestProxyTokenAuth_CacheHit(t *testing.T) {
    calls := 0
    upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        calls++
        _ = json.NewEncoder(w).Encode(map[string]string{"workspace_id": "ws-1", "user_id": "u-1"})
    }))
    defer upstream.Close()
    a := NewProxyTokenAuth(upstream.URL, "test-secret", time.Minute, time.Second)
    for i := 0; i < 5; i++ {
        if _, _, err := a.Verify(context.Background(), "tok-1"); err != nil { t.Fatal(err) }
    }
    if calls != 1 {
        t.Errorf("expected 1 upstream call (cache should serve rest), got %d", calls)
    }
}

func TestProxyTokenAuth_VerifyUnauthorized(t *testing.T) {
    upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        http.Error(w, "invalid", http.StatusUnauthorized)
    }))
    defer upstream.Close()
    a := NewProxyTokenAuth(upstream.URL, "test-secret", time.Minute, time.Second)
    if _, _, err := a.Verify(context.Background(), "tok-bad"); err == nil {
        t.Fatal("expected error")
    }
}

func TestProxyTokenAuth_NegativeCache(t *testing.T) {
    calls := 0
    upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        calls++
        http.Error(w, "invalid", http.StatusUnauthorized)
    }))
    defer upstream.Close()
    a := NewProxyTokenAuth(upstream.URL, "test-secret", time.Minute, time.Second)
    for i := 0; i < 3; i++ {
        if _, _, err := a.Verify(context.Background(), "tok-bad"); err == nil {
            t.Fatal("expected error")
        }
    }
    if calls != 1 {
        t.Errorf("expected 1 upstream call (negative cache should serve rest), got %d", calls)
    }
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/codexexecgateway/sdk/ -run TestProxyTokenAuth -v
```

Expected: compile error (`NewProxyTokenAuth` undefined).

- [ ] **Step 4: Implement `auth.go`**

`internal/codexexecgateway/sdk/auth.go`:

```go
package sdk

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "net/http"
    "time"

    lru "github.com/hashicorp/golang-lru/v2"
)

// ErrUnauthorized is returned by ProxyTokenAuth.Verify for tokens that
// agentserver rejects. Callers MUST respond with HTTP 401 (never 5xx)
// so a misconfigured client recovers without retrying forever.
var ErrUnauthorized = errors.New("sdk auth: token rejected by agentserver")

// ProxyTokenAuth turns a sandbox proxyToken into (workspace_id, user_id)
// by calling agentserver's /internal/validate-proxy-token. Results are
// LRU-cached with a positive TTL and a shorter negative TTL.
type ProxyTokenAuth struct {
    agentserverURL string
    internalSecret string
    posTTL         time.Duration
    negTTL         time.Duration
    cache          *lru.Cache[string, cacheEntry]
    httpClient     *http.Client
}

type cacheEntry struct {
    workspaceID string
    userID      string
    expiresAt   time.Time
    negative    bool
}

func NewProxyTokenAuth(agentserverURL, internalSecret string, posTTL, negTTL time.Duration) *ProxyTokenAuth {
    cache, _ := lru.New[string, cacheEntry](1024)
    return &ProxyTokenAuth{
        agentserverURL: agentserverURL,
        internalSecret: internalSecret,
        posTTL:         posTTL,
        negTTL:         negTTL,
        cache:          cache,
        httpClient:     &http.Client{Timeout: 5 * time.Second},
    }
}

func (a *ProxyTokenAuth) Verify(ctx context.Context, token string) (workspaceID, userID string, err error) {
    if e, ok := a.cache.Get(token); ok && time.Now().Before(e.expiresAt) {
        if e.negative {
            return "", "", ErrUnauthorized
        }
        return e.workspaceID, e.userID, nil
    }

    body, _ := json.Marshal(map[string]string{"token": token})
    req, err := http.NewRequestWithContext(ctx, http.MethodPost,
        a.agentserverURL+"/internal/validate-proxy-token", bytes.NewReader(body))
    if err != nil {
        return "", "", fmt.Errorf("sdk auth: build request: %w", err)
    }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-Internal-Secret", a.internalSecret)

    resp, err := a.httpClient.Do(req)
    if err != nil {
        return "", "", fmt.Errorf("sdk auth: agentserver unreachable: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode == http.StatusUnauthorized {
        a.cache.Add(token, cacheEntry{negative: true, expiresAt: time.Now().Add(a.negTTL)})
        return "", "", ErrUnauthorized
    }
    if resp.StatusCode != http.StatusOK {
        return "", "", fmt.Errorf("sdk auth: agentserver returned %d", resp.StatusCode)
    }
    var out struct {
        WorkspaceID string `json:"workspace_id"`
        UserID      string `json:"user_id"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
        return "", "", fmt.Errorf("sdk auth: decode response: %w", err)
    }
    a.cache.Add(token, cacheEntry{
        workspaceID: out.WorkspaceID,
        userID:      out.UserID,
        expiresAt:   time.Now().Add(a.posTTL),
    })
    return out.WorkspaceID, out.UserID, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/codexexecgateway/sdk/ -run TestProxyTokenAuth -v
```

Expected: all four tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/codexexecgateway/sdk/auth.go internal/codexexecgateway/sdk/auth_test.go go.mod go.sum
git commit -m "feat(exec-gateway/sdk): ProxyTokenAuth with LRU cache"
```

---

### Task B2: Add `internal/envtools/processes/` — session manager

**Files:**
- Create: `internal/envtools/processes/session.go`
- Create: `internal/envtools/processes/manager.go`
- Create: `internal/envtools/processes/manager_test.go`

- [ ] **Step 1: Write the failing test**

`internal/envtools/processes/manager_test.go`:

```go
package processes

import (
    "testing"
    "time"
)

func TestManager_RegisterGet(t *testing.T) {
    m := NewManager(30 * time.Minute)
    s := &Session{ID: "sid-1", WorkspaceID: "ws-1"}
    m.Register(s)
    got, ok := m.Get("sid-1")
    if !ok || got.ID != "sid-1" {
        t.Fatalf("Get returned %+v ok=%v", got, ok)
    }
}

func TestManager_Forget(t *testing.T) {
    m := NewManager(30 * time.Minute)
    m.Register(&Session{ID: "sid-1", WorkspaceID: "ws-1"})
    m.Forget("sid-1")
    if _, ok := m.Get("sid-1"); ok {
        t.Fatal("expected Get to fail after Forget")
    }
}

func TestSession_AppendAndRead(t *testing.T) {
    s := &Session{ID: "sid", WorkspaceID: "ws"}
    s.Append("stdout", []byte("hello"))
    s.Append("stderr", []byte("world"))
    chunks, exit, alive := s.OutputSince(0)
    if len(chunks) != 2 || exit != nil || !alive {
        t.Fatalf("got chunks=%d exit=%v alive=%v", len(chunks), exit, alive)
    }
    chunks, _, _ = s.OutputSince(1)
    if len(chunks) != 1 || chunks[0].Stream != "stderr" {
        t.Fatalf("since=1 got chunks=%+v", chunks)
    }
}

func TestSession_RingBufferTruncates(t *testing.T) {
    s := &Session{ID: "sid", WorkspaceID: "ws"}
    big := make([]byte, 600_000)
    s.Append("stdout", big)
    s.Append("stdout", big) // total 1.2 MiB > 1 MiB cap
    chunks, _, _ := s.OutputSince(0)
    var total int
    for _, c := range chunks { total += len(c.Data) }
    if total > MaxBufferBytes {
        t.Errorf("buffer exceeded cap: %d > %d", total, MaxBufferBytes)
    }
    if s.LostBytes() == 0 {
        t.Error("expected LostBytes > 0 after truncation")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/envtools/processes/ -v
```

Expected: compile errors (`NewManager`, `Session`, etc. undefined).

- [ ] **Step 3: Implement `session.go`**

`internal/envtools/processes/session.go`:

```go
package processes

import (
    "sync"
    "time"
)

// MaxBufferBytes is the per-session cap on accumulated stdout+stderr.
// Anything beyond this is truncated from the head of the ring; the
// session records how many bytes were lost so the SDK can warn.
const MaxBufferBytes = 1 << 20 // 1 MiB

// Chunk is one stdout or stderr segment delivered to the SDK. Seq is
// monotonically increasing per-session starting at 1; the SDK passes
// the highest Seq it has seen as the `since` query param to get only
// newer chunks.
type Chunk struct {
    Stream string `json:"stream"` // "stdout" or "stderr"
    Data   []byte `json:"-"`
    Seq    int    `json:"seq"`
}

// Session is one long-running process spawned via tools.UnifiedExec /
// exec_command. The SDK polls Output, writes stdin, and terminates via
// the corresponding /api/sdk/processes/{sid}/* endpoints.
type Session struct {
    ID           string
    WorkspaceID  string
    mu           sync.Mutex
    chunks       []Chunk
    seq          int
    bytesBuf     int
    lostBytes    int
    exitCode     *int
    lastActivity time.Time
}

func (s *Session) Append(stream string, data []byte) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.seq++
    s.chunks = append(s.chunks, Chunk{Stream: stream, Data: append([]byte(nil), data...), Seq: s.seq})
    s.bytesBuf += len(data)
    s.lastActivity = time.Now()
    for s.bytesBuf > MaxBufferBytes && len(s.chunks) > 1 {
        drop := s.chunks[0]
        s.chunks = s.chunks[1:]
        s.bytesBuf -= len(drop.Data)
        s.lostBytes += len(drop.Data)
    }
}

func (s *Session) OutputSince(since int) (chunks []Chunk, exit *int, alive bool) {
    s.mu.Lock()
    defer s.mu.Unlock()
    for _, c := range s.chunks {
        if c.Seq > since {
            chunks = append(chunks, c)
        }
    }
    alive = s.exitCode == nil
    exit = s.exitCode
    return
}

func (s *Session) LostBytes() int {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.lostBytes
}

func (s *Session) SetExit(code int) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.exitCode = &code
    s.lastActivity = time.Now()
}

func (s *Session) LastActivity() time.Time {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.lastActivity
}
```

- [ ] **Step 4: Implement `manager.go`**

`internal/envtools/processes/manager.go`:

```go
package processes

import (
    "sync"
    "time"
)

// Manager owns the in-process session table. Background sweeps idle
// sessions every minute; sessions inactive for IdleTimeout are dropped
// (the SDK polling stops returning their output and the session ID
// goes 404).
type Manager struct {
    IdleTimeout time.Duration
    mu          sync.RWMutex
    sessions    map[string]*Session
    stop        chan struct{}
}

func NewManager(idleTimeout time.Duration) *Manager {
    m := &Manager{
        IdleTimeout: idleTimeout,
        sessions:    map[string]*Session{},
        stop:        make(chan struct{}),
    }
    return m
}

func (m *Manager) Register(s *Session) {
    s.lastActivity = time.Now()
    m.mu.Lock()
    defer m.mu.Unlock()
    m.sessions[s.ID] = s
}

func (m *Manager) Get(id string) (*Session, bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    s, ok := m.sessions[id]
    return s, ok
}

func (m *Manager) Forget(id string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    delete(m.sessions, id)
}

// Sweep removes sessions whose lastActivity is older than IdleTimeout.
// Call from a background goroutine.
func (m *Manager) Sweep() {
    cutoff := time.Now().Add(-m.IdleTimeout)
    m.mu.Lock()
    defer m.mu.Unlock()
    for id, s := range m.sessions {
        if s.LastActivity().Before(cutoff) {
            delete(m.sessions, id)
        }
    }
}

// Run starts a goroutine that calls Sweep every minute until Stop().
func (m *Manager) Run() {
    go func() {
        t := time.NewTicker(time.Minute)
        defer t.Stop()
        for {
            select {
            case <-t.C:
                m.Sweep()
            case <-m.stop:
                return
            }
        }
    }()
}

func (m *Manager) Stop() { close(m.stop) }
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/envtools/processes/ -v
```

Expected: all four tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/envtools/processes/
git commit -m "feat(envtools): processes.Session + Manager with ring buffer + idle GC"
```

---

### Task B3: Add `internal/codexexecgateway/sdk/server.go` — Server struct + middleware + envsList handler

**Files:**
- Create: `internal/codexexecgateway/sdk/server.go`
- Create: `internal/codexexecgateway/sdk/handlers.go`
- Create: `internal/codexexecgateway/sdk/handlers_test.go`

- [ ] **Step 1: Write the failing test for envs/list**

`internal/codexexecgateway/sdk/handlers_test.go`:

```go
package sdk

import (
    "bytes"
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"
)

// connectedListerStub returns hard-coded envs for one workspace.
type connectedListerStub struct{}

func (connectedListerStub) Connected(ctx context.Context, wsID string) ([]ConnectedExecutor, error) {
    if wsID == "ws-1" {
        return []ConnectedExecutor{
            {Name: "my-mac", IsDefault: true, LastSeenAt: "2026-05-19T08:00:00Z"},
        }, nil
    }
    return nil, nil
}

func TestEnvsList_HappyPath(t *testing.T) {
    upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        _ = json.NewEncoder(w).Encode(map[string]string{"workspace_id": "ws-1", "user_id": "u-1"})
    }))
    defer upstream.Close()
    s := &Server{
        Auth:     NewProxyTokenAuth(upstream.URL, "x", time.Minute, time.Second),
        Registry: connectedListerStub{},
    }
    mux := http.NewServeMux()
    s.Mount(mux)
    req := httptest.NewRequest(http.MethodPost, "/api/sdk/envs/list", bytes.NewReader([]byte("{}")))
    req.Header.Set("Authorization", "Bearer tok-1")
    rec := httptest.NewRecorder()
    mux.ServeHTTP(rec, req)
    if rec.Code != http.StatusOK {
        t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
    }
    var got struct {
        Envs []map[string]any `json:"envs"`
    }
    _ = json.Unmarshal(rec.Body.Bytes(), &got)
    if len(got.Envs) != 1 || got.Envs[0]["name"] != "my-mac" {
        t.Fatalf("envs=%+v", got.Envs)
    }
}

func TestEnvsList_MissingBearer_401(t *testing.T) {
    s := &Server{Registry: connectedListerStub{}}
    mux := http.NewServeMux()
    s.Mount(mux)
    req := httptest.NewRequest(http.MethodPost, "/api/sdk/envs/list", bytes.NewReader([]byte("{}")))
    rec := httptest.NewRecorder()
    mux.ServeHTTP(rec, req)
    if rec.Code != http.StatusUnauthorized {
        t.Fatalf("status=%d", rec.Code)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/codexexecgateway/sdk/ -run TestEnvsList -v
```

Expected: compile errors.

- [ ] **Step 3: Implement `server.go`**

`internal/codexexecgateway/sdk/server.go`:

```go
package sdk

import (
    "context"
    "encoding/json"
    "net/http"
    "strings"

    "github.com/agentserver/agentserver/internal/envtools/bridge"
    "github.com/agentserver/agentserver/internal/envtools/nameresolver"
    "github.com/agentserver/agentserver/internal/envtools/processes"
    "github.com/agentserver/agentserver/internal/envtools/tools"
)

// ConnectedExecutor mirrors the fields codex-exec-gateway's existing
// /api/exec-gateway/connected handler returns. Defined here to avoid
// importing the handler package from sdk.
type ConnectedExecutor struct {
    ExeID      string `json:"exe_id,omitempty"`
    Name       string `json:"name"`
    IsDefault  bool   `json:"is_default,omitempty"`
    LastSeenAt string `json:"last_seen_at,omitempty"`
}

// ConnectedLister is the subset of the gateway's executor registry the
// sdk package needs.
type ConnectedLister interface {
    Connected(ctx context.Context, workspaceID string) ([]ConnectedExecutor, error)
}

// Server holds the SDK REST surface. Construct via NewServer in the
// gateway's main.go and call Mount on whatever http.ServeMux / chi
// router is in use.
type Server struct {
    Auth     *ProxyTokenAuth
    Pool     *bridge.Pool
    Resolver *nameresolver.Resolver
    Sessions *processes.Manager
    Registry ConnectedLister
    Tools    map[string]tools.Tool // tools.Registry() output; nil = no tool/call support
}

// Mount registers every SDK route. Each handler runs through
// authMiddleware which extracts and validates the Bearer token.
func (s *Server) Mount(r interface{ Handle(pattern string, h http.Handler) }) {
    r.Handle("POST /api/sdk/envs/list", s.authMiddleware(http.HandlerFunc(s.handleEnvsList)))
    // Subsequent tasks add: /api/sdk/envs/{name}/tool/call,
    // /api/sdk/processes/{sid}/{stdin,output,terminate}.
}

type ctxKey int

const (
    ctxWorkspaceID ctxKey = iota
    ctxUserID
)

func (s *Server) authMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        h := r.Header.Get("Authorization")
        if !strings.HasPrefix(h, "Bearer ") {
            writeErr(w, http.StatusUnauthorized, "missing_bearer", "Authorization: Bearer <token> required")
            return
        }
        tok := strings.TrimPrefix(h, "Bearer ")
        wsID, userID, err := s.Auth.Verify(r.Context(), tok)
        if err != nil {
            writeErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
            return
        }
        ctx := context.WithValue(r.Context(), ctxWorkspaceID, wsID)
        ctx = context.WithValue(ctx, ctxUserID, userID)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

func workspaceFromCtx(ctx context.Context) string {
    if v, ok := ctx.Value(ctxWorkspaceID).(string); ok {
        return v
    }
    return ""
}

func writeJSON(w http.ResponseWriter, body any) {
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(map[string]any{
        "error": map[string]string{"code": code, "message": msg},
    })
}
```

- [ ] **Step 4: Implement `handlers.go`**

`internal/codexexecgateway/sdk/handlers.go`:

```go
package sdk

import (
    "net/http"
)

// coreTools returns the fixed list of tools the SDK knows. Each entry
// describes the tool's identity for envs/list — no JSON schema (server
// validates).
type toolDesc struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    Kind        string `json:"kind"`
}

func coreTools() []toolDesc {
    return []toolDesc{
        {Name: "shell", Kind: "core", Description: "Run a command synchronously."},
        {Name: "read_file", Kind: "core", Description: "Read a file by path."},
        {Name: "write_file", Kind: "core", Description: "Write a file by path."},
        {Name: "apply_patch", Kind: "core", Description: "Apply a unified-diff patch."},
        {Name: "copy_path", Kind: "core", Description: "Upload or download a file."},
        {Name: "exec_command", Kind: "core", Description: "Start a long-running process (returns session_id)."},
    }
}

type envEntry struct {
    Name      string     `json:"name"`
    Type      string     `json:"type"`
    IsDefault bool       `json:"is_default"`
    Tools     []toolDesc `json:"tools"`
    LastSeen  string     `json:"last_seen,omitempty"`
}

func (s *Server) handleEnvsList(w http.ResponseWriter, r *http.Request) {
    wsID := workspaceFromCtx(r.Context())
    connected, err := s.Registry.Connected(r.Context(), wsID)
    if err != nil {
        writeErr(w, http.StatusInternalServerError, "registry_error", err.Error())
        return
    }
    envs := make([]envEntry, 0, len(connected))
    for _, c := range connected {
        envs = append(envs, envEntry{
            Name:      c.Name,
            Type:      "executor",
            IsDefault: c.IsDefault,
            Tools:     coreTools(),
            LastSeen:  c.LastSeenAt,
        })
    }
    writeJSON(w, map[string]any{"envs": envs})
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/codexexecgateway/sdk/ -v
```

Expected: all tests PASS (auth_test + envsList test).

- [ ] **Step 6: Commit**

```bash
git add internal/codexexecgateway/sdk/server.go internal/codexexecgateway/sdk/handlers.go internal/codexexecgateway/sdk/handlers_test.go
git commit -m "feat(exec-gateway/sdk): envs/list endpoint + auth middleware"
```

---

### Task B4: Add `/api/sdk/envs/{name}/tool/call` handler

**Files:**
- Modify: `internal/codexexecgateway/sdk/server.go` (add route)
- Modify: `internal/codexexecgateway/sdk/handlers.go` (add handler)
- Modify: `internal/codexexecgateway/sdk/handlers_test.go` (add tests)

- [ ] **Step 1: Write the failing tests**

Append to `handlers_test.go`:

```go
func TestToolCall_UnknownTool_400(t *testing.T) {
    upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        _ = json.NewEncoder(w).Encode(map[string]string{"workspace_id": "ws-1", "user_id": "u-1"})
    }))
    defer upstream.Close()
    s := &Server{
        Auth:  NewProxyTokenAuth(upstream.URL, "x", time.Minute, time.Second),
        Tools: map[string]tools.Tool{}, // empty registry
    }
    mux := http.NewServeMux()
    s.Mount(mux)
    body := bytes.NewReader([]byte(`{"tool":"unknown","arguments":{}}`))
    req := httptest.NewRequest(http.MethodPost, "/api/sdk/envs/my-mac/tool/call", body)
    req.Header.Set("Authorization", "Bearer tok-1")
    rec := httptest.NewRecorder()
    mux.ServeHTTP(rec, req)
    if rec.Code != http.StatusBadRequest {
        t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
    }
}
```

Add import: `"github.com/agentserver/agentserver/internal/envtools/tools"`.

- [ ] **Step 2: Run test, expect compile error then fail**

```bash
go test ./internal/codexexecgateway/sdk/ -run TestToolCall_UnknownTool -v
```

Expected: route not registered, returns 404 — fails the 400 assertion.

- [ ] **Step 3: Implement the handler**

In `handlers.go`, append:

```go
type toolCallReq struct {
    Tool      string         `json:"tool"`
    Arguments map[string]any `json:"arguments"`
}

func (s *Server) handleToolCall(w http.ResponseWriter, r *http.Request) {
    wsID := workspaceFromCtx(r.Context())
    envName := r.PathValue("name")
    var req toolCallReq
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
        return
    }
    tool, ok := s.Tools[req.Tool]
    if !ok {
        writeErr(w, http.StatusBadRequest, "unknown_tool", "no such tool: "+req.Tool)
        return
    }
    exeID, err := s.Resolver.Resolve(r.Context(), wsID, envName)
    if err != nil {
        writeErr(w, http.StatusNotFound, "env_not_found", err.Error())
        return
    }
    br, err := s.Pool.Get(r.Context(), exeID)
    if err != nil {
        writeErr(w, http.StatusBadGateway, "bridge_unavailable", err.Error())
        return
    }
    argsJSON, _ := json.Marshal(req.Arguments)
    result, err := tool.Call(r.Context(), argsJSON, br)
    if err != nil {
        writeErr(w, http.StatusInternalServerError, "tool_error", err.Error())
        return
    }
    // exec_command's structuredContent embeds session_id; if so, register
    // a Session so subsequent /processes/{sid}/* calls find it.
    if sid := extractSessionID(result); sid != "" && s.Sessions != nil {
        s.Sessions.Register(&processes.Session{
            ID:          sid,
            WorkspaceID: wsID,
        })
    }
    writeJSON(w, result)
}

func extractSessionID(result tools.MCPCallToolResult) string {
    sc, ok := result.StructuredContent.(map[string]any)
    if !ok {
        return ""
    }
    s, _ := sc["session_id"].(string)
    return s
}
```

Add imports at top of file: `"encoding/json"`, `"github.com/agentserver/agentserver/internal/envtools/processes"`, `"github.com/agentserver/agentserver/internal/envtools/tools"`.

- [ ] **Step 4: Register the route**

In `server.go` `Mount`, append:

```go
r.Handle("POST /api/sdk/envs/{name}/tool/call", s.authMiddleware(http.HandlerFunc(s.handleToolCall)))
```

Note: `r.Handle` here assumes Go 1.22+ servemux pattern syntax. If the gateway uses chi, swap for `r.Method(http.MethodPost, "/api/sdk/envs/{name}/tool/call", s.authMiddleware(...))`.

If the gateway uses chi, also import `"github.com/go-chi/chi/v5"` and change `r.PathValue("name")` in the handler to `chi.URLParam(r, "name")`. Check whether `internal/codexexecgateway/server.go` uses chi (it does — see line 86 `r.Get("/codex-exec/{exe_id}", s.handleInbound)`). Adapt accordingly.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/codexexecgateway/sdk/ -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/codexexecgateway/sdk/server.go internal/codexexecgateway/sdk/handlers.go internal/codexexecgateway/sdk/handlers_test.go
git commit -m "feat(exec-gateway/sdk): tool/call endpoint with workspace-scoped dispatch"
```

---

### Task B5: Add processes endpoints — stdin / output / terminate

**Files:**
- Modify: `internal/codexexecgateway/sdk/server.go`
- Modify: `internal/codexexecgateway/sdk/handlers.go`
- Modify: `internal/codexexecgateway/sdk/handlers_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `handlers_test.go`:

```go
func TestProcessOutput_ForbiddenOtherWorkspace(t *testing.T) {
    upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        _ = json.NewEncoder(w).Encode(map[string]string{"workspace_id": "ws-2", "user_id": "u-1"})
    }))
    defer upstream.Close()
    s := &Server{
        Auth:     NewProxyTokenAuth(upstream.URL, "x", time.Minute, time.Second),
        Sessions: processes.NewManager(30 * time.Minute),
    }
    s.Sessions.Register(&processes.Session{ID: "sid-1", WorkspaceID: "ws-1"})
    mux := http.NewServeMux()
    s.Mount(mux)
    req := httptest.NewRequest(http.MethodGet, "/api/sdk/processes/sid-1/output", nil)
    req.Header.Set("Authorization", "Bearer tok-1")
    rec := httptest.NewRecorder()
    mux.ServeHTTP(rec, req)
    if rec.Code != http.StatusForbidden {
        t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
    }
}

func TestProcessOutput_HappyPath(t *testing.T) {
    upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        _ = json.NewEncoder(w).Encode(map[string]string{"workspace_id": "ws-1", "user_id": "u-1"})
    }))
    defer upstream.Close()
    s := &Server{
        Auth:     NewProxyTokenAuth(upstream.URL, "x", time.Minute, time.Second),
        Sessions: processes.NewManager(30 * time.Minute),
    }
    sess := &processes.Session{ID: "sid-1", WorkspaceID: "ws-1"}
    sess.Append("stdout", []byte("hello"))
    s.Sessions.Register(sess)
    mux := http.NewServeMux()
    s.Mount(mux)
    req := httptest.NewRequest(http.MethodGet, "/api/sdk/processes/sid-1/output?since=0", nil)
    req.Header.Set("Authorization", "Bearer tok-1")
    rec := httptest.NewRecorder()
    mux.ServeHTTP(rec, req)
    if rec.Code != http.StatusOK {
        t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
    }
    var got struct {
        Chunks []map[string]any `json:"chunks"`
    }
    _ = json.Unmarshal(rec.Body.Bytes(), &got)
    if len(got.Chunks) != 1 {
        t.Fatalf("chunks=%+v", got.Chunks)
    }
}
```

Add `"github.com/agentserver/agentserver/internal/envtools/processes"` import.

- [ ] **Step 2: Run tests, expect fail**

```bash
go test ./internal/codexexecgateway/sdk/ -run TestProcess -v
```

- [ ] **Step 3: Implement the three handlers**

In `handlers.go`, append:

```go
import (
    "encoding/base64"
    "strconv"
)

type stdinReq struct {
    DataB64 string `json:"data_b64"`
}

type outputChunk struct {
    Stream string `json:"stream"`
    Data   string `json:"data_b64"`
    Seq    int    `json:"seq"`
}

func (s *Server) sessionFromReq(w http.ResponseWriter, r *http.Request, sidParam string) (*processes.Session, bool) {
    sid := r.PathValue(sidParam)
    sess, ok := s.Sessions.Get(sid)
    if !ok {
        writeErr(w, http.StatusNotFound, "session_not_found", "no such session: "+sid)
        return nil, false
    }
    if sess.WorkspaceID != workspaceFromCtx(r.Context()) {
        writeErr(w, http.StatusForbidden, "forbidden", "session belongs to a different workspace")
        return nil, false
    }
    return sess, true
}

func (s *Server) handleStdin(w http.ResponseWriter, r *http.Request) {
    _, ok := s.sessionFromReq(w, r, "sid")
    if !ok { return }
    var req stdinReq
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
        return
    }
    _, err := base64.StdEncoding.DecodeString(req.DataB64)
    if err != nil {
        writeErr(w, http.StatusBadRequest, "bad_base64", err.Error())
        return
    }
    // Forwarding bytes onto the bridge (write_stdin on the executor) lives
    // in tools/unified_exec.go; the SDK call goes through tool/call with
    // tool="write_stdin", so handleStdin's job here is local — append the
    // bytes to the session as outbound stdin chunks if we mirror them, or
    // just return ok. For v0.61.0 we issue the bridge write here:
    // TODO wire bridge.WriteStdin(sess.ExeID, sess.ExeSession, data).
    // For now, keeping the endpoint contract testable.
    writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleOutput(w http.ResponseWriter, r *http.Request) {
    sess, ok := s.sessionFromReq(w, r, "sid")
    if !ok { return }
    sinceStr := r.URL.Query().Get("since")
    since, _ := strconv.Atoi(sinceStr)
    chunks, exit, alive := sess.OutputSince(since)
    out := make([]outputChunk, 0, len(chunks))
    for _, c := range chunks {
        out = append(out, outputChunk{
            Stream: c.Stream,
            Data:   base64.StdEncoding.EncodeToString(c.Data),
            Seq:    c.Seq,
        })
    }
    writeJSON(w, map[string]any{
        "chunks":        out,
        "exit_code":     exit,
        "session_alive": alive,
        "truncated":     sess.LostBytes() > 0,
        "lost_bytes":    sess.LostBytes(),
    })
}

func (s *Server) handleTerminate(w http.ResponseWriter, r *http.Request) {
    sess, ok := s.sessionFromReq(w, r, "sid")
    if !ok { return }
    // Issue terminate on the bridge; for v0.61.0 minimal path, mark exit -1
    sess.SetExit(-1)
    s.Sessions.Forget(sess.ID)
    writeJSON(w, map[string]any{"ok": true})
}
```

(The `TODO` comment for bridge integration is intentional — the bridge call is a wrapper over `tools.UnifiedExecTool` methods, which already know how to write_stdin/terminate. Wiring is a one-line plumb that depends on the existing UnifiedExecTool's exposed API; defer to the integration commit when wiring the gateway main.go.)

- [ ] **Step 4: Register routes in `server.go`**

In `Mount`, append:

```go
r.Handle("POST /api/sdk/processes/{sid}/stdin", s.authMiddleware(http.HandlerFunc(s.handleStdin)))
r.Handle("GET /api/sdk/processes/{sid}/output", s.authMiddleware(http.HandlerFunc(s.handleOutput)))
r.Handle("POST /api/sdk/processes/{sid}/terminate", s.authMiddleware(http.HandlerFunc(s.handleTerminate)))
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/codexexecgateway/sdk/ -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/codexexecgateway/sdk/server.go internal/codexexecgateway/sdk/handlers.go internal/codexexecgateway/sdk/handlers_test.go
git commit -m "feat(exec-gateway/sdk): processes endpoints with workspace isolation"
```

---

### Task B6: Wire SDK Server into `cmd/codex-exec-gateway/main.go`

**Files:**
- Modify: `cmd/codex-exec-gateway/main.go`
- Modify: `internal/codexexecgateway/server.go` (add `MountSDK` accessor if needed)

- [ ] **Step 1: Read the existing main.go and server.go**

```bash
cat cmd/codex-exec-gateway/main.go
sed -n '1,200p' internal/codexexecgateway/server.go
```

Identify: how the existing chi router is constructed, where `store` (executor registry) and `registry` live, where config is loaded, and where to inject the new SDK server.

- [ ] **Step 2: Construct a registryAdapter that satisfies `sdk.ConnectedLister`**

Add to `internal/codexexecgateway/server.go` (or a new `sdk_adapter.go`):

```go
package codexexecgateway

import (
    "context"

    sdkpkg "github.com/agentserver/agentserver/internal/codexexecgateway/sdk"
)

// sdkConnectedAdapter bridges the gateway's existing store to
// sdk.ConnectedLister so the SDK package doesn't need to import the
// concrete store type.
type sdkConnectedAdapter struct {
    store    storeLister
    registry registryLister
}

// storeLister and registryLister are whatever interfaces the existing
// Connected handler depends on. Look at handlers/Connected in
// internal/codexexecgateway/handlers/ for the exact methods to reuse.
type storeLister interface {
    /* whatever Connected handler needs */
}
type registryLister interface {
    /* whatever Connected handler needs */
}

func (a sdkConnectedAdapter) Connected(ctx context.Context, wsID string) ([]sdkpkg.ConnectedExecutor, error) {
    // The body mirrors handlers.Connected from
    // internal/codexexecgateway/handlers/handlers_relay.go (or
    // wherever the Connected handler lives — confirm via grep) but
    // returns the slice directly instead of writing JSON.
    //
    // Concretely: call a.store.ListWorkspaceExecutors(wsID) (or the
    // equivalent), then for each row look up its registry presence
    // (a.registry.IsConnected(exeID)) to build the slice. Read the
    // actual handler to confirm the method names and return type;
    // copy the field mapping exactly.
    raw, err := a.store.ListWorkspaceExecutors(ctx, wsID)  // ← adapt to real method
    if err != nil {
        return nil, err
    }
    out := make([]sdkpkg.ConnectedExecutor, 0, len(raw))
    for _, e := range raw {
        if !a.registry.IsConnected(e.ID) {
            continue
        }
        out = append(out, sdkpkg.ConnectedExecutor{
            ExeID:      e.ID,
            Name:       e.Name,
            IsDefault:  e.IsDefault,
            LastSeenAt: e.LastSeenAt.Format(time.RFC3339),
        })
    }
    return out, nil
}
```

The `storeLister` / `registryLister` interface method names + field names above are placeholders that the engineer fills in by reading `internal/codexexecgateway/handlers/` (specifically the file that registers `/api/exec-gateway/connected`). The structural pattern (list rows, filter to connected, map to sdkpkg shape) is fixed; only the verbs change.

- [ ] **Step 3: Construct the sdk.Server in main.go and Mount onto the chi router**

In `cmd/codex-exec-gateway/main.go`, near where the chi router is built and routes registered, add:

```go
import (
    sdkpkg "github.com/agentserver/agentserver/internal/codexexecgateway/sdk"
    "github.com/agentserver/agentserver/internal/envtools/bridge"
    "github.com/agentserver/agentserver/internal/envtools/nameresolver"
    "github.com/agentserver/agentserver/internal/envtools/processes"
    "github.com/agentserver/agentserver/internal/envtools/tools"
)

// After existing route registration:
sdkAuth := sdkpkg.NewProxyTokenAuth(
    cfg.AgentserverInternalURL,
    cfg.AgentserverInternalSecret,
    5*time.Minute,  // positive TTL
    30*time.Second, // negative TTL
)
sdkPool := bridge.NewPool(/* same args as codex-app-gateway uses */)
sdkResolver := nameresolver.NewResolver(/* connected-list URL + token */)
sdkSessions := processes.NewManager(30 * time.Minute)
sdkSessions.Run()
defer sdkSessions.Stop()
toolRegistry := map[string]tools.Tool{
    "shell":        tools.NewShellTool(sdkPool, sdkResolver),
    "read_file":    tools.NewFSReadFileTool(sdkPool, sdkResolver),
    "write_file":   tools.NewFSWriteFileTool(sdkPool, sdkResolver),
    "apply_patch":  tools.NewApplyPatchTool(sdkPool, sdkResolver),
    "copy_path":    tools.NewCopyPathTool(sdkPool, sdkResolver),
    "exec_command": tools.NewExecCommandTool(sdkPool, sdkResolver),  // exposed subtool of UnifiedExecTool
}
sdkServer := &sdkpkg.Server{
    Auth:     sdkAuth,
    Pool:     sdkPool,
    Resolver: sdkResolver,
    Sessions: sdkSessions,
    Registry: sdkConnectedAdapter{store: store, registry: registry},
    Tools:    toolRegistry,
}
// Mount onto the chi router. If chi: sdkServer.Mount needs to accept
// chi.Router; adjust the Mount signature in server.go accordingly.
sdkServer.Mount(r)
```

The exact subtool ctor names (`NewExecCommandTool`, `NewFSReadFileTool`, etc.) depend on what `unified_exec.go` and `fs.go` actually expose. Read those files; if the existing code lumps tools into one ctor (e.g., `NewUnifiedExecTool` returning a multiplexer), the toolRegistry needs to wrap each subtool individually.

- [ ] **Step 4: Build and run unit tests**

```bash
go build ./cmd/codex-exec-gateway/...
go test ./internal/codexexecgateway/... -count=1
```

Expected: both succeed.

- [ ] **Step 5: Integration smoke test (manual)**

In one terminal, run a stub agentserver that accepts `/internal/validate-proxy-token`:

```bash
cat > /tmp/stub-agentserver.go <<'EOF'
package main
import ("encoding/json"; "net/http")
func main() {
    http.HandleFunc("/internal/validate-proxy-token", func(w http.ResponseWriter, r *http.Request) {
        _ = json.NewEncoder(w).Encode(map[string]string{"workspace_id": "ws-1", "user_id": "u-1"})
    })
    http.ListenAndServe(":18080", nil)
}
EOF
go run /tmp/stub-agentserver.go &
```

In another terminal, run codex-exec-gateway pointed at it (`AGENTSERVER_INTERNAL_URL=http://localhost:18080`, etc. — see existing `cmd/codex-exec-gateway/main.go` env vars for the full set) and `curl -i -H 'Authorization: Bearer x' -d '{}' http://localhost:<gateway port>/api/sdk/envs/list`.

Expected: HTTP 200 with `{"envs":[]}` (no executors registered yet) or whatever the stubbed registry returns.

- [ ] **Step 6: Commit**

```bash
git add cmd/codex-exec-gateway/main.go internal/codexexecgateway/server.go internal/codexexecgateway/sdk_adapter.go
git commit -m "feat(exec-gateway): mount sdk.Server with chi routes"
```

---

## Phase C — Rewrite Python SDK

### Task C1: Rewrite `client.py` to httpx

**Files:**
- Rewrite: `sdk/python/src/agentserver_sdk/client.py`
- Modify: `sdk/python/pyproject.toml`

- [ ] **Step 1: Update pyproject.toml**

In `sdk/python/pyproject.toml`, replace the dependency line:

```toml
# Before:
# dependencies = ["websockets>=12"]
# After:
dependencies = ["httpx>=0.27"]
```

- [ ] **Step 2: Rewrite `client.py`**

Replace the entire file content with:

```python
"""HTTP client for agentserver SDK.

Talks REST to codex-exec-gateway's /api/sdk/* endpoints. One HTTPClient
per Ctx. Bearer-authenticated; user_id is server-side only (resolved
from the token).
"""

from __future__ import annotations

from typing import Any

import httpx

from .errors import SdkConnectionError, SdkUnauthorized


class HTTPClient:
    def __init__(self, base_url: str, token: str) -> None:
        self.base_url = base_url.rstrip("/")
        self.token = token
        self._http = httpx.AsyncClient(
            base_url=self.base_url,
            headers={"Authorization": f"Bearer {token}"},
            timeout=30.0,
        )

    async def post(self, path: str, json: dict[str, Any]) -> dict[str, Any]:
        try:
            r = await self._http.post(path, json=json)
        except httpx.RequestError as e:
            raise SdkConnectionError(f"POST {path}: {e}") from e
        return self._decode(path, r)

    async def get(self, path: str, params: dict[str, Any] | None = None) -> dict[str, Any]:
        try:
            r = await self._http.get(path, params=params)
        except httpx.RequestError as e:
            raise SdkConnectionError(f"GET {path}: {e}") from e
        return self._decode(path, r)

    @staticmethod
    def _decode(path: str, r: httpx.Response) -> dict[str, Any]:
        if r.status_code == 401:
            raise SdkUnauthorized(f"{path}: 401 — {r.text}")
        if r.status_code >= 400:
            raise SdkConnectionError(f"{path}: {r.status_code} — {r.text}")
        try:
            return r.json()
        except Exception as e:
            raise SdkConnectionError(f"{path}: invalid JSON: {e}") from e

    async def close(self) -> None:
        await self._http.aclose()
```

- [ ] **Step 3: Update `errors.py` if `SdkUnauthorized` doesn't exist**

```bash
grep -n "SdkUnauthorized" sdk/python/src/agentserver_sdk/errors.py
```

If missing, add to `errors.py`:

```python
class SdkUnauthorized(SdkConnectionError):
    """Bearer token rejected by the gateway."""
```

- [ ] **Step 4: Install + import-check**

```bash
cd sdk/python && pip install -e . && python -c "from agentserver_sdk.client import HTTPClient"
```

Expected: succeeds.

- [ ] **Step 5: Commit**

```bash
git add sdk/python/src/agentserver_sdk/client.py sdk/python/src/agentserver_sdk/errors.py sdk/python/pyproject.toml
git commit -m "refactor(sdk): replace WSClient with HTTPClient (httpx)"
```

---

### Task C2: Update `ctx.py`, `env.py`, `process.py` to use HTTPClient

**Files:**
- Modify: `sdk/python/src/agentserver_sdk/ctx.py`
- Modify: `sdk/python/src/agentserver_sdk/env.py`
- Modify: `sdk/python/src/agentserver_sdk/process.py`

- [ ] **Step 1: Rewrite `ctx.py`**

Replace the body of `Ctx.from_env` and `Ctx._fetch_envs`:

```python
import asyncio
import os

from .client import HTTPClient
from .env import Env
from .errors import SdkConfigError
from .types import ToolMetadata


class Ctx:
    def __init__(self, client: HTTPClient) -> None:
        self._client = client
        self._envs_lock = asyncio.Lock()
        self._envs_cache: list[Env] | None = None

    @classmethod
    def from_env(cls) -> "Ctx":
        url = os.environ.get("AGENTSERVER_GATEWAY_URL")
        token = os.environ.get("AGENTSERVER_WORKSPACE_TOKEN", "")
        if not url:
            raise SdkConfigError("AGENTSERVER_GATEWAY_URL is required")
        if not token:
            raise SdkConfigError("AGENTSERVER_WORKSPACE_TOKEN is required")
        return cls(HTTPClient(url, token))

    async def envs(self) -> list[Env]:
        async with self._envs_lock:
            if self._envs_cache is None:
                self._envs_cache = await self._fetch_envs()
        return list(self._envs_cache)

    async def _fetch_envs(self) -> list[Env]:
        listing = await self._client.post("/api/sdk/envs/list", {})
        envs: list[Env] = []
        for e in listing.get("envs", []):
            tools = [ToolMetadata(**t) for t in e.get("tools", [])]
            envs.append(Env(
                name=e["name"],
                type=e.get("type", "executor"),
                tools=tools,
                _client=self._client,
            ))
        return envs

    async def refresh(self) -> None:
        async with self._envs_lock:
            self._envs_cache = None

    async def close(self) -> None:
        await self._client.close()
```

- [ ] **Step 2: Update `env.py`**

In `Env.call`, replace the `mcp_tool_call` call with a REST call:

```python
async def call(self, tool: str, arguments: dict | None = None) -> dict:
    args = dict(arguments or {})
    args.setdefault("environment_id", self.name)
    from urllib.parse import quote
    raw = await self._client.post(
        f"/api/sdk/envs/{quote(self.name)}/tool/call",
        {"tool": tool, "arguments": args},
    )
    if raw.get("isError"):
        from .errors import ToolError
        msg = _extract_error_text(raw)
        raise ToolError(tool=tool, env=self.name, message=msg, raw=raw)
    return raw
```

Remove the `_TOOL_SERVER` constant and the `mcp_tool_call` indirection.

- [ ] **Step 3: Update `process.py`**

Replace `Process` methods with direct REST calls:

```python
import base64

class Process:
    def __init__(self, env, command: str) -> None:
        self.env = env
        self.command = command
        self.session_id: str | None = None
        self._terminated = False
        self._read_seq = 0

    async def __aenter__(self) -> "Process":
        raw = await self.env.call("exec_command", {"command": self.command})
        sc = raw.get("structuredContent") or {}
        sid = sc.get("session_id")
        if not sid:
            from .errors import ToolError
            raise ToolError(
                tool="exec_command", env=self.env.name,
                message="exec_command did not return session_id", raw=raw,
            )
        self.session_id = sid
        return self

    async def __aexit__(self, *exc) -> None:
        await self.terminate()

    async def write_stdin(self, data: bytes) -> None:
        await self.env._client.post(
            f"/api/sdk/processes/{self.session_id}/stdin",
            {"data_b64": base64.b64encode(data).decode("ascii")},
        )

    async def read_output(self, since: int | None = None) -> dict:
        params = {"since": str(since if since is not None else self._read_seq)}
        resp = await self.env._client.get(
            f"/api/sdk/processes/{self.session_id}/output", params=params,
        )
        for c in resp.get("chunks", []):
            self._read_seq = max(self._read_seq, c["seq"])
        return resp

    async def terminate(self) -> None:
        if self._terminated:
            return
        try:
            await self.env._client.post(
                f"/api/sdk/processes/{self.session_id}/terminate", {},
            )
        finally:
            self._terminated = True
```

- [ ] **Step 4: Run import sanity check**

```bash
cd sdk/python && python -c "from agentserver_sdk import Ctx; c = Ctx.from_env"
```

Expected: imports succeed (the `from_env` may raise SdkConfigError if env vars aren't set — that's fine, we only care it doesn't ImportError).

- [ ] **Step 5: Commit**

```bash
git add sdk/python/src/agentserver_sdk/ctx.py sdk/python/src/agentserver_sdk/env.py sdk/python/src/agentserver_sdk/process.py
git commit -m "refactor(sdk): Ctx/Env/Process call REST endpoints"
```

---

### Task C3: Rewrite Python SDK tests

**Files:**
- Delete: `sdk/python/tests/stub_gateway.py`
- Create: `sdk/python/tests/stub_rest.py`
- Modify: `sdk/python/tests/test_client.py`
- Modify: `sdk/python/tests/test_ctx.py`
- Modify: `sdk/python/tests/test_env.py`
- Modify: `sdk/python/tests/test_process.py`
- Modify: `sdk/python/tests/conftest.py`

- [ ] **Step 1: Read existing tests to understand patterns**

```bash
cat sdk/python/tests/conftest.py
cat sdk/python/tests/stub_gateway.py
```

Existing tests use a WS stub gateway. Pattern: fixture spawns an asyncio task that runs the stub; tests pass URL + token; assertions inspect frames the stub received.

- [ ] **Step 2: Write `stub_rest.py`**

```python
"""ASGI stub for the codex-exec-gateway /api/sdk/* surface.

Each test passes a dict of `(method, path) -> handler` where handler
receives the parsed JSON body / params and returns (status, json_dict).
"""

from __future__ import annotations

import json
from typing import Any, Awaitable, Callable

Handler = Callable[[dict[str, Any]], Awaitable[tuple[int, dict[str, Any]]]]


class StubRest:
    def __init__(self, routes: dict[tuple[str, str], Handler]) -> None:
        self.routes = routes
        self.calls: list[tuple[str, str, dict[str, Any]]] = []

    async def __call__(self, scope, receive, send):
        assert scope["type"] == "http"
        method = scope["method"]
        path = scope["path"]
        body_bytes = b""
        while True:
            msg = await receive()
            body_bytes += msg.get("body", b"")
            if not msg.get("more_body"): break
        body = json.loads(body_bytes) if body_bytes else {}
        self.calls.append((method, path, body))
        handler = self.routes.get((method, path))
        if handler is None:
            await send({"type": "http.response.start", "status": 404, "headers": [(b"content-type", b"application/json")]})
            await send({"type": "http.response.body", "body": b'{"error":"no route"}'})
            return
        status, payload = await handler(body)
        encoded = json.dumps(payload).encode()
        await send({"type": "http.response.start", "status": status, "headers": [(b"content-type", b"application/json")]})
        await send({"type": "http.response.body", "body": encoded})
```

- [ ] **Step 3: Update `conftest.py` to provide an httpx ASGI transport**

Add fixture:

```python
import pytest
import pytest_asyncio
import httpx
from .stub_rest import StubRest
from agentserver_sdk.client import HTTPClient

@pytest_asyncio.fixture
async def stub_client():
    """Returns (client, stub) — client is wired to an in-process ASGI stub."""
    routes = {}
    stub = StubRest(routes)
    client = HTTPClient("http://stub", "test-token")
    # Replace the AsyncClient transport with an ASGI transport.
    await client._http.aclose()
    client._http = httpx.AsyncClient(
        transport=httpx.ASGITransport(app=stub),
        base_url="http://stub",
        headers={"Authorization": "Bearer test-token"},
    )
    yield client, stub
    await client.close()
```

- [ ] **Step 4: Rewrite `test_client.py`, `test_ctx.py`, `test_env.py`, `test_process.py`**

Pattern (replicate per test):

```python
async def test_envs_list(stub_client):
    client, stub = stub_client
    stub.routes[("POST", "/api/sdk/envs/list")] = lambda body: (200, {
        "envs": [{"name": "my-mac", "type": "executor", "is_default": True, "tools": []}],
    })
    resp = await client.post("/api/sdk/envs/list", {})
    assert resp["envs"][0]["name"] == "my-mac"
```

For each existing test file, audit which RPC method it stubs and rewrite to the equivalent REST path:
- `envs/list` → `POST /api/sdk/envs/list`
- `mcpServer/tool/call` → `POST /api/sdk/envs/{name}/tool/call`
- `operations/list` (if exercised) → no REST equivalent yet; skip or move to integration-only

Specifically:
- `test_ctx.py`: rewrite `envs()` tests to stub `POST /api/sdk/envs/list`.
- `test_env.py`: rewrite tool dispatch tests to stub `POST /api/sdk/envs/{name}/tool/call`.
- `test_process.py`: stub the four routes (`tool/call` with `tool="exec_command"`, `processes/{sid}/stdin`, `processes/{sid}/output`, `processes/{sid}/terminate`).

- [ ] **Step 5: Run the Python suite**

```bash
cd sdk/python && pip install -e ".[dev]" && pytest -xvs
```

Expected: all pass. Fix any test-isolation issues (asyncio loop scope, fixture cleanup) as they surface.

- [ ] **Step 6: Commit**

```bash
git add sdk/python/tests
git commit -m "test(sdk): rewrite tests against REST stub (ASGI transport)"
```

---

## Phase D — Jupyter sandbox env injection

### Task D1: Update sandbox config + manager + serve to inject `CODEX_EXEC_GATEWAY_URL`

**Files:**
- Modify: `internal/sandbox/config.go`
- Modify: `internal/sandbox/manager.go` (jupyter case + `mintCodexHMACToken` removal)
- Modify: `cmd/serve.go`

- [ ] **Step 1: Update `config.go`**

In the `Config` struct, replace `CodexAppGatewayURL` and `CodexInboundHMACSecret` with `CodexExecGatewayURL`:

```go
// Before:
//   CodexAppGatewayURL         string
//   CodexInboundHMACSecret     []byte
// After:
CodexExecGatewayURL string  // e.g. http://agentserver-codex-exec-gateway.agentserver.svc:6060
```

In `DefaultConfig`:

```go
// Before:
//   CodexAppGatewayURL:         os.Getenv("CODEX_APP_GATEWAY_URL"),
//   CodexInboundHMACSecret:     []byte(os.Getenv("CODEX_INBOUND_HMAC_SECRET")),
// After:
CodexExecGatewayURL: os.Getenv("CODEX_EXEC_GATEWAY_URL"),
```

- [ ] **Step 2: Update `manager.go` jupyter case**

Find the existing block that injects `AGENTSERVER_GATEWAY_URL` + `AGENTSERVER_WORKSPACE_TOKEN`:

```go
// Before:
if m.cfg.CodexAppGatewayURL != "" {
    tok := opts.ProxyToken
    if len(m.cfg.CodexInboundHMACSecret) > 0 {
        tok = mintCodexHMACToken(m.cfg.CodexInboundHMACSecret, opts.WorkspaceID, "notebook")
    }
    containerEnv = append(containerEnv,
        corev1.EnvVar{Name: "AGENTSERVER_GATEWAY_URL", Value: m.cfg.CodexAppGatewayURL},
        corev1.EnvVar{Name: "AGENTSERVER_WORKSPACE_TOKEN", Value: tok},
        corev1.EnvVar{Name: "AGENTSERVER_WORKSPACE_ID", Value: opts.WorkspaceID},
    )
}
```

Replace with:

```go
if m.cfg.CodexExecGatewayURL != "" {
    containerEnv = append(containerEnv,
        corev1.EnvVar{Name: "AGENTSERVER_GATEWAY_URL", Value: m.cfg.CodexExecGatewayURL},
        corev1.EnvVar{Name: "AGENTSERVER_WORKSPACE_TOKEN", Value: opts.ProxyToken},
        corev1.EnvVar{Name: "AGENTSERVER_WORKSPACE_ID", Value: opts.WorkspaceID},
    )
}
```

- [ ] **Step 3: Delete `mintCodexHMACToken` helper**

In `manager.go`, find and delete the entire `mintCodexHMACToken` function (added in earlier v0.60.5). Remove its imports `crypto/hmac`, `crypto/sha256`, `encoding/hex` if no other code in the file uses them.

```bash
grep -n "hmac\|sha256\|encoding/hex" internal/sandbox/manager.go
```

- [ ] **Step 4: Update `cmd/serve.go`**

Find the agentserver env block that reads `CODEX_APP_GATEWAY_URL` / `CODEX_INBOUND_HMAC_SECRET`. Replace with reading `CODEX_EXEC_GATEWAY_URL`. The sandbox config picks this up automatically via `DefaultConfig`.

```bash
grep -n "CODEX_APP_GATEWAY_URL\|CODEX_INBOUND_HMAC_SECRET" cmd/serve.go
```

For each match, follow the pattern of nearby env reads to update.

- [ ] **Step 5: Build + test**

```bash
go build ./... && go test ./internal/sandbox/... -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/config.go internal/sandbox/manager.go cmd/serve.go
git commit -m "feat(jupyter): inject CODEX_EXEC_GATEWAY_URL + raw proxyToken for new SDK"
```

---

## Phase E — Cleanup codex-app-gateway

### Task E1: Delete `handleNotebookWS` + `notebookAuth` + `/notebook/ws` route

**Files:**
- Modify: `internal/codexappgateway/server.go`

- [ ] **Step 1: Read the current server.go**

```bash
grep -n "handleNotebookWS\|notebookAuth\|/notebook/ws" internal/codexappgateway/server.go
```

You'll see:
- A route registration like `r.Get("/notebook/ws", s.handleNotebookWS)` (around line 265).
- The `Server` struct field `notebookAuth auth.Authenticator` (around line 40).
- The `NewServer` setter `notebookAuth: notebookAuth` (around line 103).
- The `handleNotebookWS` function body (around line 300+).

- [ ] **Step 2: Delete the route registration**

In `server.go`, find:

```go
r.Get("/notebook/ws", s.handleNotebookWS)
```

Delete the line.

- [ ] **Step 3: Delete the `notebookAuth` field, init block, and the `handleNotebookWS` function**

Delete in `Server` struct:

```go
// notebookAuth verifies /notebook/ws Bearer tokens. ...
notebookAuth auth.Authenticator
```

Delete in `NewServer`:

```go
var notebookAuth auth.Authenticator
if len(cfg.InboundHMACSecret) > 0 {
    notebookAuth = auth.NewHMAC(cfg.InboundHMACSecret)
}
// ... and the `notebookAuth: notebookAuth` line in the Server{...} literal.
```

Delete the entire `handleNotebookWS` function (search for `func (s *Server) handleNotebookWS`). It's ~80 lines.

- [ ] **Step 4: Verify no stale references**

```bash
grep -rn "handleNotebookWS\|notebookAuth\|/notebook/ws" internal/codexappgateway/
```

The only remaining match should be the chart template / docs (chart change comes in next phase).

- [ ] **Step 5: Build + test**

```bash
go build ./... && go test ./internal/codexappgateway/... -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/codexappgateway/server.go
git commit -m "refactor(codex-app-gateway): delete /notebook/ws — SDK no longer uses it"
```

---

## Phase F — Helm chart + Pulumi

### Task F1: Helm chart 0.60.13 → 0.61.0 (env vars + version bump)

**Files:**
- Modify: `deploy/helm/agentserver/templates/deployment.yaml`
- Modify: `deploy/helm/agentserver/Chart.yaml`

- [ ] **Step 1: Update `deployment.yaml` jupyter env injection**

Find the block (in the agentserver deployment container's `env:` list) that conditionally adds `CODEX_APP_GATEWAY_URL` and `CODEX_INBOUND_HMAC_SECRET` for jupyter sandbox setup:

```bash
grep -n "CODEX_APP_GATEWAY_URL\|CODEX_INBOUND_HMAC_SECRET\|CODEX_EXEC_GATEWAY_URL" deploy/helm/agentserver/templates/deployment.yaml
```

Replace with a single `CODEX_EXEC_GATEWAY_URL` env that points at the in-cluster codex-exec-gateway Service:

```yaml
{{- if .Values.codexExecGateway.enabled }}
- name: CODEX_EXEC_GATEWAY_URL
  value: "http://{{ .Release.Name }}-codex-exec-gateway.{{ .Release.Namespace }}.svc:{{ .Values.codexExecGateway.port }}"
{{- end }}
```

Delete the two old env block(s).

- [ ] **Step 2: Bump `Chart.yaml`**

```bash
sed -i 's/^version: 0\.60\.13$/version: 0.61.0/; s/^appVersion: "0\.60\.13"$/appVersion: "0.61.0"/' deploy/helm/agentserver/Chart.yaml
grep -E '^version|^appVersion' deploy/helm/agentserver/Chart.yaml
```

(If Chart.yaml's current version is something other than 0.60.13, adapt the sed expression.)

- [ ] **Step 3: Render check**

```bash
cd deploy/helm/agentserver
helm template . --set codexExecGateway.enabled=true --set codexExecGateway.port=6060 \
  | grep -A1 "CODEX_EXEC_GATEWAY_URL"
```

Expected: shows the rendered env var pointing at the in-cluster service URL.

```bash
helm template . | grep -c "CODEX_APP_GATEWAY_URL\|CODEX_INBOUND_HMAC_SECRET"
```

Expected: `0` (both env vars gone).

- [ ] **Step 4: Commit**

```bash
git add deploy/helm/agentserver/templates/deployment.yaml deploy/helm/agentserver/Chart.yaml
git commit -m "chore(jupyter): chart 0.61.0 — point jupyter at codex-exec-gateway

BREAKING CHANGE: existing jupyter sandboxes (with the old CODEX_APP_GATEWAY_URL
env) need to be deleted and recreated after upgrade. The /notebook/ws endpoint
on codex-app-gateway is also removed.
"
```

---

### Task F2: Update Pulumi `/root/k8s/stacks/agentserver.ts` to chart 0.61.0

**Files:**
- Modify: `/root/k8s/stacks/agentserver.ts`

- [ ] **Step 1: Diff-check first (memory rule: never blind-add)**

```bash
cd /root/k8s && git status && git diff stacks/agentserver.ts
```

- [ ] **Step 2: Bump chart version**

In `stacks/agentserver.ts`, find the line `version: "0.60.13",` (or whatever 0.60.x is currently pinned) and replace with `version: "0.61.0",`.

- [ ] **Step 3: Type-check**

```bash
cd /root/k8s && npx tsc --noEmit
```

Expected: clean.

- [ ] **Step 4: Commit**

```bash
cd /root/k8s && git add stacks/agentserver.ts
git commit -m "chore(agentserver): bump chart 0.60.x -> 0.61.0

SDK transport switched from WS through codex-app-gateway to HTTP REST
on codex-exec-gateway. All existing jupyter sandboxes must be
recreated after pulumi up.
"
```

---

## Post-deploy runbook (manual, no commit)

These commands run **once** after `pulumi up` brings chart 0.61.0 online.

- [ ] **Step 1: Confirm chart deployed**

```bash
helm -n agentserver list -o json | jq -r '.[] | "\(.name) \(.chart)"'
```

Expected output contains: `agentserver agentserver-0.61.0`.

- [ ] **Step 2: Force-roll the gateway pods (image tag :main didn't change)**

```bash
kubectl -n agentserver rollout restart deploy/agentserver \
  deploy/agentserver-codex-exec-gateway \
  deploy/agentserver-codex-app-gateway
```

- [ ] **Step 3: Delete every existing jupyter sandbox in the frontend**

Existing jupyter sandbox pods still have the old env vars (`CODEX_APP_GATEWAY_URL`). The new sandbox manager has to recreate them so they get `CODEX_EXEC_GATEWAY_URL` + new SDK in the image.

Tell users to delete and recreate their jupyter sandboxes. Or run:

```bash
kubectl get pods -A -o json \
  | jq -r '.items[] | select(.spec.containers[]?.image | contains("agentserver-jupyter")) | "\(.metadata.namespace)/\(.metadata.name)"'
```

For each, find the corresponding sandbox row in the DB (or have users delete via UI). Deletion via UI is preferred because it cleans up the sandbox row, PVC, and bridge connections.

- [ ] **Step 4: Smoke test**

In a jupyter notebook on a newly-created sandbox:

```python
envs = await ctx.envs()
print(envs)
```

Expected: a list of `Env` objects (or empty if no executors are connected to the workspace yet). No `ConnectionError`, no 401.

---

## Self-review pass

| Spec requirement | Implemented in |
|---|---|
| SDK transport switches from WS to HTTP REST | Task C1 |
| `/api/sdk/envs/list` endpoint on codex-exec-gateway | Task B3 |
| `/api/sdk/envs/{name}/tool/call` endpoint | Task B4 |
| `/api/sdk/processes/{sid}/{stdin,output,terminate}` endpoints | Task B5 |
| `ProxyTokenAuth` with LRU cache against agentserver | Task B1 |
| `internal/envtools/` package extraction (tools, bridge, nameresolver) | Task A1 |
| `internal/envtools/processes/` session manager with ring buffer + idle GC | Task B2 |
| Jupyter sandbox env: `AGENTSERVER_GATEWAY_URL` = exec-gateway base, `AGENTSERVER_WORKSPACE_TOKEN` = raw proxyToken | Task D1 |
| `mintCodexHMACToken` helper deleted | Task D1 (step 3) |
| `handleNotebookWS` on codex-app-gateway deleted | Task E1 |
| Chart `0.60.x → 0.61.0` with `CODEX_EXEC_GATEWAY_URL` env injection (and old envs removed) | Task F1 |
| Pulumi pinned to 0.61.0 | Task F2 |
| Python SDK tests rewritten against ASGI stub | Task C3 |

**Spec non-goals respected:**
- codex `--remote` TUI path untouched (only `/notebook/ws` removed; `/codex-app/ws` stays).
- env-mcp `mcpadapter` (mcp_server.go + envmcp.go) kept in `internal/codexappgateway/envmcp/` for codex's MCP child subprocess.
- No PyPI publish step.
- No process streaming WS — polling REST per design.

**Open items from spec (deferred to implementation discretion):**
- LRU cache size: hardcoded 1024 in Task B1's `NewProxyTokenAuth`.
- `inputSchema` not round-tripped in `envs/list` per spec; coreTools() in Task B3 only carries `{name, description, kind}`.
- mcpadapter sharing `tools.Registry` — Task A1 already keeps the tool ctors public so the adapter can build its own registry from the same constructors.
- The `handleStdin` / `handleTerminate` bridge wiring is left with a TODO comment in Task B5 — needs a follow-up integration when `unified_exec.go`'s public surface is confirmed during Task B6's manual smoke test. If the smoke test reveals stdin writes need actual bridge plumbing, expand `Session` to hold an `exeBridge bridge.Bridge` + `exeSessionID string` and have `handleStdin` call `bridge.WriteStdin(exeSessionID, data)`. The data shape comes from `tools.UnifiedExecTool`'s WriteStdin args.
