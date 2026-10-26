// M43 supplements the existing canonical originals; it grants no source credit.
import fs from 'node:fs';import path from 'node:path';import crypto from 'node:crypto';
import {assertFiniteHistoricalCapture,finiteHistoricalProbeNames} from './source-finite-historical-evidence.mjs';
import {sourceFingerprint} from './go-source-fingerprint.mjs';
import {validateContextEntry} from './loopback-context.mjs';
import {finiteHistoricalStages,finiteHistoricalHarness,finiteHistoricalNativeFiles,finiteHistoricalDualBackendTests,finiteHistoricalOwnedBackendTests,finiteHistoricalPostgresOnlyTests} from './finite-historical-acceptance.mjs';
const root=path.resolve(import.meta.dirname,'..'),arg=n=>process.argv.find(a=>a.startsWith('--'+n+'='))?.slice(n.length+3);
const check=(v,m)=>{if(!v)throw Error(m)},equal=(a,b)=>JSON.stringify(a)===JSON.stringify(b);
check(arg('run-dir')&&arg('output'),'Explicit run-dir and output required');
const directory=path.resolve(root,arg('run-dir')),relative=path.relative(root,directory),destination=path.resolve(root,arg('output'));
check(relative.startsWith('parity/runs/finite-historical-m43/')&&destination.startsWith(root+'/parity/runs/'),'Scoped M43 run/output required');
const read=f=>JSON.parse(fs.readFileSync(path.resolve(root,f))),hash=f=>({file:path.relative(root,path.resolve(root,f)),sha256:crypto.createHash('sha256').update(fs.readFileSync(path.resolve(root,f))).digest('hex')});
const valid=r=>r&&fs.existsSync(path.resolve(root,r.file))&&hash(r.file).sha256===r.sha256;
const full=read(directory+'/result.json'),fingerprint=sourceFingerprint(root),prepared=read(full.prepared_native_inputs.file);
check(full.complete&&full.success&&full.source_manifest_verified_after&&full.environment_adapted&&full.pristine_file===false,'Complete adapted aggregate required');
check(equal(full.native_inputs,fingerprint)&&equal(full.finished_native_inputs,fingerprint)&&equal(prepared.fingerprint,fingerprint)&&prepared.files.every(valid),'Current prepared/native inputs differ');
check(valid(full.prepared_native_inputs)&&valid(full.context_archive)&&full.harness_inputs.every(valid)&&finiteHistoricalHarness.every(f=>full.harness_inputs.some(r=>r.file===f)),'Current full harness inventory required');
check(full.commands.length===73&&finiteHistoricalStages.every(n=>full.commands.filter(c=>c.name===n&&c.exit_code===0).length===1),'Missing/duplicate M43 aggregate stage');
check(full.commands.find(c=>c.name==='finite-historical-evidence-controls').finite_source_root===directory,'Actual current source negative controls required');
const previous='parity/runs/multichain-atomic-verification.json',prior=read(previous);
check(prior.complete&&prior.success&&prior.run_dir===relative&&valid(prior.full_verification)&&prior.full_verification.sha256===hash(directory+'/result.json').sha256&&prior.inputs.every(valid),'Complete current M42 regression proof required');
function archive(ref,expected){check(valid(ref)&&path.resolve(root,ref.file)===expected+'/context-archive.json','Exact context reference required');const a=read(ref.file);check(a.project_root===root&&a.evidence_root===expected&&a.verification_phase==='before-dispose'&&Object.values(a.entries).every(e=>validateContextEntry({file:e.executed_file,sha256:e.sha256},a)),'Context archive differs')}
archive(full.context_archive,directory);
const fixture=full.postgres_fixture;
check(fixture?.data_directory_verified===true&&fixture.receipt===relative+'-fixture'&&valid(fixture.preflight)&&fixture.preflight.file===relative+'/fixture-preflight.json'&&JSON.stringify(fixture.inputs?.map(r=>r.file))===JSON.stringify(['owned-directory.txt','show-data-directory.txt','initial-inventory.json','pinned-test-lifecycle.sha256','scope.txt','legacy-fixture-scope.txt'].map(f=>relative+'-fixture/'+f))&&fixture.inputs.every(valid),'Exact M43 PostgreSQL fixture provenance required');
const receipt=path.resolve(root,fixture.receipt),owned=fs.readFileSync(receipt+'/owned-directory.txt','utf8').trim(),initial=read(receipt+'/initial-inventory.json'),preflight=read(fixture.preflight.file);
check(fixture.directory===owned+'/data'&&fs.readFileSync(receipt+'/show-data-directory.txt','utf8').trim()===fixture.directory&&preflight.exit_code===0&&preflight.stdout.trim()===fixture.directory,'Independent SHOW data_directory checks disagree');
check(JSON.stringify([...initial.databases].sort())===JSON.stringify(['ponder_test','postgres','template0','template1'])&&initial.roles.every(r=>r==='agent'||r.startsWith('pg_')),'Fresh fixture inventory differs');
check(Date.parse(full.started_utc)<=Date.parse(preflight.checked_utc)&&Date.parse(preflight.checked_utc)<=Date.parse(full.commands[0].started_utc),'Fixture ownership was not verified before legacy source commands');

