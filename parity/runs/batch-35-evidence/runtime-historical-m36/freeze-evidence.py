#!/usr/bin/env python3
"""M36 evidence freeze. Default is read-only; --write creates a NEW batch 35.
Run from the active ponder-go root after all collectors and documentation finish.
This is not a substitute for acceptance collectors. Never rewrites old batches.
"""
import os,sys,json,hashlib,subprocess,shutil,pathlib,collections
ROOT=pathlib.Path.cwd().resolve()
RUN='parity/runs/runtime-historical-m36/full-verify-01'
BASE='parity/runs/batch-35-evidence'; BATCH='parity/runs/batch-35.json'
PARENT='33761c84666733890c7176c75fe3756ded40998f'
FINGERPRINT='30751da01517a8e7aa5f23d84a517de18da4807ac9722f72b8d6aa3426995dcf'
WRITE=sys.argv[1:]==['--write']
if sys.argv[1:] not in [[],['--check-only'],['--write']]:raise SystemExit('Usage: freeze-m36.py [--check-only|--write]')
def read(p):return json.loads((ROOT/p).read_text())
def digest(p):
 h=hashlib.sha256()
 with open(p,'rb') as f:
  for chunk in iter(lambda:f.read(1024*1024),b''):h.update(chunk)
 return h.hexdigest()
def rel(p):return os.path.relpath(p,ROOT)
def check(c,m):
 if not c:raise RuntimeError(m)
def git(*a):return subprocess.check_output(['git',*a],cwd=ROOT).decode().strip()
check((ROOT/'AGENTS.md').is_file(),'Run from project root')
check(git('rev-parse','HEAD')==PARENT,'Expected exact M35 parent; re-review if changed')
check(not (ROOT/BASE).exists() and not (ROOT/BATCH).exists(),'New batch only; never overwrite an existing freeze')
full=read(RUN+'/result.json'); summary=read('parity/runs/summary.json')
check(full.get('complete') and full.get('success') and full.get('source_manifest_verified_after'),'Successful complete full run required')
check(len(full['commands'])==28 and all(c.get('exit_code')==0 for c in full['commands']),'All 28 stages must exit zero')
check(full['native_inputs']==full['finished_native_inputs']=={'files':1084,'sha256':FINGERPRINT},'Wrong native input fingerprint')
for k,v in {'ported':508,'passed':508,'unimplemented':397,'current_original_oracle_passed':508,'historical_only_original_oracles':0}.items():check(summary['parity'].get(k)==v,'Unexpected parity count '+k)
# Re-evaluate the actual native inventory instead of trusting only the report.
node="import {sourceFingerprint} from './tools/go-source-fingerprint.mjs'; console.log(JSON.stringify(sourceFingerprint(process.cwd())))"
check(json.loads(subprocess.check_output(['node','--input-type=module','-e',node],cwd=ROOT))==full['finished_native_inputs'],'Current native inputs changed')
for e in full['harness_inputs']:check(digest(ROOT/e['file'])==e['sha256'],'Frozen harness changed: '+e['file'])
for c in full['commands']:check((ROOT/RUN/(c['name']+'.log')).is_file(),'Missing command log: '+c['name'])
# Preserve all failed/focused/current M36 artifacts explicitly, including ignored logs.
selected=set(); status=subprocess.check_output(['git','status','--porcelain=v1','-z'],cwd=ROOT).split(b'\0')
for record in status:
 if not record:continue
 check(record[:2] not in [b'R ',b' R',b'C ',b' C'],'Handle rename/copy status explicitly before freezing')
 p=record[3:].decode()
 if p.startswith('parity/runs/') and not p.startswith('parity/runs/batch-'):
  q=ROOT/p
  if q.is_dir():selected.update(rel(f) for f in q.rglob('*') if f.is_file() and not f.is_symlink())
  elif q.is_file():selected.add(p)
