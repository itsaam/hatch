package main

const aiInitPrompt = `# Create a HatchPR configuration for this repository

Work in the repository currently open in your coding environment. Inspect the
Dockerfile, Compose configuration, package manifests, example environment files,
and startup scripts relevant to the apps that should run in a PR preview.

- Identify the real build contexts, Dockerfile paths, listening ports,
  dependencies, and environment variable names. Use verified values from the
  repo. Flag missing information or a missing Dockerfile for the user.
- Create .hatch.yml at the repository root. If one already exists, review it
  and propose any changes before overwriting it.
- Use version: 1 and a services map. Each service uses either build or image.
  For Kubernetes, build is a repository-relative context and dockerfile is
  relative to that context. Docker builds from the repository root and takes
  a root-relative dockerfile. Flag nested contexts that require Kubernetes
  or a Dockerfile change.
- Use the container's listening port and set expose: true on the public app
  only. Keep databases and workers internal.
- List service dependencies in depends_on and use their Hatch service names
  as internal hostnames. Use a string-valued env map. Flag requirements for
  Compose-only settings such as command, entrypoint, volumes, and env_file.
- Read example environment files for variable names. Keep actual credentials
  out of the prompt, configuration, and output. Use a reference such as
  ${SECRET_STRIPE_TEST_KEY} for a Hatch secret named STRIPE_TEST_KEY. Use ${DB_PASSWORD}
  when connecting to an isolated preview database that you configure.
- Keep previews.trigger set to mention. Leave application code, deployment
  settings, and Git history untouched.

Example for a project whose Dockerfile serves HTTP on port 3000:

    version: 1
    services:
      web:
        build: .
        port: 3000
        expose: true
    previews:
      trigger: mention

Adapt the example to the actual repository. Check the resulting YAML and verify
each build path, port, dependency, and secret reference against what you found.
Summarize the proposed configuration and list any assumptions or setup still
needed before requesting a preview.
`
