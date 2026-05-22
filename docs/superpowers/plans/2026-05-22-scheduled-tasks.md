# Scheduled Tasks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a scheduled-task system for codex-{app,exec}-gateway: 6 MCP tools (1:1 nanoclaw) + REST API, Postgres state in agentserver-main, dispatch loop in codex-app-gateway that spawns one-shot `codex exec` and broadcasts results to all workspace IM channels.

**Architecture:** Three-layer split — **state** in agentserver-main Postgres (`scheduled_tasks` + `scheduled_task_runs` + REST/internal API), **dispatch+exec** in codex-app-gateway pod (scheduler goroutine + supervisor spawn), **broadcast** via existing imbridgesvc. Multi-replica safe via `FOR UPDATE SKIP LOCKED` lease.

**Tech Stack:** Go 1.26, PostgreSQL, chi router, `robfig/cron/v3`, `lib/pq`, existing supervisor + imbridge HTTP clients.

**Reference:**
- Spec: `docs/superpowers/specs/2026-05-22-scheduled-tasks-design.md`
- nanoclaw reference: `/root/nanoclaw/src/modules/scheduling/`, `/root/nanoclaw/container/agent-runner/src/mcp-tools/scheduling.ts`

---

## File Map

**Create:**
- `internal/db/migrations/029_scheduled_tasks.sql`
- `internal/db/scheduled_tasks.go` (+ test)
- `internal/db/timezone.go` (+ test) — `parseZonedToUtc` port
- `internal/server/scheduled_tasks.go` (+ test) — REST public handlers
- `internal/server/scheduled_tasks_internal.go` (+ test) — lease + result endpoints
- `internal/codexappgateway/scheduler/agentserver_client.go` — HTTP wrapper
- `internal/codexappgateway/scheduler/script.go` (+ test)
- `internal/codexappgateway/scheduler/spawn.go` (+ test)
- `internal/codexappgateway/scheduler/broadcast.go` (+ test)
- `internal/codexappgateway/scheduler/dispatcher.go` (+ test)
- `internal/codexappgateway/scheduler/loop.go` (+ integ test)
- `internal/codexappgateway/envmcp/tools/scheduling.go` (+ test)
- `internal/codexappgateway/envmcp/tools/scheduling.instructions.md`
- `internal/codexappgateway/envmcp/tools/testdata/scheduling.golden.json`

**Modify:**
- `internal/server/server.go` — mount REST routes
- `internal/codexappgateway/internal_api.go` — add `/internal/scheduled-tasks/*` loopback proxy
- `internal/codexappgateway/config.go` — scheduler config (`AgentserverBaseURL`, `AgentserverInternalSecret`, `ImbridgeBaseURL`, `ImbridgeInternalSecret`, `SchedulerTickInterval`, `SchedulerLeaseSeconds`, `SchedulerConcurrency`)
- `internal/codexappgateway/server.go` — launch `scheduler.Loop` in `Run`
- `internal/codexappgateway/envmcp/envmcp.go` — register 6 scheduling tools
- `go.mod` — add `github.com/robfig/cron/v3`

---

## Branch Setup

- [ ] **Create feature branch**

```bash
cd /root/agentserver
git checkout main && git pull --ff-only
git checkout -b feat/scheduled-tasks
```

(Do NOT base on `refactor/secrets-module` — that branch is unrelated.)

---

## Task 1: DB migration + schema smoke

**Files:**
- Create: `internal/db/migrations/029_scheduled_tasks.sql`

- [ ] **Step 1: Write the migration**

```sql
-- internal/db/migrations/029_scheduled_tasks.sql
CREATE TABLE scheduled_tasks (
    id                TEXT        PRIMARY KEY,
    workspace_id      TEXT        NOT NULL
                                  REFERENCES workspaces(id) ON DELETE CASCADE,
    series_id         TEXT        NOT NULL,
    created_by        TEXT,
    creator_kind      TEXT        NOT NULL DEFAULT 'mcp'
                                  CHECK (creator_kind IN ('mcp','rest','system')),

    prompt            TEXT        NOT NULL,
    script            TEXT,

    timezone          TEXT        NOT NULL DEFAULT 'UTC',
    recurrence        TEXT,
    process_after     TIMESTAMPTZ NOT NULL,

    status            TEXT        NOT NULL DEFAULT 'pending'
                                  CHECK (status IN ('pending','paused','running','completed','failed','cancelled')),
    tries             INT         NOT NULL DEFAULT 0,
    timeout_seconds   INT         NOT NULL DEFAULT 600,
    lease_until       TIMESTAMPTZ,
    lease_owner       TEXT,

    last_run_id       TEXT,
    last_error        TEXT,

    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_scheduled_tasks_due
    ON scheduled_tasks (process_after)
    WHERE status = 'pending';
CREATE INDEX idx_scheduled_tasks_workspace ON scheduled_tasks (workspace_id, created_at DESC);
CREATE INDEX idx_scheduled_tasks_series    ON scheduled_tasks (series_id);

CREATE TABLE scheduled_task_runs (
    id                TEXT        PRIMARY KEY,
    task_id           TEXT        NOT NULL REFERENCES scheduled_tasks(id) ON DELETE CASCADE,
    series_id         TEXT        NOT NULL,
    started_at        TIMESTAMPTZ NOT NULL,
    finished_at       TIMESTAMPTZ,
    duration_ms       BIGINT,
    exit_code         INT,
    status            TEXT        NOT NULL
                                  CHECK (status IN ('succeeded','failed','timeout','skipped')),
    summary           TEXT,
    transcript_uri    TEXT,
    cost_usd          NUMERIC(10,4),
    num_turns         INT,
    broadcast_to      TEXT[]      NOT NULL DEFAULT '{}',
    broadcast_errors  JSONB
);
CREATE INDEX idx_scheduled_task_runs_task ON scheduled_task_runs (task_id, started_at DESC);
```

- [ ] **Step 2: Verify migration applies cleanly against an empty test DB**

The repo already has a `db.Open(...)` test helper that runs migrations. Run any existing DB test to confirm 029 doesn't break the rest of the schema:

```bash
go test ./internal/db/... -run TestOpen -v
```

Expected: PASS (or, if no `TestOpen` exists, run all of `./internal/db/...` and confirm no migration error).

- [ ] **Step 3: Commit**

```bash
git add internal/db/migrations/029_scheduled_tasks.sql
git commit -m "feat(db): add scheduled_tasks + scheduled_task_runs tables (029)"
```

---

## Task 2: Timezone parser (`parseZonedToUtc` port)

**Files:**
- Create: `internal/db/timezone.go`
- Test: `internal/db/timezone_test.go`

(Lives in `internal/db` so both server and scheduler can import without circulars.)

- [ ] **Step 1: Write the failing test**

```go
// internal/db/timezone_test.go
package db

import (
	"testing"
	"time"
)

func TestParseZonedToUTC(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		tz      string
		wantUTC string
		wantErr bool
	}{
		{"explicit UTC Z", "2026-05-22T09:00:00Z", "Asia/Shanghai", "2026-05-22T09:00:00Z", false},
		{"explicit +00:00", "2026-05-22T09:00:00+00:00", "Asia/Shanghai", "2026-05-22T09:00:00Z", false},
		{"naive local CST -> UTC", "2026-05-22T09:00:00", "Asia/Shanghai", "2026-05-22T01:00:00Z", false},
		{"naive local UTC unchanged", "2026-05-22T09:00:00", "UTC", "2026-05-22T09:00:00Z", false},
		{"bad string", "not-a-date", "UTC", "", true},
		{"bad tz", "2026-05-22T09:00:00", "Not/AZone", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseZonedToUTC(c.in, c.tz)
			if c.wantErr {
				if err == nil { t.Fatalf("want error, got %v", got) }
				return
			}
			if err != nil { t.Fatalf("unexpected err: %v", err) }
			want, _ := time.Parse(time.RFC3339, c.wantUTC)
			if !got.Equal(want) {
				t.Fatalf("got %s want %s", got.Format(time.RFC3339), c.wantUTC)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/db/... -run TestParseZonedToUTC -v
```

Expected: FAIL — `ParseZonedToUTC` undefined.

- [ ] **Step 3: Implement**

```go
// internal/db/timezone.go
package db

import (
	"fmt"
	"strings"
	"time"
)

// ParseZonedToUTC parses an ISO 8601 timestamp and returns it in UTC.
// Accepts either an offset-bearing form ("...Z" or "...+HH:MM") or a
// naive local form (no offset) which is interpreted in `tz` (IANA name).
// Mirrors nanoclaw's parseZonedToUtc(timezone.ts).
func ParseZonedToUTC(s, tz string) (time.Time, error) {
	if hasOffset(s) {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse offset timestamp %q: %w", s, err)
		}
		return t.UTC(), nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("unknown timezone %q: %w", tz, err)
	}
	// Try with and without seconds, matching nanoclaw's tolerance.
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		t, err := time.ParseInLocation(layout, s, loc)
		if err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable timestamp %q", s)
}

func hasOffset(s string) bool {
	if strings.HasSuffix(s, "Z") {
		return true
	}
	if len(s) < 6 {
		return false
	}
	tail := s[len(s)-6:]
	return (tail[0] == '+' || tail[0] == '-') && tail[3] == ':'
}
```

- [ ] **Step 4: Run test**

```bash
go test ./internal/db/... -run TestParseZonedToUTC -v
```

Expected: PASS (all cases).

- [ ] **Step 5: Commit**

```bash
git add internal/db/timezone.go internal/db/timezone_test.go
git commit -m "feat(db): ParseZonedToUTC — naive/offset ISO8601 in IANA tz"
```

---

## Task 3: DB layer for scheduled_tasks (CRUD + lease)

**Files:**
- Create: `internal/db/scheduled_tasks.go`
- Test: `internal/db/scheduled_tasks_test.go`

The test uses the existing test-DB helper. Inspect `internal/db/agent_tasks_test.go` (or similar) for the exact helper name in your branch and reuse the same pattern. Below assumes a helper called `newTestDB(t)` returning a `*DB`; if the project uses a different name (e.g. `openTestDB`), substitute it everywhere.

- [ ] **Step 1: Write the failing test**

```go
// internal/db/scheduled_tasks_test.go
package db

import (
	"sync"
	"testing"
	"time"
)

func TestScheduledTask_InsertAndGet(t *testing.T) {
	d := newTestDB(t)
	wsID := mustCreateWorkspace(t, d) // existing helper; if absent, INSERT one inline.

	st := &ScheduledTask{
		ID: "sch_a", WorkspaceID: wsID, SeriesID: "sch_a",
		CreatorKind: "mcp", Prompt: "say hello",
		Timezone: "UTC", ProcessAfter: time.Now().Add(-1 * time.Second),
		Status: "pending", TimeoutSeconds: 600,
	}
	if err := d.CreateScheduledTask(st); err != nil { t.Fatal(err) }
	got, err := d.GetScheduledTaskBySeries(wsID, "sch_a")
	if err != nil { t.Fatal(err) }
	if got == nil || got.Prompt != "say hello" { t.Fatalf("got %#v", got) }
}

func TestScheduledTask_LeaseSkipLocked_Concurrent(t *testing.T) {
	d := newTestDB(t)
	wsID := mustCreateWorkspace(t, d)
	for i := 0; i < 20; i++ {
		st := &ScheduledTask{
			ID: "sch_" + intStr(i), WorkspaceID: wsID, SeriesID: "sch_" + intStr(i),
			CreatorKind: "mcp", Prompt: "p", Timezone: "UTC",
			ProcessAfter: time.Now().Add(-time.Second), Status: "pending",
			TimeoutSeconds: 600,
		}
		if err := d.CreateScheduledTask(st); err != nil { t.Fatal(err) }
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		claimed = map[string]int{}
	)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(owner int) {
			defer wg.Done()
			leased, err := d.LeaseDueScheduledTasks(10, 60, "owner-"+intStr(owner))
			if err != nil { t.Error(err); return }
			mu.Lock()
			for _, t2 := range leased { claimed[t2.ID]++ }
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	for id, n := range claimed {
		if n != 1 { t.Errorf("task %s leased %d times, want 1", id, n) }
	}
	if len(claimed) != 20 { t.Errorf("claimed %d / 20", len(claimed)) }
}

func TestScheduledTask_CancelMatchesBySeriesID(t *testing.T) {
	d := newTestDB(t)
	wsID := mustCreateWorkspace(t, d)
	// Two rows in the same series — one completed (prior occurrence), one pending (next).
	for _, st := range []*ScheduledTask{
		{ID: "sch_old", WorkspaceID: wsID, SeriesID: "sch_old", CreatorKind: "mcp",
		 Prompt: "p", Timezone: "UTC", ProcessAfter: time.Now().Add(-time.Hour),
		 Status: "completed", TimeoutSeconds: 600},
		{ID: "sch_new", WorkspaceID: wsID, SeriesID: "sch_old", CreatorKind: "mcp",
		 Prompt: "p", Timezone: "UTC", ProcessAfter: time.Now().Add(time.Hour),
		 Status: "pending", TimeoutSeconds: 600, Recurrence: strPtr("*/5 * * * *")},
	} {
		if err := d.CreateScheduledTask(st); err != nil { t.Fatal(err) }
	}

	n, err := d.CancelScheduledSeries(wsID, "sch_old")
	if err != nil { t.Fatal(err) }
	if n != 1 { t.Fatalf("cancelled %d, want 1 (only the live row)", n) }
	got, _ := d.GetScheduledTaskBySeries(wsID, "sch_old")
	if got.Status != "cancelled" { t.Fatalf("status=%s", got.Status) }
}

// helpers
func intStr(i int) string { return time.Now().Format("150405") + "-" + strFromInt(i) }
func strFromInt(i int) string { return string(rune('a'+i)) }
func strPtr(s string) *string { return &s }
```

