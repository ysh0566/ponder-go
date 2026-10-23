import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import crypto from 'node:crypto';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
const root=path.resolve(import.meta.dirname,'..'),upstream=path.resolve(root,'../ponder-upstream');
const require=createRequire(path.join(upstream,'packages/core/package.json')),ts=require('typescript'),viem=require('viem');
const ledger=JSON.parse(fs.readFileSync(path.join(root,'parity/test-ledger.json'),'utf8'));
const sources=[];
const read=file=>{const text=fs.readFileSync(path.join(upstream,file),'utf8');sources.push({file,sha256:crypto.createHash('sha256').update(text).digest('hex')});return text;};
function evaluate(file,dependencies,select){
 let text=read(file);
 if(select){const sf=ts.createSourceFile(file,text,99,true);text=sf.statements.filter(n=>ts.isVariableStatement(n)&&n.declarationList.declarations.some(d=>select.includes(d.name.getText(sf)))).map(n=>n.getText(sf)).join('\n');}
 const exports={};vm.runInNewContext(ts.transpileModule(text,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText,{exports,require:name=>{if(!(name in dependencies))throw Error('Unexpected dependency '+name);return dependencies[name]}},{filename:file,timeout:10000});return exports;
}
const lower=evaluate('packages/core/src/utils/lowercase.ts',{}),dedupe=evaluate('packages/core/src/utils/dedupe.ts',{});
const constants=evaluate('packages/core/src/_test/constants.ts',{viem});
const rpc=evaluate('packages/core/src/rpc/index.ts',{},['sanitizeLogTopics']);
const actions=evaluate('packages/core/src/rpc/actions.ts',{},['ADDRESS_CHUNK_SIZE']);
const filter=evaluate('packages/core/src/runtime/filter.ts',{viem,'@/utils/lowercase.js':lower,'@/rpc/index.js':rpc,'@/rpc/actions.js':actions});
const fragments=evaluate('packages/core/src/runtime/fragments.ts',{'@/utils/dedupe.js':dedupe,'@/utils/lowercase.js':lower,'./filter.js':filter});
function mark(value){if(value===undefined)return {$undefined:true};if(typeof value==='number'&&!Number.isFinite(value))return {$number:String(value)};if(typeof value==='bigint')return {$bigint:String(value)};if(value instanceof Map)return {$map:[...value.entries()].map(mark)};if(Array.isArray(value))return Array.from(value,mark);if(value&&typeof value==='object')return Object.fromEntries(Object.keys(value).sort().map(k=>[k,mark(value[k])]));return value;}
const file='packages/core/src/runtime/fragments.test.ts',source=read(file),sf=ts.createSourceFile(file,source,99,true),originals=ledger.tests.filter(x=>x.source_file===file);
let current;const output=[];
const wrapped=Object.fromEntries(['getFragments','encodeFragment','decodeFragment','recoverFilter'].map(name=>[name,(...args)=>{const input=mark(args),result=fragments[name](...args);current.calls.push({operation:name,args:input,result:mark(result)});return result;}]));
const transformed=ts.transform(sf,[ctx=>function visit(n){if(ts.isImportDeclaration(n))return undefined;if(ts.isCallExpression(n)&&n.expression.getText(sf)==='test')return ts.factory.updateCallExpression(n,n.expression,n.typeArguments,[...n.arguments,ts.factory.createNumericLiteral(n.getStart(sf))]);return ts.visitEachChild(n,visit,ctx);}]).transformed[0];
const text=ts.transpileModule(ts.createPrinter().printFile(transformed),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None}}).outputText;
vm.runInNewContext(text,{...constants,zeroHash:viem.zeroHash,...wrapped,test:(name,fn,start)=>{const original=originals.find(x=>x.start===start);if(!original||original.title!==name)throw Error('Missing source case');current={id:original.id,source_file:file,source_name:original.full_name,source_sha256:original.source_sha256,source_assertions:original.assertions,source_contexts:original.contexts,calls:[],assertions:[]};fn();output.push(current);current=null;},expect:actual=>({toStrictEqual:expected=>{assert.deepStrictEqual(mark(actual),mark(expected));current.assertions.push({matcher:'toStrictEqual',call_index:current.calls.length-1,expected:mark(expected)});},toMatchInlineSnapshot:snapshot=>{const expected=vm.runInNewContext('('+snapshot+')',{}, {timeout:1000});assert.deepStrictEqual(mark(actual),mark(expected));current.assertions.push({matcher:'toMatchInlineSnapshot',call_index:current.calls.length-1,expected:mark(expected),snapshot});}})},{filename:file,timeout:15000});
if(output.length!==originals.length)throw Error('Missing source records');
const result={baseline:ledger.baseline,method:'Evaluate unchanged fixed-source test bodies and input loops with actual pinned functions. All original matcher expectations are verified, snapshots retained, undefined/null tagged losslessly. Go replays every operation and every original runtime assertion; expanded assertions do not add source records.',sources,cases:output};
fs.mkdirSync(path.join(root,'testdata/runtime'),{recursive:true});fs.writeFileSync(path.join(root,'testdata/runtime/fragments.json'),JSON.stringify(result,null,2)+'\n');console.log({source_cases:output.length,static_assertion_expressions:originals.reduce((n,t)=>n+t.assertions.length,0),runtime_assertions:output.reduce((n,t)=>n+t.assertions.length,0),operation_calls:output.reduce((n,t)=>n+t.calls.length,0)});