selected.update(rel(f) for f in (ROOT/'parity/runs/runtime-historical-m36').rglob('*') if f.is_file() and not f.is_symlink())
selected.update(p for p in ['parity/test-ledger.json','parity/feature-ledger.json','parity/mappings.json','parity/public-manifest.json','parity/runs/summary.json'] if (ROOT/p).is_file())
canon=['config-live','source-sync','source-historical','source-realtime','sql-source','source-runtime-index','source-runtime-historical']
current_roots=[RUN+'/result.json','parity/runs/summary.json']+['parity/runs/'+p+'-verification.json' for p in canon]
for p in current_roots:check((ROOT/p).is_file(),'Missing canonical proof '+p);selected.add(p)
# Historical sidecar is a bounded exception, never a fallback for arbitrary stale refs.
sidefile='parity/runs/runtime-index-m35/historical-capture-context/manifest.json'
side=read(sidefile); history_links={}
check(subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT/'../ponder-upstream').decode().strip()=='17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334','Pinned upstream changed')
check(side['commit']=='d9b2ae852a735a7e4d7c66a940d55d5f3e924400' and side['blob']=='53e5fdc3bf23b341f154fe61277ff6bafadb370e','Unexpected historical sidecar provenance')
for e in side['entries']:
 check(digest(ROOT/e['fixture']['file'])==e['fixture']['sha256'],'Historical fixture changed')
 check(digest(ROOT/e['raw_oracle']['file'])==e['raw_oracle']['sha256'],'Historical raw oracle changed')
 check(digest(ROOT/e['frozen_ref']['file'])==e['frozen_ref']['sha256'],'Historical frozen harness changed')
 history_links[(e['fixture']['file'],e['original_ref']['file'],e['original_ref']['sha256'])]=e['frozen_ref']
# Traverse current proof refs; historical report subgraphs are retained but not
# falsely required to match today's source tree. The boundary is explicit by key.
queue=collections.deque((p,False) for p in current_roots);seen=set();refs=[];archives={};historical_reports=[]
while queue:
 p,historical=queue.popleft()
 if (p,historical) in seen:continue
 seen.add((p,historical));q=ROOT/p
 if not q.is_file() or q.suffix!='.json':continue
 try:obj=json.loads(q.read_text())
 except (ValueError,UnicodeError):continue
 if isinstance(obj,dict) and obj.get('format')=='ponder-loopback-context-v1':
  for e in obj['entries'].values():archives[(e['executed_file'],e['sha256'])]=(e['persistent_file'],p)
 def visit(v,keys=()):
  if isinstance(v,dict):
   f=v.get('file');h=v.get('sha256')
   if isinstance(f,str) and isinstance(h,str) and len(h)==64:
    is_history=historical or any(k in ['history_pristine_reference','historical_reference','previous_canonical'] for k in keys)
    refs.append({'owner':p,'file':f,'sha256':h,'historical_subgraph':historical,'reference_key_path':list(keys)})
    target=ROOT/f
    if target.is_file() and target.suffix=='.json':
     targetrel=rel(target.resolve())
     if targetrel.startswith('parity/runs/batch-'):historical_reports.append(targetrel)
     elif '/node_modules/' not in str(target):queue.append((targetrel,is_history))
   for k,x in v.items():visit(x,keys+(k,))
  elif isinstance(v,list):
   for x in v:visit(x,keys)
 visit(obj)
validated=[];dependencies={};exclusions={};historical_inventory=[];errors=[]
for e in refs:
 f=e['file'];q=(ROOT/f).resolve();expected=e['sha256'];actual=digest(q) if q.is_file() else None
 if e['historical_subgraph']:
  historical_inventory.append({**e,'actual_sha256':actual,'reason':'Historical provenance subgraph, not current execution credit'})
  # Preserve the referenced report itself where it still exists; its historical
  # internal source hashes are descriptive, never substituted into current proof.
 else:
  resolved=None
  # Legacy generated fixtures use upstream-relative source paths, explicitly.
  if actual is None and (e['owner'].startswith('testdata/') or e['owner']=='config/chain_catalog.json') and (f.startswith('packages/') or f=='pnpm-lock.yaml'):
   up=(ROOT/'../ponder-upstream'/f).resolve()
   if up.is_file() and digest(up)==expected:resolved=str(up)
  if actual==expected:resolved=str(q)
  elif (e['owner'],f,expected) in history_links:
   target=history_links[(e['owner'],f,expected)];resolved=str(ROOT/target['file'])
  elif e['owner'] in ['parity/runs/sql-source-verification.json',sidefile] and f=='tools/full-oracle.config.mjs' and expected=='db4df171639a49711cfcc645e966a4643e543b24dbb69221a9b26e642a40dc3c' and any(k in ['historical_original_ref','original_source_context','original_ref'] for k in e['reference_key_path']):
   resolved=str(ROOT/side['entries'][0]['frozen_ref']['file'])
  elif (str(q),expected) in archives:
   target,archive=archives[(str(q),expected)]
   if pathlib.Path(target).is_file() and digest(target)==expected and actual is None:resolved=target
  if resolved is None:errors.append({**e,'actual_sha256':actual});continue
  validated.append({**e,'resolved_file':rel(resolved)})
 # Publishing policy is separate from validation: binary/dependency refs must
 # validate when current, even though their bytes are deliberately not published.
 target=pathlib.Path(resolved) if not e['historical_subgraph'] else q
 if not target.is_file():exclusions[f]={**e,'reason':'Historical execution-only location; not current proof'};continue
 rr=rel(target)
 if '/node_modules/' in str(target) or '/.parity-build/' in str(target) or target.is_symlink():
  exclusions[rr]={'file':f,'sha256':expected,'reason':'Validated rebuildable compiler/dependency artifact; not published'}
 elif rr.startswith('parity/runs/batch-'):dependencies[rr]={'file':rr,'sha256':digest(target),'kind':'immutable_prior_batch'}
 elif rr.startswith('parity/runs/'):selected.add(rr)
 else:dependencies[rr]={'file':rr,'sha256':digest(target),'kind':'pinned_upstream' if rr.startswith('../') else 'repository_source_or_fixture'}
