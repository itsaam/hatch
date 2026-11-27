# Deploy Hatch with Docker Compose

Prepare a Linux server with Docker and Traefik. Dokploy is one way to run
Traefik. The external Docker network `dokploy-network` must exist. The Compose
labels use Traefik's `websecure` entrypoint and a `letsencrypt` certificate
resolver. Point the site, API and wildcard preview hostnames to the server's
ingress.

```bash
git clone https://github.com/itsaam/hatch /opt/hatch
cd /opt/hatch
cp .env.example .env
```

Set the PostgreSQL credentials, `HATCH_DOMAIN`, `ALLOWED_ORIGIN` and
`HATCH_ALLOWED_OWNERS` in `.env`. Configure `GITHUB_WEBHOOK_SECRET`,
`HATCH_ADMIN_TOKEN` and a stable `HATCH_SECRET_KEY`. Set `GITHUB_APP_ID` and
store the app's private key at `/etc/hatch/secrets/github-app.pem` with
restricted permissions. Point the app webhook to the API's
`/api/github/webhook` route and subscribe to `pull_request` and
`issue_comment`. Grant the app Contents and Pull requests read access and Issues read and write
access for PR comments.

Set `HATCH_TRUSTED_PROXY_CIDRS` to the networks of proxies that sanitize or
append `X-Forwarded-For`. Leave preview networks out of this list. If you
leave the setting empty, visitors through the same proxy share its rate
limit.

Access to `/var/run/docker.sock` lets the API administer the Docker host.
The [socket hardening guide](../security/README.md) documents the Compose
override, API proxy and container creation filter.

Once the configuration is ready, run `docker compose up -d`. Check
`/readyz`, request a preview with `@hatchpr preview`, and close the test PR to
verify cleanup.
