// Evaluates the actual narrow path predicates, not a reimplementation of them.
import test from 'node:test';import assert from 'node:assert/strict';import fs from 'node:fs';import path from 'node:path';import vm from 'node:vm';
const root=path.resolve(import.meta.dirname,'..');
const files=['source-runtime-index-original','source-runtime-historical-original','source-runtime-historical-log-order','source-runtime-historical-fixture','source-runtime-multichain-original','source-runtime-multichain-probes','source-runtime-multichain-fixture','source-runtime-multichain-probe-fixture','source-indexing-service-original','source-indexing-service-proxy','source-indexing-service-cache','source-indexing-service-fixture','source-indexing-service-proxy-fixture','source-indexing-service-cache-fixture','source-indexing-service-collect','source-runtime-index-results','source-runtime-historical-results','source-runtime-multichain-results','source-indexing-service-results'];
for(const name of files)test(name+' has anchored M39 admission without sibling/traversal bypass',()=>{
 const source=fs.readFileSync(root+'/tools/'+name+'.mjs','utf8');
 const matches=[...source.matchAll(/\[(?:'[^']+',)*'[^']+'\]\.some\(stage=>/g)].filter(m=>m[0].includes("'indexing-actions-m39'"));assert(matches.length>0);
 let predicates=0;
 for(const m of matches){let depth=1,end=m.index+m[0].length;for(;end<source.length&&depth;end++){if(source[end]==='(')depth++;if(source[end]===')')depth--}const expression=source.slice(m.index,end);if(!expression.includes('startsWith('))continue;predicates++;
  const run=value=>{const resolved=path.resolve(root,value),relative=path.relative(root,resolved);return vm.runInNewContext(expression,{root,path,output:resolved,directory:resolved,relative,input:resolved})};
  for(const suffix of ['full-verify-01','nested/source-postgres','proof.json'])assert.equal(run('parity/runs/indexing-actions-m39/'+suffix),true,expression);
  for(const bad of ['parity/runs/indexing-actions-m39-evil/full','parity/runs/indexing-actions-m390/full','parity/runs/indexing-actions-m39','parity/runs/other/full','parity/runs/indexing-actions-m39/../other/full','../ponder-upstream/parity/runs/indexing-actions-m39/full'])assert.equal(run(bad),false,bad+' '+expression);
  const stages=[...expression.matchAll(/'((?:runtime|indexing)-[^']+-m\d+)'/g)].map(m=>m[1]);for(const stage of stages)assert.equal(run('parity/runs/'+stage+'/previous-allowed'),true);
 }assert(predicates>0);
});
for(const name of ['source-runtime-index-original','source-runtime-historical-original','source-runtime-historical-log-order','source-runtime-multichain-original','source-runtime-multichain-probes','source-indexing-service-original','source-indexing-service-proxy','source-indexing-service-cache'])test(name+' keeps existing-result rejection',()=>{
 const source=fs.readFileSync(root+'/tools/'+name+'.mjs','utf8'),line=source.split('\n').find(l=>l.startsWith('if(')&&l.includes('New ')&&l.includes('fs.existsSync'));
 assert(line);const output=root+'/parity/runs/indexing-actions-m39/full/source';
 assert.doesNotThrow(()=>vm.runInNewContext(line,{root,output,fs:{existsSync:()=>false}}));
 assert.throws(()=>vm.runInNewContext(line,{root,output,fs:{existsSync:()=>true}}));
});
test('M39 full driver refuses old roots and existing output directories',()=>{
 const source=fs.readFileSync(root+'/tools/indexing-actions-full-verify.mjs','utf8'),line=source.split('\n').find(l=>l.startsWith('if(')&&l.includes('New scoped'));
 assert(line);for(const [output,exists,allowed] of [[root+'/parity/runs/indexing-actions-m39/full',false,true],[root+'/parity/runs/indexing-service-m38/full',false,false],[root+'/parity/runs/indexing-actions-m39-evil/full',false,false],[root+'/parity/runs/indexing-actions-m39/full',true,false]]){
  const run=()=>vm.runInNewContext(line,{root,output,fs:{existsSync:()=>exists}});if(allowed)assert.doesNotThrow(run);else assert.throws(run);
 }
});
