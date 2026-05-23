# Exec-Gateway Audit — agentserver-side Implementation Plan (Plan 2a)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the agentserver-side foundation of the new codex-exec-gateway audit subsystem — three new tables (`exec_audit_sessions`, `exec_audit_calls`, `exec_audit_payloads`), an idempotent ingest HTTP endpoint, a workspace-scoped read API, and a background retention loop. This plan ships in isolation; until Plan 2b lands (gateway-side WAL + uploader), the tables stay empty and the read API returns empty lists. That's fine for independent delivery and lets the API surface be reviewed/integrated against before the producer side is built.

**Architecture:** All new code lives under `internal/db/` (schema + DAL), `internal/server/` (handlers + retention), `cmd/serve.go` (env wiring), and one DB migration. The ingest endpoint accepts a protobuf-encoded batch via POST and upserts rows idempotently by UUID. Payloads above 4 KiB are zstd-compressed; payloads above 4 MiB are stored as sha256+size metadata only (request body simply omits the bytes — see Plan 2b for how the gateway enforces this on the producer side). Sessions and calls share workspace_id for tenant scoping; the user-facing read API is gated by existing `requireWorkspaceMember` middleware.

**Tech Stack:** Go 1.22+, PostgreSQL (via existing `internal/db` package), chi router, `google.golang.org/protobuf` v1.36 (already in go.mod), `github.com/klauspost/compress/zstd` (new dep).

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `internal/db/migrations/032_exec_audit.sql` | Create | DDL for `exec_audit_sessions`, `exec_audit_calls`, `exec_audit_payloads` + indexes |
| `internal/db/exec_audit.go` | Create | Typed structs (`AuditSession`, `AuditCall`, `AuditPayload`) + CRUD: `UpsertAuditSession`, `UpdateAuditSessionClose`, `UpsertAuditCall`, `UpdateAuditCallEnd`, `UpsertAuditPayload`, `ListAuditSessions`, `GetAuditSession`, `ListAuditCalls`, `GetAuditCall`, `GetAuditPayload`, `PruneAuditOlderThan`. One file = one DAL, ~400 LOC. |
| `internal/db/exec_audit_test.go` | Create | Unit tests against in-memory sqlite or test postgres (follow existing pattern in db package) |
| `internal/server/exec_audit_pb/audit.proto` | Create | Protobuf schema for `BatchRecords` / `WALRecord` (shared with gateway-side Plan 2b) |
| `internal/server/exec_audit_pb/audit.pb.go` | Create (generated) | `make proto` (or hand-invoke `protoc`) regenerates from .proto |
| `internal/server/exec_audit.go` | Create | HTTP handlers: `postInternalExecAuditBatch` (POST /internal/exec-audit/batch), `getInternalExecAuditSessions/Calls/Call/Payload` and the workspace-scoped wrappers `getWorkspaceExecAuditSessions/Calls/Call/Payload`. Plus the small `decompressPayload` helper. ~500 LOC. |
| `internal/server/exec_audit_test.go` | Create | Handler unit tests with table-driven cases for ingest + query. Mock DAL via interface. |
| `internal/server/exec_audit_retention.go` | Create | Background loop: `PruneAuditOlderThan` + orphan-payload sweep, hourly tick, configurable via env. ~80 LOC. |
| `internal/server/exec_audit_retention_test.go` | Create | Test the prune query covers sessions/calls/payloads correctly. |
| `internal/server/server.go` | Modify | Register the new routes in the existing `Router()` method (internal endpoints under raw chi.Router, workspace-scoped under the existing protected group with `requireWorkspaceMember`). ~15 LOC added. |
| `internal/server/api_types.go` | Modify | Add response DTOs: `AuditSessionSummary`, `AuditSessionDetail`, `AuditCallSummary`, `AuditCallDetail`, `ListAuditSessionsResponse`, `ListAuditCallsResponse`. Plus `// @name` annotations for swag. ~80 LOC added. |
| `cmd/serve.go` | Modify | Read `AGENTSERVER_EXEC_AUDIT_RETENTION_DAYS` env (default 90, 0 disables) → start retention goroutine. ~10 LOC added. |
| `go.mod` / `go.sum` | Modify | Add `github.com/klauspost/compress` (zstd) via `go get`. |
| `Makefile` | Modify (optional) | Add `proto` target if it doesn't exist; otherwise hand-run protoc and check in the generated file. |

---

## Task 1: Add zstd dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the dependency**

```bash
cd /root/agentserver/.claude/worktrees/<your-worktree>
go get github.com/klauspost/compress@latest
```

- [ ] **Step 2: Verify it resolves and tidies cleanly**

```bash
go mod tidy
go build ./...
```

Expected: empty output (clean build). The dep appears in `go.mod` direct requires.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "$(cat <<'EOF'
chore: add klauspost/compress for zstd in exec-audit payloads

Used by the new exec_audit subsystem to compress >4 KiB payloads
before storing in BYTEA. See Plan 2a.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Add the SQL migration

**Files:**
- Create: `internal/db/migrations/032_exec_audit.sql`

- [ ] **Step 1: Verify the next migration number**

```bash
ls internal/db/migrations/ | sort | tail -5
```

Expected to show `031_drop_operations.sql` as the highest. Use `032`. If it shows a `032_*` from a parallel branch, use the next free number and update the plan accordingly.

- [ ] **Step 2: Write the migration**

Create `internal/db/migrations/032_exec_audit.sql` with this exact content:

```sql
-- 032_exec_audit.sql
-- Schema for the codex-exec-gateway audit subsystem. Each ws bridge session
-- between env-mcp (or the SDK REST bridge.Pool) and codex-exec produces one
-- exec_audit_sessions row; every logical call (JSON-RPC request/response
-- pair, or SDK tool invocation, or relay PUT/GET) produces one
-- exec_audit_calls row; payload bytes >4 KiB live in exec_audit_payloads
-- (zstd-compressed, sha256-deduped) and are referenced by id.
--
-- See docs/superpowers/specs/2026-05-23-codex-exec-gateway-audit-design.md.

CREATE TABLE exec_audit_payloads (
  id              UUID PRIMARY KEY,
  sha256          TEXT NOT NULL UNIQUE,
  compressed      BYTEA NOT NULL,
  original_size   INT NOT NULL,
  compressed_size INT NOT NULL,
  ref_count       INT NOT NULL DEFAULT 0,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX exec_audit_payloads_created ON exec_audit_payloads(created_at);

CREATE TABLE exec_audit_sessions (
  id                UUID PRIMARY KEY,
  workspace_id      TEXT NOT NULL,
  user_id           TEXT,
  exe_id            TEXT NOT NULL,
  turn_id           TEXT,
  stream_id         TEXT NOT NULL,
  client_ip         INET,
  cap_iat           TIMESTAMPTZ,
  cap_exp           TIMESTAMPTZ,
  opened_at         TIMESTAMPTZ NOT NULL,
  closed_at         TIMESTAMPTZ,
  close_reason      TEXT,
  frames_to_backend INT NOT NULL DEFAULT 0,
  frames_to_client  INT NOT NULL DEFAULT 0,
  bytes_to_backend  BIGINT NOT NULL DEFAULT 0,
  bytes_to_client   BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX exec_audit_sessions_ws_time   ON exec_audit_sessions(workspace_id, opened_at DESC);
CREATE INDEX exec_audit_sessions_exe_time  ON exec_audit_sessions(exe_id, opened_at DESC);
CREATE INDEX exec_audit_sessions_user_time ON exec_audit_sessions(user_id, opened_at DESC) WHERE user_id IS NOT NULL;
CREATE INDEX exec_audit_sessions_turn      ON exec_audit_sessions(turn_id) WHERE turn_id IS NOT NULL;

CREATE TABLE exec_audit_calls (
  id                  UUID PRIMARY KEY,
  session_id          UUID REFERENCES exec_audit_sessions(id) ON DELETE CASCADE,
  workspace_id        TEXT NOT NULL,
  user_id             TEXT,
  exe_id              TEXT NOT NULL,
  source              TEXT NOT NULL CHECK (source IN ('envmcp','rest','relay')),
  rpc_id              TEXT,
  rpc_method          TEXT,
  rpc_kind            TEXT,
  request_payload_id  UUID REFERENCES exec_audit_payloads(id),
  request_size        INT NOT NULL DEFAULT 0,
  request_sha256      TEXT,
  response_payload_id UUID REFERENCES exec_audit_payloads(id),
  response_size       INT NOT NULL DEFAULT 0,
  response_sha256     TEXT,
  is_error            BOOLEAN NOT NULL DEFAULT FALSE,
  error_summary       TEXT,
  started_at          TIMESTAMPTZ NOT NULL,
  completed_at        TIMESTAMPTZ,
  duration_ms         INTEGER
);

CREATE INDEX exec_audit_calls_ws_time   ON exec_audit_calls(workspace_id, started_at DESC);
CREATE INDEX exec_audit_calls_exe_time  ON exec_audit_calls(exe_id, started_at DESC);
CREATE INDEX exec_audit_calls_user_time ON exec_audit_calls(user_id, started_at DESC) WHERE user_id IS NOT NULL;
CREATE INDEX exec_audit_calls_method    ON exec_audit_calls(rpc_method) WHERE rpc_method IS NOT NULL;
CREATE INDEX exec_audit_calls_source    ON exec_audit_calls(source, started_at DESC);
CREATE INDEX exec_audit_calls_session   ON exec_audit_calls(session_id) WHERE session_id IS NOT NULL;
CREATE INDEX exec_audit_calls_errors    ON exec_audit_calls(workspace_id, started_at DESC) WHERE is_error;
```

