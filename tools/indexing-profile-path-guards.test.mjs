// Evaluates the actual narrow path predicates, not a reimplementation of them.
import test from 'node:test';import assert from 'node:assert/strict';import fs from 'node:fs';import path from 'node:path';import vm from 'node:vm';
const root=path.resolve(import.meta.dirname,'..');
const files=['source-runtime-index-original','source-runtime-historical-original','source-runtime-historical-log-order','source-runtime-historical-fixture','source-runtime-multichain-original','source-runtime-multichain-probes','source-runtime-multichain-fixture','source-runtime-multichain-probe-fixture','source-indexing-service-original','source-indexing-service-proxy','source-indexing-service-cache','source-indexing-service-fixture','source-indexing-service-proxy-fixture','source-indexing-service-cache-fixture','source-indexing-service-collect','source-runtime-index-results','source-runtime-historical-results','source-runtime-multichain-results','source-indexing-service-results','source-indexing-actions-original','source-indexing-actions-probes','source-indexing-actions-fixture','source-indexing-actions-probe-fixture','source-indexing-actions-collect','source-indexing-actions-results','source-indexing-profile-fixture','source-indexing-profile-probe-fixture','source-indexing-profile-collect'];
for(const name of files)test(name+' has anchored M40 admission without sibling/traversal bypass',()=>{
 const source=fs.readFileSync(root+'/tools/'+name+'.mjs','utf8');
 const matches=[...source.matchAll(/\[(?:'[^']+',)*'[^']+'\]\.some\(stage=>/g)].filter(m=>m[0].includes("'indexing-profile-m40'"));assert(matches.length>0);
 let predicates=0;
 for(const m of matches){let depth=1,end=m.index+m[0].length;for(;end<source.length&&depth;end++){if(source[end]==='(')depth++;if(source[end]===')')depth--}const expression=source.slice(m.index,end);if(!expression.includes('startsWith('))continue;predicates++;
  const run=value=>{const resolved=path.resolve(root,value),relative=path.relative(root,resolved);return vm.runInNewContext(expression,{root,path,output:resolved,directory:resolved,relative,input:resolved})};
  for(const suffix of ['full-verify-01','nested/source-postgres','proof.json'])assert.equal(run('parity/runs/indexing-profile-m40/'+suffix),true,expression);
  for(const bad of ['parity/runs/indexing-profile-m40-evil/full','parity/runs/indexing-profile-m400/full','parity/runs/indexing-profile-m40','parity/runs/other/full','parity/runs/indexing-profile-m40/../other/full','../ponder-upstream/parity/runs/indexing-profile-m40/full'])assert.equal(run(bad),false,bad+' '+expression);
  const stages=[...expression.matchAll(/'((?:runtime|indexing)-[^']+-m\d+)'/g)].map(m=>m[1]);for(const stage of stages)assert.equal(run('parity/runs/'+stage+'/previous-allowed'),true);
 }assert(predicates>0);
});
for(const name of ['source-runtime-index-original','source-runtime-historical-original','source-runtime-historical-log-order','source-runtime-multichain-original','source-runtime-multichain-probes','source-indexing-service-original','source-indexing-service-proxy','source-indexing-service-cache','source-indexing-actions-original','source-indexing-actions-probes'])test(name+' keeps existing-result rejection',()=>{
 const source=fs.readFileSync(root+'/tools/'+name+'.mjs','utf8'),line=source.split('\n').find(l=>l.startsWith('if(')&&l.includes('New ')&&l.includes('fs.existsSync'));
 assert(line);const output=root+'/parity/runs/indexing-profile-m40/full/source';
 assert.doesNotThrow(()=>vm.runInNewContext(line,{root,output,fs:{existsSync:()=>false}}));
 assert.throws(()=>vm.runInNewContext(line,{root,output,fs:{existsSync:()=>true}}));
});
test('M40 full driver refuses old roots and existing output directories',()=>{
 const source=fs.readFileSync(root+'/tools/indexing-profile-full-verify.mjs','utf8'),line=source.split('\n').find(l=>l.startsWith('if(')&&l.includes('New scoped'));
 assert(line);for(const [output,exists,allowed] of [[root+'/parity/runs/indexing-profile-m40/full',false,true],[root+'/parity/runs/indexing-service-m38/full',false,false],[root+'/parity/runs/indexing-profile-m40-evil/full',false,false],[root+'/parity/runs/indexing-profile-m40/full',true,false]]){
  const run=()=>vm.runInNewContext(line,{root,output,fs:{existsSync:()=>exists}});if(allowed)assert.doesNotThrow(run);else assert.throws(run);
 }
});

import {indexingProfileStages} from './indexing-profile-acceptance.mjs';
import {indexingActionsStages} from './indexing-actions-acceptance.mjs';
const sourceFor=name=>fs.readFileSync(root+'/tools/'+name+'.mjs','utf8');
const driverStages=name=>[...sourceFor(name).matchAll(/run\('([^']+)'(\+backend)?/g)].flatMap(m=>m[2]?['postgres','pglite'].map(b=>m[1]+b):[m[1]]);
test('M40 aggregate preserves all54 old stages and adds exactly eight',()=>{
 const old=driverStages('indexing-actions-full-verify'),current=driverStages('indexing-profile-full-verify');
 assert.equal(old.length,54);assert.equal(new Set(old).size,54);
 assert.equal(current.length,62);assert.equal(new Set(current).size,62);
 assert.deepEqual(current.filter(n=>!old.includes(n)).sort(),[...indexingProfileStages].sort());
 assert(old.every(n=>current.includes(n)));
});
for(const name of ['source-runtime-historical-results','source-runtime-multichain-results','source-indexing-service-results'])test(name+' requires exact62 current M40 stages without weakening old totals',()=>{
 const source=sourceFor(name),start=source.indexOf('const requiredCommands='),end=source.indexOf("check(full.commands.length",start);
 assert(start>=0&&end>start);const code=source.slice(start,end)+';requiredCommands';
 for(const [stage,count] of [['indexing-service-m38',46],['indexing-actions-m39',54],['indexing-profile-m40',62]]){
  const required=vm.runInNewContext(code,{relative:'parity/runs/'+stage+'/full',indexingActionsStages,indexingProfileStages});
  assert.equal(required.length,count);assert.equal(new Set(required).size,count);
  if(stage==='indexing-profile-m40')assert.deepEqual([...required].sort(),driverStages('indexing-profile-full-verify').sort());
 }
});
test('M40 canonical helper always passes explicit current output and all11 dependencies',()=>{
 const source=sourceFor('indexing-profile-collect-all'),start=source.indexOf('const collectors='),end=source.indexOf('\nsave();',start);
 const collectors=vm.runInNewContext(source.slice(start,end)+';collectors');
 assert.equal(collectors.length,11);assert.equal(new Set(collectors.map(c=>c[2])).size,11);
 assert.equal(collectors.at(-1)[2],'source-indexing-profile-verification');
 assert(source.includes("'--output='+output"));assert(source.includes("'--run-dir='+relative"));
 assert(source.includes("if(fs.existsSync(destination))throw"));
});
for(const name of ['source-indexing-profile-original','source-indexing-profile-probes'])test(name+' only admits fresh M40 source outputs',()=>{
 const line=sourceFor(name).split('\n').find(l=>l.startsWith('if(')&&l.includes('New ')&&l.includes('fs.existsSync'));
 assert(line);
 for(const [value,exists,allowed] of [['parity/runs/indexing-profile-m40/source',false,true],['parity/runs/indexing-profile-m40/source',true,false],['parity/runs/indexing-actions-m39/source',false,false],['parity/runs/indexing-profile-m40-evil/source',false,false],['parity/runs/indexing-profile-m40/../other/source',false,false],['../ponder-upstream/parity/runs/indexing-profile-m40/source',false,false]]){
  const run=()=>vm.runInNewContext(line,{root,output:path.resolve(root,value),fs:{existsSync:()=>exists}});if(allowed)assert.doesNotThrow(run);else assert.throws(run);
 }
});
