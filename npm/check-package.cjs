#!/usr/bin/env node
'use strict';

const fs = require('node:fs');
const path = require('node:path');
const { verifiedBinary } = require('./buildheadway.cjs');
const { checkDocs } = require('./check-docs.cjs');

function checkPackage(root) {
  const pkg = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8'));
  if (pkg.name !== 'buildheadway' || pkg.bin?.buildheadway !== 'npm/buildheadway.cjs') {
    throw new Error('The package must provide the buildheadway command.');
  }
  for (const platform of ['linux', 'darwin', 'win32']) {
    for (const arch of ['x64', 'arm64']) verifiedBinary(root, platform, arch);
  }
  for (const file of ['LICENSE', 'README.md', 'VALIDATION.md', 'THIRD_PARTY_LICENSES/Go-LICENSE']) {
    if (!fs.statSync(path.join(root, file)).isFile()) throw new Error(`Missing ${file}`);
  }
  for (const file of ['buildheadway-logo-v1.png', 'buildheadway-icon-v1.png', 'buildheadway-logo-on-white-v1.png']) {
    const image = fs.readFileSync(path.join(root, 'assets/brand', file));
    if (image.length < 24 || !image.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))) {
      throw new Error(`Invalid brand PNG: ${file}`);
    }
  }
  checkDocs(root);
  return pkg;
}

module.exports = { checkPackage };
if (require.main === module) {
  try {
    const pkg = checkPackage(path.resolve(__dirname, '..'));
    console.log(`${pkg.name}@${pkg.version}: six verified native builds and licenses ready`);
  } catch (error) {
    console.error(`BuildHeadway packaging: ${error.message}. Build with python3 release.py first.`);
    process.exitCode = 1;
  }
}
