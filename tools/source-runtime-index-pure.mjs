// Selected unchanged pure source test declarations, not a pristine-file oracle.
// No setup hook or network restriction is mocked or bypassed. Source credit is zero.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {createRequire} from 'node:module';
import {spawn} from 'node:child_process';
const root=path.resolve(import.meta.dirname,'..');
const upstream=path.resolve(root,'../ponder-upstream');
const require=createRequire(path.join(upstream,'packages/core/package.json'));
const ts=require('typescript');
const sourceFile='packages/core/src/runtime/index.test.ts';
const raw=fs.readFileSync(path.join(upstream,sourceFile),'utf8');
const sha=value=>crypto.createHash('sha256').update(value).digest('hex');
const ledger=JSON.parse(fs.readFileSync(path.join(root,'parity/test-ledger.json'),'utf8'));
const selected=ledger.tests.filter(t=>t.source_file===sourceFile&&/^(getCachedBlock|getRequiredIntervals|getRequiredIntervalsWithFilters)\(\)/.test(t.title));
if(selected.length!==10||selected.reduce((n,t)=>n+t.assertions.length,0)!==18)throw Error('Changed pure declaration/assertion census');
if(selected.some(t=>t.source_sha256!==sha(raw)))throw Error('Pinned original source hash mismatch');
const tree=ts.createSourceFile(sourceFile,raw,ts.ScriptTarget.Latest,true);
const declarations=[];
for(const node of tree.statements){
  if(!ts.isExpressionStatement(node)||!ts.isCallExpression(node.expression))continue;
  const call=node.expression;
  if(call.expression.getText(tree)!=='test'||!ts.isStringLiteral(call.arguments[0]))continue;
  const entry=selected.find(t=>t.title===call.arguments[0].text);
  if(!entry)continue;
  const declaration=call.getText(tree);
  if(declaration!==entry.test_source)throw Error('Declaration differs from exact ledger: '+entry.title);
  declarations.push({entry,declaration});
}
if(declarations.length!==10)throw Error('Missing selected AST declarations');
const metadata=declarations.map(({entry,declaration})=>({id:entry.id,title:entry.title,source_file:entry.source_file,source_sha256:entry.source_sha256,line:entry.line,source_state:entry.source_state,assertions:entry.assertions,declaration_sha256:sha(declaration)}));
const sourcePaths=[sourceFile,'packages/core/src/runtime/index.ts','packages/core/src/runtime/filter.ts','packages/core/src/runtime/fragments.ts','packages/core/src/utils/interval.ts','packages/core/src/utils/dedupe.ts','packages/core/src/_test/constants.ts','pnpm-lock.yaml'];
const sources=sourcePaths.map(file=>({file,sha256:sha(fs.readFileSync(path.join(upstream,file)))}));
const method='Ten unchanged pure test declarations and eighteen original assertions, importing actual pinned runtime/index.ts and fragments.ts. Original setupCommon/setupAnvil/setupIsolatedDatabase/setupCleanup hooks are NOT run. Supplemental isolated evidence only; zero original source-record credit, not pristine-file acceptance.';
const generated=`import fs from 'node:fs';
import {zeroAddress} from 'viem';
import {beforeEach,afterEach,afterAll,expect,test} from 'vitest';
import {ALICE,EMPTY_BLOCK_FILTER,EMPTY_LOG_FILTER} from '@/_test/constants.js';
import type {BlockFilter,Filter,Factory,Fragment,LogFilter} from '@/internal/types.js';
import type {Interval} from '@/utils/interval.js';
import type {CachedIntervals} from '@/runtime/index.js';
import * as actual from '@/runtime/index.js';
import {getFragments,getFactoryFragments} from '@/runtime/fragments.js';
const metadata=${JSON.stringify(metadata)};
const cases=[];let current;
// IDs persist across a call's before/result/after snapshots; each snapshot expands
// each object once, preserving Map identity, property order and explicit undefined.
function recorder(){const ids=new WeakMap();let serial=0;return function mark(value){const expanded=new Set();function walk(v){
 if(v===undefined)return {$undefined:true};
 if(typeof v==='bigint')return {$bigint:String(v)};
 if(typeof v==='number'&&!Number.isFinite(v))return {$number:String(v)};
 if(Array.isArray(v))return v.map(walk);
 if(v instanceof Map)return {$map:[...v].map(([k,value])=>[walk(k),walk(value)])};
 if(v&&typeof v==='object'){let id=ids.get(v);if(id===undefined){id=++serial;ids.set(v,id)}if(expanded.has(v))return {$ref:id};expanded.add(v);return {$id:id,$object:Object.entries(v).map(([k,value])=>[k,walk(value)])};}
 return v;
}return walk(value)}}
function capture(operation,fn){return (...args)=>{const mark=recorder(),entry={operation,args:mark(args)};current.calls.push(entry);try{const result=fn(...args);entry.result=mark(result);return result;}catch(error){entry.error={name:error.name,message:error.message};throw error;}finally{entry.after=mark(args)}}}
const getCachedBlock=capture('getCachedBlock',actual.getCachedBlock);
const getRequiredIntervals=capture('getRequiredIntervals',actual.getRequiredIntervals);
const getRequiredIntervalsWithFilters=capture('getRequiredIntervalsWithFilters',actual.getRequiredIntervalsWithFilters);
beforeEach(ctx=>{const meta=metadata.find(m=>m.title===ctx.task.name);if(!meta)throw Error('Unmapped original declaration');current={...meta,calls:[]};});
afterEach(ctx=>{current.observed_after_each_state=ctx.task.result?.state??'unknown';cases.push(current)});
afterAll(()=>fs.writeFileSync(process.env.PONDER_RUNTIME_PURE_FIXTURE!,JSON.stringify({baseline:${JSON.stringify(ledger.baseline)},method:${JSON.stringify(method)},source_credit:0,pristine_file:false,typescript:${JSON.stringify(ts.version)},sources:${JSON.stringify(sources)},cases},null,2)+'\\n'));
${declarations.map(d=>d.declaration+';').join('\n')}
`;
const folder=path.join(root,'testdata/oracle');
fs.mkdirSync(folder,{recursive:true});
const link=path.join(folder,'node_modules');
if(!fs.existsSync(link))fs.symlinkSync(path.join(upstream,'packages/core/node_modules'),link,'dir');
const testFile=path.join(folder,'source-runtime-index-pure.test.ts');
fs.writeFileSync(testFile,generated);
const provenance={method,source_credit:0,pristine_file:false,sources,generated_sha256:sha(generated),cases:metadata};
fs.writeFileSync(path.join(root,'parity/source-runtime-index-pure-declarations.json'),JSON.stringify(provenance,null,2)+'\n');
console.log(JSON.stringify({selected_tests:10,original_assertions:18,source_credit:0,generated_file:path.relative(root,testFile)}));
if(!process.argv.includes('--run'))process.exit(0);
fs.mkdirSync(path.join(root,'parity/runs'),{recursive:true});
const child=spawn(path.join(upstream,'packages/core/node_modules/.bin/vitest'),['run','--config','tools/source-runtime-index-pure.config.mjs','--configLoader','native',path.relative(root,testFile),'--reporter=json','--outputFile=parity/runs/source-runtime-index-pure-vitest.json'],{cwd:root,stdio:'inherit',env:{...process.env,CI:'1',PONDER_RUNTIME_PURE_FIXTURE:path.join(root,'testdata/runtime/source-runtime-index-pure.json')}});
const exit=await new Promise((resolve,reject)=>{child.on('error',reject);child.on('exit',resolve)});
process.exitCode=exit??1;
