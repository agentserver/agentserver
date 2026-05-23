# Remove Old operations/oplog System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Delete the entire legacy "operation log" subsystem — `internal/codexappgateway/oplog/` package, `operations` table, `/internal/operations` + `/api/workspaces/{id}/operations` HTTP routes, the agentserver retention loop, the helm `operations:` block, the `CXG_OPLOG_*` env wiring in `codex-app-gateway`, and the frontend `OperationsPanel`. Drop the `operations` table from the database. This clears the way for the new `exec_audit_*` subsystem (see `docs/superpowers/specs/2026-05-23-codex-exec-gateway-audit-design.md`).

**Architecture:** Pure deletion across Go, SQL migration, Helm, and TypeScript. No new code is added in this plan — every task removes files, removes route registrations, removes config blocks, or removes UI components, and verifies the build/test/lint suites still pass. Final step is one DB migration that drops the `operations` table.

**Tech Stack:** Go 1.22+, chi router, PostgreSQL, Helm, React 19 + TypeScript + Vite, pnpm, swag → swagger2openapi → openapi-typescript.

---

## File Structure

| File | Action |
|---|---|
| `internal/codexappgateway/oplog/` | Delete directory (5 files + tests) |
| `internal/codexappgateway/server.go` | Edit — drop `oplog` import, `oplogClient` + `oplogList` fields, init in `NewServer`, two Close calls in shutdown paths |
| `internal/codexappgateway/config.go` | Edit — drop `OperationLogURL` / `OperationLogSecret` / `OperationLogChan` struct fields + env parsing |
| `cmd/codex-app-gateway/serve_args.go` | Edit — drop three flag declarations, three env reads, three Args struct fields |
| `cmd/codex-app-gateway/serve_args_test.go` | Edit — drop OPLOG-related test cases / setenv lines |
| `cmd/codex-app-gateway/main.go` | Edit — drop the three `args.OperationLog* → cfg.OperationLog*` assignments |
| `internal/db/operations.go` | Delete file |
| `internal/server/operations.go` | Delete file |
| `internal/server/operations_retention.go` | Delete file |
| `internal/server/operations_test.go` | Delete file |
| `internal/server/server.go` | Edit — drop `OperationsRetention` field + comment, drop POST/GET `/internal/operations` routes, drop `/api/workspaces/{id}/operations` route |
| `internal/server/api_types.go` | Edit — drop `WorkspaceOperationsResponse` + `OperationRecord` types |
| `cmd/serve.go` | Edit — drop `AGENTSERVER_OPERATIONS_RETENTION_DAYS` env parsing, drop `srv.OperationsRetention` assignment, drop `go srv.StartRetentionLoop(...)` |
| `deploy/helm/agentserver/values.yaml` | Edit — drop the `operations:` block (lines ~243-253) |
| `deploy/helm/agentserver/templates/codex-app-gateway.yaml` | Edit — drop the three `CXG_OPLOG_*` env entries inside the operations conditional |
| `web/src/components/OperationsPanel.tsx` | Delete file |
| `web/src/components/WorkspaceDetail.tsx` | Edit — drop `OperationsPanel` import, drop `'operations'` tab union member, drop nav mapping entry, drop tab item, drop render branch |
| `web/src/components/ManageWorkspaces.tsx` | Edit — drop `'operations'` literal from the tabs array on line 18 |
| `web/src/lib/api.ts` | Edit — drop `OperationRecord` and `WorkspaceOperationsResponse` type re-exports, drop the entire `// === Operations (Plan 3c) ===` section (lines ~967-996) |
| `web/src/lib/api-generated/schema.d.ts` | Regenerated from swagger by `make openapi` after backend changes |
| `notebook/stub_gateway_runner.py` | Edit — drop the `operations/list` stub handler and its doc-comment reference |
| `internal/db/migrations/031_drop_operations.sql` | Create — single `DROP TABLE IF EXISTS operations;` |

---

## Task 1: Delete the oplog Go package

**Files:**
- Delete: `internal/codexappgateway/oplog/` (entire directory: `client.go`, `interceptor.go`, `operations_list.go`, `doc.go`, plus `*_test.go` files)

- [ ] **Step 1: Confirm no consumers outside the package**

Run:
```bash
grep -rn '"github.com/agentserver/agentserver/internal/codexappgateway/oplog"' /root/agentserver/ \
    --include='*.go' | grep -v '/\.claude/' | grep -v '/oplog/'
```

Expected output (exactly one line):
```
/root/agentserver/internal/codexappgateway/server.go:19:	"github.com/agentserver/agentserver/internal/codexappgateway/oplog"
```

If anything else appears, STOP and surface it — the plan needs updating before this task can proceed.

