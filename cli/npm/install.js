#!/usr/bin/env node
/**
 * Postinstall script for @hatchpr-preview/cli.
 *
 * Downloads the matching hatch binary from GitHub Releases, verifies its
 * SHA-256 checksum, extracts it, and places it at bin/hatch (or bin/hatch.exe).
 *
 * Zero runtime dependencies — uses only Node.js built-ins + the system `tar`.
 */

'use strict';

const fs = require('fs');
const os = require('os');
const path = require('path');
const https = require('https');
const crypto = require('crypto');
const { spawnSync } = require('child_process');

const REPO = 'itsaam/hatch';
const PKG = require('./package.json');
const VERSION = PKG.version;

const BIN_DIR = path.join(__dirname, 'bin');
const IS_WINDOWS = os.platform() === 'win32';
const BIN_NAME = IS_WINDOWS ? 'hatch.exe' : 'hatch';
const BIN_PATH = path.join(BIN_DIR, BIN_NAME);
const VERSION_MARKER = path.join(BIN_DIR, '.version');

// ── Platform mapping ───────────────────────────────────────────────────────
function resolvePlatform() {
  const plat = os.platform();
  const arch = os.arch();

  // GoReleaser produces capitalized OS names via {{ title .Os }}
  const osMap = { darwin: 'Darwin', linux: 'Linux', win32: 'Windows' };
  const archMap = { x64: 'amd64', arm64: 'arm64' };

  const goOs = osMap[plat];
  const goArch = archMap[arch];

  if (!goOs || !goArch) {
    throw new Error(
      `Unsupported platform: ${plat}/${arch}. ` +
      `Supported: macOS (x64/arm64), Linux (x64/arm64), Windows (x64).`
    );
  }

  if (plat === 'win32' && arch !== 'x64') {
    throw new Error(`Windows ${arch} is not supported yet. Use x64.`);
  }

  const ext = plat === 'win32' ? 'zip' : 'tar.gz';
  const archive = `hatch_${VERSION}_${goOs}_${goArch}.${ext}`;
  const base = `https://github.com/${REPO}/releases/download/v${VERSION}`;

  return {
    plat, arch, goOs, goArch, ext, archive,
    archiveUrl: `${base}/${archive}`,
    checksumUrl: `${base}/checksums.txt`,
    label: `${plat}-${arch}`,
  };
}

// ── HTTP helpers ───────────────────────────────────────────────────────────
function httpGet(url, redirectsLeft = 5) {
  return new Promise((resolve, reject) => {
    const req = https.get(url, { headers: { 'User-Agent': '@hatchpr-preview/cli-installer' } }, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        if (redirectsLeft <= 0) return reject(new Error('Too many redirects'));
        res.resume();
        return httpGet(res.headers.location, redirectsLeft - 1).then(resolve, reject);
      }
      if (res.statusCode !== 200) {
        res.resume();
        return reject(new Error(`GET ${url} → HTTP ${res.statusCode}`));
      }
      resolve(res);
    });
    req.on('error', reject);
    req.setTimeout(60_000, () => {
      req.destroy(new Error(`Timeout fetching ${url}`));
    });
  });
}

async function downloadToFile(url, destPath) {
  const res = await httpGet(url);
  await new Promise((resolve, reject) => {
    const ws = fs.createWriteStream(destPath);
    res.pipe(ws);
    ws.on('finish', () => ws.close((err) => (err ? reject(err) : resolve())));
    ws.on('error', reject);
    res.on('error', reject);
  });
}

async function downloadToString(url) {
  const res = await httpGet(url);
  const chunks = [];
  for await (const chunk of res) chunks.push(chunk);
  return Buffer.concat(chunks).toString('utf8');
}

// ── Checksum ───────────────────────────────────────────────────────────────
function sha256File(filePath) {
  return new Promise((resolve, reject) => {
    const h = crypto.createHash('sha256');
    const rs = fs.createReadStream(filePath);
    rs.on('data', (d) => h.update(d));
    rs.on('end', () => resolve(h.digest('hex')));
    rs.on('error', reject);
  });
}

function findChecksumFor(checksumFile, archiveName) {
  // Format: "<sha256>  <filename>" per line
  for (const line of checksumFile.split(/\r?\n/)) {
    const m = line.trim().match(/^([a-f0-9]{64})\s+(?:\*|\s)?(.+)$/i);
    if (m && m[2].trim() === archiveName) return m[1].toLowerCase();
  }
  return null;
}

