# Docker socket hardening

This optional Compose setup puts a proxy and a request filter between the
Hatch API and the host's Docker socket. Direct Docker API access is equivalent
to administrator access to the host. Use this guide only for the Docker
runtime.

## How it works

```text
Docker socket → Docker socket proxy → creation filter → Hatch API
```

Only the socket proxy mounts the host socket. Hatch connects to the filter's
Unix socket. The proxy allows the Docker API endpoint groups Hatch needs for
containers, images, networks, builds and BuildKit sessions. The filter checks
container creation requests for privileged settings, host namespace sharing,
dangerous capabilities, device access and unexpected bind mounts. Rejected
requests return HTTP 403 and appear in the filter logs.

BuildKit sessions, container attach and exec streams pass through without
binary inspection.

## Enable the proxy

Create a `docker-compose.override.yml` in the repository root using the example
below. The override requires Docker Compose support for `!override`. Keep the
proxy and filter on their internal Docker network.

```yaml
services:
  docker-proxy:
    image: tecnativa/docker-socket-proxy:0.3
    restart: unless-stopped
    environment:
      CONTAINERS: 1
      IMAGES: 1
      NETWORKS: 1
      EXEC: 1
      BUILD: 1
      SESSION: 1
      INFO: 1
      VERSION: 1
      AUTH: 1
      POST: 1
      VOLUMES: 0
      SWARM: 0
      SECRETS: 0
      CONFIGS: 0
      TASKS: 0
      SERVICES: 0
      SYSTEM: 0
      PLUGINS: 0
      NODES: 0
      DISTRIBUTION: 0
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    networks: [docker_proxy]

  docker-body-filter:
    image: python:3.12-alpine
    restart: unless-stopped
    command: ["python3", "-u", "/app/body_filter.py"]
    environment:
      UPSTREAM_HOST: docker-proxy
      UPSTREAM_PORT: "2375"
      LISTEN_SOCKET: /shared/docker.sock
      ALLOWED_HOST_PATHS: /etc/hatch/secrets
    volumes:
      - ./security/body_filter.py:/app/body_filter.py:ro
      - hatch_docker_sock:/shared
    networks: [docker_proxy]
    depends_on: [docker-proxy]
    read_only: true
    tmpfs: [/tmp]

  api:
    volumes: !override
      - /etc/hatch/secrets:/app/secrets:ro
      - hatch_docker_sock:/var/run
    networks:
      - hatch_internal
      - dokploy-network
    depends_on:
      hatch-postgres: {condition: service_healthy}
      docker-body-filter: {condition: service_started}

networks:
  docker_proxy:
    name: hatch_docker_proxy
    internal: true

volumes:
  hatch_docker_sock:
```

Run `docker compose up -d` when ready to apply the override. Verify the recreated
API uses the filter socket and check that a preview can start and stop.

## Bind mount allowlist

Set `ALLOWED_HOST_PATHS` to the comma-separated host paths that previews need.
Keep the list as small as possible.

```yaml
ALLOWED_HOST_PATHS: /etc/hatch/secrets
```

## Scope

- The creation filter checks `POST /containers/create`. Review the policy
  before allowing any additional endpoint groups or methods.
- A socket proxy cannot make arbitrary preview code safe. Restrict allowed
  repositories, keep production credentials out of previews and review the
  images they can run.
- Containers already started with another configuration retain their settings
  until they are recreated.
