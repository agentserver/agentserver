ALTER TABLE proxy_tokens
  ADD COLUMN IF NOT EXISTS user_id TEXT REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_proxy_tokens_user
  ON proxy_tokens (user_id)
  WHERE user_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_proxy_tokens_workspace_user
  ON proxy_tokens (workspace_id, user_id)
  WHERE user_id IS NOT NULL;
