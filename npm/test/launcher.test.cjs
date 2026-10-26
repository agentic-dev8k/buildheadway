'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const { binaryName, verifiedBinary } = require('../buildheadway.cjs');

test('unsupported hosts and missing, modified, or ambiguously checksummed binaries cannot launch', t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'buildheadway npm integrity '));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  fs.mkdirSync(path.join(root, 'bin'));
  const relative = 'bin/' + binaryName('linux', 'x64');
  const binary = path.join(root, relative);
  const data = Buffer.from('This fixture is integrity-tested and never executed.');
  const hash = crypto.createHash('sha256').update(data).digest('hex');
  assert.throws(() => verifiedBinary(root, 'linux', 'ia32'), /Unsupported platform/);
  assert.throws(() => verifiedBinary(root, 'freebsd', 'x64'), /Unsupported platform/);
  assert.throws(() => verifiedBinary(root, 'linux', 'x64'), /incomplete/);
  fs.writeFileSync(binary, data, { mode: 0o600 });
  fs.writeFileSync(path.join(root, 'SHA256SUMS'), `${hash}  ${relative}\n`);
  assert.equal(verifiedBinary(root, 'linux', 'x64'), binary);
  fs.writeFileSync(path.join(root, 'SHA256SUMS'), `${hash}  ${relative}\n${hash}  ${relative}\n`);
  assert.throws(() => verifiedBinary(root, 'linux', 'x64'), /integrity verification/);
  fs.writeFileSync(path.join(root, 'SHA256SUMS'), `${hash}  ${relative}\n`);
  fs.appendFileSync(binary, 'changed');
  assert.throws(() => verifiedBinary(root, 'linux', 'x64'), /integrity verification/);
});
