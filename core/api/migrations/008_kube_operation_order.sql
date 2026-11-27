ALTER TABLE kube_operations
    DROP CONSTRAINT IF EXISTS kube_operations_operation_check,
    ADD CONSTRAINT kube_operations_operation_check
    CHECK (operation IN ('deploy', 'close', 'delete', 'expire', 'failed_cleanup'));

CREATE TABLE IF NOT EXISTS kube_pull_request_events (
    repo_full_name TEXT NOT NULL,
    pr_number      INTEGER NOT NULL,
    event_at       TIMESTAMPTZ NOT NULL,
    closed         BOOLEAN NOT NULL,
    PRIMARY KEY (repo_full_name, pr_number)
);
