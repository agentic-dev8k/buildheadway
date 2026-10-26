#!/usr/bin/env python3
"""POSIX terminal integration: real BuildHeadway UI/processes, simulated model replies."""
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

AGENT = r'''#!/usr/bin/env python3
import json,os,sys,time
from pathlib import Path
args=sys.argv[1:]
if '--version' in args:
 print('fixture 1.0'); sys.exit()
if '--help' in args:
 print('--pure --format --model --file --dir --auto --output-schema --output-last-message --sandbox'); sys.exit()
if 'models' in args:
 print('opencode/free\n'+json.dumps({'cost':{'input':0,'output':0},'capabilities':{'toolcall':True}})); sys.exit()
prompt=Path(args[args.index('--file')+1]).read_text() if '--file' in args else sys.stdin.read()
repo=Path.cwd()
def decision(summary,done=False,tasks=None):
 return {'done':done,'summary':summary,'tasks':tasks or [],'checks':['npm test'],'success_criteria':['Numeric addition passes unchanged tests'],'check_replacements':[],'messages':[],'acceptance':[{'criterion':'Numeric addition passes unchanged tests','checks':['npm test']}]}
if 'INTERNAL TEAM CONSULTATION' in prompt:
 result=decision('TERMINAL_ADVICE: preserve numeric decimals; implement a + b, then run npm test.')
elif prompt.startswith('You are an implementation worker.'):
 if os.environ.get('BUILDHEADWAY_TEST_EXTENSIONS') and not (repo/'.forge'/'worker-waiting').exists():
  (repo/'.forge'/'worker-waiting').write_text('ready')
  deadline=time.monotonic()+30
  while not (repo/'.forge'/'worker-release').exists():
   if time.monotonic()>deadline: raise RuntimeError('Steering fixture timed out')
   time.sleep(0.05)
 if 'CONTINUE THE SAME ASSIGNMENT' not in prompt:
  result={'buildheadway_worker':1,'status':'consult','report':'Inspected the subtraction bug before editing','messages':[{'to':'lead','kind':'question','text':'Should decimal addition be preserved?'}]}
 else:
  assert 'TERMINAL_ADVICE' in prompt, 'Worker did not receive actual answer'
  (repo/'math.cjs').write_text('exports.add = (a,b) => a + b;\n')
  if os.environ.get('BUILDHEADWAY_TEST_EXTENSIONS'):
   assert 'PRESERVE_DECIMALS_AND_DOCUMENT' in prompt
   (repo/'README.md').write_text('add(1.5, 2.5) returns 4.\n')
  result={'buildheadway_worker':1,'status':'complete','report':'Applied the routed senior answer','messages':[]}
else:
 done='a + b' in (repo/'math.cjs').read_text()
 if os.environ.get('BUILDHEADWAY_TEST_EXTENSIONS'): done=done and (repo/'README.md').exists()
 result=decision('Reviewed actual arithmetic implementation' if done else 'Consult lead, then fix arithmetic',done,[] if done else [{'id':'fix','title':'Repair addition','instructions':'Inspect files, consult lead if needed, implement addition and verify npm test'}])
print(json.dumps(result))
'''

