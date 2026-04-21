# hatch CLI

Zero-config `.hatch.yml` generator.

## Install

### npm (recommended for Node devs)
```bash
npx @hatchpr-preview/cli init
# or install globally:
npm install -g @hatchpr-preview/cli
```

### curl (macOS/Linux)
```bash
curl -fsSL https://hatchpr.dev/install.sh | sh
```

### Go install (if you have Go)
```bash
go install github.com/itsaam/hatch/cli@latest
```

### From source
```bash
git clone https://github.com/itsaam/hatch && cd hatch/cli && go build -o hatch .
```

Cross-compile:

```sh
GOOS=linux  GOARCH=amd64 go build -ldflags="-s -w" -o dist/hatch-linux-amd64 .
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o dist/hatch-darwin-arm64 .
```

## Usage

```sh
cd your-repo
hatch init                 # detect stack + write .hatch.yml
hatch init --dry-run       # print to stdout, don't write
hatch init --force         # overwrite an existing .hatch.yml
hatch init --output foo.yml
hatch init --verbose
```

## Supported stacks

| Signal | Detected as |
|---|---|
| `docker-compose.yml` | best-effort compose → hatch mapping |
| `package.json` with `next` + `prisma`/`pg` | Next.js + Postgres |
| `package.json` with `next` | Next.js |
| `package.json` with `express`/`fastify` | Node API (+ Postgres if `pg`) |
| `Gemfile` with `rails` + `pg` + `redis` + `sidekiq` | Rails + Postgres + Redis + Sidekiq worker |
| `requirements.txt`/`pyproject.toml` with `fastapi`/`django`/`flask` | Python API (+ Postgres/Redis) |
| `Dockerfile` only | single `web` service |
| `landing/` + `core/api/` | Hatch repo, landing-only preview |

## Env scrubbing

Keys matching `(?i)(secret|token|api[_-]?key|password|passwd|private[_-]?key)` get their value replaced by `${SECRET_<KEY>}`. Postgres URLs get their password replaced by `${DB_PASSWORD}`.