const filterFile='packages/core/src/runtime/filter.test.ts',filterSource=read(filterFile),filterSF=ts.createSourceFile(filterFile,filterSource,99,true);
const selected=ledger.tests.filter(t=>t.source_file===filterFile&&(t.title.startsWith('mergeLogFiltersToBlockRequest ')||t.title.startsWith('getChildAddress()')));
const filterOutput=[];let lastResult;
const filterWrapped=Object.fromEntries(['getChildAddress','mergeLogFiltersToBlockRequest'].map(name=>[name,(...args)=>{const input=mark(args),result=filter[name](...args);current.calls.push({operation:name,args:input,result:mark(result)});lastResult=result;return result;}]));
function selector(actual){if(actual===lastResult)return [];for(const key of Object.keys(lastResult??{}))if(lastResult[key]===actual)return [key];throw Error('Untracked original assertion selection');}
const filterTransformed=ts.transform(filterSF,[ctx=>function visit(n){
 if(ts.isImportDeclaration(n))return undefined;
 if(ts.isExpressionStatement(n)&&ts.isCallExpression(n.expression)){
  const call=n.expression,name=call.expression.getText(filterSF);
  if(name==='beforeEach')return undefined;
  if(name==='test'){
   if(!selected.some(t=>t.start===call.getStart(filterSF)))return undefined;
   return ts.factory.updateExpressionStatement(n,ts.factory.updateCallExpression(call,call.expression,call.typeArguments,[...call.arguments,ts.factory.createNumericLiteral(call.getStart(filterSF))]));
  }
 }
 return ts.visitEachChild(n,visit,ctx);
}]).transformed[0];
const filterCode=ts.transpileModule(ts.createPrinter().printFile(filterTransformed),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None}}).outputText;
vm.runInNewContext(filterCode,{...constants,...viem,...actions,...filterWrapped,test:(name,fn,start)=>{const original=selected.find(t=>t.start===start);if(!original||original.title!==name)throw Error('Missing filter source');current={id:original.id,source_file:filterFile,source_name:original.full_name,source_sha256:original.source_sha256,source_assertions:original.assertions,source_contexts:original.contexts,calls:[],assertions:[]};fn();if(current.assertions.length!==original.assertions.length)throw Error('Lost filter assertion');filterOutput.push(current);current=null;},expect:actual=>({toStrictEqual:expected=>{assert.deepStrictEqual(mark(actual),mark(expected));current.assertions.push({matcher:'toStrictEqual',call_index:current.calls.length-1,path:selector(actual),expected:mark(expected)});},toBe:expected=>{assert.strictEqual(actual,expected);current.assertions.push({matcher:'toBe',call_index:current.calls.length-1,path:selector(actual),expected:mark(expected)});},toHaveLength:expected=>{assert.strictEqual(actual.length,expected);current.assertions.push({matcher:'toHaveLength',call_index:current.calls.length-1,path:selector(actual),expected});}})},{filename:filterFile,timeout:10000});
if(filterOutput.length!==selected.length)throw Error('Missing selected filter records');
fs.writeFileSync(path.join(root,'testdata/runtime/filters.json'),JSON.stringify({baseline:ledger.baseline,method:'Only complete static-input source cases selected; deployment/mint/range-selector cases remain unimplemented. Original test body/constants/threshold used, no generated contract ABI replacement. Native fixtures retain source contexts; full original filter oracle is separate.',sources,cases:filterOutput},null,2)+'\n');console.log({filter_source_cases:filterOutput.length,filter_original_assertions:filterOutput.reduce((n,t)=>n+t.assertions.length,0)});

