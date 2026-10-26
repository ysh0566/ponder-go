// Final M44 supplemental evidence collector. Never grants source-case credit.
import fs from 'node:fs';
import path from 'node:path';
import {spawnSync} from 'node:child_process';
import {sourceFingerprint} from './go-source-fingerprint.mjs';
import {managedFiniteHarness,managedFiniteCredit,managedFiniteDerivedOutputs} from './managed-finite-acceptance.mjs';
import {check,equal,hash,inventory,scopedPath,managedBaselinePath,validateManagedEvents,validateManagedCloseout,validateManagedSnapshotList,validateManagedSummary,validateManagedReferences,validateManagedObserver} from './managed-finite-evidence.mjs';
const root=path.resolve(import.meta.dirname,'..'),arg=n=>process.argv.find(a=>a.startsWith('--'+n+'='))?.slice(n.length+3);
const directory=scopedPath(root,arg('run-dir'),'parity/runs/managed-finite-m44',true);
const destination=scopedPath(root,arg('output'),'parity/runs/managed-finite-m44');
check(!fs.existsSync(destination),'Never overwrite collected evidence');
const read=f=>JSON.parse(fs.readFileSync(path.resolve(root,f)));
const valid=r=>r&&typeof r.file==='string'&&hash(root,r.file).sha256===r.sha256;
const run=read(directory+'/result.json'),baseline=managedBaselinePath(root,run.baseline_run,true),full=read(baseline+'/result.json');
check(path.dirname(baseline)===root+'/parity/runs/finite-historical-m43'&&path.basename(baseline).startsWith('m44-regression-'),'Exact new M44 regression child required');
check(equal(run.commands?.map(c=>c.name),['preflight','managed-guard-controls','observer-identity-controls','unchanged-full-regression','canonical-predecessor-collectors','derived-ledger-summary'])&&run.commands.every(c=>c.exit_code===0&&valid(c.log)),'Every exact M44 orchestration stage must pass');
check(run.complete&&run.success&&full.complete&&full.success&&full.commands.length===73&&full.commands.every(c=>c.exit_code===0),'Complete M44 test execution and original regression required');
check(valid(run.full_regression)&&run.full_regression.file===path.relative(root,baseline+'/result.json'),'Exact linked regression proof required');
check(run.source_credit===0&&run.ported_original===543&&run.original_total===905,'No invented original-source credit');
check(equal(run.derived_output_allowlist,managedFiniteDerivedOutputs),'Exact predeclared derived output allowance required');
validateManagedSnapshotList(run.previous_derived_outputs,managedFiniteDerivedOutputs);
for(const entry of run.previous_derived_outputs) {
 check(managedFiniteDerivedOutputs.includes(entry.file),'Unexpected derived-output exemption');
 if(!entry.absent_before)check(valid(entry.snapshot)&&entry.before.file===entry.file&&entry.before.sha256===entry.snapshot.sha256,'Prior derived evidence snapshot changed');
}
check(run.source_mapping_inputs?.length>0&&run.source_mapping_inputs.every(valid),'Original mapping/declaration/case files changed');
check(equal(run.derived_outputs?.map(r=>r.file),managedFiniteDerivedOutputs)&&run.derived_outputs.every(valid),'Canonical collection and summary not completed for this wrapper');
const ledger=read('parity/test-ledger.json');
check(ledger.tests.length===905&&ledger.tests.filter(t=>t.mapping).length===543,'Original source accounting changed');

