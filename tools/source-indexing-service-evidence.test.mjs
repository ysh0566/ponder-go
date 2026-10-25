import fs from 'node:fs';
import assert from 'node:assert/strict';
import {gunzipSync} from 'node:zlib';
import {test} from 'node:test';
import {assertIndexingServiceCapture} from './source-indexing-service-evidence.mjs';
const arg=process.argv.find(x=>x.startsWith('--fixture='));
if(!arg)throw Error('Explicit --fixture required');
const fixture=JSON.parse(gunzipSync(fs.readFileSync(arg.slice(10))));
const declarations=JSON.parse(fs.readFileSync(new URL('../parity/test-ledger.json',import.meta.url))).tests.filter(t=>t.source_file==='packages/core/src/indexing/index.test.ts');
for(const backend of ['postgres','pglite']){
 const capture={cases:fixture.backend_cases[backend]};
 test(backend+' complete capture',()=>assert.equal(assertIndexingServiceCapture(capture,declarations).callbacks,1014));
 const broken=(name,mutate)=>test(backend+' rejects '+name,()=>{const copy=structuredClone(capture);mutate(copy);assert.throws(()=>assertIndexingServiceCapture(copy,declarations))});
 broken('changed assertion',c=>c.cases[0].assertions=[]);
 broken('missing case',c=>c.cases.pop());
 broken('wrong targeted subset',c=>c.cases[15].targeted=true);
 broken('missing original callback',c=>c.cases[14].calls[0].methods[0].callbacks.pop());
 broken('reordered actual field reads',c=>c.cases[14].calls[0].methods[0].callbacks[0].operations.reverse());
 broken('fabricated invalid-access type',c=>c.cases[14].calls[0].methods[1].callbacks[0].error.$error.name='Error');
 broken('missing provider',c=>c.cases[5].calls[0].methods[0].providers=[]);
 broken('missing physical table',c=>delete c.cases[1].calls[0].methods[0].physical_after.blocks);
 broken('missing argument identity',c=>delete c.cases[2].calls[0].methods[0].callbacks[0].argument_identity);
}
