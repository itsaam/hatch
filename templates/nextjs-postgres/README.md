# nextjs-postgres — Hatch template

Minimal Next.js 15 (App Router) + Postgres preview template for [Hatch](https://github.com/hatchpr).

## Stack

- Next.js 15 (App Router, standalone output)
- React 19, TypeScript
- Postgres 16 (alpine) via `pg`

## Routes

- `/` — Server Component landing
- `/db-check` — runs `SELECT 1` against Postgres
- `/api/health` — `{ ok: true }` (used by Hatch healthcheck)

## Deploy on Hatch

1. Fork this repo (or use it as a template).
2. In Hatch, set the secret `DB_PASSWORD` for your project.
3. Push a branch / open a PR — Hatch builds and exposes a preview URL.

```bash
git checkout -b feature/test
git commit --allow-empty -m "test preview"
git push origin feature/test
```

Hatch reads `.hatch.yml`, builds the `web` image, spins up a fresh Postgres,
and exposes the preview at `pr-<n>-<project>.preview.<your-domain>`.

## Local dev

```bash
npm install
DATABASE_URL=postgres://app:pass@localhost:5432/app npm run dev
```