- [ ] **Step 3: Verify migration applies in DB tests**

```bash
go test ./internal/db/... -count=1
```

Expected: ok. The existing migration runner (`internal/db/db.go` uses `//go:embed migrations/*.sql`) auto-picks up the new file.

- [ ] **Step 4: Commit**

```bash
git add internal/db/migrations/032_exec_audit.sql
git commit -m "$(cat <<'EOF'
feat(db): add exec_audit_{sessions,calls,payloads} tables (migration 032)

Schema only — no Go callers yet (added in the next commits). The
foreign keys form: calls.session_id → sessions.id (ON DELETE CASCADE);
calls.{request,response}_payload_id → payloads.id (no cascade — payload
rows get refcounted and pruned separately so deduped payloads aren't
dropped while a peer call still references them).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: DAL types and Insert/Upsert helpers (TDD)

**Files:**
- Create: `internal/db/exec_audit.go`
- Create: `internal/db/exec_audit_test.go`

- [ ] **Step 1: Write the failing test for `UpsertAuditPayload`**

Create `internal/db/exec_audit_test.go`:

```go
package db_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/agentserver/agentserver/internal/db"
)

func TestUpsertAuditPayload_NewRow(t *testing.T) {
	ctx := context.Background()
	pgdb := newTestDB(t) // existing helper from internal/db tests; if missing, see Step 1b
	defer pgdb.Close()

	bytes := []byte("hello world, this is a payload")
	sum := sha256.Sum256(bytes)
	hash := hex.EncodeToString(sum[:])

	id, err := db.UpsertAuditPayload(ctx, pgdb, db.AuditPayload{
		Sha256:         hash,
		Compressed:     bytes, // for the DAL test, compression is the caller's job
		OriginalSize:   len(bytes),
		CompressedSize: len(bytes),
	})
	if err != nil {
		t.Fatalf("UpsertAuditPayload: %v", err)
	}
	if id == "" {
		t.Fatalf("expected non-empty id")
	}

	// Same hash again → same id, ref_count incremented.
	id2, err := db.UpsertAuditPayload(ctx, pgdb, db.AuditPayload{
		Sha256:         hash,
		Compressed:     bytes,
		OriginalSize:   len(bytes),
		CompressedSize: len(bytes),
	})
	if err != nil {
		t.Fatalf("UpsertAuditPayload again: %v", err)
	}
	if id2 != id {
		t.Fatalf("expected same id on dedupe, got %s vs %s", id2, id)
	}

	// Verify ref_count
	var refCount int
	if err := pgdb.QueryRowContext(ctx,
		`SELECT ref_count FROM exec_audit_payloads WHERE id = $1`, id,
	).Scan(&refCount); err != nil {
		t.Fatalf("read ref_count: %v", err)
	}
	if refCount != 2 {
		t.Fatalf("expected ref_count=2 after two upserts, got %d", refCount)
	}
}
```

**Step 1b: If `newTestDB(t)` doesn't exist in the `internal/db` test package**, look for the existing test helper pattern:

```bash
grep -rn 'func newTestDB\|func TestMain\|func testDB' internal/db/*_test.go
```

Use the same pattern (likely either `pgtest.NewDB(t)` or `dbtest.Open(t)`); copy the import + helper invocation from an existing test file (e.g., `internal/db/workspaces_test.go`). If no test DB helper exists in this package, STOP and escalate — DAL tests against a real postgres are required for credible coverage.

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/db/ -run TestUpsertAuditPayload_NewRow -v
```

Expected: FAIL with `undefined: db.UpsertAuditPayload` or `undefined: db.AuditPayload`.

- [ ] **Step 3: Write the minimal `internal/db/exec_audit.go`**

Create `internal/db/exec_audit.go`:

```go
// Package db: exec-audit DAL.
//
// All Upsert* helpers are idempotent by id (or by content sha256 for
// payloads) so the gateway-side uploader can retry batches safely without
// risk of duplicate rows.
package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type AuditPayload struct {
	ID             string
	Sha256         string
	Compressed     []byte
	OriginalSize   int
	CompressedSize int
}

type AuditSession struct {
	ID               string
	WorkspaceID      string
	UserID           *string
	ExeID            string
	TurnID           *string
	StreamID         string
	ClientIP         *string // store as text; cast to ::inet in SQL
	CapIAT           *time.Time
	CapEXP           *time.Time
	OpenedAt         time.Time
	ClosedAt         *time.Time
	CloseReason      *string
	FramesToBackend  int
	FramesToClient   int
	BytesToBackend   int64
	BytesToClient    int64
}

type AuditCall struct {
	ID                string
	SessionID         *string
	WorkspaceID       string
	UserID            *string
	ExeID             string
	Source            string // "envmcp" | "rest" | "relay"
	RPCID             *string
	RPCMethod         *string
	RPCKind           *string
	RequestPayloadID  *string
	RequestSize       int
	RequestSha256     *string
	ResponsePayloadID *string
	ResponseSize      int
	ResponseSha256    *string
	IsError           bool
	ErrorSummary      *string
	StartedAt         time.Time
	CompletedAt       *time.Time
	DurationMs        *int
}

// UpsertAuditPayload inserts a new payload row keyed on sha256, or
// increments ref_count on conflict. Returns the row's id.
func UpsertAuditPayload(ctx context.Context, exec ExecQuerier, p AuditPayload) (string, error) {
	if p.Sha256 == "" {
		return "", errors.New("exec_audit: payload sha256 required")
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	const q = `
		INSERT INTO exec_audit_payloads (id, sha256, compressed, original_size, compressed_size, ref_count)
		VALUES ($1, $2, $3, $4, $5, 1)
		ON CONFLICT (sha256) DO UPDATE
			SET ref_count = exec_audit_payloads.ref_count + 1
		RETURNING id`
	var id string
	if err := exec.QueryRowContext(ctx, q,
		p.ID, p.Sha256, p.Compressed, p.OriginalSize, p.CompressedSize,
	).Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}
```

If `ExecQuerier` does not exist in the package, look for the existing pattern:

```bash
grep -n 'type ExecQuerier\|type DBTX\|sql\.DB\|interface' internal/db/db.go internal/db/*.go | head
```

Use whatever the package's existing convention is for "interface that abstracts sql.DB and sql.Tx" (it might be `*sql.DB` directly with no abstraction; if so, change the parameter to `*sql.DB`).

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./internal/db/ -run TestUpsertAuditPayload_NewRow -v
```

Expected: PASS.

- [ ] **Step 5: Add the failing test for `UpsertAuditSession`**

Append to `internal/db/exec_audit_test.go`:

```go
func TestUpsertAuditSession_Idempotent(t *testing.T) {
	ctx := context.Background()
	pgdb := newTestDB(t)
	defer pgdb.Close()

	s := db.AuditSession{
		ID:          "11111111-1111-1111-1111-111111111111",
		WorkspaceID: "ws_abc",
		ExeID:       "exe_x",
		StreamID:    "stream_1",
		OpenedAt:    time.Now().UTC(),
	}
	if err := db.UpsertAuditSession(ctx, pgdb, s); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := db.UpsertAuditSession(ctx, pgdb, s); err != nil {
		t.Fatalf("second upsert (idempotent): %v", err)
	}

	var count int
	if err := pgdb.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM exec_audit_sessions WHERE id = $1`, s.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row after idempotent upserts, got %d", count)
	}
}
```

(Also add `import "time"` to the imports if not present.)

- [ ] **Step 6: Run the test to verify it fails, then add the impl**

```bash
go test ./internal/db/ -run TestUpsertAuditSession_Idempotent -v
```

Expected: FAIL with `undefined: db.UpsertAuditSession`.

Append to `internal/db/exec_audit.go`:

```go
// UpsertAuditSession inserts a session row on first call, no-ops on
// duplicate id. Use UpdateAuditSessionClose to fill in close-time fields
// later.
func UpsertAuditSession(ctx context.Context, exec ExecQuerier, s AuditSession) error {
	if s.ID == "" {
		return errors.New("exec_audit: session id required")
	}
	const q = `
		INSERT INTO exec_audit_sessions (
			id, workspace_id, user_id, exe_id, turn_id, stream_id,
			client_ip, cap_iat, cap_exp, opened_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7::inet, $8, $9, $10)
		ON CONFLICT (id) DO NOTHING`
	_, err := exec.ExecContext(ctx, q,
		s.ID, s.WorkspaceID, s.UserID, s.ExeID, s.TurnID, s.StreamID,
		s.ClientIP, s.CapIAT, s.CapEXP, s.OpenedAt,
	)
	return err
}
```

Run again:

```bash
go test ./internal/db/ -run TestUpsertAuditSession_Idempotent -v
```

Expected: PASS.

- [ ] **Step 7: Repeat the TDD cycle for `UpdateAuditSessionClose`, `UpsertAuditCall`, `UpdateAuditCallEnd`**

For each, write the failing test first (mirror the patterns above — round-trip insert+update, verify a SELECT shows the expected state), then add the SQL helper. Each pair should be a single edit cycle (~5 min) and should follow the schema columns from migration 032.

`UpdateAuditSessionClose` updates: `closed_at`, `close_reason`, `frames_to_backend`, `frames_to_client`, `bytes_to_backend`, `bytes_to_client`. Signature:

```go
func UpdateAuditSessionClose(ctx context.Context, exec ExecQuerier,
    id string, closedAt time.Time, reason string,
    framesToBackend, framesToClient int, bytesToBackend, bytesToClient int64,
) error
```

`UpsertAuditCall` inserts the call row with all "start" fields (id, session_id, workspace_id, user_id, exe_id, source, rpc_id, rpc_method, rpc_kind, request_payload_id, request_size, request_sha256, started_at). Idempotent by id.

`UpdateAuditCallEnd` updates `completed_at`, `duration_ms`, `is_error`, `error_summary`, `response_payload_id`, `response_size`, `response_sha256`.

- [ ] **Step 8: Commit**

```bash
git add internal/db/exec_audit.go internal/db/exec_audit_test.go
git commit -m "$(cat <<'EOF'
feat(db): exec-audit DAL — payload/session/call Upsert + Update helpers

All Upsert* are idempotent by id (or sha256 for payloads) so the
gateway-side uploader can safely retry batches without producing
duplicate rows. The mutation pattern is two-stage: a CallStart writes
the row with started_at; the matching CallEnd updates completed_at +
response_payload_id + error. Same shape for sessions (Open then Close).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: DAL list/get/prune helpers (TDD)

**Files:**
- Modify: `internal/db/exec_audit.go` (append)
- Modify: `internal/db/exec_audit_test.go` (append)

- [ ] **Step 1: Write the failing test for `ListAuditSessions`**

Append to `exec_audit_test.go`:

```go
func TestListAuditSessions_FilterByWorkspace(t *testing.T) {
	ctx := context.Background()
	pgdb := newTestDB(t)
	defer pgdb.Close()

	now := time.Now().UTC()
	mustUpsert := func(id, ws string, opened time.Time) {
		if err := db.UpsertAuditSession(ctx, pgdb, db.AuditSession{
			ID: id, WorkspaceID: ws, ExeID: "exe1", StreamID: "s1", OpenedAt: opened,
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	mustUpsert("aaaa1111-0000-0000-0000-000000000001", "ws_a", now.Add(-2*time.Hour))
	mustUpsert("aaaa1111-0000-0000-0000-000000000002", "ws_a", now.Add(-1*time.Hour))
	mustUpsert("bbbb1111-0000-0000-0000-000000000001", "ws_b", now)

	out, err := db.ListAuditSessions(ctx, pgdb, db.ListAuditSessionsFilter{
		WorkspaceID: "ws_a",
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 sessions for ws_a, got %d", len(out))
	}
	// Most-recent first
	if out[0].ID != "aaaa1111-0000-0000-0000-000000000002" {
		t.Fatalf("expected most-recent-first ordering, got %s first", out[0].ID)
	}
}
```

- [ ] **Step 2: Run to confirm failure, then implement**

```bash
go test ./internal/db/ -run TestListAuditSessions_FilterByWorkspace -v
```

Expected: FAIL `undefined: db.ListAuditSessions`.

Append to `exec_audit.go`:

```go
type ListAuditSessionsFilter struct {
	WorkspaceID string // required
	ExeID       string // optional
	UserID      string // optional
	TurnID      string // optional
	Since       time.Time
	Until       time.Time
	Limit       int    // 1..1000, default 100
}

func ListAuditSessions(ctx context.Context, exec ExecQuerier, f ListAuditSessionsFilter) ([]AuditSession, error) {
	if f.WorkspaceID == "" {
		return nil, errors.New("exec_audit: workspace_id required")
	}
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > 1000 {
		f.Limit = 1000
	}

	q := `SELECT id, workspace_id, user_id, exe_id, turn_id, stream_id,
	             host(client_ip), cap_iat, cap_exp, opened_at, closed_at,
	             close_reason, frames_to_backend, frames_to_client,
	             bytes_to_backend, bytes_to_client
	      FROM exec_audit_sessions
	      WHERE workspace_id = $1`
	args := []any{f.WorkspaceID}
	if f.ExeID != "" {
		q += ` AND exe_id = $` + itoa(len(args)+1)
		args = append(args, f.ExeID)
	}
	if f.UserID != "" {
		q += ` AND user_id = $` + itoa(len(args)+1)
		args = append(args, f.UserID)
	}
	if f.TurnID != "" {
		q += ` AND turn_id = $` + itoa(len(args)+1)
		args = append(args, f.TurnID)
	}
	if !f.Since.IsZero() {
		q += ` AND opened_at >= $` + itoa(len(args)+1)
		args = append(args, f.Since)
	}
	if !f.Until.IsZero() {
		q += ` AND opened_at < $` + itoa(len(args)+1)
		args = append(args, f.Until)
	}
	q += ` ORDER BY opened_at DESC LIMIT $` + itoa(len(args)+1)
	args = append(args, f.Limit)

	rows, err := exec.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditSession{}
	for rows.Next() {
		var s AuditSession
		if err := rows.Scan(
			&s.ID, &s.WorkspaceID, &s.UserID, &s.ExeID, &s.TurnID, &s.StreamID,
			&s.ClientIP, &s.CapIAT, &s.CapEXP, &s.OpenedAt, &s.ClosedAt,
			&s.CloseReason, &s.FramesToBackend, &s.FramesToClient,
			&s.BytesToBackend, &s.BytesToClient,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func itoa(n int) string { return strconv.Itoa(n) }
```

Add `"strconv"` to imports.

- [ ] **Step 3: Run to confirm pass**

```bash
go test ./internal/db/ -run TestListAuditSessions_FilterByWorkspace -v
```

Expected: PASS.

- [ ] **Step 4: Repeat TDD for ListAuditCalls, GetAuditSession, GetAuditCall, GetAuditPayload, PruneAuditOlderThan**

For each: failing test first, then implementation. Use the same filter-pattern approach as `ListAuditSessions`.

`ListAuditCallsFilter` fields:
```go
type ListAuditCallsFilter struct {
	WorkspaceID string // required
	SessionID   string // optional
	ExeID       string
	UserID      string
	Source      string // "envmcp"|"rest"|"relay"
	RPCMethod   string
	IsError     *bool  // nil = both; true = errors only; false = success only
	Since       time.Time
	Until       time.Time
	Limit       int
}
```

`GetAuditSession(ctx, exec, id)` returns the single session row (sql.ErrNoRows → return (nil, sql.ErrNoRows)).

`GetAuditCall(ctx, exec, id)` same shape.

`GetAuditPayload(ctx, exec, id)` returns the `AuditPayload` struct including the `Compressed []byte`.

`PruneAuditOlderThan(ctx, exec, cutoff time.Time)`: deletes sessions + calls older than cutoff (ON DELETE CASCADE handles call rows), then deletes orphan payloads whose `id NOT IN (SELECT request_payload_id ... UNION SELECT response_payload_id ...) AND created_at < cutoff`. Return `(deletedSessions, deletedCalls, deletedPayloads int64, err error)`.

Tests should cover:
- Filter combinations on calls (source + workspace, is_error + workspace, etc.)
- GetAuditSession returns ErrNoRows for missing id
- PruneAuditOlderThan deletes session + cascades to calls + cleans orphan payloads + leaves still-referenced payloads alone

- [ ] **Step 5: Commit**

```bash
git add internal/db/exec_audit.go internal/db/exec_audit_test.go
git commit -m "$(cat <<'EOF'
feat(db): exec-audit DAL — List/Get/Prune helpers

ListAuditSessions and ListAuditCalls support workspace-scoped filters
with optional exe_id / user_id / source / method / is_error / time
range, capped at 1000 rows. PruneAuditOlderThan cascades session
deletes to calls, then sweeps orphan payloads while protecting those
still referenced by any surviving call.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Define the audit protobuf schema

**Files:**
- Create: `internal/server/exec_audit_pb/audit.proto`
- Create: `internal/server/exec_audit_pb/audit.pb.go` (generated)
- Modify: `Makefile` (if needed)

This protobuf is the wire format between gateway uploader (Plan 2b) and agentserver ingester. Keep it small and stable; both sides depend on it.

- [ ] **Step 1: Write the .proto schema**

Create `internal/server/exec_audit_pb/audit.proto`:

```protobuf
syntax = "proto3";

package execaudit;

option go_package = "github.com/agentserver/agentserver/internal/server/exec_audit_pb;execauditpb";

import "google/protobuf/timestamp.proto";

// BatchRecords is the body of POST /internal/exec-audit/batch.
message BatchRecords {
  string gateway_id = 1;
  repeated WALRecord records = 2;
}

// WALRecord is one entry in either the gateway's on-disk WAL or the
// batched upload body. Exactly one body field is set.
message WALRecord {
  string id = 1;  // UUID of the underlying audit row (session or call)
  oneof body {
    SessionOpen  session_open  = 2;
    SessionClose session_close = 3;
    CallStart    call_start    = 4;
    CallEnd      call_end      = 5;
  }
  google.protobuf.Timestamp written_at = 7;
}

message SessionOpen {
  string workspace_id = 1;
  string user_id      = 2;
  string exe_id       = 3;
  string turn_id      = 4;
  string stream_id    = 5;
  string client_ip    = 6;
  google.protobuf.Timestamp cap_iat   = 7;
  google.protobuf.Timestamp cap_exp   = 8;
  google.protobuf.Timestamp opened_at = 9;
}

message SessionClose {
  string session_id        = 1;
  google.protobuf.Timestamp closed_at = 2;
  string close_reason      = 3;
  int32 frames_to_backend  = 4;
  int32 frames_to_client   = 5;
  int64 bytes_to_backend   = 6;
  int64 bytes_to_client    = 7;
}

message CallStart {
  string call_id      = 1;
  string session_id   = 2;  // empty for rest/relay
  string workspace_id = 3;
  string user_id      = 4;
  string exe_id       = 5;
  string source       = 6;  // "envmcp"|"rest"|"relay"
  string rpc_id       = 7;
  string rpc_method   = 8;
  string rpc_kind     = 9;
  bytes request_bytes = 10; // inline if size <= payload_max; empty if >
  int32 request_size  = 11;
  string request_sha256 = 12;
  google.protobuf.Timestamp started_at = 13;
}

message CallEnd {
  string call_id = 1;
  google.protobuf.Timestamp completed_at = 2;
  bool is_error = 3;
  string error_summary = 4;
  bytes response_bytes = 5;
  int32 response_size  = 6;
  string response_sha256 = 7;
}
```

- [ ] **Step 2: Generate the .pb.go file**

If a `make proto` target exists, run it:

```bash
make proto
```

Otherwise check for `protoc` availability and run it manually:

```bash
which protoc-gen-go || go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
protoc --go_out=. --go_opt=paths=source_relative \
    internal/server/exec_audit_pb/audit.proto
```

Then verify the generated `audit.pb.go` exists and compiles:

```bash
go build ./internal/server/exec_audit_pb/...
```

- [ ] **Step 3: If `make proto` did not exist, add it**

Inspect `Makefile`:

```bash
grep -n "^proto\|protoc\|\.proto" Makefile | head
```

If there is no protobuf target, append at the end of `Makefile`:

```makefile
proto:
	protoc --go_out=. --go_opt=paths=source_relative \
	    internal/server/exec_audit_pb/audit.proto
```

And add `proto` to `.PHONY` at the top of the file.

- [ ] **Step 4: Commit**

```bash
git add internal/server/exec_audit_pb/audit.proto internal/server/exec_audit_pb/audit.pb.go
# If Makefile changed:
git add Makefile
git commit -m "$(cat <<'EOF'
feat(exec-audit): wire-format protobuf for ingest

Defines BatchRecords / WALRecord / SessionOpen / SessionClose /
CallStart / CallEnd. The gateway-side uploader (Plan 2b) writes WAL
records in this protobuf and POSTs batches to
/internal/exec-audit/batch (Plan 2a Task 7).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Response DTOs in api_types.go

**Files:**
- Modify: `internal/server/api_types.go`

- [ ] **Step 1: Append the new types**

Edit `internal/server/api_types.go`. Find the end of the file (or a sensible insertion point near other Workspace-related response types) and append:

```go
// AuditSessionSummary is the per-row shape in ListAuditSessionsResponse.
type AuditSessionSummary struct {
	ID               string `json:"id" validate:"required"`
	WorkspaceID      string `json:"workspace_id" validate:"required"`
	UserID           string `json:"user_id,omitempty"`
	ExeID            string `json:"exe_id" validate:"required"`
	TurnID           string `json:"turn_id,omitempty"`
	StreamID         string `json:"stream_id" validate:"required"`
	ClientIP         string `json:"client_ip,omitempty"`
	OpenedAt         string `json:"opened_at" validate:"required"`           // RFC3339
	ClosedAt         string `json:"closed_at,omitempty"`                     // RFC3339
	CloseReason      string `json:"close_reason,omitempty"`
	FramesToBackend  int    `json:"frames_to_backend"`
	FramesToClient   int    `json:"frames_to_client"`
	BytesToBackend   int64  `json:"bytes_to_backend"`
	BytesToClient    int64  `json:"bytes_to_client"`
} // @name AuditSessionSummary

// AuditCallSummary is the per-row shape in ListAuditCallsResponse and
// AuditSessionDetail.FirstCalls.
type AuditCallSummary struct {
	ID                string `json:"id" validate:"required"`
	SessionID         string `json:"session_id,omitempty"`
	WorkspaceID       string `json:"workspace_id" validate:"required"`
	UserID            string `json:"user_id,omitempty"`
	ExeID             string `json:"exe_id" validate:"required"`
	Source            string `json:"source" validate:"required"` // envmcp|rest|relay
	RPCID             string `json:"rpc_id,omitempty"`
	RPCMethod         string `json:"rpc_method,omitempty"`
	RPCKind           string `json:"rpc_kind,omitempty"`
	RequestSize       int    `json:"request_size"`
	RequestSha256     string `json:"request_sha256,omitempty"`
	ResponseSize      int    `json:"response_size"`
	ResponseSha256    string `json:"response_sha256,omitempty"`
	IsError           bool   `json:"is_error"`
	ErrorSummary      string `json:"error_summary,omitempty"`
	StartedAt         string `json:"started_at" validate:"required"` // RFC3339
	CompletedAt       string `json:"completed_at,omitempty"`         // RFC3339
	DurationMs        int    `json:"duration_ms,omitempty"`
} // @name AuditCallSummary

type AuditCallDetail struct {
	AuditCallSummary
	RequestPreview  string `json:"request_preview,omitempty"`  // utf8-decoded first 8 KiB
	ResponsePreview string `json:"response_preview,omitempty"`
} // @name AuditCallDetail

type AuditSessionDetail struct {
	Session     AuditSessionSummary `json:"session" validate:"required"`
	FirstCalls  []AuditCallSummary  `json:"first_calls" validate:"required"`
} // @name AuditSessionDetail

type ListAuditSessionsResponse struct {
	Sessions   []AuditSessionSummary `json:"sessions" validate:"required"`
	NextCursor string                `json:"next_cursor,omitempty"`
} // @name ListAuditSessionsResponse

type ListAuditCallsResponse struct {
	Calls      []AuditCallSummary `json:"calls" validate:"required"`
	NextCursor string             `json:"next_cursor,omitempty"`
} // @name ListAuditCallsResponse
```

- [ ] **Step 2: Verify build**

```bash
go build ./internal/server/...
```

Expected: empty output.

- [ ] **Step 3: Commit**

```bash
git add internal/server/api_types.go
git commit -m "$(cat <<'EOF'
feat(server): exec-audit response DTOs

Used by GET /api/workspaces/{id}/exec-audit/{sessions,calls,calls/{id}}
in the next commit. Times are RFC3339 strings on the wire (matches
existing API style — see WorkspaceOperationsResponse pre-removal).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Ingest handler — POST /internal/exec-audit/batch (TDD)

**Files:**
- Create: `internal/server/exec_audit.go`
- Create: `internal/server/exec_audit_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/server/exec_audit_test.go`:

```go
package server_test

import (
	"bytes"
	"encoding/hex"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/server"
	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPostInternalExecAuditBatch_AcceptsSessionAndCall(t *testing.T) {
	srv := newTestServer(t)   // existing helper; if missing see Step 1b
	defer srv.Close()

	now := time.Now().UTC()
	reqBytes := []byte(`{"jsonrpc":"2.0","id":1,"method":"shell"}`)
	sha := sha256.Sum256(reqBytes)
	hash := hex.EncodeToString(sha[:])

	batch := &pb.BatchRecords{
		GatewayId: "test-gateway",
		Records: []*pb.WALRecord{
			{
				Id: "11111111-1111-1111-1111-111111111111",
				Body: &pb.WALRecord_SessionOpen{
					SessionOpen: &pb.SessionOpen{
						WorkspaceId: "ws_test",
						ExeId:       "exe_a",
						StreamId:    "s1",
						OpenedAt:    timestamppb.New(now),
					},
				},
				WrittenAt: timestamppb.New(now),
			},
			{
				Id: "22222222-2222-2222-2222-222222222222",
				Body: &pb.WALRecord_CallStart{
					CallStart: &pb.CallStart{
						CallId:        "22222222-2222-2222-2222-222222222222",
						SessionId:     "11111111-1111-1111-1111-111111111111",
						WorkspaceId:   "ws_test",
						ExeId:         "exe_a",
						Source:        "envmcp",
						RpcId:         "1",
						RpcMethod:     "shell",
						RpcKind:       "request",
						RequestBytes:  reqBytes,
						RequestSize:   int32(len(reqBytes)),
						RequestSha256: hash,
						StartedAt:     timestamppb.New(now),
					},
				},
				WrittenAt: timestamppb.New(now),
			},
		},
	}
	body, err := proto.Marshal(batch)
	if err != nil { t.Fatalf("marshal: %v", err) }

	req := httptest.NewRequest(http.MethodPost, "/internal/exec-audit/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Internal-Secret", srv.InternalSecret())

	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify rows landed.
	row, err := server.TestGetAuditSession(srv, "11111111-1111-1111-1111-111111111111")
	if err != nil { t.Fatalf("get session: %v", err) }
	if row.WorkspaceID != "ws_test" || row.ExeID != "exe_a" {
		t.Fatalf("session row mismatch: %+v", row)
	}
}

func TestPostInternalExecAuditBatch_Idempotent(t *testing.T) {
	// POST the same batch twice → one row, no error.
	// (Mirror the above but send batch.Records twice; assert count = 1.)
}
```

**Step 1b**: if `newTestServer(t)` and `TestGetAuditSession` don't exist:

- `newTestServer(t)` should be a helper in `internal/server/testhelpers_db_test.go` or `internal/server/server_test.go` that boots a `*server.Server` with a fresh test DB. Look for the existing pattern:
  ```bash
  grep -n 'func newTestServer\|func testServer\|server\.NewServer.*test' internal/server/*_test.go
  ```
  Use whatever's there.
- `TestGetAuditSession` is a test-only accessor — add a `server_test_export.go` file (no `_test.go` suffix, but build-tagged) **OR** if the codebase already uses the `internal/server/testexport.go` pattern, follow it. Simplest: expose `func (s *Server) DB() *sql.DB` if it doesn't exist, then call `db.GetAuditSession(ctx, s.DB(), id)` directly in tests.

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/server/ -run TestPostInternalExecAuditBatch -v
```

Expected: FAIL (route not registered).

- [ ] **Step 3: Implement the handler in `internal/server/exec_audit.go`**

Create `internal/server/exec_audit.go`:

```go
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/agentserver/agentserver/internal/db"
	pb "github.com/agentserver/agentserver/internal/server/exec_audit_pb"
	"github.com/go-chi/chi/v5"
	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/proto"
)

const (
	auditPayloadInlineMax = 4 * 1024            // store inline if ≤4 KiB before zstd
	auditPayloadHardMax   = 4 * 1024 * 1024     // refuse to store bytes above this (only hash+size)
	auditPreviewBytes     = 8 * 1024            // first 8 KiB returned as utf8 preview in GET ../call/{id}
)

// postInternalExecAuditBatch ingests one batch from the gateway uploader.
// Auth: X-Internal-Secret = INTERNAL_API_SECRET. Body:
// Content-Type: application/x-protobuf, body = BatchRecords.
//
// All record types are idempotent (CallStart upsert-by-id, SessionOpen
// upsert-by-id, CallEnd update, SessionClose update). Returns 200 OK
// with a small JSON body {"processed":N,"skipped":M}.
//
//	@Summary  Ingest a batch of exec-gateway audit records (internal)
//	@Tags     Exec-Audit
//	@Accept   application/x-protobuf
//	@Produce  json
//	@Param    X-Internal-Secret header string true "Shared secret"
//	@Success  200 {object} ExecAuditBatchAckResponse
//	@Failure  400 {string} string
//	@Failure  401 {string} string
//	@Failure  500 {string} string
//	@Router   /internal/exec-audit/batch [post]
func (s *Server) postInternalExecAuditBatch(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-protobuf") {
		http.Error(w, "Content-Type: application/x-protobuf required", http.StatusBadRequest)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 32*1024*1024))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var batch pb.BatchRecords
	if err := proto.Unmarshal(raw, &batch); err != nil {
		http.Error(w, "unmarshal: "+err.Error(), http.StatusBadRequest)
		return
	}

	processed := 0
	skipped := 0
	for _, rec := range batch.Records {
		if err := s.applyAuditRecord(r.Context(), rec); err != nil {
			// Idempotency: log + skip the bad record rather than failing
			// the whole batch. The uploader will retry on 5xx; we don't
			// want one malformed record to block the queue.
			s.logger.Warn("exec-audit: apply failed",
				"id", rec.Id, "err", err)
			skipped++
			continue
		}
		processed++
	}
	writeJSON(w, ExecAuditBatchAckResponse{Processed: processed, Skipped: skipped})
}

func (s *Server) applyAuditRecord(ctx context.Context, rec *pb.WALRecord) error {
	switch b := rec.Body.(type) {
	case *pb.WALRecord_SessionOpen:
		op := b.SessionOpen
		var capIAT, capEXP *time.Time
		if op.CapIat != nil {
			t := op.CapIat.AsTime()
			capIAT = &t
		}
		if op.CapExp != nil {
			t := op.CapExp.AsTime()
			capEXP = &t
		}
		return db.UpsertAuditSession(ctx, s.DB(), db.AuditSession{
			ID:          rec.Id,
			WorkspaceID: op.WorkspaceId,
			UserID:      strPtrOrNil(op.UserId),
			ExeID:       op.ExeId,
			TurnID:      strPtrOrNil(op.TurnId),
			StreamID:    op.StreamId,
			ClientIP:    strPtrOrNil(op.ClientIp),
			CapIAT:      capIAT,
			CapEXP:      capEXP,
			OpenedAt:    op.OpenedAt.AsTime().UTC(),
		})
	case *pb.WALRecord_SessionClose:
		cl := b.SessionClose
		return db.UpdateAuditSessionClose(ctx, s.DB(),
			cl.SessionId, cl.ClosedAt.AsTime().UTC(), cl.CloseReason,
			int(cl.FramesToBackend), int(cl.FramesToClient),
			cl.BytesToBackend, cl.BytesToClient,
		)
	case *pb.WALRecord_CallStart:
		cs := b.CallStart
		var reqPayloadID *string
		if len(cs.RequestBytes) > 0 {
			id, err := s.upsertPayload(ctx, cs.RequestBytes)
			if err != nil {
				return fmt.Errorf("upsert request payload: %w", err)
			}
			reqPayloadID = &id
		}
		return db.UpsertAuditCall(ctx, s.DB(), db.AuditCall{
			ID:               rec.Id,
			SessionID:        strPtrOrNil(cs.SessionId),
			WorkspaceID:      cs.WorkspaceId,
			UserID:           strPtrOrNil(cs.UserId),
			ExeID:            cs.ExeId,
			Source:           cs.Source,
			RPCID:            strPtrOrNil(cs.RpcId),
			RPCMethod:        strPtrOrNil(cs.RpcMethod),
			RPCKind:          strPtrOrNil(cs.RpcKind),
			RequestPayloadID: reqPayloadID,
			RequestSize:      int(cs.RequestSize),
			RequestSha256:    strPtrOrNil(cs.RequestSha256),
			StartedAt:        cs.StartedAt.AsTime().UTC(),
		})
	case *pb.WALRecord_CallEnd:
		ce := b.CallEnd
		var respPayloadID *string
		if len(ce.ResponseBytes) > 0 {
			id, err := s.upsertPayload(ctx, ce.ResponseBytes)
			if err != nil {
				return fmt.Errorf("upsert response payload: %w", err)
			}
			respPayloadID = &id
		}
		completedAt := ce.CompletedAt.AsTime().UTC()
		return db.UpdateAuditCallEnd(ctx, s.DB(), ce.CallId,
			completedAt, ce.IsError, ce.ErrorSummary,
			respPayloadID, int(ce.ResponseSize), strPtrOrNil(ce.ResponseSha256),
		)
	}
	return errors.New("exec-audit: unknown WALRecord body")
}

// upsertPayload zstd-compresses bytes ≤ 4 MiB and stores them. Returns the
// payload row id. Caller should not invoke this for byte slices that exceed
// the hard cap; the gateway is responsible for not sending them.
func (s *Server) upsertPayload(ctx context.Context, raw []byte) (string, error) {
	if len(raw) > auditPayloadHardMax {
		return "", fmt.Errorf("payload %d bytes exceeds hard cap %d", len(raw), auditPayloadHardMax)
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	encoded, err := zstdCompress(raw)
	if err != nil {
		return "", err
	}
	return db.UpsertAuditPayload(ctx, s.DB(), db.AuditPayload{
		Sha256:         hash,
		Compressed:     encoded,
		OriginalSize:   len(raw),
		CompressedSize: len(encoded),
	})
}

var zstdEncoder, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
var zstdDecoder, _ = zstd.NewReader(nil)

func zstdCompress(b []byte) ([]byte, error)   { return zstdEncoder.EncodeAll(b, nil), nil }
func zstdDecompress(b []byte) ([]byte, error) { return zstdDecoder.DecodeAll(b, nil) }

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type ExecAuditBatchAckResponse struct {
	Processed int `json:"processed"`
	Skipped   int `json:"skipped"`
} // @name ExecAuditBatchAckResponse
```

(Add the `Server.DB()` accessor if it doesn't exist — likely the codebase exposes the connection via some name; grep `grep -n 'sql\.DB\|database/sql' internal/server/server.go | head` to find it. If the field is unexported and there's no accessor, add one: `func (s *Server) DB() *sql.DB { return s.db }`.)

- [ ] **Step 4: Register the route** in `internal/server/server.go`

Find the existing `r.Get("/internal/sdk/connected", ...)` registration block (or somewhere in the internal routes section after the validate-api-key handler) and add:

```go
	r.Post("/internal/exec-audit/batch", func(w http.ResponseWriter, r *http.Request) {
		secret := os.Getenv("INTERNAL_API_SECRET")
		if secret != "" && r.Header.Get("X-Internal-Secret") != secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.postInternalExecAuditBatch(w, r)
	})
```

(The auth wrapper mirrors what the deleted operations handlers used; `INTERNAL_API_SECRET` is the existing env var.)

- [ ] **Step 5: Run the test**

```bash
go test ./internal/server/ -run TestPostInternalExecAuditBatch -v
```

Expected: PASS for the first test, FAIL with a clear message for the idempotency one (since we haven't fleshed it out — that's fine for this step).

Fill in the body of `TestPostInternalExecAuditBatch_Idempotent`:

```go
func TestPostInternalExecAuditBatch_Idempotent(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	now := time.Now().UTC()
	batch := &pb.BatchRecords{
		GatewayId: "test-gateway",
		Records: []*pb.WALRecord{
			{
				Id: "deadbeef-0000-0000-0000-000000000001",
				Body: &pb.WALRecord_SessionOpen{
					SessionOpen: &pb.SessionOpen{
						WorkspaceId: "ws_idem", ExeId: "exe_a", StreamId: "s1",
						OpenedAt: timestamppb.New(now),
					},
				},
				WrittenAt: timestamppb.New(now),
			},
		},
	}
	body, _ := proto.Marshal(batch)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/internal/exec-audit/batch", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/x-protobuf")
		req.Header.Set("X-Internal-Secret", srv.InternalSecret())
		rr := httptest.NewRecorder()
		srv.Router().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("iter %d: status %d body %s", i, rr.Code, rr.Body.String())
		}
	}

	out, err := db.ListAuditSessions(context.Background(), srv.DB(),
		db.ListAuditSessionsFilter{WorkspaceID: "ws_idem", Limit: 10})
	if err != nil { t.Fatalf("list: %v", err) }
	if len(out) != 1 {
		t.Fatalf("expected 1 session after 3 idempotent posts, got %d", len(out))
	}
}
```

Run again:

```bash
go test ./internal/server/ -run TestPostInternalExecAuditBatch -v
```

Expected: both PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/server/exec_audit.go internal/server/exec_audit_test.go internal/server/server.go
git commit -m "$(cat <<'EOF'
feat(server): POST /internal/exec-audit/batch ingest endpoint

Accepts a protobuf BatchRecords body, dispatches each WALRecord to the
matching DAL upsert/update. Idempotent by record id, so the gateway
uploader can retry batches safely. Auth: X-Internal-Secret matching
INTERNAL_API_SECRET (same as the legacy operations endpoint used).
Payloads inside CallStart/CallEnd are zstd-compressed before storage.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Workspace-scoped read API (TDD)

**Files:**
- Modify: `internal/server/exec_audit.go` (append)
- Modify: `internal/server/exec_audit_test.go` (append)
- Modify: `internal/server/server.go` (register routes)

- [ ] **Step 1: Write the failing test for the list-sessions endpoint**

Append to `internal/server/exec_audit_test.go`:

```go
func TestGetWorkspaceExecAuditSessions_GatedByWorkspace(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	// Insert two sessions: one for ws_alice, one for ws_bob.
	now := time.Now().UTC()
	if err := db.UpsertAuditSession(context.Background(), srv.DB(), db.AuditSession{
		ID: "aaaa0000-0000-0000-0000-000000000001",
		WorkspaceID: "ws_alice", ExeID: "exe_a", StreamID: "s1", OpenedAt: now,
	}); err != nil { t.Fatal(err) }
	if err := db.UpsertAuditSession(context.Background(), srv.DB(), db.AuditSession{
		ID: "bbbb0000-0000-0000-0000-000000000001",
		WorkspaceID: "ws_bob", ExeID: "exe_b", StreamID: "s2", OpenedAt: now,
	}); err != nil { t.Fatal(err) }

	// Alice's session cookie can fetch ws_alice but not ws_bob.
	aliceCookie := srv.SignInAs(t, "alice")
	srv.AddWorkspaceMember(t, "ws_alice", "alice")
	srv.AddWorkspaceMember(t, "ws_bob", "bob")

	req := httptest.NewRequest(http.MethodGet,
		"/api/workspaces/ws_alice/exec-audit/sessions?limit=10", nil)
	req.Header.Set("Cookie", aliceCookie)
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK { t.Fatalf("alice fetching ws_alice: %d %s", rr.Code, rr.Body.String()) }

	var got ListAuditSessionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil { t.Fatal(err) }
	if len(got.Sessions) != 1 || got.Sessions[0].WorkspaceID != "ws_alice" {
		t.Fatalf("expected 1 ws_alice session, got %+v", got)
	}

	// Cross-workspace: alice fetching ws_bob should 403.
	req2 := httptest.NewRequest(http.MethodGet,
		"/api/workspaces/ws_bob/exec-audit/sessions", nil)
	req2.Header.Set("Cookie", aliceCookie)
	rr2 := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusForbidden {
		t.Fatalf("expected 403 cross-workspace, got %d", rr2.Code)
	}
}
```

(If `srv.SignInAs(t, "user")` / `srv.AddWorkspaceMember(t, "ws", "user")` helpers don't exist, look for the existing pattern — `grep -n 'SignInAs\|loginAs\|sessionCookie' internal/server/*_test.go` — and adapt. The codebase tests workspace-gated routes elsewhere so this pattern definitely exists.)

- [ ] **Step 2: Run to confirm failure** (404 because route not registered).

```bash
go test ./internal/server/ -run TestGetWorkspaceExecAuditSessions -v
```

- [ ] **Step 3: Implement the four GET handlers + the workspace wrappers**

Append to `internal/server/exec_audit.go`:

```go
// getInternalExecAuditSessions: GET /internal/exec-audit/sessions.
// Same auth as the POST sibling. workspace_id required in query string.
func (s *Server) getInternalExecAuditSessions(w http.ResponseWriter, r *http.Request) {
	f, err := parseSessionsFilter(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rows, err := db.ListAuditSessions(r.Context(), s.DB(), f)
	if err != nil {
		s.writeServerError(w, "list sessions", err)
		return
	}
	writeJSON(w, ListAuditSessionsResponse{Sessions: sessionsToDTO(rows)})
}

// getWorkspaceExecAuditSessions: GET /api/workspaces/{id}/exec-audit/sessions.
// Workspace membership enforced; URL workspace_id always wins over query.
func (s *Server) getWorkspaceExecAuditSessions(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if wsID == "" {
		http.Error(w, "workspace id required", http.StatusBadRequest)
		return
	}
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}
	q := r.URL.Query()
	q.Set("workspace_id", wsID)
	r.URL.RawQuery = q.Encode()
	s.getInternalExecAuditSessions(w, r)
}

// getInternalExecAuditCalls / getWorkspaceExecAuditCalls — mirror shape
// but with parseCallsFilter and ListAuditCalls. Same wrapping.

// getInternalExecAuditCall / getWorkspaceExecAuditCall — single row by
// path param {call_id}. Includes RequestPreview / ResponsePreview built
// from auditPreviewBytes of the decompressed payload.

// getWorkspaceExecAuditCallPayload: GET .../calls/{call_id}/payload?side=request|response.
// Returns raw decompressed bytes with Content-Type application/octet-stream.
// 404 if size > auditPayloadHardMax (no bytes were ever stored).

func parseSessionsFilter(q url.Values) (db.ListAuditSessionsFilter, error) {
	f := db.ListAuditSessionsFilter{
		WorkspaceID: q.Get("workspace_id"),
		ExeID:       q.Get("exe_id"),
		UserID:      q.Get("user_id"),
		TurnID:      q.Get("turn_id"),
	}
	if f.WorkspaceID == "" {
		return f, errors.New("workspace_id required")
	}
	if s := q.Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil { return f, fmt.Errorf("since: %w", err) }
		f.Since = t
	}
	if s := q.Get("until"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil { return f, fmt.Errorf("until: %w", err) }
		f.Until = t
	}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil { return f, fmt.Errorf("limit: %w", err) }
		f.Limit = n
	}
	return f, nil
}

func sessionsToDTO(in []db.AuditSession) []AuditSessionSummary {
	out := make([]AuditSessionSummary, 0, len(in))
	for _, s := range in {
		out = append(out, AuditSessionSummary{
			ID:              s.ID,
			WorkspaceID:     s.WorkspaceID,
			UserID:          ptrStr(s.UserID),
			ExeID:           s.ExeID,
			TurnID:          ptrStr(s.TurnID),
			StreamID:        s.StreamID,
			ClientIP:        ptrStr(s.ClientIP),
			OpenedAt:        s.OpenedAt.Format(time.RFC3339),
			ClosedAt:        ptrTimeRFC(s.ClosedAt),
			CloseReason:     ptrStr(s.CloseReason),
			FramesToBackend: s.FramesToBackend,
			FramesToClient:  s.FramesToClient,
			BytesToBackend:  s.BytesToBackend,
			BytesToClient:   s.BytesToClient,
		})
	}
	return out
}

func ptrStr(p *string) string {
	if p == nil { return "" }
	return *p
}
func ptrTimeRFC(p *time.Time) string {
	if p == nil { return "" }
	return p.Format(time.RFC3339)
}
```

Implement `parseCallsFilter`, `callsToDTO`, `callToDetailDTO`, and the remaining `getInternal*` + `getWorkspace*` wrappers following the same pattern (they're mechanical).

For `getWorkspaceExecAuditCallPayload`:

```go
func (s *Server) getWorkspaceExecAuditCallPayload(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	callID := chi.URLParam(r, "call_id")
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok { return }
	side := r.URL.Query().Get("side")
	if side != "request" && side != "response" {
		http.Error(w, "side=request|response required", http.StatusBadRequest)
		return
	}
	call, err := db.GetAuditCall(r.Context(), s.DB(), callID)
	if err != nil {
		http.Error(w, "call not found", http.StatusNotFound)
		return
	}
	if call.WorkspaceID != wsID {
		http.Error(w, "not found", http.StatusNotFound) // tenant isolation
		return
	}
	var payloadID *string
	if side == "request" {
		payloadID = call.RequestPayloadID
	} else {
		payloadID = call.ResponsePayloadID
	}
	if payloadID == nil {
		http.Error(w, "payload not stored (size exceeded cap)", http.StatusNotFound)
		return
	}
	p, err := db.GetAuditPayload(r.Context(), s.DB(), *payloadID)
	if err != nil {
		s.writeServerError(w, "get payload", err)
		return
	}
	raw, err := zstdDecompress(p.Compressed)
	if err != nil {
		s.writeServerError(w, "decompress", err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
	w.Write(raw)
}
```

- [ ] **Step 4: Register the routes** in `internal/server/server.go`

In the internal-routes block (alongside the new POST registered in Task 7):

```go
	r.Get("/internal/exec-audit/sessions", func(w http.ResponseWriter, r *http.Request) {
		secret := os.Getenv("INTERNAL_API_SECRET")
		if secret != "" && r.Header.Get("X-Internal-Secret") != secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.getInternalExecAuditSessions(w, r)
	})
	r.Get("/internal/exec-audit/sessions/{session_id}", func(w http.ResponseWriter, r *http.Request) {
		// ... same auth wrap, calls s.getInternalExecAuditSession
	})
	r.Get("/internal/exec-audit/calls", func(w http.ResponseWriter, r *http.Request) {
		// ...
	})
	r.Get("/internal/exec-audit/calls/{call_id}", func(w http.ResponseWriter, r *http.Request) {
		// ...
	})
```

In the protected workspace-membership-gated group (around the existing `r.Get("/api/workspaces/{id}/members"...)` lines):

```go
		r.Get("/api/workspaces/{id}/exec-audit/sessions", s.getWorkspaceExecAuditSessions)
		r.Get("/api/workspaces/{id}/exec-audit/sessions/{session_id}", s.getWorkspaceExecAuditSession)
		r.Get("/api/workspaces/{id}/exec-audit/calls", s.getWorkspaceExecAuditCalls)
		r.Get("/api/workspaces/{id}/exec-audit/calls/{call_id}", s.getWorkspaceExecAuditCall)
		r.Get("/api/workspaces/{id}/exec-audit/calls/{call_id}/payload", s.getWorkspaceExecAuditCallPayload)
```

- [ ] **Step 5: Run all tests**

```bash
go test ./internal/server/ -run TestGetWorkspaceExecAuditSessions -v
go test ./internal/server/... -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/server/exec_audit.go internal/server/exec_audit_test.go internal/server/server.go
git commit -m "$(cat <<'EOF'
feat(server): exec-audit read API (sessions / calls / payload)

Internal (X-Internal-Secret) and workspace-scoped
(requireWorkspaceMember) variants. URL workspace_id always wins over
query string to prevent cross-tenant lookups. Payload endpoint returns
raw decompressed bytes; 404s when the payload was over the hard cap
(only sha256+size were stored).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: Retention loop

**Files:**
- Create: `internal/server/exec_audit_retention.go`
- Create: `internal/server/exec_audit_retention_test.go`
- Modify: `cmd/serve.go`

- [ ] **Step 1: Write the failing test**

Create `internal/server/exec_audit_retention_test.go`:

```go
package server_test

import (
	"context"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/server"
)

func TestPruneAuditOlderThan_RemovesOldSessionsAndOrphanPayloads(t *testing.T) {
	srv := newTestServer(t); defer srv.Close()
	ctx := context.Background()

	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	recent := time.Now().UTC().Add(-1 * time.Hour)

	// Insert old session + call + payload (should be deleted).
	mustUpsertPayload(t, srv, "old-payload", "abc")
	mustUpsertSession(t, srv, "old-session", "ws1", old)
	mustUpsertCall(t, srv, "old-call", "old-session", "ws1", old, "old-payload")

	// Insert recent session + call + payload (should survive).
	mustUpsertPayload(t, srv, "new-payload", "def")
	mustUpsertSession(t, srv, "new-session", "ws1", recent)
	mustUpsertCall(t, srv, "new-call", "new-session", "ws1", recent, "new-payload")

	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	got, err := server.RunAuditRetentionOnce(ctx, srv, cutoff)
	if err != nil { t.Fatal(err) }
	if got.Sessions != 1 || got.Calls != 1 || got.Payloads != 1 {
		t.Fatalf("expected 1/1/1, got %+v", got)
	}

	// Sanity: new survivor still present.
	if _, err := db.GetAuditSession(ctx, srv.DB(), "new-session"); err != nil {
		t.Fatalf("new-session unexpectedly deleted: %v", err)
	}
}
```

(`RunAuditRetentionOnce` is the test-export. `mustUpsert*` are small helpers — write them at the bottom of the test file.)

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/server/ -run TestPruneAuditOlderThan -v
```

Expected: FAIL undefined `RunAuditRetentionOnce`.

- [ ] **Step 3: Implement**

Create `internal/server/exec_audit_retention.go`:

```go
package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/agentserver/agentserver/internal/db"
)

type AuditRetentionResult struct {
	Sessions int64
	Calls    int64
	Payloads int64
}

// StartAuditRetentionLoop runs prune every tick until ctx is done.
// ttl <= 0 disables (loop never starts).
func (s *Server) StartAuditRetentionLoop(ctx context.Context, ttl time.Duration, tick time.Duration) {
	if ttl <= 0 {
		slog.Info("exec-audit retention: disabled (ttl=0)")
		return
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			cutoff := now.UTC().Add(-ttl)
			res, err := s.runAuditRetentionOnce(ctx, cutoff)
			if err != nil {
				slog.Warn("exec-audit retention: prune failed", "err", err)
				continue
			}
			slog.Info("exec-audit retention: pruned",
				"sessions", res.Sessions, "calls", res.Calls, "payloads", res.Payloads,
				"cutoff", cutoff.Format(time.RFC3339))
		}
	}
}

func (s *Server) runAuditRetentionOnce(ctx context.Context, cutoff time.Time) (AuditRetentionResult, error) {
	sess, calls, payloads, err := db.PruneAuditOlderThan(ctx, s.DB(), cutoff)
	return AuditRetentionResult{Sessions: sess, Calls: calls, Payloads: payloads}, err
}

// RunAuditRetentionOnce is exported for tests.
func RunAuditRetentionOnce(ctx context.Context, s *Server, cutoff time.Time) (AuditRetentionResult, error) {
	return s.runAuditRetentionOnce(ctx, cutoff)
}
```

- [ ] **Step 4: Verify the test passes**

```bash
go test ./internal/server/ -run TestPruneAuditOlderThan -v
```

Expected: PASS.

- [ ] **Step 5: Wire up in cmd/serve.go**

Edit `cmd/serve.go`. Find a sensible insertion point near the existing `go healthMon.Run(healthCtx)` line (the old retention launch sat there before deletion in Plan 1). Add:

```go
		// Exec-audit retention loop. Default 90 days; 0 disables.
		auditRetentionDays := 90
		if v := os.Getenv("AGENTSERVER_EXEC_AUDIT_RETENTION_DAYS"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				auditRetentionDays = n
			} else {
				log.Printf("Warning: AGENTSERVER_EXEC_AUDIT_RETENTION_DAYS=%q invalid, using default %d", v, auditRetentionDays)
			}
		}
		go srv.StartAuditRetentionLoop(healthCtx,
			time.Duration(auditRetentionDays)*24*time.Hour, time.Hour)
```

(Reuses `healthCtx` and `time.Hour` already imported in this file.)

- [ ] **Step 6: Verify module build + full test sweep**

```bash
go build ./... && go test ./internal/server/... ./internal/db/... -count=1 2>&1 | tail
```

Expected: green.

- [ ] **Step 7: Commit**

```bash
git add internal/server/exec_audit_retention.go internal/server/exec_audit_retention_test.go cmd/serve.go
git commit -m "$(cat <<'EOF'
feat(server): exec-audit retention loop

Hourly prune of sessions + calls older than ttl, then orphan-payload
sweep that protects rows still referenced by surviving calls. Configurable
via AGENTSERVER_EXEC_AUDIT_RETENTION_DAYS (default 90, 0 disables).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: Regenerate OpenAPI spec and full verification

**Files:**
- Modify (regenerated): `docs/api/openapi.yaml`, `docs/api/openapi.json`, `web/src/lib/api-generated/schema.d.ts`

- [ ] **Step 1: Run `make openapi`**

```bash
make openapi 2>&1 | tail -20
```

Expected: clean. The new `@Router` and `@Summary` annotations on the exec-audit handlers (added in Tasks 7 + 8) get picked up; the new types (added in Task 6) get serialized into the schema.

- [ ] **Step 2: Verify the regenerated specs include the new endpoints**

```bash
grep -iE "exec-audit|AuditSessionSummary|AuditCallSummary" docs/api/openapi.yaml | head -10
```

Expected: matches showing the endpoints and types are present.

- [ ] **Step 3: Full module test + build sweep**

```bash
make test 2>&1 | tail
make build 2>&1 | tail
```

Both green.

- [ ] **Step 4: Commit regenerated artifacts**

```bash
git add docs/api/openapi.yaml docs/api/openapi.json web/src/lib/api-generated/schema.d.ts
git commit -m "$(cat <<'EOF'
chore(api): regenerate OpenAPI for exec-audit endpoints

Auto-regenerated via make openapi. Adds the four /internal/exec-audit
endpoints, the five /api/workspaces/{id}/exec-audit endpoints, and
all the AuditSessionSummary / AuditCallSummary / AuditCallDetail /
AuditSessionDetail / List*Response / ExecAuditBatchAckResponse types.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: Open the pull request

- [ ] **Step 1: Push the branch**

```bash
git push -u github HEAD 2>&1 | tail
```

- [ ] **Step 2: Open the PR**

```bash
gh pr create --base main \
  --title "feat(exec-audit): agentserver-side foundation (tables, ingester, read API, retention)" \
  --body "$(cat <<'EOF'
## Summary

Builds the agentserver-side foundation for the new codex-exec-gateway audit subsystem:

- 3 new tables (\`exec_audit_sessions\`, \`exec_audit_calls\`, \`exec_audit_payloads\`) via migration 032
- DAL with idempotent Upsert/Update + filter-based List + Get + Prune helpers (\`internal/db/exec_audit.go\`)
- Wire-format protobuf for ingest (\`internal/server/exec_audit_pb/audit.proto\`)
- Ingest endpoint \`POST /internal/exec-audit/batch\` (auth: \`X-Internal-Secret\`), idempotent at record level so the gateway uploader can safely retry
- Workspace-scoped read API:
  - \`GET /api/workspaces/{id}/exec-audit/sessions\`
  - \`GET /api/workspaces/{id}/exec-audit/sessions/{session_id}\`
  - \`GET /api/workspaces/{id}/exec-audit/calls\`
  - \`GET /api/workspaces/{id}/exec-audit/calls/{call_id}\`
  - \`GET /api/workspaces/{id}/exec-audit/calls/{call_id}/payload?side=request|response\`
- Internal cross-tenant mirrors for ops queries
- Hourly retention loop (\`AGENTSERVER_EXEC_AUDIT_RETENTION_DAYS\`, default 90)

Until Plan 2b lands (gateway-side WAL + uploader), no rows are written; the read API returns empty for every workspace. Independently merge-able.

## Why now

Replaces the legacy operations subsystem deleted in #198. See \`docs/superpowers/specs/2026-05-23-codex-exec-gateway-audit-design.md\`.

## Test plan

- [x] \`make test\` green (new DAL + handler + retention unit tests)
- [x] \`make build\` green
- [x] \`make openapi\` regenerated spec includes the new endpoints + types
- [ ] Post-deploy: tables exist (\\\`\\d exec_audit_sessions\\\` returns the schema)
- [ ] Post-deploy: \`GET /api/workspaces/{id}/exec-audit/sessions\` returns \`{"sessions":[]}\` (200 OK, empty)

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)" 2>&1 | tail
```

- [ ] **Step 3: Report PR URL.**
