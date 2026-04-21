CREATE TABLE IF NOT EXISTS build_logs (
  id BIGSERIAL PRIMARY KEY,
  repo_full_name TEXT NOT NULL,
  pr_number INT NOT NULL,
  commit_sha TEXT NOT NULL,
  service TEXT NOT NULL,
  raw_output TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'running',  -- running|success|failed
  error TEXT,
  started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_build_logs_preview
  ON build_logs (repo_full_name, pr_number, started_at DESC);
