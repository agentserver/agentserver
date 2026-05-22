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
	ID          string
	WorkspaceID string
	SeriesID    string
	CreatedBy   sql.NullString
	CreatorKind string

	Prompt string
	Script *string

	Timezone     string
	Recurrence   *string
	ProcessAfter time.Time

	Status         string
	Tries          int
	TimeoutSeconds int
	LeaseUntil     sql.NullTime
	LeaseOwner     sql.NullString

	LastRunID sql.NullString
	LastError sql.NullString

	CreatedAt time.Time
	UpdatedAt time.Time
}

type ScheduledTaskRun struct {
	ID            string
	TaskID        string
	SeriesID      string
	StartedAt     time.Time
	FinishedAt    sql.NullTime
	DurationMS    sql.NullInt64
	ExitCode      sql.NullInt64
	Status        string // succeeded|failed|timeout|skipped
	Summary       sql.NullString
	TranscriptURI sql.NullString
	CostUSD       sql.NullFloat64
	NumTurns      sql.NullInt64
	BroadcastTo   []string
	BroadcastErrors json.RawMessage
}

const scheduledTaskCols = `id, workspace_id, series_id, created_by, creator_kind,
	prompt, script, timezone, recurrence, process_after,
	status, tries, timeout_seconds, lease_until, lease_owner,
	last_run_id, last_error, created_at, updated_at`

func (db *DB) CreateScheduledTask(t *ScheduledTask) error {
	if t.CreatorKind == "" {
		t.CreatorKind = "mcp"
	}
	if t.Timezone == "" {
		t.Timezone = "UTC"
	}
	if t.Status == "" {
		t.Status = "pending"
	}
	if t.TimeoutSeconds == 0 {
		t.TimeoutSeconds = 600
	}
	if t.SeriesID == "" {
		t.SeriesID = t.ID
	}
	_, err := db.Exec(
		`INSERT INTO scheduled_tasks `+
			`(id, workspace_id, series_id, created_by, creator_kind, prompt, script, timezone, recurrence, process_after, status, timeout_seconds) `+
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
	if err == sql.ErrNoRows {
		return nil, nil
	}
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
	if err != nil {
		return nil, err
	}
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
		        tries       = tries + 1,
		        updated_at  = NOW()
		   FROM due
		  WHERE t.id = due.id
		  RETURNING `+scheduledTaskCols,
		limit, leaseSeconds, owner,
	)
	if err != nil {
		return nil, err
	}
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
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (db *DB) PauseScheduledSeries(wsID, seriesID string) (int, error) {
	res, err := db.Exec(
		`UPDATE scheduled_tasks SET status = 'paused', updated_at = NOW()
		  WHERE workspace_id = $1 AND series_id = $2 AND status = 'pending'`,
		wsID, seriesID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (db *DB) ResumeScheduledSeries(wsID, seriesID string) (int, error) {
	res, err := db.Exec(
		`UPDATE scheduled_tasks SET status = 'pending', updated_at = NOW()
		  WHERE workspace_id = $1 AND series_id = $2 AND status = 'paused'`,
		wsID, seriesID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

type ScheduledTaskUpdate struct {
	Prompt       *string
	Script       *string    // nil = unchanged; *"" = clear (DB NULL)
	Recurrence   *string    // nil = unchanged; *"" = clear (DB NULL)
	ProcessAfter *time.Time
}

func (db *DB) UpdateScheduledSeries(wsID, seriesID string, u ScheduledTaskUpdate) (int, error) {
	sets := []string{"updated_at = NOW()"}
	args := []any{wsID, seriesID}
	add := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if u.Prompt != nil {
		add("prompt", *u.Prompt)
	}
	if u.Script != nil {
		if *u.Script == "" {
			sets = append(sets, "script = NULL")
		} else {
			add("script", *u.Script)
		}
	}
	if u.Recurrence != nil {
		if *u.Recurrence == "" {
			sets = append(sets, "recurrence = NULL")
		} else {
			add("recurrence", *u.Recurrence)
		}
	}
	if u.ProcessAfter != nil {
		add("process_after", u.ProcessAfter.UTC())
	}
	if len(sets) == 1 {
		return 0, nil
	} // only updated_at — caller passed nothing
	q := `UPDATE scheduled_tasks SET ` + strings.Join(sets, ", ") +
		` WHERE workspace_id = $1 AND series_id = $2 AND status IN ('pending','paused')`
	res, err := db.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// CreateScheduledTaskRun inserts a run row with a placeholder status of 'succeeded'.
// The scheduled_task_runs CHECK constraint has no 'running' value, so we pick a
// schema-legal placeholder; FinalizeRunAndAdvance overwrites it with the real outcome.
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
	RunID, TaskID                string
	Status                       string // succeeded|failed|timeout|skipped
	ExitCode                     int
	DurationMS                   int64
	Summary, TranscriptURI       string
	CostUSD                      *float64
	NumTurns                     *int
	BroadcastTo                  []string
	BroadcastErrors              json.RawMessage
}

func (db *DB) FinalizeRunAndAdvance(in FinalizeRunInput, nextAfter *time.Time, newID string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
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
	if err != nil {
		return err
	}

	task := &ScheduledTask{}
	err = tx.QueryRow(`SELECT `+scheduledTaskCols+` FROM scheduled_tasks WHERE id = $1 FOR UPDATE`, in.TaskID).
		Scan(&task.ID, &task.WorkspaceID, &task.SeriesID, &task.CreatedBy, &task.CreatorKind,
			&task.Prompt, &task.Script, &task.Timezone, &task.Recurrence, &task.ProcessAfter,
			&task.Status, &task.Tries, &task.TimeoutSeconds, &task.LeaseUntil, &task.LeaseOwner,
			&task.LastRunID, &task.LastError, &task.CreatedAt, &task.UpdatedAt)
	if err != nil {
		return err
	}

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
		); err != nil {
			return err
		}

		if _, err = tx.Exec(
			`UPDATE scheduled_tasks
			    SET status='completed', recurrence=NULL,
			        lease_until=NULL, lease_owner=NULL,
			        last_run_id=$2, updated_at=NOW()
			  WHERE id=$1`, in.TaskID, in.RunID); err != nil {
			return err
		}
	} else {
		newStatus := "completed"
		if in.Status == "failed" || in.Status == "timeout" {
			newStatus = "failed"
		}
		if _, err = tx.Exec(
			`UPDATE scheduled_tasks
			    SET status=$2, lease_until=NULL, lease_owner=NULL,
			        last_run_id=$3, updated_at=NOW()
			  WHERE id=$1`, in.TaskID, newStatus, in.RunID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) ListScheduledTaskRuns(taskID string, limit int) ([]ScheduledTaskRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.Query(
		`SELECT id, task_id, series_id, started_at, finished_at, duration_ms, exit_code,
		        status, summary, transcript_uri, cost_usd, num_turns,
		        broadcast_to, broadcast_errors
		   FROM scheduled_task_runs WHERE task_id = $1 ORDER BY started_at DESC LIMIT $2`,
		taskID, limit,
	)
	if err != nil {
		return nil, err
	}
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
