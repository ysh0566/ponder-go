import {indexingProfileStages} from './indexing-profile-acceptance.mjs';
import {indexingActionsStages,indexingActionsSupplementalTests,indexingActionsAdmissionScenarios} from './indexing-actions-acceptance.mjs';
// Complete M38 acceptance. Runtime success never discharges the three TS gaps.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {gunzipSync} from 'node:zlib';
import {sourceFingerprint} from './go-source-fingerprint.mjs';
import {validateContextEntry} from './loopback-context.mjs';
import {assertIndexingServiceCapture,assertIndexingServiceProxyCapture,assertIndexingServiceCacheCapture,indexingServiceTargets,indexingServiceTypeGaps,sameOriginalDeclaration} from './source-indexing-service-evidence.mjs';
const root=path.resolve(import.meta.dirname,'..');
const arg=name=>process.argv.find(a=>a.startsWith('--'+name+'='))?.slice(name.length+3);
if(!arg('run-dir')||!arg('output'))throw Error('Explicit --run-dir and --output required');
const directory=path.resolve(root,arg('run-dir')),relative=path.relative(root,directory),destination=path.resolve(root,arg('output'));
if(!['indexing-service-m38','indexing-actions-m39','indexing-profile-m40'].some(stage=>relative.startsWith('parity/runs/'+stage+'/'))||!destination.startsWith(root+'/parity/runs/'))throw Error('Scoped M38 run and evidence output required');
const read=file=>JSON.parse(fs.readFileSync(path.resolve(root,file),'utf8'));
const compressed=file=>JSON.parse(gunzipSync(fs.readFileSync(path.resolve(root,file))));
const hash=file=>({file:path.relative(root,path.resolve(root,file)),sha256:crypto.createHash('sha256').update(fs.readFileSync(path.resolve(root,file))).digest('hex')});
const valid=ref=>ref&&fs.existsSync(path.resolve(root,ref.file))&&hash(ref.file).sha256===ref.sha256;
const check=(value,message)=>{if(!value)throw Error(message)};
const equal=(a,b)=>JSON.stringify(a)===JSON.stringify(b);
const goJSON=value=>{
 const quote=value=>JSON.stringify(value).replace(/[<>&\u2028\u2029]/g,c=>'\\u'+c.charCodeAt(0).toString(16).padStart(4,'0'));
 if(Array.isArray(value))return '['+value.map(goJSON).join(',')+']';
 if(value&&typeof value==='object')return '{'+Object.keys(value).sort().map(key=>quote(key)+':'+goJSON(value[key])).join(',')+'}';
 return quote(value);
};

