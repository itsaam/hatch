# HatchPR on Kubernetes

Hatch runs one API replica with the Recreate strategy, a PostgreSQL 16
StatefulSet with a 10 Gi PVC, and a separate static NGINX landing page in
`hatch-system`. The API creates preview resources in four preprovisioned
namespaces, `hatch-preview-1` through `hatch-preview-4`.

Each namespace holds one active preview. The API keeps its queue in PostgreSQL
and defaults to three concurrent operations. A Kubernetes restart can resume
queued work. The API Deployment uses `Recreate` and requires one replica.
Add preview slots and worker-node capacity to host more previews, and size
BuildKit separately for concurrent builds.

## Prepare the deployment

1. Copy [the example overlay](overlays/example/) and set the GitHub owner
   allowlist, preview registry repository, BuildKit endpoint and server
   certificate name. Set `HATCH_DOMAIN` to the DNS suffix for preview URLs,
   `ALLOWED_ORIGIN` to the exact public landing origin, and update the system
   Ingress hosts and TLS reference. Set both Ingress class settings to the class
   installed in the cluster. Point the landing host, API host and wildcard
   preview DNS record (for example, `*.hatch.example.org`) to your ingress.
   Set `HATCH_TRUSTED_PROXY_CIDRS` to the comma-separated networks of trusted
   proxies that sanitize or append `X-Forwarded-For`. Without it the API limits
   requests by the immediate connection address, which can be shared behind an ingress.
2. Use a cluster with Pod Security Admission, a NetworkPolicy-capable CNI, an
   ingress controller and a default dynamic StorageClass. To select a different
   StorageClass or size, patch the PostgreSQL claim template before the first
   deployment. Back up PostgreSQL and `HATCH_SECRET_KEY` together.
3. Match the ingress and DNS selectors in the [preview network policy](base/preview-slot/network.yaml)
   to the target cluster. The base permits ingress from the selected controller
   and communication within each preview namespace. Egress permits same-slot
   traffic, CoreDNS on TCP/UDP 53, and public IPv4 destinations. Add any Pod,
   Service, node, or organization CIDRs outside the listed exclusions to all
   slots. For NodeLocal DNS, allow only the resolver address and port if required
   by your CNI. Check host-network ingress and local-node traffic with your CNI
   and enforce host and cloud metadata restrictions separately where needed.
   Preview applications in the same slot can reach each other. Use separate
   slots for workloads that require network separation.
