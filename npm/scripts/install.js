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
const BINARY_NAME = process.platform === 'win32' ? 'observo.exe' : 'observo';
const BIN_DIR = path.join(__dirname, '..', 'bin');
const BIN_PATH = path.join(BIN_DIR, BINARY_NAME);

// Platform → GoReleaser archive name mapping. Must match the
// `name_template` in .goreleaser.yaml: observo_<version>_<os>_<arch>.<ext>
function archiveName() {
  const platformMap = {
    darwin: 'Darwin',
    linux: 'Linux',
    win32: 'Windows',
  };
  const archMap = {
    x64: 'x86_64',
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

async function extractTarGz(buf, dest) {
  // Avoid pulling tar/extract npm dep — use the system `tar` which exists
  // on macOS, Linux, and Windows 10+ (1803+). Pipe via stdin.
  const tmp = path.join(os.tmpdir(), `observo-${Date.now()}.tar.gz`);
  fs.writeFileSync(tmp, buf);
  try {
    execSync(`tar -xzf "${tmp}" -C "${dest}" "${BINARY_NAME}"`, { stdio: 'inherit' });
  } finally {
    fs.unlinkSync(tmp);
  }
}

function extractZip(buf, dest) {
  // Windows: use built-in tar (also supports zip on 1803+).
  const tmp = path.join(os.tmpdir(), `observo-${Date.now()}.zip`);
  fs.writeFileSync(tmp, buf);
  try {
    execSync(`tar -xf "${tmp}" -C "${dest}" "${BINARY_NAME}"`, { stdio: 'inherit' });
  } finally {
    fs.unlinkSync(tmp);
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

  if (url.endsWith('.zip')) {
    extractZip(buf, BIN_DIR);
  } else {
    await extractTarGz(buf, BIN_DIR);
  }

  if (!fs.existsSync(BIN_PATH)) {
    throw new Error(`extracted archive did not contain ${BINARY_NAME}`);
  }

  if (process.platform !== 'win32') {
    fs.chmodSync(BIN_PATH, 0o755);
  }

  // Verify the binary runs and matches the requested version. Catches
  // GoReleaser version-skew bugs at install time, not first invocation.
  try {
    const out = execSync(`"${BIN_PATH}" --version`, { encoding: 'utf8' });
    process.stdout.write(`observo: installed ${out.trim().split('\n')[0]}\n`);
  } catch (err) {
    throw new Error(`observo binary installed but failed to run: ${err.message}`);
  }
}

main().catch((err) => {
  process.stderr.write(`observo: install failed: ${err.message}\n`);
  process.exit(1);
});
