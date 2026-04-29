CREATE TABLE IF NOT EXISTS preview_seed (
  id SERIAL PRIMARY KEY,
  label TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO preview_seed (label) VALUES
  ('hello from hatch'),
  ('preview deployment ready');
