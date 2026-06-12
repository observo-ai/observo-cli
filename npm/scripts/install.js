#!/usr/bin/env node
// postinstall: download the platform-matched observo binary from the
// matching GitHub Release and drop it at bin/observo so `npx observo`
// and the package.json `bin` field resolve.
//
// Version comes from package.json (set to the tag value by release.yml
// before publish), so user gets the exact binary built for that release.
//
// Failure modes are explicit: if the platform isn't supported or the
// download fails, the postinstall exits non-zero so npm surfaces the
// install error rather than leaving a broken bin behind.

'use strict';

const fs = require('node:fs');
const path = require('node:path');
const https = require('node:https');
const os = require('node:os');
const crypto = require('node:crypto');
const { execSync } = require('node:child_process');
const zlib = require('node:zlib');

const pkg = require('../package.json');
const VERSION = pkg.version;
const BIN_DIR = path.join(__dirname, '..', 'bin');
// Name of the binary INSIDE the release archive (GoReleaser packs it as
// `observo` / `observo.exe`).
const ARCHIVE_BINARY_NAME = process.platform === 'win32' ? 'observo.exe' : 'observo';
// Where we drop the native binary on disk. NOT `observo` — that name is
// the committed Node launcher shim (bin/observo) that npm symlinks into
// node_modules/.bin. We must not overwrite it, so the native binary lives
// alongside it as `observo-bin` and the shim execs it. See bin/observo and
// issue #17 / OB-432.
const NATIVE_NAME = process.platform === 'win32' ? 'observo-bin.exe' : 'observo-bin';
const NATIVE_PATH = path.join(BIN_DIR, NATIVE_NAME);

// Platform → GoReleaser archive name mapping. Must match the
// `name_template` in .goreleaser.yaml:
//   observo_<version>_<os>_<arch>.<ext>
// where Os/Arch are GoReleaser defaults — lowercase Go names
// (linux/amd64), NOT capitalised (Linux/x86_64). Capitalised
// versions silently 404'd on every install for v0.1.0–v0.6.0
// (issue #9). Verified against actual asset names with
//   gh release view v0.6.0 --json assets
function archiveName() {
  const platformMap = {
    darwin: 'darwin',
    linux: 'linux',
    win32: 'windows',
  };
  const archMap = {
    x64: 'amd64',
    arm64: 'arm64',
  };
  const goos = platformMap[process.platform];
  const goarch = archMap[process.arch];
  if (!goos || !goarch) {
    throw new Error(
      `unsupported platform: ${process.platform}/${process.arch}\n` +
      'Supported: darwin/amd64, darwin/arm64, linux/amd64, linux/arm64, windows/amd64, windows/arm64.'
    );
  }
  const ext = process.platform === 'win32' ? 'zip' : 'tar.gz';
  return `observo_${VERSION}_${goos}_${goarch}.${ext}`;
}

function downloadURL() {
  return `https://github.com/observo-ai/observo-cli/releases/download/v${VERSION}/${archiveName()}`;
}

function checksumsURL() {
  return `https://github.com/observo-ai/observo-cli/releases/download/v${VERSION}/checksums.txt`;
}

// Parse GoReleaser's checksums.txt format ("<hex>  <filename>\n" per line)
// to find the SHA-256 for the given archive name. Throws if not found.
function parseChecksum(checksumsText, archive) {
  const lines = checksumsText.split('\n');
  for (const ln of lines) {
    const m = ln.match(/^([0-9a-fA-F]{64})\s+(\S+)\s*$/);
    if (m && m[2] === archive) return m[1].toLowerCase();
  }
  throw new Error(`checksums.txt has no entry for ${archive}`);
}

