from pathlib import Path
import json,subprocess,time,datetime,hashlib,shutil,fcntl
root=Path('/root/ponder-go/work/ponder-go');base=Path(__file__).parent;previous=base/'after-m33-results.json';receipt=base/'apply-focus-results.json';assert not receipt.exists()
report={'state':'Wait for actual published M33 plus successful M34 virtual prototype/native old-new helper controls/pristine originals. No root native mutation before all guards.','complete':False,'success':False};save=lambda:receipt.write_text(json.dumps(report,indent=2)+'\n');save()
while True:
 if previous.exists():
  old=json.loads(previous.read_text())
  if old.get('complete'):
   if not old.get('success'):report.update({'state':'M34 prerequisites failed; no native apply or original/native focus run.','complete':True});save();raise SystemExit(1)
   break
 time.sleep(5)
with Path('/root/ponder-go/.cloud-bootstrap/heavy-tests.lock').open('a')as lock:
 fcntl.flock(lock,fcntl.LOCK_EX)
 published=json.loads(Path('/root/ponder-go/.cloud-bootstrap/m33-publication.json').read_text());assert published['success']and published['complete'];assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip()==published['sha'];assert subprocess.run(['git','diff','--quiet'],cwd=root).returncode==0
 untracked=subprocess.check_output(['git','ls-files','--others','--exclude-standard'],cwd=root,text=True).splitlines();assert all(p.startswith('parity/runs/realtime-m34/')or p in ['parity/runs/source-realtime-pristine.json','tools/source-realtime-pristine.mjs']for p in untracked),untracked
 pristine=json.loads((root/'parity/runs/source-realtime-pristine.json').read_text());assert pristine['complete']and pristine['success']and len(pristine['executions'])==2 and all(x['counts']['passed']==30 and x['counts']['failed']==x['counts']['pending']==0 for x in pristine['executions'])
 out=root/'parity/runs/realtime-m34';archive=out/'prototype-01';assert not archive.exists();archive.mkdir()
 for p in base.iterdir():
  if p.is_file()and p.name!='apply-focus-results.json':shutil.copyfile(p,archive/p.name)
 installed=[]
 mapping={'source_realtime_state.go.txt':'runtime/source_realtime_state.go','source_realtime_fetch.go.txt':'runtime/source_realtime_fetch.go','source_call_traces.go.txt':'rpc/source_call_traces.go','source_trace_identity_test.go.txt':'runtime/source_trace_identity_test.go','source_historical_wire_extended_test.go.txt':'runtime/source_historical_wire_test.go','source_realtime_live_test.go.txt':'runtime/source_realtime_live_test.go','source-realtime-original.mjs.txt':'tools/source-realtime-original.mjs','source-realtime-live.config.mjs.txt':'tools/source-realtime-live.config.mjs','source-realtime-live.setup.ts.txt':'tools/source-realtime-live.setup.ts','realtime-rpc-observer.mjs':'tools/source-realtime-rpc-observer.mjs'}
 for src,target in mapping.items():
  text=(base/src).read_text();lines=text.splitlines(keepends=True);removed=bool(lines and lines[0].lower().startswith('// external'))
  if removed:text=''.join(lines[1:])
  (root/target).write_text(text);installed.append({'file':target,'sha256':hashlib.sha256((root/target).read_bytes()).hexdigest(),'external_source':src,'external_sha256':hashlib.sha256((base/src).read_bytes()).hexdigest(),'only_preparation_header_removed':removed})
 text=(base/'source-trace-fixtures.mjs').read_text().replace("root='/root/ponder-go/work/ponder-go'","root=path.resolve(import.meta.dirname,'..')").replace("({file,sha256:crypto.createHash", "({file:path.relative(root,file),sha256:crypto.createHash").replace("path.resolve(import.meta.dirname,'source-trace-identity-01.json')","path.join(root,'testdata/rpc/source-trace-identity.json')").replace("if(fs.existsSync(destination))throw Error('Preserve previous source vector run');",'')
 (root/'tools/source-trace-fixtures.mjs').write_text(text)
 command=['node','tools/source-trace-fixtures.mjs'];p=subprocess.run(command,cwd=root,capture_output=True,text=True);(out/'source-trace-preparation-01.txt').write_text(p.stdout+p.stderr);assert p.returncode==0
 current=json.loads((root/'testdata/rpc/source-trace-identity.json').read_text());oldfixture=json.loads((base/'source-trace-identity-01.json').read_text());assert current['cases']==oldfixture['cases'],'Actual source vector values changed during portability adaptation'
 installed += [{'file':f,'sha256':hashlib.sha256((root/f).read_bytes()).hexdigest()}for f in ['tools/source-trace-fixtures.mjs','testdata/rpc/source-trace-identity.json']]
 def fingerprint():return json.loads(subprocess.check_output(['node','--input-type=module','-e',"import{sourceFingerprint}from'./tools/go-source-fingerprint.mjs';console.log(JSON.stringify(sourceFingerprint(process.cwd())))"],cwd=root,text=True))
 before=fingerprint();pre={'recorded_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'state':'Actual source30 original/native focus preparation only. M33 independently published. No M34 mapping credit.','published_parent':published['sha'],'native_before':before,'installed':installed,'prerequisites':old};(out/'preflight-focus-01.json').write_text(json.dumps(pre,indent=2)+'\n');report.update({'state':'M34 candidate applied after all guards. Actual complete original/native focused normal execution running, no mappings or full acceptance.','before':before,'command':['go','test','-count=1','-json','-run','^TestUpstreamSourceRealtime$','./runtime']});save();print(report['state'],flush=True)
 log=out/'live-normal-01.jsonl';assert not log.exists()
 with log.open('w')as f:p=subprocess.run(report['command'],cwd=root,stdout=f,stderr=subprocess.STDOUT)
 after=fingerprint();assert before==after
 events=[]
 for line in log.read_text().splitlines():
  if line.startswith('{'):
   try:events.append(json.loads(line))
   except json.JSONDecodeError:pass
 results={e['Test']:e['Action']for e in events if e.get('Test')and e['Action']in['pass','fail','skip']};leaves={name:state for name,state in results.items()if not any(n.startswith(name+'/')for n in results)}
 report.update({'state':'Actual first complete original/native realtime focus result, not M34 full acceptance. Inputs unchanged. Inspect failures before edits or source credit.','exit_code':p.returncode,'after':after,'leaves':len(leaves),'counts':{s:list(leaves.values()).count(s)for s in sorted(set(leaves.values()))},'complete':True,'success':p.returncode==0,'finished_utc':datetime.datetime.now(datetime.timezone.utc).isoformat()});save();(out/'live-normal-01.exit').write_text(str(p.returncode)+'\n');(out/'live-normal-01-results.json').write_text(json.dumps(report,indent=2)+'\n');print(report,flush=True);raise SystemExit(p.returncode)