const isM39=['indexing-actions-m39','indexing-profile-m40'].some(stage=>relative.startsWith('parity/runs/'+stage+'/'));
const full=read(directory+'/result.json'),fingerprint=sourceFingerprint(root);
check(full.complete&&full.success&&full.environment_adapted&&full.pristine_file===false&&full.source_manifest_verified_after,'Complete successful adapted aggregate required');
check(equal(full.native_inputs,fingerprint)&&equal(full.finished_native_inputs,fingerprint),'Current native inputs differ');
check(valid(full.prepared_native_inputs)&&valid(full.context_archive),'Prepared inventory/context changed');
const fullArchive=read(full.context_archive.file);
check(path.resolve(root,full.context_archive.file)===directory+'/context-archive.json'&&fullArchive.project_root===root&&fullArchive.evidence_root===directory&&fullArchive.verification_phase==='before-dispose'&&Object.values(fullArchive.entries).every(e=>validateContextEntry({file:e.executed_file,sha256:e.sha256},fullArchive)),'Exact full context archive bytes changed');
const prepared=read(full.prepared_native_inputs.file);
check(equal(prepared.fingerprint,fingerprint)&&prepared.files.every(valid)&&prepared.files.some(f=>f.file==='indexing/service.go'),'Complete current inventory including indexing required');
const harness=['tools/manifest.mjs','tools/source-indexing-service-cache.mjs','tools/source-indexing-service-cache.config.mjs','tools/source-indexing-service-cache.test.ts','tools/source-indexing-service-cache-fixture.mjs','tools/source-indexing-service-cache-evidence.test.mjs','tools/indexing-service-full-verify.mjs','tools/source-indexing-service-results.mjs','tools/source-indexing-service-map.mjs','tools/source-indexing-service-collect.mjs','tools/source-indexing-service-proxy.mjs','tools/source-indexing-service-proxy.config.mjs','tools/source-indexing-service-proxy.test.ts','tools/source-indexing-service-proxy-fixture.mjs','tools/source-indexing-service-proxy-evidence.test.mjs','tools/source-indexing-service-original.mjs','tools/source-indexing-service-live.config.mjs','tools/source-indexing-service-live.setup.ts','tools/source-indexing-service-fixture.mjs','tools/source-indexing-service-evidence.mjs','tools/source-indexing-service-evidence.test.mjs','tools/go-source-fingerprint.mjs','tools/record-native-inputs.mjs','tools/record-results.mjs','tools/source-sync-results.mjs','tools/source-historical-results.mjs','tools/source-realtime-results.mjs','tools/config-live-results.mjs','tools/sql-source-results.mjs','tools/source-runtime-index-results.mjs','tools/source-runtime-historical-results.mjs','tools/source-runtime-multichain-results.mjs'];
check(full.harness_inputs?.every(valid)&&harness.every(file=>full.harness_inputs.some(ref=>ref.file===file)),'Complete current acceptance harness required');
const requiredCommands=['runtime-historical-evidence-controls','remaining-mapped-original-postgres','remaining-mapped-original-pglite','runtime-index-source-postgres','runtime-index-source-pglite','runtime-historical-source-postgres','runtime-historical-source-pglite','runtime-historical-log-order-postgres','runtime-historical-log-order-pglite','runtime-historical-fixture','sync-type-controls','historical-type-controls','sdk-client-build','prepared-native-inputs','abi-generated-normal','abi-generated-race','go-normal','go-race','go-vet','cli-build','erc20-build','core-oracle','parameters-types','sync-types','historical-types','runtime-index-types','sdk-client-tests','sdk-react-types','runtime-multichain-evidence-controls','runtime-multichain-source-postgres','runtime-multichain-source-pglite','runtime-multichain-probes-postgres','runtime-multichain-probes-pglite','runtime-multichain-fixture','runtime-multichain-probe-fixture','indexing-service-evidence-controls','indexing-service-source-postgres','indexing-service-source-pglite','indexing-service-fixture','indexing-service-source-collect','indexing-service-proxy-source','indexing-service-proxy-fixture','indexing-service-proxy-evidence-controls','indexing-service-cache-source','indexing-service-cache-fixture','indexing-service-cache-evidence-controls'];
if(['indexing-actions-m39','indexing-profile-m40'].some(stage=>relative.startsWith('parity/runs/'+stage+'/')))requiredCommands.push(...indexingActionsStages);
if(relative.startsWith('parity/runs/indexing-profile-m40/'))requiredCommands.push(...indexingProfileStages);
check(full.commands.length===requiredCommands.length&&requiredCommands.every(name=>full.commands.filter(c=>c.name===name&&c.exit_code===0).length===1),'Missing, duplicate or failed aggregate stage');
const declarations=read('parity/test-ledger.json').tests.filter(c=>c.source_file==='packages/core/src/indexing/index.test.ts');
const selected=declarations.filter(c=>indexingServiceTargets.includes(c.id));
check(declarations.length===23&&declarations.reduce((n,c)=>n+c.assertions.length,0)===42&&selected.length===15&&selected.reduce((n,c)=>n+c.assertions.length,0)===33,'Pinned original census changed');
check(equal(selected.filter(c=>c.type_error_directives.length).map(c=>c.id),indexingServiceTypeGaps)&&selected.reduce((n,c)=>n+c.type_error_directives.length,0)===3,'Exact three source static gaps required');
const fixtureFile='testdata/indexing/source-indexing-service-live.json.gz',fixture=compressed(fixtureFile);
check(fixture.format==='indexing-service-marked-v1'&&fixture.source_credit===0&&equal(fixture.targets,indexingServiceTargets)&&equal(fixture.type_gaps,indexingServiceTypeGaps),'Exact runtime-only fixture format required');
const assertSupplementalOriginal=(original,file,titles)=>{
 check(original.success===true&&original.numPassedTests===titles.length&&original.numFailedTests===0&&original.numPendingTests===0&&original.numTotalTests===titles.length,'Exact supplemental result totals required');
 check(original.testResults?.length===1&&path.resolve(original.testResults[0].name)===root+'/'+file,'Exact supplemental executed source file required');
 const assertions=original.testResults[0].assertionResults;
 check(assertions?.length===titles.length&&titles.every(title=>assertions.filter(a=>a.title===title&&a.fullName===title&&(a.ancestorTitles??[]).length===0&&a.status==='passed').length===1),'Exact supplemental test labels/status required');
};
const sourceRecords={};
for(const backend of ['postgres','pglite']){
 const source=directory+'/indexing-source-'+backend,result=read(source+'/result.json'),original=read(source+'/original.json'),capture=compressed(source+'/calls.json.gz');
 check(result.complete&&result.success&&result.exit_code===0&&result.backend===backend&&result.environment_adapted&&result.pristine_file===false&&result.adapter_verified_after&&result.counts.passed===23&&!result.counts.failed&&!result.counts.pending&&result.inputs.every(valid),'Complete unchanged full original required');
 check(valid(result.context_archive)&&path.resolve(root,result.context_archive.file)===source+'/context-archive.json','Exact source archive changed');
 const archive=read(result.context_archive.file);
 check(archive.project_root===root&&archive.evidence_root===source&&archive.verification_phase==='before-dispose'&&Object.values(archive.entries).every(e=>validateContextEntry({file:e.executed_file,sha256:e.sha256},archive)),'Persisted source context differs');
 const start=Date.parse(result.started_utc),end=Date.parse(result.finished_utc);
 check(Number.isFinite(start)&&Number.isFinite(end)&&Date.parse(full.started_utc)<=start&&start<=end&&end<=Date.parse(prepared.recorded_utc),'Source outside current preparation window');
 check(original.success&&original.numPassedTests===23&&original.numFailedTests===0&&original.numPendingTests===0&&equal(capture.cases,fixture.backend_cases[backend]),'Fixture differs from actual complete original');
 assertIndexingServiceCapture(capture,declarations);
 for(const declaration of declarations){
  check(sameOriginalDeclaration(capture.cases.find(c=>c.id===declaration.id),declaration),'Immutable source declaration changed');
  const matches=original.testResults.flatMap(r=>r.assertionResults).filter(c=>c.title===declaration.title);
  check(matches.length===1&&matches[0].status==='passed','Original did not pass exactly once: '+declaration.id);
 }
 check(fixture.captures[backend].capture.sha256===hash(source+'/calls.json.gz').sha256&&Object.values(fixture.captures[backend]).flat().every(valid),'Fixture provenance changed');
 sourceRecords[backend]={source,result};
}
const proxyDirectory=directory+'/indexing-proxy',proxyResult=read(proxyDirectory+'/result.json'),proxyCapture=read(proxyDirectory+'/calls.json'),proxyFixture=read('testdata/indexing/source-indexing-service-proxy.json'),proxyOriginal=read(proxyDirectory+'/original.json');
check(proxyResult.complete&&proxyResult.success&&proxyResult.exit_code===0&&proxyResult.source_credit===0&&proxyResult.adapter_verified_after&&proxyResult.counts.passed===1&&!proxyResult.counts.failed&&!proxyResult.counts.pending&&proxyResult.inputs.every(valid)&&valid(proxyResult.context_archive)&&proxyOriginal.success,'Complete genuine source reflection probe required');
check(path.resolve(root,proxyResult.context_archive.file)===proxyDirectory+'/context-archive.json','Exact proxy context archive required');
const proxyArchive=read(proxyResult.context_archive.file);
check(proxyArchive.project_root===root&&proxyArchive.evidence_root===proxyDirectory&&Object.values(proxyArchive.entries).every(e=>validateContextEntry({file:e.executed_file,sha256:e.sha256},proxyArchive)),'Proxy context bytes changed');
check(Date.parse(full.started_utc)<=Date.parse(proxyResult.started_utc)&&Date.parse(proxyResult.started_utc)<=Date.parse(proxyResult.finished_utc)&&Date.parse(proxyResult.finished_utc)<=Date.parse(prepared.recorded_utc),'Proxy proof outside current aggregate');
assertSupplementalOriginal(proxyOriginal,'tools/source-indexing-service-proxy.test.ts',['actual source event proxy reflection and invalidation']);
assertIndexingServiceProxyCapture(proxyCapture);const {evidence:proxyEvidence,...proxyData}=proxyFixture;
check(equal(proxyData,proxyCapture)&&Object.values(proxyEvidence).flat().every(valid)&&path.resolve(root,proxyEvidence.capture.file)===proxyDirectory+'/calls.json','Native proxy fixture differs from current actual source');
const sourceOnly=read(directory+'/indexing-source-verification.json');check(sourceOnly.complete&&sourceOnly.success&&sourceOnly.source_credit===0&&valid(sourceOnly.fixture),'Current source-only collector required');
const cacheDirectory=directory+'/indexing-cache',cacheResult=read(cacheDirectory+'/result.json'),cacheCapture=read(cacheDirectory+'/calls.json'),cacheFixture=read('testdata/indexing/source-indexing-service-cache.json'),cacheOriginal=read(cacheDirectory+'/original.json');
check(cacheResult.complete&&cacheResult.success&&cacheResult.exit_code===0&&cacheResult.source_credit===0&&cacheResult.adapter_verified_after&&cacheResult.counts.passed===4&&!cacheResult.counts.failed&&!cacheResult.counts.pending&&cacheResult.inputs.every(valid)&&valid(cacheResult.context_archive)&&cacheOriginal.success,'Complete genuine delayed-cache/error-brand source required');
check(path.resolve(root,cacheResult.context_archive.file)===cacheDirectory+'/context-archive.json','Exact cache context archive required');
const cacheArchive=read(cacheResult.context_archive.file);
check(cacheArchive.project_root===root&&cacheArchive.evidence_root===cacheDirectory&&Object.values(cacheArchive.entries).every(e=>validateContextEntry({file:e.executed_file,sha256:e.sha256},cacheArchive)),'Cache source context bytes changed');
check(Date.parse(full.started_utc)<=Date.parse(cacheResult.started_utc)&&Date.parse(cacheResult.started_utc)<=Date.parse(cacheResult.finished_utc)&&Date.parse(cacheResult.finished_utc)<=Date.parse(prepared.recorded_utc),'Cache source proof outside current aggregate');
assertSupplementalOriginal(cacheOriginal,'tools/source-indexing-service-cache.test.ts',['actual cached getBalance returns before successful insert','actual cached getBalance returns before failed insert','actual indexing name-spoofed user error classification','actual indexing BaseError classification']);
assertIndexingServiceCacheCapture(cacheCapture);const {evidence:cacheEvidence,...cacheData}=cacheFixture;
check(equal(cacheData,cacheCapture)&&Object.values(cacheEvidence).flat().every(valid)&&path.resolve(root,cacheEvidence.capture.file)===cacheDirectory+'/calls.json','Native cache fixture differs from current source');
const supplementalTests=['TestSourceIndexingServiceCache','TestClientAsyncCacheOwnership','TestDatabaseErrorBrandNotDisplayName','TestServiceErrorBrandNotDisplayName','TestSourceIndexingServiceProxy','TestPropertyViewReadOrderAndPresence','TestPropertyViewInvalidAccessStickyAndMutation','TestPropertyViewWritesDeleteAndBindingIdentity','TestPropertyViewHasKeysAndDisabledInspection','TestPropertyViewComponentsAndValidation','TestColumnIncludeSourceOrder','TestClientEventBalanceAndIdentity','TestClientPersistentCacheAndErrors','TestClientHTTPTransport','TestDatabaseErrorSourceClassification','TestDatabaseStickyOnlyRetryable','TestDatabaseAdaptersRealBackends','TestDatabasePostgresRealConnectionLossSticky','TestServiceSupplementalTwoChainIdentityAndSetup','TestServiceSupplementalStickyErrorTiming','TestServiceSupplementalConvergenceAndInvalidation','TestServiceSupplementalCancellationAndReentrancy'];
const executions=[];
for(const mode of ['normal','race']){
 const base=mode==='normal'?'go-test':'go-race',log=directory+'/native/'+base+'.jsonl',manifest=read(directory+'/native/'+base+'-execution.json');
 check(manifest.complete&&manifest.success&&manifest.scope==='all_module_packages'&&manifest.race===(mode==='race')&&manifest.driver_sha256===hash('tools/go-tests.mjs').sha256&&equal(manifest.source_fingerprint,fingerprint)&&equal(manifest.finished_source_fingerprint,fingerprint),'Current complete native mode required');
 const start=Date.parse(manifest.started_utc),end=Date.parse(manifest.finished_utc);
 check(Number.isFinite(start)&&Number.isFinite(end)&&Date.parse(prepared.recorded_utc)<=start&&start<=end&&end<=Date.parse(full.finished_utc),'Native execution outside prepared aggregate');
 const events=fs.readFileSync(log,'utf8').trim().split('\n').map(JSON.parse);
 check(events.every(e=>Number.isFinite(Date.parse(e.Time))&&Date.parse(e.Time)>=start-1000&&Date.parse(e.Time)<=end+1000),'Native log outside mode execution');
 check(!events.some(e=>e.Test&&['skip','fail'].includes(e.Action)),'Native aggregate contains skip/failure');
 const passed=(test,pkg='ponder.local/ponder-go/indexing')=>events.filter(e=>e.Package===pkg&&e.Test===test&&e.Action==='pass').length===1;
 for(const pkg of manifest.packages)for(const test of pkg.listed_functions)check(passed(test,pkg.package),'Listed native function missing or duplicated');
 if(isM39){for(const test of indexingActionsSupplementalTests)check(passed(test),'Required M39 supplemental missing: '+test);for(const backend of ['postgres','pglite'])for(const name of indexingActionsAdmissionScenarios)check(passed('TestClientRealCacheAdmissionLifecycle/'+backend+'/'+name),'Required real admission lifecycle missing');}
 for(const test of supplementalTests)check(passed(test),'Required supplemental missing: '+test);
 for(const name of ['delayed-cache-insert-success','delayed-cache-insert-failure'])check(passed('TestSourceIndexingServiceCache/'+name),'Source cache replay missing: '+name);
 check(passed('TestSourceHistoricalIndexingServiceComposition','ponder.local/ponder-go/runtime'),'Actual M37 stream to real handler/store composition missing');
 const artifactFile=directory+'/native/indexing-service-'+mode+'.json',artifact=read(artifactFile);
 const stage=full.commands.find(c=>c.name===(mode==='normal'?'go-normal':'go-race'));
 check(stage.native_indexing_output===artifactFile&&artifact.source_credit===0&&artifact.cases?.length===30,'Mandatory mode-specific actual native artifact missing');
 check(artifact.source_fixture_sha256===hash(fixtureFile).sha256,'Native artifact source fixture differs');
 for(const backend of ['postgres','pglite']){
  const records=artifact.cases.filter(c=>c.backend===backend);
  check(records.length===15&&new Set(records.map(c=>c.id)).size===15,'Exact native backend artifact case set required');
  for(const source of selected){const record=records.find(c=>c.id===source.id),captured=fixture.backend_cases[backend].find(c=>c.id===source.id);check(record&&record.pass===true&&record.name===source.full_name&&record.source_assertions_checked===source.assertions.length&&record.runtime_only_type_gap===indexingServiceTypeGaps.includes(source.id)&&record.callbacks===captured.calls[0].methods.reduce((n,m)=>n+m.callbacks.length,0)&&/^[a-f0-9]{64}$/.test(record.observation_sha256)&&record.digest_input&&crypto.createHash('sha256').update(goJSON(record.digest_input)).digest('hex')===record.observation_sha256,'Native observed artifact differs from exact original');}
  check(records.reduce((n,c)=>n+c.callbacks,0)===1014&&records.reduce((n,c)=>n+c.insert_calls,0)===2&&records.reduce((n,c)=>n+c.access_calls,0)===2005,'Actual native operation census differs');
 }
 for(const backend of ['postgres','pglite']){
  for(const name of ['user-error-name-is-not-brand','actual-base-error-identity'])check(passed('TestSourceIndexingServiceCache/'+name+'/'+backend),'Source real-store error-brand replay missing');
  const cases=selected.map(c=>({source_id:c.id,go_test:'TestSourceIndexingService/'+backend+'/'+c.id,runtime_passed:true,native_type_obligations:c.type_error_directives.length,native_type_status:c.type_error_directives.length?'pending_exact_static_counterpart':'not_applicable',formal_credit:!indexingServiceTypeGaps.includes(c.id)}));
  for(const c of cases)check(passed(c.go_test),'Original native case missing: '+c.go_test);
  for(const test of ['TestDatabaseAdaptersRealBackends','TestServiceSupplementalTwoChainIdentityAndSetup','TestServiceSupplementalStickyErrorTiming','TestServiceSupplementalConvergenceAndInvalidation','TestServiceSupplementalCancellationAndReentrancy'])check(passed(test+'/'+backend),'Required actual backend supplemental missing: '+test+'/'+backend);
  check(passed('TestSourceHistoricalIndexingServiceComposition/'+backend,'ponder.local/ponder-go/runtime'),'Actual backend M37 composition missing');
  const {source,result}=sourceRecords[backend];
  executions.push({mode,backend,complete:true,cases,artifact:hash(artifactFile),native_test_prefix:'TestSourceIndexingService/'+backend,log:hash(log),native:hash(log),capture:hash(source+'/calls.json.gz'),original:hash(source+'/original.json'),result:hash(source+'/result.json'),source_context:result.inputs,context_archive:result.context_archive});
 }
}
const report={complete:true,success:true,scope:'complete_module_source_indexing_service',run_dir:relative,environment_adapted:true,pristine_file:false,summary:{source_cases:23,source_assertions:42,targeted_cases:15,targeted_assertions:33,source_error_directives:3,runtime_passed_cases:15,complete_mapping_candidates:12,native_type_pending_cases:3,native_type_obligations_verified:0,supplementary_source_credit:0,proxy_source_operations:23,proxy_native_operations:isM39?23:22,proxy_descriptor_gap:isM39?0:1,cache_invocation_prefix_gap:!isM39,cache_invocation_prefix_scope:isM39?'strict_standalone_prepared_admission_only':'legacy_sync_cache_adapter',cache_read_visibility_equivalence:false,source_sync_table_snapshots:'archival_only',native_business_database_assertions:'original_insert_find_only'},type_gaps:indexingServiceTypeGaps,limitations:{cache_invocation_prefix:isM39?'Closed for NewStrictClientSet with real standalone AsyncSourceRPCRequestCache admission; legacy NewClientSet synchronous-only fallback retains weaker prefix timing. Transaction-bound async construction is rejected. Source-vs-native concurrent cache-read visibility is not established: native Store mutex serializes an executing write and read. Source delayed-write probe is separate held-completion evidence, not a real delayed source-DB/read comparison. PGlite cross-Store Backend.Begin waits shared backend mutex non-contextually, in addition to in-flight IO limits; no commit-order or concurrency equivalence claimed.':'Source invokes insert_begin synchronously before getBalance returns. Native asynchronous persistence may begin after return. Compared behavior is nonawaited completion, explicit release/settle and ignored late failure; raw source timelines are retained, not rewritten.',source_sync_table_snapshots:'Nine physical_before/physical_after sync tables are archival-only source observations, not natively compared. Original business database insert/find assertions are executed against real native IndexingStore.',proxy_descriptor:isM39?'Separate target descriptor query and enumerable filtering now replay all 23 original operations; arbitrary DefineOwnProperty and symbol descriptors remain outside supported scope.':'Source Object.keys exposes JS descriptor behavior without an equivalent Go facade. Source23 operations, native22 supported operations; no fake empty enumeration.'},full_verification:hash(directory+'/result.json'),prepared_native_inputs:full.prepared_native_inputs,context_archive:full.context_archive,executions,supplemental_tests:supplementalTests,proxy:{capture:hash(proxyDirectory+'/calls.json'),result:hash(proxyDirectory+'/result.json'),original:hash(proxyDirectory+'/original.json'),context_archive:proxyResult.context_archive,source_context:proxyResult.inputs,fixture:hash('testdata/indexing/source-indexing-service-proxy.json'),source_credit:0},cache:{capture:hash(cacheDirectory+'/calls.json'),result:hash(cacheDirectory+'/result.json'),original:hash(cacheDirectory+'/original.json'),context_archive:cacheResult.context_archive,source_context:cacheResult.inputs,fixture:hash('testdata/indexing/source-indexing-service-cache.json'),source_credit:0},source_only:hash(directory+'/indexing-source-verification.json'),inputs:[...harness,'indexing/access.go','indexing/client.go','indexing/database.go','indexing/service.go','indexing/types.go','indexing/source_parity_test.go','indexing/source_cache_test.go','indexing/source_proxy_test.go','indexing/service_semantics_test.go','indexing/database_integration_test.go','runtime/source_indexing_service_integration_test.go',fixtureFile,'parity/source-indexing-service-declarations.json'].map(hash)};
fs.writeFileSync(destination,JSON.stringify(report,null,2)+'\n');console.log(report.summary);
