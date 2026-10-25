import fs from 'node:fs';
import path from 'node:path';
import assert from 'node:assert/strict';
import test from 'node:test';
import {assertOriginalDeclaration,sameOriginalDeclaration,sourceDeclarationFields} from './source-runtime-historical-evidence.mjs';
const root=path.resolve(import.meta.dirname,'..');
const original=JSON.parse(fs.readFileSync(root+'/parity/test-ledger.json','utf8')).tests.find(t=>t.id==='1337ad403051f00c5d99');
test('exact source declaration accepted; every immutable field is bound',()=>{
 assertOriginalDeclaration(structuredClone(original),original);
 for(const key of sourceDeclarationFields){const changed=structuredClone(original);changed[key]={tampered:true};assert.throws(()=>assertOriginalDeclaration(changed,original),/Immutable original/)}
});
test('later mapping/execution bookkeeping does not invalidate unchanged source',()=>{
 const later=structuredClone(original);for(const key of ['discovered','port_status','mapping','execution_status','pass_status','blockers','oracle_current_status'])later[key]={later:true};
 assertOriginalDeclaration(original,later);assertOriginalDeclaration(later,original);
});
test('removed original inputs/body/assertions/type directives fail closed',()=>{
 for(const key of ['inputs','test_source','assertions','type_error_directives']){const changed=structuredClone(original);delete changed[key];assert.equal(sameOriginalDeclaration(changed,original),false)}
 assert.equal(sameOriginalDeclaration(null,original),false);
});
