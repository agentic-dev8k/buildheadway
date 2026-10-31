#!/usr/bin/env python3
"""Actual pinned upstream MCP handshake/tool discovery; never launches Chromium."""
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def run(args, **kwargs):
    result = subprocess.run([str(a) for a in args], capture_output=True, text=True, timeout=120, **kwargs)
    if result.returncode:
        raise RuntimeError(result.stdout + result.stderr)
    return result.stdout


def main():
    node = shutil.which('node')
    if not node:
        raise RuntimeError('Node.js with npm is required')
    node = Path(node).resolve()
    candidates = [node.parent/'node_modules/npm/bin/npm-cli.js', node.parent/'../lib/node_modules/npm/bin/npm-cli.js']
    npm = shutil.which('npm')
    if npm:
        candidates.extend([Path(npm).resolve(), Path(npm).parent/'node_modules/npm/bin/npm-cli.js'])
    npm_cli = next((p for p in candidates if p.name == 'npm-cli.js' and p.is_file()), None)
    if npm_cli is None:
        raise RuntimeError('Cannot locate npm-cli.js')
    provider = re.search(r'const browserProvider = "([^"]+)"', (ROOT/'browser_config.go').read_text()).group(1)
    required = set(re.findall(r'call\("(browser_[^"]+)"', (ROOT/'browser_execution.go').read_text()))
    with tempfile.TemporaryDirectory(prefix='BuildHeadway actual browser protocol ') as temp:
        root = Path(temp)
        package, project = root/'provider', root/'project'
        project.mkdir()
        run([node, npm_cli, 'install', '--ignore-scripts', '--no-audit', '--no-fund', '--registry=https://registry.npmjs.org/', '--prefix', package, '@playwright/mcp@'+provider])
        # The installed package layout must satisfy the production integrity receipt.
        expected = re.findall(r'"(node_modules/[^"\n]+)"', (ROOT/'browser_runtime.go').read_text())
        for relative in expected:
            if not (package/relative).is_file():
                raise AssertionError('Pinned upstream lacks required runtime file: '+relative)
        binary = root/('buildheadway.exe' if os.name == 'nt' else 'buildheadway')
        run(['go', 'build', '-buildvcs=false', '-o', binary, '.'], cwd=ROOT)
        env = dict(os.environ, BUILDHEADWAY_HOME=str(root/'home'))
        cli = package/'node_modules/@playwright/mcp/cli.js'
        args = ['--headless', '--isolated', '--sandbox', '--caps', 'vision', '--no-webmcp', '--output-dir', str(root/'output'), '--output-max-size', '33554432', '--viewport-size', '1280x720', '--image-responses', 'omit']
        run([binary, 'mcp', 'add', '--project', project, '--timeout', '20', 'upstream', '--', node, cli, *args], env=env)
        report = json.loads(run([binary, 'mcp', 'check', '--project', project, '--json', 'upstream'], env=env))
        names = {t['name'] for t in report['tools']}
        assert report['connected'] and required <= names, 'Missing production browser tools: '+str(required-names)
        assert not (project/'.forge/latest.json').exists(), 'Protocol diagnostics must not start a goal'
        print(json.dumps({'provider':provider, 'connected':report['connected'], 'protocol':report['protocol'], 'server':report['server'], 'tool_count':len(names), 'required_tools':sorted(required), 'browser_launch':'not attempted', 'model_inference':'not performed'}, indent=2))


if __name__ == '__main__':
    main()
