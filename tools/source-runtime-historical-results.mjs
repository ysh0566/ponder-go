import {finiteHistoricalStages} from './finite-historical-acceptance.mjs';
import {atomicMultichainStages} from './multichain-atomic-acceptance.mjs';
import {independentStorageStages} from './independent-storage-acceptance.mjs';
import {indexingProfileStages} from './indexing-profile-acceptance.mjs';
import {indexingActionsStages} from './indexing-actions-acceptance.mjs';
// Accept only complete current original/backend/native runs, without source
// credit from supplemental controls or the three unimplemented source cases.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {sameOriginalDeclaration} from './source-runtime-historical-evidence.mjs';
import {sourceFingerprint} from './go-source-fingerprint.mjs';
import {validateContextEntry} from './loopback-context.mjs';
const root=path.resolve(import.meta.dirname,'..'),arg=process.argv.find(x=>x.startsWith('--run-dir='));
if(!arg)throw Error('Explicit complete run directory required');
const directory=path.resolve(root,arg.slice(10)),relative=path.relative(root,directory);
if(!['runtime-historical-m36','runtime-multichain-m37','indexing-service-m38','indexing-actions-m39','indexing-profile-m40','independent-storage-m41','multichain-atomic-m42','finite-historical-m43'].some(stage=>relative.startsWith('parity/runs/'+stage+'/')))throw Error('Scoped M36 aggregate required');
const read=file=>JSON.parse(fs.readFileSync(path.resolve(root,file),'utf8'));
const hash=file=>({file:path.relative(root,path.resolve(root,file)),sha256:crypto.createHash('sha256').update(fs.readFileSync(path.resolve(root,file))).digest('hex')});
const check=(value,message)=>{if(!value)throw Error(message)};
const valid=entry=>entry&&fs.existsSync(path.resolve(root,entry.file))&&hash(entry.file).sha256===entry.sha256;
const equal=(a,b)=>JSON.stringify(a)===JSON.stringify(b);
const targets=['1337ad403051f00c5d99','dd482eb7065250d54888','b259864fbc99fca7585d','6932328b1d777888e031','35500a04eb3407d46aa8','055e33151c56724c5459','eb47df94258c65be3a2e','1644fb814d5a0df1b23c','4a23b3323e9c3992e535','d685ca5ec1b8a49be6b3','42b8ee0fbd39ff105e51'];
const full=read(directory+'/result.json'),fingerprint=sourceFingerprint(root);
check(full.complete&&full.success&&full.environment_adapted&&full.pristine_file===false&&full.source_manifest_verified_after,'Complete adapted aggregate required');
check(equal(full.native_inputs,fingerprint)&&equal(full.finished_native_inputs,fingerprint),'Current native inputs differ');
check(valid(full.prepared_native_inputs)&&valid(full.context_archive),'Prepared/context references changed');
check(full.harness_inputs?.every(valid)&&[relative.startsWith('parity/runs/finite-historical-m43/')?'tools/finite-historical-full-verify.mjs':relative.startsWith('parity/runs/multichain-atomic-m42/')?'tools/multichain-atomic-full-verify.mjs':relative.startsWith('parity/runs/independent-storage-m41/')?'tools/independent-storage-full-verify.mjs':relative.startsWith('parity/runs/indexing-profile-m40/')?'tools/indexing-profile-full-verify.mjs':relative.startsWith('parity/runs/indexing-actions-m39/')?'tools/indexing-actions-full-verify.mjs':relative.startsWith('parity/runs/indexing-service-m38/')?'tools/indexing-service-full-verify.mjs':relative.startsWith('parity/runs/runtime-multichain-m37/')?'tools/runtime-multichain-full-verify.mjs':'tools/runtime-historical-full-verify.mjs','tools/source-runtime-historical-live.setup.ts','tools/source-runtime-historical-original.mjs','tools/source-runtime-historical-fixture.mjs','tools/source-runtime-historical-log-order.test.ts'].every(file=>full.harness_inputs.some(x=>x.file===file)),'Complete current harness inventory required');
const requiredCommands=['runtime-historical-evidence-controls','remaining-mapped-original-postgres','remaining-mapped-original-pglite','runtime-index-source-postgres','runtime-index-source-pglite','runtime-historical-source-postgres','runtime-historical-source-pglite','runtime-historical-log-order-postgres','runtime-historical-log-order-pglite','runtime-historical-fixture','sync-type-controls','historical-type-controls','sdk-client-build','prepared-native-inputs','abi-generated-normal','abi-generated-race','go-normal','go-race','go-vet','cli-build','erc20-build','core-oracle','parameters-types','sync-types','historical-types','runtime-index-types','sdk-client-tests','sdk-react-types'];
if(['runtime-multichain-m37','indexing-service-m38','indexing-actions-m39','indexing-profile-m40','independent-storage-m41','multichain-atomic-m42','finite-historical-m43'].some(stage=>relative.startsWith('parity/runs/'+stage+'/')))requiredCommands.push('runtime-multichain-evidence-controls','runtime-multichain-source-postgres','runtime-multichain-source-pglite','runtime-multichain-probes-postgres','runtime-multichain-probes-pglite','runtime-multichain-fixture','runtime-multichain-probe-fixture');
if(['indexing-service-m38','indexing-actions-m39','indexing-profile-m40','independent-storage-m41','multichain-atomic-m42','finite-historical-m43'].some(stage=>relative.startsWith('parity/runs/'+stage+'/')))requiredCommands.push('indexing-service-evidence-controls','indexing-service-source-postgres','indexing-service-source-pglite','indexing-service-fixture','indexing-service-source-collect','indexing-service-proxy-source','indexing-service-proxy-fixture','indexing-service-proxy-evidence-controls','indexing-service-cache-source','indexing-service-cache-fixture','indexing-service-cache-evidence-controls');
if(['indexing-actions-m39','indexing-profile-m40','independent-storage-m41','multichain-atomic-m42','finite-historical-m43'].some(stage=>relative.startsWith('parity/runs/'+stage+'/')))requiredCommands.push(...indexingActionsStages);
if(['indexing-profile-m40','independent-storage-m41','multichain-atomic-m42','finite-historical-m43'].some(stage=>relative.startsWith('parity/runs/'+stage+'/')))requiredCommands.push(...indexingProfileStages);
if(['independent-storage-m41','multichain-atomic-m42','finite-historical-m43'].some(stage=>relative.startsWith('parity/runs/'+stage+'/')))requiredCommands.push(...independentStorageStages);
if(['multichain-atomic-m42','finite-historical-m43'].some(stage=>relative.startsWith('parity/runs/'+stage+'/')))requiredCommands.push(...atomicMultichainStages);
if(relative.startsWith('parity/runs/finite-historical-m43/'))requiredCommands.push(...finiteHistoricalStages);
check(full.commands.length===requiredCommands.length&&requiredCommands.every(name=>full.commands.filter(c=>c.name===name&&c.exit_code===0).length===1),'Missing, duplicate or failed aggregate stage');
const prepared=read(full.prepared_native_inputs.file);
check(equal(prepared.fingerprint,fingerprint)&&prepared.files.every(valid),'Prepared inventory changed');
const ledger=read('parity/test-ledger.json'),selected=ledger.tests.filter(x=>x.source_file==='packages/core/src/runtime/historical.test.ts'),targeted=selected.filter(x=>targets.includes(x.id));
check(selected.length===14&&selected.reduce((n,x)=>n+x.assertions.length,0)===29&&selected.every(x=>x.type_error_directives.length===0)&&targeted.length===11&&targeted.reduce((n,x)=>n+x.assertions.length,0)===24,'Original census changed');
const fixture=read('testdata/runtime/source-runtime-historical-live.json');
check(equal(fixture.cases,fixture.backend_cases.postgres),'Primary fixture differs from PostgreSQL source capture');
const sourceRecords={};
for(const backend of ['postgres','pglite']){
 const source=directory+'/historical-source-'+backend,result=read(source+'/result.json'),capture=read(source+'/calls.json'),original=read(source+'/original.json');
 check(result.complete&&result.success&&result.backend===backend&&result.environment_adapted&&result.pristine_file===false&&result.adapter_verified_after&&result.counts.passed===14&&result.counts.failed===0&&result.counts.pending===0&&result.inputs.every(valid),'Current complete adapted original required');
 check(valid(result.context_archive)&&path.resolve(root,result.context_archive.file)===source+'/context-archive.json','Exact source context archive changed');
 const archive=read(result.context_archive.file);
 check(archive.project_root===root&&archive.evidence_root===source&&archive.verification_phase==='before-dispose'&&Object.values(archive.entries).every(e=>validateContextEntry({file:e.executed_file,sha256:e.sha256},archive)),'Source context archive bytes changed');
 const sourceStart=Date.parse(result.started_utc),sourceEnd=Date.parse(result.finished_utc);
 check(Number.isFinite(sourceStart)&&Number.isFinite(sourceEnd)&&Date.parse(full.started_utc)<=sourceStart&&sourceStart<=sourceEnd&&sourceEnd<=Date.parse(prepared.recorded_utc),'Source capture outside current preparation window');
 check(original.success&&original.numPassedTests===14&&original.numFailedTests===0&&original.numPendingTests===0&&capture.cases.length===14&&equal(capture.cases,fixture.backend_cases[backend]),'Native fixture differs from complete original');
 let calls=0,providers=0;
 for(const item of selected){
  const c=capture.cases.find(x=>x.id===item.id);
  check(sameOriginalDeclaration(c,item)&&c.calls.length>0,'Original declaration/inputs changed');
  const originalCases=original.testResults.flatMap(x=>x.assertionResults).filter(x=>x.title===item.title);
  check(originalCases.length===1&&originalCases[0].status==='passed','Original source case not passed exactly once');
  if(!targets.includes(item.id)){check(c.targeted===false&&c.calls.every(x=>x.unimplemented===true),'Unimplemented source case must remain explicit');continue}
  check(c.targeted===true,'Targeted source case missing');calls+=c.calls.length;
  for(const [i,call] of c.calls.entries()){
   check(!call.unimplemented&&!call.error&&call.after&&call.physical_before&&call.physical_after&&call.providers&&call.steps?.length>0&&call.results?.length>0,'Incomplete generator call capture');
   if(i===0)check(Object.values(call.physical_before).every(rows=>Array.isArray(rows)&&rows.length===0),'Native initial DB must be empty, not output-seeded');
   providers+=call.providers.length;
  }
 }
 check(calls===14&&providers===24,'Targeted call/RPC census changed');
 const probeDir=directory+'/historical-log-order-'+backend,probe=read(probeDir+'/result.json'),order=read(probeDir+'/order.json');
 check(probe.complete&&probe.success&&probe.backend===backend&&probe.environment_adapted&&probe.pristine_file===false&&probe.adapter_verified_after&&probe.counts.passed===1&&probe.counts.failed===0&&probe.counts.pending===0&&probe.inputs.every(valid)&&valid(probe.context_archive),'Current actual RPC prefetch probe required');
 check(path.resolve(root,probe.context_archive.file)===probeDir+'/context-archive.json','Exact probe archive required');
 const probeArchive=read(probe.context_archive.file),probeOriginal=read(probeDir+'/original.json');
 check(probeArchive.project_root===root&&probeArchive.evidence_root===probeDir&&probeArchive.verification_phase==='before-dispose'&&Object.values(probeArchive.entries).every(e=>validateContextEntry({file:e.executed_file,sha256:e.sha256},probeArchive)),'Probe persisted context bytes changed');
 check(probeOriginal.success&&probeOriginal.numPassedTests===1&&probeOriginal.numFailedTests===0&&probeOriginal.numPendingTests===0,'Actual source probe original result required');
 const probeStart=Date.parse(probe.started_utc),probeEnd=Date.parse(probe.finished_utc);
 check(Number.isFinite(probeStart)&&Number.isFinite(probeEnd)&&Date.parse(full.started_utc)<=probeStart&&probeStart<=probeEnd&&probeEnd<=Date.parse(prepared.recorded_utc),'Source probe outside current preparation window');
 check(order.source_credit===0&&order.backend===backend&&order.real_database===true&&order.range_rpc_prefetched===true&&equal(order.output,[25,27]),'Real RPC prefetch/order proof failed');
 const rpc=order.timeline.find(x=>x.action==='rpc_started'&&x.method==='eth_getLogs'&&x.params[0].fromBlock==='0x1a'),finish=order.timeline.find(x=>x.action==='transaction_resolved'&&x.id===1),block=order.timeline.find(x=>x.action==='block_started'&&x.interval[0]===26);
 check(rpc&&finish&&block&&rpc.sequence<finish.sequence&&block.sequence>finish.sequence,'Source range RPC/commit/block ordering differs');
 sourceRecords[backend]={capture,original,result,source,probeDir};
}
const executions=[];
for(const mode of ['normal','race']){
 const name=mode==='normal'?'go-test':'go-race',log=directory+'/native/'+name+'.jsonl',manifest=read(directory+'/native/'+name+'-execution.json');
 check(manifest.complete&&manifest.success&&manifest.scope==='all_module_packages'&&equal(manifest.source_fingerprint,fingerprint)&&equal(manifest.finished_source_fingerprint,fingerprint),'Complete current module execution required');
 check(Date.parse(prepared.recorded_utc)<=Date.parse(manifest.started_utc),'Prepared inventory must precede execution');
 check(manifest.race===(mode==='race')&&manifest.driver_sha256===hash('tools/go-tests.mjs').sha256,'Native execution mode/driver changed');
 const started=Date.parse(manifest.started_utc),finished=Date.parse(manifest.finished_utc);
 check(Number.isFinite(started)&&Number.isFinite(finished)&&Date.parse(full.started_utc)<=started&&started<=finished&&finished<=Date.parse(full.finished_utc),'Native execution outside current aggregate');
 const events=fs.readFileSync(log,'utf8').trim().split('\n').map(JSON.parse);
 check(events.every(e=>Number.isFinite(Date.parse(e.Time))&&Date.parse(e.Time)>=started-1000&&Date.parse(e.Time)<=finished+1000),'Native log outside manifest execution');
 const passed=(test,pkg='ponder.local/ponder-go/runtime')=>events.filter(e=>e.Package===pkg&&e.Test===test&&e.Action==='pass').length===1;
 check(!events.some(e=>e.Test&&['fail','skip'].includes(e.Action)),'Aggregate must have no native failures/skips');
 for(const pkg of manifest.packages)for(const test of pkg.listed_functions)check(passed(test,pkg.package),'Missing/duplicate listed function');
 for(const test of ['TestSourceRuntimeHistoricalOriginal','TestSourceRuntimeHistoricalEvidenceCompleteness','TestSourceLocalHistoricalControls'])check(passed(test),'Runtime prerequisite missing: '+test);
 check(passed('TestSourceSyncTransaction','ponder.local/ponder-go/store'),'Transaction prerequisite missing');
 for(const backend of ['postgres','pglite']){
  const {result,source,probeDir}=sourceRecords[backend],cases=[];
  for(const item of targeted){const test='TestSourceRuntimeHistoricalOriginal/'+backend+'/'+item.title.replaceAll(/\s/g,'_');check(passed(test),'Native original case missing: '+test);cases.push({source_id:item.id,go_test:test,runtime_passed:true,native_type_obligations:0,native_type_status:'not_applicable'})}
  executions.push({mode,backend,complete:true,cases,native_calls:14,provider_requests:24,log:hash(log),native:hash(log),native_test_prefix:'TestSourceRuntimeHistoricalOriginal/'+backend,capture:hash(source+'/calls.json'),original:hash(source+'/original.json'),result:hash(source+'/result.json'),source_context:result.inputs,context_archive:result.context_archive,source_order_probe:hash(probeDir+'/order.json'),source_order_result:hash(probeDir+'/result.json'),source_order_original:hash(probeDir+'/original.json'),source_order_context:hash(probeDir+'/context-archive.json')});
 }
}
const report={complete:true,success:true,scope:'complete_module_source_runtime_historical',run_dir:relative,environment_adapted:true,pristine_file:false,summary:{source_cases:14,source_assertions:29,targeted_cases:11,targeted_assertions:24,source_error_directives:0,complete_mapping_candidates:11,unimplemented_source_cases:3,supplementary_source_credit:0},full_verification:hash(directory+'/result.json'),prepared_native_inputs:full.prepared_native_inputs,context_archive:full.context_archive,executions,inputs:['tools/source-runtime-historical-evidence.mjs','tools/source-runtime-historical-results.mjs','tools/source-runtime-historical-fixture.mjs','runtime/source_local_historical.go','runtime/source_local_historical_test.go','store/source_transaction.go','testdata/runtime/source-runtime-historical-live.json'].map(hash)};
fs.writeFileSync(path.resolve(root,process.argv.find(x=>x.startsWith('--output='))?.slice(9)??'parity/runs/source-runtime-historical-verification.json'),JSON.stringify(report,null,2)+'\n');console.log(report.summary);
