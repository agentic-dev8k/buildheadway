'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const { execFileSync } = require('node:child_process');
const { checkPackage } = require('../check-package.cjs');
const { binaryName } = require('../buildheadway.cjs');

const source = path.resolve(__dirname, '../..');
const stage = process.env.BUILDHEADWAY_PACKAGE_DIR || path.join(source, 'dist', 'BuildHeadway');
const metadata = JSON.parse(fs.readFileSync(path.join(source, 'npm/package.json'), 'utf8'));
const tarball = process.env.BUILDHEADWAY_PACKAGE_TGZ || path.join(path.dirname(stage), `${metadata.name}-${metadata.version}.tgz`);

function npmCLI() {
  const nodeDir = path.dirname(process.execPath);
  const candidates = [process.env.npm_execpath,
    path.join(nodeDir, 'node_modules/npm/bin/npm-cli.js'),
    path.join(nodeDir, '../lib/node_modules/npm/bin/npm-cli.js')];
  for (const dir of (process.env.PATH || '').split(path.delimiter)) {
    try { candidates.push(fs.realpathSync(path.join(dir, process.platform === 'win32' ? 'npm-cli.js' : 'npm'))); } catch {}
    candidates.push(path.join(dir, 'node_modules/npm/bin/npm-cli.js'));
  }
  const cli = candidates.find(file => file && file.endsWith('npm-cli.js') && fs.existsSync(file));
  if (!cli) throw new Error('Install Node.js with npm to run package integration tests.');
  return cli;
}

