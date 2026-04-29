# hatch-fastapi-postgres

Template de démarrage pour [Hatch](https://hatchpr.dev) — FastAPI + Postgres.

## Inclus

- FastAPI 0.115 + uvicorn
- Postgres 16 (alpine)
- Endpoints :
  - `GET /` — status
  - `GET /health` — healthcheck
  - `GET /db-check` — `SELECT 1` + compte les lignes de `preview_seed`
- Seed SQL automatique (`seed/preview.sql`) appliqué au boot par Hatch
- Dockerfile slim, healthcheck, dépendances minimales

## Fork & deploy

1. Fork ce repo sur ton GitHub.
2. Installe l'app GitHub Hatch sur ton fork.
3. Ouvre une PR (même triviale).
4. Hatch build, déploie, et te poste l'URL preview en commentaire.

Sur l'URL preview :

- `/` → `{"status":"ok"}`
- `/db-check` → confirme que le seed a bien été appliqué.

## Variables

`${DB_PASSWORD}` est injecté par Hatch (dérivé du secret webhook). Pas de config manuelle.
