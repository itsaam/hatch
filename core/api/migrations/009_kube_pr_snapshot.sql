ALTER TABLE kube_pull_request_events
    ADD COLUMN IF NOT EXISTS head_sha TEXT NOT NULL DEFAULT '',
    DROP COLUMN IF EXISTS event_at;