// The block case needs a genuine RPC genesis block; keep the source fixture
// expression and expectations, then obtain the block in the native test.
const blockOriginal=ledger.tests.find(t=>t.source_file===filterFile&&t.title==='isBlockFilterMatched');
const helperFile='packages/core/src/_test/utils.ts',helperSource=read(helperFile),helperSF=ts.createSourceFile(helperFile,helperSource,99,true);
const helperStatement=helperSF.statements.find(n=>ts.isVariableStatement(n)&&n.declarationList.declarations.some(d=>d.name.getText(helperSF)==='getBlocksIndexingBuild'));
const helperFilter=(()=>{let found;function walk(n){if(ts.isPropertyAssignment(n)&&n.name.getText(helperSF)==='filter'&&ts.isObjectLiteralExpression(n.initializer))found=n.initializer;ts.forEachChild(n,walk);}walk(helperStatement);return found.getText(helperSF);})();
const blockInput=vm.runInNewContext('('+helperFilter+')',{params:{interval:1},defaultBlockFilterInclude:filter.defaultBlockFilterInclude});
fs.writeFileSync(path.join(root,'testdata/runtime/block-filter.json'),JSON.stringify({baseline:ledger.baseline,sources,source_id:blockOriginal.id,source_name:blockOriginal.full_name,source_file:filterFile,source_sha256:blockOriginal.source_sha256,source_assertions:blockOriginal.assertions,source_contexts:blockOriginal.contexts,helper_source:helperStatement.getText(helperSF),initial_filter:mark(blockInput),rpc:{method:'eth_getBlockByNumber',params:['0x0',true]},expected:[true,false],mutation:{interval:2,offset:1}},null,2)+'\n');

