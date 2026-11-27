<div align="center">

<img src="assets/logo.png" alt="Hatch" width="96" />

# Hatch

Self-hosted preview deployments par pull request — app, base de données et services isolés à chaque PR.

**Installation sur Kubernetes avec Argo CD, ou sur un serveur avec Docker Compose.**

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

Tu pushes une branche, ouvres une PR et commentes `@hatchpr preview`. Hatch construit les images, démarre les services dans un environnement isolé, attend les healthchecks et commente le lien HTTPS sur la PR. À la fermeture, Hatch supprime les ressources de la preview et conserve son état dans l'historique.

Chaque preview a sa propre DB vierge, son propre mot de passe (dérivé HMAC), aucune fuite d'une PR à l'autre.

## Ce qui marche aujourd'hui

Multi-services arbitraires (tout ce qui rentre dans une image Docker). Seed SQL automatique au boot. `depends_on` qui attend vraiment le healthcheck. HTTPS Let's Encrypt sans config. Bot GitHub qui publie le lien de preview et annonce sa suppression. Cleanup automatique à la fermeture + TTL 7 jours d'inactivité. Dashboard web pour voir les previews actifs, redeployer, lire les logs en streaming, gérer les secrets chiffrés.

## Quick start

1. Installer la GitHub App : [github.com/apps/hatchpr](https://github.com/apps/hatchpr)
2. Ajouter `.hatch.yml` à la racine du repo (voir [docs](https://hatchpr.dev/docs/))
3. Ouvrir une PR et commenter `@hatchpr preview`. Répéter la commande après un push pour mettre la preview à jour.

Le [demo repo](https://github.com/itsaam/hatch-demo-node) montre un stack Node + Postgres complet et fonctionnel.

## Architecture

```
core/api/            API Go — webhooks, cycle de vie des previews, bot GitHub
cli/                 CLI Go pour générer un .hatch.yml à partir d'un projet
landing/             Site, documentation et dashboard, servis par Nginx
security/            Protection optionnelle du socket Docker
docker-compose.yml   Installation de l'API, du site et de PostgreSQL 16
```

L'API vérifie la signature des webhooks GitHub, filtre les propriétaires autorisés et lit le `.hatch.yml` au commit de la PR. PostgreSQL conserve les états de preview, les identifiants de commentaires GitHub, les logs de build et les secrets de dépôt chiffrés. Le dashboard passe par l'API pour consulter les logs ou relancer une preview.

## Installation sur Kubernetes

Argo CD installe l'API, le site, PostgreSQL et les namespaces de preview depuis les manifests Git. Dans Lumovi, l'intégration Argo CD indique la santé et la synchronisation de Hatch. Les événements GitHub alimentent la file d'opérations de Hatch. Quand une PR éligible demande une preview, Hatch construit les images via BuildKit, les publie dans le registre configuré et crée les workloads, Services et Ingress de la PR. Voir le [guide d'installation Kubernetes](./deploy/kubernetes/README.md).

Chaque preview occupe un namespace préprovisionné, avec ses quotas et ses règles réseau. Le déploiement de base ouvre quatre emplacements. Tu peux en ajouter et augmenter la capacité des nœuds Kubernetes et du builder indépendamment. L'API tourne avec une seule réplique et conserve sa file PostgreSQL au redémarrage. Les installations publiques doivent garder la liste des propriétaires GitHub autorisés limitée aux dépôts de confiance. Voir le [guide Kubernetes sur l'isolation des workloads](https://kubernetes.io/docs/concepts/security/multi-tenancy/).

Pour migrer depuis Docker Compose :

1. Sauvegarder PostgreSQL et vérifier sa restauration. Récupérer `HATCH_SECRET_KEY` et la configuration de la GitHub App via le gestionnaire de secrets. Si l'ancienne instance dérivait la clé du secret webhook, définir `HATCH_SECRET_KEY` sur `hatch-fallback:` suivi de l'ancien secret webhook. Vérifier le déchiffrement des secrets de dépôt avant la bascule.
2. Valider sur un environnement de test le cycle complet d'une PR, dont le build, le démarrage des dépendances, le seed, la route HTTPS, les logs, la mise à jour GitHub et le nettoyage à la fermeture. Vérifier aussi le traitement des anciens états de preview en base lors du changement de moteur.
3. Vérifier que l'ancienne instance est arrêtée et effectuer la bascule du routage du site, de l'API et des previews avant de rediriger le webhook GitHub. Garder le volume Compose et la configuration d'origine pour le retour arrière, puis décider comment réconcilier les écritures faites sur la base cible si ce retour est nécessaire.

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

Pour garder des limites par visiteur derrière Traefik, renseigne
`HATCH_TRUSTED_PROXY_CIDRS` avec les adresses des proxies qui contrôlent
`X-Forwarded-For`. Sans ce réglage, les requêtes d'un même proxy partagent leur
limite. Évite d'y inclure les réseaux qui hébergent les conteneurs des previews.

Procédure complète et runbook : [hatchpr.dev/docs](https://hatchpr.dev/docs/).

## Limites actuelles

C'est une beta utilisable mais incomplète. Ne sont pas encore livrés :

- volumes persistants (la DB d'une preview repart de zéro à chaque redeploy — c'est souvent voulu, mais pas configurable) ;
- multi-region ;
- rollback explicite vers un commit antérieur.

Les bugs et idées vivent dans les [issues GitHub](https://github.com/itsaam/hatch/issues).

## Licence

Voir [LICENSE](./LICENSE).

## Auteur

Samy Abdelmalek — [samyabdelmalek.fr](https://samyabdelmalek.fr/)

---

<div align="center">
Si Hatch te sert, <a href="https://github.com/itsaam/hatch">⭐ star le repo</a>. Ça aide vraiment.
</div>
