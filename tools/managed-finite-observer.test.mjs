// Exercise the actual observer parser/path/admission functions with bounded
// in-memory process fixtures. Never spawn its launch/DB command or signal a PID.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {spawnSync} from 'node:child_process';
const root=path.resolve(import.meta.dirname,'..');
test('actual Python observer census/admission reject unsafe fixtures without launch',()=>{
 const retained=fs.mkdtempSync(path.join(os.tmpdir(),'ponder-m44-observer-unit-'));
 const program=String.raw`
import importlib.util,pathlib,sys,types
sys.dont_write_bytecode=True
spec=importlib.util.spec_from_file_location('managed_observer',sys.argv[1]);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
realPath=pathlib.Path
class Stat:
 def __init__(self,text=None,error=None):self.text,self.error=text,error
 def read_text(self):
  if self.error:raise self.error
  return self.text
class Proc:
 def __init__(self,name,stat):self.name,self.stat=name,stat
 def __truediv__(self,name):assert name=='stat';return self.stat
fields=['S','7','42']+['0']*16+['123456']+['0']*4
proc=[Proc('42',Stat('42 (worker (space)) '+' '.join(fields))),Proc('43',Stat(error=FileNotFoundError())),Proc('44',Stat(error=PermissionError())),Proc('sys',None)]
m.pathlib.Path=lambda p:types.SimpleNamespace(iterdir=lambda:iter(proc)) if p=='/proc' else realPath(p)
try:
 result=m.census();assert result=={(42,'123456'):{'pid':42,'ppid':7,'pgid':42,'start_ticks':'123456','state':'S','comm':'worker (space)'}},result
 try:m.census({44})
 except RuntimeError as error:assert 'previously owned task PID' in str(error)
 else:raise AssertionError('known PID permission denial became false absence')
finally:m.pathlib.Path=realPath
m.ROOT=realPath(sys.argv[2]);scope=m.ROOT/'parity/runs/managed-finite-m44';scope.mkdir(parents=True)
assert m.scoped('parity/runs/managed-finite-m44/fresh','parity/runs/managed-finite-m44')==scope/'fresh'
(scope/'existing').mkdir()
for candidate in ['parity/runs/managed-finite-m44/existing','parity/runs/managed-finite-m44/old/nested','../escape']:
 try:m.scoped(candidate,'parity/runs/managed-finite-m44')
 except RuntimeError:pass
 else:raise AssertionError(candidate)
file=m.ROOT/'preserve.json';m.write_new(file,{'keep':True})
try:m.write_new(file,{'keep':False})
except FileExistsError:pass
else:raise AssertionError('overwrote receipt')
assert file.read_text().find('true')>=0
# Invalid budget/floor is rejected by actual main before any child launch.
calls=[]
def forbidden(*args,**kwargs):calls.append(args);raise AssertionError('must not launch')
m.subprocess.Popen=forbidden
m.shutil.disk_usage=lambda _:types.SimpleNamespace(free=10*1024**3)
for budget,free in [('5',10),('0',10),('4',7)]:
 m.shutil.disk_usage=lambda _,free=free:types.SimpleNamespace(free=free*1024**3)
 sys.argv=['observer','--output','parity/runs/managed-finite-m44/new','--baseline','parity/runs/finite-historical-m43/m44-regression-new','--budget-gib',budget]
 try:m.main()
 except RuntimeError:pass
 else:raise AssertionError('invalid budget admitted')
assert not calls
print('actual observer pure fixtures passed; retained '+str(m.ROOT))
`;
 const r=spawnSync('python3',['-c',program,root+'/tools/managed-finite-observe.py',retained],{cwd:root,encoding:'utf8',timeout:10000,maxBuffer:1024*1024});
 assert.equal(r.status,0,r.stderr||r.error?.message);assert.match(r.stdout,/actual observer pure fixtures passed/);
});
