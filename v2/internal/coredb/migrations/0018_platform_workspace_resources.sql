ALTER TABLE workspaces
    ADD COLUMN name text NOT NULL DEFAULT 'Untitled workspace';

ALTER TABLE workspaces
    DROP CONSTRAINT workspaces_status_valid;

UPDATE workspaces
SET status = 'archived'
WHERE status = 'deleted';

ALTER TABLE workspaces
    ADD CONSTRAINT workspaces_status_valid CHECK (
        status IN ('active', 'suspended', 'archived')
    ),
    ADD CONSTRAINT workspaces_name_bounded CHECK (
        pg_catalog.octet_length(name) BETWEEN 1 AND 256
    ),
    ADD CONSTRAINT workspaces_name_canonical CHECK (
        name = pg_catalog.btrim(name)
        AND name !~ '[[:cntrl:]]'
    );

CREATE INDEX workspace_members_workspace_role_user_idx
    ON workspace_members (workspace_id, role, user_id);