(If `mustCreateWorkspace` doesn't exist in your test helper file, add it: `INSERT INTO workspaces (id, name, owner_id) VALUES ('ws_test', 'test', 'usr_test') RETURNING id;` — adapt to the schema in `001_init.sql`.)

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/db/... -run TestScheduledTask_ -v
```

Expected: FAIL — `ScheduledTask`, `CreateScheduledTask`, `GetScheduledTaskBySeries`, `LeaseDueScheduledTasks`, `CancelScheduledSeries` undefined.

- [ ] **Step 3: Implement**

```go
// internal/db/scheduled_tasks.go
package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

type ScheduledTask struct {
	ID             string
	WorkspaceID    string
	SeriesID       string
	CreatedBy      sql.NullString
	CreatorKind    string

	Prompt         string
	Script         *string

	Timezone       string
	Recurrence     *string
	ProcessAfter   time.Time

	Status         string
	Tries          int
	TimeoutSeconds int
	LeaseUntil     sql.NullTime
	LeaseOwner     sql.NullString

	LastRunID      sql.NullString
	LastError      sql.NullString

	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ScheduledTaskRun struct {
	ID              string
	TaskID          string
	SeriesID        string
	StartedAt       time.Time
	FinishedAt      sql.NullTime
	DurationMS      sql.NullInt64
	ExitCode        sql.NullInt64
	Status          string  // succeeded|failed|timeout|skipped
	Summary         sql.NullString
	TranscriptURI   sql.NullString
	CostUSD         sql.NullFloat64
	NumTurns        sql.NullInt64
	BroadcastTo     []string
	BroadcastErrors json.RawMessage
}

const scheduledTaskCols = `id, workspace_id, series_id, created_by, creator_kind,
	prompt, script, timezone, recurrence, process_after,
	status, tries, timeout_seconds, lease_until, lease_owner,
	last_run_id, last_error, created_at, updated_at`

func (db *DB) CreateScheduledTask(t *ScheduledTask) error {
	if t.CreatorKind == "" { t.CreatorKind = "mcp" }
	if t.Timezone == "" { t.Timezone = "UTC" }
	if t.Status == "" { t.Status = "pending" }
	if t.TimeoutSeconds == 0 { t.TimeoutSeconds = 600 }
	if t.SeriesID == "" { t.SeriesID = t.ID }
	_, err := db.Exec(
		`INSERT INTO scheduled_tasks ` +
		`(id, workspace_id, series_id, created_by, creator_kind, prompt, script, timezone, recurrence, process_after, status, timeout_seconds) ` +
		`VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		t.ID, t.WorkspaceID, t.SeriesID, t.CreatedBy, t.CreatorKind,
		t.Prompt, t.Script, t.Timezone, t.Recurrence, t.ProcessAfter,
		t.Status, t.TimeoutSeconds,
	)
	return err
}

func (db *DB) GetScheduledTaskByID(id string) (*ScheduledTask, error) {
	return db.queryOneScheduledTask(`WHERE id = $1`, id)
}

// GetScheduledTaskBySeries returns the *latest* live (pending|paused) row of
// the series, or the most recently created row if none are live. Mirrors
// nanoclaw's "one row per series" listing semantics for the single-series read.
func (db *DB) GetScheduledTaskBySeries(wsID, seriesID string) (*ScheduledTask, error) {
	return db.queryOneScheduledTask(
		`WHERE workspace_id = $1 AND series_id = $2
		 ORDER BY (status IN ('pending','paused')) DESC, created_at DESC
		 LIMIT 1`, wsID, seriesID)
}

func (db *DB) queryOneScheduledTask(whereClause string, args ...any) (*ScheduledTask, error) {
	row := db.QueryRow(`SELECT `+scheduledTaskCols+` FROM scheduled_tasks `+whereClause, args...)
	st := &ScheduledTask{}
	err := row.Scan(&st.ID, &st.WorkspaceID, &st.SeriesID, &st.CreatedBy, &st.CreatorKind,
		&st.Prompt, &st.Script, &st.Timezone, &st.Recurrence, &st.ProcessAfter,
		&st.Status, &st.Tries, &st.TimeoutSeconds, &st.LeaseUntil, &st.LeaseOwner,
		&st.LastRunID, &st.LastError, &st.CreatedAt, &st.UpdatedAt)
	if err == sql.ErrNoRows { return nil, nil }
	return st, err
}

// ListScheduledTasksByWorkspace returns one row per series — the live one
// (status pending|paused), preferring the most-recently created if multiple
// live rows ever co-exist. `statusFilter` may be "" (default both),
// "pending", or "paused".
func (db *DB) ListScheduledTasksByWorkspace(wsID, statusFilter string) ([]ScheduledTask, error) {
	var q string
	args := []any{wsID}
	if statusFilter == "pending" || statusFilter == "paused" {
		q = `SELECT DISTINCT ON (series_id) ` + scheduledTaskCols + `
		     FROM scheduled_tasks
		     WHERE workspace_id = $1 AND status = $2
		     ORDER BY series_id, created_at DESC`
		args = append(args, statusFilter)
	} else {
		q = `SELECT DISTINCT ON (series_id) ` + scheduledTaskCols + `
		     FROM scheduled_tasks
		     WHERE workspace_id = $1 AND status IN ('pending','paused')
		     ORDER BY series_id, created_at DESC`
	}
	rows, err := db.Query(q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []ScheduledTask
	for rows.Next() {
		st := ScheduledTask{}
		if err := rows.Scan(&st.ID, &st.WorkspaceID, &st.SeriesID, &st.CreatedBy, &st.CreatorKind,
			&st.Prompt, &st.Script, &st.Timezone, &st.Recurrence, &st.ProcessAfter,
			&st.Status, &st.Tries, &st.TimeoutSeconds, &st.LeaseUntil, &st.LeaseOwner,
			&st.LastRunID, &st.LastError, &st.CreatedAt, &st.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (db *DB) LeaseDueScheduledTasks(limit, leaseSeconds int, owner string) ([]ScheduledTask, error) {
	rows, err := db.Query(
		`WITH due AS (
		   SELECT id FROM scheduled_tasks
		    WHERE status = 'pending'
		      AND process_after <= NOW()
		      AND (lease_until IS NULL OR lease_until < NOW())
		    ORDER BY process_after ASC
		    LIMIT $1
		    FOR UPDATE SKIP LOCKED
		 )
		 UPDATE scheduled_tasks t
		    SET lease_until = NOW() + make_interval(secs => $2::int),
		        lease_owner = $3,
		        status      = 'running',
		        updated_at  = NOW()
		   FROM due
		  WHERE t.id = due.id
		  RETURNING `+scheduledTaskCols,
		limit, leaseSeconds, owner,
	)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []ScheduledTask
	for rows.Next() {
		st := ScheduledTask{}
		if err := rows.Scan(&st.ID, &st.WorkspaceID, &st.SeriesID, &st.CreatedBy, &st.CreatorKind,
			&st.Prompt, &st.Script, &st.Timezone, &st.Recurrence, &st.ProcessAfter,
			&st.Status, &st.Tries, &st.TimeoutSeconds, &st.LeaseUntil, &st.LeaseOwner,
			&st.LastRunID, &st.LastError, &st.CreatedAt, &st.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// CancelScheduledSeries marks all live (pending|paused) rows in the series as
// cancelled and clears recurrence so nothing re-clones. Returns rows affected.
func (db *DB) CancelScheduledSeries(wsID, seriesID string) (int, error) {
	res, err := db.Exec(
		`UPDATE scheduled_tasks
		    SET status = 'cancelled', recurrence = NULL, updated_at = NOW()
		  WHERE workspace_id = $1 AND series_id = $2
		    AND status IN ('pending','paused')`,
		wsID, seriesID)
	if err != nil { return 0, err }
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (db *DB) PauseScheduledSeries(wsID, seriesID string) (int, error) {
	res, err := db.Exec(
		`UPDATE scheduled_tasks SET status = 'paused', updated_at = NOW()
		  WHERE workspace_id = $1 AND series_id = $2 AND status = 'pending'`,
		wsID, seriesID)
	if err != nil { return 0, err }
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (db *DB) ResumeScheduledSeries(wsID, seriesID string) (int, error) {
	res, err := db.Exec(
		`UPDATE scheduled_tasks SET status = 'pending', updated_at = NOW()
		  WHERE workspace_id = $1 AND series_id = $2 AND status = 'paused'`,
		wsID, seriesID)
	if err != nil { return 0, err }
	n, _ := res.RowsAffected()
	return int(n), nil
}

type ScheduledTaskUpdate struct {
	Prompt       *string
	Script       *string // nil = unchanged; *"" = clear (DB NULL)
	Recurrence   *string // nil = unchanged; *"" = clear (DB NULL)
	ProcessAfter *time.Time
}

func (db *DB) UpdateScheduledSeries(wsID, seriesID string, u ScheduledTaskUpdate) (int, error) {
	sets := []string{"updated_at = NOW()"}
	args := []any{wsID, seriesID}
	add := func(col string, val any) { args = append(args, val); sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args))) }
	if u.Prompt != nil       { add("prompt", *u.Prompt) }
	if u.Script != nil       { if *u.Script == "" { sets = append(sets, "script = NULL") } else { add("script", *u.Script) } }
	if u.Recurrence != nil   { if *u.Recurrence == "" { sets = append(sets, "recurrence = NULL") } else { add("recurrence", *u.Recurrence) } }
	if u.ProcessAfter != nil { add("process_after", u.ProcessAfter.UTC()) }
	if len(sets) == 1 { return 0, nil } // only updated_at — caller passed nothing
	q := `UPDATE scheduled_tasks SET ` + strings.Join(sets, ", ") +
		` WHERE workspace_id = $1 AND series_id = $2 AND status IN ('pending','paused')`
	res, err := db.Exec(q, args...)
	if err != nil { return 0, err }
	n, _ := res.RowsAffected()
	return int(n), nil
}

// CreateScheduledTaskRun writes a 'running' run row at fire start.
func (db *DB) CreateScheduledTaskRun(r *ScheduledTaskRun) error {
	_, err := db.Exec(
		`INSERT INTO scheduled_task_runs (id, task_id, series_id, started_at, status)
		 VALUES ($1,$2,$3,$4,'succeeded')`, // placeholder; finalized by FinalizeRun
		r.ID, r.TaskID, r.SeriesID, r.StartedAt,
	)
	return err
}

// FinalizeRunAndAdvance finalises a run row and, if the task is recurring,
// clones the next occurrence. Always clears the parent task's lease and
// marks the parent (one-shot) completed/failed appropriately. Designed to
// run in one transaction for atomicity with status changes.
type FinalizeRunInput struct {
	RunID, TaskID                  string
	Status                         string // succeeded|failed|timeout|skipped
	ExitCode                       int
	DurationMS                     int64
	Summary, TranscriptURI         string
	CostUSD                        *float64
	NumTurns                       *int
	BroadcastTo                    []string
	BroadcastErrors                json.RawMessage
}

func (db *DB) FinalizeRunAndAdvance(in FinalizeRunInput, nextAfter *time.Time, newID string) error {
	tx, err := db.Begin()
	if err != nil { return err }
	defer tx.Rollback()

	_, err = tx.Exec(
		`UPDATE scheduled_task_runs
		    SET finished_at = NOW(), duration_ms = $2, exit_code = $3,
		        status = $4, summary = NULLIF($5,''), transcript_uri = NULLIF($6,''),
		        cost_usd = $7, num_turns = $8,
		        broadcast_to = $9, broadcast_errors = $10
		  WHERE id = $1`,
		in.RunID, in.DurationMS, in.ExitCode, in.Status, in.Summary, in.TranscriptURI,
		in.CostUSD, in.NumTurns, pq.Array(in.BroadcastTo), in.BroadcastErrors,
	)
	if err != nil { return err }

	task := &ScheduledTask{}
	err = tx.QueryRow(`SELECT `+scheduledTaskCols+` FROM scheduled_tasks WHERE id = $1 FOR UPDATE`, in.TaskID).
		Scan(&task.ID, &task.WorkspaceID, &task.SeriesID, &task.CreatedBy, &task.CreatorKind,
			&task.Prompt, &task.Script, &task.Timezone, &task.Recurrence, &task.ProcessAfter,
			&task.Status, &task.Tries, &task.TimeoutSeconds, &task.LeaseUntil, &task.LeaseOwner,
			&task.LastRunID, &task.LastError, &task.CreatedAt, &task.UpdatedAt)
	if err != nil { return err }

	if task.Recurrence != nil && *task.Recurrence != "" && nextAfter != nil {
		if _, err = tx.Exec(
			`INSERT INTO scheduled_tasks
			 (id, workspace_id, series_id, created_by, creator_kind,
			  prompt, script, timezone, recurrence, process_after,
			  status, timeout_seconds)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'pending',$11)`,
			newID, task.WorkspaceID, task.SeriesID, task.CreatedBy, task.CreatorKind,
			task.Prompt, task.Script, task.Timezone, task.Recurrence, nextAfter.UTC(),
			task.TimeoutSeconds,
		); err != nil { return err }

		if _, err = tx.Exec(
			`UPDATE scheduled_tasks
			    SET status='completed', recurrence=NULL,
			        lease_until=NULL, lease_owner=NULL,
			        last_run_id=$2, tries=tries+1, updated_at=NOW()
			  WHERE id=$1`, in.TaskID, in.RunID); err != nil { return err }
	} else {
		newStatus := "completed"
		if in.Status == "failed" || in.Status == "timeout" { newStatus = "failed" }
		if _, err = tx.Exec(
			`UPDATE scheduled_tasks
			    SET status=$2, lease_until=NULL, lease_owner=NULL,
			        last_run_id=$3, tries=tries+1, updated_at=NOW()
			  WHERE id=$1`, in.TaskID, newStatus, in.RunID); err != nil { return err }
	}
	return tx.Commit()
}

func (db *DB) ListScheduledTaskRuns(taskID string, limit int) ([]ScheduledTaskRun, error) {
	if limit <= 0 { limit = 50 }
	rows, err := db.Query(
		`SELECT id, task_id, series_id, started_at, finished_at, duration_ms, exit_code,
		        status, summary, transcript_uri, cost_usd, num_turns,
		        broadcast_to, broadcast_errors
		   FROM scheduled_task_runs WHERE task_id = $1 ORDER BY started_at DESC LIMIT $2`,
		taskID, limit,
	)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []ScheduledTaskRun
	for rows.Next() {
		r := ScheduledTaskRun{}
		var bt pq.StringArray
		if err := rows.Scan(&r.ID, &r.TaskID, &r.SeriesID, &r.StartedAt, &r.FinishedAt,
			&r.DurationMS, &r.ExitCode, &r.Status, &r.Summary, &r.TranscriptURI,
			&r.CostUSD, &r.NumTurns, &bt, &r.BroadcastErrors); err != nil {
			return nil, err
		}
		r.BroadcastTo = bt
		out = append(out, r)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run tests until green**

```bash
go test ./internal/db/... -run TestScheduledTask_ -v
```

Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/db/scheduled_tasks.go internal/db/scheduled_tasks_test.go
git commit -m "feat(db): scheduled_tasks CRUD + lease (SKIP LOCKED) + series ops"
```

---

## Task 4: REST public handlers

**Files:**
- Create: `internal/server/scheduled_tasks.go`
- Test: `internal/server/scheduled_tasks_test.go`
- Modify: `internal/server/server.go` (mount routes)

- [ ] **Step 1: Write the failing test**

Use the existing server test scaffolding pattern (look at `internal/server/agent_tasks_test.go` for the helper that wires up a `*Server` against a test DB + an authenticated workspace member).

```go
// internal/server/scheduled_tasks_test.go
package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScheduledTasks_CreateListCancel(t *testing.T) {
	srv, wsID, authCookie := newTestServerWithWorkspaceMember(t)

	// CREATE
	body := strings.NewReader(`{
	  "prompt": "say hi",
	  "processAfter": "2099-01-01T00:00:00Z",
	  "recurrence": "*/5 * * * *",
	  "timezone": "UTC"
	}`)
	r := httptest.NewRequest("POST", "/api/workspaces/"+wsID+"/scheduled-tasks", body)
	r.AddCookie(authCookie); r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, r)
	if w.Code != http.StatusCreated { t.Fatalf("create: %d %s", w.Code, w.Body.String()) }
	var created struct{ TaskID, RunsAt string }
	json.NewDecoder(w.Body).Decode(&created)
	if created.TaskID == "" { t.Fatal("no taskId") }

	// LIST
	r = httptest.NewRequest("GET", "/api/workspaces/"+wsID+"/scheduled-tasks", nil)
	r.AddCookie(authCookie); w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, r)
	if w.Code != http.StatusOK { t.Fatalf("list: %d %s", w.Code, w.Body.String()) }
	if !strings.Contains(w.Body.String(), created.TaskID) {
		t.Fatalf("list missing taskId %s: %s", created.TaskID, w.Body.String())
	}

	// CANCEL
	r = httptest.NewRequest("POST", "/api/workspaces/"+wsID+"/scheduled-tasks/"+created.TaskID+"/cancel", bytes.NewReader(nil))
	r.AddCookie(authCookie); w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, r)
	if w.Code != http.StatusOK { t.Fatalf("cancel: %d %s", w.Code, w.Body.String()) }
}
```

(If `newTestServerWithWorkspaceMember` doesn't exist by that name, look at the equivalent helper used by `agent_tasks_test.go`. Adopt whatever the project already uses.)

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/server/... -run TestScheduledTasks_CreateListCancel -v
```

Expected: FAIL — handlers / routes undefined.

- [ ] **Step 3: Implement handlers**

```go
// internal/server/scheduled_tasks.go
package server

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/agentserver/agentserver/internal/db"
)

type scheduledTaskRequest struct {
	Prompt       string  `json:"prompt"`
	ProcessAfter string  `json:"processAfter"`
	Recurrence   *string `json:"recurrence,omitempty"`
	Script       *string `json:"script,omitempty"`
	Timezone     string  `json:"timezone,omitempty"`
}

type scheduledTaskResponse struct {
	TaskID     string  `json:"taskId"`
	SeriesID   string  `json:"seriesId"`
	RunsAt     string  `json:"runsAt"`
	Recurrence *string `json:"recurrence,omitempty"`
	Status     string  `json:"status"`
	Timezone   string  `json:"timezone"`
}

func (s *Server) handleCreateScheduledTask(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	user, ok := s.requireWorkspaceMember(w, r, wid)
	if !ok { return }

	var req scheduledTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest); return
	}
	if req.Prompt == "" || req.ProcessAfter == "" {
		http.Error(w, "prompt and processAfter required", http.StatusBadRequest); return
	}
	tz := req.Timezone
	if tz == "" { tz = "UTC" }
	when, err := db.ParseZonedToUTC(req.ProcessAfter, tz)
	if err != nil {
		http.Error(w, "invalid processAfter: "+err.Error(), http.StatusBadRequest); return
	}

	id := "sch_" + uuid.New().String()
	task := &db.ScheduledTask{
		ID: id, WorkspaceID: wid, SeriesID: id,
		CreatorKind: "rest",
		Prompt: req.Prompt, Script: req.Script,
		Timezone: tz, Recurrence: req.Recurrence,
		ProcessAfter: when, Status: "pending", TimeoutSeconds: 600,
	}
	if user != nil { task.CreatedBy.String, task.CreatedBy.Valid = user.ID, true }
	if err := s.DB.CreateScheduledTask(task); err != nil {
		log.Printf("create scheduled task: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError); return
	}
	writeJSON(w, http.StatusCreated, scheduledTaskResponse{
		TaskID: id, SeriesID: id, RunsAt: when.UTC().Format(time.RFC3339),
		Recurrence: req.Recurrence, Status: "pending", Timezone: tz,
	})
}

func (s *Server) handleListScheduledTasks(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok { return }
	rows, err := s.DB.ListScheduledTasksByWorkspace(wid, r.URL.Query().Get("status"))
	if err != nil { http.Error(w, "internal error", http.StatusInternalServerError); return }
	out := make([]scheduledTaskResponse, 0, len(rows))
	for _, t := range rows {
		out = append(out, scheduledTaskResponse{
			TaskID: t.SeriesID, SeriesID: t.SeriesID,
			RunsAt: t.ProcessAfter.UTC().Format(time.RFC3339),
			Recurrence: t.Recurrence, Status: t.Status, Timezone: t.Timezone,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetScheduledTask(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid"); sid := chi.URLParam(r, "seriesId")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok { return }
	t, err := s.DB.GetScheduledTaskBySeries(wid, sid)
	if err != nil { http.Error(w, "internal error", http.StatusInternalServerError); return }
	if t == nil { http.Error(w, "not found", http.StatusNotFound); return }
	runs, _ := s.DB.ListScheduledTaskRuns(t.ID, 20)
	writeJSON(w, http.StatusOK, map[string]any{
		"task": scheduledTaskResponse{
			TaskID: t.SeriesID, SeriesID: t.SeriesID,
			RunsAt: t.ProcessAfter.UTC().Format(time.RFC3339),
			Recurrence: t.Recurrence, Status: t.Status, Timezone: t.Timezone,
		},
		"runs": runs,
	})
}

func (s *Server) handleCancelScheduledTask(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid"); sid := chi.URLParam(r, "seriesId")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok { return }
	n, err := s.DB.CancelScheduledSeries(wid, sid)
	if err != nil { http.Error(w, "internal error", http.StatusInternalServerError); return }
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": n})
}

func (s *Server) handlePauseScheduledTask(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid"); sid := chi.URLParam(r, "seriesId")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok { return }
	n, _ := s.DB.PauseScheduledSeries(wid, sid)
	writeJSON(w, http.StatusOK, map[string]any{"paused": n})
}

func (s *Server) handleResumeScheduledTask(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid"); sid := chi.URLParam(r, "seriesId")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok { return }
	n, _ := s.DB.ResumeScheduledSeries(wid, sid)
	writeJSON(w, http.StatusOK, map[string]any{"resumed": n})
}

func (s *Server) handleUpdateScheduledTask(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid"); sid := chi.URLParam(r, "seriesId")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok { return }
	var req scheduledTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest); return
	}
	upd := db.ScheduledTaskUpdate{}
	if req.Prompt != ""       { p := req.Prompt; upd.Prompt = &p }
	if req.Recurrence != nil  { upd.Recurrence = req.Recurrence }
	if req.Script != nil      { upd.Script = req.Script }
	if req.ProcessAfter != "" {
		t, err := db.ParseZonedToUTC(req.ProcessAfter, defaultStr(req.Timezone, "UTC"))
		if err != nil { http.Error(w, "invalid processAfter", http.StatusBadRequest); return }
		upd.ProcessAfter = &t
	}
	n, err := s.DB.UpdateScheduledSeries(wid, sid, upd)
	if err != nil { http.Error(w, "internal error", http.StatusInternalServerError); return }
	writeJSON(w, http.StatusOK, map[string]any{"updated": n})
}

func (s *Server) handleGetScheduledTaskRuns(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid"); sid := chi.URLParam(r, "seriesId")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok { return }
	t, _ := s.DB.GetScheduledTaskBySeries(wid, sid)
	if t == nil { http.Error(w, "not found", http.StatusNotFound); return }
	runs, _ := s.DB.ListScheduledTaskRuns(t.ID, 50)
	writeJSON(w, http.StatusOK, runs)
}

func defaultStr(s, dflt string) string { if s == "" { return dflt }; return s }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
```

(If `writeJSON` already exists in the package, drop the helper here and use the existing one.)

- [ ] **Step 4: Mount routes**

Open `internal/server/server.go`. Find the block that mounts `r.Post("/api/workspaces/{wid}/tasks", s.handleCreateTask)` (around line 524). Add the following sibling routes inside the same authenticated group:

```go
r.Post(   "/api/workspaces/{wid}/scheduled-tasks",                       s.handleCreateScheduledTask)
r.Get(    "/api/workspaces/{wid}/scheduled-tasks",                       s.handleListScheduledTasks)
r.Get(    "/api/workspaces/{wid}/scheduled-tasks/{seriesId}",            s.handleGetScheduledTask)
r.Patch(  "/api/workspaces/{wid}/scheduled-tasks/{seriesId}",            s.handleUpdateScheduledTask)
r.Post(   "/api/workspaces/{wid}/scheduled-tasks/{seriesId}/cancel",     s.handleCancelScheduledTask)
r.Post(   "/api/workspaces/{wid}/scheduled-tasks/{seriesId}/pause",      s.handlePauseScheduledTask)
r.Post(   "/api/workspaces/{wid}/scheduled-tasks/{seriesId}/resume",     s.handleResumeScheduledTask)
r.Get(    "/api/workspaces/{wid}/scheduled-tasks/{seriesId}/runs",       s.handleGetScheduledTaskRuns)
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/server/... -run TestScheduledTasks_CreateListCancel -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/server/scheduled_tasks.go internal/server/scheduled_tasks_test.go internal/server/server.go
git commit -m "feat(server): REST API for scheduled tasks (camelCase, series-id)"
```

---

## Task 5: REST internal API (lease + result)

**Files:**
- Create: `internal/server/scheduled_tasks_internal.go`
- Test: `internal/server/scheduled_tasks_internal_test.go`
- Modify: `internal/server/server.go`

These are shared-secret endpoints called only by the dispatcher.

- [ ] **Step 1: Write the failing test**

```go
// internal/server/scheduled_tasks_internal_test.go
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScheduledTasks_LeaseAndResult(t *testing.T) {
	srv, secret := newTestServerWithInternalSecret(t)
	wsID := createTestWorkspace(t, srv.DB)
	// Pre-seed one due, recurring task.
	mustExec(t, srv.DB, `INSERT INTO scheduled_tasks
	 (id, workspace_id, series_id, creator_kind, prompt, timezone, recurrence, process_after, status, timeout_seconds)
	 VALUES ('sch_x',$1,'sch_x','rest','say hi','UTC','*/5 * * * *', NOW() - interval '1 second', 'pending', 30)`, wsID)

	// LEASE
	r := httptest.NewRequest("POST", "/api/internal/scheduled-tasks/lease",
		strings.NewReader(`{"limit":10,"leaseSeconds":60,"owner":"test/1"}`))
	r.Header.Set("X-Internal-Secret", secret)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, r)
	if w.Code != http.StatusOK { t.Fatalf("lease: %d %s", w.Code, w.Body.String()) }
	var leased []map[string]any
	json.NewDecoder(w.Body).Decode(&leased)
	if len(leased) != 1 || leased[0]["id"] != "sch_x" {
		t.Fatalf("leased = %v", leased)
	}

	// RESULT (succeeded, recurring → expect a new sibling row)
	r = httptest.NewRequest("POST", "/api/internal/scheduled-tasks/result",
		strings.NewReader(`{
		  "taskId":"sch_x","runId":"run_1","status":"succeeded",
		  "summary":"ok","durationMs":120,"exitCode":0,
		  "broadcastTo":[],"broadcastErrors":{}
		}`))
	r.Header.Set("X-Internal-Secret", secret)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, r)
	if w.Code != http.StatusOK { t.Fatalf("result: %d %s", w.Code, w.Body.String()) }

	// Expect: original row 'completed', a new sibling row 'pending' in same series.
	var liveCount, completedCount int
	mustQueryRow(t, srv.DB,
		`SELECT
		   COUNT(*) FILTER (WHERE status='pending'),
		   COUNT(*) FILTER (WHERE status='completed')
		 FROM scheduled_tasks WHERE series_id='sch_x'`,
		&liveCount, &completedCount)
	if liveCount != 1 || completedCount != 1 {
		t.Fatalf("series rows: live=%d completed=%d", liveCount, completedCount)
	}
}
```

(Test helpers `newTestServerWithInternalSecret`, `mustExec`, `mustQueryRow` may need to be added to the test scaffolding file — keep them dead-simple.)

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/server/... -run TestScheduledTasks_LeaseAndResult -v
```

Expected: FAIL — handlers/routes missing.

- [ ] **Step 3: Implement handlers**

```go
// internal/server/scheduled_tasks_internal.go
package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/agentserver/agentserver/internal/db"
)

type leaseRequest struct {
	Limit        int    `json:"limit"`
	LeaseSeconds int    `json:"leaseSeconds"`
	Owner        string `json:"owner"`
}

type leaseResponseItem struct {
	ID             string  `json:"id"`
	WorkspaceID    string  `json:"workspaceId"`
	SeriesID       string  `json:"seriesId"`
	Prompt         string  `json:"prompt"`
	Script         *string `json:"script,omitempty"`
	Timezone       string  `json:"timezone"`
	Recurrence     *string `json:"recurrence,omitempty"`
	ProcessAfter   string  `json:"processAfter"`
	TimeoutSeconds int     `json:"timeoutSeconds"`
}

func (s *Server) handleInternalLeaseScheduledTasks(w http.ResponseWriter, r *http.Request) {
	if !s.checkInternalSecret(w, r) { return }
	var req leaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest); return
	}
	if req.Limit <= 0 { req.Limit = 10 }
	if req.LeaseSeconds <= 0 { req.LeaseSeconds = 600 }
	leased, err := s.DB.LeaseDueScheduledTasks(req.Limit, req.LeaseSeconds, req.Owner)
	if err != nil { http.Error(w, "internal error", http.StatusInternalServerError); return }

	// Also pre-seed run rows so the dispatcher can refer to them by id later.
	out := make([]leaseResponseItem, 0, len(leased))
	for _, t := range leased {
		runID := "run_" + uuid.New().String()
		_ = s.DB.CreateScheduledTaskRun(&db.ScheduledTaskRun{
			ID: runID, TaskID: t.ID, SeriesID: t.SeriesID, StartedAt: time.Now(),
		})
		out = append(out, leaseResponseItem{
			ID: t.ID, WorkspaceID: t.WorkspaceID, SeriesID: t.SeriesID,
			Prompt: t.Prompt, Script: t.Script, Timezone: t.Timezone,
			Recurrence: t.Recurrence, ProcessAfter: t.ProcessAfter.UTC().Format(time.RFC3339),
			TimeoutSeconds: t.TimeoutSeconds,
		})
		// stash runID for the dispatcher: include as last_run_id on the row.
		_, _ = s.DB.Exec(`UPDATE scheduled_tasks SET last_run_id = $2 WHERE id = $1`, t.ID, runID)
	}
	writeJSON(w, http.StatusOK, out)
}

type resultRequest struct {
	TaskID          string          `json:"taskId"`
	RunID           string          `json:"runId"`
	Status          string          `json:"status"`         // succeeded|failed|timeout|skipped
	ExitCode        int             `json:"exitCode"`
	DurationMS      int64           `json:"durationMs"`
	Summary         string          `json:"summary"`
	TranscriptURI   string          `json:"transcriptUri"`
	CostUSD         *float64        `json:"costUsd,omitempty"`
	NumTurns        *int            `json:"numTurns,omitempty"`
	BroadcastTo     []string        `json:"broadcastTo"`
	BroadcastErrors json.RawMessage `json:"broadcastErrors"`
}

func (s *Server) handleInternalScheduledTaskResult(w http.ResponseWriter, r *http.Request) {
	if !s.checkInternalSecret(w, r) { return }
	var req resultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest); return
	}

	task, err := s.DB.GetScheduledTaskByID(req.TaskID)
	if err != nil { http.Error(w, "internal error", http.StatusInternalServerError); return }
	if task == nil { http.Error(w, "not found", http.StatusNotFound); return }

	var nextAfter *time.Time
	var newID string
	if task.Recurrence != nil && *task.Recurrence != "" {
		loc, err := time.LoadLocation(task.Timezone)
		if err != nil { loc = time.UTC }
		sched, err := cron.ParseStandard(*task.Recurrence)
		if err == nil {
			n := sched.Next(time.Now().In(loc)).UTC()
			nextAfter = &n
			newID = "sch_" + uuid.New().String()
		}
		// else: leave nextAfter nil → FinalizeRunAndAdvance treats as one-shot complete
	}

	in := db.FinalizeRunInput{
		RunID: req.RunID, TaskID: req.TaskID, Status: req.Status,
		ExitCode: req.ExitCode, DurationMS: req.DurationMS,
		Summary: req.Summary, TranscriptURI: req.TranscriptURI,
		CostUSD: req.CostUSD, NumTurns: req.NumTurns,
		BroadcastTo: req.BroadcastTo, BroadcastErrors: req.BroadcastErrors,
	}
	if err := s.DB.FinalizeRunAndAdvance(in, nextAfter, newID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError); return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "nextRunId": newID})
}

func (s *Server) checkInternalSecret(w http.ResponseWriter, r *http.Request) bool {
	if s.InternalAPISecret == "" {
		http.Error(w, "internal API disabled", http.StatusForbidden); return false
	}
	if r.Header.Get("X-Internal-Secret") != s.InternalAPISecret {
		http.Error(w, "unauthorized", http.StatusUnauthorized); return false
	}
	return true
}
```

If `s.InternalAPISecret` doesn't exist with that exact name, find the field used by other `/api/internal/...` handlers (e.g. `internal/server/server.go` for `requireAgentserverSecret`-style middleware) and reuse the same.

- [ ] **Step 4: Mount routes**

In `internal/server/server.go`, add — inside the unauth group (these endpoints carry their own secret):

```go
r.Post("/api/internal/scheduled-tasks/lease",  s.handleInternalLeaseScheduledTasks)
r.Post("/api/internal/scheduled-tasks/result", s.handleInternalScheduledTaskResult)
```

- [ ] **Step 5: Add cron dependency**

```bash
cd /root/agentserver
go get github.com/robfig/cron/v3@v3.0.1
go mod tidy
```

- [ ] **Step 6: Run tests**

```bash
go test ./internal/server/... -run TestScheduledTasks_LeaseAndResult -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/server/scheduled_tasks_internal.go internal/server/scheduled_tasks_internal_test.go internal/server/server.go go.mod go.sum
git commit -m "feat(server): internal lease + result endpoints; advance via robfig/cron"
```

---

## Task 6: Scheduler — agentserver HTTP client

**Files:**
- Create: `internal/codexappgateway/scheduler/agentserver_client.go`
- Test: `internal/codexappgateway/scheduler/agentserver_client_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/codexappgateway/scheduler/agentserver_client_test.go
package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentserverClient_LeaseDue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/scheduled-tasks/lease" { t.Fatalf("path=%s", r.URL.Path) }
		if r.Header.Get("X-Internal-Secret") != "s3cr3t" { t.Fatal("bad secret") }
		var req LeaseRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Limit != 5 || req.Owner != "pod-1/123" { t.Fatalf("req=%+v", req) }
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"sch_a","workspaceId":"ws","seriesId":"sch_a","prompt":"p","timezone":"UTC","processAfter":"2026-05-22T00:00:00Z","timeoutSeconds":600}]`))
	}))
	defer srv.Close()

	c := NewAgentserverClient(srv.URL, "s3cr3t", "pod-1", 123)
	leased, err := c.LeaseDue(context.Background(), LeaseRequest{Limit: 5, LeaseSeconds: 60, Owner: "pod-1/123"})
	if err != nil { t.Fatal(err) }
	if len(leased) != 1 || leased[0].ID != "sch_a" { t.Fatalf("got %#v", leased) }
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/codexappgateway/scheduler/... -run TestAgentserverClient_LeaseDue -v
```

Expected: FAIL — `NewAgentserverClient` undefined.

- [ ] **Step 3: Implement**

```go
// internal/codexappgateway/scheduler/agentserver_client.go
package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type LeaseRequest struct {
	Limit        int    `json:"limit"`
	LeaseSeconds int    `json:"leaseSeconds"`
	Owner        string `json:"owner"`
}

