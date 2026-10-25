// Twelve exact originals only; controlled source probes receive no source credit.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {gunzipSync} from 'node:zlib';
import {sourceFingerprint} from './go-source-fingerprint.mjs';
import {validateContextEntry} from './loopback-context.mjs';
import {sameOriginalDeclaration} from './source-runtime-historical-evidence.mjs';
import {indexingProfileTargets,assertIndexingProfileCapture,assertIndexingProfileProbeCapture} from './source-indexing-profile-evidence.mjs';
import {indexingProfileStages,indexingProfileHarness,indexingProfileSupplementalTests,indexingProfileStoreTests,indexingProfileStoreScenarios} from './indexing-profile-acceptance.mjs';
const root=path.resolve(import.meta.dirname,'..'),arg=n=>process.argv.find(a=>a.startsWith('--'+n+'='))?.slice(n.length+3);
const check=(v,m)=>{if(!v)throw Error(m)},equal=(a,b)=>JSON.stringify(a)===JSON.stringify(b);
check(arg('run-dir')&&arg('output'),'Explicit --run-dir and --output required');
const directory=path.resolve(root,arg('run-dir')),relative=path.relative(root,directory),destination=path.resolve(root,arg('output'));
check(relative.startsWith('parity/runs/indexing-profile-m40/')&&destination.startsWith(root+'/parity/runs/'),'Scoped M40 run/output required');
const read=f=>JSON.parse(fs.readFileSync(path.resolve(root,f))),compressed=f=>JSON.parse(gunzipSync(fs.readFileSync(path.resolve(root,f))));
const hash=f=>({file:path.relative(root,path.resolve(root,f)),sha256:crypto.createHash('sha256').update(fs.readFileSync(path.resolve(root,f))).digest('hex')});
const valid=r=>r&&fs.existsSync(path.resolve(root,r.file))&&hash(r.file).sha256===r.sha256;
const goJSON=v=>{const quote=x=>JSON.stringify(x).replace(/[<>&\u2028\u2029]/g,c=>'\\u'+c.charCodeAt(0).toString(16).padStart(4,'0'));return Array.isArray(v)?'['+v.map(goJSON).join(',')+']':v&&typeof v==='object'?'{'+Object.keys(v).sort().map(k=>quote(k)+':'+goJSON(v[k])).join(',')+'}':quote(v)};
const full=read(directory+'/result.json'),fingerprint=sourceFingerprint(root),prepared=read(full.prepared_native_inputs.file);
check(full.complete&&full.success&&full.environment_adapted&&full.pristine_file===false&&full.source_manifest_verified_after,'Complete successful adapted aggregate required');
check(equal(full.native_inputs,fingerprint)&&equal(full.finished_native_inputs,fingerprint)&&equal(prepared.fingerprint,fingerprint)&&prepared.files.every(valid),'Current native inventory differs');
check(valid(full.prepared_native_inputs)&&valid(full.context_archive)&&full.harness_inputs.every(valid)&&indexingProfileHarness.every(f=>full.harness_inputs.some(r=>r.file===f)),'Complete current source/harness/context required');
const archive=(ref,expected)=>{check(valid(ref)&&path.resolve(root,ref.file)===expected+'/context-archive.json','Exact archive ref required');const a=read(ref.file);check(a.project_root===root&&a.evidence_root===expected&&a.verification_phase==='before-dispose'&&Object.values(a.entries).every(e=>validateContextEntry({file:e.executed_file,sha256:e.sha256},a)),'Archived actual context differs')};
archive(full.context_archive,directory);
check(indexingProfileStages.every(n=>full.commands.filter(c=>c.name===n&&c.exit_code===0).length===1),'Missing/duplicate M40 stage');
const oldFile='parity/runs/source-indexing-actions-verification.json',old=read(oldFile);
check(old.complete&&old.success&&old.run_dir===relative&&valid(old.full_verification)&&old.full_verification.sha256===hash(directory+'/result.json').sha256&&old.inputs.every(valid),'Current complete M39 regression proof required');
const declarations=read('parity/test-ledger.json').tests.filter(t=>['packages/core/src/indexing/profile.test.ts','packages/core/src/indexing/client.test.ts'].includes(t.source_file)),selected=declarations.filter(t=>indexingProfileTargets.includes(t.id));
check(declarations.length===24&&declarations.reduce((n,t)=>n+t.assertions.length,0)===63&&selected.length===12&&selected.reduce((n,t)=>n+t.assertions.length,0)===30&&selected.every(t=>t.type_error_directives.length===0),'Exact12/30/0 census required');
const fixtureFile='testdata/indexing/source-indexing-profile.json.gz',fixture=compressed(fixtureFile),probeFile='testdata/indexing/source-indexing-profile-probes.json.gz',probeFixture=compressed(probeFile);
check(fixture.source_credit===0&&fixture.format==='indexing-profile-source-v1'&&equal(fixture.targets,indexingProfileTargets)&&probeFixture.source_credit===0&&probeFixture.real_rpc===false&&probeFixture.real_database===false,'Honest original and controlled source-probe fixture formats required');
const sourceOnly=read(directory+'/profile-source-verification.json');check(sourceOnly.complete&&sourceOnly.success&&sourceOnly.source_credit===0&&valid(sourceOnly.fixture)&&sourceOnly.fixture.sha256===hash(fixtureFile).sha256,'Current source-only collector required');
const sources={};
for(const backend of ['postgres','pglite']){
 const source=directory+'/profile-source-'+backend,result=read(source+'/result.json'),original=read(source+'/original.json'),capture=compressed(source+'/calls.json.gz');
 check(result.complete&&result.success&&result.exit_code===0&&result.backend===backend&&result.environment_adapted&&result.pristine_file===false&&result.adapter_verified_after&&result.counts.passed===24&&!result.counts.failed&&!result.counts.pending&&result.inputs.every(valid),'Complete unchanged source24 required');
 archive(result.context_archive,source);
 check(Date.parse(full.started_utc)<=Date.parse(result.started_utc)&&Date.parse(result.started_utc)<=Date.parse(result.finished_utc)&&Date.parse(result.finished_utc)<=Date.parse(prepared.recorded_utc),'Source outside preparation window');
 check(original.success&&original.numPassedTests===24&&original.numFailedTests===0&&original.numPendingTests===0&&equal(capture.cases,fixture.backend_cases[backend]),'Exact source original/fixture differs');
 assertIndexingProfileCapture(capture,declarations);
 for(const d of declarations){
  check(sameOriginalDeclaration(capture.cases.find(c=>c.id===d.id),d),'Original declaration/state/inputs/assertions changed');
  const files=original.testResults.filter(f=>f.name.endsWith(d.source_file.replace('packages/core/',''))),matches=files.flatMap(f=>f.assertionResults).filter(t=>[...(t.ancestorTitles??[]),t.title].join(' > ')===d.full_name);
  check(files.length===1&&matches.length===1&&matches[0].status==='passed','Exact original file/name/status required');
 }
 check(Object.values(fixture.captures[backend]).flat().every(valid)&&fixture.captures[backend].capture.sha256===hash(source+'/calls.json.gz').sha256,'Fixture source provenance differs');
 const probe=directory+'/profile-probes-'+backend,pr=read(probe+'/result.json'),po=read(probe+'/original.json'),pc=read(probe+'/calls.json');
 check(pr.complete&&pr.success&&pr.exit_code===0&&pr.backend===backend&&pr.environment_adapted&&pr.pristine_file===false&&pr.adapter_verified_after&&pr.counts.passed===2&&!pr.counts.failed&&!pr.counts.pending&&pr.inputs.every(valid),'Complete actual controlled source probe required');archive(pr.context_archive,probe);
 check(Date.parse(full.started_utc)<=Date.parse(pr.started_utc)&&Date.parse(pr.started_utc)<=Date.parse(pr.finished_utc)&&Date.parse(pr.finished_utc)<=Date.parse(prepared.recorded_utc),'Probe outside preparation window');
 const probeTitles=['source profile derivation and hint edge vectors','source predictive client causal boundary vectors'];
 check(po.success&&po.numPassedTests===2&&po.numTotalTests===2&&po.numFailedTests===0&&po.numPendingTests===0&&po.testResults.length===1&&path.resolve(po.testResults[0].name)===root+'/tools/source-indexing-profile-probes.test.ts'&&po.testResults[0].assertionResults.length===2&&probeTitles.every(n=>po.testResults[0].assertionResults.filter(a=>a.fullName===n&&a.status==='passed').length===1),'Exact controlled source probe titles/status required');
 assertIndexingProfileProbeCapture(pc);check(equal(pc.cases,probeFixture.backend_cases[backend])&&Object.values(probeFixture.captures[backend]).flat().every(valid)&&probeFixture.captures[backend].capture.sha256===hash(probe+'/calls.json').sha256,'Source probe fixture differs');
 sources[backend]={source,result,probe,pr};
}
const executions=[];
for(const mode of ['normal','race']){
 const base=mode==='normal'?'go-test':'go-race',log=directory+'/native/'+base+'.jsonl',manifest=read(directory+'/native/'+base+'-execution.json');
 check(manifest.complete&&manifest.success&&manifest.scope==='all_module_packages'&&manifest.race===(mode==='race')&&manifest.driver_sha256===hash('tools/go-tests.mjs').sha256&&equal(manifest.source_fingerprint,fingerprint)&&equal(manifest.finished_source_fingerprint,fingerprint),'Complete current native mode required');
 const start=Date.parse(manifest.started_utc),end=Date.parse(manifest.finished_utc);check(Date.parse(prepared.recorded_utc)<=start&&start<=end&&end<=Date.parse(full.finished_utc),'Native outside prepared aggregate');
 const events=fs.readFileSync(log,'utf8').trim().split('\n').map(JSON.parse),passed=(name,pkg='ponder.local/ponder-go/indexing')=>events.filter(e=>e.Package===pkg&&e.Test===name&&e.Action==='pass').length===1;
 check(events.every(e=>Number.isFinite(Date.parse(e.Time))&&Date.parse(e.Time)>=start-1000&&Date.parse(e.Time)<=end+1000)&&!events.some(e=>e.Test&&['skip','fail'].includes(e.Action)),'Native failure/skip or timestamp mismatch');
 for(const p of manifest.packages)for(const name of p.listed_functions)check(passed(name,p.package),'Listed native test missing/duplicate');
 for(const name of indexingProfileSupplementalTests)check(passed(name),'Current supplemental missing: '+name);
 for(const name of indexingProfileStoreTests)check(passed(name,'ponder.local/ponder-go/store'),'Current read-queue production test missing: '+name);
 for(const backend of ['postgres','pglite'])for(const name of indexingProfileStoreScenarios)check(passed('TestSourceRPCAsyncReadBackends/'+backend+'/'+name,'ponder.local/ponder-go/store'),'Actual backend read-queue lifecycle missing');
 const artifactFile=directory+'/native/indexing-profile-'+mode+'.json',artifact=read(artifactFile),stage=full.commands.find(c=>c.name==='go-'+mode);
 check(stage.native_indexing_profile_output===artifactFile&&artifact.source_credit===0&&artifact.source_fixture_sha256===hash(fixtureFile).sha256&&artifact.cases.length===24,'Mandatory observed original artifact required');
 const probeArtifactFile=directory+'/native/indexing-profile-probes-'+mode+'.json',probeArtifact=read(probeArtifactFile);
 check(stage.native_indexing_profile_probes_output===probeArtifactFile&&probeArtifact.source_credit===0&&probeArtifact.source_fixture_sha256===hash(probeFile).sha256,'Mandatory observed supplemental artifact required');
 for(const backend of ['postgres','pglite']){
  const records=artifact.cases.filter(c=>c.backend===backend);check(records.length===12&&new Set(records.map(c=>c.id)).size===12,'Exact native twelve case set');
  for(const s of selected){const r=records.find(c=>c.id===s.id),prefix=s.source_file.endsWith('/profile.test.ts')?'TestSourceIndexingProfile':'TestSourceIndexingPrefetch';check(r&&r.pass===true&&r.name===s.full_name&&r.source_assertions_checked===s.assertions.length&&r.digest_input&&crypto.createHash('sha256').update(goJSON(r.digest_input)).digest('hex')===r.observation_sha256,'Actual original assertion artifact differs');check(passed(prefix+'/'+backend+'/'+s.id),'Native original missing');}
  check(records.filter(r=>['ff30deb8b9aca842519b','4ace7b36bca44245e4b7'].includes(r.id)).reduce((n,r)=>n+r.rpc_calls,0)===3,'Exact native original prefetch three-RPC census required');
  // Supplemental records carry the complete actual vector/scenario observations.
  for(const category of ['profile-edges','client-boundaries']){
   const records=probeArtifact.cases.filter(r=>r.backend===backend&&r.id===category),r=records[0];
   const sourceCategory=probeFixture.backend_cases[backend].find(c=>c.name===category),entries=sourceCategory[category==='profile-edges'?'vectors':'scenarios'],prefix=category==='profile-edges'?'TestSourceIndexingProfileEdges':'TestSourceIndexingPrefetchProbes';
   check(passed(prefix+'/'+backend)&&entries.every(e=>passed(prefix+'/'+backend+'/'+e.name.replaceAll(/\s/g,'_'))),'Every actual native source probe vector/scenario must pass');
   check(records.length===1&&r.pass===true&&r.count===entries.length&&r.digest_input&&crypto.createHash('sha256').update(goJSON(r.digest_input)).digest('hex')===r.observation_sha256,'Observed supplemental category missing');
  }
  const s=sources[backend];executions.push({mode,backend,complete:true,cases:selected.map(t=>({source_id:t.id,go_test:(t.source_file.endsWith('/profile.test.ts')?'TestSourceIndexingProfile':'TestSourceIndexingPrefetch')+'/'+backend+'/'+t.id,runtime_passed:true,formal_credit:true,native_type_obligations:0})),artifact:hash(artifactFile),probe_artifact:hash(probeArtifactFile),log:hash(log),native:hash(log),capture:hash(s.source+'/calls.json.gz'),original:hash(s.source+'/original.json'),result:hash(s.source+'/result.json'),source_context:s.result.inputs,context_archive:s.result.context_archive,probe:{capture:hash(s.probe+'/calls.json'),result:hash(s.probe+'/result.json'),original:hash(s.probe+'/original.json'),context_archive:s.pr.context_archive,source_context:s.pr.inputs}});
 }
 check(probeArtifact.cases.length===4,'Exactly four observed supplemental category/backend records required');
}
const report={complete:true,success:true,scope:'complete_module_source_indexing_profile',run_dir:relative,environment_adapted:true,pristine_file:false,summary:{source_cases:24,source_assertions:63,targeted_cases:12,targeted_assertions:30,source_error_directives:0,complete_mapping_candidates:12,supplementary_source_credit:0,profile_originals:10,prefetch_originals:2,unmapped_client_originals:12},supplemental_tests:indexingProfileSupplementalTests,store_tests:indexingProfileStoreTests,store_scenarios:indexingProfileStoreScenarios,limitations:{probe_boundary:'Supplemental source and native controlled RPC/database doubles prove causal boundary semantics; original prefetch cases separately use real source/native PostgreSQL/PGlite caches. Neither claims network scheduling parity.',client_scope:'Only existing action facade plus profile/prefetch. Twelve other client.test.ts originals remain unmapped. Not full cachedViemClient/raw transport or outer runner/readiness/cutover.',backend_scope:'Strict standalone prepared write and optional AsyncCacheReader prepared SELECT admission only; no new database topology. Transaction-bound adapters do not expose detached asynchronous readers. Legacy synchronous fallback, database read visibility/commit order, source batch insertion grouping, PGlite shared-lock and IO cancellation limitations remain. Full Viem custom-error decoding/wrapping is not claimed.',cross_chain:'Source canonical dedupe happens before chain grouping and excludes chain ID; preserved, not improved. Causal controlled lookup/admission probes do not establish complete database read-visibility or commit-order equivalence.'},full_verification:hash(directory+'/result.json'),prepared_native_inputs:full.prepared_native_inputs,context_archive:full.context_archive,previous_actions_proof:hash(oldFile),source_only:hash(directory+'/profile-source-verification.json'),executions,inputs:[...indexingProfileHarness,fixtureFile,probeFile,'parity/source-indexing-profile-declarations.json','rpc/async_cache_reader.go','store/source_rpc_async_read.go','store/source_rpc_async_read_test.go','store/source_rpc_async.go',...prepared.files.filter(f=>f.file.startsWith('indexing/')).map(f=>f.file)].map(hash)};
fs.writeFileSync(destination,JSON.stringify(report,null,2)+'\n');console.log(report.summary);