check(run.managed_harness_snapshots?.length===managedFiniteHarness.length&&run.managed_harness_snapshots.every(r=>valid(r.input)&&valid(r.snapshot)&&r.input.sha256===r.snapshot.sha256),'Physical managed harness snapshots differ');
check(run.harness_inputs.every(valid)&&equal(run.harness_inputs.map(r=>r.file),managedFiniteHarness)&&run.managed_test_inputs.every(valid),'Current M44 harness/test bytes required');
const fingerprint=sourceFingerprint(root),prepared=read(full.prepared_native_inputs.file);
check(valid(full.prepared_native_inputs)&&equal(full.native_inputs,fingerprint)&&equal(full.finished_native_inputs,fingerprint)&&equal(prepared.fingerprint,fingerprint)&&prepared.files.every(valid),'Fresh identical prepared/native sources required');
// The immutable prepared archive remains hash-verified. Only the exact approved
// derived outputs may differ after collectors run; all source/harness/type input
// bytes remain checked. This classification is fixed before launch, not a
// post-failure blanket exclusion of inconvenient frozen files.
const frozen=full.prepared_input_archive;
check(frozen?.complete&&frozen.success&&frozen.live_inputs_verified_after_native&&valid(frozen.archive)&&valid(frozen.manifest),'Actual immutable prepared archive required');
const physical=read(frozen.manifest.file),derivedAfterPreparation=[];
for(const entry of physical.entries) {
 if(managedFiniteDerivedOutputs.includes(entry.file)) {
  check(run.derived_outputs.some(ref=>ref.file===entry.file&&valid(ref)),'Derived frozen entry lacks exact current output receipt');
  derivedAfterPreparation.push({file:entry.file,prepared_sha256:entry.sha256,current_sha256:hash(root,entry.file).sha256});
 } else check(valid(entry),'Immutable prepared input changed after collection: '+entry.file);
}
const canonical=read(baseline+'/canonical-collectors.json');
check(canonical.complete&&canonical.success&&canonical.run_dir===path.relative(root,baseline)&&canonical.commands.length===14&&canonical.commands.every(c=>c.exit_code===0&&valid(c.output)),'All 14 fresh predecessor collectors required; never reuse accepted old proofs');
validateManagedReferences(canonical.commands.map(c=>c.output),managedFiniteDerivedOutputs.slice(0,14),valid);
const finite=canonical.commands.find(c=>c.name==='finite-historical');check(finite,'Finite historical collector required');
const prior=read(finite.output.file);check(prior.complete&&prior.success&&prior.run_dir===run.baseline_run&&prior.source_credit===0&&valid(prior.full_verification)&&prior.full_verification.sha256===run.full_regression.sha256,'Exact current predecessor proof required');
const closeout=read(directory+'/closeout.json');validateManagedCloseout(closeout);
check(closeout.run_dir===path.relative(root,directory)&&equal(closeout.wrapper,hash(root,directory+'/result.json'))&&equal(closeout.baseline,hash(root,baseline+'/result.json'))&&[closeout.launch,closeout.join,...closeout.fixture_inputs].every(valid),'Missing/stale closeout input references');
validateManagedReferences(closeout.fixture_inputs,['owned-directory.txt','show-data-directory.txt','initial-inventory.json','final-inventory.json','shutdown.log','exit-code.txt'].map(f=>run.baseline_run+'-fixture/'+f),valid);
check(Date.parse(closeout.observed_utc)>=Date.parse(run.finished_utc),'Closeout predates completed wrapper');
// A valid old receipt cannot make an actively restarted fixture ready to accept.
const currentPG=spawnSync('pg_ctl',['-D',full.postgres_fixture.directory,'status'],{encoding:'utf8'}),currentPS=spawnSync('ps',['-eo','pgid='],{encoding:'utf8'});
check(currentPG.status===3&&!currentPG.error&&!fs.existsSync(full.postgres_fixture.directory+'/postmaster.pid')&&currentPS.status===0&&!currentPS.error&&!currentPS.stdout.trim().split(/\s+/).map(Number).includes(closeout.launch_pgid),'Fixture/group active or unobservable at final collection');
const finalObservation={observed_utc:new Date().toISOString(),postgres_exit_code:currentPG.status,launch_group_empty:true};
const observerFile=directory+'-observer/result.json',observer=read(observerFile);validateManagedObserver(observer);
check(observer.run_dir===path.relative(root,directory)&&equal(observer.wrapper,hash(root,directory+'/result.json'))&&equal(observer.closeout,hash(root,directory+'/closeout.json'))&&equal(observer.launch,closeout.launch)&&equal(observer.observer,hash(root,'tools/managed-finite-observe.py')),'Exact current observer/wrapper/closeout/launcher linkage required');
check(hash(root,observerFile).sha256===hash(root,directory+'/outer-closeout.json').sha256,'Canonical observer and outer-closeout copies differ');
check(Date.parse(observer.started_utc)<=Date.parse(run.started_utc)&&Date.parse(observer.finished_utc)>=Date.parse(closeout.observed_utc),'Observer does not cover this completed run');
validateManagedReferences(observer.inputs,['admission.json','root-identity.json','timeline.jsonl','launch.log'].map(f=>path.relative(root,directory+'-observer/'+f)),valid);
const admission=read(directory+'-observer/admission.json'),rootIdentity=read(directory+'-observer/root-identity.json');
check(admission.format===observer.format&&admission.started_utc===observer.started_utc&&admission.run_dir===observer.run_dir&&equal(admission.observer,observer.observer)&&equal(rootIdentity.root_identity,observer.root_identity)&&equal(rootIdentity.command,observer.command)&&equal(admission.command,observer.command),'Observer admission/root identity receipts differ');
check(equal(observer.command,['node','tools/managed-finite-launch.mjs','--output='+directory,'--baseline='+baseline]),'Exact outer observed command required');
const timeline=fs.readFileSync(directory+'-observer/timeline.jsonl','utf8').trim().split('\n').map(JSON.parse);
check(timeline.length>0&&timeline.every(row=>Number.isFinite(Date.parse(row.utc))&&Date.parse(row.utc)>=Date.parse(observer.started_utc)&&Date.parse(row.utc)<=Date.parse(observer.finished_utc)&&Number.isSafeInteger(row.free_bytes)&&row.free_bytes>=observer.capacity.floor_bytes&&observer.capacity.initial_free_bytes-row.free_bytes<=observer.capacity.budget_bytes&&Array.isArray(row.processes)),'Observer timeline lost floor/budget or chronology');
const liveGroups=currentPS.stdout.trim().split(/\s+/).map(Number),observedGroups=[...new Set(observer.observed.map(x=>x.pgid))];
check(!observedGroups.some(g=>liveGroups.includes(g)),'Previously observed task group is currently active');
for(const identity of observer.observed) {
 try {const raw=fs.readFileSync('/proc/'+identity.pid+'/stat','utf8'),ticks=raw.slice(raw.lastIndexOf(')')+2).split(' ')[19];check(ticks!==identity.start_ticks,'Previously observed task process is still alive');}
 catch(error){if(error.code!=='ENOENT')throw error;}
}
finalObservation.observed_task_groups_empty=true;