// ── Extraction ─────────────────────────────────────────────────────────────
function extract(archivePath, outDir, ext) {
  // tar handles both tar.gz (-xzf) and zip (-xf on bsdtar / Windows 10+ tar.exe).
  const args = ext === 'zip' ? ['-xf', archivePath, '-C', outDir] : ['-xzf', archivePath, '-C', outDir];
  const r = spawnSync('tar', args, { stdio: 'inherit' });
  if (r.error) {
    throw new Error(
      `Failed to run tar: ${r.error.message}. ` +
      `Please install tar (macOS/Linux ship it; Windows 10+ includes tar.exe).`
    );
  }
  if (r.status !== 0) {
    throw new Error(`tar exited with status ${r.status} while extracting ${archivePath}`);
  }
}

// ── Skip if already installed ──────────────────────────────────────────────
function alreadyInstalled() {
  try {
    if (!fs.existsSync(BIN_PATH)) return false;
    if (!fs.existsSync(VERSION_MARKER)) return false;
    return fs.readFileSync(VERSION_MARKER, 'utf8').trim() === VERSION;
  } catch {
    return false;
  }
}

// ── Main ───────────────────────────────────────────────────────────────────
async function run() {
  // Skip during local dev linking, CI dry-runs, etc.
  if (process.env.HATCH_SKIP_DOWNLOAD === '1') {
    console.log('[@hatchpr-preview/cli] HATCH_SKIP_DOWNLOAD=1 — skipping binary download.');
    return;
  }

  const p = resolvePlatform();

  if (alreadyInstalled()) {
    console.log(`[@hatchpr-preview/cli] hatch v${VERSION} already installed at ${BIN_PATH}`);
    return;
  }

  console.log(`[@hatchpr-preview/cli] Downloading hatch v${VERSION} for ${p.label}…`);

  if (!fs.existsSync(BIN_DIR)) fs.mkdirSync(BIN_DIR, { recursive: true });

  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'hatchpr-cli-'));
  const archivePath = path.join(tmpDir, p.archive);

  const attempt = async (n) => {
    try {
      await downloadToFile(p.archiveUrl, archivePath);
    } catch (err) {
      if (n < 2) {
        console.warn(`[@hatchpr-preview/cli] Download failed (${err.message}) — retrying…`);
        return attempt(n + 1);
      }
      throw err;
    }
  };

  try {
    await attempt(1);
  } catch (err) {
    if (String(err.message).includes('404')) {
      throw new Error(
        `Release v${VERSION} not found on GitHub yet.\n` +
        `Check https://github.com/${REPO}/releases — the tag may not be published.`
      );
    }
    throw err;
  }

  console.log('[@hatchpr-preview/cli] Verifying checksum…');
  const checksumsText = await downloadToString(p.checksumUrl);
  const expected = findChecksumFor(checksumsText, p.archive);
  if (!expected) {
    throw new Error(`No SHA-256 entry for ${p.archive} in checksums.txt`);
  }
  const actual = await sha256File(archivePath);
  if (actual !== expected) {
    throw new Error(
      `Checksum mismatch for ${p.archive}\n  expected: ${expected}\n  actual:   ${actual}`
    );
  }

  console.log('[@hatchpr-preview/cli] Extracting…');
  extract(archivePath, tmpDir, p.ext);

  // Locate the binary inside the extracted files
  const extractedBin = path.join(tmpDir, BIN_NAME);
  if (!fs.existsSync(extractedBin)) {
    throw new Error(`Binary ${BIN_NAME} not found after extraction in ${tmpDir}`);
  }

  fs.copyFileSync(extractedBin, BIN_PATH);
  if (!IS_WINDOWS) fs.chmodSync(BIN_PATH, 0o755);
  fs.writeFileSync(VERSION_MARKER, VERSION);

  // Best-effort cleanup
  try { fs.rmSync(tmpDir, { recursive: true, force: true }); } catch { /* ignore */ }

  console.log(`[@hatchpr-preview/cli] Done. Installed hatch v${VERSION} → ${BIN_PATH}`);
}

run().catch((err) => {
  console.error(`[@hatchpr-preview/cli] Install failed: ${err.message}`);
  console.error(
    `\nYou can still install hatch manually:\n` +
    `  curl -fsSL https://hatchpr.dev/install.sh | sh\n` +
    `  # or download from https://github.com/${REPO}/releases\n`
  );
  process.exit(1);
});
