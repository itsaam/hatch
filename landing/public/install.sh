#!/bin/sh
# Hatch CLI installer — https://hatchpr.dev/install.sh
#
# Usage:
#   curl -fsSL https://hatchpr.dev/install.sh | sh
#   curl -fsSL https://hatchpr.dev/install.sh | sh -s -- --version v0.1.0
#
# POSIX sh compatible. No bashisms.

set -eu

REPO="itsaam/hatch"
BIN_NAME="hatch"
REQUESTED_VERSION=""

# ── Colors (ANSI, fallback to plain) ──────────────────────────────────────
if [ -t 1 ] && [ -n "${TERM:-}" ] && [ "${TERM}" != "dumb" ]; then
  C_RESET="$(printf '\033[0m')"
  C_BOLD="$(printf '\033[1m')"
  C_DIM="$(printf '\033[2m')"
  C_GREEN="$(printf '\033[32m')"
  C_YELLOW="$(printf '\033[33m')"
  C_RED="$(printf '\033[31m')"
  C_CYAN="$(printf '\033[36m')"
else
  C_RESET=""; C_BOLD=""; C_DIM=""; C_GREEN=""; C_YELLOW=""; C_RED=""; C_CYAN=""
fi

info()  { printf '%s  %s%s\n' "$C_DIM" "$1" "$C_RESET"; }
step()  { printf '%s%s%s %s\n' "$C_CYAN" "$1" "$C_RESET" "$2"; }
ok()    { printf '%s%s %s%s\n' "$C_GREEN" "$1" "$2" "$C_RESET"; }
warn()  { printf '%s%s%s\n' "$C_YELLOW" "$1" "$C_RESET" >&2; }
err()   { printf '%s%s%s\n' "$C_RED" "$1" "$C_RESET" >&2; }

die() { err "Error: $1"; exit 1; }

# ── Parse args ────────────────────────────────────────────────────────────
while [ $# -gt 0 ]; do
  case "$1" in
    --version)
      [ $# -ge 2 ] || die "--version requires an argument"
      REQUESTED_VERSION="$2"; shift 2
      ;;
    --version=*)
      REQUESTED_VERSION="${1#--version=}"; shift
      ;;
    -h|--help)
      cat <<EOF
Hatch CLI installer

Usage:
  curl -fsSL https://hatchpr.dev/install.sh | sh
  curl -fsSL https://hatchpr.dev/install.sh | sh -s -- --version v0.1.0

Options:
  --version <tag>   Install a specific version (e.g. v0.1.0). Defaults to latest.
  -h, --help        Show this help.
EOF
      exit 0
      ;;
    *)
      die "Unknown argument: $1"
      ;;
  esac
done

# ── Detect downloader ─────────────────────────────────────────────────────
if command -v curl >/dev/null 2>&1; then
  DL_CMD="curl"
elif command -v wget >/dev/null 2>&1; then
  DL_CMD="wget"
else
  die "Neither curl nor wget found. Install one and retry."
fi

fetch_to_stdout() {
  # $1 = URL
  if [ "$DL_CMD" = "curl" ]; then
    curl -fsSL "$1"
  else
    wget -qO- "$1"
  fi
}

fetch_to_file() {
  # $1 = URL, $2 = dest
  if [ "$DL_CMD" = "curl" ]; then
    curl -fsSL -o "$2" "$1"
  else
    wget -qO "$2" "$1"
  fi
}

# ── Detect platform ───────────────────────────────────────────────────────
UNAME_S="$(uname -s)"
UNAME_M="$(uname -m)"

case "$UNAME_S" in
  Darwin) GO_OS="Darwin" ;;
  Linux)  GO_OS="Linux"  ;;
  *)      die "Unsupported OS: $UNAME_S (this script supports macOS and Linux; use the npm package on Windows)" ;;
esac

case "$UNAME_M" in
  x86_64|amd64)      GO_ARCH="amd64" ;;
  arm64|aarch64)     GO_ARCH="arm64" ;;
  *)                 die "Unsupported arch: $UNAME_M" ;;
esac

PLATFORM="$(echo "$GO_OS" | tr '[:upper:]' '[:lower:]')-$GO_ARCH"

# ── Resolve version ───────────────────────────────────────────────────────
printf '%s🥚 Installing hatch CLI...%s\n' "$C_BOLD" "$C_RESET"

if [ -z "$REQUESTED_VERSION" ]; then
  step "  Resolving" "latest version..."
  LATEST_JSON="$(fetch_to_stdout "https://api.github.com/repos/${REPO}/releases/latest" || true)"
  [ -n "$LATEST_JSON" ] || die "Failed to query GitHub API for latest release."
  # Extract tag_name
  VERSION_TAG="$(printf '%s\n' "$LATEST_JSON" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
  [ -n "$VERSION_TAG" ] || die "Could not parse latest tag from GitHub API response."
