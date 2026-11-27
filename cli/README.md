# hatch CLI

Generate a starting `.hatch.yml` from an existing repository.

## Install

### npm (recommended for Node devs)
```bash
npx @hatchpr/cli init
# or install globally:
npm install -g @hatchpr/cli
```

### curl (macOS/Linux)
```bash
curl -fsSL https://hatchpr.dev/install.sh | sh
```

### From source with Go
```bash
git clone https://github.com/itsaam/hatch && cd hatch/cli && go build -o hatch .
```

Use `go build -o hatch` to name the executable `hatch`. Running `go install`
on the `github.com/itsaam/hatch/cli` module installs a binary named `cli`.

Cross-compile:

```sh
GOOS=linux  GOARCH=amd64 go build -ldflags="-s -w" -o dist/hatch-linux-amd64 .
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o dist/hatch-darwin-arm64 .
```

## Usage

```sh
cd your-repo
hatch init                 # detect stack + write .hatch.yml
hatch init --dry-run       # print only YAML to stdout, don't write a file
hatch init --force         # overwrite an existing .hatch.yml
hatch init --output foo.yml
hatch init --verbose
```

Review the generated configuration before committing it. Build tools can pipe
`--dry-run` into a YAML parser. Warnings and verbose output go to stderr.
Open a PR and comment `@hatchpr preview` on its own to request a preview.

If you use release `v0.1.4`, build from source for machine-readable dry runs.
That release includes progress text on stdout.

## Supported stacks

| Signal | Detected as |
|---|---|
| `docker-compose.yml` | Best-effort Compose conversion. Review ports and move custom startup commands into Dockerfiles. Nested build contexts require the Kubernetes runtime. |
| `package.json` with `next` + `prisma`/`pg` | Next.js + Postgres |
| `package.json` with `next` | Next.js |
| `package.json` with `express`/`fastify` | Node API (+ Postgres if `pg`) |
| `Gemfile` with `rails` + `pg` + `redis` + `sidekiq` | Rails + Postgres + Redis + Sidekiq worker |
| `requirements.txt`/`pyproject.toml` with `fastapi`/`django`/`flask` | Python API (+ Postgres/Redis) |
| `Dockerfile` only | Single `web` service. Hatch can infer the port from the image or Dockerfile. Set it explicitly if needed. |
| `landing/` + `core/api/` | Hatch repo, landing-only preview |

## Env scrubbing

Keys matching `(?i)(secret|token|api[_-]?key|password|passwd|private[_-]?key)` get their value replaced by `${SECRET_<KEY>}`. Postgres URLs get their password replaced by `${DB_PASSWORD}`.
