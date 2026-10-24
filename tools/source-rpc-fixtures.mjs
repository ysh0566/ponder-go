// Prepare actual pinned source helper vectors before native input freezing.
import fs from 'node:fs';import path from 'node:path';import crypto from 'node:crypto';import{createRequire}from'node:module';import{pathToFileURL}from'node:url';
const root=path.resolve(import.meta.dirname,'..'),upstream=path.resolve(root,'../ponder-upstream'),require=createRequire(upstream+'/packages/core/package.json');
const {orderObject}=await import(pathToFileURL(upstream+'/packages/core/src/utils/order.ts'));
const {toLowerCase}=await import(pathToFileURL(upstream+'/packages/core/src/utils/lowercase.ts'));
const ts=require('typescript');const source=fs.readFileSync(upstream+'/packages/core/src/indexing/client.ts','utf8');
const tree=ts.createSourceFile('client.ts',source,ts.ScriptTarget.Latest,true);const declaration=tree.statements.find(s=>ts.isVariableStatement(s)&&s.declarationList.declarations.some(d=>d.name.getText(tree)==='extractBlockNumberParam'));
if(!declaration)throw Error('Actual extractBlockNumberParam export missing');
const emitted=ts.transpileModule(declaration.getText(tree),{compilerOptions:{target:ts.ScriptTarget.ESNext,module:ts.ModuleKind.ESNext}}).outputText;
const {extractBlockNumberParam}=await import('data:text/javascript;base64,'+Buffer.from(emitted).toString('base64'));
const viem=require('viem');
const cases=[];function vector(name,request){const entry={name,request:mark(request)};try{entry.serialized=JSON.stringify(orderObject(request));entry.lowercase=toLowerCase(entry.serialized);entry.hash=crypto.createHash('md5').update(entry.lowercase).digest('hex');const block=extractBlockNumberParam(request);entry.block=mark(block);entry.numeric_block=viem.isHex(block)?viem.hexToNumber(block):null;}catch(error){entry.error={name:error.name,message:error.message};}cases.push(entry);}
for(const n of ['0x1','0x2','0x3','0x4'])vector('original-invalid-call/'+n,{method:'eth_call',params:[n]});
for(const method of ['eth_getBlockByNumber','eth_getBlockTransactionCountByNumber','eth_getTransactionByBlockNumberAndIndex','eth_getUncleByBlockNumberAndIndex','debug_traceBlockByNumber'])vector('block-param-0/'+method,{method,params:['0x10']});
for(const method of ['eth_getBalance','eth_call','eth_getCode','eth_estimateGas','eth_feeHistory','eth_getTransactionCount'])vector('block-param-1/'+method,{method,params:['0x1','0x10']});
for(const method of ['eth_getProof','eth_getStorageAt'])vector('block-param-2/'+method,{method,params:['0x1','0x2','0x10']});
for(const [name,request]of[
 ['key-order',{params:[{value:'0xAB',to:'0xABC',data:'0xFF'},'0x1'],method:'eth_call'}],
 ['case-normalized',{method:'ETH_CALL',params:['0xA']}],['genesis',{method:'eth_call',params:[{},'0x0']}],['latest',{method:'eth_call',params:[{},'latest']}],['missing-params',{method:'eth_call'}],['unrelated-method',{method:'eth_chainId',params:[]}],
 ['numeric-keys',{method:'eth_call',params:[{'10':'a','2':'b','1':'c','01':'d'},'0x1']}],
 ['html-and-unicode',{method:'eth_call',params:[{data:'<>\\u2028\u2028\u2029ßİΟΣA'},'0x1']}],
 ['undefined-property',{method:'eth_call',params:[{to:undefined},'0x1']}],['undefined-array',{method:'eth_call',params:[undefined,'0x1']}],['null-object',{method:'eth_call',params:[null,'0x1']}],['bigint',{method:'eth_call',params:[{value:1n},'0x1']}],
 ['nonfinite',{method:'eth_call',params:[{value:NaN,other:Infinity},'0x1']}],
 ['proto-key',JSON.parse('{"method":"eth_call","params":[{"__proto__":{"x":1},"to":"0xA"},"0x1"]}')],
 ['utf16-key-order',{method:'eth_call',params:[{'\u{10000}':'a','\ue000':'b'},'0x1']}],
 ...[0,-0,1e-7,1e-6,1e20,1e21,9007199254740991,9007199254740992].map(n=>['number/'+String(n),{method:'eth_call',params:[{n},'0x1']}]),
])vector(name,request);
function mark(v){if(v===undefined)return{$undefined:true};if(typeof v==='bigint')return{$bigint:String(v)};if(typeof v==='number'&&!Number.isFinite(v))return{$number:String(v)};if(Object.is(v,-0))return{$number:'-0'};if(Array.isArray(v))return v.map(mark);if(v&&typeof v==='object')return{$object:Object.entries(v).map(([k,v])=>[k,mark(v)])};return v;}
const viemEntry=require.resolve('viem'),viemDirectory=path.dirname(viemEntry);
const out={state:'Actual isolated pinned helper exports, not original test execution or native acceptance; zero credit.',source_revision:'17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334',node:process.version,typescript:ts.version,sources:[import.meta.filename,upstream+'/packages/core/src/utils/order.ts',upstream+'/packages/core/src/utils/lowercase.ts',upstream+'/packages/core/src/indexing/client.ts',upstream+'/pnpm-lock.yaml',viemEntry,path.join(viemDirectory,'utils/encoding/fromHex.js'),path.join(viemDirectory,'utils/data/isHex.js')].map(file=>({file:path.relative(root,file),sha256:crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')})),cases};
fs.writeFileSync(path.join(root,'testdata/rpc/source-cache.json'),JSON.stringify(out,null,2)+'\n');console.log({actual_vectors:cases.length,errors:cases.filter(c=>c.error).map(c=>({name:c.name,error:c.error.name})),source_credit:0});
