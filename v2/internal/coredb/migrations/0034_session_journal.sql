-- A session-wide commit-ordered journal. Permission changes are not run
-- events: they must remain observable even before the first prompt. Allocate
-- positions under a per-session row lock (not a SQL sequence, whose commits
-- can arrive out of order). Source mutations and journal entries commit or
-- roll back together. The allocator never locks sessions/runs in reverse.
LOCK TABLE sessions, runs, run_launch_states, run_events IN SHARE ROW EXCLUSIVE MODE;

CREATE TABLE session_journal_heads (
    session_id uuid PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    next_seq bigint NOT NULL CHECK (next_seq BETWEEN 1 AND 9007199254740991)
);

CREATE TABLE session_journal (
    session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    seq bigint NOT NULL CHECK (seq BETWEEN 1 AND 9007199254740990),
    kind text NOT NULL CHECK (kind IN ('prompt', 'run_event', 'permission')),
    run_id uuid REFERENCES runs(id) ON DELETE CASCADE,
    run_seq bigint,
    permission_mode text CHECK (permission_mode IN ('read-only', 'auto', 'full-access')),
    permission_version bigint,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (session_id, seq),
    UNIQUE (run_id, run_seq),
    UNIQUE (session_id, permission_version),
    CHECK (
        (kind = 'prompt' AND run_id IS NOT NULL AND run_seq IS NOT NULL AND run_seq = 0 AND permission_mode IS NULL AND permission_version IS NULL)
        OR (kind = 'run_event' AND run_id IS NOT NULL AND run_seq IS NOT NULL AND run_seq > 0 AND permission_mode IS NULL AND permission_version IS NULL)
        OR (kind = 'permission' AND run_id IS NULL AND run_seq IS NULL AND permission_mode IS NOT NULL AND permission_version IS NOT NULL AND permission_version > 0)
    )
);

-- Preserve the existing run/prompt replay prefix. Only the current permission
-- checkpoint is known for old sessions; append it at the tail, never invent
-- historical permission changes or insert entries into an observed prefix.
INSERT INTO session_journal (session_id, seq, kind, run_id, run_seq, created_at)
SELECT session_id, row_number() OVER (PARTITION BY session_id ORDER BY run_created_at, run_id, run_seq),
       kind, run_id, run_seq, created_at
FROM (
    SELECT r.session_id, r.created_at AS run_created_at, r.id AS run_id,
           0::bigint AS run_seq, 'prompt' AS kind, r.created_at
    FROM runs r JOIN run_launch_states l ON l.run_id = r.id
    UNION ALL
    SELECT r.session_id, r.created_at, r.id, e.seq, 'run_event', e.created_at
    FROM runs r JOIN run_launch_states l ON l.run_id = r.id JOIN run_events e ON e.run_id = r.id
) source;

INSERT INTO session_journal (session_id, seq, kind, permission_mode, permission_version, created_at)
SELECT s.id, COALESCE(max(j.seq), 0) + 1, 'permission', s.permission_mode, s.permission_mode_version, s.updated_at
FROM sessions s LEFT JOIN session_journal j ON j.session_id = s.id GROUP BY s.id;

INSERT INTO session_journal_heads (session_id, next_seq)
SELECT session_id, max(seq) + 1 FROM session_journal GROUP BY session_id;

CREATE FUNCTION next_session_journal_seq(target uuid) RETURNS bigint
LANGUAGE sql SET search_path FROM CURRENT AS $$
    INSERT INTO session_journal_heads (session_id, next_seq) VALUES (target, 2)
    ON CONFLICT (session_id) DO UPDATE SET next_seq = session_journal_heads.next_seq + 1
    RETURNING next_seq - 1;
$$;

CREATE FUNCTION journal_session_permission() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
    IF TG_OP = 'INSERT' OR NEW.permission_mode IS DISTINCT FROM OLD.permission_mode THEN
        INSERT INTO session_journal (session_id, seq, kind, permission_mode, permission_version, created_at)
        VALUES (NEW.id, next_session_journal_seq(NEW.id), 'permission', NEW.permission_mode, NEW.permission_mode_version, NEW.updated_at);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER session_permission_journal AFTER INSERT OR UPDATE OF permission_mode ON sessions
FOR EACH ROW EXECUTE FUNCTION journal_session_permission();

CREATE FUNCTION journal_session_prompt() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE source runs;
BEGIN
    SELECT * INTO STRICT source FROM runs WHERE id = NEW.run_id;
    INSERT INTO session_journal (session_id, seq, kind, run_id, run_seq, created_at)
    VALUES (source.session_id, next_session_journal_seq(source.session_id), 'prompt', source.id, 0, source.created_at);
    RETURN NEW;
END;
$$;
CREATE TRIGGER session_prompt_journal AFTER INSERT ON run_launch_states
FOR EACH ROW EXECUTE FUNCTION journal_session_prompt();

CREATE FUNCTION journal_session_run_event() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE target uuid;
BEGIN
    SELECT session_id INTO STRICT target FROM runs WHERE id = NEW.run_id;
    INSERT INTO session_journal (session_id, seq, kind, run_id, run_seq, created_at)
    VALUES (target, next_session_journal_seq(target), 'run_event', NEW.run_id, NEW.seq, NEW.created_at);
    RETURN NEW;
END;
$$;
CREATE TRIGGER session_run_event_journal AFTER INSERT ON run_events
FOR EACH ROW EXECUTE FUNCTION journal_session_run_event();