// Extra differential vectors exercise the actual pinned public functions; these
// are synthetic inputs, never extra source-test credit or a generated ABI.
const vectors=[];
function vector(operation,args){let result,error;try{result=({...filter,...fragments})[operation](...args);}catch(e){error={name:e.name,message:e.message};}vectors.push({operation,args:mark(args),...(error?{error}:{result:mark(result)})});}
const aa='0x'+'ab'.repeat(20),bb='0x'+'cd'.repeat(20),cc='0x'+'ef'.repeat(20),topic='0x'+'12'.repeat(32),topicB='0x'+'34'.repeat(32);
const parent={id:'factory-id',type:'log',chainId:1,sourceId:'Parent',address:[aa,bb],eventSelector:topic,childAddressLocation:'topic1',fromBlock:5,toBlock:20};
const addressValues=[undefined,null,aa,[aa,bb],[],parent,{...parent,address:undefined}, {...parent,address:[]}];
for(const a of addressValues){vector('isAddressFactory',[a]);vector('getAddressFragments',[a]);}
for(const a of [null,aa,{address:aa,eventSelector:topic,childAddressLocation:'topic1',fromBlock:null,toBlock:20}]){vector('isFragmentAddressFactory',[a]);vector('fragmentAddressToId',[a]);}
for(let i=0;i<addressValues.length;i++){
 const address=addressValues[i];
 if(address!==null){const p={...parent,address};vector('getFactoryFragments',[p]);vector('getFactoryFragmentIds',[p]);vector('getFactoryFragmentId',[p,aa.toUpperCase().replace('0X','0x')]);vector('recoverFactory',[p,[{address:bb},{address:bb},{address:aa}]]);}
 for(const type of ['block','log','transaction','trace','transfer']){
  const f={type,chainId:1,sourceId:'Input',address,fromAddress:address,toAddress:addressValues[(i+3)%addressValues.length],topic0:topic,topic1:i%2?[topicB,topic]:undefined,topic2:null,topic3:undefined,functionSelector:'0x12345678',hasTransactionReceipt:i%2===0,interval:3,offset:1,fromBlock:i,toBlock:i+10,include:['block.hash','transaction.hash','block.number','trace.from','transactionReceipt.status']};
  vector('getFragments',[f]);const fs=fragments.getFragments(f).map(x=>x.fragment);vector('recoverFilter',[f,fs.slice(0,2)]);
  for(const x of fs.slice(0,2)){vector('encodeFragment',[x]);vector('decodeFragment',[fragments.encodeFragment(x)]);}
  vector('getFilterFactories',[f]);vector('getFilterFromBlock',[f]);vector('getFilterToBlock',[f]);
  for(const n of [i-1,i,i+10,i+11])vector('isBlockInFilter',[f,n]);
 }
}
for(const factory of [parent,{...parent,address:aa},{...parent,address:undefined},{...parent,address:[]}])for(const address of [aa,bb,cc,aa.toUpperCase().replace('0X','0x')])for(const n of [4,5,20,21]){
 const log={address,topics:[topic,topicB],data:'0x',blockNumber:n%2?n:'0x'+n.toString(16)};
 vector('isLogFactoryMatched',[{factory,log}]);
}
for(const n of [0,4,5,20,21])for(const address of [aa,cc,aa.toUpperCase().replace('0X','0x')])for(const to of [bb,undefined,null]){
 const f={type:'log',address:[aa],fromAddress:aa,toAddress:[bb],topic0:topic,topic1:null,topic2:undefined,topic3:undefined,functionSelector:['0x12345678'],callType:undefined,fromBlock:5,toBlock:20};
 const transaction={from:address,to,blockNumber:'0x'+n.toString(16)},trace={...transaction,type:'CALL',input:'0x12345678abcdef',value:'0x01'},block={number:n};
 vector('isLogFilterMatched',[{filter:f,log:{address,blockNumber:n,topics:[topic]}}]);
 vector('isTransactionFilterMatched',[{filter:f,transaction}]);vector('isTraceFilterMatched',[{filter:f,trace,block}]);vector('isTransferFilterMatched',[{filter:f,trace,block}]);
 vector('isBlockFilterMatched',[{filter:{...f,interval:3,offset:2},block}]);
}
for(const value of [undefined,null,'0x0','0x1','1000000000000000000000000000000','invalid'])for(const type of ['CALL','CREATE','DELEGATECALL','CALLCODE'])vector('isTransferFilterMatched',[{filter:{},trace:{from:aa,to:bb,value,type},block:{number:1}}]);
for(const address of [undefined,aa,cc,aa.toUpperCase().replace('0X','0x')])for(const n of [4,5,20])vector('isAddressMatched',[{address,blockNumber:n,childAddresses:new Map([[aa,5],[bb,20]])}]);
for(const interval of [[0,9],[2,11],[3,12],[10,19],[0,0]])for(const dimension of ['same','address','topic0','two','factory','empty-factory','wide-factory']){
 const f={type:'log',address:aa,topic0:topic,topic1:undefined,topic2:undefined,topic3:undefined},g={...f};
 const children=new Map([['factory-id',new Map([[aa,5],[bb,10]])]]);
 if(dimension==='address')g.address=bb;if(dimension==='topic0')g.topic0=topicB;if(dimension==='two'){g.address=bb;g.topic0=topicB;}if(dimension.endsWith('factory'))g.address=parent;
 if(dimension==='empty-factory')children.set('factory-id',new Map());
 vector('mergeLogFiltersToRequests',[[{filter:f,interval:[0,9]},{filter:g,interval}],children,dimension==='wide-factory'?2:100]);
}
for(const count of [0,1,49,50,51]){const addresses=Array.from({length:count},(_,i)=>'0x'+i.toString(16).padStart(40,'0'));vector('mergeLogFiltersToBlockRequest',[{logFilters:[{type:'log',address:addresses,topic0:topic,topic1:null}],factories:[]}]);}
const includes=[{include:['block.hash','block.number','transaction.hash','trace.from']},{include:['block.hash','transactionReceipt.status','trace.to']}];
for(const name of ['unionFilterIncludeBlock','unionFilterIncludeTransaction','unionFilterIncludeTransactionReceipt','unionFilterIncludeTrace'])vector(name,[includes]);
fs.writeFileSync(path.join(root,'testdata/runtime/filter-differential.json'),JSON.stringify({baseline:ledger.baseline,counting:'Synthetic differential vectors only; no source-test credit.',sources,vectors},null,2)+'\n');console.log({extra_differential_vectors:vectors.length,errors:vectors.filter(x=>x.error).length});
