from pathlib import Path
import subprocess,json,hashlib,datetime
root=Path('/root/ponder-go/work/ponder-go');base=Path(__file__).parent
receipt=root/'parity/runs/sync-store-m32/full-verify-02.exit'
if not receipt.exists()or receipt.read_text().strip()!='0':
 (base/'after-m32-aborted.json').write_text(json.dumps({'state':'Not started: actual full02exit0 required','source_credit':0},indent=2)+'\n');raise SystemExit(1)
def fp():
 p=subprocess.run(['node','--input-type=module','-e',"import{sourceFingerprint}from'./tools/go-source-fingerprint.mjs';console.log(JSON.stringify(sourceFingerprint(process.cwd())))"],cwd=root,text=True,capture_output=True,check=True);return json.loads(p.stdout)
def run(stem,args):
 with(base/(stem+'.jsonl'if args[0]=='go'and'test'in args else stem+'.txt')).open('w')as f:p=subprocess.run(args,cwd=root,stdout=f,stderr=subprocess.STDOUT)
 (base/(stem+'.exit')).write_text(str(p.returncode)+'\n');print({'step':stem,'exit':p.returncode},flush=True);return p.returncode
def counts(stem):
 events=[]
 for line in(base/(stem+'.jsonl')).read_text().splitlines():
  if line.startswith('{'):events.append(json.loads(line))
 results={(e['Package'],e['Test']):e['Action']for e in events if e.get('Test')and e['Action']in['pass','fail','skip']};leaves={k:v for k,v in results.items()if not any(a==k[0]and b.startswith(k[1]+'/')for a,b in results)}
 return {'leaf_cases':len(leaves),'counts':{action:sum(v==action for v in leaves.values())for action in sorted(set(leaves.values()))}}
def overlay(stem,files,packages,pattern):
 path=base/(stem+'.json');path.write_text(json.dumps({'Replace':{str(root/v):str(base/b)for v,b in files.items()}},indent=2)+'\n')
 p=subprocess.run(['go','list','-overlay',str(path),'-json',*packages],cwd=root,text=True,capture_output=True);(base/(stem+'-list.jsonl')).write_text(p.stdout);(base/(stem+'-list.stderr.txt')).write_text(p.stderr)
 listed=[];text=p.stdout;decoder=json.JSONDecoder()
 if p.returncode==0:
  while text.strip():value,end=decoder.raw_decode(text.lstrip());listed.append(value);text=text.lstrip()[end:]
  for virtual in files:
   pkg=next(p for p in listed if p['Dir']==str((root/virtual).parent));assert Path(virtual).name in pkg.get('GoFiles',[])+pkg.get('TestGoFiles',[])
 if p.returncode:return {'exit_code':p.returncode,'compile_list_failed':True}
 before=fp();args=['go','test','-overlay',str(path),'-count=1','-race','-json','-run',pattern,*packages];code=run(stem,args);after=fp();assert before==after
 return {'command':args,'exit_code':code,'virtual_files':files,'go_list_confirmed_virtual_files':len(files),'before':before,'after':after,'unchanged':True,**counts(stem)}
before=fp();typed=[]
for kind in ['in-memory','index']:
 for mode in ['pristine','stripped']:
  stem='types-'+kind+'-'+mode;code=run(stem,['node',str(base/'audit-types.mjs'),'--kind='+kind,'--mode='+mode]);typed.append({'kind':kind,'mode':mode,'exit_code':code})
  if code:break
 if typed[-1]['exit_code']:break
files={'rpc/source_block.go':'source_block.go.txt','rpc/source_block_vectors_test.go':'source_block_vectors_test.go.txt','runtime/source_historical_merge.go':'source_historical_merge.go.txt','runtime/source_historical_merge_test.go':'source_historical_merge_test.go.txt'}
source=overlay('overlay-01',files,['./rpc','./runtime'],'^(TestSourceHistoricalMergeVectors|TestSourceBlockVectors)$')
child=[]
for mode,backing in [('old','source_child_old.go.txt'),('checked','source_child.go.txt')]:
 child.append({'mode':mode,**overlay('child-overlay-'+mode+'-01',{'filter/source_child.go':backing,'filter/source_child_test.go':'source_child_test.go.txt'},['./filter'],'^TestSourceChildAddressVectors$')})
after=fp();assert before==after
inputs=[*files.values(),'merge-source.mjs','merge-vectors.json','block-source.mjs','block-vectors.json','source_child_old.go.txt','source_child.go.txt','source_child_test.go.txt','child-source.mjs','child-vectors.json','audit-types.mjs']
report={'recorded_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'state':'Actual isolated read-only audits/Go virtual overlays only. No native candidate application, fulloriginal40/typecompletion or source mappings.','source_credit':0,'before':before,'after':after,'unchanged':True,'type_audits':typed,'source_overlay':source,'child_overlays':child,'inputs':[{'file':str(base/f),'sha256':hashlib.sha256((base/f).read_bytes()).hexdigest()}for f in inputs]};(base/'after-m32-results.json').write_text(json.dumps(report,indent=2)+'\n')
success=len(typed)==4 and all(t['exit_code']==0 for t in typed)and source.get('exit_code')==0 and source.get('counts')=={'pass':56}and child[0].get('exit_code')==1 and child[0].get('leaf_cases')==25 and child[0].get('counts',{}).get('fail',0)>0 and child[1].get('exit_code')==0 and child[1].get('counts')=={'pass':25}
print(json.dumps({'unchanged':True,'typed_programs':len(typed),'source_overlay':source.get('counts'),'child_overlays':[r.get('counts')for r in child],'success':success,'source_credit':0}),flush=True);raise SystemExit(0 if success else 1)