const controlDir=directory+'/finite-shortcut-controls',control=read(controlDir+'/result.json');
check(control.productionUnchanged&&control.negativeControlsFailedInDeclaredScope&&control.restoredGreen&&control.originalCredit===0&&equal(control.nativeInputs,fingerprint)&&equal(control.finishedNativeInputs,fingerprint)&&control.scriptHash===hash('tools/finite-historical-negative-controls.sh').sha256,'Current immutable runner shortcut controls required');
check(fs.readFileSync(controlDir+'/fixture-receipt.txt','utf8').trim()===receipt&&fs.readFileSync(controlDir+'/show-data-directory.txt','utf8').trim()===fixture.directory,'Runner controls must use aggregate-owned fixture');
const controlCommand=full.commands.find(c=>c.name==='finite-historical-negative-controls'),controlStart=Date.parse(control.started_at);
check(Number.isFinite(controlStart)&&Date.parse(prepared.recorded_utc)-1000<=controlStart&&Date.parse(controlCommand.started_utc)-1000<=controlStart&&controlStart<=Date.parse(controlCommand.finished_utc),'Runner controls must execute within current prepared aggregate stage');
const controlInputs=Object.entries(control.hashes).map(([file,sha256])=>({file:path.relative(root,path.join(controlDir,file)),sha256}));
check(controlInputs.length>0&&controlInputs.every(valid),'Runner shortcut artifacts changed');
check(equal(control.controls,[{name:'discard-refetch',test:'TestFiniteHistoricalRunnerRetry',backends:['postgres'],suffix:'/sticky_false',reason:'missing-field attempts: InvalidEventAccessError key=block.gasUsed retries=9'},{name:'repeat-setup',test:'TestFiniteHistoricalRunner',backends:['postgres','pglite'],suffix:'',reason:'multichain runtime already initialized or has pending state'},{name:'retain-resume-complete',test:'TestFiniteHistoricalRunnerResumeCompleteCache',backends:['postgres','pglite'],suffix:'',reason:'resume complete-cache conflict: sqlstate=23505 constraint=account_pkey prefix_unchanged=true resumed=true committed_groups=0 setup_unchanged=true'}]),'Exact PG-only refetch and dual-backend setup/resume-cache controls required');
for(const c of control.controls){
 check(typeof c.name==='string'&&typeof c.test==='string'&&typeof c.reason==='string'&&c.reason.length>0,'Explicit runtime failure contract required');
 const log=fs.readFileSync(controlDir+'/'+c.name+'.red.log','utf8');
 check(fs.readFileSync(controlDir+'/'+c.name+'.exit-code.txt','utf8').trim()==='1'&&log.includes(c.reason)&&!log.includes('[build failed]'),'Runner shortcut must fail actual runtime assertions');
 if(c.name==='discard-refetch')check(log.includes(c.reason+' missing='),'Discarded refetch must fail with actual missing-field class/key and exact nine retries');
 if(c.name==='retain-resume-complete')for(const backend of c.backends){const header='=== RUN   '+c.test+'/'+backend+c.suffix+'\n',start=log.indexOf(header),end=log.indexOf('\n=== RUN ',start+header.length),segment=log.slice(start+header.length,end<0?log.length:end);check(start>=0&&log.indexOf(header,start+1)<0&&segment.includes(c.reason+' backend='+backend+' observation='+({postgres:'pgconn_structured',pglite:'pglite_cause_text'}[backend])+' waits=[] '),'Each resume-cache backend must fail its real SQL conflict with durable prefix unchanged');}
 for(const backend of c.backends)check(log.includes('--- FAIL: '+c.test+'/'+backend+c.suffix+' '),'Declared runner shortcut leaf must fail');
}
const green=fs.readFileSync(controlDir+'/restored-green.log','utf8');
check(!green.includes('--- FAIL:')&&!green.includes('--- SKIP:')&&green.includes('PASS\n'),'Restored runner controls must pass without skips');
for(const c of control.controls)for(const backend of c.backends)check(green.includes('--- PASS: '+c.test+'/'+backend+c.suffix+' '),'Restored declared runner test missing');
const nativeControls={result:hash(controlDir+'/result.json'),inputs:controlInputs,source_credit:0};