else
  VERSION_TAG="$REQUESTED_VERSION"
  case "$VERSION_TAG" in
    v*) ;;
    *)  VERSION_TAG="v$VERSION_TAG" ;;
  esac
fi

VERSION_NUM="${VERSION_TAG#v}"

info "Platform: $PLATFORM"
info "Version:  $VERSION_TAG"

# ── Download archive + checksums ──────────────────────────────────────────
ARCHIVE_NAME="hatch_${VERSION_NUM}_${GO_OS}_${GO_ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION_TAG}"
ARCHIVE_URL="${BASE_URL}/${ARCHIVE_NAME}"
CHECKSUMS_URL="${BASE_URL}/checksums.txt"

TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t hatch-install)"
# shellcheck disable=SC2064
trap "rm -rf \"$TMP_DIR\"" EXIT INT TERM

step "  Downloading" "$ARCHIVE_NAME..."
fetch_to_file "$ARCHIVE_URL" "${TMP_DIR}/${ARCHIVE_NAME}" || die "Download failed: $ARCHIVE_URL"

step "  Fetching" "checksums..."
fetch_to_file "$CHECKSUMS_URL" "${TMP_DIR}/checksums.txt" || die "Failed to fetch $CHECKSUMS_URL"

# ── Verify checksum ───────────────────────────────────────────────────────
step "  Verifying" "checksum..."
EXPECTED="$(grep " ${ARCHIVE_NAME}\$" "${TMP_DIR}/checksums.txt" | awk '{print $1}' | head -n1)"
[ -n "$EXPECTED" ] || die "No checksum entry found for $ARCHIVE_NAME"

if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL="$(sha256sum "${TMP_DIR}/${ARCHIVE_NAME}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  ACTUAL="$(shasum -a 256 "${TMP_DIR}/${ARCHIVE_NAME}" | awk '{print $1}')"
else
  warn "  Warning: neither sha256sum nor shasum available — skipping checksum verification."
  ACTUAL="$EXPECTED"
fi

if [ "$ACTUAL" != "$EXPECTED" ]; then
  die "Checksum mismatch!
  expected: $EXPECTED
  actual:   $ACTUAL"
fi

# ── Extract ───────────────────────────────────────────────────────────────
step "  Extracting" "..."
tar -xzf "${TMP_DIR}/${ARCHIVE_NAME}" -C "$TMP_DIR" || die "Failed to extract archive."

[ -f "${TMP_DIR}/${BIN_NAME}" ] || die "Binary $BIN_NAME not found in archive."

# ── Install ───────────────────────────────────────────────────────────────
# Prefer ~/.local/bin, fall back to /usr/local/bin (may need sudo).
LOCAL_BIN="${HOME}/.local/bin"
SYSTEM_BIN="/usr/local/bin"
INSTALL_DIR=""

if mkdir -p "$LOCAL_BIN" 2>/dev/null && [ -w "$LOCAL_BIN" ]; then
  INSTALL_DIR="$LOCAL_BIN"
elif [ -w "$SYSTEM_BIN" ]; then
  INSTALL_DIR="$SYSTEM_BIN"
elif command -v sudo >/dev/null 2>&1; then
  INSTALL_DIR="$SYSTEM_BIN"
  USE_SUDO=1
else
  die "No writable install location (tried $LOCAL_BIN and $SYSTEM_BIN)."
fi

DEST="${INSTALL_DIR}/${BIN_NAME}"
step "  Installing" "to $DEST"

if [ "${USE_SUDO:-0}" = "1" ]; then
  sudo install -m 0755 "${TMP_DIR}/${BIN_NAME}" "$DEST" || die "Failed to install with sudo."
else
  install -m 0755 "${TMP_DIR}/${BIN_NAME}" "$DEST" 2>/dev/null || {
    cp "${TMP_DIR}/${BIN_NAME}" "$DEST" && chmod 0755 "$DEST"
  } || die "Failed to install binary."
fi

ok "✓" "Done"
printf '\n'

# ── PATH warning ──────────────────────────────────────────────────────────
case ":$PATH:" in
  *":${INSTALL_DIR}:"*)
    printf '  Run: %shatch init%s\n\n' "$C_BOLD" "$C_RESET"
    ;;
  *)
    warn "  Note: $INSTALL_DIR is not in your PATH."
    printf '  Add it to your shell config:\n'
    printf '    %secho '"'"'export PATH="%s:$PATH"'"'"' >> ~/.zshrc%s   # zsh\n' "$C_DIM" "$INSTALL_DIR" "$C_RESET"
    printf '    %secho '"'"'export PATH="%s:$PATH"'"'"' >> ~/.bashrc%s  # bash\n' "$C_DIM" "$INSTALL_DIR" "$C_RESET"
    printf '\n  Then reload your shell and run: %shatch init%s\n\n' "$C_BOLD" "$C_RESET"
    ;;
esac
