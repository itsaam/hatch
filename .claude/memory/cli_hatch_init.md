---
name: CLI `hatch init`
description: Binaire Go qui scaffold un .hatch.yml auto-détecté selon le stack du projet
type: reference
---

## Emplacement

`/Users/saam/hatch/cli/` — module Go indépendant (pas de dépendance à `core/api`).

## Build

```bash
cd /Users/saam/hatch/cli
go build -o hatch .                              # local
go build -ldflags="-s -w" -o hatch .             # release stripped (~3.4 MB)

# Cross-compile
GOOS=linux   GOARCH=amd64 go build -o hatch-linux-amd64 .
GOOS=darwin  GOARCH=arm64 go build -o hatch-darwin-arm64 .
```

Dépendances externes : `gopkg.in/yaml.v3`, `github.com/charmbracelet/lipgloss`, `golang.org/x/term`.

## Usage

```bash
hatch init                      # scan + prompt Y/n + écriture
hatch init --dry-run            # affiche YAML, pas d'écriture, pas de prompt
hatch init --force              # écrase un .hatch.yml existant
hatch init --yes                # skip le prompt (CI-friendly)
hatch init -y                   # idem (short)
hatch init --output custom.yml  # chemin custom
hatch init --verbose            # détails de détection
hatch init --no-animation       # désactive le délai 80ms entre steps
```

## Architecture

- `main.go` — flags, orchestration, prompt confirm (bufio.NewReader sur stdin)
- `detect.go` — dispatch par priorité :
  1. `docker-compose.yml` présent → convert vers `.hatch.yml`
  2. `package.json` → Next.js / Node + détection deps (next-auth, prisma, stripe, redis clients, resend, openai, s3/r2)
  3. `Gemfile` → Rails + Sidekiq/pg/redis
  4. `requirements.txt` / `pyproject.toml` → Python (Django/FastAPI)
  5. `Dockerfile` seul → fallback stateless
  6. Structure Hatch (landing/ + core/api/) → cas edge landing-only
- `envparse.go` — parse .env.example / .env.sample / .env.template, classifie chaque var :
  - URLs dynamiques (NEXTAUTH_URL, APP_URL, etc.) → `${PREVIEW_URL}`
  - Secrets sensibles (regex `(?i)(secret|token|api[_-]?key|password|passwd|private[_-]?key|client[_-]?secret|webhook[_-]?secret)`) → dummy + commentaire `# provide via ${SECRET_<NAME>}`
  - Auth secrets (NEXTAUTH_SECRET, AUTH_SECRET, JWT_SECRET) → `${DB_PASSWORD}` (stable déterministe)
  - Booleans email (EMAILS_ENABLED) → `"false"` par défaut
  - DATABASE_URL / postgres://...@db/... → réécrit avec `${DB_PASSWORD}`
  - REDIS_URL → `redis://redis:6379`
  - NODE_ENV → `production`
  - PORT → quoté `"3000"`
- `generate.go` — émission YAML manuelle (ordre stable, quoting défensif des valeurs numériques)
- `ui.go` — lipgloss, palette Hatch (`#FF7A3D` orange, `#7FD99C` vert, `#6E6355` muted, `#E57373` rouge)
  - Header boxed
  - Steps animés 80 ms (fallback plain si pas TTY ou `--no-animation`)
  - Summary box (title + lines)
  - DryRunBlock : YAML sans box, séparateur horizontal, syntax highlight léger (keys en accent, comments en muted)

## Services auto-détectés via package.json

| Dep détectée                                          | Action                                                |
|-------------------------------------------------------|-------------------------------------------------------|
| `next`                                                | Stack Next.js, port 3000                              |
| `@prisma/client`, `prisma`                            | Ajoute service db Postgres + DATABASE_URL             |
| `pg`, `mongoose`, `mysql2`                            | Ajoute service db                                     |
| `ioredis`, `redis`, `@upstash/redis`                  | Ajoute service redis + REDIS_URL + healthcheck        |
| `next-auth`, `@auth/*`                                | Force NEXTAUTH_URL=${PREVIEW_URL}, NEXTAUTH_SECRET=${DB_PASSWORD} |
| `stripe`                                              | Dummies sk_test_/whsec_preview_dummy                  |
| `resend`, `@sendgrid/*`, `nodemailer`                 | EMAILS_ENABLED="false"                                |
| `openai`, `@anthropic-ai/*`                           | Commentaire "# provide via ${SECRET_OPENAI_API_KEY}"  |
| `@aws-sdk/client-s3`, `@cloudflare/workers-types`     | Commentaire secret nécessaire                         |

## Règles importantes

- Sans `--force`, le CLI refuse d'écraser un `.hatch.yml` existant et renvoie un code 1 avec message clair
- Un `Dockerfile` manquant est **signalé en warning** après l'écriture, jamais créé automatiquement
- Aucun fichier autre que `.hatch.yml` n'est écrit
- En mode non-interactif (stdin pas TTY, pipe, CI) le prompt Y/n auto-accepte

## Tests

`cd cli && go test ./...` — 10+ tests :
- Détection compose, Next.js + Prisma, Gemfile Rails, Dockerfile seul, cas Hatch repo
- Parse .env.example avec cas mixtes (secrets, URLs, booleans)
- Détection Redis + NextAuth ensemble
- Détection Stripe seul
- Round-trip generate (parseable par yaml.v3)

## Installation système (pour partager avec des potes)

```bash
cd /Users/saam/hatch/cli && go install .
# → ~/go/bin/hatch disponible partout
```

Ou distribuer le binaire compilé directement (~3.4 MB).
