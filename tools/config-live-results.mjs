// Validate actual executions in both complete Go logs; never substitute a
// previously captured Go result for running the native source builder.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
const root=path.resolve(import.meta.dirname,'..'),arg=name=>process.argv.find(a=>a.startsWith('--'+name+'='))?.slice(name.length+3),read=file=>JSON.parse(fs.readFileSync(path.resolve(root,file),'utf8'));
const hash=file=>({file:path.relative(root,path.resolve(root,file)),sha256:crypto.createHash('sha256').update(fs.readFileSync(path.resolve(root,file))).digest('hex')});
const diskValid=entry=>entry&&fs.existsSync(path.resolve(root,entry.file))&&hash(entry.file).sha256===entry.sha256;
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
const validHash=valid;
const destination=arg('output')??(adapted?path.join(runDirectory,'config-live-verification.json'):'parity/runs/config-live-verification.json');
const ledger=read("parity/test-ledger.json"),selected=ledger.tests.filter(t=>t.source_file==="packages/core/src/build/config.test.ts");
if(selected.length!==40)throw Error("Expected 40 complete expanded source records");
const executions=[];
const typedModes=[];
for(const mode of ["go-test","go-race"]){
 const log=mode==="go-test"?normal:race;
 const lines=fs.readFileSync(path.resolve(root,log),"utf8").trim().split("\n").map(JSON.parse);
 const location=lines.filter(x=>x.Package==="ponder.local/ponder-go/config"&&x.Test==="TestUpstreamConfigRuntime"&&x.Output?.includes("Live original/native evidence: "));
 if(location.length!==1||!lines.some(x=>x.Package==="ponder.local/ponder-go/config"&&x.Test==="TestUpstreamConfigRuntime"&&x.Action==="pass"))throw Error("Missing actual successful complete native config execution: "+mode);
 const directory=location[0].Output.split("Live original/native evidence: ")[1].trim();
 if(!directory.startsWith(path.join(root,"parity/runs/config-types-m29/live-")))throw Error("Unexpected native evidence directory");
 const file=path.join(directory,"native.json"),native=read(file),capture=read(path.join(directory,"calls.json")),original=read(path.join(directory,"original.json")),compile=read(path.join(directory,"block-filter-compile.json"));
 if(adapted){
  if(!Number.isFinite(native.elapsed_ms)||native.elapsed_ms<0||!Number.isFinite(Date.parse(native.started_utc)))throw Error('Config live execution timing missing');
  validateLiveRun({...native,finished_utc:new Date(Date.parse(native.started_utc)+native.elapsed_ms).toISOString()},mode==='go-test'?'normal':'race','ponder.local/ponder-go/config','TestUpstreamConfigRuntime');
  if(lines.some(x=>x.Package==='ponder.local/ponder-go/config'&&x.Test?.startsWith('TestUpstreamConfigRuntime')&&['fail','skip'].includes(x.Action)))throw Error('Incomplete config native execution');
 }
 if(native.cases.length!==39||capture.cases.length!==39||native.calls.length!==69||native.calls.some(c=>c.differences?.length))throw Error("Incomplete source calls or native differences: "+mode);
 if(!original.success||original.numPassedTests!==39||original.numPendingTests!==1||original.numFailedTests!==0)throw Error("Incomplete unchanged source assertions");
 if(!native.source_context?.length||!native.source_context.every(validHash)||!compile.inputs.every(validHash)||!validHash(compile.positive)||!validHash(compile.negative)||!compile.compile_passed)throw Error("Stale source or compiler context");
 for(const c of capture.cases){const source=selected.find(t=>t.id===c.id);if(!source||source.source_sha256!==c.source_sha256||source.test_source!==c.test_source||JSON.stringify(source.assertions)!==JSON.stringify(c.assertions)||!c.calls.length)throw Error("Incomplete original declaration: "+c.id);}

 const typedCompile=read(path.join(directory,"typed-compile.json")),literalCompile=read(path.join(directory,"literal-compile.json"));
 const extraDirectives=[...typedCompile.directives,...literalCompile.directives],controls=native.calls.flatMap(c=>c.typed_controls??[]);
 if(extraDirectives.length!==9||controls.length!==9||!validHash(literalCompile.generated)||![...typedCompile.inputs,...literalCompile.inputs].every(validHash))throw Error("Actual typed/generated production controls missing or stale");
 for(const directive of extraDirectives){
  const control=controls.find(c=>c.source_id===directive.source_id&&c.mode===directive.mode);
  if(!control||control.exit_code!==0||!control.output||!validHash(directive.positive)||!validHash(directive.negative)||directive.build_positive.exit_code!==0||directive.build_negative.exit_code<=0)throw Error("Typed directive lacks positive/build/native-negative proof: "+directive.mode);
 }
 typedModes.push({mode,directives:extraDirectives,controls,compile:hash(path.join(directory,"typed-compile.json")),literal_compile:hash(path.join(directory,"literal-compile.json")),generated:literalCompile.generated,corrected_event_source_error:literalCompile.corrected_event_source_error});
 const proofs=native.calls.filter(c=>c.type_proof);
 if(proofs.length!==1||proofs[0].case_id!==compile.source_id||proofs[0].type_proof.passed!==true)throw Error("Missing actual public filter positive execution");
 const output=JSON.parse(proofs[0].type_proof.positive_output);if(output.interval!==1||output.filter.fromBlock!==16370000||output.filter.toBlock!==16370020||output.callback!=="a:block")throw Error("Positive compiler fixture did not execute original materialized block input");
 const metadataChecks=native.calls.reduce((n,c)=>n+(c.metadata_identity_checks??0),0),hyperliquidChecks=native.calls.reduce((n,c)=>n+(c.hyperliquid_identity_checks??0),0);
 if(!metadataChecks||hyperliquidChecks!==2)throw Error("Original Hyperliquid toBe requires actual canonical native object identity checks");
 const rpcChecks=native.calls.reduce((n,c)=>n+(c.rpc_identity_checks??0),0),callbackChecks=native.calls.reduce((n,c)=>n+(c.callback_identity_checks??0),0);
 if(!rpcChecks||!callbackChecks)throw Error("Actual client/callback identity checks missing");
 executions.push({mode,complete:true,...(adapted?{native_mode:mode==="go-test"?"normal":"race",log:hash(log),context_archive:hash(archiveArgument),result:hash(file),result_kind:"native_capture_with_elapsed_time",environment_adapted:true,pristine_file:false}:{}),native:hash(file),capture:hash(path.join(directory,"calls.json")),original:hash(path.join(directory,"original.json")),compile:hash(path.join(directory,"block-filter-compile.json")),source_context:native.source_context,original_passed:39,original_pending:1,actual_native_calls:69,rpc_identity_checks:rpcChecks,callback_identity_checks:callbackChecks,metadata_identity_checks:metadataChecks,hyperliquid_identity_checks:hyperliquidChecks,positive_execution:{source_id:compile.source_id,exit_code:0,input:proofs[0].type_proof.input,output},compile_report:compile});
}
const first=executions[0].compile_report;
const directive={source_id:first.source_id,source_sha256:first.source_sha256,source_error_directive:first.source_error_directive,status:"passed",positive:first.positive,negative:first.negative,build_positive:first.build_positive,build_negative:first.build_negative,execute_positive:{exit_code:0,method:"Original materialized config input on the actual original Anvil during both complete native Go runs",normal:executions[0].positive_execution,race:executions[1].positive_execution}};

