from pathlib import Path
import json,subprocess,datetime,hashlib,os
root=Path('/root/ponder-go/work/ponder-go');base=Path(__file__).parent;out=root/'parity/runs/realtime-m34';old=json.loads((base/'after-m33-attempt-01-results.json').read_text());assert old['steps'][0]['exit_code']==-15 and old['steps'][1]['exit_code']==0
assert os.environ['GOCACHE']==str(root/'.toolcache/gobuild')and os.environ['GOMODCACHE']==str(root/'.toolcache/gomod')
for e in old['steps'][0]['inputs']:assert hashlib.sha256(Path(e['file']).read_bytes()).hexdigest()==e['sha256'],'Prototype source changed since interrupted01'
pristine=json.loads((root/'parity/runs/source-realtime-pristine.json').read_text());assert pristine['complete']and pristine['success']
report={'attempt':2,'state':'Use explicit existing task compiler/module caches after interrupting attempt01. Actual successful pristine source30 on each backend reused unchanged; no source/native candidate mutation or mappings.','complete':False,'success':False,'cache_paths':{k:os.environ[k]for k in ['GOCACHE','GOMODCACHE']},'steps':[]};path=base/'after-m33-results.json';save=lambda:path.write_text(json.dumps(report,indent=2)+'\n');save()
overlay=base/'prototype-overlay-01.json';command=['go','test','-count=1','-overlay',str(overlay),'-run','^$','./runtime'];log=out/'prototype-compile-02.txt';assert not log.exists()
with log.open('w')as f:p=subprocess.run(command,cwd=root,stdout=f,stderr=subprocess.STDOUT)
report['steps'].append({'step':'external_prototype_compile','command':command,'exit_code':p.returncode,'log':str(log.relative_to(root)),'inputs':old['steps'][0]['inputs']});save();print({'step':'external_prototype_compile','exit':p.returncode},flush=True)
if p.returncode==0:
 source=json.loads(overlay.read_text());env=os.environ.copy();env['PONDER_M34_TRACE_FIXTURE']=str(base/'source-trace-identity-01.json')
 for mode,expected in [('old',1),('new',0)]:
  chosen=json.loads(json.dumps(source));chosen['Replace'][str(root/'runtime/source_trace_identity_test.go')]=str(base/('source_trace_identity_old_test.go.txt'if mode=='old'else'source_trace_identity_test.go.txt'));selected=base/('trace-'+mode+'-overlay-01.json');assert not selected.exists();selected.write_text(json.dumps(chosen,indent=2)+'\n');command=['go','test','-count=1','-json','-overlay',str(selected),'-run','^TestSourceTraceIdentityVectors$','./runtime'];log=out/('trace-'+mode+'-01.jsonl');assert not log.exists()
  with log.open('w')as f:p=subprocess.run(command,cwd=root,env=env,stdout=f,stderr=subprocess.STDOUT)
  events=[]
  for line in log.read_text().splitlines():
   if line.startswith('{'):
    try:events.append(json.loads(line))
    except json.JSONDecodeError:pass
  leaves={e['Test']:e['Action']for e in events if e.get('Test','').startswith('TestSourceTraceIdentityVectors/')and e['Action']in['pass','fail','skip']};build=any(e['Action']=='build-fail'for e in events);parent=[e for e in events if e.get('Test')=='TestSourceTraceIdentityVectors'and e['Action']in['pass','fail','skip']];valid=p.returncode==expected and len(leaves)==12 and not build and len(parent)==1 and parent[0]['Action']==('fail'if mode=='old'else'pass')and (('identity differs'in log.read_text())if mode=='old'else all(v=='pass'for v in leaves.values()))
  report['steps'].append({'step':'trace_'+mode+'_source_vectors','command':command,'exit_code':p.returncode,'expected_exit':expected,'proof_valid':valid,'native_leaf_results':leaves,'build_fail':build,'log':str(log.relative_to(root))});save();print({'step':'trace_'+mode,'exit':p.returncode,'proof':valid},flush=True)
report['steps'].append({**old['steps'][1],'reused_actual_execution_unchanged_source':True});report.update({'complete':True,'success':all(e['exit_code']==e.get('expected_exit',0)and e.get('proof_valid',True)for e in report['steps']),'finished_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'state':'Actual external compile/old-new native vectors and separate original pristine prerequisite results only. Full source30 native comparison and complete module acceptance pending.'});save();print(report,flush=True);raise SystemExit(0 if report['success'] else 1)
