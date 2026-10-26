// M41 supplements the existing canonical originals; it grants no source credit.
import fs from 'node:fs';import path from 'node:path';import crypto from 'node:crypto';
import {assertIndependentSourceCapture} from './independent-storage-evidence.mjs';
import {sourceFingerprint} from './go-source-fingerprint.mjs';
import {validateContextEntry} from './loopback-context.mjs';
import {independentStorageStages,independentStorageHarness} from './independent-storage-acceptance.mjs';
const root=path.resolve(import.meta.dirname,'..'),arg=n=>process.argv.find(a=>a.startsWith('--'+n+'='))?.slice(n.length+3);
const check=(v,m)=>{if(!v)throw Error(m)},equal=(a,b)=>JSON.stringify(a)===JSON.stringify(b);
check(arg('run-dir')&&arg('output'),'Explicit run-dir and output required');
const directory=path.resolve(root,arg('run-dir')),relative=path.relative(root,directory),destination=path.resolve(root,arg('output'));
check(['independent-storage-m41','multichain-atomic-m42','finite-historical-m43'].some(stage=>relative.startsWith('parity/runs/'+stage+'/'))&&destination.startsWith(root+'/parity/runs/'),'Scoped M41 run/output required');
const read=f=>JSON.parse(fs.readFileSync(path.resolve(root,f))),hash=f=>({file:path.relative(root,path.resolve(root,f)),sha256:crypto.createHash('sha256').update(fs.readFileSync(path.resolve(root,f))).digest('hex')});
const valid=r=>r&&fs.existsSync(path.resolve(root,r.file))&&hash(r.file).sha256===r.sha256;
const full=read(directory+'/result.json'),fingerprint=sourceFingerprint(root),prepared=read(full.prepared_native_inputs.file);
check(full.complete&&full.success&&full.source_manifest_verified_after&&full.environment_adapted&&full.pristine_file===false,'Complete adapted aggregate required');
check(equal(full.native_inputs,fingerprint)&&equal(full.finished_native_inputs,fingerprint)&&equal(prepared.fingerprint,fingerprint)&&prepared.files.every(valid),'Current prepared/native inputs differ');
check(valid(full.prepared_native_inputs)&&valid(full.context_archive)&&full.harness_inputs.every(valid)&&independentStorageHarness.every(f=>full.harness_inputs.some(r=>r.file===f)),'Current full harness inventory required');
check(full.commands.length===(relative.startsWith('parity/runs/finite-historical-m43/')?73:relative.startsWith('parity/runs/multichain-atomic-m42/')?69:65)&&independentStorageStages.every(n=>full.commands.filter(c=>c.name===n&&c.exit_code===0).length===1),'Missing/duplicate M41 aggregate stage');
const previous='parity/runs/source-indexing-profile-verification.json',prior=read(previous);
check(prior.complete&&prior.success&&prior.run_dir===relative&&valid(prior.full_verification)&&prior.full_verification.sha256===hash(directory+'/result.json').sha256&&prior.inputs.every(valid),'Complete current M40 regression proof required');
function archive(ref,expected){check(valid(ref)&&path.resolve(root,ref.file)===expected+'/context-archive.json','Exact context reference required');const a=read(ref.file);check(a.project_root===root&&a.evidence_root===expected&&a.verification_phase==='before-dispose'&&Object.values(a.entries).every(e=>validateContextEntry({file:e.executed_file,sha256:e.sha256},a)),'Context archive differs')}
archive(full.context_archive,directory);
const fixture=full.postgres_fixture;
check(fixture?.data_directory_verified===true&&fixture.receipt===relative+'-fixture'&&valid(fixture.preflight)&&fixture.preflight.file===relative+'/fixture-preflight.json'&&JSON.stringify(fixture.inputs?.map(r=>r.file))===JSON.stringify(['owned-directory.txt','show-data-directory.txt','initial-inventory.json','pinned-test-lifecycle.sha256','scope.txt','legacy-fixture-scope.txt'].map(f=>relative+'-fixture/'+f))&&fixture.inputs.every(valid),'Exact M41 PostgreSQL fixture provenance required');
const receipt=path.resolve(root,fixture.receipt),owned=fs.readFileSync(receipt+'/owned-directory.txt','utf8').trim(),initial=read(receipt+'/initial-inventory.json'),preflight=read(fixture.preflight.file);
check(fixture.directory===owned+'/data'&&fs.readFileSync(receipt+'/show-data-directory.txt','utf8').trim()===fixture.directory&&preflight.exit_code===0&&preflight.stdout.trim()===fixture.directory,'Independent SHOW data_directory checks disagree');
check(JSON.stringify([...initial.databases].sort())===JSON.stringify(['ponder_test','postgres','template0','template1'])&&initial.roles.every(r=>r==='agent'||r.startsWith('pg_')),'Fresh fixture inventory differs');
check(Date.parse(full.started_utc)<=Date.parse(preflight.checked_utc)&&Date.parse(preflight.checked_utc)<=Date.parse(full.commands[0].started_utc),'Fixture ownership was not verified before legacy source commands');