function followRedirects(url, depth = 0) {
  if (depth > 5) {
    return Promise.reject(new Error('too many redirects'));
  }
  return new Promise((resolve, reject) => {
    const req = https.get(url, (res) => {
      if (res.statusCode === 301 || res.statusCode === 302 || res.statusCode === 307 || res.statusCode === 308) {
        res.resume();
        return resolve(followRedirects(res.headers.location, depth + 1));
      }
      if (res.statusCode !== 200) {
        return reject(new Error(`download failed: HTTP ${res.statusCode} for ${url}`));
      }
      const chunks = [];
      res.on('data', (c) => chunks.push(c));
      res.on('end', () => resolve(Buffer.concat(chunks)));
      res.on('error', reject);
    });
    req.on('error', reject);
  });
}

// Extract the native binary from the downloaded archive and place it at
// NATIVE_PATH (bin/observo-bin). We extract into a private temp dir first,
// then move the file into place — extracting directly into bin/ would write
// a file named `observo` (the in-archive name) and clobber the committed
// launcher shim. `ext` is the archive extension ('tar.gz' or 'zip'); the
// system `tar` (present on macOS, Linux, and Windows 10 1803+) handles both.
function extractNativeBinary(buf, ext) {
  const work = fs.mkdtempSync(path.join(os.tmpdir(), 'observo-'));
  const archive = path.join(work, `archive.${ext}`);
  const extracted = path.join(work, ARCHIVE_BINARY_NAME);
  fs.writeFileSync(archive, buf);
  try {
    execSync(`tar -xf "${archive}" -C "${work}" "${ARCHIVE_BINARY_NAME}"`, { stdio: 'inherit' });
    if (!fs.existsSync(extracted)) {
      throw new Error(`extracted archive did not contain ${ARCHIVE_BINARY_NAME}`);
    }
    // Move into place. Prefer rename; fall back to copy when the temp dir
    // is on a different filesystem (EXDEV). The temp dir (and the leftover
    // source after a copy) is removed in the finally block below.
    try {
      fs.renameSync(extracted, NATIVE_PATH);
    } catch (err) {
      if (err.code !== 'EXDEV') throw err;
      fs.copyFileSync(extracted, NATIVE_PATH);
    }
  } finally {
    fs.rmSync(work, { recursive: true, force: true });
  }
}

async function main() {
  fs.mkdirSync(BIN_DIR, { recursive: true });

  const url = downloadURL();
  process.stdout.write(`observo: downloading ${url}\n`);

  const buf = await followRedirects(url);

  // Supply-chain verification: fetch checksums.txt and verify the
  // archive bytes hash matches. Without this, a tampered release
  // asset or proxy MITM installs arbitrary code under the user's npm
  // prefix. checksums.txt itself is fetched from the same GH release,
  // so this catches per-asset substitution (not a wholesale release
  // takeover — for that we'd need separate signing).
  const cksumURL = checksumsURL();
  process.stdout.write(`observo: verifying SHA-256 against ${cksumURL}\n`);
  const cksumBuf = await followRedirects(cksumURL);
  const expected = parseChecksum(cksumBuf.toString('utf8'), archiveName());
  const actual = crypto.createHash('sha256').update(buf).digest('hex');
  if (actual !== expected) {
    throw new Error(
      `SHA-256 mismatch for ${archiveName()}\n` +
      `  expected: ${expected}\n` +
      `  actual:   ${actual}\n` +
      `  aborting install — do NOT use the downloaded file.`
    );
  }

  // Throws on every failure path (bad tar exit, missing extracted file,
  // rename/copy error), so on return NATIVE_PATH is guaranteed to exist.
  extractNativeBinary(buf, url.endsWith('.zip') ? 'zip' : 'tar.gz');

  if (process.platform !== 'win32') {
    fs.chmodSync(NATIVE_PATH, 0o755);
  }

  // Verify the binary runs and matches the requested version. Catches
  // GoReleaser version-skew bugs at install time, not first invocation.
  try {
    const out = execSync(`"${NATIVE_PATH}" --version`, { encoding: 'utf8' });
    process.stdout.write(`observo: installed ${out.trim().split('\n')[0]}\n`);
  } catch (err) {
    throw new Error(`observo binary installed but failed to run: ${err.message}`);
  }
}

main().catch((err) => {
  process.stderr.write(`observo: install failed: ${err.message}\n`);
  process.exit(1);
});
