# @hatchpr/cli

Official npm wrapper for the [Hatch](https://hatchpr.dev) CLI — self-hosted PR preview deployments.

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
hatch init --dry-run       # print to stdout, don't write
hatch init --force         # overwrite an existing .hatch.yml
```

See the full docs: https://hatchpr.dev

## How it works

On `npm install`, the `postinstall` hook (`install.js`) detects your platform
(`darwin`/`linux`/`win32`, `x64`/`arm64`), downloads the matching archive from
`https://github.com/itsaam/hatch/releases/download/v<version>/…`, verifies the
SHA-256 checksum, and extracts the `hatch` binary into `bin/`.

No runtime dependencies.

## Supported platforms

| OS       | arch            |
|----------|-----------------|
| macOS    | x64, arm64      |
| Linux    | x64, arm64      |
| Windows  | x64             |

## License

MIT — see [LICENSE](./LICENSE).