- [ ] **Step 2: Delete the directory**

Run:
```bash
rm -rf /root/agentserver/internal/codexappgateway/oplog
```

- [ ] **Step 3: Verify Go modules still resolve**

Run:
```bash
cd /root/agentserver && go build ./internal/codexappgateway/oplog/... 2>&1 | head
```

Expected output:
```
package github.com/agentserver/agentserver/internal/codexappgateway/oplog: cannot find package
```

This is the desired result — the package no longer exists. Subsequent tasks fix the remaining import in `server.go`.

- [ ] **Step 4: Do NOT commit yet**

Wait until Task 2 fixes the dangling import in `server.go`, otherwise the tree is unbuildable mid-commit.

---

## Task 2: Strip oplog wiring from codex-app-gateway server + config

**Files:**
- Modify: `internal/codexappgateway/server.go` (drop import line 19, fields 73-75, init 138-140, two Close blocks 276-277 and 285-286)
- Modify: `internal/codexappgateway/config.go` (drop fields 70-74 + env parsing 126-135)

- [ ] **Step 1: Remove the import**

Edit `internal/codexappgateway/server.go`. Find:

```go
	"github.com/agentserver/agentserver/internal/codexappgateway/oplog"
```

Delete that single line.

- [ ] **Step 2: Remove the two struct fields**

Edit `internal/codexappgateway/server.go`. Find:

```go
	execClient connectedClient // exposed for the loopback /internal/connected handler

	// oplogClient is nil when OperationLogURL/Secret are empty.
	oplogClient *oplog.Client
	oplogList   *oplog.ListClient

	// brokerPool caches per-workspace broker.Conn instances (max idle 5 min).
```

Replace with:

```go
	execClient connectedClient // exposed for the loopback /internal/connected handler

	// brokerPool caches per-workspace broker.Conn instances (max idle 5 min).
```

- [ ] **Step 3: Remove the NewServer init block**

Edit `internal/codexappgateway/server.go`. Find:

```go
	if cfg.OperationLogURL != "" && cfg.OperationLogSecret != "" {
		s.oplogClient = oplog.NewClient(cfg.OperationLogURL, cfg.OperationLogSecret, cfg.OperationLogChan)
	}
	return s, nil
```

Replace with:

```go
	return s, nil
```

- [ ] **Step 4: Remove both shutdown-path Close calls**

Edit `internal/codexappgateway/server.go`. There are two identical guarded Close blocks. Find each occurrence of:

```go
		if s.oplogClient != nil {
			s.oplogClient.Close()
		}
```

And delete it (two locations: ~line 276 and ~line 285). Use Edit with `replace_all: true` only if the surrounding context is identical for both; otherwise edit each in turn with unique context.

- [ ] **Step 5: Remove the three config struct fields + their doc comment**

Edit `internal/codexappgateway/config.go`. Find:

```go
	// OperationLog endpoint + auth. When OperationLogURL is empty,
	// oplogClient is nil and Submit calls are no-ops.
	OperationLogURL    string
	OperationLogSecret string // X-Internal-Secret header value
	OperationLogChan   int    // bounded channel capacity, default 1024

	// Scheduler config — when AgentserverInternalURL is empty the scheduler is
```

Replace with:

```go
	// Scheduler config — when AgentserverInternalURL is empty the scheduler is
```

- [ ] **Step 6: Remove the env parsing block**

Edit `internal/codexappgateway/config.go`. Find:

```go
	cfg.AgentserverInternalURL = os.Getenv("CXG_AGENTSERVER_INTERNAL_URL")
	cfg.AgentserverInternalSecret = os.Getenv("CXG_AGENTSERVER_INTERNAL_SECRET")
	cfg.OperationLogURL = os.Getenv("CXG_OPLOG_URL")
	cfg.OperationLogSecret = os.Getenv("CXG_OPLOG_SECRET")
	cfg.OperationLogChan = 1024
	if v := os.Getenv("CXG_OPLOG_CHAN"); v != "" {
		n, err := strconv.Atoi(v)
```

There's a closing block after this (the `if v != ""` body assigns to `cfg.OperationLogChan` and returns an error on parse failure). Read lines 126-138 of `config.go` to see the full structure, then delete from `cfg.OperationLogURL = ...` through the closing `}` of the `if v := os.Getenv("CXG_OPLOG_CHAN"); v != "" {` block (inclusive). The result should be:

```go
	cfg.AgentserverInternalURL = os.Getenv("CXG_AGENTSERVER_INTERNAL_URL")
	cfg.AgentserverInternalSecret = os.Getenv("CXG_AGENTSERVER_INTERNAL_SECRET")
```

followed by whatever line was previously after the OPLOG_CHAN block.

