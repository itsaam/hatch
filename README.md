<div align="center">

# 🥚 Hatch

**Self-hosted PR preview deployments avec isolation complète (app + DB) par pull request.**

[Site](https://hatchpr.dev) · [Docs](https://hatchpr.dev/docs/) · [GitHub App](https://github.com/apps/hatchpr) · [Demo repo](https://github.com/itsaam/hatch-demo-node)

![status](https://img.shields.io/badge/status-beta-orange) ![license](https://img.shields.io/badge/license-MIT-green) ![stack](https://img.shields.io/badge/backend-Go-00ADD8) ![runtime](https://img.shields.io/badge/runtime-Docker-2496ED)

</div>

---

## Le problème

Vercel/Netlify font des previews pour les apps stateless. Dès que ton projet a une base de données, Redis, un worker, des secrets — tu galères :

- DB staging partagée → pollution entre reviewers
- Setup local → reviewer non-tech perdu
- Vercel Pro + DB managées + Redis managé pour chaque PR → facture qui explose

## La solution

Tu ajoutes un `.hatch.yml` à la racine de ton repo :

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

Tu pushes une branche, ouvres une PR. Hatch :

1. **Spawn** le stack complet (app + DB + services) sur TON serveur
2. **Isole** chaque PR dans son propre réseau Docker — aucune fuite de données entre previews
3. **Route** l'URL `pr-<num>-<repo>.hatchpr.dev` avec HTTPS Let's Encrypt automatique
4. **Commente** le lien sur la PR
5. **Nettoie** tout quand la PR ferme (containers, network, DB)

## Features

- ✅ **Multi-services** : app + DB + Redis + workers… tout ce qui rentre dans un Dockerfile ou une image Docker
- ✅ **Isolation par PR** : réseau Docker dédié, DB vierge, mot de passe dérivé HMAC unique
- ✅ **Seed SQL** : pré-remplir la DB avec des données de test à chaque preview
- ✅ **Healthchecks** : `depends_on` attend que le service cible soit healthy
- ✅ **HTTPS auto** : Traefik + Let's Encrypt, zéro config
- ✅ **Bot GitHub** : commente le lien preview, met à jour au sync, annonce la suppression
- ✅ **Cleanup** : fermeture de PR + TTL 7 jours d'inactivité
- ✅ **Self-hosted** : ton serveur, tes règles, pas de vendor lock

## Quick start

1. Installe la GitHub App sur ton repo : [github.com/apps/hatchpr](https://github.com/apps/hatchpr)
2. Ajoute un `.hatch.yml` à la racine (cf. [docs](https://hatchpr.dev/docs/))
3. Ouvre une PR → preview live sous 30-60s

Le [demo repo](https://github.com/itsaam/hatch-demo-node) montre un stack Node + Postgres complet.

## Architecture

```
core/api/              Control plane Go (webhook, deployer, bot, reconciler)
landing/               Site + docs (Vite + React)
docker-compose.yml     Déploiement prod du control plane lui-même
```

- **Backend** : Go 1.23, chi, pgx/v5
- **DB** : PostgreSQL 16
- **Runtime** : Docker Engine API (pas de SDK — appels HTTP directs via unix socket)
- **Reverse proxy** : Traefik (ré-utilisé si déjà présent sur l'host, ex. Dokploy)
- **GitHub** : App auth JWT RS256 + installation tokens

## Installation (self-host)

Serveur Linux avec Docker + Traefik (ou Dokploy) + DNS wildcard `*.yourdomain.com` → IP du host.

```bash
git clone https://github.com/itsaam/hatch /opt/hatch
cd /opt/hatch
cp .env.example .env
# édite .env : POSTGRES_PASSWORD, GITHUB_APP_ID, GITHUB_WEBHOOK_SECRET, HATCH_DOMAIN
# place ta clé privée GitHub App dans /etc/hatch/secrets/github-app.pem (chmod 600)
docker compose up -d
```

Détails complets : [docs d'installation](https://hatchpr.dev/docs/).

## Limitations actuelles

Beta. Pas encore :
- Dashboard web (status via bot GitHub + logs serveur pour l'instant)
- Secret store UI (secrets hardcodés dans `.hatch.yml` pour les valeurs de test uniquement)
- Volumes persistants (la DB d'une preview repart de zéro à chaque redeploy — souvent voulu)
- Multi-region

## Licence

MIT — voir [LICENSE](./LICENSE).

## Auteur

Samy Abdelmalek — [samyabdelmalek.fr](https://samyabdelmalek.fr/)

---

<div align="center">
  Si Hatch te plaît, <a href="https://github.com/itsaam/hatch">⭐ star le repo</a> — ça m'aide énormément.
</div>
