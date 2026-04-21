# Launch kit — Hatch

Scripts et posts prêts à publier pour lancer Hatch.

---

## 1. Hacker News — Show HN

**Titre** (≤ 80 chars, obligatoire) :
```
Show HN: Hatch – Self-hosted PR preview deployments with isolated databases
```

**URL** : `https://hatchpr.dev`

**Texte** (premier commentaire, posté par toi juste après submission) :

```
Author here.

I got tired of paying for Vercel Pro + a managed DB + managed Redis just so
reviewers could click a PR preview link. Worse: staging DBs shared between
PRs create data pollution, and frontend-focused preview tools (Vercel,
Netlify, Cloudflare Pages) don't help when your app actually has a backend
and a database.

Hatch is a self-hosted control plane (Go + Docker Engine API) that watches
your GitHub PRs. You drop a .hatch.yml at the root of a repo, declare your
stack (app + Postgres + Redis + workers, whatever), and every PR gets its
own isolated preview environment with:

- A dedicated Docker bridge network per PR (no data leak between reviewers)
- Fresh DB, seeded optionally via a SQL script
- HTTPS URL with Let's Encrypt, commented back on the PR by a bot
- Full cleanup (containers + volumes + network) on PR close or TTL expiry

Runs on a single VPS behind Traefik. Integrates cleanly with Dokploy if you
already run it. Install: point DNS wildcard to your server, install the
GitHub App, docker compose up.

Stack: Go 1.23, PostgreSQL, raw Docker Engine HTTP API (no SDK for portability),
Traefik for routing. Container names follow hatch-pr-<slug>-<pr>-<service>
and are reconciled/cleaned on boot so zombie containers don't accumulate.

Demo repo (Node + Postgres with register/login) that you can PR to see the
whole flow: https://github.com/itsaam/hatch-demo-node

Happy to answer any question about the design (JWT RS256 for GitHub App auth
hand-rolled with crypto/rsa, deterministic HMAC for per-PR DB passwords,
TTL reaper, etc.).

Docs: https://hatchpr.dev/docs/
Code: https://github.com/itsaam/hatch (MIT)
```

**Tips HN** :
- Post le matin US (9-11h ET, 15-17h Paris)
- Mardi ou mercredi idéal
- Réponds à TOUS les commentaires dans les 2h — l'engagement fait le rank
- Garde le ton humble et technique, évite le marketing speak

---

## 2. Reddit /r/selfhosted

**Titre** :
```
Hatch — self-hosted PR preview deployments with per-PR database isolation
```

**Flair** : `Software Development`

**Corps** :

```
Hey r/selfhosted,

I built Hatch to scratch my own itch: I wanted Vercel-like preview
deployments for stateful apps (Node + Postgres, Rails + Sidekiq, Django
+ Redis…) without paying per-seat pricing and without the vendor lock-in.

## What it does

- You install the GitHub App on your repos
- You drop a `.hatch.yml` at the root declaring your stack (services,
  dependencies, healthchecks, env, optional SQL seed)
- Every PR gets its own isolated environment with a fresh database,
  an HTTPS URL, and a comment from the bot on the PR

## Why self-hosted matters here

- Your preview environments run on YOUR infrastructure (VPS, homelab,
  whatever runs Docker + Traefik)
- No data goes through a third party — important when your test data
  has real shapes from prod
- Zero per-seat cost: the only limit is what your server can spawn
- Runs alongside Dokploy if you already use it

## Stack

- Go 1.23 control plane (chi, pgx/v5)
- PostgreSQL for state
- Docker Engine API directly (unix socket, no SDK)
- Traefik for ingress + Let's Encrypt
- GitHub App auth via hand-rolled JWT RS256

## What's already there

- Multi-service stacks with `depends_on` + healthcheck waits
- Per-PR network isolation
- Seed SQL scripts
- Auto cleanup on PR close / TTL expiry
- Reconciliation on boot (no zombie containers)

## What's missing (beta)

- Web dashboard (for now: GitHub bot comments + server logs)
- Secret store UI (secrets hardcoded in `.hatch.yml` for now — test values only)
- Persistent volumes
- Multi-region

Site + docs: https://hatchpr.dev
Code (MIT): https://github.com/itsaam/hatch
Demo repo you can fork and PR: https://github.com/itsaam/hatch-demo-node

Feedback very welcome — especially on .hatch.yml format and pain points
I might have missed.
```

---

## 3. Reddit /r/devops

Même post que r/selfhosted mais avec un angle plus CI/CD :

**Titre** :
```
Hatch — GitHub PR preview environments with isolated DBs, self-hosted
```

**Accroche** :
```
Tired of staging DBs polluted by reviewer testing? Of setup-locally docs
that stop working after 3 months? I built a self-hosted alternative to
Vercel previews that handles multi-service stacks with per-PR database
isolation.
```

Reste du post : idem r/selfhosted, ajoute en bas :

```
How it integrates in a CI pipeline: the GitHub App webhook triggers on
opened/synchronize/reopened. Build + deploy runs in a single goroutine
per PR (mutex-serialized to avoid races on push+reopen). Closed event
triggers a synchronous destroy of the whole stack.
```

---

## 4. Twitter / X thread

