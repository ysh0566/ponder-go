// Evaluate the actual scoped predicates; retain legacy acceptance obligations.
import test from 'node:test';import assert from 'node:assert/strict';import fs from 'node:fs';import path from 'node:path';import vm from 'node:vm';
import {indexingProfileStages} from './indexing-profile-acceptance.mjs';
import {indexingActionsStages} from './indexing-actions-acceptance.mjs';
import {independentStorageStages} from './independent-storage-acceptance.mjs';
const root=path.resolve(import.meta.dirname,'..');
const names=fs.readdirSync(root+'/tools').filter(f=>f.endsWith('.mjs')&&!f.endsWith('.test.mjs')&&fs.readFileSync(root+'/tools/'+f,'utf8').includes("'independent-storage-m41'"));
for(const name of names)test(name+' anchored M41 paths',()=>{
 const source=fs.readFileSync(root+'/tools/'+name,'utf8');let count=0;
 for(const m of source.matchAll(/\[(?:'[^']+',)*'[^']+'\]\.some\(stage=>/g)){
  if(!m[0].includes("'independent-storage-m41'"))continue;
  let depth=1,end=m.index+m[0].length;for(;end<source.length&&depth;end++){if(source[end]==='(')depth++;if(source[end]===')')depth--}
  const expression=source.slice(m.index,end);if(!expression.includes('startsWith('))continue;count++;
  const run=value=>{const resolved=path.resolve(root,value);return vm.runInNewContext(expression,{root,path,relative:path.relative(root,resolved),output:resolved,directory:resolved,input:resolved})};
  assert.equal(run('parity/runs/independent-storage-m41/full/source'),true);
  for(const bad of ['parity/runs/independent-storage-m41','parity/runs/independent-storage-m410/full','parity/runs/independent-storage-m41-evil/full','parity/runs/independent-storage-m41/../other/full','../ponder-upstream/parity/runs/independent-storage-m41/full'])assert.equal(run(bad),false,bad);
 }
 assert(count>0,name);
});
const source=name=>fs.readFileSync(root+'/tools/'+name+'.mjs','utf8');
const stages=name=>[...source(name).matchAll(/run\('([^']+)'(\+backend)?/g)].flatMap(m=>m[2]?['postgres','pglite'].map(b=>m[1]+b):[m[1]]);
test('M41 retains exact62 previous stages and adds exactly3',()=>{const old=stages('indexing-profile-full-verify'),current=stages('independent-storage-full-verify');assert.equal(old.length,62);assert.equal(current.length,65);assert.equal(new Set(current).size,65);assert(old.every(n=>current.includes(n)));assert.deepEqual(current.filter(n=>!old.includes(n)).sort(),[...independentStorageStages].sort())});
for(const name of ['source-runtime-historical-results','source-runtime-multichain-results','source-indexing-service-results'])test(name+' exact old/new aggregate obligations',()=>{
 const s=source(name),start=s.indexOf('const requiredCommands='),end=s.indexOf('check(full.commands.length',start);assert(start>=0&&end>start);
 for(const [stage,count] of [['indexing-service-m38',46],['indexing-actions-m39',54],['indexing-profile-m40',62],['independent-storage-m41',65]]){
  const result=vm.runInNewContext(s.slice(start,end)+';requiredCommands',{relative:'parity/runs/'+stage+'/full',indexingActionsStages,indexingProfileStages,independentStorageStages});assert.equal(result.length,count);assert.equal(new Set(result).size,count);if(count===65)assert.deepEqual([...result].sort(),stages('independent-storage-full-verify').sort());
 }
});
test('M41 driver rejects old roots and output reuse',()=>{const line=source('independent-storage-full-verify').split('\n').find(l=>l.startsWith('if(')&&l.includes('New scoped'));assert(line);for(const [value,exists,allowed] of [['independent-storage-m41/full',false,true],['indexing-profile-m40/full',false,false],['independent-storage-m41-evil/full',false,false],['independent-storage-m41/full',true,false]]){const run=()=>vm.runInNewContext(line,{root,output:root+'/parity/runs/'+value,fs:{existsSync:()=>exists}});if(allowed)assert.doesNotThrow(run);else assert.throws(run)}});
test('M41 canonical helper retains11 existing collectors and adds supplemental proof with explicit outputs',()=>{const s=source('independent-storage-collect-all'),start=s.indexOf('const collectors='),end=s.indexOf('\nsave();',start);const list=vm.runInNewContext(s.slice(start,end)+';collectors');assert.equal(list.length,12);assert.equal(new Set(list.map(c=>c[2])).size,12);assert.equal(list[10][2],'source-indexing-profile-verification');assert.equal(list[11][2],'independent-storage-verification');assert(s.includes("'--output='+output"));assert(s.includes('if(fs.existsSync(destination))throw'))});
test('M41 driver refuses missing or mismatched live fixture before any legacy source command',()=>{
 const s=source('independent-storage-full-verify'),start=s.indexOf(' const fixtureReceipt='),end=s.indexOf(' copy=createAdaptedUpstream();',start);assert(start>0&&end>start);const code=s.slice(start,end);
 const output=root+'/parity/runs/independent-storage-m41/full',receipt=output+'-fixture',owned='/fixturetmp/ponder-m41-pg-abcdefgh',directory=owned+'/data';
 const run=(change={})=>{
  const env={TMPDIR:'/fixturetmp',PONDER_TEST_POSTGRES:'task-url',PONDER_M41_POSTGRES_DIRECTORY:directory,PONDER_M41_POSTGRES_RECEIPT:receipt,...change.env};
  const inventory=change.inventory??{databases:['ponder_test','postgres','template0','template1'],roles:['agent','pg_monitor']};let live=0;
  vm.runInNewContext(code,{root,output,path,crypto:{createHash:()=>({update:()=>({digest:()=> 'hash'})})},report:{},save(){},process:{env},spawnSync(){live++;return {status:change.status??0,stdout:change.liveDirectory??directory,stderr:''}},fs:{realpathSync:()=>change.realpath??directory,readFileSync(file){if(file.endsWith('/owned-directory.txt'))return change.owned??owned;if(file.endsWith('/initial-inventory.json'))return JSON.stringify(inventory);if(file.endsWith('/show-data-directory.txt'))return change.receiptDirectory??directory;return 'record'},writeFileSync(){}}});return live;
 };
 assert.equal(run(),1);
 for(const change of [{env:{PONDER_M41_POSTGRES_DIRECTORY:undefined}},{env:{PONDER_M41_POSTGRES_RECEIPT:undefined}},{env:{PONDER_M41_POSTGRES_RECEIPT:root+'/parity/runs/other'}},{owned:'/old/postgres'},{realpath:'/alias/old'},{liveDirectory:'/old/postgres/data'},{receiptDirectory:'/old/postgres/data'},{status:1},{inventory:{databases:['ponder_test','postgres','template0','template1','preexisting'],roles:['agent']}},{inventory:{databases:['ponder_test','postgres','template0','template1'],roles:['agent','business_role']}}])assert.throws(()=>run(change));
});