test('the real release package installs, launches, upgrades, and uninstalls with npm', {
  skip: !fs.existsSync(tarball) ? 'Build the release package first with python3 release.py.' : false,
  timeout: 120000,
}, t => {
  const pkg = checkPackage(stage);
  assert.equal(pkg.version, metadata.version);
  assert.equal(pkg.dependencies, undefined, 'the launcher must not need registry downloads');
  assert.equal(pkg.scripts.install, undefined, 'installing BuildHeadway must not execute code or fetch binaries');
  assert.equal(pkg.scripts.postinstall, undefined);
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'BuildHeadway npm install with spaces '));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const prefix = path.join(root, 'npm prefix');
  const home = path.join(root, 'BuildHeadway user data');
  const project = path.join(root, 'project with spaces');
  fs.mkdirSync(project, { recursive: true });
  fs.mkdirSync(path.join(home, 'config'), { recursive: true });
  const settings = path.join(home, 'config', 'user-note.txt');
  fs.writeFileSync(settings, 'preserve user configuration');
  const bin = process.platform === 'win32' ? prefix : path.join(prefix, 'bin');
  const env = { ...process.env, BUILDHEADWAY_HOME: home, NPM_CONFIG_CACHE: path.join(root, 'npm cache') };
  const pathKey = Object.keys(env).find(key => key.toLowerCase() === 'path') || 'PATH';
  env[pathKey] = bin + path.delimiter + (env[pathKey] || '');
  const options = { cwd: project, env, encoding: 'utf8', timeout: 45000, windowsHide: true };
  const npm = args => execFileSync(process.execPath, [npmCLI(), ...args, '--prefix', prefix, '--offline', '--no-audit', '--no-fund'], options);
  const buildheadway = args => {
    if (process.platform === 'win32') {
      // Only fixed test flags are interpolated; project paths stay in cwd/env.
      assert.ok(args.every(arg => /^[A-Za-z0-9._/-]+$/.test(arg)));
      return execFileSync('cmd.exe', ['/d', '/c', 'buildheadway ' + args.join(' ')], options);
    }
    return execFileSync('buildheadway', args, options);
  };
  npm(['install', '--global', '--ignore-scripts', tarball]);
  assert.ok(fs.existsSync(path.join(bin, process.platform === 'win32' ? 'buildheadway.cmd' : 'buildheadway')));
  assert.match(buildheadway(['--version']), new RegExp('BuildHeadway ' + pkg.version.replaceAll('.', '\\.')));
  const installed = path.join(prefix, process.platform === 'win32' ? 'node_modules' : 'lib/node_modules', pkg.name);
  assert.ok(fs.existsSync(path.join(installed, 'bin', binaryName())));
  assert.match(fs.readFileSync(path.join(installed, 'README.md'), 'utf8'), /assets\/brand\/buildheadway-logo-on-white-v1\.png/);
  assert.ok(fs.existsSync(path.join(installed, 'BRANDING.md')));
  for (const file of ['buildheadway-logo-v1.png', 'buildheadway-icon-v1.png', 'buildheadway-logo-on-white-v1.png']) {
    const relative = path.join('assets/brand', file);
    assert.deepEqual(fs.readFileSync(path.join(installed, relative)), fs.readFileSync(path.join(source, relative)), `Missing or changed installed brand asset: ${file}`);
  }
  const icon = path.join(installed, 'assets/brand/buildheadway-icon-v1.png');
  const iconBytes = fs.readFileSync(icon);
  try {
    fs.unlinkSync(icon);
    assert.throws(() => checkPackage(installed), /ENOENT/, 'incomplete branding must fail the package check');
    fs.writeFileSync(icon, Buffer.from('not a PNG'));
    assert.throws(() => checkPackage(installed), /Invalid brand PNG/, 'invalid branding must fail the package check');
  } finally {
    fs.writeFileSync(icon, iconBytes);
  }
  checkPackage(installed);
  assert.match(buildheadway(['--self-test']), /Self-test passed: initial failure, automatic repair, passing checks, and final review/);
  assert.deepEqual(JSON.parse(buildheadway(['history', '--json'])), []);
  const diagnostic = JSON.parse(buildheadway(['doctor', '--json']));
  assert.equal(diagnostic.install_method, 'npm');
  assert.equal(diagnostic.version, pkg.version);
  assert.ok(diagnostic.checks.some(c => c.name === 'project' && c.status === 'ok'));
  assert.match(buildheadway(['update']), /managed by npm/);
  const workflows = JSON.parse(buildheadway(['workflows', '--json']));
  assert.ok(workflows.some(w => w.name === 'review' && w.kind === 'builtin'));
  assert.match(buildheadway(['workflow', '--preview', 'debug', 'regression']), /Diagnose and repair: regression/);
  fs.mkdirSync(path.join(project, '.buildheadway-skills', 'contract'), { recursive: true });
  fs.writeFileSync(path.join(project, '.buildheadway-skills', 'contract', 'SKILL.md'), '---\nname: contract\ndescription: Verify contracts\n---\nInspect public contracts: $ARGUMENTS');
  assert.equal(JSON.parse(buildheadway(['skills', '--json']))[0].name, 'contract');
  assert.match(buildheadway(['workflow', '--preview', 'contract', 'api']), /Inspect public contracts: api/);
  const runID = 'package-checkpoint';
  const runDir = path.join(project, '.forge/runs', runID);
  fs.mkdirSync(runDir, { recursive: true });
  const checkpoint = {
    version: 3, run_id: runID, goal: 'Inspect a saved goal', status: 'paused', phase: 'reviewing',
    cycle: 1, planner_calls: 1, worker_calls: 1, next_task: 1,
    tasks: [{ id: 'inspect', title: 'Inspect the package', instructions: 'Verify installation' }],
    task_history: [{ key: '1:0', cycle: 1, task: { id: 'inspect', title: 'Inspect the package', instructions: 'Verify installation' }, status: 'reported', attempts: 1 }],
    events: [{ at: '2026-10-20T00:00:00Z', kind: 'task-reported', text: 'Inspection attempted' }],
  };
  fs.writeFileSync(path.join(runDir, 'state.json'), JSON.stringify(checkpoint));
  fs.writeFileSync(path.join(project, '.forge/latest.json'), JSON.stringify({ run_id: runID }));
  assert.equal(JSON.parse(buildheadway(['status', '--json'])).goal, checkpoint.goal);
  assert.deepEqual(JSON.parse(buildheadway(['routing', '--json'])).models, []);
  assert.match(buildheadway(['routing']), /No free-model routing history yet/);
  checkpoint.model_health = [{ role: 'worker', model: 'opencode/free', attempts: 3, successes: 2, failures: 1, consecutive_failures: 0, failure_kind: 'transient', last_error: '503 previously overloaded' }];
  fs.writeFileSync(path.join(runDir, 'state.json'), JSON.stringify(checkpoint));
  const routing = JSON.parse(buildheadway(['routing', '--json']));
  assert.equal(routing.models[0].attempts, 3);
  assert.equal(routing.models[0].successes, 2);
  assert.equal(routing.models[0].status, 'needs_catalog_check');
  assert.match(buildheadway(['routing']), /3 attempts · 2 usable responses · 1 failures/);
  assert.match(buildheadway(['steer', 'check-compatibility']), /Saved steer-/);
  assert.match(buildheadway(['steering']), /check-compatibility/);
  assert.equal(JSON.parse(fs.readFileSync(path.join(runDir, 'steering.json'), 'utf8')).messages.length, 1);
  assert.equal(JSON.parse(buildheadway(['tasks', '--json']))[0].attempts, 1);
  assert.equal(JSON.parse(buildheadway(['events', '--json']))[0].kind, 'task-reported');
  assert.equal(JSON.parse(buildheadway(['discussion', '--json'])), null); // Older runs have no council transcript.
  fs.writeFileSync(path.join(home, 'config', 'connection.json'), JSON.stringify({
    version: 3, planner: { backend: 'codex', model: 'account-model' }, worker: { backend: 'opencode', model: 'auto' },
    seniors: [{ name: 'critic', connection: { backend: 'claude', model: 'opus' } }],
  }));
  const team = JSON.parse(buildheadway(['team', '--json']));
  assert.equal(team[0].connection.backend, 'codex');
  assert.equal(team[1].connection.backend, 'claude');
  buildheadway(['detach', 'critic']);
  assert.equal(JSON.parse(buildheadway(['team', '--json']))[1].name, 'reviewer');
  assert.match(buildheadway(['--help']), /buildheadway figureout/);
  assert.equal(JSON.parse(buildheadway(['history', '--json']))[0].run_id, runID);
  assert.match(buildheadway(['init']), /Project ready/);
  const configPath = path.join(project, '.buildheadway.json');
  const config = JSON.parse(buildheadway(['config']));
  assert.equal(config.version, 1);
  assert.match(buildheadway(['hooks']), /No hooks configured/);
  const hookScript = "const fs=require('node:fs');const assert=require('node:assert/strict');const data=JSON.parse(fs.readFileSync(0,'utf8'));assert.equal(data.event,'after_check');assert.ok(data.checks.length);";
  fs.writeFileSync(path.join(project, 'hook.cjs'), hookScript);
  config.hooks = [{ name: 'audit', event: 'after_check', command: 'node hook.cjs' }];
  config.checks = ['echo verified'];
  fs.writeFileSync(configPath, JSON.stringify(config));
  const checked = JSON.parse(buildheadway(['check', '--json']));
  assert.equal(checked.status, 'passed');
  assert.equal(checked.hooks[0].exit_code, 0);
  assert.match(checked.results[0].log, /\.log$/);
  assert.match(checked.hooks[0].log, /\.log$/);
  const logs = JSON.parse(buildheadway(['logs', '--last-check', '--json']));
  assert.equal(logs.id, checked.id);
  assert.ok(logs.files.some(file => file.name === checked.results[0].log));
  const fullLog = JSON.parse(buildheadway(['logs', '--last-check', '--json', checked.results[0].log]));
  assert.match(fullLog.output, /verified/);
  assert.equal(fullLog.truncated, false);
  assert.throws(() => buildheadway(['logs', '--last-check', '../state.json']), error => error.status !== 0);

  assert.equal(JSON.parse(buildheadway(['status', '--json'])).status, 'paused', 'standalone checks preserve the goal');
  // A second command must not restore source and conceal an earlier edit.
  fs.writeFileSync(path.join(project, 'boundary-source.txt'), 'original');
  fs.writeFileSync(path.join(project, 'mutate.cjs'), "const fs=require('node:fs');const p='boundary-source.txt';fs.writeFileSync(p,fs.readFileSync(p,'utf8')==='original'?'changed':'original');console.log('mutated source');");
  fs.writeFileSync(path.join(project, 'restore.cjs'), "const fs=require('node:fs');fs.writeFileSync('boundary-source.txt','original');fs.writeFileSync('.forge/restored','unexpected');");
  config.checks = ['node mutate.cjs', 'node restore.cjs'];
  fs.writeFileSync(configPath, JSON.stringify(config));
  assert.throws(() => buildheadway(['check', '--json']), error => error.status !== 0);
  const invalidated = JSON.parse(fs.readFileSync(path.join(project, '.forge/last-check.json'), 'utf8'));
  assert.notEqual(invalidated.status, 'passed');
  assert.equal(invalidated.results.length, 1);
  assert.equal(invalidated.results[0].exit_code, 0);
  assert.equal(invalidated.results[0].source_changed, true);
  assert.match(invalidated.error, /after check node mutate\.cjs/);
  assert.equal(fs.existsSync(path.join(project, '.forge/restored')), false);
  assert.match(JSON.parse(buildheadway(['logs', '--last-check', '--json', invalidated.results[0].log])).output, /mutated source/);
  config.checks = ['echo verified'];
  fs.writeFileSync(configPath, JSON.stringify(config));

  const note = JSON.parse(buildheadway(['remember', '--json', 'keep-tests']));
  assert.equal(JSON.parse(buildheadway(['memory', '--json'])).entries[0].id, note.id);
  buildheadway(['buildheadwayt', note.id]);
  assert.equal(JSON.parse(buildheadway(['memory', '--json'])).entries.length, 0);
  const queued = JSON.parse(buildheadway(['queue', '--json', 'add', 'next-goal']));
  assert.equal(JSON.parse(buildheadway(['queue', '--json'])).items[0].id, queued.id);
  buildheadway(['queue', 'remove', queued.id]);
  assert.equal(JSON.parse(buildheadway(['queue', '--json'])).items[0].status, 'canceled');
  const autonomous = JSON.parse(buildheadway(['queue', '--json', 'figureout', 'autonomous-goal']));
  assert.equal(autonomous.mode, 'figureout');
  buildheadway(['queue', 'remove', autonomous.id]);
  assert.equal(JSON.parse(buildheadway(['history', '--json', '--search', 'inspect'])).length, 1);
  assert.match(buildheadway(['report']), /BuildHeadway run report/);
  const exported = JSON.parse(buildheadway(['export', '--json']));
  assert.equal(JSON.parse(fs.readFileSync(exported.path, 'utf8')).run_id, runID);
  const inventory = JSON.parse(buildheadway(['scan', '--json']));
  assert.equal(inventory.os, { win32: 'windows', linux: 'linux', darwin: 'darwin' }[process.platform]);
  for (const backend of ['codex', 'claude', 'opencode', 'pi', 'cline', 'gemini']) {
    assert.ok(inventory.agents.some(agent => agent.id === backend), `${backend} missing from native scan`);
  }
  assert.throws(() => buildheadway(['--unknown-option']), error => error.status === 2, 'native exit codes must reach the terminal');
  assert.match(buildheadway(['uninstall']), /npm uninstall -g buildheadway/);
  assert.ok(fs.existsSync(path.join(installed, 'bin', binaryName())), 'native self-uninstall must not corrupt an npm package');
  npm(['install', '--global', '--ignore-scripts', tarball]);
  assert.match(buildheadway(['--version']), /BuildHeadway/);
  assert.equal(JSON.parse(buildheadway(['status', '--json'])).run_id, runID, 'upgrade must retain project checkpoints');
  assert.equal(fs.readFileSync(settings, 'utf8'), 'preserve user configuration');
  const binary = path.join(installed, 'bin', binaryName());
  fs.appendFileSync(binary, 'tampered');
  assert.throws(() => buildheadway(['--version']), error => error.status === 2 && /integrity verification/.test(error.stderr), 'a changed native binary must never launch');
  npm(['uninstall', '--global', pkg.name]);
  assert.ok(!fs.existsSync(path.join(bin, process.platform === 'win32' ? 'buildheadway.cmd' : 'buildheadway')));
  assert.equal(fs.readFileSync(settings, 'utf8'), 'preserve user configuration');
  assert.equal(JSON.parse(fs.readFileSync(path.join(runDir, 'state.json'), 'utf8')).run_id, runID);
});
