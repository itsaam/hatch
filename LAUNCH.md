# Hatch launch notes

Use these drafts once the public quickstart and preview lifecycle have been
verified on the instance people will use.

## Release checklist

- [ ] Verify the GitHub App installation, the allowlist, and `issue_comment` delivery for the demo.
- [ ] Request a preview with `@hatchpr preview`, open the HTTPS link, and verify cleanup after closing the PR.
- [ ] Check the README, public docs, template links, and CLI install instructions.
- [ ] Review CI and scan results for the images planned for release.
- [ ] Confirm the public demo and self-hosting instructions describe separate deployment paths.

## Show HN

**Title** · Show HN: Hatch, self-hosted previews for GitHub pull requests

**URL** · <https://hatchpr.dev>

**Draft comment**

I built Hatch to make it easier to review changes in apps that need a database
or a worker. A `.hatch.yml` in the repository declares the stack. Comment
`@hatchpr preview` on a PR to start it, and Hatch posts the preview link.
Closing the PR removes its resources.

Hatch is open source under the AGPL-3.0 license. The repo has deployment guides
for [Docker Compose](deploy/docker-compose.md) and
[Kubernetes with Argo CD](deploy/kubernetes/README.md).

[Source](https://github.com/itsaam/hatch) · [Docs](https://hatchpr.dev/docs/) · [Node and PostgreSQL demo](https://github.com/itsaam/hatch-demo-node)

## Reddit

**Title** · Hatch, self-hosted PR previews for apps with databases and workers

**Draft post**

I wanted reviewers to be able to open a PR preview without spinning up the app,
Postgres, Redis and workers on their machines. Hatch runs that stack on your
infrastructure and posts a link on the PR. You declare the services in
`.hatch.yml` and ask for the preview with `@hatchpr preview`. Each preview runs
with its own temporary resources.

For r/selfhosted, link the Compose setup. For r/devops, describe the deployment
path you tested and link its runbook. Ask for feedback on onboarding and config.

## X and LinkedIn

**Draft post**

Hatch runs GitHub PR previews for apps with databases, caches and workers.
Add a `.hatch.yml`, comment `@hatchpr preview` and share the link Hatch posts.
Closing the PR removes the environment.

[Docs](https://hatchpr.dev/docs/) · [Code](https://github.com/itsaam/hatch)

## FAQ

**Does every PR start a preview?** Preview requests are manual by default. A
repository can enable automatic builds with `previews.trigger: auto`.

**Can a fork PR build?** Fork PRs are rejected. The instance operator controls
which repository owners can request builds.

**How long does data last?** A preview's database is temporary and can be
recreated on redeploy. Closing the PR or reaching the configured TTL removes
the preview.

**How many previews fit?** Docker capacity depends on the host. Kubernetes
capacity depends on the configured preview namespaces and builder capacity.
