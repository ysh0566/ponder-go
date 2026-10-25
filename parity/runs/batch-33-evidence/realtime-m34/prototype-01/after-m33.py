from pathlib import Path
import json,subprocess,time,datetime,hashlib,shutil
root=Path('/root/ponder-go/work/ponder-go');base=Path(__file__).parent;previous=base.parent/'m33/after-full-results.json';reportfile=base/'after-m33-results.json';assert not reportfile.exists()
report={'state':'Wait for actual M33 verified publication before new M34 candidate work. External prototype overlay compile and separate original pristine only; no mappings/acceptance.','complete':False,'steps':[]};save=lambda:reportfile.write_text(json.dumps(report,indent=2)+'\n');save()
while True:
 if previous.exists():
  old=json.loads(previous.read_text())
  if old.get('complete'):
   if not old.get('success'):report.update({'state':'Prior M33 guarded completion failed; no M34 candidate writes or execution.','complete':True,'success':False});save();raise SystemExit(1)
   break
 time.sleep(5)
commit=json.loads(Path('/root/ponder-go/.cloud-bootstrap/m33-commit.json').read_text());publication=json.loads(Path('/root/ponder-go/.cloud-bootstrap/m33-publication.json').read_text());assert commit['published']and publication['complete']and publication['success']and publication['observed_after']==commit['commit']
assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip()==commit['commit'];assert not subprocess.check_output(['git','status','--porcelain'],cwd=root,text=True).strip()
out=root/'parity/runs/realtime-m34';assert not out.exists();out.mkdir()
for name in ['m33-commit.json','m33-publication.json','m33-bundle-verification.json']:shutil.copyfile(Path('/root/ponder-go/.cloud-bootstrap')/name,out/name)
overlay={'Replace':{str(root/'runtime/source_realtime_state.go'):str(base/'source_realtime_state.go.txt'),str(root/'runtime/source_realtime_fetch.go'):str(base/'source_realtime_fetch.go.txt'),str(root/'rpc/source_call_traces.go'):str(base/'source_call_traces.go.txt'),str(root/'runtime/source_trace_identity_test.go'):str(base/'source_trace_identity_test.go.txt'),str(root/'runtime/source_historical_wire_test.go'):str(base/'source_historical_wire_extended_test.go.txt'),str(root/'runtime/source_realtime_live_test.go'):str(base/'source_realtime_live_test.go.txt')}};overlayfile=base/'prototype-overlay-01.json';assert not overlayfile.exists();overlayfile.write_text(json.dumps(overlay,indent=2)+'\n')
command=['go','test','-count=1','-overlay',str(overlayfile),'-run','^$','./runtime'];log=out/'prototype-compile-01.txt';
with log.open('w')as f:p=subprocess.run(command,cwd=root,stdout=f,stderr=subprocess.STDOUT)
report['steps'].append({'step':'external_prototype_compile','command':command,'exit_code':p.returncode,'log':str(log.relative_to(root)),'inputs':[{'file':str(path),'sha256':hashlib.sha256(path.read_bytes()).hexdigest()}for path in [base/'source_realtime_state.go.txt',base/'source_realtime_fetch.go.txt',base/'source_call_traces.go.txt',base/'source_trace_identity_test.go.txt',base/'source_historical_wire_extended_test.go.txt',base/'source_realtime_live_test.go.txt',overlayfile]]});save();print({'step':'external_prototype_compile','exit_code':p.returncode},flush=True)
# Actual old/new source helper controls use identical real-source fixtures.
if report['steps'][0]['exit_code']==0:
 import os
 env=os.environ.copy();env['PONDER_M34_TRACE_FIXTURE']=str(base/'source-trace-identity-01.json')
 for mode,expected in [('old',1),('new',0)]:
  selected=json.loads(json.dumps(overlay));selected['Replace'][str(root/'runtime/source_trace_identity_test.go')]=str(base/('source_trace_identity_old_test.go.txt'if mode=='old'else'source_trace_identity_test.go.txt'));selectedfile=base/('trace-'+mode+'-overlay-01.json');selectedfile.write_text(json.dumps(selected,indent=2)+'\n')
  command=['go','test','-count=1','-json','-overlay',str(selectedfile),'-run','^TestSourceTraceIdentityVectors$','./runtime'];log=out/('trace-'+mode+'-01.jsonl')
  with log.open('w')as f:result=subprocess.run(command,cwd=root,env=env,stdout=f,stderr=subprocess.STDOUT)
  events=[]
  for line in log.read_text().splitlines():
   if line.startswith('{'):
    try:events.append(json.loads(line))
    except json.JSONDecodeError:pass
  leaves={e['Test']:e['Action']for e in events if e.get('Test','').startswith('TestSourceTraceIdentityVectors/')and e['Action']in['pass','fail','skip']};build_fail=any(e['Action']=='build-fail'for e in events);parent=[e for e in events if e.get('Test')=='TestSourceTraceIdentityVectors'and e['Action']in['pass','fail','skip']];verified=result.returncode==expected and len(leaves)==12 and not build_fail and len(parent)==1 and parent[0]['Action']==('fail'if mode=='old'else'pass')and (('identity differs'in log.read_text())if mode=='old'else all(x=='pass'for x in leaves.values()))
  report['steps'].append({'step':'trace_'+mode+'_source_vectors','command':command,'exit_code':result.returncode,'expected_exit':expected,'proof_valid':verified,'native_leaf_results':leaves,'build_fail':build_fail,'log':str(log.relative_to(root))});save();print({'step':'trace_'+mode+'_source_vectors','exit_code':result.returncode,'expected':expected,'proof_valid':verified},flush=True)
# Separate pristine is useful even if the new native draft does not compile.
tool=root/'tools/source-realtime-pristine.mjs';assert not tool.exists();shutil.copyfile(base/'source-realtime-pristine.mjs.txt',tool)
command=['node','tools/source-realtime-pristine.mjs'];log=out/'pristine-original-01-driver.txt'
with log.open('w')as f:p=subprocess.run(command,cwd=root,stdout=f,stderr=subprocess.STDOUT)
report['steps'].append({'step':'separate_pristine_original','command':command,'exit_code':p.returncode,'log':str(log.relative_to(root))});report.update({'complete':True,'success':all(s['exit_code']==s.get('expected_exit',0)and s.get('proof_valid',True)for s in report['steps']),'state':'Actual separate pristine and external virtual-source compilation only. Native source30 comparison/runtime/integration/full normal/race and exact mappings remain pending.','finished_utc':datetime.datetime.now(datetime.timezone.utc).isoformat()});save();print(report,flush=True)
