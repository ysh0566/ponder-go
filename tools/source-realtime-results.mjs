// Aggregate complete original/native runs and independent source controls.
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
const destination=arg('output')??(adapted?path.join(runDirectory,'source-realtime-verification.json'):'parity/runs/source-realtime-verification.json');
const ledger=read('parity/test-ledger.json'),selected=ledger.tests.filter(t=>t.source_file==='packages/core/src/sync-realtime/index.test.ts');
const normalTrace=arg('normal-trace');
check(selected.length===30&&selected.reduce((n,t)=>n+t.assertions.length,0)===203&&selected.every(t=>t.source_state==='active'&&t.type_error_directives.length===0),'Changed complete realtime source inventory');
const pristine=adapted?null:read('parity/runs/source-realtime-pristine.json');if(!adapted){
check(pristine.complete&&pristine.success&&pristine.executions.length===2&&new Set(pristine.executions.map(e=>e.backend)).size===2,'Separate complete pristine realtime originals required');
for(const e of pristine.executions)check(e.complete&&e.success&&e.counts.passed===30&&e.counts.failed===0&&e.counts.pending===0&&[e.report,e.raw_oracle,...e.source_context].every(valid),'Pristine realtime source/context changed');
}
const probe=read('parity/runs/realtime-m34/observer-identity-proof.json');check(probe.passed&&probe.original_rpc_identity&&probe.learned_log_requests_before_extra_controls===3&&probe.later_mock_captured_once&&probe.later_spy_call_count===3&&probe.concurrent_captures===2&&probe.block_receipt_attempts===1&&probe.single_receipts===2&&probe.total_captures===10&&probe.source_credit===0&&probe.sources.every(valid),'Actual source RPC metadata/later spy/concurrent observer control changed');
const trace=read('testdata/rpc/source-trace-identity.json'),controls=read('testdata/runtime/source-realtime-controls.json');check(trace.cases.length===12&&trace.cases.filter(c=>c.error).length===4&&trace.sources.every(valid)&&controls.cases.length===12&&controls.sources.every(valid)&&controls.source_credit===0,'Supplementary actual-source controls changed');
const oldEvents=fs.readFileSync(path.join(root,'parity/runs/realtime-m34/trace-old-01.jsonl'),'utf8').trim().split('\n').map(JSON.parse),oldLeaves=oldEvents.filter(e=>e.Test?.startsWith('TestSourceTraceIdentityVectors/')&&['pass','fail','skip'].includes(e.Action));check(oldLeaves.length===12&&oldLeaves.filter(e=>e.Action==='fail').length===10&&oldLeaves.filter(e=>e.Action==='pass').length===2,'Actual old-copy-helper identity red control missing');
const executions=[];
for(const[mode,log]of[['normal',normal],['race',race]]){
 const events=fs.readFileSync(path.resolve(root,log),'utf8').trim().split('\n').map(JSON.parse),pkg='ponder.local/ponder-go/runtime',test='TestUpstreamSourceRealtime',passed=name=>events.some(e=>e.Package===pkg&&e.Test===name&&e.Action==='pass');
 check(passed(test)&&!events.some(e=>e.Package===pkg&&e.Test?.startsWith(test)&&['fail','skip'].includes(e.Action)),'Complete realtime native run missing: '+mode);
 const traceEvents=mode==='normal'&&normalTrace?fs.readFileSync(path.resolve(root,normalTrace),'utf8').trim().split('\n').map(JSON.parse):events;
 for(const[prefix,count,stream]of[['TestSourceRealtimeControls',12,events],['TestSourceTraceIdentityVectors',12,traceEvents]]){const leaves=stream.filter(e=>e.Package===pkg&&e.Test?.startsWith(prefix+'/')&&['pass','fail','skip'].includes(e.Action));check(leaves.length===count&&new Set(leaves.map(e=>e.Test)).size===count&&leaves.every(e=>e.Action==='pass'),'Actual same-input supplementary native execution missing: '+mode+'/'+prefix)}
 for(const backend of['postgres','pglite']){
  const prefix=test+'/'+backend,locations=events.filter(e=>e.Package===pkg&&e.Test===prefix&&e.Output?.includes('Live source realtime evidence: '));check(locations.length===1,'Unique realtime live path required: '+prefix);const directory=locations[0].Output.split('Live source realtime evidence: ')[1].trim();check(directory.startsWith(root+'/parity/runs/realtime-m34/live-'),'Realtime evidence escaped task run directory');
  const native=read(directory+'/native.json'),capture=read(directory+'/calls.json'),original=read(directory+'/original.json'),result=read(directory+'/result.json');
  validateLiveRun(result,mode,pkg,test);
  check(native.backend===backend&&native.cases.length===30&&native.original_exit_error==='<nil>'&&capture.cases.length===30&&original.success&&original.numPassedTests===30&&original.numFailedTests===0&&original.numPendingTests===0&&result.complete&&result.success&&result.exit_code===0&&result.backend===backend&&result.source_context.every(valid),'Complete realtime file/native result missing: '+mode+'/'+backend);
  let calls=0,providers=0,external=0,warnings=0;const operations={};
  for(const source of selected){
   const c=capture.cases.find(c=>c.id===source.id),n=native.cases.find(c=>c.id===source.id);check(c&&n&&c.source_sha256===source.source_sha256&&n.source_sha256===source.source_sha256&&c.test_source===source.test_source&&c.full_name===source.full_name&&JSON.stringify(c.assertions)===JSON.stringify(source.assertions)&&JSON.stringify(c.type_error_directives)===JSON.stringify(source.type_error_directives)&&c.source_state===source.source_state,'Realtime original body/inputs/assertions changed: '+source.id);
   check(passed(prefix+'/'+source.id)&&n.complete&&n.actual_memory_api_no_database&&n.errors.length===0&&c.errors.length===0&&n.calls.length===c.calls.length&&n.calls.length>0,'Realtime independent call/state comparison incomplete: '+source.id);
   const reports=original.testResults.flatMap(f=>f.assertionResults).filter(t=>t.title===source.full_name);check(reports.length===1&&reports[0].status==='passed','Original realtime assertion record missing: '+source.id);
   for(let i=0;i<c.calls.length;i++){const call=c.calls[i],run=n.calls[i];check(call.case_id===source.id&&call.sequence===i&&run.sequence===i&&call.operation===run.operation&&Object.hasOwn(call,'args')&&Object.hasOwn(call,'result')&&Object.hasOwn(call,'after')&&run.actual_memory_api_no_database,'Realtime actual public call sequence incomplete');
    check(Array.isArray(call.providers)&&run.provider_inputs===call.providers.length&&run.native_requests.length===call.providers.length&&Array.isArray(call.warnings)&&run.warnings===call.warnings.length,'Realtime actual provider/warnings input coverage missing');
    if(call.operation==='construct')check(call.rpc_identity_preserved&&call.providers.length===0,'Original realtime constructor RPC identity changed');
    else if(call.operation==='sync')check(call.consumer_steps.length>0&&call.consumer_steps.every(s=>['next','return'].includes(s.operation)&&Array.isArray(s.args)&&s.args.length===0)&&typeof call.drained==='boolean','Original actual consumer operations/arguments must be supported exactly');
    else check(call.operation==='onError','Unknown original realtime operation');calls++;providers+=call.providers.length;warnings+=call.warnings.length;operations[call.operation]=(operations[call.operation]??0)+1;
   }
   check(Array.isArray(c.external_provider_inputs),'Original test preparation RPCs must remain separately recorded');for(const p of c.external_provider_inputs)check(p.request&&Object.hasOwn(p,'response')&&!p.error,'Original external provider preparation input missing');external+=c.external_provider_inputs.length;
  }
  check(calls===83&&operations.construct===34&&operations.sync===49&&providers===46&&external===3,'Complete realtime public/provider/preparation operation count changed');executions.push({mode,backend,...(adapted?{environment_adapted:true,pristine_file:false,context_archive:hash(archiveArgument)}:{}),complete:true,original_passed:30,source_assertions:203,native_calls:calls,raw_provider_inputs:providers,original_external_preparation_requests:external,parent_warnings_compared:warnings,operations,native_memory_api_no_database:true,log:hash(log),...(mode==='normal'&&normalTrace?{supplementary_trace_log:hash(normalTrace)}:{}),native:hash(directory+'/native.json'),capture:hash(directory+'/calls.json'),requests:hash(directory+'/calls.json.requests.jsonl'),original:hash(directory+'/original.json'),result:hash(directory+'/result.json'),source_context:result.source_context});
 }
}
const files=['tools/source-realtime-results.mjs','tools/source-realtime-original.mjs','tools/source-realtime-live.setup.ts','tools/source-realtime-live.config.mjs','tools/source-realtime-rpc-observer.mjs','tools/source-realtime-observer-probe.mjs','tools/source-realtime-pristine.mjs','tools/source-realtime-module-loader.mjs','tools/source-realtime-controls.mjs','tools/source-trace-fixtures.mjs','rpc/source_call_traces.go','runtime/source_realtime_state.go','runtime/source_realtime_fetch.go','runtime/source_realtime_live_test.go','runtime/source_realtime_controls_test.go','runtime/source_trace_identity_test.go','runtime/source_historical_wire_test.go','testdata/rpc/source-trace-identity.json','testdata/runtime/source-realtime-controls.json','parity/runs/realtime-m34/observer-identity-proof.json','parity/runs/realtime-m34/trace-old-01.jsonl'];
const report={baseline:ledger.baseline,complete:true,scope:focused?'focused_source_realtime':'complete_module_source_realtime',summary:{source_cases:30,source_assertions:203,source_error_directives:0,backend_mode_executions:4,native_calls_per_backend_mode:83,raw_provider_inputs_per_backend_mode:46,original_preparation_requests_per_backend_mode:3,supplementary_source_vectors:24,supplementary_source_credit:0,native_memory_api_no_database:true},...(adapted?{run_dir:path.relative(root,runDirectory),environment_adapted:true,pristine_file:false,source_credit:0,context_archive:hash(archiveArgument),...(fs.existsSync(root+'/parity/runs/source-realtime-pristine.json')?{history_pristine_reference:hash('parity/runs/source-realtime-pristine.json')}:{})}:{pristine:hash('parity/runs/source-realtime-pristine.json')}),executions,inputs:[...currentRunInputs,...files.map(hash),...probe.sources,...trace.sources,...controls.sources]};fs.writeFileSync(path.resolve(root,destination),JSON.stringify(report,null,2)+'\n');console.log(report.summary);
