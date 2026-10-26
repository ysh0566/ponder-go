import test from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import fs from 'node:fs';
import {scopedPath,validateManagedEvents} from './managed-finite-evidence.mjs';
import {managedFiniteDerivedOutputs,managedFiniteCredit} from './managed-finite-acceptance.mjs';
const root=path.resolve(import.meta.dirname,'..');
test('managed path scope rejects predecessors, traversal and sibling prefix',()=>{
 for(const value of ['parity/runs/finite-historical-m43/full','parity/runs/managed-finite-m44evil/full','parity/runs/managed-finite-m44/../old/full','/tmp/out','parity\\runs\\managed-finite-m44\\full']) assert.throws(()=>scopedPath(root,value,'parity/runs/managed-finite-m44'));
 assert.equal(scopedPath(root,'parity/runs/managed-finite-m44/new-run','parity/runs/managed-finite-m44'),root+'/parity/runs/managed-finite-m44/new-run');
});
const pkg='ponder.local/ponder-go/store',name='TestManagedFixture',spec=[{package:pkg,name,scope:'postgres_and_persistent_pglite'}];
const events=[name,name+'/postgres',name+'/pglite'].flatMap(Test=>[{Package:pkg,Test,Action:'run'},{Package:pkg,Test,Action:'pass'}]);
test('managed evidence requires both real declared backend leaves',()=>{
 assert.equal(validateManagedEvents(events,spec)[0].leaves.length,2);
 assert.throws(()=>validateManagedEvents(events.filter(e=>!e.Test.includes('pglite')),spec));
});
for(const action of ['fail','skip'])test('managed evidence rejects '+action,()=>assert.throws(()=>validateManagedEvents([...events,{Package:pkg,Test:name,Action:action}],spec)));
test('managed evidence rejects duplicate and absent passing leaves',()=>{
 assert.throws(()=>validateManagedEvents([...events,{Package:pkg,Test:name+'/postgres',Action:'pass'}],spec));
 assert.throws(()=>validateManagedEvents(events.filter(e=>!(e.Test===name+'/postgres'&&e.Action==='pass')),spec));
});
test('derived output allowance is fixed, exact, not a source exemption',()=>{
 assert.equal(managedFiniteDerivedOutputs.length,16);assert.equal(new Set(managedFiniteDerivedOutputs).size,16);
 assert.deepEqual(managedFiniteCredit,{source_credit:0,ported_original:543,original_total:905});
 assert(managedFiniteDerivedOutputs.includes('parity/test-ledger.json'));
 assert(managedFiniteDerivedOutputs.includes('parity/runs/summary.json'));
 assert(managedFiniteDerivedOutputs.every(p=>p==='parity/test-ledger.json'||/^parity\/runs\/[a-z-]+(?:-verification)?\.json$/.test(p)));
 assert(!managedFiniteDerivedOutputs.includes('parity/mappings.json'));
 const wrapper=fs.readFileSync(root+'/tools/managed-finite-full-verify.mjs','utf8');
 assert(wrapper.indexOf('report.previous_derived_outputs=[]')<wrapper.indexOf("run('unchanged-full-regression'"));
 assert(wrapper.includes("process.argv.includes('--collect-canonical')"));
});
import {validateManagedCloseout,validateManagedSnapshotList,validateManagedSummary} from './managed-finite-evidence.mjs';
const closed={complete:true,success:true,launch_pgid:1234,launch_exit_code:0,launch_signal:null,interrupted:null,joined_processes:[],current_processes:[],fixture_exit_code:0,postgres_status:{exit_code:3,error:null},postmaster_pid_present:false,shutdown_log:'server stopped',initial_inventory:{databases:['ponder_test','postgres','template0','template1'],roles:['agent','pg_monitor']},final_inventory:{databases:['ponder_test','postgres','template0','template1'],roles:['agent','pg_monitor']},observed_utc:'2026-10-26T10:00:00Z'};
test('closeout accepts actual closed-shape only',()=>assert.equal(validateManagedCloseout(closed),true));
for(const [name,mutate] of Object.entries({missing:c=>delete c.postgres_status,active:c=>c.postgres_status.exit_code=0,shutdown:c=>c.fixture_exit_code=1,drain:c=>c.current_processes.push({pid:1}),join:c=>c.joined_processes.push({pid:1}),signal:c=>c.launch_signal='SIGTERM',pidfile:c=>c.postmaster_pid_present=true,inventory:c=>c.final_inventory.databases.push('foreign')}))test('closeout rejects '+name,()=>{const c=structuredClone(closed);mutate(c);assert.throws(()=>validateManagedCloseout(c));});
test('snapshot list rejects duplicate, reordered and omitted allowed inputs',()=>{
 const entries=managedFiniteDerivedOutputs.map(file=>({file}));validateManagedSnapshotList(entries,managedFiniteDerivedOutputs);
 for(const bad of [entries.slice(1),[entries[0],...entries.slice(0,-1)],[...entries].reverse()])assert.throws(()=>validateManagedSnapshotList(bad,managedFiniteDerivedOutputs));
});
test('summary rejects stale linkage, changed counts and false passing status',()=>{
 const baseline='parity/runs/finite-historical-m43/m44-regression-test',ref={file:baseline+'/result.json',sha256:'f'.repeat(64)},counts={normal:34,race:34};
 const s={run_dir:baseline,full_verification:ref,environment_adapted:true,pristine_file:false,parity:{expanded_static_records:905,ported:543,passed:543,failed:0,blocked:0,unimplemented:362,current_original_oracle_passed:543,historical_only_original_oracles:0},native_go:{file:baseline+'/native/go-test.jsonl',leaf_cases:34,pass:34,fail:0,skip:0},race:{file:baseline+'/native/go-race.jsonl',leaf_cases:34,pass:34,fail:0,skip:0}};
 validateManagedSummary(s,baseline,ref,counts);
 for(const mutate of [v=>v.run_dir+='stale',v=>v.full_verification.sha256='0'.repeat(64),v=>v.parity.passed=542,v=>v.race.pass=33,v=>v.native_go.fail=1]){const v=structuredClone(s);mutate(v);assert.throws(()=>validateManagedSummary(v,baseline,ref,counts));}
});
import {validateManagedReferences} from './managed-finite-evidence.mjs';
test('collector inputs reject missing, reordered, stale and changed references',()=>{
 const files=['a','b'],refs=files.map(file=>({file,sha256:'f'.repeat(64)})),valid=r=>r?.sha256==='f'.repeat(64);
 validateManagedReferences(refs,files,valid);
 for(const bad of [[],[refs[0],refs[0]],[...refs].reverse(),[refs[0],{...refs[1],sha256:'0'.repeat(64)}]])assert.throws(()=>validateManagedReferences(bad,files,valid));
});
import {managedBaselinePath} from './managed-finite-evidence.mjs';
test('baseline scope cannot nest a new child inside accepted evidence',()=>{
 assert.equal(managedBaselinePath(root,'parity/runs/finite-historical-m43/m44-regression-new'),root+'/parity/runs/finite-historical-m43/m44-regression-new');
 for(const value of ['parity/runs/finite-historical-m43/accepted/m44-regression-new','parity/runs/finite-historical-m43/rebuild-full-05','parity/runs/finite-historical-m43/m44-regression-new/nested'])assert.throws(()=>managedBaselinePath(root,value));
});
import {validateManagedObserver} from './managed-finite-evidence.mjs';
const observed={format:'managed-finite-observed-descendants-v1',complete:true,success:true,exit_code:0,signal:null,interrupted:[],capacity:{floor_bytes:5*1024**3,budget_bytes:4*1024**3,initial_free_bytes:10*1024**3,final_free_bytes:7*1024**3},sampling_interval_ms:1000,root_identity:{pid:500,pgid:500,start_ticks:'100'},observed:[{pid:500,pgid:500,start_ticks:'100'},{pid:501,pgid:501,start_ticks:'110'}],final_remaining_identities:[],final_remaining_groups:[],started_utc:'2026-10-26T10:00:00Z',finished_utc:'2026-10-26T10:05:00Z'};
test('observer requires actual unique identities and drained recorded groups',()=>{
 validateManagedObserver(observed);
 for(const mutate of [o=>o.observed=[],o=>o.observed.push(o.observed[0]),o=>o.final_remaining_identities.push(o.root_identity),o=>o.final_remaining_groups.push({pgid:500,processes:[]}),o=>o.exit_code=1,o=>o.signal='SIGTERM',o=>o.sampling_interval_ms=0,o=>o.interrupted.push('capacity'),o=>o.capacity.final_free_bytes=4*1024**3,o=>o.capacity.budget_bytes=0,o=>o.observed[0].start_ticks='mismatch']){const o=structuredClone(observed);mutate(o);assert.throws(()=>validateManagedObserver(o));}
});
import vm from 'node:vm';
import {execFileSync} from 'node:child_process';
import {finiteHistoricalNativeFiles,finiteHistoricalStages,finiteHistoricalHarness} from './finite-historical-acceptance.mjs';
import {managedFiniteNativeFiles} from './managed-finite-acceptance.mjs';
const acceptedM43='42783182f418b5dd6abc795f6e3cc8acfec6f409';
const acceptedInventorySource=execFileSync('git',['show',acceptedM43+':tools/finite-historical-acceptance.mjs'],{cwd:root,encoding:'utf8'});
const acceptedInventory=vm.runInNewContext(acceptedInventorySource.replaceAll('export const ','const ')+';({finiteHistoricalNativeFiles,finiteHistoricalStages,finiteHistoricalHarness})');
test('M44 registers exactly six supplemental files while preserving accepted M43 inventory and stages',()=>{
 assert.equal(managedFiniteNativeFiles.length,6);
 assert.deepEqual(finiteHistoricalNativeFiles,[...acceptedInventory.finiteHistoricalNativeFiles,...managedFiniteNativeFiles]);
 assert.deepEqual(finiteHistoricalStages,[...acceptedInventory.finiteHistoricalStages]);
 assert.deepEqual(finiteHistoricalHarness,[...acceptedInventory.finiteHistoricalHarness]);
});
test('actual unchanged M43 new-test registration guard rejects unknown supplemental test file',()=>{
 const original=fs.readFileSync(root+'/tools/finite-historical-path-guards.test.mjs','utf8');
 const start=original.indexOf("test('M43 native inventory covers new production-package test files, not archived candidates'");
 assert(start>=0);const end=original.indexOf('\n});',start);assert(end>start);
 const body=original.slice(start,end),lines=body.split('\n').filter(l=>l.trim().startsWith('const changed=')||l.trim().startsWith('for(const file of new Set(changed))')||l.trim().startsWith('assert.equal(new Set(finiteHistoricalNativeFiles)'));
 assert.equal(lines.length,3,'Use the actual unchanged guard predicate, never a rewritten approximation');
 const run=files=>vm.runInNewContext(lines.join('\n'),{finiteHistoricalNativeFiles,assert,git:(command)=>command==='ls-files'?files:[]});
 assert.doesNotThrow(()=>run(managedFiniteNativeFiles));
 assert.throws(()=>run([...managedFiniteNativeFiles,'store/m44_unregistered_probe_test.go']),/New test file omitted from finite inventory: store\/m44_unregistered_probe_test.go/);
});
