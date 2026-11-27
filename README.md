<div align="center">

<img src="assets/logo.png" alt="Hatch" width="96" />

# Hatch

Self-hosted previews for GitHub pull requests. Run the app, database, and supporting services together in a temporary environment.

[Website](https://hatchpr.dev) · [Documentation](https://hatchpr.dev/docs/) · [GitHub App](https://github.com/apps/hatchpr) · [Demo repository](https://github.com/itsaam/hatch-demo-node)

</div>

## Request your first preview

1. Install the GitHub App on a repository allowed by your Hatch instance.
2. Add a root `Dockerfile` and `.hatch.yml`. Replace `3000` with your app's listening port.

   ```yaml
   version: 1
   services:
     web:
       build: .
       port: 3000
       expose: true
   ```

3. Open a PR and comment `@hatchpr preview` on its own. Hatch posts the preview link on the PR.

Repeat the command after pushing a commit. To build automatically, set `previews.trigger: auto` explicitly. Closing the PR removes the preview and its temporary data.

The [Node and PostgreSQL demo](https://github.com/itsaam/hatch-demo-node) shows a multi-service configuration. The [CLI](cli/README.md) can generate a starting configuration for your repository.

## Deploy Hatch

- [Kubernetes and Argo CD](deploy/kubernetes/README.md) for a remote builder, registry and isolated preview namespaces.
- [Docker Compose](deploy/docker-compose.md) for a Docker host behind Traefik. See [socket hardening](security/README.md) for the optional Docker API filter.

[Report an issue](https://github.com/itsaam/hatch/issues) · [License](LICENSE)