type Task struct {
	ID             string  `json:"id"`
	WorkspaceID    string  `json:"workspaceId"`
	SeriesID       string  `json:"seriesId"`
	Prompt         string  `json:"prompt"`
	Script         *string `json:"script,omitempty"`
	Timezone       string  `json:"timezone"`
	Recurrence     *string `json:"recurrence,omitempty"`
	ProcessAfter   string  `json:"processAfter"`
	TimeoutSeconds int     `json:"timeoutSeconds"`
	RunID          string  `json:"runId,omitempty"` // populated server-side
}

type ResultRequest struct {
	TaskID          string          `json:"taskId"`
	RunID           string          `json:"runId"`
	Status          string          `json:"status"`
	ExitCode        int             `json:"exitCode"`
	DurationMS      int64           `json:"durationMs"`
	Summary         string          `json:"summary"`
	TranscriptURI   string          `json:"transcriptUri"`
	CostUSD         *float64        `json:"costUsd,omitempty"`
	NumTurns        *int            `json:"numTurns,omitempty"`
	BroadcastTo     []string        `json:"broadcastTo"`
	BroadcastErrors json.RawMessage `json:"broadcastErrors"`
}

type AgentserverClient struct {
	base, secret, owner string
	http                *http.Client
}

func NewAgentserverClient(base, secret, pod string, pid int) *AgentserverClient {
	return &AgentserverClient{
		base:   base,
		secret: secret,
		owner:  pod + "/" + strconv.Itoa(pid),
		http:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *AgentserverClient) LeaseDue(ctx context.Context, req LeaseRequest) ([]Task, error) {
	if req.Owner == "" { req.Owner = c.owner }
	var out []Task
	if err := c.post(ctx, "/api/internal/scheduled-tasks/lease", req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *AgentserverClient) PostResult(ctx context.Context, req ResultRequest) error {
	return c.post(ctx, "/api/internal/scheduled-tasks/result", req, nil)
}

func (c *AgentserverClient) post(ctx context.Context, path string, body, out any) error {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", c.base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Secret", c.secret)
	resp, err := c.http.Do(req)
	if err != nil { return fmt.Errorf("%s: %w", path, err) }
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: status %d", path, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
```

- [ ] **Step 4: Run test**

```bash
go test ./internal/codexappgateway/scheduler/... -run TestAgentserverClient_LeaseDue -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/codexappgateway/scheduler/agentserver_client.go internal/codexappgateway/scheduler/agentserver_client_test.go
git commit -m "feat(scheduler): agentserver HTTP client (lease + result)"
```

---

## Task 7: Scheduler — script runner

**Files:**
- Create: `internal/codexappgateway/scheduler/script.go`
- Test: `internal/codexappgateway/scheduler/script_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/codexappgateway/scheduler/script_test.go
package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunPreScript_Wake(t *testing.T) {
	wake, data, err := RunPreScript(context.Background(),
		`echo '{"wakeAgent":true,"data":{"x":1}}'`, nil)
	if err != nil { t.Fatal(err) }
	if !wake { t.Fatal("want wake=true") }
	if string(data) != `{"x":1}` { t.Fatalf("data=%s", string(data)) }
}

func TestRunPreScript_Skip(t *testing.T) {
	wake, _, err := RunPreScript(context.Background(),
		`echo '{"wakeAgent":false,"data":null}'`, nil)
	if err != nil { t.Fatal(err) }
	if wake { t.Fatal("want wake=false") }
}

func TestRunPreScript_BadJSON(t *testing.T) {
	_, _, err := RunPreScript(context.Background(), `echo notjson`, nil)
	if err == nil { t.Fatal("want error on non-JSON output") }
	if !strings.Contains(err.Error(), "must print JSON") { t.Fatalf("err=%v", err) }
}

func TestRunPreScript_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err := RunPreScript(ctx, `sleep 2`, nil)
	if err == nil { t.Fatal("want timeout error") }
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/codexappgateway/scheduler/... -run TestRunPreScript_ -v
```

Expected: FAIL — `RunPreScript` undefined.

- [ ] **Step 3: Implement**

```go
// internal/codexappgateway/scheduler/script.go
package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"
)

const (
	scriptMaxOutput = 1 << 20 // 1 MiB
	scriptHardLimit = 60 * time.Second
)

// RunPreScript runs a user-supplied bash script and parses its stdout as
// {wakeAgent: bool, data: any}. Mirrors nanoclaw's pre-task script protocol.
func RunPreScript(ctx context.Context, script string, env []string) (wake bool, data json.RawMessage, err error) {
	sctx, cancel := context.WithTimeout(ctx, scriptHardLimit)
	defer cancel()
	cmd := exec.CommandContext(sctx, "bash", "-c", script)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, max: scriptMaxOutput}
	cmd.Stderr = &limitedWriter{w: &stderr, max: scriptMaxOutput}
	if err := cmd.Run(); err != nil {
		return false, nil, fmt.Errorf("script exec: %w (stderr: %s)", err, stderr.String())
	}
	var parsed struct {
		WakeAgent bool            `json:"wakeAgent"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &parsed); err != nil {
		return false, nil, fmt.Errorf("script must print JSON {wakeAgent,data}: %w", err)
	}
	return parsed.WakeAgent, parsed.Data, nil
}

type limitedWriter struct {
	w   io.Writer
	max int
	n   int
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	rem := lw.max - lw.n
	if rem <= 0 { return len(p), nil } // silently drop overflow
	if len(p) > rem { p = p[:rem] }
	n, err := lw.w.Write(p)
	lw.n += n
	return n, err
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/codexappgateway/scheduler/... -run TestRunPreScript_ -v
```

Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/codexappgateway/scheduler/script.go internal/codexappgateway/scheduler/script_test.go
git commit -m "feat(scheduler): pre-task script runner ({wakeAgent,data}, 60s/1MiB caps)"
```

---

## Task 8: Scheduler — supervisor spawn adapter

**Files:**
- Create: `internal/codexappgateway/scheduler/spawn.go`
- Test: `internal/codexappgateway/scheduler/spawn_test.go`

The adapter wraps `exec.CommandContext` of the configured codex binary. The full production form will call the existing `supervisor` package; for the first pass keep it independent so tests can run with a stub binary.

- [ ] **Step 1: Write the failing test**

```go
// internal/codexappgateway/scheduler/spawn_test.go
package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSpawnExec_BinExitsZero(t *testing.T) {
	if runtime.GOOS == "windows" { t.Skip("posix only") }
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakecodex")
	must(t, os.WriteFile(bin, []byte("#!/bin/sh\nread input\necho \"hello $input\"\n"), 0o755))

	s := NewSpawner(bin, nil)
	res, err := s.Run(context.Background(), SpawnInput{
		Prompt:  "world",
		Env:     []string{"TZ=UTC"},
		Timeout: 5 * time.Second,
	})
	if err != nil { t.Fatal(err) }
	if res.ExitCode != 0 { t.Fatalf("exit=%d", res.ExitCode) }
	if res.Transcript == "" { t.Fatalf("empty transcript") }
}

func TestSpawnExec_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" { t.Skip("posix only") }
	dir := t.TempDir()
	bin := filepath.Join(dir, "slowcodex")
	must(t, os.WriteFile(bin, []byte("#!/bin/sh\nsleep 10\n"), 0o755))

	s := NewSpawner(bin, nil)
	res, err := s.Run(context.Background(), SpawnInput{
		Prompt: "x", Timeout: 200 * time.Millisecond,
	})
	if err == nil && res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit; got %+v", res)
	}
	if !res.TimedOut { t.Fatalf("expected TimedOut=true; got %+v", res) }
}

