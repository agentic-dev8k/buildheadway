'use strict';

const fs = require('node:fs');
const path = require('node:path');

const requiredDocs = [
  'README.md', 'CHANGELOG.md', 'CONTRIBUTING.md', 'docs/README.md',
  'docs/install.md', 'docs/usage.md', 'docs/harness.md', 'docs/extensions.md',
  'docs/branding.md', 'docs/maintainers/naming.md',
  'docs/maintainers/publishing.md', 'docs/maintainers/validation.md',
];

function prose(text) {
  let fence;
  return text.split(/\r?\n/).map(line => {
    const match = /^\s*(`{3,}|~{3,})/.exec(line);
    if (match) {
      if (!fence) fence = match[1];
      else if (match[1][0] === fence[0] && match[1].length >= fence.length) fence = undefined;
      return '';
    }
    return fence ? '' : line;
  }).join('\n');
}

function anchors(text) {
  const seen = new Map();
  return new Set([...prose(text).matchAll(/^#{1,6}\s+(.+?)\s*#*\s*$/gm)].map(match => {
    const base = match[1].toLowerCase().replace(/[^\p{L}\p{N}_\-\s]/gu, '').replace(/ /g, '-');
    const count = seen.get(base) || 0;
    seen.set(base, count + 1);
    return count ? `${base}-${count}` : base;
  }));
}

function checkLinks(root, files) {
  root = path.resolve(root);
  let count = 0;
  for (const relative of files) {
    const file = path.join(root, relative);
    const text = prose(fs.readFileSync(file, 'utf8'));
    const targets = [...text.matchAll(/!?\[[^\]\n]*\]\(<?([^\s)>]+)>?(?:\s+"[^"]*")?\)/g)].map(m => m[1]);
    for (const match of text.matchAll(/<(?:img|a)\b[^>]*\b(?:src|href)="([^"]+)"[^>]*>/g)) targets.push(match[1]);
    for (const target of targets) {
      if (/^(?:https?:|mailto:)/i.test(target)) continue;
      const [name, fragment] = target.split('#');
      const resolved = path.resolve(path.dirname(file), decodeURIComponent(name || path.basename(file)));
      const local = path.relative(root, resolved);
      if (local.startsWith('..' + path.sep) || local === '..' || path.isAbsolute(local)) {
        throw new Error(`${relative}: link escapes documentation root: ${target}`);
      }
      if (!fs.statSync(resolved, { throwIfNoEntry: false })?.isFile()) {
        throw new Error(`${relative}: missing local link: ${target}`);
      }
      if (fragment && resolved.endsWith('.md') && !anchors(fs.readFileSync(resolved, 'utf8')).has(decodeURIComponent(fragment))) {
        throw new Error(`${relative}: missing heading: ${target}`);
      }
      count++;
    }
  }
  return count;
}

function checkDocs(root) {
  for (const file of requiredDocs) {
    if (!fs.statSync(path.join(root, file), { throwIfNoEntry: false })?.isFile()) throw new Error(`Missing documentation: ${file}`);
  }
  const files = [...requiredDocs];
  const brandIndex = 'assets/brand/README.md';
  if (fs.existsSync(path.join(root, brandIndex))) files.push(brandIndex);
  return checkLinks(root, files);
}

module.exports = { checkDocs, checkLinks, requiredDocs };
if (require.main === module) {
  try {
    const count = checkDocs(path.resolve(__dirname, '..'));
    console.log(`Documentation: required guides and ${count} local links verified`);
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
