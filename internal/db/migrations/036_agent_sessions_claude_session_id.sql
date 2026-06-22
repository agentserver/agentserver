-- 036_agent_sessions_claude_session_id.sql
-- Phase 4 (cc-app-gateway IM intake): cc-app-gateway requires pure-UUID
-- sessionId per Phase 1's turn_api.go uuidRe validation. agent_sessions.id
-- uses "cse_<uuid>" format which doesn't satisfy that regex; this column
-- holds the cc-app-gateway-compatible session identifier. Nullable; only
-- populated for rows that use the managed_cc routing mode.
ALTER TABLE agent_sessions ADD COLUMN IF NOT EXISTS claude_session_id TEXT;

-- Reverse-lookup index for Phase 5+ (NOT used in Phase 4).
CREATE INDEX IF NOT EXISTS idx_agent_sessions_claude_session_id
    ON agent_sessions (claude_session_id)
    WHERE claude_session_id IS NOT NULL;
