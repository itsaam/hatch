---
name: Hatch deploy workflow on sunny
description: Commandes standard pour déployer Hatch (core API + landing) sur le serveur sunny après un push
type: reference
originSessionId: 65da8261-41e4-4dbe-a8ee-eb31fdb53b8f
---
Pour déployer un changement sur Hatch en prod après un push Git :

```bash
ssh sunny "cd /opt/hatch && git pull && docker compose build api landing && docker compose up -d api landing"
```

Pour une partie seulement :
- API uniquement : `docker compose build api && docker compose up -d api`
- Landing uniquement : `docker compose build landing && docker compose up -d landing`

**Serveur** : `sunny` (alias SSH déjà configuré localement, pas besoin de user@ip).

**Chemin du repo sur le serveur** : `/opt/hatch` (clone direct depuis `https://github.com/itsaam/hatch`).

**Logs live** :
- API : `ssh sunny "docker logs hatch-api-1 -f"`
- Landing : `ssh sunny "docker logs hatch-landing-1 -f"`

**Vérifier les previews actives** :
```bash
ssh sunny "docker ps --filter label=hatch.managed=true --format 'table {{.Names}}\t{{.Status}}'"
```

**Admin token** stocké dans `/opt/hatch/.env` sur le serveur (`HATCH_ADMIN_TOKEN`). Rotate :
```bash
ssh sunny "NEW=\$(openssl rand -hex 32) && sed -i \"s|^HATCH_ADMIN_TOKEN=.*|HATCH_ADMIN_TOKEN=\$NEW|\" /opt/hatch/.env && cd /opt/hatch && docker compose up -d api && echo \$NEW"
```

**Docker network** utilisé pour les previews : `dokploy-network` (external, attachable, overlay swarm). Traefik de Dokploy route vers les containers labelés `traefik.enable=true`.

**Reconcile** : au boot de l'API, les containers orphelins (pas de preview active en DB) sont supprimés automatiquement.

Règle globale : JAMAIS de `git commit` / `git push` / `docker compose` sans validation explicite de l'utilisateur.
