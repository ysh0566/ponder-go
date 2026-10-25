// Accept only the complete, unchanged aggregate run and its own source captures.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {sourceFingerprint} from './go-source-fingerprint.mjs';
import {validateContextEntry} from './loopback-context.mjs';
const root=path.resolve(import.meta.dirname,'..'),arg=process.argv.find(x=>x.startsWith('--run-dir='));
if(!arg)throw Error('Explicit complete run directory required');
const directory=path.resolve(root,arg.slice(10)),relative=path.relative(root,directory);
if(!relative.startsWith('parity/runs/runtime-index-m35/'))throw Error('Scoped M35 run required');
const read=file=>JSON.parse(fs.readFileSync(path.resolve(root,file),'utf8'));
const hash=file=>({file:path.relative(root,path.resolve(root,file)),sha256:crypto.createHash('sha256').update(fs.readFileSync(path.resolve(root,file))).digest('hex')});
const check=(value,message)=>{if(!value)throw Error(message)};
const valid=entry=>entry&&fs.existsSync(path.resolve(root,entry.file))&&hash(entry.file).sha256===entry.sha256;
const equal=(a,b)=>JSON.stringify(a)===JSON.stringify(b);
const full=read(directory+'/result.json'),fingerprint=sourceFingerprint(root);
check(full.complete&&full.success&&full.environment_adapted&&full.pristine_file===false&&full.source_manifest_verified_after,'Complete adapted aggregate acceptance required');
check(equal(full.native_inputs,fingerprint)&&equal(full.finished_native_inputs,fingerprint),'Current native inputs differ');
check(valid(full.prepared_native_inputs)&&valid(full.context_archive),'Prepared/context references changed');
const prepared=read(full.prepared_native_inputs.file);
check(equal(prepared.fingerprint,fingerprint)&&prepared.files.every(valid),'Prepared complete native inventory changed');
const ledger=read('parity/test-ledger.json'),selected=ledger.tests.filter(x=>x.source_file==='packages/core/src/runtime/index.test.ts');
check(selected.length===15&&selected.reduce((n,x)=>n+x.assertions.length,0)===30&&selected.reduce((n,x)=>n+x.type_error_directives.length,0)===6,'Pinned original census changed');
const fixture=read('testdata/runtime/source-runtime-index-live.json'),types=read('parity/runs/source-runtime-index-types.json');
check(types.complete&&types.success&&types.original_source_unchanged&&types.pristine_diagnostics===0&&types.stripped_diagnostics===6&&types.explicit_key_union_diagnostics===0&&types.obligations.length===6,'Actual source-language compiler audit missing');
check(types.executions.length===3&&types.executions.every(e=>e.exit_code===0&&valid(e.report)&&valid(e.log))&&types.go_obligation_treatment.translated_go_negative_compile_tests===0,'Do not invent native type credit');
const executions=[];
for(const mode of ['normal','race']){
 const name=mode==='normal'?'go-test':'go-race',log=directory+'/native/'+name+'.jsonl',manifest=read(directory+'/native/'+name+'-execution.json');
 check(manifest.complete&&manifest.success&&manifest.scope==='all_module_packages'&&equal(manifest.source_fingerprint,fingerprint)&&equal(manifest.finished_source_fingerprint,fingerprint),'Complete current module execution required');
 check(Date.parse(prepared.recorded_utc)<=Date.parse(manifest.started_utc),'Prepared inventory must precede native execution');
 const events=fs.readFileSync(log,'utf8').trim().split('\n').map(JSON.parse),passed=test=>events.filter(e=>e.Package==='ponder.local/ponder-go/runtime'&&e.Test===test&&e.Action==='pass').length===1;
 check(!events.some(e=>e.Test&&['fail','skip'].includes(e.Action)),'Aggregate must have no native failures/skips');
 for(const pkg of manifest.packages)for(const test of pkg.listed_functions)check(events.filter(e=>e.Package===pkg.package&&e.Test===test&&e.Action==='pass').length===1,'Missing/duplicate listed function');
 for(const test of ['TestSourceRuntimeIndexFullFile','TestSourceRuntimeIndexEvidenceCompleteness','TestSourceLocalSyncProgressAssertions','TestSourceLocalSyncProgressSameHeightIdentity','TestSourceLocalSyncProgressControls','TestSourceSyncProgressMethods','TestSourceSyncProgressRPC','TestSourceEventOrderOriginal','TestSourceEventOrderControls'])check(passed(test),'Supplemental runtime prerequisite missing: '+test);
 for(const backend of ['postgres','pglite']){
  const source=directory+'/source-'+backend,result=read(source+'/result.json'),capture=read(source+'/calls.json'),original=read(source+'/original.json');
  check(result.complete&&result.success&&result.backend===backend&&result.environment_adapted&&result.pristine_file===false&&result.adapter_verified_after&&result.counts.passed===15&&result.counts.failed===0&&result.counts.pending===0&&result.inputs.every(valid),'Current complete adapted original required');
  check(valid(result.context_archive),'Source context archive changed');
  const archive=read(result.context_archive.file);
  check(Object.values(archive.entries).every(e=>validateContextEntry({file:e.executed_file,sha256:e.sha256},archive)),'Source context archive bytes changed');
  check(Date.parse(result.finished_utc)<=Date.parse(prepared.recorded_utc),'Source capture must precede native snapshot');
  check(original.success&&original.numPassedTests===15&&original.numFailedTests===0&&original.numPendingTests===0&&capture.cases.length===15&&equal(capture.cases,fixture.backend_cases[backend]),'Native fixture must equal corresponding complete original capture');
  const cases=[];let calls=0;
  for(const item of selected){
   const c=capture.cases.find(x=>x.id===item.id),test='TestSourceRuntimeIndexFullFile/'+backend+'/'+item.title.replaceAll(/\s/g,'_');
   check(c&&c.source_sha256===item.source_sha256&&c.test_source===item.test_source&&equal(c.assertions,item.assertions)&&equal(c.type_error_directives,item.type_error_directives)&&c.source_state===item.source_state&&c.calls.length>0,'Original declaration or inputs changed');
   const sourceTests=original.testResults.flatMap(x=>x.assertionResults).filter(x=>x.title===item.title);
   check(sourceTests.length===1&&sourceTests[0].status==='passed'&&passed(test),'Original/native mapped case missing');
   calls+=c.calls.length;cases.push({source_id:item.id,go_test:test,runtime_passed:true,native_type_obligations:item.type_error_directives.length,native_type_status:item.type_error_directives.length?'pending':'not_applicable'});
  }
  check(calls===28,'Complete actual operation census changed');
  executions.push({mode,backend,complete:true,cases,native_calls:28,log:hash(log),native:hash(log),native_test_prefix:'TestSourceRuntimeIndexFullFile/'+backend,capture:hash(source+'/calls.json'),original:hash(source+'/original.json'),result:hash(source+'/result.json'),source_context:result.inputs,context_archive:result.context_archive});
 }
}
const report={complete:true,success:true,scope:'complete_module_source_runtime_index',run_dir:relative,environment_adapted:true,pristine_file:false,summary:{source_cases:15,source_assertions:30,source_error_directives:6,runtime_passed_cases:15,complete_mapping_candidates:11,native_type_pending_cases:4,native_type_obligations_verified:0,supplementary_source_credit:0},full_verification:hash(directory+'/result.json'),prepared_native_inputs:full.prepared_native_inputs,context_archive:full.context_archive,source_types:hash('parity/runs/source-runtime-index-types.json'),executions,inputs:['tools/source-runtime-index-results.mjs','runtime/source_index_live_test.go','runtime/source_sync_progress.go','runtime/source_event_merge.go','runtime/source_sync_cache.go','rpc/async.go','testdata/runtime/source-runtime-index-live.json','parity/runs/runtime-index-progress-order-source/probe.json'].map(hash)};
fs.writeFileSync(root+'/parity/runs/source-runtime-index-verification.json',JSON.stringify(report,null,2)+'\n');console.log(report.summary);