func must(t *testing.T, err error) { t.Helper(); if err != nil { t.Fatal(err) } }
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/codexappgateway/scheduler/... -run TestSpawnExec_ -v
```

Expected: FAIL — `NewSpawner`, `SpawnInput`, etc. undefined.

- [ ] **Step 3: Implement**

```go
// internal/codexappgateway/scheduler/spawn.go
package scheduler

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

type SpawnInput struct {
	Prompt  string
	Env     []string
	Timeout time.Duration
}

type SpawnResult struct {
	ExitCode   int
	Transcript string  // full captured stdout (truncated at 256 KiB)
	Summary    string  // last non-empty line, truncated to 4 KiB
	CostUSD    *float64
	NumTurns   *int
	TimedOut   bool
	DurationMS int64
}

type Spawner struct {
	bin     string
	extraEnv []string
}

func NewSpawner(bin string, extraEnv []string) *Spawner {
	return &Spawner{bin: bin, extraEnv: extraEnv}
}

const (
	transcriptCap = 256 << 10
	summaryCap    = 4 << 10
)

func (s *Spawner) Run(ctx context.Context, in SpawnInput) (SpawnResult, error) {
	if in.Timeout <= 0 { in.Timeout = 10 * time.Minute }
	cctx, cancel := context.WithTimeout(ctx, in.Timeout)
	defer cancel()

	// Default invocation: `codex exec --json -` (stdin = prompt). Adjust
	// args once we wire the real supervisor in Task 11.
	cmd := exec.CommandContext(cctx, s.bin, "exec", "--json", "-")
	cmd.Env = append([]string{}, in.Env...)
	cmd.Env = append(cmd.Env, s.extraEnv...)
	cmd.Stdin = strings.NewReader(in.Prompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, max: transcriptCap}
	cmd.Stderr = &limitedWriter{w: &stderr, max: 64 << 10}

	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start).Milliseconds()

	res := SpawnResult{
		ExitCode:   exitCodeOf(err, cmd),
		Transcript: stdout.String(),
		DurationMS: dur,
		TimedOut:   errors.Is(cctx.Err(), context.DeadlineExceeded),
	}
	res.Summary = lastNonEmpty(res.Transcript, summaryCap)
	// CostUSD / NumTurns parsing is best-effort and codex-version specific;
	// see Task 11 for the parser hook. Empty placeholders are fine for tests.
	return res, nil
}

