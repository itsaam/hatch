# Hatch — Docker socket hardening

Le control plane de hatch a besoin du Docker Engine API pour build et lancer
les previews. Sans précaution, monter `/var/run/docker.sock` dans le container
`api` équivaut à donner **root sur l'host** à tout attaquant qui obtiendrait
une RCE dans `api` (création d'un container `--privileged` + bind mount sur
`/`).

Ce dossier contient une isolation à deux étages, optionnelle, à activer via le
`docker-compose.override.yml` fourni en exemple plus bas.

## Architecture

```
host /var/run/docker.sock (RO)
        │
        ▼
docker-proxy (tecnativa/docker-socket-proxy)   ← Layer 1 : filtre par endpoint
        │  TCP 2375, réseau interne dédié
        ▼
docker-body-filter (body_filter.py)            ← Layer 2 : filtre par body JSON
        │  unix socket /shared/docker.sock
        ▼
api (voit /var/run/docker.sock)
```

`api` ne touche jamais le vrai socket de l'host. Le seul container qui le
monte est `docker-proxy`, en lecture seule.

## Layer 1 — endpoint allowlist (tecnativa)

[`tecnativa/docker-socket-proxy`](https://github.com/Tecnativa/docker-socket-proxy)
filtre l'API par chemin. Les endpoints autorisés correspondent à ce que hatch
appelle réellement (`/containers`, `/images`, `/networks`, `/exec`, `/build`,
`/session`, `/info`, `/version`, `/_ping`, `/auth`).

Les endpoints suivants renvoient **403** :

```
/volumes  /swarm  /secrets  /configs  /tasks  /services
/system   /plugins  /distribution  /nodes
```

## Layer 2 — body filter (`body_filter.py`)

Tecnativa ne valide pas les bodies. Un attaquant pourrait passer un
`HostConfig.Privileged: true` à `POST /containers/create` et s'évader.

`body_filter.py` est un petit reverse-proxy Python (~150 lignes, stdlib
uniquement) qui :

1. écoute sur un unix socket dans un volume partagé avec `api` ;
2. relaye toutes les requêtes vers le proxy tecnativa, **sauf** `POST /containers/create` qu'il inspecte ;
3. rejette avec `403` toute requête dont le `HostConfig` contient :

   | Champ | Règle |
   |---|---|
   | `Privileged` | doit être absent ou `false` |
   | `PublishAllPorts` | doit être absent ou `false` |
   | `Binds`, `Mounts[].Source` (type=bind) | host path doit être dans `ALLOWED_HOST_PATHS` |
   | `CapAdd` | aucune capability dangereuse (`SYS_ADMIN`, `SYS_PTRACE`, `NET_ADMIN`, `DAC_OVERRIDE`, `BPF`, `ALL`, …) |
   | `NetworkMode`, `PidMode`, `IpcMode`, `UsernsMode`, `UTSMode`, `CgroupnsMode` | doit ≠ `host` |
   | `SecurityOpt` | rejette `seccomp=unconfined`, `apparmor=unconfined`, `no-new-privileges:false` |
   | `Devices`, `DeviceRequests` | rejet si renseignés |
   | `Runtime` | doit être `runc` ou vide |
   | `GroupAdd` | rejette `0`, `root`, `docker` |

Les requêtes hijackées (`/exec/start`, `/containers/{id}/attach`, build
sessions BuildKit) sont relayées telles quelles en mode bidirectionnel — le
filtre ne lit jamais de stream binaire.

Le filtre logue chaque blocage avec le détail :

```
2026-05-05 19:28:39 WARNING BLOCKED POST /v1.43/containers/create: HostConfig.CapAdd=CAP_SYS_PTRACE
```

## Activer l'isolation

Crée un `docker-compose.override.yml` à la racine du repo (déjà gitignoré) :

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

Puis `docker compose up -d`. Le compose principal (`docker-compose.yml`) reste
inchangé.

## Personnaliser l'allowlist des bind mounts

Si ton déploiement a besoin d'exposer d'autres chemins host à l'`api` (par
exemple un cache BuildKit partagé sous `/var/lib/hatch/buildkit-cache`),
ajoute-les à `ALLOWED_HOST_PATHS` (séparés par virgules) :

```yaml
ALLOWED_HOST_PATHS: /etc/hatch/secrets,/var/lib/hatch/buildkit-cache
```

Tout chemin host hors de cette liste sera refusé en bind mount.

## Limites

- Le proxy tecnativa ne valide pas les bodies ; tout le filtrage de
  `/containers/create` repose donc sur `body_filter.py`. Si tu modifies les
  endpoints autorisés au layer 1, garde en tête que d'autres endpoints
  (`POST /containers/{id}/update`, par ex.) peuvent eux aussi accepter du
  `HostConfig` — étends le filtre en conséquence.
- Les options dangereuses passées via `Cmd` ou `Entrypoint` (un binaire
  malveillant déjà dans l'image) ne sont pas détectées : c'est hors scope
  d'un proxy de socket. Restreindre les images buildables est un sujet
  orthogonal (registry allowlist, scan, signature).
- Les containers déjà en cours d'exécution avant l'activation de l'isolation
  ne sont pas affectés ; un `down` puis `up -d` est nécessaire pour basculer
  `api` derrière le filtre.