```
1/ I just shipped Hatch 🥚

Self-hosted PR preview deployments for stateful apps (app + DB + Redis
+ workers), with full isolation per pull request.

Demo → https://hatchpr.dev
Code → https://github.com/itsaam/hatch (MIT)

🧵
```

```
2/ The problem: Vercel/Netlify previews are great for static sites and
stateless frontends. But the moment your app has a Postgres, a worker,
a cache — you're either paying a fortune or sharing a staging DB that
three reviewers pollute simultaneously.
```

```
3/ Hatch fixes this with a .hatch.yml file at the root of your repo:

- declare services (web, db, redis, …)
- healthchecks, depends_on
- env vars with ${PR} / ${DB_PASSWORD} / ${SHA} substitution
- optional SQL seed script

Screenshot 👇
[attach yaml screenshot]
```

```
4/ Every PR → its own Docker bridge network. Every service → its own
container. The app joins the public Traefik network for HTTPS routing.
Others stay internal.

TL;DR: two reviewers register with the same email on two PRs — their
data never meets.
```

```
5/ Cleanup is automatic:
- PR closed → stack destroyed (containers + volumes + network)
- 7 days inactivity → hibernation
- API boot → reconciler kills zombie containers

No manual ops. The GitHub bot comments the status on every PR.
```

```
6/ Built with Go 1.23 + chi + pgx, Docker Engine API (no SDK — direct
HTTP on the unix socket), Traefik for ingress.

Runs on a single VPS. Plays nice with Dokploy if you already have one.

Try it on your infra → https://hatchpr.dev/docs/
```

```
7/ Next on the roadmap:
- web dashboard
- secret store UI
- auto-detection of docker-compose.yml → .hatch.yml
- template gallery

If you find this useful, a star on GitHub means a lot 🌟
https://github.com/itsaam/hatch

#buildinpublic #selfhosted #devtools
```

---

## 5. LinkedIn post (version FR)

```
🥚 Hatch est en ligne.

J'ai passé les dernières semaines à construire un outil open-source de
PR preview deployments self-hosted. Concrètement :

- Tu ajoutes un fichier .hatch.yml à la racine de ton repo
- Chaque Pull Request GitHub déclenche un déploiement automatique
- App + base de données + cache + workers sont spawnés sur TON serveur
- Chaque PR a SA propre base isolée — zéro pollution entre reviewers
- URL HTTPS publique commentée sur la PR par un bot
- Cleanup automatique à la fermeture

Stack : Go 1.23, PostgreSQL, Docker Engine API direct (sans SDK), Traefik.

Pourquoi self-hosted ? Parce que :
- Les previews Vercel/Netlify ne gèrent pas les apps stateful proprement
- Payer par seat pour des outils de dev devient vite délirant
- Tes données de test restent chez toi

Code MIT, docs complètes, demo repo fonctionnel :
→ https://hatchpr.dev
→ https://github.com/itsaam/hatch

Feedback très welcome.
```

---

## 6. Post Product Hunt (plus tard, une fois le dashboard fait)

À préparer après avoir ajouté le dashboard et eu quelques users beta.

---

## Checklist pré-launch

- [ ] README GitHub à jour avec GIF démo
- [ ] LICENCE MIT présente
- [ ] Docs à jour (hatchpr.dev/docs/)
- [ ] Demo repo `hatch-demo-node` fonctionnel
- [ ] GitHub App publique (réglages → Public dans les settings)
- [ ] Ouvrir une issue "Feedback welcome" épinglée sur le repo
- [ ] Préparer réponses aux questions classiques :
  - "Pourquoi Go et pas Rust / Node ?"
  - "Comment ça compare à Coolify / CapRover / Dokku ?"
  - "Ça marche avec GitLab / Bitbucket ?" (non, seulement GitHub pour l'instant)
  - "Est-ce que les secrets sont chiffrés ?" (pas de store UI encore)
- [ ] Timezone du post : mardi 9h ET pour HN

---

## Questions FAQ à préparer

**Q: Comment ça compare à Coolify / CapRover ?**
R: Ces outils sont des PaaS généralistes (tu déploies une app et c'est live). Hatch est spécifiquement un outil de *PR preview* — il s'active uniquement quand une PR est ouverte, chaque preview est éphémère avec sa DB isolée, et tout est nettoyé à la fermeture. Plus proche de Vercel Preview que d'un Heroku self-hosted.

**Q: Pourquoi .hatch.yml et pas docker-compose.yml ?**
R: docker-compose est conçu pour du long-running. `.hatch.yml` encode des concepts spécifiques aux previews : `expose: true` (un seul service routé), `seed` (SQL appliqué une fois), variables dérivées par PR (`${DB_PASSWORD}` stable et unique). Un futur `hatch init` pourra auto-générer un `.hatch.yml` depuis un `docker-compose.yml` existant.

**Q: Scaling ?**
R: Pour l'instant, un seul host. Pour des besoins plus larges, la V2 utilisera Kubernetes.

**Q: Rate limit GitHub ?**
R: Les installation tokens GitHub App ont 5000 req/h par installation. Hatch fait ~3 calls par webhook (create comment, update comment, read file). Un repo avec 50 PRs actives consomme 150 req/h — largement sous la limite.