const extraDirectives=typedModes[0].directives.map(directive=>{
 const normal=typedModes[0].controls.find(c=>c.source_id===directive.source_id&&c.mode===directive.mode),race=typedModes[1].controls.find(c=>c.source_id===directive.source_id&&c.mode===directive.mode);
 return {...directive,status:"passed",execute_positive:{exit_code:0,method:"Actual production typed/generated API program on the original live Anvil during both native Go modes",normal,race}};
});
if(!adapted){const pristine=read("parity/runs/config-oracle.json");if(!pristine.complete||!pristine.success||!validHash(pristine.raw_oracle)||!pristine.source_context.every(validHash))throw Error("Pristine original config acceptance missing or stale");}

const report={complete:true,scope:"complete_module_config",baseline:ledger.baseline,counting:"39 active declarations executed on both sides; all39 fully mapped with10 actual native compiler obligations. One original skip retained,unmapped. Supplemental runtime/backend cases add zero source credit.",summary:{source_cases:40,active_runtime_cases:39,original_skip:1,source_assertion_expressions:94,active_assertion_expressions:92,fully_mapped:39,mapped_assertion_expressions:92,source_error_directives:10,source_error_directives_passed:10,source_error_directives_remaining:0},executions,...(adapted?{run_dir:path.relative(root,runDirectory),environment_adapted:true,pristine_file:false,source_credit:0,context_archive:hash(archiveArgument),...(fs.existsSync(root+"/parity/runs/config-oracle.json")?{history_pristine_reference:hash("parity/runs/config-oracle.json")}: {})}:{pristine:hash("parity/runs/config-oracle.json")}),directives:[directive,...extraDirectives],typed_executions:typedModes,inputs:[...currentRunInputs,...["config/source_native.go","config/typed_source.go","config/source_bindings.go","cmd/source-codegen/main.go","config/source_upstream_test.go","runtime/source.go","tools/config-live-results.mjs","tools/config-compile-fixtures.mjs","tools/config-typed-compile-fixtures.mjs","tools/config-literal-compile-fixtures.mjs"].map(hash)]};
fs.writeFileSync(path.resolve(root,destination),JSON.stringify(report,null,2)+"\n");
console.log(report.summary);