- [ ] **Step 7: Drop now-unused `strconv` import if applicable**

Run:
```bash
cd /root/agentserver && go build ./internal/codexappgateway/... 2>&1 | head -20
```

If you see `imported and not used: "strconv"` in `config.go`, remove that import line. Otherwise leave imports as-is.

- [ ] **Step 8: Verify codex-app-gateway compiles**

Run:
```bash
cd /root/agentserver && go build ./internal/codexappgateway/... ./cmd/codex-app-gateway/... 2>&1
```

Expected output: empty (zero output = success). If you get `undefined: oplog` or similar, re-grep for leftover references in those two files and remove them.

- [ ] **Step 9: Commit**

```bash
cd /root/agentserver && git add internal/codexappgateway/oplog internal/codexappgateway/server.go internal/codexappgateway/config.go
git commit -m "$(cat <<'EOF'
chore(codex-app-gateway): remove oplog package and OperationLog config

The oplog package and the OperationLogURL/Secret/Chan config were the
client side of the legacy /internal/operations audit. The audit hook
was never wired into the running ws bridge (the interceptor in the
package is defined but has no production callsite), so removal is
behaviour-preserving. The new exec-gateway audit subsystem replaces
this entirely — see
docs/superpowers/specs/2026-05-23-codex-exec-gateway-audit-design.md.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Strip oplog flags from codex-app-gateway cmd args

**Files:**
- Modify: `cmd/codex-app-gateway/serve_args.go` (drop three Args fields, three flag declarations, three env reads)
- Modify: `cmd/codex-app-gateway/serve_args_test.go` (drop OPLOG-related test cases / setenv)
- Modify: `cmd/codex-app-gateway/main.go` (drop the three `args.OperationLog* → cfg.OperationLog*` lines around 93-101)

- [ ] **Step 1: Inspect serve_args.go**

Run:
```bash
grep -n "OperationLog\|oplog" /root/agentserver/cmd/codex-app-gateway/serve_args.go
```

You should see the three struct fields (lines ~13-15), three `fs.String/Int` flag definitions (lines ~23-25), and three blocks that read env vars when the flag was not set (lines ~35-44).

- [ ] **Step 2: Delete the three struct fields**

Edit `cmd/codex-app-gateway/serve_args.go`. Find:

```go
	OperationLogURL    string
	OperationLogSecret string
	OperationLogChan   int
```

Delete those three lines (preserve surrounding struct members).

- [ ] **Step 3: Delete the three flag declarations**

Edit `cmd/codex-app-gateway/serve_args.go`. Find:

```go
	opLogURL := fs.String("oplog-url", "", "agentserver /internal/operations URL (env CXG_OPLOG_URL)")
	opLogSecret := fs.String("oplog-secret", "", "X-Internal-Secret header value (env CXG_OPLOG_SECRET)")
	opLogChan := fs.Int("oplog-chan", 1024, "bounded channel capacity (env CXG_OPLOG_CHAN)")
