-- Documents the 'expired' status used by the TTL reaper for previews
-- that have been hibernated after a prolonged inactivity window.
-- The `status` column is TEXT so no schema change is required.
COMMENT ON COLUMN previews.status IS
    'Lifecycle: pending | building | running | failed | closed | expired';
