CREATE TABLE IF NOT EXISTS kube_github_deliveries (
    id BIGSERIAL PRIMARY KEY,
    repo_full_name TEXT NOT NULL,
    pr_number INTEGER NOT NULL,
    installation_id BIGINT NOT NULL,
    commit_sha TEXT NOT NULL,
    closed BOOLEAN NOT NULL,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    mismatches SMALLINT NOT NULL DEFAULT 0,
    UNIQUE (repo_full_name, pr_number, commit_sha, closed)
);
CREATE INDEX IF NOT EXISTS idx_kube_github_deliveries_available
    ON kube_github_deliveries (available_at, id);

CREATE TABLE IF NOT EXISTS kube_chatops_commands (
    comment_id BIGINT PRIMARY KEY
);