```

Delete those three lines.

- [ ] **Step 4: Delete the env-fallback blocks**

Edit `cmd/codex-app-gateway/serve_args.go`. Find each of:

```go
	if envURL := os.Getenv("CXG_OPLOG_URL"); envURL != "" && *opLogURL == "" {
```

(...and its block body), and the analogous blocks for `CXG_OPLOG_SECRET` (with `*opLogSecret == ""`) and `CXG_OPLOG_CHAN` (with `*opLogChan == 1024`). Delete each `if ... { ... }` block in its entirety. Also delete any `args.OperationLog* = *opLog*` lines that referenced the now-deleted flag pointers.

- [ ] **Step 5: Inspect and edit main.go**

Run:
```bash
sed -n '90,103p' /root/agentserver/cmd/codex-app-gateway/main.go
```

Find the block:

```go
	if args.OperationLogURL != "" {
		cfg.OperationLogURL = args.OperationLogURL
	}
	if args.OperationLogSecret != "" {
		cfg.OperationLogSecret = args.OperationLogSecret
	}
	if args.OperationLogChan > 0 {
		cfg.OperationLogChan = args.OperationLogChan
	}
```

Delete the entire 9-line block.

- [ ] **Step 6: Update serve_args_test.go**

Run:
```bash
grep -n "OPLOG\|OperationLog\|oplog" /root/agentserver/cmd/codex-app-gateway/serve_args_test.go
```

For each test function that has `t.Setenv("CXG_OPLOG_*", ...)` lines, delete those `t.Setenv` lines. For test cases that asserted on `args.OperationLog*` values, delete those assertion lines. If an entire test case existed solely to validate OPLOG flag/env wiring, delete the whole test function.

- [ ] **Step 7: Verify build and tests**

Run:
```bash
cd /root/agentserver && go build ./cmd/codex-app-gateway/... && go test ./cmd/codex-app-gateway/...
```

Expected output: `ok  	github.com/agentserver/agentserver/cmd/codex-app-gateway	<duration>s`

- [ ] **Step 8: Commit**

```bash
cd /root/agentserver && git add cmd/codex-app-gateway/serve_args.go cmd/codex-app-gateway/serve_args_test.go cmd/codex-app-gateway/main.go
git commit -m "$(cat <<'EOF'
chore(codex-app-gateway): drop --oplog-* CLI flags and env wiring

Follows up the previous commit by removing the cmd-layer plumbing for
the three OPLOG flags. No deployed manifest still passes these values
(see deploy/helm/.../codex-app-gateway.yaml in the next commit).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Strip oplog env injection from Helm chart

**Files:**
- Modify: `deploy/helm/agentserver/templates/codex-app-gateway.yaml` (drop the `CXG_OPLOG_*` env entries inside `{{- if .Values.operations.enabled }}`)
- Modify: `deploy/helm/agentserver/values.yaml` (drop the entire `operations:` block at lines ~243-253)

- [ ] **Step 1: Inspect the chart conditional**

Run:
```bash
sed -n '125,140p' /root/agentserver/deploy/helm/agentserver/templates/codex-app-gateway.yaml
```

Find the block:

```yaml
            {{- if .Values.operations.enabled }}
            - name: CXG_OPLOG_URL
              value: "http://{{ .Release.Name }}.{{ .Release.Namespace }}.svc:{{ .Values.service.port }}/internal/operations"
            - name: CXG_OPLOG_SECRET
              valueFrom: ...
            - name: CXG_OPLOG_CHAN
              value: {{ .Values.operations.channelCapacity | quote }}
            {{- end }}
```

- [ ] **Step 2: Delete the conditional block**

Edit `deploy/helm/agentserver/templates/codex-app-gateway.yaml`. Delete the entire `{{- if .Values.operations.enabled }} ... {{- end }}` block (lines ~129-137 — confirm exact range by reading the file). Preserve indentation of surrounding env entries.

- [ ] **Step 3: Delete the operations block in values.yaml**

Edit `deploy/helm/agentserver/values.yaml`. Find:

```yaml
# Operation log (Plan 2): codex-app-gateway records every MCP tool call to
# agentserver's /internal/operations endpoint for audit + replay.
operations:
  # When true, codex-app-gateway POSTs every mcpServer/tool/call to
  # agentserver's /internal/operations. Disable to revert to pre-Plan-2
  # behavior (no logging).
  enabled: true
  # Retention in days. 0 disables the cleanup loop.
  retentionDays: 90
  # Bounded channel capacity inside the gateway. Drops on overflow.
  channelCapacity: 1024
```

Delete that entire commented + valued block.

- [ ] **Step 4: Verify the chart renders**

Run:
```bash
cd /root/agentserver/deploy/helm/agentserver && helm template . --debug 2>&1 | grep -iE "CXG_OPLOG|operations.enabled|nil pointer" | head
```

Expected output: empty (no leftover references, no template errors).

If helm is not on PATH, this is OK — flag in the commit message that template rendering must be verified in CI. Alternative: run `helm lint .` if available.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver && git add deploy/helm/agentserver/templates/codex-app-gateway.yaml deploy/helm/agentserver/values.yaml
git commit -m "$(cat <<'EOF'
chore(helm): drop operations: block and CXG_OPLOG_* env injection

The new exec-gateway audit subsystem (separate spec) replaces this.
operations.enabled was on in the default values.yaml but the underlying
oplog interceptor was never wired in code — so the env vars were
configured but had no effect.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Delete agentserver operations HTTP routes and handlers

**Files:**
- Delete: `internal/server/operations.go`
- Delete: `internal/server/operations_test.go`
- Modify: `internal/server/server.go` (drop two `r.Post|Get("/internal/operations", ...)` registrations at lines ~325-345; drop `r.Get("/api/workspaces/{id}/operations", s.getWorkspaceOperations)` at line ~496; drop preceding comment lines)
- Modify: `internal/server/api_types.go` (drop `WorkspaceOperationsResponse` and `OperationRecord` types at lines ~541-567)

- [ ] **Step 1: Delete the two handler files**

Run:
```bash
rm /root/agentserver/internal/server/operations.go /root/agentserver/internal/server/operations_test.go
```

- [ ] **Step 2: Remove internal route registrations**

Edit `internal/server/server.go`. Find:

```go
	// Internal operation-log endpoints — POST from gateways (fire-and-forget),
	// GET for SDK retrieval. Auth: X-Internal-Secret matching INTERNAL_API_SECRET.
	r.Post("/internal/operations", func(w http.ResponseWriter, r *http.Request) {
		secret := os.Getenv("INTERNAL_API_SECRET")
		if secret != "" {
			if r.Header.Get("X-Internal-Secret") != secret {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		s.postInternalOperations(w, r)
	})
	r.Get("/internal/operations", func(w http.ResponseWriter, r *http.Request) {
		secret := os.Getenv("INTERNAL_API_SECRET")
		if secret != "" {
			if r.Header.Get("X-Internal-Secret") != secret {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		s.getInternalOperations(w, r)
	})

```

Delete the entire block (comment + both `r.Post` and `r.Get` blocks, plus the trailing blank line).

- [ ] **Step 3: Remove the workspace-scoped route**

Edit `internal/server/server.go`. Find:

```go
		// Workspace operations log (read-only, member-gated, wraps /internal/operations)
		r.Get("/api/workspaces/{id}/operations", s.getWorkspaceOperations)

```

Delete those three lines (the comment, the route registration, the blank line).

- [ ] **Step 4: Remove the OperationsRetention field**

Edit `internal/server/server.go`. Find:

```go
	// OperationsRetention is the TTL for rows in the operations table.
	// 0 disables the background retention loop. Configurable via
	// AGENTSERVER_OPERATIONS_RETENTION_DAYS (default 90).
	OperationsRetention time.Duration

```

Delete those five lines (comment + field + blank line).

- [ ] **Step 5: Remove the api_types definitions**

Edit `internal/server/api_types.go`. Find the consecutive block starting at:

```go
// WorkspaceOperationsResponse is returned by GET /api/workspaces/{id}/operations.
type WorkspaceOperationsResponse struct {
	Operations []OperationRecord `json:"operations" validate:"required"`
} // @name WorkspaceOperationsResponse
```

and ending at the closing brace of `type OperationRecord struct { ... } // @name OperationRecord`. Read lines 541-567 first to see exact extent, then delete all those lines (typically ~27 lines).

- [ ] **Step 6: Verify agentserver compiles**

Run:
```bash
cd /root/agentserver && go build ./internal/server/... ./cmd/...
```

Expected: empty output (success). If you get `undefined: getWorkspaceOperations` / `undefined: postInternalOperations` / `undefined: OperationRecord` / `undefined: WorkspaceOperationsResponse`, search for stragglers:

```bash
grep -rn "getWorkspaceOperations\|postInternalOperations\|getInternalOperations\|WorkspaceOperationsResponse\|OperationRecord\b" /root/agentserver/internal /root/agentserver/cmd --include='*.go' | grep -v '\.claude'
```

Any matches that survived are in code that wasn't in this task's plan — STOP and surface them.

- [ ] **Step 7: Run server tests**

Run:
```bash
cd /root/agentserver && go test ./internal/server/... -count=1
```

Expected: all tests pass. If there are remaining references to the deleted types in other test files (besides `operations_test.go` which we already deleted), they need to be removed too.

- [ ] **Step 8: Commit**

```bash
cd /root/agentserver && git add internal/server/operations.go internal/server/operations_test.go internal/server/server.go internal/server/api_types.go
git commit -m "$(cat <<'EOF'
feat(server): remove /internal/operations and /api/workspaces/{id}/operations

Drops the four route registrations, the two handler files, and the
WorkspaceOperationsResponse/OperationRecord DTOs. The OperationsRetention
field on Server is removed; retention loop wiring in cmd/serve.go is
removed in the next commit.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Delete agentserver retention loop wiring

**Files:**
- Delete: `internal/server/operations_retention.go`
- Delete: `internal/db/operations.go`
- Modify: `cmd/serve.go` (drop `AGENTSERVER_OPERATIONS_RETENTION_DAYS` parsing at lines ~283-293, drop `go srv.StartRetentionLoop(...)` at line ~333)

- [ ] **Step 1: Delete the retention loop file**

Run:
```bash
rm /root/agentserver/internal/server/operations_retention.go /root/agentserver/internal/db/operations.go
```

- [ ] **Step 2: Remove the env parsing block in serve.go**

Edit `cmd/serve.go`. Find:

```go
		// Operations retention TTL — 90 days default, 0 disables. Env var
		// AGENTSERVER_OPERATIONS_RETENTION_DAYS overrides.
		retentionDays := 90
		if v := os.Getenv("AGENTSERVER_OPERATIONS_RETENTION_DAYS"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				retentionDays = n
			} else {
				log.Printf("Warning: AGENTSERVER_OPERATIONS_RETENTION_DAYS=%q invalid, using default %d", v, retentionDays)
			}
		}
		srv.OperationsRetention = time.Duration(retentionDays) * 24 * time.Hour

