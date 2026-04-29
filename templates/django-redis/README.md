# hatch-django-redis

Template de démarrage pour [Hatch](https://hatchpr.dev) — Django 5 + Redis (cache).

## Inclus

- Django 5.1 + gunicorn
- Redis 7 (alpine) configuré comme backend de cache
- Endpoints :
  - `GET /` — status
  - `GET /health` — healthcheck
  - `GET /cache-check` — set/get/incr Redis pour prouver la connexion
- DB SQLite locale (suffisant pour la démo) — pour Postgres voir le template `fastapi-postgres`

## Fork & deploy

1. Fork ce repo.
2. Installe l'app GitHub Hatch sur ton fork.
3. Crée un secret repo `DJANGO_SECRET_KEY` (ex: `python -c "import secrets;print(secrets.token_urlsafe(50))"`).
4. Ouvre une PR. Hatch build, deploy, te commente l'URL.

Sur l'URL preview :

- `/` → status JSON
- `/cache-check` → confirme que Redis répond.

## Variables

- `${SECRET_DJANGO_SECRET_KEY}` — injecté depuis le secret repo (Hatch).
- `REDIS_URL` — pointé sur le service `cache` du compose.
