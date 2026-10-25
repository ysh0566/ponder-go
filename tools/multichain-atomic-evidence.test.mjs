import test from 'node:test';import assert from 'node:assert/strict';
import {atomicMultichainProofReferencesValid} from './multichain-atomic-evidence.mjs';
const ref={file:'evidence.json',sha256:'a'.repeat(64)},valid=r=>r.file===ref.file&&r.sha256===ref.sha256;
function proof(){return {scope:'atomic_multichain_setup_and_historical_group_primitives',source_credit:0,native_controls:{result:ref,inputs:[ref],source_credit:0},full_verification:ref,prepared_native_inputs:ref,context_archive:ref,previous_independent_proof:ref,inputs:[ref],postgres_fixture:{data_directory_verified:true,directory:'/new/data',receipt:'new-fixture',preflight:ref,inputs:Array(6).fill(ref)},source:['postgres','pglite'].map(backend=>({backend,result:ref,original:ref,capture:ref,context_archive:ref})),executions:['normal','race'].map(mode=>({mode,source_credit:0,log:ref,manifest:ref,tests:[{name:'TestReal'}]}))}}
test('M42 strict nested evidence requirements',()=>{assert.equal(atomicMultichainProofReferencesValid(proof(),valid),true);for(const mutate of [p=>p.source_credit=1,p=>p.scope='runner_complete',p=>delete p.previous_independent_proof,p=>delete p.native_controls,p=>p.source.pop(),p=>p.executions.pop(),p=>p.postgres_fixture.data_directory_verified=false,p=>p.source[1].capture={...ref,sha256:'b'.repeat(64)},p=>p.extra={nested:{...ref,sha256:'b'.repeat(64)}}]){const p=proof();mutate(p);assert.equal(atomicMultichainProofReferencesValid(p,valid),false)}});

// At full acceptance, mutate the actual current source captures. These are
// validator negative controls only; altered copies are never parity evidence.
import fs from 'node:fs';import path from 'node:path';
import {assertMultichainAtomicCapture} from './source-multichain-atomic-evidence.mjs';
if(process.env.PONDER_MULTICHAIN_ATOMIC_SOURCE_ROOT){
 const root=path.resolve(import.meta.dirname,'..'),run=path.resolve(process.env.PONDER_MULTICHAIN_ATOMIC_SOURCE_ROOT);
 assert(run.startsWith(root+'/parity/runs/multichain-atomic-m42/'));
 for(const backend of ['postgres','pglite']){
  const observed=JSON.parse(fs.readFileSync(run+'/atomic-source-'+backend+'/calls.json','utf8'));
  test(backend+' actual bounded source capture accepted',()=>assert.doesNotThrow(()=>assertMultichainAtomicCapture(observed,backend)));
  for(const [name,change] of Object.entries({credit:c=>c.source_credit=1,missing_case:c=>c.cases.pop(),wrong_backend:c=>c.cases[0].backend='other',inflight:c=>c.cases[0].drained.rpcInflight=1,identity:c=>c.cases[0].identities[0].context=false,inside_transaction_stop:c=>c.cases[0].harness_stop.outside_transaction=false,reordered_callback:c=>c.cases[0].timeline.find(x=>x.kind==='setup-handler').chainId=2,wrong_initial:c=>c.cases[0].initialCheckpoints[0].initial='0'.repeat(75),wrong_completed:c=>c.cases.find(x=>x.name==='historical-second-event-failure').completedMetric.values.find(x=>x.labels.event==='Blocks:block').value=1,wrong_checkpoint:c=>{const x=c.cases.find(x=>x.name==='historical-success');x.after.checkpoints.find(p=>p.chainId===x.groups[0].chainId).latestCheckpoint='0'.repeat(75)},missing_difference:c=>delete c.cases[0].native_difference}))test(backend+' rejects '+name,()=>{const c=structuredClone(observed);change(c);assert.throws(()=>assertMultichainAtomicCapture(c,backend))});
 }
}