const source=[];
for(const backend of ['postgres','pglite']){
 const dir=directory+'/independent-source-'+backend,r=read(dir+'/result.json'),original=read(dir+'/original.json'),capture=read(dir+'/calls.json');
 check(r.complete&&r.success&&r.exit_code===0&&r.backend===backend&&r.source_credit===0&&r.environment_adapted&&r.pristine_file===false&&r.adapter_verified_after&&r.counts.passed===1&&!r.counts.failed&&!r.counts.pending&&r.inputs.every(valid),'Complete actual source lifecycle probe required');
 check(original.success&&original.numTotalTests===1&&original.numPassedTests===1&&original.numFailedTests===0&&original.numPendingTests===0,'Actual source assertions did not pass');
 const files=original.testResults,assertions=files.flatMap(f=>f.assertionResults);
 check(files.length===1&&path.resolve(files[0].name)===root+'/tools/source-independent-storage.test.ts'&&assertions.length===1&&assertions[0].title==='actual source persistent indexing cache/client across userQB bindings'&&assertions[0].fullName==='actual source persistent indexing cache/client across userQB bindings'&&assertions[0].status==='passed','Exact source probe file/title/status required');
 archive(r.context_archive,dir);check(Date.parse(full.started_utc)<=Date.parse(r.started_utc)&&Date.parse(r.started_utc)<=Date.parse(r.finished_utc)&&Date.parse(r.finished_utc)<=Date.parse(prepared.recorded_utc),'Source outside preparation window');
 assertIndependentSourceCapture(capture,backend);
 check(capture.source_credit===0&&capture.real_database===true&&capture.real_rpc===false&&capture.backend===backend&&capture.batches.length===4&&capture.identities.length===4&&capture.identities.every(v=>Object.values(v).every(x=>x===true)),'Actual source identity/binding observations incomplete');
 check(capture.calls.filter(c=>c.operation==='rpc').length===6&&capture.calls.filter(c=>c.operation==='cache-write-admit').length===6&&capture.calls.filter(c=>c.operation==='cache-write-admit').every(c=>c.settled===true),'Actual source RPC and completed insert observations missing');
 check(capture.pending_before.cache.length===4&&capture.final.cache.length===6,'Source pending write independence missing');
 check(capture.final.business.length===(backend==='postgres'?2:4)&&typeof capture.intentional_native_difference==='string','PG/PGlite rollback distinction missing');
 source.push({backend,result:hash(dir+'/result.json'),original:hash(dir+'/original.json'),capture:hash(dir+'/calls.json'),context_archive:r.context_archive});
}
const nativeFiles=['indexing/owned_integration_test.go','indexing/owned_lifecycle_test.go','indexing/database_binding_test.go','store/indexing_session_test.go','store/independent_storage_test.go'];
const expected=nativeFiles.flatMap(file=>[...fs.readFileSync(root+'/'+file,'utf8').matchAll(/^func (Test\w+)\(/gm)].map(m=>({package:'ponder.local/ponder-go/'+file.split('/')[0],name:m[1]})));
const executions=[];
for(const mode of ['normal','race']){
 const base=mode==='normal'?'go-test':'go-race',log=directory+'/native/'+base+'.jsonl',manifest=read(directory+'/native/'+base+'-execution.json');
 check(manifest.complete&&manifest.success&&manifest.scope==='all_module_packages'&&manifest.race===(mode==='race')&&manifest.driver_sha256===hash('tools/go-tests.mjs').sha256&&equal(manifest.source_fingerprint,fingerprint)&&equal(manifest.finished_source_fingerprint,fingerprint),'Complete current native mode required');
 const start=Date.parse(manifest.started_utc),end=Date.parse(manifest.finished_utc);check(Date.parse(prepared.recorded_utc)<=start&&start<=end&&end<=Date.parse(full.finished_utc),'Native outside prepared aggregate');
 const events=fs.readFileSync(log,'utf8').trim().split('\n').map(JSON.parse),passed=(name,pkg)=>events.filter(e=>e.Package===pkg&&e.Test===name&&e.Action==='pass').length===1;
 check(events.every(e=>Number.isFinite(Date.parse(e.Time))&&Date.parse(e.Time)>=start-1000&&Date.parse(e.Time)<=end+1000)&&!events.some(e=>e.Test&&['skip','fail'].includes(e.Action)),'Native failure/skip or timestamp mismatch');
 for(const p of manifest.packages)for(const name of p.listed_functions)check(passed(name,p.package),'Listed native test missing/duplicate');
 for(const t of expected)check(passed(t.name,t.package),'Current M41 test missing: '+t.name);
 for(const backend of ['postgres','pglite'])for(const [pkg,name] of [['indexing','TestOwnedPersistentHandlerTransactions'],['indexing','TestOwnedPendingCacheAcrossBindingExit'],['indexing','TestOwnedCloseWaitsActiveBinding'],['store','TestIndexingSessionRealTransactions']])check(passed(name+'/'+backend,'ponder.local/ponder-go/'+pkg),'Actual backend M41 integration missing: '+name+'/'+backend);
 executions.push({mode,source_credit:0,log:hash(log),manifest:hash(directory+'/native/'+base+'-execution.json'),tests:expected});
}
const report={complete:true,success:true,scope:'independent_storage_and_reusable_bindings',run_dir:relative,source_credit:0,environment_adapted:true,pristine_file:false,full_verification:hash(directory+'/result.json'),prepared_native_inputs:full.prepared_native_inputs,context_archive:full.context_archive,previous_profile_proof:hash(previous),postgres_fixture:fixture,source,executions,inputs:[...independentStorageHarness,...prepared.files.filter(f=>/^(indexing|store)\//.test(f.file)).map(f=>f.file)].map(hash),limitations:{source_rpc:'Deterministic RPC boundary, actual production source clients/cache and PostgreSQL/PGlite SQL; no network schedule equivalence.',source_namespace:'Fixture-local schema identifiers; no production source modification or preexisting namespace reuse.',pending_gate:'Source wrapper admission delays original insertion before SQL; eventual SQL is real. Native actual queue-admission tests are independent. No concurrent SQL scheduling/commit-order equivalence claim.',pglite:'Native separate owned persistent directories and real business SQL rollback differ intentionally from source same-instance callback-only transaction.',runner:'No outer historical/realtime runner, multichain atomic setup, readiness or cutover claim.',cache_durability:'WaitCacheWrites means settled, not necessarily persisted; explicit shutdown/caller cancellation may cancel independent work.'}};
fs.writeFileSync(destination,JSON.stringify(report,null,2)+'\n');console.log({source_credit:0,source_probes:source.length,native_tests:expected.length,modes:executions.length});
