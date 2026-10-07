ALTER TABLE sessions
    ADD COLUMN title_source text NOT NULL DEFAULT 'placeholder' CHECK (title_source IN ('placeholder','manual','fallback','generated')),
    ADD COLUMN title_version bigint NOT NULL DEFAULT 1 CHECK (title_version BETWEEN 1 AND 9007199254740991);
UPDATE sessions SET title_source='manual' WHERE title NOT IN ('New session','New conversation');

ALTER TABLE session_journal
    DROP CONSTRAINT session_journal_kind_check,
    DROP CONSTRAINT session_journal_check,
    ADD COLUMN title text,
    ADD COLUMN title_source text,
    ADD COLUMN title_version bigint,
    ADD CONSTRAINT session_journal_kind_check CHECK (kind IN ('prompt','run_event','permission','title')),
    ADD CONSTRAINT session_journal_shape CHECK (
      (kind='prompt' AND run_id IS NOT NULL AND run_seq IS NOT NULL AND run_seq=0 AND permission_mode IS NULL AND permission_version IS NULL AND title IS NULL AND title_source IS NULL AND title_version IS NULL)
      OR (kind='run_event' AND run_id IS NOT NULL AND run_seq IS NOT NULL AND run_seq>0 AND permission_mode IS NULL AND permission_version IS NULL AND title IS NULL AND title_source IS NULL AND title_version IS NULL)
      OR (kind='permission' AND run_id IS NULL AND run_seq IS NULL AND permission_mode IS NOT NULL AND permission_version IS NOT NULL AND permission_version>0 AND title IS NULL AND title_source IS NULL AND title_version IS NULL)
      OR (kind='title' AND run_id IS NULL AND run_seq IS NULL AND permission_mode IS NULL AND permission_version IS NULL AND title IS NOT NULL AND title_source IS NOT NULL AND title_source IN ('placeholder','manual','fallback','generated') AND title_version IS NOT NULL AND title_version>0)
    ),
    ADD UNIQUE (session_id,title_version);

INSERT INTO session_journal (session_id,seq,kind,title,title_source,title_version,created_at)
SELECT id,next_session_journal_seq(id),'title',title,title_source,title_version,updated_at FROM sessions;

CREATE FUNCTION journal_session_title() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
    IF TG_OP='INSERT' OR NEW.title_version IS DISTINCT FROM OLD.title_version THEN
        INSERT INTO session_journal (session_id,seq,kind,title,title_source,title_version,created_at)
        VALUES (NEW.id,next_session_journal_seq(NEW.id),'title',NEW.title,NEW.title_source,NEW.title_version,NEW.updated_at);
    END IF;
    RETURN NEW;
END;
$$;
-- During a rolling deployment an older Core may still rename a title without
-- the new source/version fields. Preserve that explicit rename as manual and
-- advance its journal watermark, rather than allowing a late auto-title to win.
CREATE FUNCTION version_legacy_session_title() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
    IF NEW.title IS DISTINCT FROM OLD.title AND NEW.title_version = OLD.title_version THEN
        NEW.title_version = OLD.title_version + 1;
        NEW.title_source = 'manual';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER session_title_legacy_version BEFORE UPDATE OF title ON sessions
FOR EACH ROW EXECUTE FUNCTION version_legacy_session_title();

CREATE TRIGGER session_title_journal AFTER INSERT OR UPDATE OF title, title_source, title_version ON sessions
FOR EACH ROW EXECUTE FUNCTION journal_session_title();