```

Delete the entire 12-line block.

- [ ] **Step 3: Remove the goroutine launch**

Edit `cmd/serve.go`. Find:

```go
		// Operations retention background loop. Disabled when TTL is 0.
		go srv.StartRetentionLoop(healthCtx, srv.OperationsRetention, time.Hour)

```

Delete those three lines.

- [ ] **Step 4: Clean up unused imports in serve.go**

Run:
```bash
cd /root/agentserver && go build ./cmd/... 2>&1
```

If you see `imported and not used: "strconv"` because `strconv.Atoi` was only used in the deleted block, remove the `strconv` import from `cmd/serve.go`. Same check for `"time"` — `time.Duration` and `time.Hour` may still be used elsewhere in the file; only drop the import if the build complains.

- [ ] **Step 5: Verify full build + tests**

Run:
```bash
cd /root/agentserver && go build ./... && go test ./internal/server/... ./internal/db/... -count=1
```

Expected: empty build output, all tests pass.

- [ ] **Step 6: Commit**

```bash
cd /root/agentserver && git add internal/server/operations_retention.go internal/db/operations.go cmd/serve.go
git commit -m "$(cat <<'EOF'
feat(server): remove operations retention loop and DB CRUD

Drops StartRetentionLoop + the operations DAL. The
AGENTSERVER_OPERATIONS_RETENTION_DAYS env var is no longer read.
Migration that drops the operations table itself is added in a
separate commit.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Delete frontend OperationsPanel and api.ts plumbing

