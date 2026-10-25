import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import {gunzipSync} from 'node:zlib';
import {assertIndexingActionsCapture} from './source-indexing-actions-evidence.mjs';
const fixture=JSON.parse(gunzipSync(fs.readFileSync(new URL('../testdata/indexing/source-indexing-actions-live.json.gz',import.meta.url))));
const declarations=JSON.parse(fs.readFileSync(new URL('../parity/test-ledger.json',import.meta.url))).tests.filter(t=>t.source_file==='packages/core/src/indexing/index.test.ts');
const capture=()=>({source_credit:0,format:fixture.format,cases:structuredClone(fixture.backend_cases.postgres)});
test('genuine original8 action census and both backend tapes',()=>{for(const backend of ['postgres','pglite'])assert.deepEqual(assertIndexingActionsCapture({...capture(),cases:fixture.backend_cases[backend]},declarations),{targeted:8,assertions:9,type_directives:0,providers:9,rows:8})});
for(const [name,mutate] of [
 ['missing original case',c=>c.cases.pop()],
 ['changed declaration assertion',c=>c.cases[15].assertions=[]],
 ['invented TS gap',c=>c.cases[15].type_error_directives=[{}]],
 ['missing target',c=>c.cases[15].targeted=false],
 ['missing real RPC',c=>c.cases[15].actions[0].providers=[]],
 ['nonaggregate multicall',c=>c.cases[21].actions[0].providers[0].request.params[0].data='0x18160ddd'],
 ['missing original retry',c=>c.cases[20].actions[0].providers.shift()],
 ['fabricated successful first retry response',c=>c.cases[20].actions[0].providers[0].response='0x01'],
 ['missing persisted cache row',c=>c.cases[15].physical_after=[]],
 ['missing cache admission',c=>c.cases[15].store=c.cases[15].store.filter(s=>s.operation!=='insertRpcRequestResults')],
])test('reject '+name,()=>{const c=capture();mutate(c);assert.throws(()=>assertIndexingActionsCapture(c,declarations))});
const probeFixture=JSON.parse(gunzipSync(fs.readFileSync(new URL('../testdata/indexing/source-indexing-actions-probes.json.gz',import.meta.url))));
const {assertIndexingActionsProbeCapture}=await import('./source-indexing-actions-evidence.mjs');
const probe=()=>({source_credit:0,real_rpc:true,real_database:true,cases:structuredClone(probeFixture.backend_cases.postgres)});
test('genuine real partial cache probes both backends',()=>{for(const b of ['postgres','pglite'])assert.deepEqual(assertIndexingActionsProbeCapture({...probe(),cases:probeFixture.backend_cases[b]}),{cases:1,actions:11,providers:10,source_credit:0})});
for(const [name,mutate] of [
 ['missing second chunk',c=>c.cases[0].actions[9].providers.pop()],
 ['missing later chunk cache',c=>c.cases[0].actions[9].physical_after.pop()],
 ['encoder error wrongly classified revert',c=>c.cases[0].actions[10].result[0].error.$error.cause_name='ContractFunctionRevertedError'],
 ['revert wrongly classified zero data',c=>c.cases[0].actions[3].result[1].error.$error.cause_name='ContractFunctionZeroDataError'],
 ['missing probe action',c=>c.cases[0].actions.pop()],
 ['all-hit invented RPC',c=>c.cases[0].actions[2].providers=[{}]],
 ['bad duplicate ordering',c=>c.cases[0].actions[1].result.reverse().pop()],
 ['cached failed child',c=>c.cases[0].actions[3].physical_after.push({})],
 ['missing failure',c=>c.cases[0].actions[4].error=undefined],
 ['immutable wrong block',c=>c.cases[0].actions[6].providers[0].request.params[1]='0x0'],
 ['cached empty code',c=>c.cases[0].actions[8].providers=[]],
])test('reject probe '+name,()=>{const c=probe();mutate(c);assert.throws(()=>assertIndexingActionsProbeCapture(c))});
