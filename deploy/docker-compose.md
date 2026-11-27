# Déployer Hatch avec Docker Compose

Prépare un serveur Linux avec Docker et Traefik, par exemple via Dokploy. Fais
pointer le domaine du site, celui de l’API et les sous-domaines de preview vers
l’ingress du serveur. Le réseau Docker externe `dokploy-network` doit exister.

```bash
git clone https://github.com/itsaam/hatch /opt/hatch
cd /opt/hatch
cp .env.example .env
```

Renseigne les paramètres dans `.env`, dont les identifiants PostgreSQL,
`HATCH_DOMAIN`, `ALLOWED_ORIGIN`, `HATCH_ALLOWED_OWNERS` et les secrets du
webhook, du tableau de bord et du chiffrement. Place la clé de la GitHub App dans
`/etc/hatch/secrets/github-app.pem` avec des permissions restreintes. Confirme que
son webhook reçoit `pull_request` et `issue_comment`.

Pour conserver une limite par visiteur derrière Traefik, renseigne
`HATCH_TRUSTED_PROXY_CIDRS` avec les réseaux des proxies qui contrôlent
`X-Forwarded-For`. Garde les réseaux des conteneurs de preview hors de cette
liste. Sans ce réglage, les visiteurs d’un même proxy partagent sa limite.

L’accès direct à `/var/run/docker.sock` donne à l’API la capacité d’administrer
le serveur Docker. Le [guide de protection du socket](../security/README.md)
décrit l’override Compose, le proxy d’API et le filtre des créations de
conteneurs.

Une fois la configuration prête, lance `docker compose up -d`, puis vérifie
`/readyz` et le cycle d’une PR avec `@hatchpr preview`.
