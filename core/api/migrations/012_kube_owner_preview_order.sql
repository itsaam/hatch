ALTER TABLE kube_pull_request_events
    ADD COLUMN IF NOT EXISTS owner_approved_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_closed_at TIMESTAMPTZ;
