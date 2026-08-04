ALTER TABLE sessions
    ADD COLUMN creator_id uuid,
    ADD COLUMN title text NOT NULL DEFAULT 'New conversation',
    ADD COLUMN status text NOT NULL DEFAULT 'active';

-- Existing development and production bootstrap sessions predate first-class
-- Browser session ownership. Prefer their first run actor, then the recorded
-- production bootstrap owner, and finally the oldest current workspace
-- member. Refuse to finish the migration if no durable owner can be proven.
UPDATE sessions AS session
SET creator_id = COALESCE(
    (
        SELECT run.actor_id
        FROM runs AS run
        WHERE run.session_id = session.id
        ORDER BY run.created_at, run.id
        LIMIT 1
    ),
    (
        SELECT seed.user_id
        FROM production_bootstrap_seeds AS seed
        WHERE seed.session_id = session.id
          AND seed.workspace_id = session.workspace_id
        LIMIT 1
    ),
    (
        SELECT member.user_id
        FROM workspace_members AS member
        WHERE member.workspace_id = session.workspace_id
        ORDER BY member.created_at, member.user_id
        LIMIT 1
    )
)
WHERE session.creator_id IS NULL;

ALTER TABLE sessions
    ALTER COLUMN creator_id SET NOT NULL,
    ADD CONSTRAINT sessions_creator_fk
        FOREIGN KEY (creator_id) REFERENCES users(id),
    ADD CONSTRAINT sessions_status_valid CHECK (
        status IN ('active', 'archived')
    ),
    ADD CONSTRAINT sessions_title_bounded CHECK (
        pg_catalog.octet_length(title) BETWEEN 1 AND 256
    ),
    ADD CONSTRAINT sessions_title_canonical CHECK (
        title = pg_catalog.btrim(title)
        AND title !~ '[[:cntrl:]]'
    );

CREATE INDEX sessions_creator_activity_idx
    ON sessions (workspace_id, creator_id, status, updated_at DESC, id);