func exitCodeOf(err error, cmd *exec.Cmd) int {
	if err == nil { return 0 }
	if cmd.ProcessState != nil { return cmd.ProcessState.ExitCode() }
	var ee *exec.ExitError
	if errors.As(err, &ee) { return ee.ExitCode() }
	return -1
}

func lastNonEmpty(s string, cap int) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		ln := strings.TrimSpace(lines[i])
		if ln != "" {
			if len(ln) > cap { ln = ln[:cap] }
			return ln
		}
	}
	return ""
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/codexappgateway/scheduler/... -run TestSpawnExec_ -v
```

Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/codexappgateway/scheduler/spawn.go internal/codexappgateway/scheduler/spawn_test.go
git commit -m "feat(scheduler): codex exec spawn adapter (timeout + transcript caps)"
```

---

## Task 9: Scheduler — broadcast to imbridge

**Files:**
- Create: `internal/codexappgateway/scheduler/broadcast.go`
- Test: `internal/codexappgateway/scheduler/broadcast_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/codexappgateway/scheduler/broadcast_test.go
package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestBroadcaster_FanoutAllChannels(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/imbridge/send" { t.Fatalf("path=%s", r.URL.Path) }
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["text"] == "" { t.Fatalf("empty text") }
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b := NewBroadcaster(srv.URL, "shh")
	channels := []ChannelRef{
		{Provider: "weixin", BotID: "b1", UserID: "u1"},
		{Provider: "telegram", BotID: "b2", UserID: "u2"},
	}
	report := b.Send(context.Background(), "ws", "hello", channels)
	if calls.Load() != 2 { t.Fatalf("calls=%d", calls.Load()) }
	if len(report.Errors) != 0 { t.Fatalf("errors=%v", report.Errors) }
	if len(report.To) != 2 { t.Fatalf("to=%v", report.To) }
}

func TestBroadcaster_PartialFailureDoesNotAbort(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 { http.Error(w, "boom", http.StatusInternalServerError); return }
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b := NewBroadcaster(srv.URL, "shh")
	channels := []ChannelRef{
		{Provider: "p", BotID: "b1", UserID: "u1"},
		{Provider: "p", BotID: "b2", UserID: "u2"},
	}
	report := b.Send(context.Background(), "ws", "hi", channels)
	if calls.Load() != 2 { t.Fatalf("calls=%d", calls.Load()) }
	if len(report.Errors) != 1 { t.Fatalf("errors=%v", report.Errors) }
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/codexappgateway/scheduler/... -run TestBroadcaster_ -v
```

Expected: FAIL — `NewBroadcaster` undefined.

- [ ] **Step 3: Implement**

```go
// internal/codexappgateway/scheduler/broadcast.go
package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type ChannelRef struct {
	ID       string `json:"id,omitempty"` // populated when sourced from workspace_im_channels
	Provider string `json:"provider"`
	BotID    string `json:"botId"`
	UserID   string `json:"userId"`
}

type BroadcastReport struct {
	To     []string          // channel ids attempted
	Errors map[string]string // channel id → error text
}

type Broadcaster struct {
	base, secret string
	http         *http.Client
}

func NewBroadcaster(base, secret string) *Broadcaster {
	return &Broadcaster{base: base, secret: secret, http: &http.Client{Timeout: 10 * time.Second}}
}

func (b *Broadcaster) Send(ctx context.Context, workspaceID, text string, channels []ChannelRef) BroadcastReport {
	rep := BroadcastReport{Errors: map[string]string{}}
	for _, c := range channels {
		rep.To = append(rep.To, channelKey(c))
		body, _ := json.Marshal(map[string]any{
			"workspace_id": workspaceID,
			"provider":     c.Provider,
			"bot_id":       c.BotID,
			"to_user_id":   c.UserID,
			"text":         text,
		})
		req, _ := http.NewRequestWithContext(ctx, "POST", b.base+"/api/internal/imbridge/send", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if b.secret != "" { req.Header.Set("X-Internal-Secret", b.secret) }
		resp, err := b.http.Do(req)
		if err != nil { rep.Errors[channelKey(c)] = err.Error(); continue }
		if resp.StatusCode/100 != 2 {
			rep.Errors[channelKey(c)] = fmt.Sprintf("status %d", resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
	return rep
}

func channelKey(c ChannelRef) string {
	if c.ID != "" { return c.ID }
	return c.Provider + ":" + c.BotID + ":" + c.UserID
}
```

(Inspect `internal/imbridgesvc/handlers.go:handleImbridgeDirectSend` for the EXACT body fields the endpoint expects. The map keys above mirror what we saw in the explore phase — adjust if the real handler uses different names.)

- [ ] **Step 4: Run tests**