def run(binary, root, extensions=False):
    project, home, agents = root/'project with spaces', root/'user data', root/'agents'
    for directory in (project, home/'config', agents):
        directory.mkdir(parents=True)
    for name in ('codex', 'opencode'):
        (agents/name).write_text(AGENT)
        (agents/name).chmod(0o755)
    settings={'version':3,'planner':{'backend':'codex','executable':str(agents/'codex')},'worker':{'backend':'opencode','model':'auto','executable':str(agents/'opencode')}}
    (home/'config'/'connection.json').write_text(json.dumps(settings))
    (project/'package.json').write_text('{"private":true,"scripts":{"test":"node --test math.test.cjs"}}')
    (project/'math.cjs').write_text('exports.add = (a,b) => a - b;\n')
    tests="const {test}=require('node:test'); const assert=require('node:assert/strict'); const {add}=require('./math.cjs'); test('addition',()=>{assert.equal(add(4,3),7);assert.equal(add(1.5,2.5),4)});\n"
    (project/'math.test.cjs').write_text(tests)
    env=dict(os.environ, BUILDHEADWAY_HOME=str(home), PATH=str(agents)+os.pathsep+os.environ['PATH'], TERM='xterm-256color')
    if extensions:
        env['BUILDHEADWAY_TEST_EXTENSIONS']='1'
        (project/'.buildheadway-prompts').mkdir()
        (project/'.buildheadway-prompts'/'repair.md').write_text('---\ndescription: Repair arithmetic\n---\nRepair the arithmetic implementation: $ARGUMENTS')
        (project/'hook.cjs').write_text("const fs=require('node:fs');const assert=require('node:assert/strict');const input=JSON.parse(fs.readFileSync(0,'utf8'));assert.equal(input.version,1);assert.equal(input.event,process.env.BUILDHEADWAY_HOOK_EVENT);assert.ok(input.run_id);assert.ok(Array.isArray(input.checks));if(input.event==='after_check')assert.ok(input.checks.some(c=>c.command==='npm test'));fs.appendFileSync('.forge/hook-events.jsonl',JSON.stringify(input)+'\\n');")
        config={'version':1,'hooks':[{'name':'trace-before','event':'before_check','command':'node hook.cjs'},{'name':'trace-after','event':'after_check','command':'node hook.cjs'}]}
        (project/'.buildheadway.json').write_text(json.dumps(config))
    baseline=subprocess.run(['node','--test','math.test.cjs'],cwd=project,env=env,capture_output=True)
    assert baseline.returncode != 0, 'Fixture must start failing'
    master,slave=pty.openpty()
    fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',48,140,0,0))
    proc=subprocess.Popen([str(binary)],cwd=project,env=env,stdin=slave,stdout=slave,stderr=slave,start_new_session=True)
    os.close(slave)
    transcript=bytearray()
    def pump():
        if select.select([master],[],[],0.05)[0]:
            try: transcript.extend(os.read(master,65536))
            except OSError: pass
    def wait_for(predicate,why,timeout=40):
        deadline=time.monotonic()+timeout
        while time.monotonic()<deadline:
            pump()
            if predicate(): return
            if proc.poll() is not None: raise AssertionError('BuildHeadway exited: '+why)
        raise AssertionError('Timed out: '+why)
    def send(value):
        os.write(master,value.encode()+b'\r')
    def state():
        try:
            latest=json.loads((project/'.forge'/'latest.json').read_text())
            return json.loads((project/'.forge'/'runs'/latest['run_id']/'state.json').read_text())
        except (FileNotFoundError,json.JSONDecodeError): return {}
    try:
        wait_for(lambda:b'/discussion' in transcript,'terminal welcome')
        # Scanning completes before the editor accepts ordinary goals.
        wait_for(lambda:b'Connected' in transcript,'ready editor')
        if extensions:
            send('/workflows')
            wait_for(lambda:b'Repair arithmetic' in transcript,'custom workflow discovery')
            send('/repair "Fix addition"')
            wait_for(lambda:(project/'.forge'/'worker-waiting').exists(),'active worker boundary')
            send('/steer PRESERVE_DECIMALS_AND_DOCUMENT: preserve decimals and write a README usage example')
            wait_for(lambda:bool(list((project/'.forge'/'runs').glob('*/steering.json'))),'durable steering inbox')
            (project/'.forge'/'worker-release').write_text('continue')
        else:
            send('Fix addition and preserve the existing tests')
        wait_for(lambda:state().get('status') in ('complete','blocked','paused'),'plain goal completion')
        saved=state()
        assert saved['status']=='complete',saved.get('error')
        assert saved['mode']=='figureout' and saved['access']=='full'
        if extensions:
            assert saved['worker_calls']==3 and len(saved['steering'])==1
            assert len(saved['hook_results'])==2 and all(h['exit_code']==0 for h in saved['hook_results'])
            assert 'Workflow: repair' in saved['goal']
            assert (project/'README.md').exists()
            hook_events=[json.loads(line) for line in (project/'.forge'/'hook-events.jsonl').read_text().splitlines()]
            assert len(hook_events)>=4
        else:
            assert saved['worker_calls']==2 and saved['planner_calls']==7
        questions=[m for m in saved['discussion'] if m['from']=='@worker' and m['kind']=='question']
        assert len(questions)==1
        assert any(m['kind']=='answer' and m['to']=='@worker' and m.get('reply_to')==questions[0]['sequence'] for m in saved['discussion'])
        assert saved['council']['consensus'] and all(c['exit_code']==0 for c in saved['check_results'])
        assert (project/'math.test.cjs').read_text()==tests
        # Wait for UI to consume the runner's final event.
        wait_for(lambda:b'Done' in transcript,'completion display')
        send('/acceptance')
        wait_for(lambda:b'[passed] Numeric addition passes unchanged tests' in transcript,'acceptance coverage display')
        send('/routing')
        wait_for(lambda:b'Free-model routing' in transcript and b'usable responses' in transcript,'routing health display')
        send('/logs')
        wait_for(lambda:b'Read one with buildheadway logs' in transcript,'saved logs display')
        send('/discussion')
        wait_for(lambda:b'TERMINAL_ADVICE' in transcript,'conversation display')
        send('/export')
        wait_for(lambda:bool(list((project/'.forge'/'exports').glob('*.md'))),'report export')
        send('/queue add Build the next feature')
        wait_for(lambda:(project/'.forge'/'queue.json').exists(),'queued goal persistence')
        send('/quit')
        wait_for(lambda:proc.poll() is not None,'clean shutdown')
        assert proc.returncode==0
        queue=json.loads(subprocess.check_output([str(binary),'queue','--json'],cwd=project,env=env))
        assert len(queue['items'])==1 and queue['items'][0]['mode']=='figureout'
        assert queue['items'][0]['status']=='pending'
        report=list((project/'.forge'/'exports').glob('*.md'))[0].read_text()
        assert 'TERMINAL_ADVICE' in report
        assert '[passed] Numeric addition passes unchanged tests' in report
        assert 'Free-model routing' in report and 'usable responses' in report
        routing=json.loads(subprocess.check_output([str(binary),'routing','--json'],cwd=project,env=env))
        worker_health=[h for h in routing['models'] if h['role']=='worker']
        assert sum(h['attempts'] for h in worker_health)==saved['worker_calls']
        assert sum(h['successes'] for h in worker_health)>=2
        assert all(h['retry_after_seconds']==0 for h in worker_health)
        coverage=json.loads(subprocess.check_output([str(binary),'acceptance','--json'],cwd=project,env=env))
        assert len(coverage)==1 and coverage[0]['status']=='passed'
        assert coverage[0]['evidence'][0]['command']=='npm test' and coverage[0]['evidence'][0]['exit_code']==0
        (root/'result.json').write_text(json.dumps({'status':'passed','model_replies':'simulated subprocesses','verification':'actual Node.js tests','senior_calls':saved['planner_calls'],'worker_calls':saved['worker_calls'],'plain_goal_mode':saved['mode'],'queue_mode':queue['items'][0]['mode'],'extensions_tested':extensions,'steering_applied':len(saved.get('steering',[])),'hook_results':len(saved.get('hook_results',[]))},indent=2)+'\n')
        print('PASS: terminal workflow' if extensions else 'PASS: plain terminal goal', 'with routed question/answer, continuation, actual checks, review, model health, discussion, export, queue and clean exit')
    finally:
        if proc.poll() is None:
            proc.terminate()
            try: proc.wait(timeout=5)
            except subprocess.TimeoutExpired: proc.kill(); proc.wait()
        os.close(master)
        (root/'terminal.log').write_bytes(transcript)

if __name__=='__main__':
    parser=argparse.ArgumentParser()
    parser.add_argument('binary',type=Path)
    parser.add_argument('--output',type=Path)
    parser.add_argument('--extensions',action='store_true')
    args=parser.parse_args()
    if os.name=='nt': raise SystemExit('Use the native Go and npm tests on Windows; this terminal driver requires POSIX PTYs.')
    if args.output:
        args.output.mkdir(parents=True,exist_ok=True)
        run(args.binary.resolve(),args.output.resolve(),args.extensions)
    else:
        with tempfile.TemporaryDirectory(prefix='buildheadway-terminal-') as directory:
            run(args.binary.resolve(),Path(directory),args.extensions)

