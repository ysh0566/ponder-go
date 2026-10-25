// Isolated actual pinned async generator; no native/source test credit.
import fs from 'node:fs';import path from 'node:path';import crypto from 'node:crypto';import {createRequire} from 'node:module';
const root='/root/ponder-go/work/ponder-go', upstream=path.resolve(root,'../ponder-upstream'),require=createRequire(upstream+'/packages/core/package.json'),ts=require('typescript');
const file=upstream+'/packages/core/src/sync-historical/in-memory.ts',source=fs.readFileSync(file,'utf8'),tree=ts.createSourceFile(file,source,ts.ScriptTarget.Latest,true);
const declaration=tree.statements.find(n=>ts.isFunctionDeclaration(n)&&n.name?.text==='mergeGeneratorIntervals');if(!declaration)throw Error('Actual source function missing');
const emitted=ts.transpileModule(declaration.getText(tree),{compilerOptions:{target:ts.ScriptTarget.ESNext,module:ts.ModuleKind.ESNext}}).outputText;
const {mergeGeneratorIntervals}=await import('data:text/javascript;base64,'+Buffer.from(emitted).toString('base64'));
const inputs=[
 {name:'original/common-range',generators:[{interval:[1,10],yields:[[1,4],[5,10]]},{interval:[1,10],yields:[[1,2],[3,6],[7,10]]}]},
 {name:'original/different-ranges',generators:[{interval:[1,5],yields:[[1,5]]},{interval:[3,10],yields:[[3,10]]}]},
 {name:'original/empty',generators:[]},
 {name:'empty-generator',generators:[{interval:[1,10],yields:[]}]},
 {name:'empty-and-live',generators:[{interval:[-5,10],yields:[]},{interval:[2,8],yields:[[2,8]]}]},
 {name:'same-ends',generators:[{interval:[1,10],yields:[[1,4],[5,10]]},{interval:[1,10],yields:[[1,4],[5,10]]}]},
 {name:'gapped-ranges',generators:[{interval:[1,3],yields:[[1,3]]},{interval:[7,9],yields:[[7,9]]}]},
 {name:'reverse-key-order',generators:[{interval:[7,9],yields:[[7,9]]},{interval:[1,3],yields:[[1,3]]}]},
 {name:'stale-end',generators:[{interval:[1,10],yields:[[1,4],[1,2],[5,10]]}]},
 {name:'negative',generators:[{interval:[-10,-1],yields:[[-10,-4],[-3,-1]]},{interval:[-8,0],yields:[[-8,-5],[-4,0]]}]},
 {name:'three-generators',generators:[{interval:[1,5],yields:[[1,2],[3,5]]},{interval:[2,7],yields:[[2,3],[4,7]]},{interval:[3,9],yields:[[3,8],[9,9]]}]},
 {name:'fractional',generators:[{interval:[0.5,2.5],yields:[[0.5,1.5],[2,2.5]]}]},
];
const cases=[];
for(const input of inputs){const trace=[],map=new Map();for(const [index,g] of input.generators.entries()){map.set({interval:g.interval},(async function*(){for(const interval of g.yields){trace.push({kind:'generator_yield',index,interval});yield interval;trace.push({kind:'generator_resume',index})}trace.push({kind:'generator_done',index})})())}const result=[];for await(const interval of mergeGeneratorIntervals(map)){trace.push({kind:'consumer',interval});result.push(interval)}cases.push({...input,result,trace})}
const report={state:'Twelve actual isolated source vectors, including original three helper inputs; no native execution or exact mappings.',typescript:ts.version,sources:[import.meta.filename,file,upstream+'/pnpm-lock.yaml'].map(file=>({file,sha256:crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')})),cases};
fs.writeFileSync(import.meta.dirname+'/merge-vectors.json',JSON.stringify(report,null,2)+'\n');console.log({actual_source_vectors:cases.length,source_credit:0});
