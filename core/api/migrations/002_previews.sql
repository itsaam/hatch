CREATE TABLE IF NOT EXISTS previews (
    id              BIGSERIAL PRIMARY KEY,
    repo_full_name  TEXT        NOT NULL,
    pr_number       INTEGER     NOT NULL,
    branch          TEXT        NOT NULL,
    commit_sha      TEXT        NOT NULL,
    status          TEXT        NOT NULL,
    url             TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (repo_full_name, pr_number)
);

CREATE INDEX IF NOT EXISTS idx_previews_status ON previews (status);
CREATE INDEX IF NOT EXISTS idx_previews_updated_at ON previews (updated_at DESC);
