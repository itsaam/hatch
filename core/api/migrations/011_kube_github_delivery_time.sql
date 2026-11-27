ALTER TABLE kube_github_deliveries
    ADD COLUMN IF NOT EXISTS source_updated_at TIMESTAMPTZ;
