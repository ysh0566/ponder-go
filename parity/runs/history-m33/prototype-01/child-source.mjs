// Actual source extraction/error behavior, outside the frozen candidate.
import fs from 'node:fs';import path from 'node:path';import crypto from 'node:crypto';import {createRequire} from 'node:module';
const root='/root/ponder-go/work/ponder-go',upstream=path.resolve(root,'../ponder-upstream'),require=createRequire(upstream+'/packages/core/package.json'),ts=require('typescript'),file=upstream+'/packages/core/src/runtime/filter.ts',text=fs.readFileSync(file,'utf8'),tree=ts.createSourceFile(file,text,ts.ScriptTarget.Latest,true);
const node=tree.statements.find(n=>ts.isVariableStatement(n)&&n.declarationList.declarations.some(d=>d.name.getText(tree)==='getChildAddress'));if(!node)throw Error('Actual source helper missing');
const emitted=ts.transpileModule(node.getText(tree),{compilerOptions:{module:ts.ModuleKind.ESNext,target:ts.ScriptTarget.ESNext}}).outputText;
const {getChildAddress}=await import('data:text/javascript;base64,'+Buffer.from(emitted).toString('base64'));
const word='0x'+'0'.repeat(24)+'a'.repeat(40),cases=[];
function mark(v){if(v===undefined)return{$undefined:true};if(Array.isArray(v))return v.map(mark);if(v&&typeof v==='object')return{$object:Object.entries(v).map(([k,v])=>[k,mark(v)])};return v}
function capture(name,log,factory){const input={log,factory},entry={name,args:mark(input)};try{entry.result=getChildAddress(input)}catch(error){entry.error={name:error.name,message:error.message}}cases.push(entry)}
for(const location of ['topic1','topic2','topic3','offset0','offset32'])capture('valid/'+location,{topics:[word,word,word,word],data:word+word.slice(2)},{childAddressLocation:location});
for(const topics of [[],[word],[word,word],[word,word,word]])capture('missing-topic3/'+topics.length,{topics,data:word},{childAddressLocation:'topic3'});
for(const [name,value]of [['undefined',undefined],['null',null],['number',1],['empty',''],['short','0x']])capture('topic-value/'+name,{topics:[word,value],data:word},{childAddressLocation:'topic1'});
for(const [name,value]of [['undefined',undefined],['null',null],['number',1],['empty',''],['short','0x']])capture('data-value/'+name,{topics:[word],data:value},{childAddressLocation:'offset0'});
for(const location of ['offset-20','offset1.5','offsetfoo','unknown',undefined,null])capture('location/'+String(location),{topics:[word,word,word,word],data:word},{childAddressLocation:location});
fs.writeFileSync(import.meta.dirname+'/child-vectors.json',JSON.stringify({state:'Actual isolated child extraction25 vectors; no native comparison/mapping credit.',typescript:ts.version,sources:[import.meta.filename,file,upstream+'/pnpm-lock.yaml'].map(file=>({file,sha256:crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')})),cases},null,2)+'\n');console.log({actual_source_vectors:cases.length,errors:cases.filter(c=>c.error).length,source_credit:0});
