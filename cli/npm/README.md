# @hatchpr/cli

The npm wrapper for the [Hatch](https://hatchpr.dev) configuration CLI.

The native Go binary is downloaded from GitHub Releases on install, matching your OS/arch.

## Install

```sh
# One-shot use
npx @hatchpr/cli init

# Or install globally
npm install -g @hatchpr/cli
hatch init
```

## Usage

```sh
cd your-repo
hatch init                 # detect stack + write .hatch.yml
hatch init --dry-run       # print YAML to stdout, don't write
hatch init --ai            # print a setup prompt for a coding agent
hatch init --force         # overwrite an existing .hatch.yml
```

Run `npx @hatchpr/cli init --ai` to get a prompt for a coding agent with
local access to your repository.

Review the generated `.hatch.yml`, then open a PR and comment
`@hatchpr preview` on its own. See the [configuration reference](https://hatchpr.dev/docs/#config).

## How it works

On `npm install`, the `postinstall` hook (`install.js`) detects your platform
(see supported platforms below), downloads the matching archive from
`https://github.com/itsaam/hatch/releases/download/v<version>/…`, verifies the
SHA-256 checksum, and extracts the native binary into `vendor/`. The executable in `bin/` forwards
commands to that binary.

No runtime dependencies.

## Supported platforms

| OS       | arch            |
|----------|-----------------|
| macOS    | x64, arm64      |
| Linux    | x64, arm64      |
| Windows  | x64             |

## License

MIT — see [LICENSE](./LICENSE).