const source=[],source_scopes=[];
for(const backend of ['postgres','pglite']){
 const dir=directory+'/finite-source-'+backend,r=read(dir+'/result.json'),original=read(dir+'/original.json'),capture=read(dir+'/calls.json');
 check(r.complete&&r.success&&r.exit_code===0&&r.backend===backend&&r.source_credit===0&&r.environment_adapted&&r.pristine_file===false&&r.adapter_verified_after&&r.counts.passed===finiteHistoricalProbeNames.length&&!r.counts.failed&&!r.counts.pending&&r.inputs.every(valid)&&equal(r.started_inputs,r.inputs)&&r.inputs.some(f=>f.file==='tools/source-finite-field-order.ts')&&r.inputs.some(f=>f.file==='tools/source-finite-capture.ts'),'Complete actual source multichain probe required');
 check(original.success&&original.numTotalTests===r.counts.passed&&original.numPassedTests===r.counts.passed&&original.numFailedTests===0&&original.numPendingTests===0,'Actual source assertions did not pass');
 if(backend==='postgres')check(r.postgres_fixture?.directory===fixture.directory&&r.postgres_fixture?.receipt===fixture.receipt&&r.postgres_fixture?.data_directory_verified===true,'Source PostgreSQL did not use exact aggregate-owned cluster');
 const files=original.testResults,assertions=files.flatMap(f=>f.assertionResults);
 check(files.length===1&&path.resolve(files[0].name)===root+'/tools/source-finite-historical.test.ts'&&assertions.length===r.counts.passed&&assertions.every(a=>a.status==='passed'),'Exact source probe file/status required');
 check(equal(assertions.map(a=>a.title).sort(),finiteHistoricalProbeNames.map(n=>'actual runMultichain finite '+n).sort())&&assertions.every(a=>a.fullName===a.title),'Exact finite-runner source probe titles required');
 archive(r.context_archive,dir);check(Date.parse(full.started_utc)<=Date.parse(r.started_utc)&&Date.parse(r.started_utc)<=Date.parse(r.finished_utc)&&Date.parse(r.finished_utc)<=Date.parse(prepared.recorded_utc),'Source outside preparation window');
 const validation=assertFiniteHistoricalCapture(capture,backend);check(equal(r.capture_validation,validation),'Current source outcome scope differs from actual capture');source_scopes.push(validation.source_scope);
 source.push({backend,result:hash(dir+'/result.json'),original:hash(dir+'/original.json'),capture:hash(dir+'/calls.json'),context_archive:r.context_archive});
}
const nativeFiles=finiteHistoricalNativeFiles;
const expected=nativeFiles.flatMap(file=>[...fs.readFileSync(root+'/'+file,'utf8').matchAll(/^func (Test\w+)\(/gm)].map(m=>({package:'ponder.local/ponder-go/'+file.split('/')[0],name:m[1]})));
check(expected.length>0,'M43 integration test inventory required');
const executions=[];
for(const mode of ['normal','race']){
 const base=mode==='normal'?'go-test':'go-race',log=directory+'/native/'+base+'.jsonl',manifest=read(directory+'/native/'+base+'-execution.json');
 check(manifest.complete&&manifest.success&&manifest.scope==='all_module_packages'&&manifest.race===(mode==='race')&&manifest.driver_sha256===hash('tools/go-tests.mjs').sha256&&equal(manifest.source_fingerprint,fingerprint)&&equal(manifest.finished_source_fingerprint,fingerprint),'Complete current native mode required');
 const start=Date.parse(manifest.started_utc),end=Date.parse(manifest.finished_utc);check(Date.parse(prepared.recorded_utc)<=start&&start<=end&&end<=Date.parse(full.finished_utc),'Native outside prepared aggregate');
 const events=fs.readFileSync(log,'utf8').trim().split('\n').map(JSON.parse),passed=(name,pkg)=>events.filter(e=>e.Package===pkg&&e.Test===name&&e.Action==='pass').length===1;
 check(events.every(e=>Number.isFinite(Date.parse(e.Time))&&Date.parse(e.Time)>=start-1000&&Date.parse(e.Time)<=end+1000)&&!events.some(e=>e.Test&&['skip','fail'].includes(e.Action)),'Native failure/skip or timestamp mismatch');
 for(const p of manifest.packages)for(const name of p.listed_functions)check(passed(name,p.package),'Listed native test missing/duplicate');
 for(const t of expected)check(passed(t.name,t.package),'Current M43 test missing: '+t.name);
 for(const backend of ['postgres','pglite'])for(const [name,suffixes] of finiteHistoricalDualBackendTests)for(const suffix of suffixes)check(passed(name+'/'+backend+suffix,'ponder.local/ponder-go/historical'),'Actual backend M43 integration missing: '+name+'/'+backend+suffix);
 for(const backend of ['postgres','pglite'])for(const name of finiteHistoricalOwnedBackendTests)check(passed(name+'/'+backend,'ponder.local/ponder-go/indexing'),'Actual backend M43 ownership/profile integration missing: '+name+'/'+backend);
 for(const name of finiteHistoricalPostgresOnlyTests)check(passed(name,'ponder.local/ponder-go/historical'),'Actual PostgreSQL-only fencing integration missing: '+name);
 executions.push({mode,source_credit:0,backend_scope:{dual_backend:finiteHistoricalDualBackendTests,owned_dual_backend:finiteHistoricalOwnedBackendTests,postgres_only:finiteHistoricalPostgresOnlyTests},log:hash(log),manifest:hash(directory+'/native/'+base+'-execution.json'),tests:expected});
}
const report={complete:true,success:true,scope:'finite_default_multichain_historical_runner',finite_finalized_ranges_only:true,readiness_cutover_completed:false,cross_domain_atomic_rewind:false,run_dir:relative,source_credit:0,environment_adapted:true,pristine_file:false,full_verification:hash(directory+'/result.json'),prepared_native_inputs:full.prepared_native_inputs,context_archive:full.context_archive,previous_atomic_proof:hash(previous),postgres_fixture:fixture,native_controls:nativeControls,source,source_scopes,executions,inputs:[...finiteHistoricalHarness,...prepared.files.filter(f=>/^(abi|config|events|filter|historical|indexing|rpc|runtime|store)\//.test(f.file)).map(f=>f.file)].map(hash),limitations:{runner:'Finite finalized-bound default-multichain historical runner only. No unbounded/catchup, readiness, views/triggers handoff, realtime or cutover.',pglite:'Native independent owned persistent directories and real SQL atomicity intentionally differ from source same-instance callback-only transactions. Under the unchanged 1-byte forced-eviction 240-event pressure input, source PGlite naturally exhausts 10 attempts / 9 waits and raises its original text-serializer Error; pressure did not complete. The separate 240-event actual-default-cache-budget case completes on both source backends. Native recovered text-key coercion is a stronger internal compatibility boundary, not public CRUD relaxation. Passing supplemental assertions do not mean PGlite pressure completed.',recovery:'No atomic cross-domain sync rewind. Business suffix rollback and safe-prefix constraints remain; owned reverts invalidate retained row caches.',coverage:'Supplemental source and native evidence adds zero original source cases, assertions or type credit. Source uses real Anvil chains 1/2 and native controlled source builds 1/10: shared production-boundary observations, not identical-input full-output differential parity.'}};
fs.writeFileSync(destination,JSON.stringify(report,null,2)+'\n');console.log({source_credit:0,source_probes:source.length,native_tests:expected.length,modes:executions.length});
