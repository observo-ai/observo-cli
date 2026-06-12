#!/usr/bin/env node
// Regression guard for issue #17 / OB-432. Every check runs in CI WITHOUT a
// published release, so it gates the PR that introduces the fix:
//
//   1. The published tarball must contain bin/observo (the launcher shim).
//      If it ever drops out again, npm creates no node_modules/.bin/observo
//      symlink and every fresh `npm install` breaks (`observo` / `npx
//      observo` → ENOENT), silently killing the Playwright reporter
//      writeback chain.
//   2. The shim must be valid JS, must exec the native sidecar binary while
//      forwarding argv + exit code, and must fail loudly (non-zero + a
//      pointer to `npm rebuild`) when the native binary is absent.
'use strict';

const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const assert = require('node:assert');
const { execFileSync, spawnSync } = require('node:child_process');

const PKG_DIR = path.join(__dirname, '..');
const SHIM = path.join(PKG_DIR, 'bin', 'observo');
let failures = 0;

function check(name, fn) {
  try {
    fn();
    process.stdout.write(`  ok   ${name}\n`);
  } catch (err) {
    failures++;
    process.stdout.write(`  FAIL ${name}\n       ${err.message}\n`);
  }
}

// 1. The shim must ship in the npm tarball.
check('npm pack includes bin/observo', () => {
  const dest = fs.mkdtempSync(path.join(os.tmpdir(), 'observo-pack-'));
  try {
    const out = execFileSync('npm', ['pack', '--json', '--pack-destination', dest], {
      cwd: PKG_DIR, encoding: 'utf8',
    });
    const files = ((JSON.parse(out)[0] || {}).files || []).map((f) => f.path);
    assert(files.includes('bin/observo'),
      `tarball is missing bin/observo. Files: ${files.join(', ')}`);
  } finally {
    fs.rmSync(dest, { recursive: true, force: true });
  }
});

// 2. The shim must be syntactically valid.
check('shim passes node --check', () => {
  execFileSync(process.execPath, ['--check', SHIM]);
});

// 3. The shim must exec the native sidecar, forwarding argv and exit code.
check('shim forwards args and exit code to observo-bin', () => {
  if (process.platform === 'win32') return; // fake-binary trick is POSIX-only
  const work = fs.mkdtempSync(path.join(os.tmpdir(), 'observo-shim-'));
  try {
    fs.copyFileSync(SHIM, path.join(work, 'observo'));
    const fake = path.join(work, 'observo-bin');
    fs.writeFileSync(fake, '#!/bin/sh\necho "args=$*"\nexit 7\n');
    fs.chmodSync(fake, 0o755);
    const res = spawnSync(process.execPath,
      [path.join(work, 'observo'), 'run', '--id', 'x'], { encoding: 'utf8' });
    assert.strictEqual(res.status, 7, `expected exit 7, got ${res.status}`);
    assert.match(res.stdout, /args=run --id x/, `argv not forwarded: ${res.stdout}`);
  } finally {
    fs.rmSync(work, { recursive: true, force: true });
  }
});

// 4. The shim must fail loudly when the native binary is missing.
check('shim exits non-zero with guidance when observo-bin absent', () => {
  if (process.platform === 'win32') return;
  const work = fs.mkdtempSync(path.join(os.tmpdir(), 'observo-miss-'));
  try {
    fs.copyFileSync(SHIM, path.join(work, 'observo'));
    const res = spawnSync(process.execPath, [path.join(work, 'observo')], { encoding: 'utf8' });
    assert.strictEqual(res.status, 1, `expected exit 1, got ${res.status}`);
    assert.match(res.stderr, /native binary missing/, `missing-binary message absent: ${res.stderr}`);
  } finally {
    fs.rmSync(work, { recursive: true, force: true });
  }
});

if (failures > 0) {
  process.stderr.write(`\n${failures} check(s) failed\n`);
  process.exit(1);
}
process.stdout.write('\nall npm-wrapper checks passed\n');