check(not errors,'Current proof references failed strict validation:\n'+json.dumps(errors[:30],indent=2))
# Validate prior batch identity before/after writing; original Git paths are immutable.
prior=[p for p in git('ls-files','parity/runs/batch-34.json','parity/runs/batch-34-evidence').splitlines() if p]
prior_hashes={p:digest(ROOT/p) for p in prior}
check(not git('diff','HEAD','--name-only','--','parity/runs/batch-34.json','parity/runs/batch-34-evidence'),'Prior batch34 modified')
entries=[];total=0
for p in sorted(selected):
 q=ROOT/p
 check(q.is_file() and not q.is_symlink(),'Unexpected missing/symlink evidence '+p)
 check(not any(x in q.parts for x in ['node_modules','.parity-build','.git']),'Forbidden evidence path '+p)
 with open(q,'rb') as stream:check(stream.read(4)!=b'\x7fELF','Executable artifact in evidence '+p)
 size=q.stat().st_size;check(size<95*1024*1024,'Oversize evidence file '+p)
 suffix=p[len('parity/runs/'):] if p.startswith('parity/runs/') else p
 entries.append({'file':BASE+'/'+suffix,'sha256':digest(q),'bytes':size,'copied_from':p});total+=size
validation={'mode':'write' if WRITE else 'check-only','frozen_files':len(entries),'frozen_bytes':total,'strict_current_references':len(validated),'historical_subgraph_references':len(historical_inventory),'dependency_refs':len(dependencies),'excluded_rebuildable_refs':len(exclusions),'prior_batch34_files':len(prior),'source_fingerprint':FINGERPRINT}
print(json.dumps(validation,indent=2))
if not WRITE:raise SystemExit(0)
free=shutil.disk_usage(ROOT).free;check(free-total>=5*1024**3,'Freeze would breach5GiB free-space floor')
(ROOT/BASE).mkdir()
for e in entries:
 dest=ROOT/e['file'];dest.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(ROOT/e['copied_from'],dest);check(digest(dest)==e['sha256'],'Copied bytes differ')
manifest={'format':'m36-frozen-evidence-v1','files':entries,'dependencies':list(dependencies.values()),'not_published_artifacts':list(exclusions.values()),'strict_current_reference_validation':validated,'historical_subgraph_inventory':historical_inventory,'validation':validation,'portability':'Absolute execution roots/compiler hashes retained; static audit supported, relocated replay requires fresh pinned preparation and run contexts.'}
mp=ROOT/BASE/'manifest.json';mp.write_text(json.dumps(manifest,indent=2)+'\n')
batch=dict(summary);batch.update({'milestone':'M36','status':'verified_subset_project_incomplete','recorded_utc':full['finished_utc'],'based_on':PARENT,'new_exact_source_records':11,'new_original_assertions':24,'native_test_nodes_per_mode':2863,'runtime_historical_unmapped_cases':3,'runtime_index_type_pending_cases':4,'native_source_fingerprint':full['finished_native_inputs'],'full_verification':{'file':RUN+'/result.json','sha256':digest(ROOT/RUN/'result.json')},'frozen_evidence':entries,'evidence_manifest':{'file':rel(mp),'sha256':digest(mp)},'published':False,'scope_limits':['508/905 formal mappings;397 records remain.','Runtime historical11 newly mapped;3 source cases remain unmapped.','M35 four runtime-only Map type cases remain unmapped.','Explicit startup adaptation, never pristine; no full project parity claim.','Dependency/compiler binaries excluded; absolute-root contexts require new preparation on relocation.']})
(ROOT/BATCH).write_text(json.dumps(batch,indent=2)+'\n')
for p,h in prior_hashes.items():check(digest(ROOT/p)==h,'Prior batch34 changed during freeze')
print('Created NEW batch35 evidence and manifest. Not staged, committed or pushed.')
