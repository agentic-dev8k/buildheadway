#!/usr/bin/env python3
"""Actual POSIX first-launch UI with local agent/catalog fixtures; no inference."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import struct
import subprocess
import tempfile
import termios
import time

from terminal_conversation_test import AGENT


def run(binary, root):
    home, project, agents = root / 'user data', root / 'project', root / 'agents'
    project.mkdir()
    agents.mkdir()
    (agents / 'opencode').write_text(AGENT)
    (agents / 'codex').write_text('#!/usr/bin/env python3\nprint("incompatible fixture")\n')
    for agent in agents.iterdir():
        agent.chmod(0o755)
    env = dict(os.environ, BUILDHEADWAY_HOME=str(home), PATH=str(agents) + os.pathsep + os.environ['PATH'], TERM='xterm-256color')
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 38, 120, 0, 0))
    proc = subprocess.Popen([str(binary)], cwd=project, env=env, stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
    os.close(slave)
    transcript = bytearray()

    def wait_for(predicate, description, timeout=30):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if select.select([master], [], [], 0.05)[0]:
                try:
                    transcript.extend(os.read(master, 65536))
                except OSError:
                    pass
            if predicate():
                return
            if proc.poll() is not None:
                raise AssertionError('Exited: ' + description)
        raise AssertionError('Timed out: ' + description)

    def state():
        try:
            return json.loads((home / 'data' / 'freebies.json').read_text())
        except (FileNotFoundError, json.JSONDecodeError):
            return {}

    try:
        wait_for(lambda: b'Choose a connection or skip installation' in transcript, 'first setup inspection')
        os.write(master, b'1\r')
        wait_for(lambda: b'Review private installation' in transcript and b'@openai/codex' in transcript, 'visible package review')
        assert not (home / 'data' / 'tools').exists(), 'Selection silently installed tools'
        os.write(master, b'2\r')
        wait_for(lambda: state().get('report', {}).get('catalog_status') == 'native_catalog', 'native catalog watcher')
        policy = json.loads((home / 'data' / 'setup-policy.json').read_text())
        assert policy['decline_install'] and not policy.get('approved'), policy
        assert state()['watch'] and not state()['x']['enabled']
        assert not (home / 'config' / 'connection.json').exists()
        assert not (project / '.forge' / 'runs').exists()
        os.write(master, b'/freebies stop\r')
        wait_for(lambda: state().get('watch') is False, 'stop only discovery')
        os.write(master, b'/quit\r')
        # A terminal can block its cleanup writes when the PTY is full,
        # especially on macOS. Drain it while retaining the five-second bound.
        wait_for(lambda: proc.poll() is not None, 'clean shutdown', timeout=5)
        assert proc.returncode == 0
        print(json.dumps({'first_launch': 'inspected', 'package_review': 'visible', 'installation': 'declined', 'catalog': 'native fixture', 'x': 'disabled', 'watcher': 'stopped', 'models': 'not invoked'}))
    finally:
        if proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
        os.close(master)
        (root / 'terminal.log').write_bytes(transcript)


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('binary', type=Path)
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    if args.output:
        args.output.mkdir(parents=True, exist_ok=True)
        run(args.binary.resolve(), args.output.resolve())
    else:
        with tempfile.TemporaryDirectory(prefix='buildheadway-onboarding-') as directory:
            run(args.binary.resolve(), Path(directory))