**Files:**
- Delete: `web/src/components/OperationsPanel.tsx`
- Modify: `web/src/components/WorkspaceDetail.tsx` (line 68 import, line 79 tab union member, line 110 nav mapping, line 200 tab item, lines 284-285 render branch)
- Modify: `web/src/components/ManageWorkspaces.tsx` (line 18 array literal)
- Modify: `web/src/lib/api.ts` (lines 48-49 type re-exports, lines 967-996 functions and types)

- [ ] **Step 1: Delete the panel file**

Run:
```bash
rm /root/agentserver/web/src/components/OperationsPanel.tsx
```

- [ ] **Step 2: Remove WorkspaceDetail.tsx references**

Edit `web/src/components/WorkspaceDetail.tsx`. Five places:

a) Find and delete the import line:
```typescript
import OperationsPanel from './OperationsPanel'
```

b) Find the `tab` union type containing `| 'operations'` and remove the `| 'operations'` segment. Read line 79 area first to see the exact union.

c) Find the nav mapping line:
```typescript
  operations: 'operations',
```
and delete it.

d) Find the tab definition:
```typescript
    { key: 'operations', label: 'Operations', icon: <Activity size={16} /> },
```
and delete the entire array entry (including trailing comma if it's not the last element). If `Activity` is no longer used in any other tab definition in this file, also remove `Activity` from the `lucide-react` import line.

e) Find the render branch:
```typescript
          {tab === 'operations' && (
            <OperationsPanel workspaceId={workspace.id} />
          )}
```
and delete those three lines.

- [ ] **Step 3: Remove ManageWorkspaces.tsx reference**

Edit `web/src/components/ManageWorkspaces.tsx`. Find:

```typescript
    'llm', 'im', 'traces', 'operations', 'credentials', 'members', 'api-keys', 'settings',
```

Replace with:

```typescript
    'llm', 'im', 'traces', 'credentials', 'members', 'api-keys', 'settings',
```

(remove just the `'operations', ` entry, preserve comma + space).

- [ ] **Step 4: Remove api.ts type re-exports and functions**

Edit `web/src/lib/api.ts`. Two locations.

First, find and delete these two consecutive lines (near line 48):
```typescript
export type OperationRecord = components['schemas']['OperationRecord']
export type WorkspaceOperationsResponse = components['schemas']['WorkspaceOperationsResponse']
```

Second, find the section header:
```typescript
// === Operations (Plan 3c) ===
```
Delete from that comment through the end of the `listOperations` function (search for the closing `}` and look ahead for the next `// ===` section header or the next top-level export to find the right cutoff). Read lines 965-1005 of `api.ts` first to see the exact extent, then delete everything inside.

- [ ] **Step 5: Verify the frontend builds**

Run:
```bash
cd /root/agentserver/web && pnpm install --frozen-lockfile && pnpm build 2>&1 | tail -30
```

Expected: successful build, no TypeScript errors. If you see `Cannot find name 'OperationRecord'` or `Property 'operations' does not exist`, grep:

