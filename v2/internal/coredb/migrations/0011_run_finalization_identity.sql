ALTER TABLE run_attempts
    ADD COLUMN terminal_thread_id text,
    ADD COLUMN terminal_turn_id text,
    ADD CONSTRAINT run_attempts_terminal_identity_complete CHECK (
        (terminal_thread_id IS NULL AND terminal_turn_id IS NULL)
        OR
        (terminal_thread_id IS NOT NULL AND terminal_turn_id IS NOT NULL)
    ),
    ADD CONSTRAINT run_attempts_terminal_thread_bounded CHECK (
        terminal_thread_id IS NULL
        OR pg_catalog.octet_length(terminal_thread_id) BETWEEN 1 AND 256
    ),
    ADD CONSTRAINT run_attempts_terminal_turn_bounded CHECK (
        terminal_turn_id IS NULL
        OR pg_catalog.octet_length(terminal_turn_id) BETWEEN 1 AND 256
    );