4. Provision a dedicated external BuildKit on isolated infrastructure with mTLS.
   Restrict access to the API's network, use a server certificate matching
   `HATCH_BUILDKIT_SERVER_NAME`, and use a dedicated client certificate for Hatch.
   Restrict build executor access to sensitive networks and the daemon socket.
   The builder must be able to push to the preview registry. The API uses
   `HATCH_REGISTRY_CONFIG` to pass registry authentication to BuildKit.
   [BuildKit TCP and TLS documentation](https://github.com/moby/buildkit#expose-buildkit-as-a-tcp-service)
5. Select matching API and landing images by digest or published tag in your
   overlay's `images` field. The base uses the mutable `edge` tag. The landing
   image embeds `VITE_API_URL` at build time. The upstream image defaults to
   `https://api.hatchpr.dev`. For a different API hostname, build the landing
   Dockerfile with the corresponding `VITE_API_URL` and set `HATCH_PUBLIC_API_URL`
   in the overlay for the dashboard. For GHCR builds in this repository, set the
   `HATCH_LANDING_API_URL` Actions variable.

## Preview compatibility

Use a dedicated builder and restrict `HATCH_ALLOWED_OWNERS` to trusted GitHub
owners. GitHub App installations by other owners and pull requests from forks
are skipped. Repositories, build contexts, and Dockerfiles are fetched at the
PR commit, with `dockerfile` relative to `build`. Kubernetes builds consume a
GitHub archive and require a Dockerfile that builds from that archive. Set an
explicit `port` if the Dockerfile relies on variable or inherited `EXPOSE`.
Hatch reads the current PR state from GitHub before updating the queue. If
GitHub App mode is disabled, these reads use the unauthenticated GitHub API,
which supports only public repositories and has a lower request limit.
`HATCH_BUILD_SECRETS` applies to the Docker runtime. Dockerfiles that require
these secret mounts need to be adapted before they can run on Kubernetes.
Set up retention in the preview registry for pushed image tags and cached layers.

## Public HTTPS without Dokploy

Use an ingress controller or an external edge that routes the landing host,
API host and wildcard preview domain. For Cloudflare Tunnel, run a dedicated
connector in Kubernetes and point the public hosts at the Kubernetes ingress
controller's internal Service. Keep the original Host header for routing. If
the tunnel terminates public TLS and the in-cluster leg uses HTTP, remove the
system Ingress TLS block and leave `HATCH_KUBE_TLS_SECRET` empty, then configure
the cluster ingress HTTP entrypoint accordingly. Confirm wildcard DNS and edge
certificate coverage before using a test hostname. See [Cloudflare Tunnel routing](https://developers.cloudflare.com/tunnel/concepts/routing/).

The [Sunny overlay](overlays/sunny/) enables `HATCH_PREVIEW_OWNER_ONLY`.
Only the GitHub repository owner can run preview commands. PR events cannot
start builds. The bot asks the owner to confirm the exact head with
`@hatchpr preview <commit-sha>`. To redeploy on Sunny, use the same command.
Closing a PR removes its preview. Reopening requires a fresh confirmation.

Sunny uses `hatchpr.dev` and `api.hatchpr.dev` with the
`traefik-kubernetes` ingress class. It expects HTTPS at Cloudflare and
HTTP from the connector to Traefik; route `www.hatchpr.dev` to the canonical site
with a redirect at the edge. Before syncing, set the real builder address and
server name through `hatch-api-env`, configure the registry credentials, and
pin the published images by digest in the GitOps configuration. Check the
webhook, public TLS and preview lifecycle before moving the production DNS.
The overlay restricts access to the API pods to Traefik and trusts Forwarded
For hops from Sunny's current pod subnet and Cloudflare connector. Update the
trusted ranges when adding ingress nodes or replacing the connector. Verify
per-client request limits through the ingress before cutover.

## Provision credentials

Bootstrap the namespaces and preview policies before provisioning credentials or
starting the API.

```sh
kubectl apply -f deploy/kubernetes/base/system/namespace.yaml
kubectl apply -k deploy/kubernetes/base/previews
```

Populate the following Kubernetes Secrets from your existing secret store or
certificate manager. For manual setup, use files exported to a protected
directory outside the repository with `kubectl create secret ... --from-file`
or `--from-env-file`. For example, after exporting the PEMs from the secret store,
use the following command.

```sh
kubectl -n hatch-system create secret generic hatch-buildkit-tls \
  --from-file=ca.pem=/secure/hatch/ca.pem \
  --from-file=cert.pem=/secure/hatch/cert.pem \
  --from-file=key.pem=/secure/hatch/key.pem
```

| Namespace | Secret | Keys |
| --- | --- | --- |
| `hatch-system` | `hatch-postgres-env` | `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB` |
| `hatch-system` | `hatch-api-env` | `DATABASE_URL`, `GITHUB_WEBHOOK_SECRET`, `GITHUB_APP_ID` when enabled, `HATCH_SECRET_KEY`, `HATCH_ADMIN_TOKEN` |
| `hatch-system` | `hatch-github-app` | `github-app.pem` when GitHub App mode is enabled |
| `hatch-system` | `hatch-buildkit-tls` | `ca.pem`, `cert.pem`, `key.pem` |
| `hatch-system` | `hatch-registry-config` | `.dockerconfigjson` (a self-contained Docker config with inline registry auth) |
| `hatch-system` | `hatch-public-tls` | `tls.crt`, `tls.key` for the API and landing hosts |
| Each preview slot | `HATCH_KUBE_TLS_SECRET` value, if set | `tls.crt`, `tls.key` for the wildcard preview DNS name |
| Each preview slot | `HATCH_KUBE_IMAGE_PULL_SECRET` value, if set | `.dockerconfigjson` with read-only access to the preview registry |

The API's `DATABASE_URL` uses the PostgreSQL credentials, with URL-escaped
credentials as needed, and the host `hatch-postgres:5432`. For example, use
`postgres://USER:PASSWORD@hatch-postgres:5432/DB?sslmode=disable` within the cluster.
Keep the encryption key stable when rotating the GitHub webhook secret. Set
`hatch-fallback:<old webhook secret>` as the value of `HATCH_SECRET_KEY` when
the previous system used the webhook-derived fallback. Verify decryption with
restored data before exposing production traffic. Use
Secrets of type `kubernetes.io/tls` for Ingress TLS and
`kubernetes.io/dockerconfigjson` for the registry and pull credentials.
Supply preview TLS through the ingress controller's default wildcard certificate
if `HATCH_KUBE_TLS_SECRET` is omitted. If the API or landing GHCR packages are
private, configure pull-only credentials separately in `hatch-system` and patch
the corresponding pod specs. Preview pods use an unbound default service account
with token automount disabled. The kubelet uses pull credentials independently.

## Sync manually

**Argo CD.** Adapt [the sample Application](argocd-application.example.yaml) with
your repository, overlay and AppProject. The destination project and Argo CD
controller need permission to manage the installation namespaces and their
declared resources. Apply the Application and select **Sync**, or run
`argocd app sync hatch` after credential provisioning. Sync waves put namespaces
and slot policies ahead of workloads. Automated sync is disabled.

**kubectl.** After bootstrapping and provisioning credentials, apply the overlay
and wait for the workloads. These commands use the example path.

```sh
kubectl kustomize deploy/kubernetes/overlays/example
kubectl apply -k deploy/kubernetes/overlays/example
kubectl -n hatch-system rollout status statefulset/hatch-postgres
kubectl -n hatch-system rollout status deployment/hatch-api
kubectl -n hatch-system rollout status deployment/hatch-landing
```

Verify the public API at `/readyz` and open the landing URL. Point the GitHub App
webhook to `https://<api-host>/api/github/webhook` and subscribe to
`pull_request` and `issue_comment`. Grant Contents and Pull requests read access
and Issues read and write access. Open a test PR in an allowed
repository, comment `@hatchpr preview`, and confirm the external build, image pull, HTTPS preview and cleanup
on PR close.

## Migrate from Docker Compose

1. Back up the legacy PostgreSQL database and test its restoration. Recover
   the existing GitHub App configuration and encryption key from the secret
   store. After restoring the target database, verify it can decrypt existing
   repository secrets and account for legacy preview records.
2. Test one real PR before cutover. Confirm the GitHub App receives
   `issue_comment`, the builder pushes its image, Kubernetes pulls it, and the
   preview serves HTTPS. Verify SQL seeding, logs, the GitHub comment and cleanup
   on close.
3. Stop the old instance before switching the site, API and preview routing.
   Route GitHub webhooks to the ready API. Retain the original volume and
   configuration for rollback and plan how to handle writes made after cutover.

## Add a preview namespace

Copy an existing slot directory under [base/previews](base/previews/), change
its namespace name in both files, and add it to that directory's Kustomization.
Add the namespace to `HATCH_KUBE_PREVIEW_NAMESPACES` in the API ConfigMap
and provision the optional TLS and pull Secrets before it receives a preview.
The template binds the `hatch-system/hatch-api` service account to a Role in
each slot. The Role covers preview resources and pod logs. The API service
account has no cluster-wide role.

## Recover a reserved slot after an interrupted Kubernetes API write

Hatch records in-progress writes in `hatch-pending.*` or `hatch-seed-pending.*`
ConfigMaps. An abrupt coordinator failure or an ambiguous Kubernetes response
can leave a slot reserved when Hatch cannot establish whether a write happened.
Pause the API by scaling it to zero in the desired Git configuration. Use
Kubernetes API diagnostics to confirm the old create is no longer in flight,
then inspect the claim, marker and named resource in that slot. If the named
object exists under the claim, let Hatch reconcile it when the API resumes. If
the create definitively did not happen, remove only the reviewed pending marker,
then resume the API so it can clean up the slot. If the outcome is still unclear,
keep the slot reserved and use another namespace until it is resolved.

## Image workflow

Pull requests compile the API and landing images on GitHub-hosted runners.
Trivy reports vulnerabilities in both images without blocking on findings. Pushes
to `main` or `v*` tags publish both images to GHCR with full `sha-<commit>` tags,
plus `edge` for `main` or the Git tag for releases. Deployment is a separate
manual operation.


## Error monitoring

Set `SENTRY_DSN` in the existing `hatch-api-env` Secret to send API and
preview worker errors to a dedicated GlitchTip project. Set
`SENTRY_ENVIRONMENT` and `SENTRY_RELEASE` for the deployment environment and
source revision. The API reports HTTP 5xx responses, panics, and unexpected
delivery, deploy and cleanup failures. Events use route templates and fixed
categories. Request bodies, headers, login data and raw worker errors are
excluded. Monitoring is optional and does not affect readiness.
