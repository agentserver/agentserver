CREATE INDEX executions_executor_status_created_idx
    ON executions (executor_id, status, created_at, id)
    WHERE status IN ('dispatching', 'running', 'cancelling');
