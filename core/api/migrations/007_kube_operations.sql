CREATE TABLE IF NOT EXISTS kube_operations (
    id              BIGSERIAL NOT NULL UNIQUE,
    repo_full_name  TEXT NOT NULL,
    pr_number       INTEGER NOT NULL,
    operation       TEXT NOT NULL CHECK (operation IN ('deploy', 'close', 'delete', 'expire', 'failed_cleanup')),
    available_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expiry_days     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (repo_full_name, pr_number),
    FOREIGN KEY (repo_full_name, pr_number) REFERENCES previews (repo_full_name, pr_number) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_kube_operations_available ON kube_operations (available_at, id);
