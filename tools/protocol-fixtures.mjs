import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import crypto from 'node:crypto';
import {createRequire} from 'node:module';
const root=path.resolve(import.meta.dirname,'..'),upstream=path.resolve(root,'../ponder-upstream');
const require=createRequire(path.join(upstream,'packages/core/package.json')),ts=require('typescript'),viem=require('viem');
const files=['packages/core/src/sync-store/encode.ts','packages/core/src/utils/lowercase.ts'];
const source=Object.fromEntries(files.map(file=>[file,fs.readFileSync(path.join(upstream,file),'utf8')]));
function evaluate(file,dependencies){const exports={};vm.runInNewContext(ts.transpileModule(source[file],{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText,{exports,require:name=>{if(!(name in dependencies))throw Error('Unexpected import '+name);return dependencies[name]}},{filename:file,timeout:5000});return exports;}
const lower=evaluate(files[1],{}),encode=evaluate(files[0],{viem,'@/utils/lowercase.js':lower});
const address='0xABCDEF0000000000000000000000000000000001',hash='0x'+'12'.repeat(32),huge='0x'+'f'.repeat(64);
const base={number:'0x1',timestamp:'0x123',hash,parentHash:hash,logsBloom:'0x'+'00'.repeat(256),miner:address,gasUsed:'0x5208',gasLimit:'0xffffff',baseFeePerGas:huge,nonce:'0x0000000000000000',mixHash:hash,stateRoot:hash,receiptsRoot:hash,transactionsRoot:hash,sha3Uncles:hash,size:'0x321',difficulty:'0x0',totalDifficulty:huge,extraData:'0x'};
const tx={blockNumber:'0x1',transactionIndex:'0x0',hash,blockHash:hash,from:address,to:address,input:'0x1234',value:huge,nonce:'0x0',r:hash,s:hash,v:'0x0',type:'0x2',gas:'0x5208',gasPrice:huge,maxFeePerGas:huge,maxPriorityFeePerGas:'0x0',accessList:[]};
const receipt={blockNumber:'0x1',transactionIndex:'0x0',transactionHash:hash,blockHash:hash,from:address,to:address,contractAddress:address,logsBloom:base.logsBloom,gasUsed:'0x5208',cumulativeGasUsed:'0x5208',effectiveGasPrice:huge,status:'0x1',type:'0x2'};
const trace={traceAddress:[],from:address,to:address,input:'0x1234',output:'0x',value:huge,type:'CALL',gas:'0x5208',gasUsed:'0x1234'};
const log={blockNumber:'0x1',logIndex:'0x0',transactionIndex:'0x0',blockHash:hash,transactionHash:hash,address,topics:[hash,hash,hash,hash],data:'0x1234'};
const cases=[];
for(const variant of ['full','nullable','empty_topics_nested_trace','zero_quantities']){
 const b={...base},t={...tx},r={...receipt},tr={...trace},l={...log};
 if(variant==='nullable'){for(const key of ['baseFeePerGas','nonce','mixHash','sha3Uncles','totalDifficulty'])delete b[key];for(const key of ['to','r','s','v','type','gasPrice','maxFeePerGas','maxPriorityFeePerGas','accessList'])delete t[key];r.to=null;r.contractAddress=null;delete tr.output;delete tr.value;tr.to=null;l.topics=[hash];}
 if(variant==='empty_topics_nested_trace'){l.topics=[];tr.traceAddress=[0,12,1];t.accessList=[{address,storageKeys:[hash]}];}
 if(variant==='zero_quantities'){b.baseFeePerGas='0x0';b.totalDifficulty='0x0';t.value='0x0';t.v='0x0';tr.value='0x0';}
 for(const [table,input,operation,arg] of [['blocks',b,'encodeBlock',{block:b}],['logs',l,'encodeLog',{log:l}],['transactions',t,'encodeTransaction',{transaction:t}],['transaction_receipts',r,'encodeTransactionReceipt',{transactionReceipt:r}],['traces',{...tr,blockNumber:b.number,transactionIndex:t.transactionIndex},'encodeTrace',{trace:{trace:tr},block:b,transaction:t}]]){
  const result=encode[operation]({...arg,chainId:1}),expected={};
  for(const [key,value] of Object.entries(result))expected[key.replace(/[A-Z]/g,ch=>'_'+ch.toLowerCase())]=typeof value==='bigint'?value.toString():Array.isArray(value)?'{'+value.join(',')+'}':value;
  cases.push({name:table+'/'+variant,table,chain:1,input,expected});
 }
}
const out={baseline:'17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334',counting:'20 generated differential vectors from unmodified source functions, not upstream test records. No generated contract fixture is substituted.',sources:files.map(file=>({file,sha256:crypto.createHash('sha256').update(source[file]).digest('hex')})),cases};
fs.mkdirSync(path.join(root,'testdata/protocol'),{recursive:true});fs.writeFileSync(path.join(root,'testdata/protocol/encoding.json'),JSON.stringify(out,null,2)+'\n');console.log({vectors:cases.length,sources:out.sources});