const tests=inventory(root),executions=[],counts={};
for(const mode of ['normal','race']) {
 const stem=mode==='normal'?'go-test':'go-race',file=baseline+'/native/'+stem+'.jsonl',manifestFile=baseline+'/native/'+stem+'-execution.json',manifest=read(manifestFile);
 check(manifest.complete&&manifest.success&&manifest.scope==='all_module_packages'&&manifest.race===(mode==='race')&&manifest.driver_sha256===hash(root,'tools/go-tests.mjs').sha256,'Actual complete current full native mode required');
 check(equal(manifest.source_fingerprint,fingerprint)&&equal(manifest.finished_source_fingerprint,fingerprint),'Native mode fingerprint differs');
 const events=fs.readFileSync(file,'utf8').trim().split('\n').map(JSON.parse),start=Date.parse(manifest.started_utc),end=Date.parse(manifest.finished_utc);
 check(Number.isFinite(start)&&Number.isFinite(end)&&start>=Date.parse(prepared.recorded_utc)&&end<=Date.parse(full.finished_utc),'Execution outside prepared full run');
 check(events.every(e=>Number.isFinite(Date.parse(e.Time))&&Date.parse(e.Time)>=start-1000&&Date.parse(e.Time)<=end+1000),'Event timestamps outside actual run');
 for(const pkg of manifest.packages)for(const name of pkg.listed_functions)check(events.filter(e=>e.Package===pkg.package&&e.Test===name&&e.Action==='pass').length===1,'Missing/duplicate listed function '+name);
 const allNames=[...new Set(events.filter(e=>e.Test&&e.Action==='run').map(e=>e.Package+'\0'+e.Test))];
 counts[mode]=allNames.filter(n=>!allNames.some(other=>other.startsWith(n+'/'))).length;
 executions.push({mode,log:hash(root,file),manifest:hash(root,manifestFile),tests:validateManagedEvents(events,tests)});
}
check(equal(executions[0].tests,executions[1].tests),'Normal/race managed leaves differ');
validateManagedSummary(read('parity/runs/summary.json'),run.baseline_run,run.full_regression,counts);
const report={complete:true,success:true,scope:'managed_finite_default_multichain_durable_readiness',...managedFiniteCredit,recorded_utc:new Date().toISOString(),run_dir:path.relative(root,directory),full_execution:hash(root,directory+'/result.json'),closeout:hash(root,directory+'/closeout.json'),final_shutdown_observation:finalObservation,summary:hash(root,'parity/runs/summary.json'),outer_observer:hash(root,observerFile),original_regression:hash(root,baseline+'/result.json'),canonical_collectors:hash(root,baseline+'/canonical-collectors.json'),previous_finite_proof:finite.output,prepared_native_inputs:full.prepared_native_inputs,native_inputs:fingerprint,harness_inputs:run.harness_inputs,derived_output_classification:{allowlist:managedFiniteDerivedOutputs,changed_since_preparation:derivedAfterPreparation},executions,limitations:{live_runtime:false,source_equivalence:'Supplemental tests only; no new original-case/assertion/type credit.',pglite:'Actual persistent filesystem/process exclusion, not source stale-heartbeat takeover parity.',processes:'Only actual sampled task identities/groups are proven drained; no exhaustive unobserved-fork claim.',pid:'backend_start mismatch simulation is not actual OS PID reuse.'}};
fs.mkdirSync(path.dirname(destination),{recursive:true});fs.writeFileSync(destination,JSON.stringify(report,null,2)+'\n',{flag:'wx'});console.log(JSON.stringify({output:path.relative(root,destination),source_credit:0,managed_leaf_cases_per_mode:executions[0].tests.reduce((n,t)=>n+t.leaves.length,0)}));
