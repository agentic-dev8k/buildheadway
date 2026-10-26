#!/usr/bin/env python3
"""POSIX installer integration using a fake HTTPS transport and a real native binary."""
import argparse
import hashlib
import os
from pathlib import Path
import subprocess
import tempfile

FAKE_CURL = r'''#!/usr/bin/env python3
import json, os, shutil, sys
from pathlib import Path
args=sys.argv[1:]
url=next(a for a in args if a.startswith('https://'))
root=Path(os.environ['INSTALL_FIXTURE'])
with (root/'requests.jsonl').open('a') as f: f.write(json.dumps(url)+'\n')
assert "example/project" in url
if url.endswith('/releases/latest'):
 print('https://github.com/example/project/releases/tag/v0.11.0',end='')
else:
 assert '/releases/download/v0.11.0/' in url
 dest=Path(args[args.index('-o')+1])
 shutil.copyfile(root/url.rsplit('/',1)[-1],dest)
'''

def run(binary, installer):
    with tempfile.TemporaryDirectory(prefix='buildheadway-installer-check-') as directory:
        root=Path(directory)
        tools=root/'tools'; tools.mkdir()
        curl=tools/'curl'; curl.write_text(FAKE_CURL); curl.chmod(0o755)
        target='darwin' if os.uname().sysname=='Darwin' else 'linux'
        arch='arm64' if os.uname().machine in ('arm64','aarch64') else 'amd64'
        name=f'buildheadway-{target}-{arch}'
        data=binary.read_bytes()
        (root/name).write_bytes(data)
        manifest=f'{hashlib.sha256(data).hexdigest()}  bin/{name}\n'
        (root/'SHA256SUMS').write_text(manifest)
        home=root/'user data'
        env=dict(os.environ, BUILDHEADWAY_HOME=str(home), BUILDHEADWAY_RELEASE_REPOSITORY='example/project', INSTALL_FIXTURE=str(root), PATH=str(tools)+os.pathsep+os.environ['PATH'])
        script=installer.read_text()
        command=['sh','-s','--','--no-path','--dir',str(root/'installed command')]
        first=subprocess.run(command,input=script,text=True,env=env,cwd=root,capture_output=True)
        assert first.returncode==0, first.stdout+first.stderr
        app=root/'installed command'/'buildheadway'
        assert subprocess.run([str(app),'--version'],env=env,capture_output=True).returncode==0
        # The installed receipt remembers the selected update repository.
        import json
        receipt=json.loads((home/'data'/'buildheadway-installation.json').read_text())
        assert receipt['repository']=='example/project'
        requests=(root/'requests.jsonl').read_text()
        assert requests.count('/releases/latest')==1
        assert requests.count('/releases/download/v0.11.0/')==2
        (root/name).write_bytes(data+b'corrupt')
        second=subprocess.run(command,input=script,text=True,env=env,cwd=root,capture_output=True)
        assert second.returncode!=0 and 'integrity verification' in second.stderr
        assert app.read_bytes()==data
        # Piped installers must not pick a binary/manifest out of cwd.
        badenv=dict(env, BUILDHEADWAY_RELEASE_REPOSITORY='')
        third=subprocess.run(command,input=script,text=True,env=badenv,cwd=root,capture_output=True)
        assert third.returncode!=0 and 'no published release repository' in third.stderr
        print('Native network-installer flow passed: pinned HTTPS URLs, verified asset, paths with spaces, recorded update source, corrupt download rejection, piped-cwd isolation. Transport was simulated; native executable was real.')

if __name__=='__main__':
    parser=argparse.ArgumentParser()
    parser.add_argument('binary',type=Path)
    parser.add_argument('--installer',type=Path,default=Path('install.sh'))
    args=parser.parse_args()
    run(args.binary.resolve(),args.installer.resolve())
