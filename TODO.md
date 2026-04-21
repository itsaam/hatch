# TODO — Hatch

Suivi des tâches restantes, par ordre de valeur.

## Priorité 1 — UX onboarding

### CLI `hatch init` (1j)
- `hatch init` scan le repo en cours
- Détecte `docker-compose.yml`, `package.json`, `Gemfile`, `requirements.txt`, `composer.json`
- Génère un `.hatch.yml` auto-adapté au stack
- Gros gain : les utilisateurs n'écrivent pas le yml à la main
- Binaire Go cross-compilé (darwin + linux), distribué via Homebrew + `go install`

### Logs streaming real-time dans le dashboard (0.5j)
- Remplacer le polling par du SSE ou WebSocket côté API
- Endpoint `GET /api/previews/{owner}/{repo}/{pr}/logs/stream`
- Dashboard affiche la stream en direct, auto-scroll avec pause sur hover
- Voir les builds et les containers run en live

## Priorité 2 — Robustesse

### Resource limits dans `.hatch.yml` (0.5j)
```yaml
services:
  web:
    build: .
    limits:
      memory: 512m
      cpu: 0.5
```
- Traduction vers `HostConfig.Memory` / `NanoCPUs` de Docker
- Défaut raisonnable (ex: 1 GB / 1 CPU) si absent
- Évite qu'une preview sature la machine

### Backup / export des secrets (0.5j)
- Endpoint `GET /api/secrets/export` (bearer admin)
- Retourne un dump chiffré avec un autre key (fourni en param)
- Endpoint `POST /api/secrets/import` pour restore
- Sécurise le cas "j'ai perdu HATCH_SECRET_KEY"

### Healthcheck global sur le service exposé (0.25j)
- Si `web` ne répond pas HTTP 200 dans les 60s après start, marquer la preview `failed` et alert dans le bot
- Évite les "preview running" qui sont en fait crashloop

## Priorité 3 — Confort

### Template gallery (0.5j)
- 4-5 repos publics prêts à forker :
  - `hatch-template-nextjs-postgres`
  - `hatch-template-rails-sidekiq`
  - `hatch-template-django-redis`
  - `hatch-template-fastapi-postgres`
  - `hatch-template-static`
- Chacun avec `.hatch.yml` pré-config, README clair, Dockerfile minimal
- Lien "Utiliser ce template" sur la landing

### BuildKit session gRPC (1j)
- Établir une vraie session BuildKit (HTTP/2 upgrade + gRPC multiplexé)
- Débloque les caches persistants (`--mount=type=cache`, `--mount=type=secret`)
- Builds 3-5x plus rapides sur des repos déjà buildés une fois
- Débloque aussi les Dockerfiles avec `# syntax=docker/dockerfile:1.6`

### Dashboard — filtrage/recherche (0.25j)
- Input de recherche par repo/branche
- Filtrage combiné (status + repo)
- Tri manuel par colonne

## Priorité 4 — Scaling

### Support GitLab / Bitbucket (1-2j par plateforme)
- Abstraction du "webhook provider" côté API
- Webhook handlers séparés avec signature vérifiée
- OAuth App / Access Token flow équivalent à GitHub App
- URL patterns : `pr-N-slug.domain` reste identique

### Multi-host / worker nodes (3-5j)
- Control plane (actuel) + pool de `hatch-worker`
- Distribuer les builds/containers selon charge
- Nécessaire au-delà de 20-30 previews simultanées
- Kubernetes probablement plus propre que DIY

### Monitoring CPU/RAM par preview (1j)
- Endpoint `/api/previews/{...}/stats` qui streame les stats Docker
- Graphe live dans le dashboard
- Alert si une preview > 80% RAM pendant 5min

## Priorité 5 — Product / business

### Beta privée outbound (0.5j)
- Liste de 20-50 devs potentiels
- Email d'invitation personnalisé avec token d'accès
- Feedback form simple
- Priorité aux devs solo + petites équipes (1-5 personnes)

### Analytics internes (0.5j)
- Compteurs : nb previews actives par repo, temps moyen de build, taux d'échec
- Dashboard admin avec les métriques
- Pas de tracking utilisateur, juste du technique

### Pricing / monétisation (à cadrer)
- Self-hosted = gratuit, toujours
- Managed cloud = payant (si besoin), hébergé chez nous
- Réfléchir au moment du launch public, pas avant

## Bugs connus / petits trucs

- [ ] Le scrub token ne couvre pas les tokens en base64 dans les traces BuildKit (faible risque, à vérifier)
- [ ] Dashboard : icône "disabled" sur le bouton Open preview quand URL absente — testé visuel mais pas fonctionnellement
- [ ] Dashboard : pas de pagination si > 500 previews (LIMIT 500 côté API, à paginer quand ça devient un souci)
- [ ] Certains emojis Unicode cassent l'affichage terminal des logs (rare, non-bloquant)

## Documentation à compléter

- [ ] Guide d'installation détaillé (DNS wildcard Cloudflare, Docker sur Debian/Ubuntu, sécurisation du serveur)
- [ ] Troubleshooting spécifique par stack (Rails assets:precompile, Next.js standalone build, etc.)
- [ ] Guide de migration depuis docker-compose.yml vers .hatch.yml
- [ ] Architecture interne (diagramme deploy.go / compose.go / Docker API flow)

## Launch (quand prêt)

- [ ] Rendre la GitHub App publique dans les settings
- [ ] Demo GIF sur la landing (Kap ou Gifski)
- [ ] Show HN — draft prêt dans `LAUNCH.md`
- [ ] Reddit /r/selfhosted — draft prêt
- [ ] Reddit /r/devops — draft prêt
- [ ] Twitter/X thread — draft prêt
- [ ] LinkedIn post — draft prêt

## Release workflow

1. Bump la version dans `cli/npm/package.json`
2. Commit
3. Tag : `git tag v0.1.0 && git push --tags`
4. GitHub Actions (GoReleaser) build + publish les 5 binaires sur GitHub Releases automatiquement
5. Publish npm : `cd cli/npm && npm publish --access public`

**Pré-requis** (une fois) :
- `npm login` avec un compte npm (gratuit)
- Créer l'organisation `@hatchpr` sur npmjs.com (gratuit)
- Aucun secret GitHub à configurer : `GITHUB_TOKEN` est auto-provisionné dans les Actions.
