// Accept only actual source/native executions from both supplied Go logs.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";

const root=path.resolve(import.meta.dirname,'..'),sourceFile='packages/core/src/sync-store/index.test.ts';
const arg=name=>process.argv.find(a=>a.startsWith('--'+name+'='))?.slice(name.length+3),argument=arg;
const read=file=>JSON.parse(fs.readFileSync(path.resolve(root,file),'utf8'));
const hash=file=>({file:path.relative(root,path.resolve(root,file)),sha256:crypto.createHash('sha256').update(fs.readFileSync(path.resolve(root,file))).digest('hex')});
const diskValid=entry=>entry&&fs.existsSync(path.resolve(root,entry.file))&&hash(entry.file).sha256===entry.sha256;
const requireCondition=(condition,message)=>{if(!condition)throw Error(message)};
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
const destination=arg('output')??(adapted?path.join(runDirectory,'source-sync-verification.json'):'parity/runs/source-sync-verification.json');
const ledger=read("parity/test-ledger.json"), selected=ledger.tests.filter(t=>t.source_file===sourceFile);
requireCondition(selected.length===38&&selected.every(t=>t.source_state==="active")&&selected.reduce((n,t)=>n+t.assertions.length,0)===63,"Changed complete source declaration inventory");
const pristine=adapted?null:read("parity/runs/source-sync-pristine.json"), types=read("parity/runs/source-sync-types.json"), compile=read("parity/runs/sync-store-m32/type-compile.json");
if(!adapted){
requireCondition(pristine.complete&&pristine.success&&pristine.executions.length===2&&new Set(pristine.executions.map(e=>e.backend)).size===2,"Both pristine original backends required");
for(const e of pristine.executions)requireCondition(e.complete&&e.success&&e.counts.passed===38&&e.counts.failed===0&&e.counts.pending===0&&[e.report,e.raw_oracle,...e.source_context].every(valid),"Stale/incomplete pristine source evidence");
}
requireCondition(types.complete&&types.success&&types.pristine.length===0&&types.stripped.length===9&&types.stripped.every(d=>d.code===2322&&d.source_id)&&types.source_context.every(valid),"Actual source compiler obligations missing");
requireCondition(compile.directives.length===9&&compile.inputs.every(valid)&&compile.source_context.every(valid),"Stale native compiler context");
for(const d of compile.directives){
 const source=selected.find(t=>t.id===d.source_id);
 requireCondition(source&&d.source_sha256===source.source_sha256&&d.source_error_directive===source.type_error_directives[d.source_directive_index]&&d.status==="compiled_runtime_pending"&&d.build_positive.exit_code===0&&d.build_negative.exit_code>0&&/cannot use \[\]string.*as rpc\.SourceCallParameters value/.test(d.build_negative.stderr)&&[d.positive,d.negative].every(valid)&&hash(d.positive_binary).sha256===d.positive_binary_sha256,"Invalid actual request type control");
}
requireCondition(new Set(compile.directives.map(d=>`${d.source_id}/${d.source_directive_index}`)).size===9,"Repeated native type control");
const executions=[];
const vectorInputs=[];
if(!focused){
 for(const [file,count] of [["testdata/rpc/source-cache.json",40],["testdata/rpc/source-range.json",12]]){
  const fixture=read(file);
  requireCondition(fixture.cases.length===count&&fixture.sources.every(valid),"Actual source vector context changed");
  vectorInputs.push(hash(file),...fixture.sources);
 }
}
for(const [mode,log] of [["normal",normal],["race",race]]){
 const lines=fs.readFileSync(path.resolve(root,log),"utf8").trim().split("\n").map(JSON.parse);
 const packageName="ponder.local/ponder-go/store", test="TestUpstreamSourceSyncStore";
 const passed=name=>lines.some(x=>x.Package===packageName&&x.Test===name&&x.Action==="pass");
 requireCondition(passed(test)&&!lines.some(x=>x.Package===packageName&&x.Test?.startsWith(test)&&["fail","skip"].includes(x.Action)),"Missing successful complete source native run: "+mode);
 if(!focused)requireCondition(passed("TestSourceRPCVectors")&&passed("TestSourceFilterBlockRangeVectors")&&passed("TestSourceRPCBoundaries"),"Supplementary native source vector/boundary execution missing");
 for(const backend of ["postgres","pglite"]){
  const locations=lines.filter(x=>x.Package===packageName&&x.Test===`${test}/${backend}`&&x.Output?.includes("Live source sync evidence: "));
  requireCondition(locations.length===1,"Expected unique actual native evidence path: "+mode+"/"+backend);
  const directory=locations[0].Output.split("Live source sync evidence: ")[1].trim();
  requireCondition(directory.startsWith(path.join(root,"parity/runs/sync-store-m32/live-")),"Unexpected native evidence path");
  const native=read(directory+"/native.json"), capture=read(directory+"/calls.json"), original=read(directory+"/original.json"), result=read(directory+"/result.json");
  validateLiveRun(result,mode,packageName,test);
  requireCondition(native.backend===backend&&native.native_backend_closed_before_types&&native.types_finished&&native.type_execution_phase==="after_all_original_comparisons_before_original_anvil_teardown"&&native.original_exit_error==="<nil>"&&native.cases.length===38&&capture.cases.length===38&&original.success&&original.numPassedTests===38&&original.numFailedTests===0&&original.numPendingTests===0&&result.backend===backend&&result.complete&&result.success&&result.exit_code===0&&result.source_context.every(valid),"Incomplete source/native execution: "+mode+"/"+backend);
  let callCount=0, seedCount=0;const typed=[];
  for(const source of selected){
   const c=capture.cases.find(c=>c.id===source.id), n=native.cases.find(c=>c.id===source.id);
   requireCondition(c&&n&&c.source_sha256===source.source_sha256&&n.source_sha256===source.source_sha256&&c.test_source===source.test_source&&c.title===source.title&&JSON.stringify(c.assertions)===JSON.stringify(source.assertions)&&JSON.stringify(c.type_error_directives)===JSON.stringify(source.type_error_directives)&&c.source_state===source.source_state,"Original inputs/body/assertions changed: "+source.id);
   requireCondition(passed(`${test}/${backend}/${source.id}`)&&n.complete&&n.errors.length===0&&n.calls===c.calls.length&&n.calls>0&&n.setup_seeds===c.seeds.length&&n.sequence===c.sequence,"Incomplete native method/state comparison: "+source.id);
   requireCondition(c.calls.every(call=>call.case_id===source.id&&Object.hasOwn(call,"args")&&Object.hasOwn(call,"result")&&(call.operation==="getFilterBlockRange"||Object.keys(call.physical_rows??{}).length===9)),"Missing genuine method/physical state snapshots");
   const sourceResult=original.testResults.flatMap(f=>f.assertionResults).filter(t=>t.title===source.title);
   requireCondition(sourceResult.length===1&&sourceResult[0].status==="passed","Original declaration assertions missing: "+source.id);
   const controls=compile.directives.filter(d=>d.source_id===source.id), runs=n.native_type_executions??[];
   requireCondition(runs.length===controls.length,"Missing actual typed RPC/storage execution: "+source.id);
   for(const d of controls){
    const matches=runs.filter(r=>r.source_id===source.id&&r.directive_index===d.source_directive_index), run=matches[0];
    requireCondition(matches.length===1&&run.passed&&run.phase==="after_all_original_comparisons"&&run.native_backend_closed&&run.backend===backend&&run.exit_error==="<nil>"&&run.positive_binary_sha256===d.positive_binary_sha256,"Failed actual typed control: "+source.id);
    const value=JSON.parse(run.stdout);
    requireCondition(value.passed&&value.source_id===source.id&&value.directive_index===d.source_directive_index&&value.rpc_result==="0x","Typed positive did not execute actual RPC");
    typed.push(run);
   }
   callCount+=n.calls;seedCount+=n.setup_seeds;
  }
  requireCondition(callCount===95&&typed.length===9,"Incomplete full source operations or type execution");
  executions.push({mode,backend,...(adapted?{environment_adapted:true,pristine_file:false,context_archive:hash(archiveArgument)}:{}),complete:true,original_passed:38,original_assertions:63,native_calls:callCount,setup_dml:seedCount,typed_controls:typed,log:hash(log),native:hash(directory+"/native.json"),capture:hash(directory+"/calls.json"),original:hash(directory+"/original.json"),result:hash(directory+"/result.json"),source_context:result.source_context});
 }
}
const directives=compile.directives.map(d=>({...d,status:"passed",execute_positive:{exit_code:0,method:"Actual original live Anvil, source-shaped native RPC/storage/pruning program, both backends in normal and race Go runs",executions:executions.map(e=>({mode:e.mode,backend:e.backend,run:e.typed_controls.find(r=>r.source_id===d.source_id&&r.directive_index===d.source_directive_index)}))}}));
const report={baseline:ledger.baseline,complete:true,scope:focused?"focused_source_sync":"complete_module_source_sync",summary:{source_cases:38,source_assertions:63,native_calls_per_backend_mode:95,source_error_directives:9,source_error_directives_passed:9,backend_mode_executions:4,supplementary_source_credit:0},...(adapted?{run_dir:path.relative(root,runDirectory),environment_adapted:true,pristine_file:false,source_credit:0,context_archive:hash(archiveArgument),...(fs.existsSync(root+'/parity/runs/source-sync-pristine.json')?{history_pristine_reference:hash('parity/runs/source-sync-pristine.json')}:{})}:{pristine:hash("parity/runs/source-sync-pristine.json")}),source_types:hash("parity/runs/source-sync-types.json"),compile:hash("parity/runs/sync-store-m32/type-compile.json"),executions,directives,inputs:[...currentRunInputs,...vectorInputs,...["tools/source-sync-results.mjs","tools/source-sync-live.setup.ts","tools/source-sync-live.config.mjs","store/source_sync_live_test.go","store/source_sync_calls_test.go","store/source_sync_wire_test.go","store/source_sync_sql_test.go","store/source_sync_types_test.go"].map(hash)]};
fs.writeFileSync(path.resolve(root,destination),JSON.stringify(report,null,2)+"\n");console.log(report.summary);
