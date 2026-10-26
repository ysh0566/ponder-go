// M44 orchestration with freshly executed M43 regression stages and original
// assertions unchanged; its explicit native inventory adds exactly six M44 files.
// Invoke inside the explicitly authorized M43 PostgreSQL fixture. No old cluster
// is adopted; no accepted run directory or canonical collector pointer is edited
// by this wrapper. The predecessor itself refreshes its usual generated inputs.
import fs from 'node:fs';
import path from 'node:path';
import {spawnSync} from 'node:child_process';
import {managedFiniteHarness,managedFiniteNativeFiles,managedFiniteCredit,managedFiniteDerivedOutputs} from './managed-finite-acceptance.mjs';
import {check,equal,hash,inventory,scopedPath,managedBaselinePath,validateManagedEvents} from './managed-finite-evidence.mjs';
const root=path.resolve(import.meta.dirname,'..');
const arg=n=>process.argv.find(a=>a.startsWith('--'+n+'='))?.slice(n.length+3);
const output=scopedPath(root,arg('output'),'parity/runs/managed-finite-m44');
const baseline=managedBaselinePath(root,arg('baseline'));
check(path.dirname(baseline)===root+'/parity/runs/finite-historical-m43'&&path.basename(baseline).startsWith('m44-regression-'),'New explicitly named M44 regression child required');
check(!fs.existsSync(output)&&!fs.existsSync(baseline),'Never overwrite a prior verification run');
check(path.resolve(process.env.PONDER_M43_POSTGRES_RECEIPT??'')===baseline+'-fixture','Exact predecessor fixture receipt required');
check(process.env.PONDER_M43_POSTGRES_DIRECTORY,'Task-owned fixture directory required');
const retained=path.join(path.dirname(process.env.PONDER_M43_POSTGRES_DIRECTORY),'managed-finite-persistent');
check(!fs.existsSync(retained),'Managed retained fixture root already exists');
fs.mkdirSync(retained);fs.mkdirSync(output,{recursive:true});
const report={complete:false,success:false,scope:'m44_full_test_execution_not_final_collector_acceptance',...managedFiniteCredit,started_utc:new Date().toISOString(),baseline_run:path.relative(root,baseline),baseline_inventory_extension:{accepted_commit:'42783182f418b5dd6abc795f6e3cc8acfec6f409',original_native_files:15,current_native_files:21,added_files:managedFiniteNativeFiles,original_stages:73,original_assertions_unchanged:true,source_credit:0},harness_inputs:managedFiniteHarness.map(f=>hash(root,f)),managed_test_inputs:managedFiniteNativeFiles.map(f=>hash(root,f)),retained_fixture_root:retained,commands:[]};
// Snapshots precede preparation and collector writes; accepted immutable runs
// remain untouched. Mappings are inputs, not derived-output exemptions.
report.managed_harness_snapshots=report.harness_inputs.map(ref=>{
 const snapshot=path.join(output,'prepared-managed-harness',ref.file);
 fs.mkdirSync(path.dirname(snapshot),{recursive:true});fs.copyFileSync(path.join(root,ref.file),snapshot,fs.constants.COPYFILE_EXCL);
 const saved=hash(root,snapshot);check(saved.sha256===ref.sha256,'Managed harness changed while copying');
 return {input:ref,snapshot:saved};
});
report.derived_output_allowlist=managedFiniteDerivedOutputs;
report.previous_derived_outputs=[];
for(const file of managedFiniteDerivedOutputs) {
 const previous=path.join(output,'previous-derived',file);
 if(fs.existsSync(path.join(root,file))) { fs.mkdirSync(path.dirname(previous),{recursive:true}); fs.copyFileSync(path.join(root,file),previous,fs.constants.COPYFILE_EXCL); report.previous_derived_outputs.push({file,before:hash(root,file),snapshot:hash(root,previous)}); }
 else report.previous_derived_outputs.push({file,absent_before:true});
}
const mappingFiles=fs.readdirSync(root+'/parity').filter(f=>f.endsWith('.json')&&(f.includes('mapping')||f.includes('declarations')||f.includes('cases'))).map(f=>'parity/'+f).sort();
report.source_mapping_inputs=mappingFiles.map(f=>hash(root,f));
const save=()=>fs.writeFileSync(output+'/result.json',JSON.stringify(report,null,2)+'\n');save();
function run(name,args,executable=process.execPath) {
 const command=[executable,...args],log=path.join(output,name+'.log');
 const entry={name,command,started_utc:new Date().toISOString()};report.commands.push(entry);save();
 const fd=fs.openSync(log,'wx');let r;
 try{r=spawnSync(executable,args,{cwd:root,env:{...process.env,PYTHONDONTWRITEBYTECODE:'1',PONDER_M44_TEST_ROOT:retained},stdio:['ignore',fd,fd]});}finally{fs.closeSync(fd);}
 Object.assign(entry,{finished_utc:new Date().toISOString(),exit_code:r.status,signal:r.signal,error:r.error?.message??null,log:hash(root,log)});save();
 check(r.status===0&&!r.error,'Stage failed: '+name);
}
try {
 run('preflight',['tools/managed-finite-preflight.mjs','--output='+path.relative(root,output+'/preflight.json')]);
 run('managed-guard-controls',['--test','tools/managed-finite-path-guards.test.mjs','tools/managed-finite-observer.test.mjs']);
 run('observer-identity-controls',['tools/managed-finite-observe.test.py'],'python3');
 run('unchanged-full-regression',['tools/finite-historical-full-verify.mjs','--output='+path.relative(root,baseline)]);
 const full=JSON.parse(fs.readFileSync(baseline+'/result.json'));
 check(full.complete&&full.success&&full.commands.length===73&&full.commands.every(c=>c.exit_code===0),'Full regression must complete every actual predecessor stage');
 report.full_regression=hash(root,baseline+'/result.json');
 report.prepared_native_inputs=full.prepared_native_inputs;
 report.native_inputs=full.native_inputs;
 const tests=inventory(root);report.executions=[];
 for(const mode of ['normal','race']) {
  const stem=mode==='normal'?'go-test':'go-race',file=baseline+'/native/'+stem+'.jsonl';
  const events=fs.readFileSync(file,'utf8').trim().split('\n').map(JSON.parse);
  const manifest=JSON.parse(fs.readFileSync(baseline+'/native/'+stem+'-execution.json'));
  check(manifest.complete&&manifest.success&&manifest.scope==='all_module_packages'&&manifest.race===(mode==='race'),'Actual complete normal/race mode required');
  check(equal(manifest.source_fingerprint,full.native_inputs)&&equal(manifest.finished_source_fingerprint,full.native_inputs),'No mixed managed/native build');
  report.executions.push({mode,log:hash(root,file),manifest:hash(root,baseline+'/native/'+stem+'-execution.json'),tests:validateManagedEvents(events,tests)});
 }
 check(equal(report.executions[0].tests,report.executions[1].tests),'Normal/race managed leaf inventories differ');
 for(const ref of [...report.harness_inputs,...report.managed_test_inputs])check(hash(root,ref.file).sha256===ref.sha256,'Managed inputs changed during execution: '+ref.file);
 if(process.argv.includes('--collect-canonical')) {
  run('canonical-predecessor-collectors',['tools/finite-historical-collect-all.mjs','--run-dir='+path.relative(root,baseline)]);
  run('derived-ledger-summary',['tools/record-results.mjs','--run-dir='+path.relative(root,baseline)]);
  for(const ref of report.source_mapping_inputs)check(hash(root,ref.file).sha256===ref.sha256,'Original mapping/declaration input changed: '+ref.file);
  report.derived_outputs=managedFiniteDerivedOutputs.map(file=>hash(root,file));
  const ledger=JSON.parse(fs.readFileSync(root+'/parity/test-ledger.json'));
  check(ledger.tests.length===905&&ledger.tests.filter(t=>t.mapping).length===543,'Original credit inventory changed');
 }
 report.success=true;
 report.acceptance_pending='Run canonical predecessor collectors under separately authorized evidence preservation, then managed-finite-results. Test execution success is not final milestone acceptance.';
} catch(error) {report.error=String(error.stack??error);process.exitCode=1;} finally {report.complete=true;report.finished_utc=new Date().toISOString();save();console.log(JSON.stringify({output:path.relative(root,output),success:report.success,error:report.error}));}
