-- A session's workspace binding is a logical executor authority.  The host
-- root is never persisted here; it is resolved from the registered
-- executor_environment identified by working_environment_id.
ALTER TABLE sessions
    ADD COLUMN working_environment_id uuid,
    ADD COLUMN working_directory text NOT NULL DEFAULT '.',
    ADD COLUMN working_directory_version bigint NOT NULL DEFAULT 1,
    ADD CONSTRAINT sessions_working_directory_bounded CHECK (
        pg_catalog.octet_length(working_directory) BETWEEN 1 AND 4096
        AND pg_catalog.left(working_directory, 1) <> '/'
		AND pg_catalog.strpos(working_directory, E'\\') = 0
		AND working_directory !~ '[[:cntrl:]]'
		AND pg_catalog.strpos(working_directory, '//') = 0
		AND (working_directory = '.' OR pg_catalog.right(working_directory, 1) <> '/')
		AND (working_directory = '.' OR working_directory !~ E'(^|/)(\\.|\\.\\.)(/|$)')
    ),
    ADD CONSTRAINT sessions_working_directory_version_positive CHECK (
        working_directory_version BETWEEN 1 AND 9007199254740991
    ),
    ADD CONSTRAINT sessions_working_environment_pair CHECK (
        working_environment_id IS NOT NULL OR working_directory = '.'
    );

-- A run launch row freezes the environment generation and descriptor digest in
-- addition to the session's independent CAS version.  All columns remain
-- nullable as a compatibility representation for runs created before this
-- authority existed.
ALTER TABLE run_launch_states
    ADD COLUMN workspace_environment_id uuid,
    ADD COLUMN workspace_environment_version bigint,
    ADD COLUMN workspace_root_sha256 bytea,
    ADD COLUMN workspace_working_directory text,
    ADD COLUMN workspace_working_directory_version bigint,
    ADD CONSTRAINT run_launch_states_workspace_binding_pair CHECK (
        (
            workspace_environment_id IS NULL
            AND workspace_environment_version IS NULL
            AND workspace_root_sha256 IS NULL
            AND workspace_working_directory IS NULL
            AND workspace_working_directory_version IS NULL
        )
        OR
        (
            workspace_environment_id IS NOT NULL
            AND workspace_environment_version BETWEEN 1 AND 9007199254740991
            AND pg_catalog.octet_length(workspace_root_sha256) = 32
            AND workspace_working_directory IS NOT NULL
            AND pg_catalog.octet_length(workspace_working_directory) BETWEEN 1 AND 4096
            AND workspace_working_directory_version BETWEEN 1 AND 9007199254740991
			AND pg_catalog.left(workspace_working_directory, 1) <> '/'
			AND pg_catalog.strpos(workspace_working_directory, E'\\') = 0
			AND workspace_working_directory !~ '[[:cntrl:]]'
			AND pg_catalog.strpos(workspace_working_directory, '//') = 0
			AND (workspace_working_directory = '.' OR pg_catalog.right(workspace_working_directory, 1) <> '/')
			AND (workspace_working_directory = '.' OR workspace_working_directory !~ E'(^|/)(\\.|\\.\\.)(/|$)')
        )
    ),
    ADD CONSTRAINT run_launch_states_workspace_root_sha256_exact CHECK (
        workspace_root_sha256 IS NULL
        OR pg_catalog.octet_length(workspace_root_sha256) = 32
    );
