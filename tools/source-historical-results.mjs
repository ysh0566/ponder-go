// Aggregate actual complete original/native/type runs. Preparation is not acceptance.
import fs from'node:fs';import path from'node:path';import crypto from'node:crypto';
const root=path.resolve(import.meta.dirname,'..'),arg=name=>process.argv.find(a=>a.startsWith('--'+name+'='))?.slice(name.length+3),read=file=>JSON.parse(fs.readFileSync(path.resolve(root,file),'utf8')),hash=file=>({file:path.relative(root,path.resolve(root,file)),sha256:crypto.createHash('sha256').update(fs.readFileSync(path.resolve(root,file))).digest('hex')}),diskValid=file=>file&&fs.existsSync(path.resolve(root,file.file))&&hash(file.file).sha256===file.sha256,check=(condition,message)=>{if(!condition)throw Error(message)};
import {sourceFingerprint} from './go-source-fingerprint.mjs';
import {validateContextEntry} from './loopback-context.mjs';

const runArgument=arg('run-dir'), runDirectory=runArgument?path.resolve(root,runArgument):null;
const adapted=!!runDirectory, archiveArgument=arg('context-archive');
if(adapted&&(arg('normal')||arg('race')||arg('normal-trace')))throw Error('Explicit run directory cannot mix other execution logs');
if(!adapted&&archiveArgument)throw Error('Context archive requires an explicit run directory');
const fullRun=adapted?read(path.join(runDirectory,'result.json')):null;
let contextArchive=null;
const currentRunInputs=[];
if(adapted){
 if(!runDirectory.startsWith(root+'/parity/runs/')||!fullRun.complete||!fullRun.success||fullRun.environment_adapted!==true||fullRun.pristine_file!==false||fullRun.source_manifest_verified_after!==true)throw Error('Complete verified environment-adapted full run required');
 const archivePath=archiveArgument?path.resolve(root,archiveArgument):'';
 if(archivePath!==path.join(runDirectory,'context-archive.json')||!fullRun.context_archive||path.resolve(root,fullRun.context_archive.file)!==archivePath||!diskValid(fullRun.context_archive))throw Error('Exact full-run context archive and hash required');
 contextArchive=read(archivePath);
 if(contextArchive.project_root!==root||contextArchive.evidence_root!==runDirectory||contextArchive.verification_phase!=='before-dispose')throw Error('Context archive is not the completed current run archive');
 currentRunInputs.push(hash(path.join(runDirectory,'result.json')),hash(archivePath),hash('tools/loopback-context.mjs'),hash('tools/go-source-fingerprint.mjs'));
}
const valid=entry=>adapted?validateContextEntry(entry,contextArchive):diskValid(entry);
const normal=adapted?path.join(runDirectory,'native/go-test.jsonl'):(arg('normal')??'parity/runs/go-test.jsonl');
const race=adapted?path.join(runDirectory,'native/go-race.jsonl'):(arg('race')??'parity/runs/go-race.jsonl');
const focused=!adapted&&!!(arg('normal')||arg('race'));
const runManifests={};
if(adapted){
 const inventory=sourceFingerprint(root),fingerprint=inventory.sha256;
 if(fullRun.native_inputs?.sha256!==fingerprint||fullRun.finished_native_inputs?.sha256!==fingerprint)throw Error('Full-run native inputs changed');
 for(const [mode,name,log]of[['normal','go-test',normal],['race','go-race',race]]){
  const file=path.join(runDirectory,'native',name+'-execution.json'), execution=read(file);
  if(!execution.complete||!execution.success||execution.scope!=='all_module_packages'||execution.race!==(mode==='race')||execution.source_fingerprint?.sha256!==fingerprint||execution.finished_source_fingerprint?.sha256!==fingerprint||execution.driver_sha256!==hash('tools/go-tests.mjs').sha256)throw Error('Current unchanged complete module execution required: '+mode);
  const start=Date.parse(execution.started_utc),end=Date.parse(execution.finished_utc);
  if(!Number.isFinite(start)||!Number.isFinite(end)||start>end||start<Date.parse(fullRun.started_utc)||end>Date.parse(fullRun.finished_utc))throw Error('Execution time outside full run');
  const events=fs.readFileSync(log,'utf8').trim().split('\n').map(JSON.parse);
  if(events.some(e=>!Number.isFinite(Date.parse(e.Time))||Date.parse(e.Time)<start-1000||Date.parse(e.Time)>end+1000))throw Error('Log does not belong to execution');
  if(!Array.isArray(execution.packages)||execution.packages.length===0)throw Error('Missing module inventory');
  for(const pkg of execution.packages){
   if(pkg.batches.some(b=>!b.complete||b.exit_code!==0))throw Error('Incomplete module batch');
   const names=pkg.batches.flatMap(b=>b.functions);
   if(new Set(names).size!==pkg.listed_functions.length||names.length!==pkg.listed_functions.length||pkg.listed_functions.some(n=>!names.includes(n)))throw Error('Incomplete module function partition');
   for(const name of pkg.listed_functions)if(events.filter(e=>e.Package===pkg.package&&e.Test===name&&['pass','fail','skip'].includes(e.Action)).length!==1)throw Error('Missing or duplicated listed function result');
  }
  runManifests[mode]=execution;currentRunInputs.push(hash(file),hash(log));
 }
 const preparedFile=path.join(runDirectory,'prepared-native-inputs.json');
 if(!fullRun.prepared_native_inputs||path.resolve(root,fullRun.prepared_native_inputs.file)!==preparedFile||!diskValid(fullRun.prepared_native_inputs))throw Error('Exact run-local prepared input snapshot required');
 const prepared=read(preparedFile);
 if(prepared.fingerprint?.sha256!==fingerprint||prepared.fingerprint.files!==inventory.files||!Array.isArray(prepared.files)||prepared.files.length!==inventory.files||crypto.createHash('sha256').update(JSON.stringify(prepared.files.map(f=>[f.file,f.sha256]))).digest('hex')!==fingerprint||!Number.isFinite(Date.parse(prepared.recorded_utc))||Date.parse(prepared.recorded_utc)<Date.parse(fullRun.started_utc)||fullRun.commands.filter(c=>['abi-generated-normal','abi-generated-race','go-normal','go-race'].includes(c.name)).some(c=>Date.parse(prepared.recorded_utc)>Date.parse(c.started_utc))||['normal','race'].some(mode=>Date.parse(prepared.recorded_utc)>Date.parse(runManifests[mode].started_utc))||!prepared.files.every(diskValid)||!diskValid(prepared.tool))throw Error('Complete native input snapshot must precede both module executions');
 currentRunInputs.push(hash(preparedFile));
}
function validateLiveRun(result,mode,pkg,test){
 if(!adapted)return;
 if(result.environment_adapted!==true||result.pristine_file!==false)throw Error('Current live evidence must explicitly identify adapted startup');
 const batches=runManifests[mode].packages.find(p=>p.package===pkg)?.batches.filter(b=>b.functions.includes(test))??[];
 const start=Date.parse(result.started_utc),end=Date.parse(result.finished_utc);
 if(batches.length!==1||!Number.isFinite(start)||!Number.isFinite(end)||start>end||start<Date.parse(batches[0].started_utc)||!Number.isFinite(batches[0].elapsed_ms)||end>Date.parse(batches[0].started_utc)+batches[0].elapsed_ms+1000)throw Error('Live source execution does not belong to current module batch');
}
const destination=arg('output')??(adapted?path.join(runDirectory,'source-historical-verification.json'):'parity/runs/source-historical-verification.json');
const ledger=read('parity/test-ledger.json'),selected=ledger.tests.filter(t=>t.source_file.includes('/sync-historical/'));
check(selected.length===40&&selected.reduce((n,t)=>n+t.assertions.length,0)===102&&selected.reduce((n,t)=>n+t.type_error_directives.length,0)===12&&selected.every(t=>t.source_state==='active'),'Changed complete historical source inventory');
const pristine=adapted?null:read('parity/runs/source-historical-pristine.json'),types=read('parity/runs/source-historical-types.json'),compile=read('testdata/runtime/source-historical-types.json');
if(!adapted){
check(pristine.complete&&pristine.success&&pristine.executions.length===2&&new Set(pristine.executions.map(e=>e.backend)).size===2,'Separate complete pristine originals required');
for(const e of pristine.executions)check(e.complete&&e.success&&e.counts.passed===40&&e.counts.failed===0&&e.counts.pending===0&&[e.report,e.raw_oracle,...e.source_context].every(valid),'Pristine historical input/context changed');
}
check(types.complete&&types.success&&types.source_error_directives===12&&types.diagnostics===12&&types.executions.length===4,'Actual original historical compiler programs missing');
for(const e of types.executions)check(e.complete&&e.success&&e.exit_code===0&&e.other.length===0&&e.actual.length===(e.mode==='pristine'?0:6)&&[e.report,...e.source_context].every(valid),'Original historical compiler context changed');
check(compile.complete&&compile.success&&compile.directives.length===12&&compile.inputs.every(valid)&&compile.production_inputs.length>100&&compile.production_inputs.every(valid),'Historical native compiler inputs changed');
for(const d of compile.directives){const source=selected.find(t=>t.id===d.source_id);check(source&&source.source_sha256===d.source_sha256&&source.test_source===d.source_body&&JSON.stringify(source.type_error_directives[d.directive_index])===JSON.stringify(d.directive)&&d.status==='runtime_pending'&&d.build_positive.exit_code===0&&d.build_negative.exit_code>0&&d.specific_negative_diagnostic&&[d.positive_source,d.negative_source,d.positive_binary].every(valid),'Specific native type compiler control missing');if(d.role==='return')check(/cannot use[\s\S]*any[\s\S]*SourceLog/.test(d.build_negative.stderr),'Actual method-return rejection missing');else check(/has no field or method (Address|SetAddress)/.test(d.build_negative.stderr),'Actual closed-filter member rejection missing')}
check(new Set(compile.directives.map(d=>d.source_id+'/'+d.directive_index)).size===12,'Duplicate historical compiler controls');
const observerProbe=read('parity/runs/history-m33/observer-identity-proof-01.json'),[oldObserver,currentObserver]=observerProbe.modes;
check(observerProbe.source_credit===0&&observerProbe.sources.every(valid)&&oldObserver.mode==='old_proxy'&&oldObserver.logs_request_count===4&&oldObserver.block_receipt_attempts===2&&!oldObserver.original_rpc_identity&&currentObserver.mode==='original_object'&&currentObserver.logs_request_count===3&&currentObserver.block_receipt_attempts===1&&currentObserver.single_receipt_attempts===2&&currentObserver.original_rpc_identity,'Actual original RPC identity/state control changed');
const executions=[],sourceInputs=[hash('parity/runs/history-m33/observer-identity-proof-01.json'),...observerProbe.sources];
for(const[mode,log]of[['normal',normal],['race',race]]){
 const events=fs.readFileSync(path.resolve(root,log),'utf8').trim().split('\n').map(JSON.parse),pkg='ponder.local/ponder-go/runtime',test='TestUpstreamSourceHistorical',passed=name=>events.some(e=>e.Package===pkg&&e.Test===name&&e.Action==='pass');
 check(passed(test)&&!events.some(e=>e.Package===pkg&&e.Test?.startsWith(test)&&['fail','skip'].includes(e.Action)),'Complete historical native run missing: '+mode);
 if(!focused){for(const[prefix,pkgName]of[['TestSourceHistoricalMergeVectors','runtime'],['TestSourceRequiredIntervalVectors','runtime'],['TestSourceBlockVectors','rpc'],['TestSourceChildAddressVectors','filter'],['TestSourceHistoricalProviderOwnership','runtime'],['TestSourceHistoricalMemoryBound','runtime'],['TestSourceHistoricalPersistentLive','runtime'],['TestSourceHistoricalFactoryDrain','runtime'],['TestSourceHistoricalMemoryLive','runtime']])check(events.some(e=>e.Package==='ponder.local/ponder-go/'+pkgName&&e.Test===prefix&&e.Action==='pass'),'Historical prerequisite/runtime execution missing: '+prefix)}
 for(const backend of['postgres','pglite'])for(const[kind,count]of[['in-memory',23],['index',17]]){
  const prefix=test+'/'+backend+'/'+kind,locations=events.filter(e=>e.Package===pkg&&e.Test===prefix&&e.Output?.includes('Live source historical evidence: '));check(locations.length===1,'Unique historical live path required: '+prefix);const directory=locations[0].Output.split('Live source historical evidence: ')[1].trim();check(directory.startsWith(root+'/parity/runs/history-m33/live-'),'Historical evidence escaped task run directory');
  const native=read(directory+'/native.json'),capture=read(directory+'/calls.json'),original=read(directory+'/original.json'),result=read(directory+'/result.json'),records=selected.filter(t=>t.source_file.endsWith('/'+kind+'.test.ts'));
  validateLiveRun(result,mode,pkg,test);
  check(native.backend===backend&&native.kind===kind&&native.cases.length===count&&native.original_exit_error==='<nil>'&&capture.cases.length===count&&original.success&&original.numPassedTests===count&&original.numFailedTests===0&&original.numPendingTests===0&&result.complete&&result.success&&result.exit_code===0&&result.backend===backend&&result.kind===kind&&result.source_context.every(valid),'Complete historical file/native result missing: '+mode+'/'+backend+'/'+kind);
  let calls=0;const typed=[];
  for(const source of records){
   const c=capture.cases.find(c=>c.id===source.id),n=native.cases.find(c=>c.id===source.id);check(c&&n&&c.source_sha256===source.source_sha256&&n.source_sha256===source.source_sha256&&c.test_source===source.test_source&&c.full_name===source.full_name&&JSON.stringify(c.assertions)===JSON.stringify(source.assertions)&&JSON.stringify(c.type_error_directives)===JSON.stringify(source.type_error_directives)&&c.source_state===source.source_state,'Historical original body/inputs/assertions changed: '+source.id);
   check(passed(prefix+'/'+source.id)&&n.complete&&n.types_done&&n.comparison_backend_closed&&n.errors.length===0&&c.type_errors.length===0&&n.calls.length===c.calls.length&&n.calls.length>0,'Historical call/state/type comparison incomplete: '+source.id);
   const reported=original.testResults.flatMap(f=>f.assertionResults).filter(t=>t.title===source.full_name);check(reported.length===1&&reported[0].status==='passed','Original historical assertion record missing: '+source.id);
   for(let i=0;i<c.calls.length;i++){const call=c.calls[i],run=n.calls[i];check(call.case_id===source.id&&call.sequence===i&&call.operation===run.operation&&Object.hasOwn(call,'args')&&Object.hasOwn(call,'result'),'Historical actual public call sequence incomplete');if(call.operation.startsWith('construct/'))check(call.rpc_identity_preserved,'Original RPC identity was replaced');if(call.operation.startsWith('memory/')||call.operation.startsWith('persistent/'))check(Array.isArray(call.providers)&&run.provider_inputs===call.providers.length&&run.native_requests.length===call.providers.length,'Historical provider-input requests missing');if(call.physical_rows)check(Object.keys(call.physical_rows).length===9&&run.physical_compared,'All nine physical tables must compare')}
   const expected=compile.directives.filter(d=>d.source_id===source.id),runs=n.type_executions??[];check(runs.length===expected.length,'Missing actual historical type program');
   for(const d of expected){const matches=runs.filter(r=>r.source_id===source.id&&r.directive_index===d.directive_index),run=matches[0];check(matches.length===1&&run.passed&&run.backend===backend&&run.native_backend_closed&&run.exit_error==='<nil>'&&run.phase==='after_original_case_comparisons_before_original_setupAnvil_cleanup'&&run.positive_binary_sha256===d.positive_binary.sha256,'Historical positive program did not execute in original case context');const output=JSON.parse(run.stdout);check(output.passed&&output.source_id===source.id&&output.directive_index===d.directive_index&&output.backend===backend&&output.endpoint==='http://127.0.0.1:8545/1'&&Array.isArray(output.outcomes)&&output.outcomes.length===c.phase_inputs.length&&c.callback_inputs.length>0,'Historical actual program/input proof incomplete');if(kind==='index')check(output.namespace.startsWith('ponder_go_hist_type_'),'Actual distinct historical type backend required');else check(output.namespace==='','Memory API must retain its actual no-database behavior');typed.push(run)}
   calls+=n.calls.length;
  }
  check(calls===(kind==='in-memory'?42:94)&&typed.length===6,'Complete historical operations or six type controls missing');executions.push({mode,backend,...(adapted?{environment_adapted:true,pristine_file:false,context_archive:hash(archiveArgument)}:{}),kind,complete:true,original_passed:count,source_assertions:records.reduce((n,t)=>n+t.assertions.length,0),native_calls:calls,typed_controls:typed,log:hash(log),native:hash(directory+'/native.json'),capture:hash(directory+'/calls.json'),requests:hash(directory+'/calls.json.requests.jsonl'),original:hash(directory+'/original.json'),result:hash(directory+'/result.json'),source_context:result.source_context});
 }
}
if(!focused){for(const[file,count]of[['testdata/rpc/source-block.json',44],['testdata/runtime/source-child.json',25],['testdata/runtime/source-historical-merge.json',12],['testdata/runtime/source-required-intervals.json',25]]){const fixture=read(file);check(fixture.cases.length===count&&fixture.sources.every(valid),'Historical supplementary actual-source fixture changed');sourceInputs.push(hash(file),...fixture.sources)}}
const directives=compile.directives.map(d=>({...d,positive:d.positive_source,negative:d.negative_source,status:'passed',execute_positive:{exit_code:0,phase:'Actual matching original case Anvil, after native comparison backend closes and before original cleanup; database-free memory API or actual separate PG/PGlite persistent store.',executions:executions.filter(e=>e.typed_controls.some(r=>r.source_id===d.source_id&&r.directive_index===d.directive_index)).map(e=>({mode:e.mode,backend:e.backend,kind:e.kind,run:e.typed_controls.find(r=>r.source_id===d.source_id&&r.directive_index===d.directive_index)}))}}));check(directives.every(d=>d.execute_positive.executions.length===4),'Four backend/mode executions required for every exact type directive');
const inputs=['tools/source-historical-results.mjs','tools/source-historical-original.mjs','tools/source-historical-live.setup.ts','tools/source-historical-live.config.mjs','tools/source-historical-rpc-observer.mjs','runtime/source_historical_live_test.go','runtime/source_historical_wire_test.go','runtime/source_historical_physical_test.go','runtime/source_historical_types_test.go'].map(hash);const report={baseline:ledger.baseline,complete:true,scope:focused?'focused_source_historical':'complete_module_source_historical',summary:{source_cases:40,source_assertions:102,native_calls_per_backend_mode:136,source_error_directives:12,source_error_directives_passed:12,backend_mode_executions:4,file_backend_mode_executions:8,typed_positive_executions:48,supplementary_source_credit:0},...(adapted?{run_dir:path.relative(root,runDirectory),environment_adapted:true,pristine_file:false,source_credit:0,context_archive:hash(archiveArgument),...(fs.existsSync(root+'/parity/runs/source-historical-pristine.json')?{history_pristine_reference:hash('parity/runs/source-historical-pristine.json')}:{})}:{pristine:hash('parity/runs/source-historical-pristine.json')}),source_types:hash('parity/runs/source-historical-types.json'),compile:hash('testdata/runtime/source-historical-types.json'),executions,directives,inputs:[...currentRunInputs,...inputs,...sourceInputs]};fs.writeFileSync(path.resolve(root,destination),JSON.stringify(report,null,2)+'\n');console.log(report.summary);
