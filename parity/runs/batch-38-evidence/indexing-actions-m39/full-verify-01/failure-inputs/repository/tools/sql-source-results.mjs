// Validate source preparation separately from module-wide native acceptance.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import {sourceFingerprint} from "./go-source-fingerprint.mjs";
import {validateContextEntry} from "./loopback-context.mjs";
import {historicalCaptureContext,manifestFile as historicalManifest} from "./historical-capture-context.mjs";

const root=path.resolve(import.meta.dirname,".."),arg=name=>process.argv.find(a=>a.startsWith("--"+name+"="))?.slice(name.length+3);
const read=file=>JSON.parse(fs.readFileSync(path.resolve(root,file),"utf8"));
const hash=file=>({file:path.relative(root,path.resolve(root,file)),sha256:crypto.createHash("sha256").update(fs.readFileSync(path.resolve(root,file))).digest("hex")});
const diskValid=entry=>entry&&fs.existsSync(path.resolve(root,entry.file))&&hash(entry.file).sha256===entry.sha256;
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
const destination=arg('output')??(adapted?path.join(runDirectory,'sql-source-verification.json'):'parity/runs/sql-source-verification.json');
const ledger=read("parity/test-ledger.json");
const file="packages/core/src/utils/sql-parse.test.ts";
const source=ledger.tests.filter(t=>t.source_file===file);
if(source.length!==14||source.reduce((n,c)=>n+c.assertions.length,0)!==19||source.some(c=>c.type_error_directives.length||c.source_state!=="active"))throw Error("Unexpected original SQL declarations");
const fixture=read("testdata/sql/captured-original-sql.json");
const historicalContext=historicalCaptureContext("testdata/sql/captured-original-sql.json");
if(fixture.baseline.revision!==ledger.baseline.revision||fixture.cases.length!==14||fixture.capture_proof?.scope!=="complete_original_with_call_through"||!historicalContext.every(valid)||!valid(fixture.capture_proof.raw_oracle))throw Error("Incomplete/stale SQL capture context");
const captureOracle=read(fixture.capture_proof.raw_oracle.file);
if(!captureOracle.success||captureOracle.numPassedTests!==14||captureOracle.numPendingTests!==0||captureOracle.numFailedTests!==0)throw Error("Incomplete captured original file");
for(const c of fixture.cases){
  const original=source.find(t=>t.id===c.id);
  if(!original||c.source_sha256!==original.source_sha256||c.test_source!==original.test_source||JSON.stringify(c.assertions)!==JSON.stringify(original.assertions)||!c.calls.length)throw Error("Changed SQL declaration/input/assertion: "+c.id);
}
const calls=fixture.cases.reduce((n,c)=>n+c.calls.length,0);
const relational=fixture.cases.find(c=>c.id==="a0e8f19f1c34af2d3d6c");
if(calls!==38||relational.calls.length!==8)throw Error("Missing original SQL calls/actual Drizzle relational inputs");
const pristinePath=process.argv.find(a=>a.startsWith("--pristine="))?.slice(11)??"parity/runs/sql-original-pristine.json";
let pristine=null,currentOriginal=null;
if(!adapted){
pristine=read(pristinePath);
if(!pristine.complete||!pristine.success||pristine.scope!=="complete_pristine_original"||!pristine.source_context.every(valid)||!valid(pristine.raw_oracle))throw Error("Incomplete/stale pristine SQL oracle");
const pristineOracle=read(pristine.raw_oracle.file);
if(!pristineOracle.success||pristineOracle.numPassedTests!==14||pristineOracle.numPendingTests!==0||pristineOracle.numFailedTests!==0)throw Error("Pristine original SQL assertions failed");
}else{
 if(arg('pristine'))throw Error('Adapted SQL acceptance cannot substitute a pristine report');
 const executions=[];
 const contextFiles=[file,'packages/core/src/utils/sql-parse.ts','packages/core/src/_test/globalSetup.ts','packages/core/src/_test/setup.ts','packages/core/vite.config.ts','pnpm-lock.yaml'];
 const sourceContext=contextFiles.map(relative=>{
  const entry=contextArchive.entries[relative];
  if(!entry)throw Error('SQL source context absent from executed archive: '+relative);
  const ref={file:path.relative(root,entry.executed_file),sha256:entry.sha256};
  if(!valid(ref))throw Error('SQL executed context changed: '+relative);
  return ref;
 });
 if(sourceContext[0].sha256!==source[0].source_sha256)throw Error('SQL source file differs from pinned ledger');
 for(const backend of ['postgres','pglite']){
  const name='remaining-mapped-original-'+backend,rawFile=path.join(runDirectory,name+'.json');
  const commands=fullRun.commands.filter(c=>c.name===name);
  if(commands.length!==1||commands[0].exit_code!==0||commands[0].error||commands[0].signal)throw Error('Current remaining original command failed or missing: '+backend);
  const command=commands[0],start=Date.parse(command.started_utc),end=Date.parse(command.finished_utc);
  if(!Number.isFinite(start)||!Number.isFinite(end)||start>end||start<Date.parse(fullRun.started_utc)||end>Date.parse(fullRun.finished_utc)||!command.command.includes('src/utils/sql-parse.test.ts')||!command.command.includes('--outputFile='+rawFile))throw Error('SQL original command provenance mismatch');
  const original=read(rawFile),expectedPath=path.join(contextArchive.execution_root,file);
  const files=original.testResults?.filter(f=>f.name===expectedPath)??[];
  if(!original.success||original.numFailedTests!==0||files.length!==1)throw Error('Complete successful SQL original file required: '+backend);
  const suite=files[0],results=suite.assertionResults;
  if(suite.status!=='passed'||!Number.isFinite(suite.startTime)||!Number.isFinite(suite.endTime)||suite.startTime<start-1000||suite.endTime>end+1000||results.length!==14||results.some(r=>r.status!=='passed'))throw Error('SQL original assertions or execution time incomplete');
  const occurrences=new Map();
  for(const declaration of source){
   const title=declaration.full_name,occurrence=occurrences.get(title)??0;
   const matches=results.filter(r=>[...(r.ancestorTitles??[]),r.title].join(' > ')===title);
   if(!matches[occurrence]||matches[occurrence].status!=='passed')throw Error('Missing exact SQL declaration: '+declaration.id);
   occurrences.set(title,occurrence+1);
  }
  if([...occurrences].some(([title,count])=>results.filter(r=>[...(r.ancestorTitles??[]),r.title].join(' > ')===title).length!==count))throw Error('Unexpected SQL declaration multiplicity');
  executions.push({backend,complete:true,environment_adapted:true,pristine_file:false,raw_oracle:hash(rawFile),source_context:sourceContext,source_cases:14,source_assertions:19,command_record:command,context_archive:hash(archiveArgument)});
 }
 currentOriginal={environment_adapted:true,pristine_file:false,executions};
}
const vectors=read("testdata/sql/source-vectors.json");
if(vectors.cases.length!==61||vectors.source_parser_version!==160001||!vectors.sources.every(valid))throw Error("Incomplete/stale actual source SQL vectors");
const nested=vectors.cases.find(c=>c.sql==="SELECT (SELECT private_task.bump_cached_balance()) AS value");
if(!nested||nested.readonly!==false||nested.first_validation.error?.message!=="SubLink not supported")throw Error("Missing source conservative nested-query classification");
const ruleProof=read("parity/runs/sql-rules.json");
if(!valid(ruleProof.rules)||ruleProof.source_sha256!==hash("../ponder-upstream/"+ruleProof.source).sha256)throw Error("Native SQL rules differ from source");
const proof={
  complete:true,baseline:ledger.baseline,
  ...(adapted?{run_dir:path.relative(root,runDirectory),environment_adapted:true,pristine_file:false,source_credit:0,context_archive:hash(archiveArgument),current_original:currentOriginal,...(fs.existsSync(path.resolve(root,pristinePath))?{history_pristine_reference:hash(pristinePath)}:{})}:{}),
  counting:"14 exact source declarations/19assertions,38calls.61 supplementary source vectors and native cache/HTTP/transaction regressions add zero source credit. Native execution status comes only from complete unchanged-input Go/race evidence.",
  source_file:file,
  source_sha256:source[0].source_sha256,
  summary:{source_cases:14,source_assertions:19,source_error_directives:0,captured_operations:38,actual_relational_inputs:8,source_vectors:61,source_parser_version:160001},
  ...(!adapted?{pristine:{report:hash(pristinePath),raw_oracle:pristine.raw_oracle,source_context:pristine.source_context}}:{}),
  capture:{fixture:hash("testdata/sql/captured-original-sql.json"),raw_oracle:fixture.capture_proof.raw_oracle,source_context:historicalContext,historical_archive:hash(historicalManifest),original_source_context:fixture.capture_proof.source_context},
  vectors:{fixture:hash("testdata/sql/source-vectors.json"),sources:vectors.sources},
  rules:hash("parity/runs/sql-rules.json"),
  inputs:[hash(historicalManifest),hash("tools/historical-capture-context.mjs"),...currentRunInputs,...["sqlquery/sql.go","sqlquery/cache.go","sqlquery/upstream_test.go","sqlquery/cache_test.go","sqlquery/differential_test.go","sqlquery/rules.json","store/sql.go","store/sql_parser_cache_test.go","query/sql_validation_test.go","tools/sql-source-results.mjs"].map(hash)],
};
fs.writeFileSync(path.resolve(root,destination),JSON.stringify(proof,null,2)+"\n");
console.log(proof.summary);
