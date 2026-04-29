# hatch-rails-sidekiq

Template de démarrage pour [Hatch](https://hatchpr.dev) — Rails 7 (API) + Sidekiq + Postgres + Redis.

## Inclus

- Rails 7.1 (API-only) + Puma
- Sidekiq 7 (worker séparé via `RUN_MODE=worker`)
- Postgres 16 (alpine) — DB applicative
- Redis 7 (alpine) — broker Sidekiq + cache
- Endpoints :
  - `GET /` — status
  - `GET /health` — healthcheck
  - `GET /db-check` — `SELECT 1` + count `preview_seed`
  - `GET /cache-check` — set/get/incr Redis
  - `GET /enqueue` — push un job `PingWorker` dans Sidekiq (logs visibles côté worker)
- Seed SQL (`seed/preview.sql`) appliqué automatiquement par Hatch

## Architecture des services

```
web     → Puma, sert l'API (expose: true)
worker  → Sidekiq, même image, RUN_MODE=worker
db      → Postgres
redis   → Redis (Sidekiq + cache)
```

## Fork & deploy

1. Fork ce repo.
2. Installe l'app GitHub Hatch sur ton fork.
3. Crée un secret repo `RAILS_SECRET_KEY_BASE` (ex: `bundle exec rails secret` ou `openssl rand -hex 64`).
4. Ouvre une PR. Hatch build, deploy, te commente l'URL.

Pour vérifier Sidekiq : visite `/enqueue` puis regarde les logs du service `worker` dans le dashboard Hatch — tu verras `[PingWorker] received ts=...`.

## Variables

- `${SECRET_RAILS_SECRET_KEY_BASE}` — depuis le secret repo.
- `${DB_PASSWORD}` — injecté par Hatch.
