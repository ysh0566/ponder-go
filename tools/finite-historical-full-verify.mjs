import {finiteHistoricalHarness} from './finite-historical-acceptance.mjs';
import {atomicMultichainHarness} from './multichain-atomic-acceptance.mjs';
import {indexingProfileHarness} from './indexing-profile-acceptance.mjs';
import {independentStorageHarness} from './independent-storage-acceptance.mjs';
import {indexingActionsHarness} from './indexing-actions-acceptance.mjs';
// Complete module verification under the explicitly recorded startup adapter.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {spawnSync} from 'node:child_process';
import {createAdaptedUpstream} from './loopback-adapter.mjs';
import {persistAdaptedContext} from './loopback-context.mjs';
import {sourceFingerprint} from './go-source-fingerprint.mjs';
import {assertPinnedTypechecker} from './pinned-typecheck-preflight.mjs';
const root=path.resolve(import.meta.dirname,'..');
const arg=process.argv.find(x=>x.startsWith('--output='));
if(!arg)throw Error('Explicit new full verification output required');
const output=path.resolve(root,arg.slice(9));
if(!output.startsWith(root+'/parity/runs/finite-historical-m43/')||fs.existsSync(output))throw Error('New scoped full verification directory required');
fs.mkdirSync(output,{recursive:true});
const report={complete:false,success:false,environment_adapted:true,pristine_file:false,source_credit:0,scope:'All module Go tests, fuzz seeds and examples in disjoint serial batches, normal and race; actual source oracles, SDK, compiler controls, vet and CLI builds.',started_utc:new Date().toISOString(),commands:[]};
const save=()=>fs.writeFileSync(output+'/result.json',JSON.stringify(report,null,2)+'\n');save();
let copy;
function physicalSHA256(file){
 const fd=fs.openSync(file,'r'),buffer=Buffer.allocUnsafe(1024*1024),hash=crypto.createHash('sha256');
 try{for(let n;(n=fs.readSync(fd,buffer,0,buffer.length,null))>0;)hash.update(buffer.subarray(0,n));}finally{fs.closeSync(fd)}
 return hash.digest('hex');
}
// Synchronous physical recovery boundary, not an additional verification stage.
// The explicit list prevents recursive inclusion of this run's outputs/archive.
function freezePreparedInputs(preparedReference,harness){
 const program=String.raw`
import datetime,gzip,hashlib,io,json,os,pathlib,re,stat,subprocess,sys,tarfile
if not __debug__: raise RuntimeError('Physical freeze requires enabled assertion checks')
root=pathlib.Path(sys.argv[1]).resolve(); out=pathlib.Path(sys.argv[2]).resolve()
upstream=(root/'../ponder-upstream').resolve(); cfg=json.load(sys.stdin)
archive=out/'prepared-inputs.tar.gz'; manifest_path=out/'prepared-inputs-manifest.json'
assert not archive.exists() and not manifest_path.exists(), 'Never overwrite prepared recovery evidence'
started=datetime.datetime.now(datetime.timezone.utc).isoformat()
def capacity():
 s=os.statvfs(root)
 assert s.f_bavail*s.f_frsize>=5*1024**3, 'Hard 5 GiB disk floor reached'
def digest(file):
 h=hashlib.sha256()
 with open(file,'rb') as f:
  while block:=f.read(1024*1024): h.update(block)
 return h.hexdigest()
entries={}
def add(ref,origin):
 assert isinstance(ref,dict) and isinstance(ref.get('file'),str) and re.fullmatch('[a-f0-9]{64}',ref.get('sha256','')), ('Invalid real input reference',origin)
 raw=ref['file']; assert '\\' not in raw and '\x00' not in raw
 p=pathlib.Path(os.path.abspath(root/raw)); assert p.resolve()==p, ('Symlink/alias input rejected',raw)
 if p.is_relative_to(root): arc='repo/'+p.relative_to(root).as_posix()
 elif p.is_relative_to(upstream): arc='upstream/'+p.relative_to(upstream).as_posix()
 else: raise AssertionError(('Input outside fixed repo/oracle',raw))
 assert p not in [archive,manifest_path], 'Archive must not include itself or its own manifest'
 mode=p.lstat().st_mode; assert stat.S_ISREG(mode), ('Input must be a regular file',raw)
 assert digest(p)==ref['sha256'], ('Prepared input hash differs',raw)
 if arc in entries:
  assert entries[arc]['sha256']==ref['sha256'], ('Conflicting references',raw)
  entries[arc]['origins'].append(origin); return
 entries[arc]={'file':os.path.relpath(p,root),'source_path':str(p),'archive_path':arc,'sha256':ref['sha256'],'bytes':p.stat().st_size,'mode':stat.S_IMODE(mode),'origins':[origin]}
def current_ref(file):
 p=root/file; return {'file':file,'sha256':digest(p)}
capacity(); add(cfg['prepared_native_inputs'],'prepared-native-manifest')
prepared=json.loads((root/cfg['prepared_native_inputs']['file']).read_text())
assert prepared['fingerprint']==cfg['native_inputs'], 'Prepared/native fingerprints disagree'
pairs=[[e['file'],e['sha256']] for e in prepared['files']]
assert len(pairs)==prepared['fingerprint']['files'] and hashlib.sha256(json.dumps(pairs,separators=(',',':'),ensure_ascii=False).encode()).hexdigest()==prepared['fingerprint']['sha256'], 'Prepared inventory fingerprint differs'
for ref in prepared['files']: add(ref,'prepared-native-file')
# Enumerate the actual unfiltered root-module inputs, not an allowlisted guess.
# This is discovery only; it executes no Go tests, programs, or generated binaries.
expected_packages=sorted('ponder.local/ponder-go/'+name for name in ['abi','bloom','checkpoint','cmd/abi-codegen','cmd/parity-probe','cmd/ponder','cmd/source-codegen','config','events','examples/erc20','factory','filter','finality','historical','indexing','internal/jsvalue','interval','orm','query','rpc','runtime','schema','sqlquery','store'])
command=['go','list','-json','./...']
go_result=subprocess.run(command,cwd=root,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=300)
go_raw=out/'prepared-go-discovery.json'
with open(go_raw,'xb') as f: f.write(go_result.stdout); f.flush(); os.fsync(f.fileno())
assert go_result.returncode==0, ('Actual unfiltered Go package discovery failed',go_result.returncode,go_result.stderr.decode(errors='replace'))
text=go_result.stdout.decode(); decoder=json.JSONDecoder(); position=0; packages=[]
while position<len(text):
 while position<len(text) and text[position].isspace(): position+=1
 if position==len(text): break
 package,position=decoder.raw_decode(text,position); packages.append(package)
actual_packages=sorted(p['ImportPath'] for p in packages)
assert actual_packages==expected_packages and len(set(actual_packages))==24, ('Actual Go package inventory must be the exact 24 root-module packages',actual_packages)
prepared_sources={ref['file']:ref for ref in prepared['files']}
assert len(prepared_sources)==len(prepared['files']), 'Duplicate prepared source references'
sources={}
for package in packages:
 directory=pathlib.Path(package['Dir']); module=package['Module']
 assert directory.is_absolute() and directory.resolve()==directory and directory.is_relative_to(root), ('Package directory outside canonical module',package['ImportPath'])
 assert module['Main'] is True and pathlib.Path(module['Dir']).resolve()==root and module['Path']=='ponder.local/ponder-go', ('Unexpected external module in root package census',package['ImportPath'])
 for kind in ['GoFiles','CgoFiles','TestGoFiles','XTestGoFiles']:
  for name in package.get(kind,[]):
   assert isinstance(name,str) and not pathlib.Path(name).is_absolute(), ('Unexpected external/generated Go input path',name)
   p=pathlib.Path(os.path.abspath(directory/name))
   assert p.resolve()==p and p.is_relative_to(root) and stat.S_ISREG(p.lstat().st_mode), ('Actual Go source must be a canonical root-module file',name)
   file=p.relative_to(root).as_posix(); ref=prepared_sources.get(file)
   assert ref is not None, ('Discovered Go input absent from prepared inventory',file)
   actual=digest(p); assert actual==ref['sha256'], ('Discovered Go input differs from prepared bytes',file)
   add(ref,'actual-go-discovery:'+package['ImportPath']+':'+kind)
   entry=sources.setdefault(file,{'file':file,'sha256':actual,'bytes':p.stat().st_size,'kinds':[],'packages':[]})
   if kind not in entry['kinds']: entry['kinds'].append(kind)
   if package['ImportPath'] not in entry['packages']: entry['packages'].append(package['ImportPath'])
for entry in sources.values(): entry['kinds'].sort(); entry['packages'].sort()
assert sources, 'Actual Go source inventory must not be empty'
go_raw_ref=current_ref(os.path.relpath(go_raw,root)); add(go_raw_ref,'actual-go-discovery-raw-json-stream')
go_discovery={'command':command,'exit_code':go_result.returncode,'stderr':go_result.stderr.decode(errors='replace'),'raw':go_raw_ref,'packages':actual_packages,'package_count':24,'sources':[sources[file] for file in sorted(sources)],'source_file_count':len(sources),'prepared_subset_verified':True,'scope':'All GoFiles/CgoFiles/TestGoFiles/XTestGoFiles of the exact 24 root-module packages from unfiltered go list ./.... Persisted repository-generated source files are hashed normally. This does not claim to enumerate standard-library/module-cache dependencies or transient compiler/testmain files. Independently generated type fixtures and 21 cached runtime executables retain their separate actual type-report references.'}
assert len(cfg['harness_inputs'])==139 and len({x['file'] for x in cfg['harness_inputs']})==139, 'Exact current harness inventory required'
for ref in cfg['harness_inputs']: add(ref,'verification-harness')
type_reports=[]; binaries=[]
def walk(value,origin):
 if isinstance(value,dict):
  if 'file' in value and 'sha256' in value: add(value,origin)
  for key,item in value.items(): walk(item,origin+'.'+key)
 elif isinstance(value,list):
  for i,item in enumerate(value): walk(item,origin+'['+str(i)+']')
for name,count in [('parity/runs/sync-store-m32/type-compile.json',9),('testdata/runtime/source-historical-types.json',12)]:
 ref=current_ref(name); add(ref,'actual-type-preparation-report'); report=json.loads((root/name).read_text())
 assert len(report['directives'])==count, ('Wrong actual prepared directive inventory',name)
 if count==9: assert report['summary']['compiled']==9 and report['summary']['failed']==0
 else: assert report['complete'] is True and report['success'] is True
 walk(report,'type-report:'+name)
 for i,d in enumerate(report['directives']):
  binary=d['positive_binary']; binary_ref={'file':binary,'sha256':d['positive_binary_sha256']} if isinstance(binary,str) else binary
  add(binary_ref,'actual-runtime-type-executable:'+name+'['+str(i)+']')
  p=pathlib.Path(os.path.abspath(root/binary_ref['file']))
  assert p.is_relative_to(root/'.parity-build') and p.stat().st_mode&0o111, ('Expected executable current preparation output',p)
  binaries.append({'file':os.path.relpath(p,root),'sha256':binary_ref['sha256']})
 type_reports.append(ref)
assert len(binaries)==21 and len({b['file'] for b in binaries})==21, 'Every actual cached runtime type executable must be frozen'
ordered=sorted(entries.values(),key=lambda e:e['archive_path'])
manifest={'format':'ponder-m43-prepared-physical-freeze-v1','recorded_utc':started,'phase':'after-preparation-before-native-controls','root':str(root),'upstream_root':str(upstream),'prepared_native_inputs':cfg['prepared_native_inputs'],'native_inputs':cfg['native_inputs'],'harness_inputs':cfg['harness_inputs'],'type_reports':type_reports,'runtime_type_executables':binaries,'go_discovery':go_discovery,'input_file_count':len(ordered),'input_raw_bytes':sum(e['bytes'] for e in ordered),'entries':ordered}
manifest_bytes=(json.dumps(manifest,indent=2)+'\n').encode()
with open(manifest_path,'xb') as f: f.write(manifest_bytes); f.flush(); os.fsync(f.fileno())
class HashedReader:
 def __init__(self,stream): self.stream=stream; self.hash=hashlib.sha256(); self.bytes=0
 def read(self,n=-1):
  capacity(); data=self.stream.read(n); self.hash.update(data); self.bytes+=len(data); return data
with open(archive,'xb') as raw:
 with gzip.GzipFile(filename='',mode='wb',fileobj=raw,mtime=0) as compressed:
  with tarfile.open(fileobj=compressed,mode='w|') as tar:
   info=tarfile.TarInfo('prepared-inputs-manifest.json'); info.size=len(manifest_bytes); info.mode=0o644; tar.addfile(info,io.BytesIO(manifest_bytes))
   for entry in ordered:
    capacity(); info=tarfile.TarInfo(entry['archive_path']); info.size=entry['bytes']; info.mode=entry['mode']
    with open(entry['source_path'],'rb') as source:
     reader=HashedReader(source); tar.addfile(info,reader)
     assert reader.bytes==entry['bytes'] and reader.hash.hexdigest()==entry['sha256'], ('Input changed while physically copied',entry['file'])
 raw.flush(); os.fsync(raw.fileno())
expected={e['archive_path']:e for e in ordered}; expected['prepared-inputs-manifest.json']={'bytes':len(manifest_bytes),'sha256':hashlib.sha256(manifest_bytes).hexdigest(),'mode':0o644}
seen=set()
with tarfile.open(archive,'r|gz') as tar:
 for member in tar:
  capacity(); assert member.name in expected and member.name not in seen and member.isfile(), ('Unexpected/duplicate archive member',member.name)
  want=expected[member.name]; assert member.size==want['bytes'] and member.mode==want['mode'], ('Archive metadata differs',member.name)
  h=hashlib.sha256(); stream=tar.extractfile(member)
  while block:=stream.read(1024*1024): h.update(block)
  assert h.hexdigest()==want['sha256'], ('Archived bytes differ',member.name)
  seen.add(member.name)
assert seen==set(expected), 'Archive missing prepared inputs'
for entry in ordered:
 p=pathlib.Path(entry['source_path']); assert p.resolve()==p and stat.S_ISREG(p.lstat().st_mode) and p.stat().st_size==entry['bytes'] and stat.S_IMODE(p.stat().st_mode)==entry['mode'] and digest(p)==entry['sha256'], ('Prepared inputs changed during archive verification',entry['file'])
capacity()
receipt={'complete':True,'success':True,'phase':'after-preparation-before-native-controls','started_utc':started,'finished_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'archive':{'file':os.path.relpath(archive,root),'sha256':digest(archive),'bytes':archive.stat().st_size},'manifest':{'file':os.path.relpath(manifest_path,root),'sha256':digest(manifest_path)},'prepared_native_inputs':cfg['prepared_native_inputs'],'harness_file_count':139,'runtime_type_executable_count':21,'go_discovery':go_discovery,'input_file_count':len(ordered),'input_raw_bytes':manifest['input_raw_bytes'],'archive_member_count':len(seen),'archive_uncompressed_file_bytes':manifest['input_raw_bytes']+len(manifest_bytes),'streaming_no_raw_staging_copy':True,'copied_sha256_verified':True,'archive_sha256_verified_per_member':True,'live_inputs_verified_after_copy':True}
print(json.dumps(receipt))
`;
 const result=spawnSync('python3',['-c',program,root,output],{cwd:root,encoding:'utf8',input:JSON.stringify({prepared_native_inputs:preparedReference,native_inputs:report.native_inputs,harness_inputs:harness}),maxBuffer:2*1024*1024});
 fs.writeFileSync(output+'/prepared-inputs-freeze.log',JSON.stringify({exit_code:result.status,signal:result.signal,error:result.error?.message??null})+'\n'+result.stdout+'\n'+result.stderr,{flag:'wx'});
 if(result.error||result.status!==0)throw Error('Synchronous prepared physical freeze failed; preserve partial evidence: '+result.stderr);
 const receipt=JSON.parse(result.stdout);
 if(!receipt.complete||!receipt.success||receipt.archive.file!==path.relative(root,output+'/prepared-inputs.tar.gz')||receipt.manifest.file!==path.relative(root,output+'/prepared-inputs-manifest.json'))throw Error('Prepared physical freeze returned an invalid receipt');
 report.prepared_input_archive=receipt;save();
}
function run(name,command,args,options={}){
 const capacity=spawnSync('python3',['/workspace/scratch/fac5abfa4c9e/ponder-restored-20261026/check-capacity.py'],{cwd:root,encoding:'utf8'});
 fs.appendFileSync(output+'/capacity.jsonl',JSON.stringify({name,exit_code:capacity.status,report:capacity.stdout,error:capacity.stderr})+'\n');
 if(capacity.status!==0)throw Error('Capacity hard gate failed before '+name);
 const record={name,command:[command,...args],started_utc:new Date().toISOString(),...(options.env?.PONDER_FINITE_HISTORICAL_SOURCE_ROOT?{finite_source_root:options.env.PONDER_FINITE_HISTORICAL_SOURCE_ROOT}:{}),...(options.env?.PONDER_MULTICHAIN_ATOMIC_SOURCE_ROOT?{atomic_source_root:options.env.PONDER_MULTICHAIN_ATOMIC_SOURCE_ROOT}:{}),...(options.env?.PONDER_INDEXING_PROFILE_NATIVE_OUTPUT?{native_indexing_profile_output:options.env.PONDER_INDEXING_PROFILE_NATIVE_OUTPUT}:{}),...(options.env?.PONDER_INDEXING_PROFILE_PROBES_NATIVE_OUTPUT?{native_indexing_profile_probes_output:options.env.PONDER_INDEXING_PROFILE_PROBES_NATIVE_OUTPUT}:{}),...(options.env?.PONDER_INDEXING_ACTIONS_PROBES_NATIVE_OUTPUT?{native_indexing_actions_probes_output:options.env.PONDER_INDEXING_ACTIONS_PROBES_NATIVE_OUTPUT}:{}),...(options.env?.PONDER_INDEXING_ACTIONS_NATIVE_OUTPUT?{native_indexing_actions_output:options.env.PONDER_INDEXING_ACTIONS_NATIVE_OUTPUT}:{}),...(options.env?.PONDER_INDEXING_SERVICE_NATIVE_OUTPUT?{native_indexing_output:options.env.PONDER_INDEXING_SERVICE_NATIVE_OUTPUT}:{})};report.commands.push(record);save();
 const fd=fs.openSync(output+'/'+name+'.log','w');let result;
 try{result=spawnSync(command,args,{cwd:options.cwd??root,env:options.env??process.env,stdio:['ignore',fd,fd]})}finally{fs.closeSync(fd)}
 Object.assign(record,{exit_code:result.status,signal:result.signal,error:result.error?.message??null,finished_utc:new Date().toISOString()});save();
 console.log(JSON.stringify({name,exit_code:result.status}));
 if(result.status!==0&&!options.keepGoing)throw Error('Verification failed: '+name);
 return result.status===0;
}
try{
 if(process.env.NODE_OPTIONS)throw Error('Unexpected NODE_OPTIONS');
 report.typechecker_preflight=assertPinnedTypechecker(path.resolve(root,'../ponder-upstream'));save();
 const url=new URL(process.env.PONDER_TEST_POSTGRES??'');if(url.hostname!=='127.0.0.1'||url.port!=='55432'||url.pathname!=='/ponder_test')throw Error('Explicit task PostgreSQL 55432 required');
 // Fail closed even if someone invokes this driver directly against the old endpoint.
 const fixtureReceipt=process.env.PONDER_M43_POSTGRES_RECEIPT,fixtureDirectory=process.env.PONDER_M43_POSTGRES_DIRECTORY;
 if(!fixtureReceipt||!fixtureDirectory||path.resolve(fixtureReceipt)!==output+'-fixture')throw Error('New M43 wrapper receipt matching this output is required');
 const owned=fs.readFileSync(fixtureReceipt+'/owned-directory.txt','utf8').trim();
 if(fixtureDirectory!==owned+'/data'||fs.realpathSync(fixtureDirectory)!==fixtureDirectory||!owned.startsWith(path.resolve(process.env.TMPDIR)+'/ponder-m43-pg-'))throw Error('Exact canonical M43-owned cluster required');
 const initial=JSON.parse(fs.readFileSync(fixtureReceipt+'/initial-inventory.json','utf8'));
 if(JSON.stringify([...initial.databases].sort())!==JSON.stringify(['ponder_test','postgres','template0','template1'])||!initial.roles.every(r=>r==='agent'||r.startsWith('pg_')))throw Error('Initial cluster was not a new owned fixture');
 const dataCheck=spawnSync('psql',['-XAt','-d',process.env.PONDER_TEST_POSTGRES,'-c','SHOW data_directory'],{cwd:root,encoding:'utf8'});
 fs.writeFileSync(output+'/fixture-preflight.json',JSON.stringify({command:['psql','-XAt','-d','task-loopback-ponder_test','-c','SHOW data_directory'],exit_code:dataCheck.status,stdout:dataCheck.stdout,stderr:dataCheck.stderr,checked_utc:new Date().toISOString()},null,2)+'\n');
 if(dataCheck.status!==0||dataCheck.stdout.trim()!==fixtureDirectory||fs.readFileSync(fixtureReceipt+'/show-data-directory.txt','utf8').trim()!==fixtureDirectory)throw Error('Endpoint is not the exact newly owned M43 cluster; refusing legacy source fixture DDL');
 const fixtureHash=f=>({file:path.relative(root,f),sha256:crypto.createHash('sha256').update(fs.readFileSync(f)).digest('hex')});
 report.postgres_fixture={directory:fixtureDirectory,receipt:path.relative(root,fixtureReceipt),data_directory_verified:true,preflight:fixtureHash(output+'/fixture-preflight.json'),inputs:['owned-directory.txt','show-data-directory.txt','initial-inventory.json','pinned-test-lifecycle.sha256','scope.txt','legacy-fixture-scope.txt'].map(f=>fixtureHash(fixtureReceipt+'/'+f))};save();
 copy=createAdaptedUpstream();fs.writeFileSync(output+'/adapter-manifest.json',JSON.stringify(copy.manifest,null,2)+'\n');fs.writeFileSync(output+'/adapter.diff',copy.diff);
 process.env.PONDER_UPSTREAM_ROOT=copy.directory;process.env.CI='1';process.env.PONDER_RUN_DIR=output+'/native';fs.mkdirSync(process.env.PONDER_RUN_DIR,{recursive:true});
 run('runtime-multichain-evidence-controls',process.execPath,['--test','tools/source-runtime-multichain-evidence.test.mjs']);
 run('runtime-historical-evidence-controls',process.execPath,['--test','tools/source-runtime-historical-evidence.test.mjs']);
 const remainingOriginalFiles=['database/actions','database/index','indexing-store/cache','indexing-store/index','indexing-store/profile','runtime/events','runtime/filter','sync-realtime/bloom','utils/sql-parse'].map(name=>'src/'+name+'.test.ts');
 for(const backend of ['postgres','pglite']){
  const env={...process.env};if(backend==='postgres')env.DATABASE_URL=process.env.PONDER_TEST_POSTGRES;else delete env.DATABASE_URL;
  run('remaining-mapped-original-'+backend,root+'/../ponder-upstream/packages/core/node_modules/.bin/vitest',['run','--config','tools/full-oracle.config.mjs','--configLoader','native',...remainingOriginalFiles,'--reporter=json','--outputFile='+output+'/remaining-mapped-original-'+backend+'.json'],{env});
 }
 fs.copyFileSync(root+'/testdata/runtime/source-runtime-index-live.json',output+'/previous-runtime-index-fixture.json');
 const sourceCaptures={};
 for(const backend of ['postgres','pglite']){
  const directory=output+'/source-'+backend;
  run('runtime-index-source-'+backend,process.execPath,['tools/source-runtime-index-original.mjs','--backend='+backend,'--output='+directory]);
  const source=JSON.parse(fs.readFileSync(directory+'/result.json'));
  if(!source.complete||!source.success||source.counts.passed!==15||!source.environment_adapted||source.pristine_file!==false)throw Error('Complete runtime source capture required');
  sourceCaptures[backend]=JSON.parse(fs.readFileSync(directory+'/calls.json'));
 }
 const fixture={...sourceCaptures.postgres,backend_cases:Object.fromEntries(Object.entries(sourceCaptures).map(([backend,value])=>[backend,value.cases])),native_replay_method:'Each real Go database backend consumes its corresponding complete source backend capture. Expected outputs are comparison-only.'};
 fs.writeFileSync(root+'/testdata/runtime/source-runtime-index-live.json',JSON.stringify(fixture,null,2)+'\n');
 fs.copyFileSync(root+'/testdata/runtime/source-runtime-historical-live.json',output+'/previous-runtime-historical-fixture.json');
 for(const backend of ['postgres','pglite']){
  run('runtime-historical-source-'+backend,process.execPath,['tools/source-runtime-historical-original.mjs','--backend='+backend,'--output='+output+'/historical-source-'+backend]);
  run('runtime-historical-log-order-'+backend,process.execPath,['tools/source-runtime-historical-log-order.mjs','--backend='+backend,'--output='+output+'/historical-log-order-'+backend]);
 }
 run('runtime-historical-fixture',process.execPath,['tools/source-runtime-historical-fixture.mjs','--postgres='+output+'/historical-source-postgres','--pglite='+output+'/historical-source-pglite']);
 for(const file of ['source-runtime-multichain-live.json','source-runtime-multichain-probes.json'])if(fs.existsSync(root+'/testdata/runtime/'+file))fs.copyFileSync(root+'/testdata/runtime/'+file,output+'/previous-'+file);
 for(const backend of ['postgres','pglite']){
  run('runtime-multichain-source-'+backend,process.execPath,['tools/source-runtime-multichain-original.mjs','--backend='+backend,'--output='+output+'/multichain-source-'+backend]);
  run('runtime-multichain-probes-'+backend,process.execPath,['tools/source-runtime-multichain-probes.mjs','--backend='+backend,'--output='+output+'/multichain-probes-'+backend]);
 }
 run('runtime-multichain-fixture',process.execPath,['tools/source-runtime-multichain-fixture.mjs','--postgres='+output+'/multichain-source-postgres','--pglite='+output+'/multichain-source-pglite']);
 run('runtime-multichain-probe-fixture',process.execPath,['tools/source-runtime-multichain-probe-fixture.mjs','--postgres='+output+'/multichain-probes-postgres','--pglite='+output+'/multichain-probes-pglite']);
 for(const backend of ['postgres','pglite'])run('indexing-service-source-'+backend,process.execPath,['tools/source-indexing-service-original.mjs','--backend='+backend,'--output='+output+'/indexing-source-'+backend]);
 run('indexing-service-fixture',process.execPath,['tools/source-indexing-service-fixture.mjs','--postgres='+output+'/indexing-source-postgres','--pglite='+output+'/indexing-source-pglite','--output=testdata/indexing/source-indexing-service-live.json.gz','--declarations=parity/source-indexing-service-declarations.json']);
 run('indexing-service-source-collect',process.execPath,['tools/source-indexing-service-collect.mjs','--postgres='+output+'/indexing-source-postgres','--pglite='+output+'/indexing-source-pglite','--fixture=testdata/indexing/source-indexing-service-live.json.gz','--output='+output+'/indexing-source-verification.json']);
 run('indexing-service-proxy-source',process.execPath,['tools/source-indexing-service-proxy.mjs','--output='+output+'/indexing-proxy']);
 run('indexing-service-proxy-fixture',process.execPath,['tools/source-indexing-service-proxy-fixture.mjs','--input='+output+'/indexing-proxy','--output=testdata/indexing/source-indexing-service-proxy.json']);
 run('indexing-service-evidence-controls',process.execPath,['tools/source-indexing-service-evidence.test.mjs','--fixture=testdata/indexing/source-indexing-service-live.json.gz']);
 run('indexing-service-proxy-evidence-controls',process.execPath,['tools/source-indexing-service-proxy-evidence.test.mjs','--fixture=testdata/indexing/source-indexing-service-proxy.json']);
 run('indexing-service-cache-source',process.execPath,['tools/source-indexing-service-cache.mjs','--output='+output+'/indexing-cache']);
 run('indexing-service-cache-fixture',process.execPath,['tools/source-indexing-service-cache-fixture.mjs','--input='+output+'/indexing-cache','--output=testdata/indexing/source-indexing-service-cache.json']);
 run('indexing-service-cache-evidence-controls',process.execPath,['tools/source-indexing-service-cache-evidence.test.mjs','--fixture=testdata/indexing/source-indexing-service-cache.json']);
 for(const backend of ['postgres','pglite']){
  run('indexing-actions-source-'+backend,process.execPath,['tools/source-indexing-actions-original.mjs','--backend='+backend,'--output='+output+'/actions-source-'+backend]);
  run('indexing-actions-probes-'+backend,process.execPath,['tools/source-indexing-actions-probes.mjs','--backend='+backend,'--output='+output+'/actions-probes-'+backend]);
 }
 run('indexing-actions-fixture',process.execPath,['tools/source-indexing-actions-fixture.mjs','--postgres='+output+'/actions-source-postgres','--pglite='+output+'/actions-source-pglite','--output=testdata/indexing/source-indexing-actions-live.json.gz','--declarations=parity/source-indexing-actions-cases.json']);
 run('indexing-actions-source-collect',process.execPath,['tools/source-indexing-actions-collect.mjs','--postgres='+output+'/actions-source-postgres','--pglite='+output+'/actions-source-pglite','--fixture=testdata/indexing/source-indexing-actions-live.json.gz','--output='+output+'/actions-source-verification.json']);
 run('indexing-actions-probe-fixture',process.execPath,['tools/source-indexing-actions-probe-fixture.mjs','--postgres='+output+'/actions-probes-postgres','--pglite='+output+'/actions-probes-pglite','--output=testdata/indexing/source-indexing-actions-probes.json.gz']);
 run('indexing-actions-evidence-controls',process.execPath,['--test','tools/source-indexing-actions-evidence.test.mjs','tools/indexing-actions-path-guards.test.mjs']);
 for(const backend of ['postgres','pglite']){
  run('indexing-profile-source-'+backend,process.execPath,['tools/source-indexing-profile-original.mjs','--backend='+backend,'--output='+output+'/profile-source-'+backend]);
  run('indexing-profile-probes-'+backend,process.execPath,['tools/source-indexing-profile-probes.mjs','--backend='+backend,'--output='+output+'/profile-probes-'+backend]);
 }
 run('indexing-profile-fixture',process.execPath,['tools/source-indexing-profile-fixture.mjs','--postgres='+output+'/profile-source-postgres','--pglite='+output+'/profile-source-pglite','--output=testdata/indexing/source-indexing-profile.json.gz','--declarations=parity/source-indexing-profile-declarations.json']);
 run('indexing-profile-source-collect',process.execPath,['tools/source-indexing-profile-collect.mjs','--postgres='+output+'/profile-source-postgres','--pglite='+output+'/profile-source-pglite','--fixture=testdata/indexing/source-indexing-profile.json.gz','--output='+output+'/profile-source-verification.json']);
 run('indexing-profile-probe-fixture',process.execPath,['tools/source-indexing-profile-probe-fixture.mjs','--postgres='+output+'/profile-probes-postgres','--pglite='+output+'/profile-probes-pglite','--output=testdata/indexing/source-indexing-profile-probes.json.gz']);
 run('indexing-profile-evidence-controls',process.execPath,['--test','tools/source-indexing-profile-evidence.test.mjs','tools/indexing-profile-path-guards.test.mjs']);
 for(const backend of ['postgres','pglite'])run('independent-storage-source-'+backend,process.execPath,['tools/source-independent-storage.mjs','--backend='+backend,'--output='+output+'/independent-source-'+backend]);
 run('independent-storage-evidence-controls',process.execPath,['--test','tools/independent-storage-path-guards.test.mjs','tools/independent-storage-evidence.test.mjs']);
 for(const backend of ['postgres','pglite'])run('multichain-atomic-source-'+backend,process.execPath,['tools/source-multichain-atomic.mjs','--backend='+backend,'--output='+output+'/atomic-source-'+backend]);
 run('multichain-atomic-evidence-controls',process.execPath,['--test','tools/multichain-atomic-path-guards.test.mjs','tools/multichain-atomic-evidence.test.mjs']);
 for(const backend of ['postgres','pglite'])run('finite-historical-source-'+backend,process.execPath,['tools/source-finite-historical.mjs','--backend='+backend,'--output='+output+'/finite-source-'+backend]);
 run('finite-historical-evidence-controls',process.execPath,['--test','tools/finite-historical-path-guards.test.mjs','tools/finite-historical-evidence.test.mjs'],{env:{...process.env,PONDER_FINITE_HISTORICAL_SOURCE_ROOT:output}});
 for(const [i,file] of ['parity/runs/sync-store-m32/type-compile.json','testdata/runtime/source-historical-types.json'].entries())if(fs.existsSync(root+'/'+file))fs.copyFileSync(root+'/'+file,output+'/previous-type-control-'+i+'.json');
 run('sync-type-controls',process.execPath,['tools/source-sync-compile.mjs','--write']);
 run('historical-type-controls',process.execPath,['tools/source-historical-types/compile.mjs']);
 run('sdk-client-build',root+'/sdk/typescript/client/node_modules/.bin/tsup',[],{cwd:root+'/sdk/typescript/client'});
 run('prepared-native-inputs',process.execPath,['tools/record-native-inputs.mjs']);
 fs.copyFileSync(root+'/parity/runs/prepared-native-inputs.json',output+'/prepared-native-inputs.json');
 report.prepared_native_inputs={file:path.relative(root,output+'/prepared-native-inputs.json'),sha256:crypto.createHash('sha256').update(fs.readFileSync(output+'/prepared-native-inputs.json')).digest('hex')};
 report.native_inputs=sourceFingerprint(root);save();
 const beforeFiles=[...new Set([...finiteHistoricalHarness,...atomicMultichainHarness,...independentStorageHarness,...indexingProfileHarness,...indexingActionsHarness,'tools/manifest.mjs','tools/source-indexing-service-cache.mjs','tools/source-indexing-service-cache.config.mjs','tools/source-indexing-service-cache.test.ts','tools/source-indexing-service-cache-fixture.mjs','tools/source-indexing-service-cache-evidence.test.mjs','tools/indexing-service-full-verify.mjs','tools/source-indexing-service-results.mjs','tools/source-indexing-service-map.mjs','tools/source-indexing-service-collect.mjs','tools/source-indexing-service-proxy.mjs','tools/source-indexing-service-proxy.config.mjs','tools/source-indexing-service-proxy.test.ts','tools/source-indexing-service-proxy-fixture.mjs','tools/source-indexing-service-proxy-evidence.test.mjs','tools/source-indexing-service-original.mjs','tools/source-indexing-service-live.config.mjs','tools/source-indexing-service-live.setup.ts','tools/source-indexing-service-fixture.mjs','tools/source-indexing-service-evidence.mjs','tools/source-indexing-service-evidence.test.mjs','tools/go-source-fingerprint.mjs','tools/record-native-inputs.mjs','tools/source-sync-results.mjs','tools/source-historical-results.mjs','tools/source-realtime-results.mjs','tools/config-live-results.mjs','tools/sql-source-results.mjs','tools/source-runtime-multichain-results.mjs','tools/source-runtime-multichain-map.mjs','tools/record-results.mjs','tools/source-runtime-historical-results.mjs','tools/source-runtime-index-results.mjs','tools/go-tests.mjs','tools/runtime-multichain-full-verify.mjs','tools/pinned-typecheck-preflight.mjs','tools/loopback-adapter.mjs','tools/loopback-context.mjs','tools/client-compat-launch.mjs','tools/client-compat-provenance.mjs','tools/source-runtime-index-live.setup.ts','tools/source-runtime-index-original.mjs','tools/source-runtime-historical-original.mjs','tools/source-runtime-historical-live.config.mjs','tools/source-runtime-historical-live.setup.ts','tools/source-runtime-historical-fixture.mjs','tools/source-runtime-historical-evidence.mjs','tools/source-runtime-historical-evidence.test.mjs','tools/source-runtime-historical-log-order.mjs','tools/source-runtime-historical-log-order.config.mjs','tools/source-runtime-historical-log-order.test.ts','tools/source-runtime-multichain-original.mjs','tools/source-runtime-multichain-live.config.mjs','tools/source-runtime-multichain-live.setup.ts','tools/source-runtime-multichain-fixture.mjs','tools/source-runtime-multichain-evidence.mjs','tools/source-runtime-multichain-evidence.test.mjs','tools/source-runtime-multichain-probes.mjs','tools/source-runtime-multichain-probes.config.mjs','tools/source-runtime-multichain-probes.test.ts','tools/source-runtime-multichain-probe-fixture.mjs'])].map(file=>({file,sha256:crypto.createHash('sha256').update(fs.readFileSync(root+'/'+file)).digest('hex')}));report.harness_inputs=beforeFiles;save();
 freezePreparedInputs(report.prepared_native_inputs,beforeFiles);
 run('multichain-atomic-negative-controls','bash',['tools/multichain-atomic-negative-controls.sh',output+'/native-shortcut-controls']);
 run('finite-historical-negative-controls','bash',['tools/finite-historical-negative-controls.sh',output+'/finite-shortcut-controls']);
 run('abi-generated-normal','go',['test','-count=1','-json','./abi/testdata/compile/generated']);
 run('abi-generated-race','go',['test','-count=1','-race','-json','./abi/testdata/compile/generated']);
 const normal=run('go-normal',process.execPath,['tools/go-tests.mjs'],{keepGoing:true,env:{...process.env,PONDER_INDEXING_PROFILE_NATIVE_OUTPUT:output+'/native/indexing-profile-normal.json',PONDER_INDEXING_PROFILE_PROBES_NATIVE_OUTPUT:output+'/native/indexing-profile-probes-normal.json',PONDER_INDEXING_ACTIONS_PROBES_NATIVE_OUTPUT:output+'/native/indexing-actions-probes-normal.json',PONDER_INDEXING_ACTIONS_NATIVE_OUTPUT:output+'/native/indexing-actions-normal.json',PONDER_INDEXING_SERVICE_NATIVE_OUTPUT:output+'/native/indexing-service-normal.json'}});
 const race=run('go-race',process.execPath,['tools/go-tests.mjs','--race'],{keepGoing:true,env:{...process.env,PONDER_INDEXING_PROFILE_NATIVE_OUTPUT:output+'/native/indexing-profile-race.json',PONDER_INDEXING_PROFILE_PROBES_NATIVE_OUTPUT:output+'/native/indexing-profile-probes-race.json',PONDER_INDEXING_ACTIONS_PROBES_NATIVE_OUTPUT:output+'/native/indexing-actions-probes-race.json',PONDER_INDEXING_ACTIONS_NATIVE_OUTPUT:output+'/native/indexing-actions-race.json',PONDER_INDEXING_SERVICE_NATIVE_OUTPUT:output+'/native/indexing-service-race.json'}});
 run('go-vet','go',['vet','./...']);
 fs.mkdirSync(root+'/bin',{recursive:true});run('cli-build','go',['build','-o','bin/ponder','./cmd/ponder']);run('erc20-build','go',['build','-o','bin/erc20','./examples/erc20']);
 run('core-oracle',root+'/../ponder-upstream/packages/core/node_modules/.bin/vitest',['run','--config','tools/oracle.config.mjs','--configLoader','native','--maxWorkers=2','src/utils/checkpoint.test.ts','src/utils/interval.test.ts','src/utils/finality.test.ts','src/build/schema.test.ts','src/utils/decodeEventLog.test.ts','src/utils/decodeAbiParameters.test.ts','src/build/factory.test.ts','src/utils/offset.test.ts','src/indexing-store/utils.test.ts','src/runtime/fragments.test.ts','src/rpc/actions.test.ts','--reporter=json','--outputFile='+output+'/core-oracle.json']);
 run('parameters-types',process.execPath,['tools/parameters-typecheck.mjs']);
 run('sync-types',process.execPath,['tools/source-sync-types.mjs']);
 run('historical-types',process.execPath,['tools/source-historical-types.mjs']);
 run('runtime-index-types',process.execPath,['tools/source-runtime-index-types.mjs'],{env:{...process.env,PONDER_UPSTREAM_ROOT:path.resolve(root,'../ponder-upstream')}});
 run('sdk-client-tests',root+'/sdk/typescript/client/node_modules/.bin/vitest',['run','--configLoader','native','--maxWorkers=2','--reporter=json','--outputFile='+output+'/sdk-client.json'],{cwd:root+'/sdk/typescript/client'});
 run('sdk-react-types',root+'/sdk/typescript/react/node_modules/.bin/vitest',['run','--typecheck.only','--configLoader','native','--maxWorkers=2','--reporter=json','--outputFile='+output+'/sdk-react.json'],{cwd:root+'/sdk/typescript/react'});
 report.finished_native_inputs=sourceFingerprint(root);
 if(report.native_inputs.sha256!==report.finished_native_inputs.sha256)throw Error('Native inputs changed across aggregate verification');
 for(const entry of beforeFiles)if(crypto.createHash('sha256').update(fs.readFileSync(root+'/'+entry.file)).digest('hex')!==entry.sha256)throw Error('Verification harness changed: '+entry.file);
 const frozen=report.prepared_input_archive;
 for(const ref of [frozen.archive,frozen.manifest])if(physicalSHA256(path.resolve(root,ref.file))!==ref.sha256)throw Error('Prepared physical archive changed after native execution');
 const physicalManifest=JSON.parse(fs.readFileSync(path.resolve(root,frozen.manifest.file),'utf8'));
 for(const entry of physicalManifest.entries){const stat=fs.lstatSync(entry.source_path);if(!stat.isFile()||fs.realpathSync(entry.source_path)!==entry.source_path||stat.size!==entry.bytes||(stat.mode&0o7777)!==entry.mode||physicalSHA256(entry.source_path)!==entry.sha256)throw Error('Physically frozen input changed after native execution: '+entry.file)}
 frozen.live_inputs_verified_after_native=true;
 report.success=normal&&race;
}catch(error){report.error??=String(error.stack??error);report.success=false;process.exitCode=1;}finally{
 if(copy){try{copy.verify();const persisted=persistAdaptedContext(copy,output);report.context_archive={file:path.relative(root,persisted.path),sha256:persisted.sha256};report.source_manifest_verified_after=true}catch(error){report.context_error=String(error.stack??error);report.error??=report.context_error;report.success=false;process.exitCode=1}finally{try{copy.dispose()}catch(error){report.dispose_error=String(error.stack??error);report.error??=report.dispose_error;report.success=false;process.exitCode=1}}}
 report.complete=true;report.finished_utc=new Date().toISOString();save();console.log(JSON.stringify({output,success:report.success,error:report.error}));if(!report.success)process.exitCode=1;
}
