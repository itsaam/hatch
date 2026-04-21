CREATE TABLE IF NOT EXISTS repo_secrets (
  id BIGSERIAL PRIMARY KEY,
  repo_full_name TEXT NOT NULL,
  name TEXT NOT NULL,
  value_encrypted BYTEA NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(repo_full_name, name)
);
CREATE INDEX IF NOT EXISTS idx_repo_secrets_repo ON repo_secrets(repo_full_name);
