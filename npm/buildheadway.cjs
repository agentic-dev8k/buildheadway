#!/usr/bin/env node
'use strict';

const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const os = require('node:os');
const { spawn } = require('node:child_process');

function binaryName(platform = process.platform, arch = process.arch) {
  const targetOS = { win32: 'windows', darwin: 'darwin', linux: 'linux' }[platform];
  const targetArch = { x64: 'amd64', arm64: 'arm64' }[arch];
  if (!targetOS || !targetArch) {
    throw new Error(`Unsupported platform ${platform}/${arch}. BuildHeadway supports Windows, macOS, and Linux on x64 and ARM64.`);
  }
  return `buildheadway-${targetOS}-${targetArch}${platform === 'win32' ? '.exe' : ''}`;
}

function verifiedBinary(root, platform = process.platform, arch = process.arch) {
  const name = binaryName(platform, arch);
  const relative = `bin/${name}`;
  const binary = path.join(root, 'bin', name);
  let manifest, data;
  try {
    manifest = fs.readFileSync(path.join(root, 'SHA256SUMS'), 'utf8');
    data = fs.readFileSync(binary);
  } catch {
    throw new Error('The BuildHeadway package is incomplete. Reinstall it with npm using the release package.');
  }
  const entries = manifest.split(/\r?\n/).map(line => line.trim().split(/\s+/));
  const matches = entries.filter(parts => parts[1] === relative);
  const actual = crypto.createHash('sha256').update(data).digest('hex');
  if (matches.length !== 1 || !/^[a-f0-9]{64}$/.test(matches[0][0]) || matches[0][0] !== actual) {
    throw new Error('BuildHeadway failed integrity verification. Reinstall it with npm using the release package.');
  }
  if (platform !== 'win32') {
    try {
      fs.accessSync(binary, fs.constants.X_OK);
    } catch {
      // Some package extractors omit executable bits. Only the verified file is changed.
      fs.chmodSync(binary, 0o755);
    }
  }
  return binary;
}

function main(args = process.argv.slice(2)) {
  let binary;
  try {
    binary = verifiedBinary(path.resolve(__dirname, '..'));
  } catch (error) {
    console.error(`BuildHeadway: ${error.message}`);
    process.exitCode = 2;
    return;
  }
  // No shell, cwd override, detached process, downloaded launcher, or install-time execution.
  const child = spawn(binary, args, {
    stdio: 'inherit',
    env: { ...process.env, BUILDHEADWAY_INSTALL_METHOD: 'npm' },
  });
  const handlers = new Map();
  for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
    const handler = () => { if (!child.killed) child.kill(signal); };
    handlers.set(signal, handler);
    process.on(signal, handler);
  }
  const cleanup = () => {
    for (const [signal, handler] of handlers) process.removeListener(signal, handler);
  };
  child.once('error', error => {
    cleanup();
    console.error(`BuildHeadway: could not start the native terminal app (${error.code || error.message}). Reinstall the release package.`);
    process.exitCode = 2;
  });
  child.once('exit', (code, signal) => {
    cleanup();
    process.exitCode = code ?? (128 + (os.constants.signals[signal] || 1));
  });
}

module.exports = { binaryName, verifiedBinary, main };
if (require.main === module) main();
