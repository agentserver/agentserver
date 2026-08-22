-- Codex permission is a session preference for the next turn.  Keep its
-- version independent from sessions.version: the latter also changes for
-- run lifecycle and title mutations and must not create spurious permission
-- CAS conflicts.
ALTER TABLE sessions
    ADD COLUMN permission_mode text NOT NULL DEFAULT 'read-only',
    ADD COLUMN permission_mode_version bigint NOT NULL DEFAULT 1,
    ADD CONSTRAINT sessions_permission_mode_valid CHECK (
        permission_mode IN ('read-only', 'auto', 'full-access')
    ),
    ADD CONSTRAINT sessions_permission_mode_version_positive CHECK (
        permission_mode_version BETWEEN 1 AND 9007199254740991
    );

-- A launch row is immutable authority for one run.  Nullable columns retain
-- the legacy distinction for rows created before session permission modes
-- existed; those runs continue to use the historical strict worker projection.
ALTER TABLE run_launch_states
    ADD COLUMN permission_mode text,
    ADD COLUMN permission_mode_version bigint,
    ADD CONSTRAINT run_launch_states_permission_mode_pair CHECK (
        (permission_mode IS NULL AND permission_mode_version IS NULL)
        OR
        (permission_mode IS NOT NULL
         AND permission_mode_version IS NOT NULL
         AND permission_mode IN ('read-only', 'auto', 'full-access')
         AND permission_mode_version BETWEEN 1 AND 9007199254740991)
    );
