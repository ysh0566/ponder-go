// Separate M44 full-02 corrective audit. Original reports and logs are immutable.
// The only admitted discrepancy is six no-Test empty-package skip events.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {spawnSync} from 'node:child_process';
import {pathToFileURL,fileURLToPath} from 'node:url';
export const must=(v,m)=>{if(!v)throw Error(m);};
const equal=(a,b)=>JSON.stringify(a)===JSON.stringify(b);
export const packages=['abi','bloom','checkpoint','cmd/abi-codegen','cmd/parity-probe','cmd/ponder','cmd/source-codegen','config','events','examples/erc20','factory','filter','finality','historical','indexing','internal/jsvalue','interval','orm','query','rpc','runtime','schema','sqlquery','store'].map(p=>'ponder.local/ponder-go/'+p).sort();
export const emptyPackages=['cmd/abi-codegen','cmd/parity-probe','cmd/ponder','cmd/source-codegen','examples/erc20','internal/jsvalue'].map(p=>'ponder.local/ponder-go/'+p).sort();
export function validateDiscovery(discovered) {
 must(equal(discovered.map(p=>p.ImportPath).sort(),packages),'Raw independent package discovery differs');
 for(const p of discovered.filter(p=>emptyPackages.includes(p.ImportPath)))must((p.TestGoFiles??[]).length===0&&(p.XTestGoFiles??[]).length===0,'Skipped package has independent discovered test files');
}
export function classifyNative(manifest,events,discovered,mode) {
 must(equal([...discovered].sort(),packages),'Independent 24-package census differs');
 must(manifest.complete&&manifest.success&&manifest.scope==='all_module_packages'&&manifest.race===(mode==='race'),'Complete exact native mode required');
 must(equal(manifest.packages.map(p=>p.package).sort(),packages),'Missing, duplicate or unknown package');
 const byPackage=new Map(manifest.packages.map(p=>[p.package,p]));
 must(equal(manifest.packages.filter(p=>p.listed_functions.length===0).map(p=>p.package).sort(),emptyPackages),'Exact six genuinely empty packages required');
 for(const p of manifest.packages) {
  must(new Set(p.listed_functions).size===p.listed_functions.length,'Duplicate listed function');
  const batched=p.batches.flatMap(b=>b.functions);
  must(equal(batched,p.listed_functions)&&new Set(batched).size===batched.length,'Incomplete/duplicate function partition');
  must(p.batches.length===Math.max(1,Math.ceil(p.listed_functions.length/4)),'Wrong batch census');
  for(const b of p.batches) {
   must(b.complete&&b.exit_code===0&&!b.signal&&!b.error,'Unsuccessful batch');
   const command=['go','test','-count=1',...(mode==='race'?['-race']:[]),'-json',...(b.functions.length?['-run','^('+b.functions.join('|')+')$']:[]),p.package];
   must(equal(b.command,command),'Unexpected native batch command');
  }
 }
 const start=Date.parse(manifest.started_utc),end=Date.parse(manifest.finished_utc);
 must(Number.isFinite(start)&&Number.isFinite(end)&&start<=end&&events.length>0,'Native timing/log absent');
 const run=new Map(),pass=new Map(),skips=[];
 for(const e of events) {
  must(byPackage.has(e.Package),'Unknown event package');
  must(Number.isFinite(Date.parse(e.Time))&&Date.parse(e.Time)>=start-1000&&Date.parse(e.Time)<=end+1000,'Event outside native execution');
  must(e.Action!=='fail','Package or test failure is never exempt');
  if(e.Action==='skip') {
   must(!Object.hasOwn(e,'Test')&&emptyPackages.includes(e.Package),'Test skip/nonempty package skip is never exempt');
   const p=byPackage.get(e.Package);must(p.listed_functions.length===0&&p.batches.length===1&&p.batches[0].functions.length===0&&p.batches[0].exit_code===0,'Skip lacks actual empty-manifest proof');
   skips.push(e);continue;
  }
  if(Object.hasOwn(e,'Test')) {
   must(typeof e.Test==='string'&&e.Test.length>0&&byPackage.get(e.Package).listed_functions.includes(e.Test.split('/')[0]),'Unlisted test event');
   const key=e.Package+'\0'+e.Test;
   if(e.Action==='run')run.set(key,(run.get(key)??0)+1);
   if(e.Action==='pass')pass.set(key,(pass.get(key)??0)+1);
  }
 }
 must(equal(skips.map(e=>e.Package).sort(),emptyPackages),'Missing/duplicate exact empty-package skips');
 must(run.size===pass.size&&[...run].every(([k,n])=>n===1&&pass.get(k)===1),'Missing/duplicate test run/pass');
 for(const p of manifest.packages)for(const name of p.listed_functions)must(pass.get(p.package+'\0'+name)===1,'Listed function did not pass exactly once');
 const names=[...run.keys()],leaves=names.filter(n=>!names.some(other=>other.startsWith(n+'/'))).sort();
 return {package_skips:skips,leaf_names:leaves,leaf_count:leaves.length,filtered:events.filter(e=>e.Action!=='skip')};
}
export const collectors=[['source-sync','source-sync-results','source-sync-verification',true],['source-historical','source-historical-results','source-historical-verification',true],['source-realtime','source-realtime-results','source-realtime-verification',true],['config-live','config-live-results','config-live-verification',true],['sql-source','sql-source-results','sql-source-verification',true],['source-runtime-index','source-runtime-index-results','source-runtime-index-verification',false],['source-runtime-historical','source-runtime-historical-results','source-runtime-historical-verification',false],['source-runtime-multichain','source-runtime-multichain-results','source-runtime-multichain-verification',false],['source-indexing-service','source-indexing-service-results','source-indexing-service-verification',false],['source-indexing-actions','source-indexing-actions-results','source-indexing-actions-verification',false],['source-indexing-profile','source-indexing-profile-results','source-indexing-profile-verification',false],['independent-storage','independent-storage-results','independent-storage-verification',false],['multichain-atomic','multichain-atomic-results','multichain-atomic-verification',false],['finite-historical','finite-historical-results','finite-historical-verification',false]];
const W='parity/runs/managed-finite-m44/full-02-20261026',B='parity/runs/finite-historical-m43/m44-regression-full-02-20261026';
function hash(root,file) {const target=path.resolve(root,file),fd=fs.openSync(target,'r'),h=crypto.createHash('sha256'),buf=Buffer.alloc(1024*1024);try{for(let n;(n=fs.readSync(fd,buf,0,buf.length,null))>0;)h.update(buf.subarray(0,n));}finally{fs.closeSync(fd);}return{file:path.relative(root,target),sha256:h.digest('hex')};}
function verifyArchive(root,frozen) {
 const program=String.raw`
import sys,json,pathlib,tarfile,hashlib
root=pathlib.Path(sys.argv[1]);cfg=json.load(sys.stdin)
manifest=root/cfg['manifest']['file'];physical=json.loads(manifest.read_text())
expected={e['archive_path']:{'sha256':e['sha256'],'bytes':e['bytes'],'mode':e['mode']}for e in physical['entries']}
assert len(expected)==len(physical['entries'])
expected['prepared-inputs-manifest.json']={'sha256':cfg['manifest']['sha256'],'bytes':manifest.stat().st_size,'mode':0o644}
seen=set();total=0
with tarfile.open(root/cfg['archive']['file'],'r|gz') as archive:
 for member in archive:
  assert member.name in expected and member.name not in seen and member.isfile(),member.name
  want=expected[member.name];assert member.size==want['bytes'] and member.mode==want['mode'],member.name
  h=hashlib.sha256();stream=archive.extractfile(member)
  while chunk:=stream.read(1024*1024):h.update(chunk)
  assert h.hexdigest()==want['sha256'],member.name
  seen.add(member.name);total+=member.size
assert seen==set(expected)
print(json.dumps({'complete':True,'success':True,'members':len(seen),'uncompressed_bytes':total,'method':'Independent streaming hash/size/mode check of every immutable tar member; no extraction.'}))
`;
 const r=spawnSync('python3',['-c',program,root],{input:JSON.stringify(frozen),encoding:'utf8',timeout:300000,maxBuffer:1024*1024});
 must(r.status===0&&!r.error,'Independent archive member verification failed: '+r.stderr);return JSON.parse(r.stdout);
}
function infrastructure(root) {
 const read=f=>JSON.parse(fs.readFileSync(path.resolve(root,f),'utf8'));
 const valid=r=>r&&typeof r.file==='string'&&hash(root,r.file).sha256===r.sha256;
 return {read,valid,ref:f=>hash(root,f)};
}
export async function audit(root,{afterResume=false}={}) {
 const {read,valid,ref}=infrastructure(root);
 const helpers=await import(pathToFileURL(root+'/tools/managed-finite-evidence.mjs'));
 const manifest=await import(pathToFileURL(root+'/tools/managed-finite-acceptance.mjs'));
 const {sourceFingerprint}=await import(pathToFileURL(root+'/tools/go-source-fingerprint.mjs'));
 const w=read(W+'/result.json'),full=read(B+'/result.json'),frozen=full.prepared_input_archive,physical=read(frozen.manifest.file),prepared=read(full.prepared_native_inputs.file);
 must(w.complete&&w.success===false&&w.executions.length===0&&w.error.startsWith('Error: Native failures/skips cannot establish acceptance\n')&&w.error.includes('/tools/managed-finite-evidence.mjs:32:3')&&w.error.includes('/tools/managed-finite-full-verify.mjs:66:121'),'Not the exact known wrapper-only classifier failure');
 must(equal(w.commands.map(c=>c.name),['preflight','managed-guard-controls','observer-identity-controls','unchanged-full-regression'])&&w.commands.every(c=>c.exit_code===0&&!c.signal&&!c.error&&valid(c.log)),'Unexpected run/missing successful wrapper stage');
 must(!fs.existsSync(root+'/'+B+'/canonical-collectors.json')&&!fs.existsSync(root+'/'+W+'/verification.json')&&!fs.existsSync(root+'/'+W+'/closeout.json'),'Original unrun stage evidence unexpectedly exists');
 must(full.complete&&full.success&&full.commands.length===73&&new Set(full.commands.map(c=>c.name)).size===73&&full.commands.every(c=>c.exit_code===0&&!c.signal&&!c.error),'Full 73-stage baseline did not pass');
 must(full.environment_adapted&&full.pristine_file===false&&full.source_manifest_verified_after&&full.source_credit===0,'Baseline scope differs');
 must(valid(w.full_regression)&&w.full_regression.file===B+'/result.json'&&w.source_credit===0&&w.ported_original===543&&w.original_total===905,'Wrong immutable baseline/credit linkage');
 helpers.validateManagedSnapshotList(w.previous_derived_outputs,manifest.managedFiniteDerivedOutputs);
 must(equal(w.derived_output_allowlist,manifest.managedFiniteDerivedOutputs)&&w.previous_derived_outputs.every(e=>!e.absent_before&&e.before.file===e.file&&e.before.sha256===e.snapshot.sha256&&valid(e.snapshot)),'Exact16 original before-snapshots differ');
 must(w.harness_inputs.length===12&&equal(w.harness_inputs.map(r=>r.file),manifest.managedFiniteHarness)&&w.harness_inputs.every(valid)&&w.managed_test_inputs.every(valid)&&w.source_mapping_inputs.every(valid),'Frozen managed source/harness/identities changed');
 must(w.managed_harness_snapshots.length===12&&w.managed_harness_snapshots.every(e=>valid(e.input)&&valid(e.snapshot)&&e.input.sha256===e.snapshot.sha256),'Physical managed snapshots differ');
 const fingerprint=sourceFingerprint(root);
 must(equal(fingerprint,full.native_inputs)&&equal(fingerprint,full.finished_native_inputs)&&equal(fingerprint,prepared.fingerprint)&&valid(full.prepared_native_inputs)&&prepared.files.every(valid),'1191 current prepared/native inputs differ');
 must(fingerprint.files===1191&&frozen.complete&&frozen.success&&frozen.live_inputs_verified_after_native&&valid(frozen.archive)&&valid(frozen.manifest),'Prepared physical archive differs');
 must(full.harness_inputs.length===139&&full.harness_inputs.every(valid)&&physical.entries.length===1385&&physical.runtime_type_executables.length===21,'Exact original harness/archive/type census differs');
 for(const e of physical.entries){if(afterResume&&manifest.managedFiniteDerivedOutputs.includes(e.file))continue;const stat=fs.lstatSync(path.resolve(root,e.file));must(stat.isFile()&&fs.realpathSync(path.resolve(root,e.file))===e.source_path&&stat.size===e.bytes&&(stat.mode&0o7777)===e.mode&&valid(e),'Frozen input changed: '+e.file);}
 const archiveVerification=verifyArchive(root,frozen);
 must(equal(physical.go_discovery.packages,packages)&&physical.go_discovery.package_count===24&&valid(physical.go_discovery.raw),'Independent frozen Go discovery differs');
 const rawDiscovery=fs.readFileSync(root+'/'+physical.go_discovery.raw.file,'utf8'),discovered=[];let depth=0,string=false,escaped=false,start=0;
 for(let i=0;i<rawDiscovery.length;i++){const c=rawDiscovery[i];if(string){if(escaped)escaped=false;else if(c==='\\')escaped=true;else if(c==='"')string=false;continue;}if(c==='"')string=true;else if(c==='{'){if(depth===0)start=i;depth++;}else if(c==='}'&&--depth===0)discovered.push(JSON.parse(rawDiscovery.slice(start,i+1)));}
 validateDiscovery(discovered);
 const tests=helpers.inventory(root),modes=[];
 for(const mode of ['normal','race']) {
  const stem=mode==='normal'?'go-test':'go-race',execution=read(B+'/native/'+stem+'-execution.json'),log=B+'/native/'+stem+'.jsonl';
  must(execution.driver_sha256===ref('tools/go-tests.mjs').sha256&&equal(execution.source_fingerprint,fingerprint)&&equal(execution.finished_source_fingerprint,fingerprint),'Driver/native mode hashes differ');
  must(Date.parse(execution.started_utc)>=Date.parse(prepared.recorded_utc)&&Date.parse(execution.finished_utc)<=Date.parse(full.finished_utc),'Mode outside prepared baseline');
  const events=fs.readFileSync(root+'/'+log,'utf8').trim().split('\n').map(JSON.parse),proof=classifyNative(execution,events,discovered.map(p=>p.ImportPath),mode);
  must(proof.leaf_count===3463,'Wrong actual full native leaf census');
  const managed=helpers.validateManagedEvents(proof.filtered,tests);must(managed.reduce((n,t)=>n+t.leaves.length,0)===107&&managed.length===36,'Wrong actual managed leaf/top-level census');
  modes.push({mode,manifest:ref(B+'/native/'+stem+'-execution.json'),log:ref(log),package_skips:proof.package_skips,leaf_count:proof.leaf_count,leaf_names:proof.leaf_names,managed});
 }
 must(equal(modes[0].leaf_names,modes[1].leaf_names)&&equal(modes[0].managed,modes[1].managed),'Normal/race actual test inventories differ');
 const observer=read(W+'-observer/result.json'),launch=read(W+'-launch/launch.json'),joined=read(W+'-launch/joined.json'),sequence=read(W+'-exec/result.json');
 must(observer.complete&&observer.success===false&&observer.exit_code===1&&observer.signal===null&&observer.interrupted.length===0&&!observer.error&&!observer.drain_error,'Observer failure is not solely propagated wrapper status1');
 must(observer.final_remaining_identities.length===0&&observer.final_remaining_groups.length===0&&observer.observed.length===3618&&new Set(observer.observed.map(o=>o.pid+':'+o.start_ticks)).size===3618,'Raw observed identities did not drain');
 must(observer.inputs.every(valid)&&valid(observer.observer)&&observer.observer.sha256===ref('tools/managed-finite-observe.py').sha256&&ref(W+'/outer-closeout.json').sha256===ref(W+'-observer/result.json').sha256,'Raw observer source/inputs/copy changed');
 helpers.validateManagedReferences(observer.inputs,['admission.json','root-identity.json','timeline.jsonl','launch.log'].map(n=>W+'-observer/'+n),valid);
 const cap=observer.capacity;must(cap.floor_bytes===5*1024**3&&cap.budget_bytes===4*1024**3&&cap.final_free_bytes>=cap.floor_bytes&&cap.initial_free_bytes-cap.final_free_bytes<=cap.budget_bytes,'Capacity contract not preserved');
 const timeline=fs.readFileSync(root+'/'+W+'-observer/timeline.jsonl','utf8').trim().split('\n').map(JSON.parse);
 must(timeline.length>0&&timeline.at(-1).processes.length===0&&timeline.every(r=>r.free_bytes>=cap.floor_bytes&&cap.initial_free_bytes-r.free_bytes<=cap.budget_bytes&&Date.parse(r.utc)>=Date.parse(observer.started_utc)&&Date.parse(r.utc)<=Date.parse(observer.finished_utc)),'Raw observer timeline capacity/drain differs');
 must(joined.exit_code===1&&joined.signal===null&&joined.interrupted===null&&joined.processes.length===0&&joined.pgid===launch.pgid&&launch.pgid===launch.pid&&equal(joined.launch,ref(W+'-launch/launch.json'))&&valid(launch.launcher),'Original launch did not join cleanly with propagated failure');
 must(observer.observed.some(o=>o.pid===launch.pid&&o.pgid===launch.pgid&&o.start_ticks===launch.proc_start_ticks),'Inner launch not actually observed');
 must(Date.parse(joined.observed_utc)>=Date.parse(w.finished_utc)&&Date.parse(observer.finished_utc)>=Date.parse(joined.observed_utc),'Historical closeout chronology differs');
 must(sequence.complete&&sequence.success===false&&sequence.observer_exit_code==='1'&&sequence.collector_exit_code==='not_run'&&sequence.shell_exit_code===1&&!sequence.interrupted&&sequence.script_unchanged&&sequence.pid_namespace_before===sequence.pid_namespace_after,'Original same-exec propagation differs');
 for(const r of sequence.references)if(!r.missing)must(valid(r),'Original same-exec referenced input changed');
 const fixture=B+'-fixture',owned=fs.readFileSync(root+'/'+fixture+'/owned-directory.txt','utf8').trim(),data=fs.readFileSync(root+'/'+fixture+'/show-data-directory.txt','utf8').trim(),initial=read(fixture+'/initial-inventory.json'),final=read(fixture+'/final-inventory.json');
 must(data===owned+'/data'&&data===full.postgres_fixture.directory&&full.postgres_fixture.receipt===fixture&&full.postgres_fixture.data_directory_verified&&valid(full.postgres_fixture.preflight)&&full.postgres_fixture.inputs.every(valid),'Frozen exact fixture identity differs');
 must(fs.readFileSync(root+'/'+fixture+'/exit-code.txt','utf8').trim()==='1'&&fs.readFileSync(root+'/'+fixture+'/shutdown.log','utf8').includes('server stopped')&&equal(initial,final)&&equal([...final.databases].sort(),['ponder_test','postgres','template0','template1'])&&final.roles.every(r=>r==='agent'||r.startsWith('pg_'))&&!fs.existsSync(data+'/postmaster.pid'),'Historical fixture stop/final inventory not confirmed');
 const originalRefs=[ref(W+'/result.json'),ref(B+'/result.json'),ref(W+'-observer/result.json'),ref(W+'-launch/launch.json'),ref(W+'-launch/joined.json'),ref(W+'-exec/result.json'),...['owned-directory.txt','show-data-directory.txt','initial-inventory.json','final-inventory.json','shutdown.log','exit-code.txt'].map(f=>ref(fixture+'/'+f)),...w.commands.map(c=>c.log),...modes.flatMap(m=>[m.manifest,m.log]),frozen.archive,frozen.manifest,full.prepared_native_inputs,physical.go_discovery.raw];
 return {complete:true,success:true,scope:'independent_corrective_audit_not_original_wrapper_success',source_credit:0,original_failure_preserved:true,unrun_stages:['canonical-predecessor-collectors','derived-ledger-summary'],original_references:originalRefs,source_fingerprint:fingerprint,modes,physical:{archive:frozen.archive,manifest:frozen.manifest,input_files:physical.entries.length,member_verification:archiveVerification},historical_closeout:{observer_exit_code:observer.exit_code,observer_success:false,launch_exit_code:joined.exit_code,fixture_exit_code:1,observed_identity_count:observer.observed.length,remaining_identities:[],remaining_groups:[],observer:ref(W+'-observer/result.json'),joined:ref(W+'-launch/joined.json'),same_exec:ref(W+'-exec/result.json'),scope:'Raw original same-exec observations and filesystem shutdown receipts. No current cross-exec PID/group visibility claim.'},derived_output_allowlist:manifest.managedFiniteDerivedOutputs,original_snapshot_references:w.previous_derived_outputs,recorded_utc:new Date().toISOString()};
}
export async function resume(root,output,auditFile,controlsFile) {
 const {read,valid,ref}=infrastructure(root),prior=read(auditFile),controls=read(controlsFile);
 must(prior.complete&&prior.success&&prior.scope==='independent_corrective_audit_not_original_wrapper_success'&&prior.original_references.every(valid),'Reviewed audit is stale or incomplete');
 must(valid(prior.tool)&&prior.tool.sha256===hash(root,fileURLToPath(import.meta.url)).sha256&&valid(prior.tool_snapshot),'Corrective implementation differs from audited bytes');
 must(controls.complete&&controls.success&&controls.exit_code===0&&controls.failed===0&&controls.passed>=20&&controls.inputs.every(valid)&&valid(controls.log)&&controls.inputs.some(r=>r.sha256===prior.tool.sha256),'Reviewed negative controls absent/stale');
 const helpers=await import(pathToFileURL(root+'/tools/managed-finite-evidence.mjs')),manifest=await import(pathToFileURL(root+'/tools/managed-finite-acceptance.mjs'));
 const expectedCollectors=JSON.parse(fs.readFileSync(root+'/tools/finite-historical-collect-all.mjs','utf8').match(/const collectors=(\[[\s\S]*?\n\]);/)[1].replaceAll("'",'"').replace(/,\s*\]/g,']'));
 must(equal(expectedCollectors,collectors),'Canonical collector inventory drifted');
 const result={complete:false,success:false,scope:'separate_corrective_resume_of_two_previously_unrun_stages',source_credit:0,ported_original:543,original_total:905,started_utc:new Date().toISOString(),original_wrapper_status_preserved:'complete=true, success=false; exact empty-package classification failure',audit:ref(auditFile),controls:ref(controlsFile),commands:[],derived_output_allowlist:manifest.managedFiniteDerivedOutputs,previous_derived_outputs:[]};
 const save=()=>fs.writeFileSync(output+'/resume.json',JSON.stringify(result,null,2)+'\n');
 try {
  const current=await audit(root);must(equal(current.source_fingerprint,prior.source_fingerprint),'Source fingerprint drifted since approved audit');
  for(const file of manifest.managedFiniteDerivedOutputs){const destination=output+'/before-derived/'+file;fs.mkdirSync(path.dirname(destination),{recursive:true});fs.copyFileSync(root+'/'+file,destination,fs.constants.COPYFILE_EXCL);result.previous_derived_outputs.push({file,before:ref(file),snapshot:ref(destination)});}
  helpers.validateManagedSnapshotList(result.previous_derived_outputs,manifest.managedFiniteDerivedOutputs);save();
  function run(name,args) {
   const entry={name,command:[process.execPath,...args],started_utc:new Date().toISOString()},log=output+'/'+name+'.log';result.commands.push(entry);save();
   const fd=fs.openSync(log,'wx');let p;try{p=spawnSync(process.execPath,args,{cwd:root,env:process.env,stdio:['ignore',fd,fd]});}finally{fs.closeSync(fd);}
   Object.assign(entry,{finished_utc:new Date().toISOString(),exit_code:p.status,signal:p.signal,error:p.error?.message??null,log:ref(log)});save();
   must(p.status===0&&!p.error&&!p.signal,'Unrun collector stage failed: '+name);
  }
  for(const [name,tool,target,context]of collectors){
   run(name,['tools/'+tool+'.mjs','--run-dir='+B,...(context?['--context-archive='+B+'/context-archive.json']:[]),'--output=parity/runs/'+target+'.json']);
   const file='parity/runs/'+target+'.json';result.commands.at(-1).output=ref(file);save();
  }
  result.canonical_collectors={complete:true,success:true,run_dir:B,commands:result.commands.slice()};
  fs.writeFileSync(output+'/canonical-collectors.json',JSON.stringify(result.canonical_collectors,null,2)+'\n',{flag:'wx'});
  run('derived-ledger-summary',['tools/record-results.mjs','--run-dir='+B]);
  result.derived_outputs=manifest.managedFiniteDerivedOutputs.map(ref);helpers.validateManagedReferences(result.derived_outputs,manifest.managedFiniteDerivedOutputs,valid);
  for(const c of result.commands.slice(0,14))must(c.output&&valid(c.output),'New canonical output changed');
  const ledger=read('parity/test-ledger.json');must(ledger.tests.length===905&&ledger.tests.filter(t=>t.mapping).length===543,'Ledger credit changed');
  helpers.validateManagedSummary(read('parity/runs/summary.json'),B,ref(B+'/result.json'),{normal:3463,race:3463});
  const after=await audit(root,{afterResume:true});must(equal(after.original_references,prior.original_references),'Original full02 reports/logs changed');
  result.post_resume_audit=after;
  result.canonical_receipt=ref(output+'/canonical-collectors.json');
  result.summary=ref('parity/runs/summary.json');result.original_references=prior.original_references;
  result.native_leaf_cases_per_mode=3463;result.managed_leaf_cases_per_mode=107;
  result.completed_unrun_stages=['canonical-predecessor-collectors','derived-ledger-summary'];
  result.historical_closeout=after.historical_closeout;
  result.success=true;
 }catch(error){result.error=String(error.stack??error);process.exitCode=1;}finally{result.complete=true;result.finished_utc=new Date().toISOString();save();}
 return result;
}
async function main() {
 const arg=n=>process.argv.find(a=>a.startsWith('--'+n+'='))?.slice(n.length+3),root=path.resolve(arg('root')??''),output=path.resolve(arg('output')??'');
 must(root.endsWith('/ponder-restored-20261026/work/ponder-go')&&fs.existsSync(root+'/go.mod'),'Explicit exact repository root required');
 must(output.startsWith('/workspace/scratch/fac5abfa4c9e/ponder-m44-work/')||output.startsWith(root+'/parity/runs/managed-finite-m44/full-02-corrective-'),'New scoped corrective evidence required');
 must(!fs.existsSync(output),'Never overwrite corrective evidence');fs.mkdirSync(output,{recursive:true});
 const toolSnapshot=output+'/corrective-tool.mjs';fs.copyFileSync(fileURLToPath(import.meta.url),toolSnapshot,fs.constants.COPYFILE_EXCL);
 const resumeAudit=arg('resume-approved-audit');
 if(resumeAudit){must(arg('controls'),'Explicit reviewed control receipt required');const result=await resume(root,output,resumeAudit,arg('controls'));console.log(JSON.stringify({resume:output+'/resume.json',success:result.success,error:result.error}));return;}
 let result={complete:false,success:false,scope:'corrective_audit_only',started_utc:new Date().toISOString(),tool:hash(root,fileURLToPath(import.meta.url)),tool_snapshot:hash(root,toolSnapshot)};
 try {Object.assign(result,await audit(root));}catch(error){result.error=String(error.stack??error);process.exitCode=1;}finally{result.complete=true;result.finished_utc=new Date().toISOString();fs.writeFileSync(output+'/audit.json',JSON.stringify(result,null,2)+'\n',{flag:'wx'});console.log(JSON.stringify({audit:output+'/audit.json',success:result.success,error:result.error}));}
}
if(process.argv[1]&&path.resolve(process.argv[1])===fileURLToPath(import.meta.url))await main();