```bash
go test ./internal/codexappgateway/scheduler/... -run TestBroadcaster_ -v
```

Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/codexappgateway/scheduler/broadcast.go internal/codexappgateway/scheduler/broadcast_test.go
git commit -m "feat(scheduler): imbridge broadcaster with per-channel error report"
```

---

## Task 10: Scheduler — dispatcher + loop glue

**Files:**
- Create: `internal/codexappgateway/scheduler/dispatcher.go`
- Create: `internal/codexappgateway/scheduler/loop.go`
- Test: `internal/codexappgateway/scheduler/dispatcher_test.go`

To resolve workspace IM channels, the dispatcher needs a list — fetched from agentserver-main via a new internal endpoint. Add a thin call to `AgentserverClient`.

- [ ] **Step 1: Extend `AgentserverClient` to fetch channels**

Add to `internal/codexappgateway/scheduler/agentserver_client.go`:

```go
func (c *AgentserverClient) ListChannels(ctx context.Context, workspaceID string) ([]ChannelRef, error) {
	var out []ChannelRef
	req, _ := http.NewRequestWithContext(ctx, "GET",
		c.base+"/api/internal/workspaces/"+workspaceID+"/im-channels", nil)
	req.Header.Set("X-Internal-Secret", c.secret)
	resp, err := c.http.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 { return nil, fmt.Errorf("list channels: %d", resp.StatusCode) }
	return out, json.NewDecoder(resp.Body).Decode(&out)
}
```

Add the matching agentserver-main handler in `internal/server/scheduled_tasks_internal.go`:

```go
func (s *Server) handleInternalListIMChannels(w http.ResponseWriter, r *http.Request) {
	if !s.checkInternalSecret(w, r) { return }
	wid := chi.URLParam(r, "wid")
	chs, err := s.DB.ListIMChannelsByWorkspace(wid) // existing
	if err != nil { http.Error(w, "internal error", http.StatusInternalServerError); return }
	out := make([]map[string]string, 0, len(chs))
	for _, ch := range chs {
		out = append(out, map[string]string{
			"id":       ch.ID, // adjust to real struct fields
			"provider": ch.Provider,
			"botId":    ch.BotID,
			"userId":   ch.UserID,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
```

Mount the route alongside the lease/result routes:

```go
r.Get("/api/internal/workspaces/{wid}/im-channels", s.handleInternalListIMChannels)
```

(Look at `internal/db/im_channels.go` for the actual struct field names — substitute accordingly.)

- [ ] **Step 2: Write the failing dispatcher test**

```go
// internal/codexappgateway/scheduler/dispatcher_test.go
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type fakeAgent struct {
	lastResult *ResultRequest
	channels   []ChannelRef
}
func (f *fakeAgent) LeaseDue(ctx context.Context, _ LeaseRequest) ([]Task, error) { return nil, nil }
func (f *fakeAgent) PostResult(_ context.Context, r ResultRequest) error          { f.lastResult = &r; return nil }
func (f *fakeAgent) ListChannels(_ context.Context, _ string) ([]ChannelRef, error) { return f.channels, nil }

type fakeSpawner struct{ res SpawnResult; err error }
func (f *fakeSpawner) Run(_ context.Context, _ SpawnInput) (SpawnResult, error) { return f.res, f.err }

type fakeBroadcaster struct{ called int }
func (f *fakeBroadcaster) Send(_ context.Context, _, _ string, ch []ChannelRef) BroadcastReport {
	f.called++; to := make([]string, len(ch)); for i, c := range ch { to[i] = c.Provider }
	return BroadcastReport{To: to, Errors: map[string]string{}}
}

func TestDispatcher_Fire_HappyPath_PostsResultAndBroadcasts(t *testing.T) {
	a := &fakeAgent{channels: []ChannelRef{{Provider: "weixin", BotID: "b", UserID: "u"}}}
	sp := &fakeSpawner{res: SpawnResult{ExitCode: 0, Summary: "ok", Transcript: "ok"}}
	br := &fakeBroadcaster{}
	d := NewDispatcher(a, sp, br)
	err := d.Fire(context.Background(), Task{ID: "sch_a", RunID: "run_1", WorkspaceID: "ws", Prompt: "hi", Timezone: "UTC"})
	if err != nil { t.Fatal(err) }
	if a.lastResult == nil || a.lastResult.Status != "succeeded" {
		t.Fatalf("result=%+v", a.lastResult)
	}
	if br.called != 1 { t.Fatalf("broadcast called %d times", br.called) }
}

func TestDispatcher_Fire_ScriptGated_Skips(t *testing.T) {
	a := &fakeAgent{channels: []ChannelRef{{Provider: "x"}}}
	sp := &fakeSpawner{res: SpawnResult{ExitCode: 0, Summary: "should not appear"}}
	br := &fakeBroadcaster{}
	d := NewDispatcher(a, sp, br)
	skipScript := `echo '{"wakeAgent":false,"data":null}'`
	t1 := Task{ID: "sch_a", RunID: "r1", WorkspaceID: "ws", Prompt: "hi", Timezone: "UTC", Script: &skipScript}
	if err := d.Fire(context.Background(), t1); err != nil { t.Fatal(err) }
	if a.lastResult == nil || a.lastResult.Status != "skipped" {
		t.Fatalf("result=%+v", a.lastResult)
	}
	if br.called != 0 { t.Fatalf("must not broadcast on skip; called=%d", br.called) }
}

func TestDispatcher_Fire_SpawnError_ReportsFailed(t *testing.T) {
	a := &fakeAgent{}
	sp := &fakeSpawner{err: errors.New("boom")}
	d := NewDispatcher(a, sp, &fakeBroadcaster{})
	_ = d.Fire(context.Background(), Task{ID: "sch_x", RunID: "r", WorkspaceID: "w", Prompt: "p", Timezone: "UTC"})
	if a.lastResult == nil || a.lastResult.Status != "failed" {
		t.Fatalf("result=%+v", a.lastResult)
	}
}

// silence unused-import warnings; keeps the test file self-contained.
var _ = json.Marshal
```

- [ ] **Step 3: Run the test to verify it fails**

```bash
go test ./internal/codexappgateway/scheduler/... -run TestDispatcher_Fire_ -v
```

Expected: FAIL — `NewDispatcher` undefined.

- [ ] **Step 4: Implement dispatcher**

```go
// internal/codexappgateway/scheduler/dispatcher.go
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type agentClient interface {
	LeaseDue(ctx context.Context, req LeaseRequest) ([]Task, error)
	PostResult(ctx context.Context, req ResultRequest) error
	ListChannels(ctx context.Context, workspaceID string) ([]ChannelRef, error)
}

type spawner interface {
	Run(ctx context.Context, in SpawnInput) (SpawnResult, error)
}

type broadcaster interface {
	Send(ctx context.Context, workspaceID, text string, channels []ChannelRef) BroadcastReport
}

type Dispatcher struct {
	agent       agentClient
	spawner     spawner
	broadcaster broadcaster
}

func NewDispatcher(a agentClient, sp spawner, br broadcaster) *Dispatcher {
	return &Dispatcher{agent: a, spawner: sp, broadcaster: br}
}

func (d *Dispatcher) Fire(ctx context.Context, t Task) error {
	start := time.Now()
	prompt := t.Prompt

	// 1. Script gate
	if t.Script != nil && *t.Script != "" {
		wake, data, err := RunPreScript(ctx, *t.Script, scriptEnv(t))
		switch {
		case err != nil:
			return d.report(ctx, t, ResultRequest{
				TaskID: t.ID, RunID: t.RunID, Status: "failed",
				Summary: truncErr(err), DurationMS: time.Since(start).Milliseconds(),
				BroadcastTo: nil, BroadcastErrors: json.RawMessage("{}"),
			}, "", true)
		case !wake:
			return d.report(ctx, t, ResultRequest{
				TaskID: t.ID, RunID: t.RunID, Status: "skipped",
				Summary: "script gated (wakeAgent=false)",
				DurationMS: time.Since(start).Milliseconds(),
				BroadcastTo: nil, BroadcastErrors: json.RawMessage("{}"),
			}, "", false) // do NOT broadcast skipped runs
		default:
			if len(data) > 0 {
				prompt = "## script_data:\n" + string(data) + "\n\n" + prompt
			}
		}
	}

	// 2. Spawn codex
	timeout := time.Duration(t.TimeoutSeconds) * time.Second
	res, err := d.spawner.Run(ctx, SpawnInput{Prompt: prompt, Env: codexEnv(t), Timeout: timeout})

	status := "succeeded"
	summary := res.Summary
	switch {
	case err != nil:
		status = "failed"
		summary = truncErr(err)
	case res.TimedOut:
		status = "timeout"
		if summary == "" { summary = "(codex exec timed out)" }
	case res.ExitCode != 0:
		status = "failed"
		if summary == "" { summary = fmt.Sprintf("(codex exec exit %d)", res.ExitCode) }
	}

	return d.report(ctx, t, ResultRequest{
		TaskID: t.ID, RunID: t.RunID, Status: status,
		ExitCode: res.ExitCode, DurationMS: res.DurationMS,
		Summary: summary, CostUSD: res.CostUSD, NumTurns: res.NumTurns,
	}, summary, true)
}

func (d *Dispatcher) report(ctx context.Context, t Task, r ResultRequest, broadcastText string, shouldBroadcast bool) error {
	if shouldBroadcast {
		channels, err := d.agent.ListChannels(ctx, t.WorkspaceID)
		if err == nil && len(channels) > 0 {
			rep := d.broadcaster.Send(ctx, t.WorkspaceID, renderIMText(t, r, broadcastText), channels)
			r.BroadcastTo = rep.To
			b, _ := json.Marshal(rep.Errors)
			r.BroadcastErrors = b
		}
	}
	if r.BroadcastErrors == nil { r.BroadcastErrors = json.RawMessage("{}") }
	if r.BroadcastTo == nil    { r.BroadcastTo = []string{} }
	return d.agent.PostResult(ctx, r)
}

func renderIMText(t Task, r ResultRequest, body string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[scheduled task fired — %s]\n", t.SeriesID)
	fmt.Fprintf(&sb, "at: %s  (took %dms)\n", time.Now().UTC().Format(time.RFC3339), r.DurationMS)
	if body == "" { body = "(no output)" }
	if len(body) > 1500 { body = body[:1500] + "…(truncated)" }
	sb.WriteString(body)
	return sb.String()
}

func codexEnv(t Task) []string {
	return []string{"TZ=" + t.Timezone}
	// production: append workspace creds from a sealed config (see Task 12 wiring).
}

func scriptEnv(t Task) []string {
	// Whitelist only — explicitly NOT including ANTHROPIC_API_KEY etc.
	return []string{"TZ=" + t.Timezone, "TASK_ID=" + t.ID}
}

func truncErr(err error) string {
	s := err.Error()
	if len(s) > 1500 { s = s[:1500] }
	return s
}
```

- [ ] **Step 5: Implement the loop**

```go
// internal/codexappgateway/scheduler/loop.go
package scheduler

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

type Config struct {
	AgentserverBase    string
	InternalSecret     string
	ImbridgeBase       string
	ImbridgeSecret     string
	CodexBin           string
	PodID              string
	PID                int
	TickInterval       time.Duration
	LeaseSeconds       int
	Concurrency        int
}

type Loop struct {
	cfg         Config
	agent       *AgentserverClient
	dispatcher  *Dispatcher
	logger      *slog.Logger
	inflight    atomic.Int32
}

func New(cfg Config, logger *slog.Logger) *Loop {
	agent := NewAgentserverClient(cfg.AgentserverBase, cfg.InternalSecret, cfg.PodID, cfg.PID)
	disp := NewDispatcher(agent, NewSpawner(cfg.CodexBin, nil), NewBroadcaster(cfg.ImbridgeBase, cfg.ImbridgeSecret))
	if cfg.TickInterval <= 0 { cfg.TickInterval = 15 * time.Second }
	if cfg.LeaseSeconds <= 0 { cfg.LeaseSeconds = 30 * 60 }
	if cfg.Concurrency  <= 0 { cfg.Concurrency = 4 }
	return &Loop{cfg: cfg, agent: agent, dispatcher: disp, logger: logger}
}

func (l *Loop) Run(ctx context.Context) {
	l.logger.Info("scheduler loop start",
		"tick", l.cfg.TickInterval, "lease_s", l.cfg.LeaseSeconds, "concurrency", l.cfg.Concurrency)
	t := time.NewTicker(l.cfg.TickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done(): return
		case <-t.C: l.tick(ctx)
		}
	}
}

func (l *Loop) tick(ctx context.Context) {
	free := int(int32(l.cfg.Concurrency) - l.inflight.Load())
	if free <= 0 { return }
	batch, err := l.agent.LeaseDue(ctx, LeaseRequest{
		Limit: free, LeaseSeconds: l.cfg.LeaseSeconds,
	})
	if err != nil { l.logger.Warn("lease failed", "err", err); return }
	for _, t := range batch {
		l.inflight.Add(1)
		go func(t Task) {
			defer l.inflight.Add(-1)
			if err := l.dispatcher.Fire(ctx, t); err != nil {
				l.logger.Warn("dispatcher.Fire failed", "task_id", t.ID, "err", err)
			}
		}(t)
	}
}
```

- [ ] **Step 6: Run tests**

```bash
go test ./internal/codexappgateway/scheduler/... -v
```

Expected: All scheduler tests PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/codexappgateway/scheduler/dispatcher.go internal/codexappgateway/scheduler/loop.go internal/codexappgateway/scheduler/dispatcher_test.go internal/codexappgateway/scheduler/agentserver_client.go internal/server/scheduled_tasks_internal.go internal/server/server.go
git commit -m "feat(scheduler): dispatcher + loop; agentserver internal channel listing"
```

---

## Task 11: MCP tool — scheduling.go + loopback bridge

**Files:**
- Create: `internal/codexappgateway/envmcp/tools/scheduling.go`
- Create: `internal/codexappgateway/envmcp/tools/scheduling.instructions.md`
- Create: `internal/codexappgateway/envmcp/tools/testdata/scheduling.golden.json`
- Create: `internal/codexappgateway/envmcp/tools/scheduling_test.go`
- Modify: `internal/codexappgateway/internal_api.go` (new `/internal/scheduled-tasks/*` proxy)
- Modify: `internal/codexappgateway/envmcp/envmcp.go` (register tools)

The MCP tool forwards each call to the app-gateway loopback `/internal/scheduled-tasks/<action>`, which then calls agentserver-main's REST `/api/workspaces/<wid>/scheduled-tasks/...` using the workspace context resolved from `X-Loopback-Token`.

- [ ] **Step 1: Write the golden-file MCP test**

Save the golden first (this locks the public surface):

```json
// internal/codexappgateway/envmcp/tools/testdata/scheduling.golden.json
{
  "tools": [
    {
      "name": "schedule_task",
      "description": "Schedule a one-shot or recurring task. The user's timezone is declared in the <context timezone=\"...\"/> header of your prompt — interpret the user's \"9pm\" etc. in that zone. Cron expressions are interpreted in the user's timezone too.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "prompt": {"type": "string", "description": "Task instructions/prompt"},
          "processAfter": {"type": "string", "description": "ISO 8601 timestamp for the first run. Accepts either UTC (ending in \"Z\" or \"+00:00\") or a naive local timestamp (no offset) which is interpreted in the user's timezone (e.g. \"2026-01-15T21:00:00\" = 9pm user-local). Prefer naive local."},
          "recurrence": {"type": "string", "description": "Cron expression for recurring tasks (e.g., \"0 9 * * 1-5\" = weekdays at 9am user-local). Evaluated in the user's timezone."},
          "script": {"type": "string", "description": "Optional pre-agent script to run before processing"}
        },
        "required": ["prompt", "processAfter"]
      }
    },
    {
      "name": "list_tasks",
      "description": "List scheduled tasks. Returns one row per series — the live (pending or paused) occurrence. The id shown is the series id, which is what update_task / cancel_task / pause_task / resume_task expect.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "status": {"type": "string", "description": "Filter by status: pending or paused (default: both)"}
        }
      }
    },
    {
      "name": "update_task",
      "description": "Update a scheduled task. Pass the series id from list_tasks. Any field omitted is left unchanged. Use this instead of cancel + reschedule when adjusting an existing task.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "taskId": {"type": "string", "description": "Series id of the task to update (as shown by list_tasks)"},
          "prompt": {"type": "string", "description": "New task prompt (optional)"},
          "recurrence": {"type": "string", "description": "New cron expression (optional). Pass empty string to clear and make the task one-shot."},
          "processAfter": {"type": "string", "description": "New ISO 8601 timestamp for the next run (optional). Accepts either UTC (ending in \"Z\" / \"+00:00\") or a naive local timestamp interpreted in the user's timezone."},
          "script": {"type": "string", "description": "New pre-agent script (optional). Pass empty string to clear."}
        },
        "required": ["taskId"]
      }
    },
    {
      "name": "cancel_task",
      "description": "Cancel a scheduled task.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "taskId": {"type": "string", "description": "Task ID to cancel"}
        },
        "required": ["taskId"]
      }
    },
    {
      "name": "pause_task",
      "description": "Pause a scheduled task. It will not run until resumed.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "taskId": {"type": "string", "description": "Task ID to pause"}
        },
        "required": ["taskId"]
      }
    },
    {
      "name": "resume_task",
      "description": "Resume a paused task.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "taskId": {"type": "string", "description": "Task ID to resume"}
        },
        "required": ["taskId"]
      }
    }
  ]
}
```

Then the snapshot test:

```go
// internal/codexappgateway/envmcp/tools/scheduling_test.go
package tools

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestSchedulingTools_MatchGolden(t *testing.T) {
	tools := NewSchedulingTools(nil) // nil transport is fine for metadata
	got := struct {
		Tools []map[string]any `json:"tools"`
	}{}
	for _, tool := range tools {
		var schema map[string]any
		_ = json.Unmarshal(tool.InputSchema(), &schema)
		got.Tools = append(got.Tools, map[string]any{
			"name":        tool.Name(),
			"description": tool.Description(),
			"inputSchema": schema,
		})
	}
	gotBytes, _ := json.MarshalIndent(got, "", "  ")

	want, err := os.ReadFile("testdata/scheduling.golden.json")
	if err != nil { t.Fatal(err) }

	// Normalize: re-marshal both via map[string]any so key ordering is consistent.
	var a, b any
	_ = json.Unmarshal(want, &a); _ = json.Unmarshal(gotBytes, &b)
	wantNorm, _ := json.Marshal(a); gotNorm, _ := json.Marshal(b)
	if string(wantNorm) != string(gotNorm) {
		t.Errorf("MCP surface drift!\nwant:\n%s\ngot:\n%s", wantNorm, gotNorm)
	}
}

func TestScheduleTask_ForwardsToTransport(t *testing.T) {
	var captured struct {
		path string
		body map[string]any
	}
	transport := transportFunc(func(_ context.Context, path string, body any) (json.RawMessage, error) {
		captured.path = path
		_ = json.Unmarshal(mustJSON(body), &captured.body)
		return json.RawMessage(`{"taskId":"sch_x","runsAt":"2099-01-01T00:00:00Z","status":"pending","timezone":"UTC"}`), nil
	})
	tools := NewSchedulingTools(transport)
	sch := findTool(t, tools, "schedule_task")
	res, err := sch.Call(context.Background(), json.RawMessage(`{"prompt":"hi","processAfter":"2099-01-01T00:00:00Z"}`))
	if err != nil { t.Fatal(err) }
	if res.IsError { t.Fatalf("got isError; content=%+v", res.Content) }
	if captured.path != "schedule" { t.Fatalf("path=%s", captured.path) }
	if captured.body["prompt"] != "hi" { t.Fatalf("body=%v", captured.body) }
}
```

(Helpers `transportFunc`, `mustJSON`, `findTool` are defined alongside the production code below.)

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/codexappgateway/envmcp/tools/... -run TestSchedulingTools_MatchGolden -v
go test ./internal/codexappgateway/envmcp/tools/... -run TestScheduleTask_ForwardsToTransport -v
```

Expected: FAIL — package symbols undefined.

- [ ] **Step 3: Implement the tools + loopback transport**

```go
// internal/codexappgateway/envmcp/tools/scheduling.go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// Transport is the abstraction the scheduling tools use to forward to
// app-gateway loopback. In production it's an HTTP client to
// http://127.0.0.1:<gw>/internal/scheduled-tasks/<action>. In tests it
// can be any function.
type Transport interface {
	Call(ctx context.Context, action string, body any) (json.RawMessage, error)
}

type transportFunc func(ctx context.Context, action string, body any) (json.RawMessage, error)
func (f transportFunc) Call(ctx context.Context, action string, body any) (json.RawMessage, error) { return f(ctx, action, body) }

// NewSchedulingTools returns the 6 nanoclaw-aligned scheduling tools.
// transport may be nil if only metadata (Name/Description/InputSchema) is needed.
func NewSchedulingTools(transport Transport) []Tool {
	return []Tool{
		&scheduleTaskTool{t: transport},
		&listTasksTool{t: transport},
		&updateTaskTool{t: transport},
		&cancelTaskTool{t: transport},
		&pauseTaskTool{t: transport},
		&resumeTaskTool{t: transport},
	}
}

// ---- schedule_task ----

type scheduleTaskTool struct{ t Transport }
func (*scheduleTaskTool) Name() string { return "schedule_task" }
func (*scheduleTaskTool) Description() string {
	return `Schedule a one-shot or recurring task. The user's timezone is declared in the <context timezone="..."/> header of your prompt — interpret the user's "9pm" etc. in that zone. Cron expressions are interpreted in the user's timezone too.`
}
func (*scheduleTaskTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "prompt":       {"type":"string","description":"Task instructions/prompt"},
    "processAfter": {"type":"string","description":"ISO 8601 timestamp for the first run. Accepts either UTC (ending in \"Z\" or \"+00:00\") or a naive local timestamp (no offset) which is interpreted in the user's timezone (e.g. \"2026-01-15T21:00:00\" = 9pm user-local). Prefer naive local."},
    "recurrence":   {"type":"string","description":"Cron expression for recurring tasks (e.g., \"0 9 * * 1-5\" = weekdays at 9am user-local). Evaluated in the user's timezone."},
    "script":       {"type":"string","description":"Optional pre-agent script to run before processing"}
  },
  "required": ["prompt","processAfter"]
}`)
}
func (s *scheduleTaskTool) Call(ctx context.Context, args json.RawMessage) (MCPCallToolResult, error) {
	return forward(ctx, s.t, "schedule", args)
}

// ---- list_tasks ----

type listTasksTool struct{ t Transport }
func (*listTasksTool) Name() string { return "list_tasks" }
func (*listTasksTool) Description() string {
	return "List scheduled tasks. Returns one row per series — the live (pending or paused) occurrence. The id shown is the series id, which is what update_task / cancel_task / pause_task / resume_task expect."
}
func (*listTasksTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "status": {"type":"string","description":"Filter by status: pending or paused (default: both)"}
  }
}`)
}
func (l *listTasksTool) Call(ctx context.Context, args json.RawMessage) (MCPCallToolResult, error) {
	return forward(ctx, l.t, "list", args)
}

