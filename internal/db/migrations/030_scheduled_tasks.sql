-- internal/db/migrations/030_scheduled_tasks.sql
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
