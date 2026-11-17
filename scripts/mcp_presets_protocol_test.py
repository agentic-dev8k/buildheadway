#!/usr/bin/env python3
"""Opt-in real preset initialization/tool discovery; no model or tool calls."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parent.parent
REQUIRED = {
    'filesystem': 'read_text_file',
    'git': 'git_status',
    'fetch': 'fetch',
    'memory': 'read_graph',
    'sequential-thinking': 'sequentialthinking',
    'time': 'get_current_time',
    'context7': 'resolve-library-id',
}


def run(args, **kwargs):
    result = subprocess.run([str(a) for a in args], capture_output=True, text=True, timeout=180, **kwargs)
    if result.returncode:
        raise RuntimeError(result.stdout + result.stderr)
    return result.stdout


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, help='Existing native BuildHeadway binary')
    parser.add_argument('--remote', action='store_true', help='Also initialize the public Context7 endpoint')
    args = parser.parse_args()
    if not shutil.which('npx') or not shutil.which('uvx'):
        raise RuntimeError('Node.js 24+ with npx, and uvx with Python 3.10+, are required')
    with tempfile.TemporaryDirectory(prefix='BuildHeadway real MCP presets ') as temp:
        root = Path(temp)
        project = root / 'project with spaces'
        project.mkdir()
        run(['git', 'init', project])
        binary = args.binary.resolve() if args.binary else root / ('buildheadway.exe' if os.name == 'nt' else 'buildheadway')
        if not args.binary:
            run(['go', 'build', '-buildvcs=false', '-o', binary, '.'], cwd=ROOT)
        env = dict(os.environ, BUILDHEADWAY_HOME=str(root / 'home'), MEMORY_FILE_PATH=str(root / 'memory.jsonl'))
        catalog = json.loads(run([binary, 'mcp', 'catalog', '--json'], env=env))
        results = []
        for item in catalog:
            name = item['id']
            if item['transport'] == 'http' and not (args.remote and name == 'context7'):
                continue
            print(f'Checking {name}@{item["version"]}', file=sys.stderr, flush=True)
            command = [binary, 'mcp', 'add', '--project', project, '--preset', name]
            # Corporate/test proxies are explicit forwarded names, never values
            # copied into project configuration or printed in the result.
            if item['transport'] == 'stdio':
                for key in ('HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'NO_PROXY', 'UV_INDEX_URL', 'UV_EXTRA_INDEX_URL', 'NODE_EXTRA_CA_CERTS', 'NPM_CONFIG_CAFILE', 'SSL_CERT_FILE', 'SSL_CERT_DIR', 'REQUESTS_CA_BUNDLE', 'CURL_CA_BUNDLE'):
                    if env.get(key):
                        command.extend(['--env', key])
            run(command, env=env)
            report = json.loads(run([binary, 'mcp', 'check', '--project', project, '--timeout', '90', '--json', name], env=env))
            names = {t['name'] for t in report['tools']}
            if not report['connected'] or REQUIRED[name] not in names:
                raise AssertionError(f'{name} did not expose its required tool: {REQUIRED[name]}')
            if name == 'filesystem' and {'write_file', 'edit_file', 'move_file'} & names:
                raise AssertionError('Default filesystem preset exposed writing tools')
            results.append({'preset': name, 'version': item['version'], 'server': report['server'], 'protocol': report['protocol'], 'tools': len(names)})
        if len(results) != (7 if args.remote else 6):
            raise AssertionError('Not every selected provider was checked')
        if (project / '.forge' / 'runs').exists():
            raise AssertionError('Diagnostics started a coding goal')
        print(json.dumps({'connected': results, 'tool_calls': 'not performed', 'model_inference': 'not performed', 'github': 'requires an existing user token; not attempted'}, indent=2))


if __name__ == '__main__':
    main()
