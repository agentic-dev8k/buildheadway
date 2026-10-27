'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { checkDocs, checkLinks } = require('../check-docs.cjs');

test('source documentation has every guide and resolves local links', () => {
  assert.ok(checkDocs(path.resolve(__dirname, '../..')) > 25);
});

test('documentation validation rejects broken files, headings and escaping paths', t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'BuildHeadway docs with spaces '));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  fs.mkdirSync(path.join(root, 'docs'));
  fs.writeFileSync(path.join(root, 'docs/guide.md'), '# Start here\n\n## Start here\n');
  const check = text => {
    fs.writeFileSync(path.join(root, 'README.md'), text);
    return checkLinks(root, ['README.md']);
  };
  assert.equal(check('[Guide](docs/guide.md#start-here)\n[Repeat](docs/guide.md#start-here-1)'), 2);
  assert.throws(() => check('[Guide](docs/missing.md)'), /missing local link/);
  assert.throws(() => check('[Guide](docs/guide.md#missing)'), /missing heading/);
  assert.throws(() => check('[Outside](../outside.md)'), /escapes documentation root/);
  assert.equal(check('```md\n[Example](missing.md)\n```\n[Site](https://example.com)'), 0);
  assert.throws(() => checkDocs(root), /Missing documentation/);
});