```bash
grep -rn "OperationRecord\|WorkspaceOperationsResponse\|OperationsPanel\|'operations'" /root/agentserver/web/src 2>&1 | grep -v "api-generated\|node_modules"
```

Any remaining match needs editing.

- [ ] **Step 6: Run eslint**

Run:
```bash
cd /root/agentserver/web && pnpm lint 2>&1 | tail
```

Expected: clean (or only warnings unrelated to our changes).

- [ ] **Step 7: Commit**

```bash
cd /root/agentserver && git add web/src/components/OperationsPanel.tsx web/src/components/WorkspaceDetail.tsx web/src/components/ManageWorkspaces.tsx web/src/lib/api.ts
git commit -m "$(cat <<'EOF'
feat(web): remove OperationsPanel and operations API client

The Operations tab in WorkspaceDetail is removed. The new exec-audit
panel (separate PR per plan) will reuse that tab slot. The generated
schema.d.ts is regenerated by make openapi in a follow-up commit once
all backend changes have landed.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Clean up notebook stub gateway runner

**Files:**
- Modify: `notebook/stub_gateway_runner.py` (drop `operations/list` handler at line 61-71 plus the doc-comment reference at line 9)

- [ ] **Step 1: Read the file to see exact structure**

Run:
```bash
sed -n '1,80p' /root/agentserver/notebook/stub_gateway_runner.py
```

- [ ] **Step 2: Delete the operations_list handler**

Edit `notebook/stub_gateway_runner.py`. Find the function:

```python
    def operations_list(p):
        ...
        return {"operations": [
            ...
        ]}
```

(spans lines ~61-69). Delete the entire function definition.

Also delete the handler registration on line ~71:
```python
    g.on("operations/list", operations_list)
```

- [ ] **Step 3: Update the module docstring**

Find and delete the line near line 9 referencing `operations/list`:

```
  - operations/list -> 2 synthetic records (smoke walkthrough Cell 4)
```

- [ ] **Step 4: Verify the runner is still importable**

Run:
```bash
cd /root/agentserver/notebook && python3 -c "import stub_gateway_runner" && echo OK
```

Expected output: `OK`. If you get `SyntaxError` or `NameError`, re-read the file and fix dangling references.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver && git add notebook/stub_gateway_runner.py
git commit -m "$(cat <<'EOF'
chore(notebook): drop operations/list from stub_gateway_runner

The synthetic operations/list reply is no longer exercised — the
real operations endpoint is gone. Notebook walkthrough's Cell 4 will
be updated in a separate notebook refresh.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: Add the DROP TABLE migration

**Files:**
- Create: `internal/db/migrations/031_drop_operations.sql`

- [ ] **Step 1: Verify the next migration number**

Run:
```bash
ls /root/agentserver/internal/db/migrations/ | sort | tail -5
```

Expected to show numbers up through 030. Use `031` for our new migration. If the listing shows a 031 already exists (a parallel branch landed first), use the next available number and update this plan.

- [ ] **Step 2: Create the migration**

Create `internal/db/migrations/031_drop_operations.sql` with content:

```sql
-- 031_drop_operations.sql
-- Drop the legacy operations table. The codex-app-gateway oplog interceptor
-- was never wired in production, so this table has remained empty since
-- introduction. Audit responsibility moves to the new exec_audit_* tables
-- (see docs/superpowers/specs/2026-05-23-codex-exec-gateway-audit-design.md).

DROP TABLE IF EXISTS operations;
```

- [ ] **Step 3: Verify migration applies cleanly against an empty DB**

Run the agentserver unit tests that exercise migrations:

```bash
cd /root/agentserver && go test ./internal/db/... -count=1 -run TestMigrations
```

If a `TestMigrations` test does not exist, instead run the full DB test suite:

```bash
cd /root/agentserver && go test ./internal/db/... -count=1
```

Expected: all tests pass. The migration applies cleanly.

- [ ] **Step 4: Commit**

```bash
cd /root/agentserver && git add internal/db/migrations/031_drop_operations.sql
git commit -m "$(cat <<'EOF'
feat(db): drop operations table (migration 031)

The operations table is empty in production — the oplog interceptor that
was supposed to populate it was never wired into the running ws bridge.
All upstream code that read or wrote this table has been removed in the
preceding commits. The new exec-gateway audit subsystem stores its data
in the new exec_audit_* tables (see spec).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: Regenerate OpenAPI schema and run full verification

**Files:**
- Modify: `docs/api/openapi.yaml`, `docs/api/openapi.json`, `web/src/lib/api-generated/schema.d.ts` (all auto-regenerated by `make openapi`)

- [ ] **Step 1: Regenerate the OpenAPI spec**

Run:
```bash
cd /root/agentserver && make openapi 2>&1 | tail -20
```