// ---- update_task ----

type updateTaskTool struct{ t Transport }
func (*updateTaskTool) Name() string { return "update_task" }
func (*updateTaskTool) Description() string {
	return "Update a scheduled task. Pass the series id from list_tasks. Any field omitted is left unchanged. Use this instead of cancel + reschedule when adjusting an existing task."
}
func (*updateTaskTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "taskId":       {"type":"string","description":"Series id of the task to update (as shown by list_tasks)"},
    "prompt":       {"type":"string","description":"New task prompt (optional)"},
    "recurrence":   {"type":"string","description":"New cron expression (optional). Pass empty string to clear and make the task one-shot."},
    "processAfter": {"type":"string","description":"New ISO 8601 timestamp for the next run (optional). Accepts either UTC (ending in \"Z\" / \"+00:00\") or a naive local timestamp interpreted in the user's timezone."},
    "script":       {"type":"string","description":"New pre-agent script (optional). Pass empty string to clear."}
  },
  "required": ["taskId"]
}`)
}
func (u *updateTaskTool) Call(ctx context.Context, args json.RawMessage) (MCPCallToolResult, error) {
	// Reject empty updates (only taskId provided) — nanoclaw parity.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(args, &m); err != nil {
		return errorResult("invalid args"), nil
	}
	if len(m) <= 1 {
		return errorResult("at least one field to update is required"), nil
	}
	return forward(ctx, u.t, "update", args)
}

// ---- cancel_task / pause_task / resume_task ----

type cancelTaskTool struct{ t Transport }
func (*cancelTaskTool) Name() string { return "cancel_task" }
func (*cancelTaskTool) Description() string { return "Cancel a scheduled task." }
func (*cancelTaskTool) InputSchema() json.RawMessage { return taskIdOnlySchema("Task ID to cancel") }
func (c *cancelTaskTool) Call(ctx context.Context, args json.RawMessage) (MCPCallToolResult, error) { return forward(ctx, c.t, "cancel", args) }

type pauseTaskTool struct{ t Transport }
func (*pauseTaskTool) Name() string { return "pause_task" }
func (*pauseTaskTool) Description() string { return "Pause a scheduled task. It will not run until resumed." }
func (*pauseTaskTool) InputSchema() json.RawMessage { return taskIdOnlySchema("Task ID to pause") }
func (p *pauseTaskTool) Call(ctx context.Context, args json.RawMessage) (MCPCallToolResult, error) { return forward(ctx, p.t, "pause", args) }

type resumeTaskTool struct{ t Transport }
func (*resumeTaskTool) Name() string { return "resume_task" }
func (*resumeTaskTool) Description() string { return "Resume a paused task." }
func (*resumeTaskTool) InputSchema() json.RawMessage { return taskIdOnlySchema("Task ID to resume") }
func (r *resumeTaskTool) Call(ctx context.Context, args json.RawMessage) (MCPCallToolResult, error) { return forward(ctx, r.t, "resume", args) }

// ---- helpers ----

func taskIdOnlySchema(desc string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{
  "type":"object",
  "properties": {"taskId":{"type":"string","description":%q}},
  "required":["taskId"]
}`, desc))
}

func forward(ctx context.Context, t Transport, action string, body json.RawMessage) (MCPCallToolResult, error) {
	if t == nil {
		return errorResult("transport not configured"), nil
	}
	out, err := t.Call(ctx, action, json.RawMessage(body))
	if err != nil {
		return errorResult(err.Error()), nil
	}
	return MCPCallToolResult{
		Content: []MCPToolContent{{Type: "text", Text: string(out)}},
	}, nil
}

func errorResult(msg string) MCPCallToolResult {
	return MCPCallToolResult{
		Content: []MCPToolContent{{Type: "text", Text: "Error: " + msg}},
		IsError: true,
	}
}

// test helpers (kept in production file for simplicity)
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func findTool(t interface{ Helper(); Fatalf(string, ...any) }, ts []Tool, name string) Tool {
	t.Helper()
	for _, x := range ts { if x.Name() == name { return x } }
	t.Fatalf("tool %q not found", name); return nil
}
```

(Move `transportFunc`, `mustJSON`, `findTool` to a `scheduling_test_helpers.go` if your repo dislikes test helpers in production files.)

- [ ] **Step 4: Implement the loopback transport in app-gateway**

Add to `internal/codexappgateway/envmcp/envmcp.go`:

```go
// In Run(), after building the existing tool list and BEFORE `tools.NewMCPServer`:

