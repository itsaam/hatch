<div align="center">

<img src="assets/logo.png" alt="Hatch" width="96" />

# Hatch

Self-hosted preview deployments par pull request — app, base de données et services isolés à chaque PR.

[hatchpr.dev](https://hatchpr.dev) · [Docs](https://hatchpr.dev/docs/) · [GitHub App](https://github.com/apps/hatchpr) · [Demo repo](https://github.com/itsaam/hatch-demo-node)

</div>

---

## Pourquoi

Vercel et Netlify font des previews très bien — tant que ton app tient dans une lambda. Dès qu'il y a une base de données, un Redis, un worker, des secrets partagés, le modèle craque :

- une DB de staging partagée pollue les PRs entre elles ;
- un setup local complet exclut tous les reviewers non-tech ;
- une base managée par PR sur du Vercel Pro coûte un bras.

Hatch fait tourner tout le stack — app, DB, Redis, workers — sur **ton serveur**, isolé par PR, jetable à la fermeture.

## Comment ça marche

Tu poses un `.hatch.yml` à la racine de ton repo :

```yaml
version: 1
services:
  web:
    build: .
    port: 3000
    expose: true
    env:
      DATABASE_URL: postgres://app:${DB_PASSWORD}@db:5432/app
    depends_on: [db]
  db:
    image: postgres:16-alpine
    env:
      POSTGRES_USER: app
      POSTGRES_PASSWORD: ${DB_PASSWORD}
      POSTGRES_DB: app
    healthcheck:
      cmd: pg_isready -U app
seed:
  after: db
  sql: ./seed/preview.sql
```

Tu pushes une branche, ouvres une PR. Hatch build l'image, démarre le stack dans son propre réseau Docker, attend que les healthchecks passent, expose `pr-<num>-<repo>.hatchpr.dev` en HTTPS, et commente le lien sur la PR. À la fermeture, tout est détruit — containers, volumes, certificat, ligne en base.

Chaque preview a sa propre DB vierge, son propre mot de passe (dérivé HMAC), aucune fuite d'une PR à l'autre.

## Ce qui marche aujourd'hui

Multi-services arbitraires (tout ce qui rentre dans une image Docker). Seed SQL automatique au boot. `depends_on` qui attend vraiment le healthcheck. HTTPS Let's Encrypt sans config. Bot GitHub qui poste, met à jour au sync, annonce la suppression. Cleanup automatique à la fermeture + TTL 7 jours d'inactivité. Dashboard web pour voir les previews actifs, redeployer, lire les logs en streaming, gérer les secrets chiffrés.

## Quick start

1. Installer la GitHub App : [github.com/apps/hatchpr](https://github.com/apps/hatchpr)
2. Ajouter `.hatch.yml` à la racine du repo (voir [docs](https://hatchpr.dev/docs/))
3. Ouvrir une PR — preview live en 30 à 60 secondes

Le [demo repo](https://github.com/itsaam/hatch-demo-node) montre un stack Node + Postgres complet et fonctionnel.

## Architecture

```
core/api/            Control plane Go — webhook, deployer, reconciler, bot GitHub
landing/             Site marketing + docs + dashboard (Vite + React)
templates/           Templates de démarrage (Next.js, FastAPI, Django, Rails, static)
security/            Hardening optionnel du socket Docker (proxy + body filter)
docker-compose.yml   Déploiement du control plane lui-même
```

Backend Go 1.23 (chi + pgx/v5), PostgreSQL 16, Docker Engine API en direct via socket Unix (pas de SDK), Traefik en reverse proxy (réutilisé si déjà présent sur l'host, ex. Dokploy), GitHub App auth via JWT RS256 + installation tokens.

## Sécurité

Le control plane parle au Docker Engine pour build et lancer les previews.
Monter `/var/run/docker.sock` brut dans `api` est équivalent à donner root sur
l'host : une RCE dans `api` permettrait de lancer un container `--privileged`
avec `/` bind-mounté, et de pivoter sur tout le serveur.

`security/` fournit une isolation à deux étages, à activer via un
`docker-compose.override.yml` (cf. [`security/README.md`](./security/README.md)) :

- **Layer 1** — [`tecnativa/docker-socket-proxy`](https://github.com/Tecnativa/docker-socket-proxy)
  filtre l'API par endpoint et bloque tout ce dont hatch n'a pas besoin
  (`/volumes`, `/swarm`, `/secrets`, `/system`, `/plugins`, …).
- **Layer 2** — `security/body_filter.py`, un reverse-proxy Python ~150 lignes
  qui inspecte les bodies de `POST /containers/create` et rejette
  `Privileged`, `CapAdd: SYS_ADMIN`/etc., `NetworkMode/PidMode/IpcMode=host`,
  `seccomp/apparmor=unconfined`, `Devices`, `Runtime` non-runc, et tout bind
  mount sortant d'une allowlist de chemins host.

Une fois en place, une compromission de `api` reste cantonnée à ce que hatch
fait déjà (créer des previews avec `Memory` / `NanoCpus` / `RestartPolicy`),
sans s'évader sur l'host. Chaque blocage est loggué.

## Installation self-host

Prérequis : un serveur Linux avec Docker, un Traefik (ou Dokploy) déjà sur la machine, un DNS wildcard `*.tondomaine.com` qui pointe sur l'IP de l'host.

```bash
git clone https://github.com/itsaam/hatch /opt/hatch
cd /opt/hatch
cp .env.example .env
# édite .env : POSTGRES_PASSWORD, GITHUB_APP_ID, GITHUB_WEBHOOK_SECRET, HATCH_DOMAIN
# place la clé privée GitHub App dans /etc/hatch/secrets/github-app.pem (chmod 600)
docker compose up -d
```

Procédure complète et runbook : [hatchpr.dev/docs](https://hatchpr.dev/docs/).

## Limites actuelles

C'est une beta utilisable mais incomplète. Ne sont pas encore livrés :

- volumes persistants (la DB d'une preview repart de zéro à chaque redeploy — c'est souvent voulu, mais pas configurable) ;
- multi-region ;
- rollback explicite vers un commit antérieur (un nouveau push réécrit la preview).

Les bugs et idées vivent dans les [issues GitHub](https://github.com/itsaam/hatch/issues).

## Licence

Voir [LICENSE](./LICENSE).

## Auteur

Samy Abdelmalek — [samyabdelmalek.fr](https://samyabdelmalek.fr/)

---

<div align="center">
Si Hatch te sert, <a href="https://github.com/itsaam/hatch">⭐ star le repo</a>. Ça aide vraiment.
</div>
