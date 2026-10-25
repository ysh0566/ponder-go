import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import {gunzipSync} from 'node:zlib';
import {assertIndexingProfileCapture,assertIndexingProfileProbeCapture} from './source-indexing-profile-evidence.mjs';
const declarations=JSON.parse(fs.readFileSync(new URL('../parity/test-ledger.json',import.meta.url))).tests.filter(t=>['packages/core/src/indexing/profile.test.ts','packages/core/src/indexing/client.test.ts'].includes(t.source_file));
const fixture=JSON.parse(gunzipSync(fs.readFileSync(new URL('../testdata/indexing/source-indexing-profile.json.gz',import.meta.url))));
for(const backend of ['postgres','pglite']){
 const valid={source_credit:0,format:'indexing-profile-source-v1',cases:fixture.backend_cases[backend]};
 test(backend+' original24 positive',()=>assert.equal(assertIndexingProfileCapture(valid,declarations).targeted,12));
 for(const [name,mutate] of [
  ['missing original',c=>c.cases.pop()],['source body',c=>c.cases[0].test_source+=' '],['source state',c=>c.cases[0].source_state='skip'],['assertion',c=>c.cases[0].assertions.pop()],['hint identity',c=>c.cases.find(c=>c.id==='d8952ac4024fd02802a7').profile_ops[1].result_hint_index=-1],['RPC admission',c=>c.cases.find(c=>c.id==='4ace7b36bca44245e4b7').providers[1].operation_index=4],['disabled DB',c=>c.cases.find(c=>c.id==='4ace7b36bca44245e4b7').store.push({operation:'getRpcRequestResults'})],['false credit',c=>c.source_credit=12],
 ])test(backend+' rejects '+name,()=>{const c=structuredClone(valid);mutate(c);assert.throws(()=>assertIndexingProfileCapture(c,declarations));});
}
const probes=JSON.parse(gunzipSync(fs.readFileSync(new URL('../testdata/indexing/source-indexing-profile-probes.json.gz',import.meta.url))));
for(const backend of ['postgres','pglite']){
 const valid={source_credit:0,real_rpc:false,real_database:false,cases:probes.backend_cases[backend]};
 test(backend+' probes positive',()=>assert.equal(assertIndexingProfileProbeCapture(valid).client_scenarios,21));
 for(const [name,mutate] of [
  ['blocked later chunk admission',c=>c.cases[1].scenarios.find(s=>s.name==='pending-chunk-clear').operations.find(o=>o.operation==='waitRPC').snapshot.rpc=2],
  ['missing actual later-chunk write admission',c=>c.cases[1].scenarios.find(s=>s.name==='pending-chunk-clear').operations.find(o=>o.operation==='waitWrites').snapshot.writes=1],
  ['clear lost pending result',c=>c.cases[1].scenarios.find(s=>s.name==='pending-chunk-clear').operations.at(-1).result.pop()],
  ['early aggregate prefetched insertion',c=>c.cases[1].scenarios.find(s=>s.name==='aggregate-rejection-no-write').operations.at(-1).snapshot.writes=2],
  ['crosschain blocked second lookup',c=>c.cases[1].scenarios.find(s=>s.name==='crosschain-held-db').operations.find(o=>o.operation==='waitRPC').snapshot.pending_db=0],
  ['crosschain wrong admission order',c=>c.cases[1].scenarios.find(s=>s.name==='crosschain-held-db').rpc[1].chainId=1],
  ['failfast awaited held sibling',c=>c.cases[1].scenarios.find(s=>s.name==='crosschain-failfast-sibling').operations.find(o=>o.operation==='awaitAction').snapshot.pending_db=0],
  ['failfast cancelled sibling',c=>c.cases[1].scenarios.find(s=>s.name==='crosschain-failfast-sibling').rpc.pop()],
  ['fake realDB',c=>c.real_database=true],['missing scenario',c=>c.cases[1].scenarios.pop()],['trace args collapse',c=>c.cases[0].vectors.find(v=>v.name==='trace-both-args-result').result.pattern.args.pop()],['noargs cache hint',c=>c.cases[0].vectors.find(v=>v.name==='hint-noargs-cache-mismatch').result_hint_index=-1],['threshold boundary',c=>c.cases[1].scenarios.find(s=>s.name==='threshold-rpc-exact').operations.at(-1).rpc.push({})],['crosschain dedupe',c=>c.cases[1].scenarios.find(s=>s.name==='dedupe-before-chain').operations.at(-1).rpc.push({chainId:2})],['early future completion',c=>c.cases[1].scenarios.find(s=>s.name==='causal-clear-disabled-true').operations.find(o=>o.operation==='prefetch').pending_rpc=0],
 ])test(backend+' probes rejects '+name,()=>{const c=structuredClone(valid);mutate(c);assert.throws(()=>assertIndexingProfileProbeCapture(c));});
}