Expected: completes without error. The targets regenerate `docs/api/openapi.yaml`, `docs/api/openapi.json`, and `web/src/lib/api-generated/schema.d.ts` from swag annotations in the Go source.

- [ ] **Step 2: Verify the regenerated files no longer mention operations**

Run:
```bash
grep -iE "operationrecord|workspaceoperationsresponse|/internal/operations|/api/workspaces/\{id\}/operations" \
    /root/agentserver/docs/api/openapi.yaml \
    /root/agentserver/docs/api/openapi.json \
    /root/agentserver/web/src/lib/api-generated/schema.d.ts
```

Expected output: empty. If any match remains, a swag annotation still exists somewhere — `grep -rn '@Router /api/workspaces/{id}/operations' /root/agentserver --include='*.go'` to find it.

- [ ] **Step 3: Full build + test sweep**

Run:
```bash
cd /root/agentserver && make test
```

Expected: `go vet` clean, all tests pass.

Then:
```bash
cd /root/agentserver && make build
```

Expected: frontend + backend both build cleanly.

- [ ] **Step 4: Final grep — ensure no stragglers**

Run:
```bash
cd /root/agentserver && grep -rn "OperationLog\|oplogClient\|OperationsPanel\|OperationRecord\|WorkspaceOperationsResponse\|operations_retention\|operations\.go\|/internal/operations\|/api/workspaces/{id}/operations\|CXG_OPLOG\|AGENTSERVER_OPERATIONS_RETENTION" \
    --include='*.go' --include='*.ts' --include='*.tsx' --include='*.yaml' --include='*.yml' --include='*.py' --include='*.sql' . 2>/dev/null | \
    grep -v '/\.claude/' | grep -v '/node_modules/' | grep -v '/docs/superpowers/' | grep -v 'docs/api/openapi'
```

Expected output: empty. Lines from `docs/superpowers/` (historical specs/plans) and `docs/api/openapi.{yaml,json}` (regenerated) are excluded by the grep. If anything else surfaces, address it now before commit.

- [ ] **Step 5: Commit the regenerated artifacts**

```bash
cd /root/agentserver && git add docs/api/openapi.yaml docs/api/openapi.json web/src/lib/api-generated/schema.d.ts
git commit -m "$(cat <<'EOF'
chore(api): regenerate OpenAPI spec after operations removal

Auto-regenerated via make openapi. Drops OperationRecord,
WorkspaceOperationsResponse, and the two operations routes from the
spec and the generated TypeScript schema.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: Open the pull request

- [ ] **Step 1: Check branch state**

Run:
```bash
cd /root/agentserver && git status && git log --oneline main..HEAD
```

Expected: clean working tree, 8-9 commits ahead of main (one per task that produced commits; tasks 1+2 share a commit per the plan, tasks 5/6/7/8/9/10 each have one).

- [ ] **Step 2: Push the branch**

If the current branch already tracks a remote, just push. Otherwise create the upstream branch:

```bash
cd /root/agentserver && git push -u origin HEAD
```

- [ ] **Step 3: Open the PR**

Run:
```bash
cd /root/agentserver && gh pr create --title "chore: remove legacy operations/oplog subsystem" --body "$(cat <<'EOF'
## Summary

- Deletes the entire \`internal/codexappgateway/oplog/\` package, the \`operations\` table, the two HTTP routes (\`/internal/operations\` and \`/api/workspaces/{id}/operations\`), the retention loop, the helm \`operations:\` block + \`CXG_OPLOG_*\` envs, and the frontend \`OperationsPanel\` + api client functions.
- Adds migration \`031_drop_operations.sql\` to drop the table.
- The oplog interceptor was never wired into the running ws bridge (no production callsite), so this is behaviour-preserving — production traffic has produced zero \`operations\` rows in 11+ hours of gateway uptime in nj-prod (verified during the design session on 2026-05-23).
- Spec: \`docs/superpowers/specs/2026-05-23-codex-exec-gateway-audit-design.md\`
- Plan: \`docs/superpowers/plans/2026-05-23-remove-operations-oplog.md\`
- Follow-up: the new exec-gateway audit subsystem lands in a separate PR.

## Test plan

- [ ] \`make test\` green
- [ ] \`make build\` green (backend + frontend)
- [ ] \`make openapi\` produces a diff with only operations-related removals
- [ ] \`helm template deploy/helm/agentserver --debug\` renders without errors
- [ ] Manual check post-deploy: \`/api/workspaces/{id}/operations\` returns 404 (route removed)
- [ ] Manual check post-deploy: \`\\d operations\` in psql returns "Did not find any relation"

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 4: Report the PR URL back to the user.**
