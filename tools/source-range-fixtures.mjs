// Actual pinned helper body, isolated without modifying original imports/tests.
import fs from'node:fs';import path from'node:path';import crypto from'node:crypto';import{createRequire}from'node:module';
const root=path.resolve(import.meta.dirname,'..'),upstream=path.resolve(root,'../ponder-upstream'),file=upstream+'/packages/core/src/sync-store/index.ts',source=fs.readFileSync(file,'utf8'),require=createRequire(upstream+'/packages/core/package.json'),ts=require('typescript');
const tree=ts.createSourceFile('index.ts',source,ts.ScriptTarget.Latest,true);const node=tree.statements.find(s=>ts.isVariableStatement(s)&&s.declarationList.declarations.some(d=>d.name.getText(tree)==='getFilterBlockRange'));if(!node)throw Error('Missing actual export');
const emitted=ts.transpileModule(node.getText(tree),{compilerOptions:{module:ts.ModuleKind.ESNext,target:ts.ScriptTarget.ESNext}}).outputText;const{getFilterBlockRange}=await import('data:text/javascript;base64,'+Buffer.from(emitted).toString('base64'));
const inputs=[
 {filters:[{fromBlock:10,toBlock:80},{fromBlock:20,toBlock:90}],fromBlock:0,toBlock:100},
 {filters:[{fromBlock:10,toBlock:80},{fromBlock:undefined,toBlock:undefined}],fromBlock:20,toBlock:70},
 {filters:[{fromBlock:0,toBlock:30}],fromBlock:20,toBlock:90},
 {filters:[],fromBlock:20,toBlock:90},
 {filters:[{}],fromBlock:20,toBlock:90},
 {filters:[{fromBlock:null,toBlock:null}],fromBlock:20,toBlock:90},
 {filters:[{fromBlock:200,toBlock:300}],fromBlock:0,toBlock:100},
 {filters:[{fromBlock:-100,toBlock:-1}],fromBlock:-200,toBlock:10},
 {filters:[{fromBlock:NaN,toBlock:30},{fromBlock:0,toBlock:40}],fromBlock:0,toBlock:100},
 {filters:[{fromBlock:-Infinity,toBlock:Infinity}],fromBlock:0,toBlock:100},
 {filters:[{fromBlock:-0,toBlock:0}],fromBlock:-0,toBlock:0},
 {filters:[{fromBlock:1.5,toBlock:3.5}],fromBlock:0,toBlock:10},
];
function mark(v){if(v===undefined)return{$undefined:true};if(typeof v==='number'){if(!Number.isFinite(v))return{$number:String(v)};if(Object.is(v,-0))return{$number:'-0'}}if(Array.isArray(v))return v.map(mark);if(v&&typeof v==='object')return Object.fromEntries(Object.entries(v).map(([k,v])=>[k,mark(v)]));return v}
const cases=inputs.map((args,i)=>({name:String(i),args:mark(args),result:mark(getFilterBlockRange(args))}));const out={state:'Twelve actual pinned helper vectors; supplementary differential coverage, no additional original declaration credit.',sources:[import.meta.filename,file,upstream+'/pnpm-lock.yaml'].map(file=>({file:path.relative(root,file),sha256:crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')})),cases};fs.writeFileSync(path.join(root,'testdata/rpc/source-range.json'),JSON.stringify(out,null,2)+'\n');console.log({actual_source_vectors:cases.length,out_of_range_result:cases[6].result,source_credit:0});
