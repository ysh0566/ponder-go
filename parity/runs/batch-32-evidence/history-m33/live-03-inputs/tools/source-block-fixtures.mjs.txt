// Actual source helper isolation for the historical fetch prerequisites.
import fs from 'node:fs';import path from 'node:path';import crypto from 'node:crypto';import {createRequire} from 'node:module';import {pathToFileURL} from 'node:url';
const root=path.resolve(import.meta.dirname,'..'),upstream=path.resolve(root,'../ponder-upstream'),require=createRequire(upstream+'/packages/core/package.json'),ts=require('typescript'),viem=require('viem');
const file=upstream+'/packages/core/src/rpc/actions.ts',text=fs.readFileSync(file,'utf8'),tree=ts.createSourceFile(file,text,ts.ScriptTarget.Latest,true);
const names=['standardizeBlock','standardizeTransactions','validateTransactionsAndBlock','validateTracesAndBlock','validateReceiptsAndBlock','requestText'];
const selected=tree.statements.filter(n=>ts.isFunctionDeclaration(n)?names.includes(n.name?.text):ts.isVariableStatement(n)&&n.declarationList.declarations.some(d=>names.includes(d.name.getText(tree))));if(selected.length!==names.length)throw Error('Changed actual declarations');
const pgFile=upstream+'/packages/core/src/utils/pg.ts',pg=ts.createSourceFile(pgFile,fs.readFileSync(pgFile,'utf8'),ts.ScriptTarget.Latest,true);
const constants=pg.statements.filter(n=>ts.isVariableStatement(n)&&n.declarationList.declarations.some(d=>['PG_BIGINT_MAX','PG_INTEGER_MAX'].includes(d.name.getText(pg))));if(constants.length!==2)throw Error('Missing source limits');
const errors=await import(pathToFileURL(upstream+'/packages/core/src/internal/errors.ts'));
const emitted=ts.transpileModule([...constants.map(n=>n.getText(pg)),...selected.map(n=>n.getText(tree))].join('\n'),{compilerOptions:{target:ts.ScriptTarget.ESNext,module:ts.ModuleKind.ESNext}}).outputText.replaceAll('export ','');
const source=new Function('RpcProviderError','hexToBigInt','hexToNumber','zeroAddress','zeroHash',emitted+'\nreturn{'+names.join(',')+'}')(errors.RpcProviderError,viem.hexToBigInt,viem.hexToNumber,viem.zeroAddress,viem.zeroHash);
function mark(v){if(v===undefined)return{$undefined:true};if(typeof v==='bigint')return{$bigint:String(v)};if(typeof v==='number'&&!Number.isFinite(v))return{$number:String(v)};if(Array.isArray(v))return v.map(mark);if(v&&typeof v==='object')return{$object:Object.entries(v).map(([k,v])=>[k,mark(v)])};return v}
const request={method:'eth_getBlockByNumber',params:['0x1',true]},header={hash:'0xaa',number:'0x1',timestamp:'0x2',logsBloom:'0x',parentHash:'0xbb',transactions:[]},transaction={hash:'0xcc',transactionIndex:'0x0',blockNumber:'0x1',blockHash:'0xaa',from:viem.zeroAddress,to:null};
const cases=[];
function capture(name,method,args){const input=mark(args);let result,error;try{result=mark(source[method](...args))}catch(e){error={name:e.name,message:e.message,meta:mark(e.meta)}}cases.push({name,method,args:input,result,error,after:mark(args)})}
capture('minimal-block','standardizeBlock',[structuredClone(header),request]);
for(const key of ['hash','number','timestamp','logsBloom','parentHash','transactions']){const value=structuredClone(header);delete value[key];capture('missing/'+key,'standardizeBlock',[value,request]);const explicit=structuredClone(header);explicit[key]=undefined;capture('undefined/'+key,'standardizeBlock',[explicit,request])}
for(const key of ['miner','gasUsed','gasLimit','baseFeePerGas','nonce','mixHash','stateRoot','transactionsRoot','sha3Uncles','size','difficulty','totalDifficulty','extraData'])capture('null/'+key,'standardizeBlock',[{...structuredClone(header),[key]:null},request]);
for(const key of ['number','timestamp'])capture('overflow/'+key,'standardizeBlock',[{...structuredClone(header),[key]:'0x8000000000000000'},request]);
capture('header-hashes','standardizeBlock',[{...structuredClone(header),transactions:['0xcc']},{method:'eth_getBlockByNumber',params:['0x1',false]}]);
capture('subscription','standardizeBlock',[{...structuredClone(header),transactions:['0xcc']},{method:'eth_subscribe',params:['newHeads']}]);
capture('transaction-defaults','standardizeBlock',[{...structuredClone(header),transactions:[structuredClone(transaction)]},request]);
capture('transaction-sorting','standardizeBlock',[{...structuredClone(header),transactions:[{...transaction,hash:'0xdd',transactionIndex:'0x1'},structuredClone(transaction)]},request]);
for(const key of ['blockHash','blockNumber'])capture('transaction-mismatch/'+key,'validateTransactionsAndBlock',[{...structuredClone(header),transactions:[{...transaction,[key]:'0xff'}]},request]);
capture('transactions-valid','validateTransactionsAndBlock',[{...structuredClone(header),transactions:[transaction]},request]);
const block={...structuredClone(header),transactions:[transaction]},traceRequest={method:'debug_traceBlockByNumber',params:['0x1',{tracer:'callTracer'}]},receiptRequest={method:'eth_getBlockReceipts',params:['0xaa']},receipt={blockHash:'0xaa',blockNumber:'0x1',transactionIndex:'0x0',transactionHash:'0xcc'};
capture('traces-valid','validateTracesAndBlock',[[{transactionHash:'0xcc'}],block,traceRequest,request]);capture('traces-wrong-hash','validateTracesAndBlock',[[{transactionHash:'0xdd'}],block,traceRequest,request]);
capture('receipts-valid','validateReceiptsAndBlock',[[receipt],block,receiptRequest,request]);
for(const key of ['blockHash','blockNumber','transactionIndex','transactionHash'])capture('receipt-mismatch/'+key,'validateReceiptsAndBlock',[[{...receipt,[key]:'0xff'}],block,receiptRequest,request]);
capture('block-receipt-count','validateReceiptsAndBlock',[[],block,receiptRequest,request]);capture('single-receipt-count-not-checked','validateReceiptsAndBlock',[[],block,{method:'eth_getTransactionReceipt',params:['0xcc']},request]);
const entry=require.resolve('viem'),base=path.dirname(entry),sources=[import.meta.filename,file,pgFile,upstream+'/packages/core/src/internal/errors.ts',upstream+'/pnpm-lock.yaml',entry,base+'/utils/encoding/fromHex.js'];
fs.writeFileSync(path.join(root,'testdata/rpc/source-block.json'),JSON.stringify({state:'Actual isolated helpers and mutations/errors only; no native comparison, original declarations or mapping credit.',typescript:ts.version,sources:sources.map(file=>({file:path.relative(root,file),sha256:crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')})),cases},null,2)+'\n');console.log({actual_source_vectors:cases.length,errors:cases.filter(c=>c.error).length,source_credit:0});