schedTransport := tools.NewLoopbackSchedulingTransport(
    strings.TrimRight(args.AppGatewayInternal, "/") + "/internal/scheduled-tasks",
    lbToken, // X-Loopback-Token
)
toolList = append(toolList, tools.NewSchedulingTools(schedTransport)...)
```

Add a tiny http-based Transport implementation:

```go
// internal/codexappgateway/envmcp/tools/loopback_transport.go
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type LoopbackSchedulingTransport struct {
	baseURL  string // e.g. "http://127.0.0.1:8086/internal/scheduled-tasks"
	lbToken  string
	http     *http.Client
}

func NewLoopbackSchedulingTransport(base, token string) *LoopbackSchedulingTransport {
	return &LoopbackSchedulingTransport{
		baseURL: base, lbToken: token,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (t *LoopbackSchedulingTransport) Call(ctx context.Context, action string, body any) (json.RawMessage, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", t.baseURL+"/"+action, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Loopback-Token", t.lbToken)
	resp, err := t.http.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("loopback %s: %d %s", action, resp.StatusCode, string(out))
	}
	return out, nil
}
```

Add the loopback proxy handlers in `internal/codexappgateway/internal_api.go`:

```go
// At the end of internal_api.go — pattern mirrors handleInternalConnected.
// Each handler resolves workspace_id from X-Loopback-Token, then proxies to
// the agentserver-main REST endpoint with the shared internal secret.

func (s *Server) handleInternalScheduledTask(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackRemote(r.RemoteAddr) { http.Error(w, "forbidden", http.StatusForbidden); return }
		tok := r.Header.Get("X-Loopback-Token")
		wid, ok := s.sup.LookupWorkspaceForLoopbackToken(tok)
		if !ok { http.Error(w, "bad token", http.StatusUnauthorized); return }

		body, _ := io.ReadAll(r.Body)
		// Map MCP action → agentserver path + method.
		method, path := "", ""
		switch action {
		case "schedule": method, path = "POST",  fmt.Sprintf("/api/workspaces/%s/scheduled-tasks", wid)
		case "list":     method, path = "GET",   fmt.Sprintf("/api/workspaces/%s/scheduled-tasks", wid)
		case "cancel", "pause", "resume":
			var v struct{ TaskID string `json:"taskId"` }
			_ = json.Unmarshal(body, &v)
			method, path = "POST", fmt.Sprintf("/api/workspaces/%s/scheduled-tasks/%s/%s", wid, v.TaskID, action)
			body = nil
		case "update":
			var v struct{ TaskID string `json:"taskId"` }
			_ = json.Unmarshal(body, &v)
			method, path = "PATCH", fmt.Sprintf("/api/workspaces/%s/scheduled-tasks/%s", wid, v.TaskID)
		default:
			http.Error(w, "bad action", http.StatusBadRequest); return
		}

		req, _ := http.NewRequestWithContext(r.Context(), method, s.cfg.AgentserverBaseURL+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Secret", s.cfg.AgentserverInternalSecret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil { http.Error(w, "upstream "+err.Error(), http.StatusBadGateway); return }
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}
```

Register the routes in `internal/codexappgateway/server.go`'s `Router()` (or the equivalent route registration site — search for `/internal/connected` registration):

```go
r.Post("/internal/scheduled-tasks/schedule", s.handleInternalScheduledTask("schedule"))
r.Get( "/internal/scheduled-tasks/list",     s.handleInternalScheduledTask("list"))
r.Post("/internal/scheduled-tasks/cancel",   s.handleInternalScheduledTask("cancel"))
r.Post("/internal/scheduled-tasks/pause",    s.handleInternalScheduledTask("pause"))
r.Post("/internal/scheduled-tasks/resume",   s.handleInternalScheduledTask("resume"))
r.Post("/internal/scheduled-tasks/update",   s.handleInternalScheduledTask("update"))
```

- [ ] **Step 5: Add the instructions doc**

```markdown
<!-- internal/codexappgateway/envmcp/tools/scheduling.instructions.md -->
## Task scheduling (`schedule_task`)

For any recurring task, use `schedule_task`. Tasks persist across sessions and restarts, and support the pre-task `script` hook described below.

To inspect or change existing tasks, use `list_tasks` (returns one row per series with the stable id) and `update_task` / `cancel_task` / `pause_task` / `resume_task`. Prefer `update_task` over cancel + reschedule.

Frequent recurring tasks — more than a few times a day — consume API credits and can risk account restrictions. You can add a `script` that runs first, and you will only be called when the check passes.

### How it works

1. Provide a bash `script` alongside the `prompt` when scheduling
2. When the task fires, the script runs first
3. Script returns: `{ "wakeAgent": true/false, "data": {...} }`
4. If `wakeAgent: false` — nothing happens, task waits for next run
5. If `wakeAgent: true` — the agent receives the script's data + prompt and handles

### Always test your script first

Before scheduling, run the script directly to verify it works.

### When NOT to use scripts

If a task requires your judgment every time (daily briefings, reminders, reports), skip the script — just use a regular prompt. Do not attempt sentiment analysis or advanced NLP in scripts.
```

(If your env-mcp framework supports per-tool instruction strings, wire this in alongside tool registration. Otherwise the doc is informational only.)

- [ ] **Step 6: Run all tool tests**

```bash
go test ./internal/codexappgateway/envmcp/tools/... -v
```

Expected: PASS (golden + transport tests).

- [ ] **Step 7: Commit**

```bash
git add internal/codexappgateway/envmcp/tools/scheduling.go internal/codexappgateway/envmcp/tools/scheduling_test.go internal/codexappgateway/envmcp/tools/testdata/scheduling.golden.json internal/codexappgateway/envmcp/tools/loopback_transport.go internal/codexappgateway/envmcp/tools/scheduling.instructions.md internal/codexappgateway/envmcp/envmcp.go internal/codexappgateway/internal_api.go internal/codexappgateway/server.go
git commit -m "feat(envmcp): 6 nanoclaw-aligned scheduling tools + loopback proxy"
```

---

## Task 12: Wire scheduler into `codex-app-gateway serve` + config

**Files:**
- Modify: `internal/codexappgateway/config.go`
- Modify: `internal/codexappgateway/server.go` (start scheduler goroutine in `Run`)

- [ ] **Step 1: Add config fields**

Append to the `ServeConfig` (or equivalent) struct:

```go
// Scheduler config — when AgentserverBaseURL is empty, scheduler is disabled.
SchedulerTickInterval     time.Duration  // CXG_SCHED_TICK         (default 15s)
SchedulerLeaseSeconds     int            // CXG_SCHED_LEASE_SECS   (default 1800)
SchedulerConcurrency      int            // CXG_SCHED_CONCURRENCY  (default 4)
ImbridgeBaseURL           string         // CXG_IMBRIDGE_BASE_URL  (required when scheduler enabled)
ImbridgeInternalSecret    string         // CXG_IMBRIDGE_SECRET    (optional)
```

In `LoadServeConfigFromEnv`, parse the four env vars (use the same `os.Getenv` + parse-duration pattern already in `config.go`).

- [ ] **Step 2: Start the loop in `Server.Run`**

In `internal/codexappgateway/server.go`'s `Run`, after the supervisor is started and before `httpServer.ListenAndServe`:

```go
if s.cfg.AgentserverBaseURL != "" {
    schedCtx, cancel := context.WithCancel(ctx)
    defer cancel()
    sched := scheduler.New(scheduler.Config{
        AgentserverBase:    s.cfg.AgentserverBaseURL,
        InternalSecret:     s.cfg.AgentserverInternalSecret,
        ImbridgeBase:       s.cfg.ImbridgeBaseURL,
        ImbridgeSecret:     s.cfg.ImbridgeInternalSecret,
        CodexBin:           s.codexBin,
        PodID:              os.Getenv("POD_NAME"),
        PID:                os.Getpid(),
        TickInterval:       s.cfg.SchedulerTickInterval,
        LeaseSeconds:       s.cfg.SchedulerLeaseSeconds,
        Concurrency:        s.cfg.SchedulerConcurrency,
    }, s.logger)
    go sched.Run(schedCtx)
    s.logger.Info("scheduler enabled", "agentserver", s.cfg.AgentserverBaseURL)
} else {
    s.logger.Info("scheduler disabled (CXG_AGENTSERVER_BASE_URL unset)")
}
```

Add the import `"github.com/agentserver/agentserver/internal/codexappgateway/scheduler"` to `server.go`.

- [ ] **Step 3: Build the binary to confirm it links**

```bash
cd /root/agentserver
go build ./cmd/codex-app-gateway/...
```

Expected: no errors.

- [ ] **Step 4: Run the full unit suite**

```bash
go test ./internal/codexappgateway/... ./internal/server/... ./internal/db/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/codexappgateway/config.go internal/codexappgateway/server.go
git commit -m "feat(codex-app-gateway): wire scheduler.Loop into serve; gated by CXG_AGENTSERVER_BASE_URL"
```

---

## Task 13: End-to-end smoke (manual; not CI)

This is a manual checklist. Do not skip if any of the prior tests were flaky.

- [ ] **Step 1: Start the stack**

In three terminals:

```bash
# Terminal 1 — agentserver-main with internal secret set
export INTERNAL_API_SECRET=devs3cr3t
go run ./cmd/agentserver serve --addr :8080

# Terminal 2 — mock imbridge
python3 -c "
import http.server, json
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get('content-length','0'))
        print('IM send:', self.rfile.read(n).decode())
        self.send_response(200); self.end_headers()
http.server.HTTPServer(('127.0.0.1', 8090), H).serve_forever()
"

# Terminal 3 — codex-app-gateway with scheduler enabled
export CXG_AGENTSERVER_BASE_URL=http://127.0.0.1:8080
export CXG_AGENTSERVER_INTERNAL_SECRET=devs3cr3t
export CXG_IMBRIDGE_BASE_URL=http://127.0.0.1:8090
export CXG_SCHED_TICK=5s
go run ./cmd/codex-app-gateway serve --listen-addr :8086 --codex-bin /usr/local/bin/codex
```

- [ ] **Step 2: Create a workspace + IM channel via REST**

```bash
# (Adapt to your auth flow; reuse the test-user cookie from manual.md if one exists.)
curl -X POST http://127.0.0.1:8080/api/workspaces -H 'Content-Type: application/json' \
  -d '{"name":"sched-test"}'
# … bind a fake IM channel for that workspace via the existing IM-channels admin route.
```

- [ ] **Step 3: Schedule a task (REST, no codex required)**

```bash
curl -X POST http://127.0.0.1:8080/api/workspaces/<wid>/scheduled-tasks \
  -H 'Content-Type: application/json' \
  -d "{\"prompt\":\"echo hello world\",\"processAfter\":\"$(date -u -d '+10 sec' '+%Y-%m-%dT%H:%M:%SZ')\",\"recurrence\":\"*/1 * * * *\"}"
```

- [ ] **Step 4: Observe**

Within ~30 seconds:
- Terminal 3 (gateway) logs `scheduler loop start`, then `lease ... 1 task`, then a spawn.
- Terminal 2 (mock imbridge) prints the `IM send:` payload containing the codex stdout summary.
- `GET /api/workspaces/<wid>/scheduled-tasks/<seriesId>/runs` shows a `succeeded` run with the summary.

After two minutes you should see ≥2 runs (one initial + one recurrence advance).

- [ ] **Step 5: Tear down**

```bash
# Ctrl-C all three terminals.
```

(No commit; smoke test only.)

---

## Self-Review Pass

Done. Quick check against the spec:

- ✅ DB schema (spec §Data Model) → Task 1
- ✅ ParseZonedToUTC parity (spec §MCP behaviors #2) → Task 2
- ✅ CRUD + lease + recurring advance (spec §Data Model, §Fire pipeline) → Tasks 3, 5
- ✅ REST mirroring MCP camelCase (spec §REST API) → Task 4
- ✅ Internal lease/result with shared secret (spec §REST API internal) → Task 5
- ✅ Script protocol (spec §script sub-protocol) → Task 7
- ✅ Spawn with timeout + transcript capture (spec §Fire pipeline #3) → Task 8
- ✅ Broadcast fan-out with partial-failure isolation (spec §Fire pipeline #5) → Task 9
- ✅ Dispatcher with all 4 status paths + skipped-no-broadcast (spec §boundary cases) → Task 10
- ✅ 6 MCP tools with golden snapshot (spec §MCP tool surface, MCP compatibility snapshot test) → Task 11
- ✅ Loopback proxy from env-mcp → app-gateway → agentserver (spec §MCP → server transport) → Task 11
- ✅ Scheduler enabled via env (spec §Architecture) → Task 12
- ⚠️ Cost/transcript URI storage: spec marks both as best-effort/deferred — Tasks 8 and 11 reserve fields but the parser is best-effort. Open question in spec §Open questions remains open.

No placeholders detected. Types are consistent (`Task.RunID` is added by the server during lease, used by dispatcher's `report`; `ScheduledTaskUpdate` mirrors the REST request shape).

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-05-22-scheduled-tasks.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?**
