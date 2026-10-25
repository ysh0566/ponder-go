// Supplemental actual-source probe. Logical request registration is observed;
// network arrival order is neither exercised nor claimed. No original test credit.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
const root=path.resolve(import.meta.dirname,'..'),upstream=path.resolve(root,'../ponder-upstream');
const require=createRequire(path.join(upstream,'packages/core/package.json')),ts=require('typescript');
const loaded=[],cache=new Map();
function load(file){
 file=path.resolve(file);if(!fs.existsSync(file)&&file.endsWith('.js'))file=file.slice(0,-3)+'.ts';
 if(cache.has(file))return cache.get(file).exports;
 const module={exports:{}};cache.set(file,module);loaded.push(file);
 const code=ts.transpileModule(fs.readFileSync(file,'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS,esModuleInterop:true}}).outputText;
 const local=id=>id==='@ponder/utils'?load(upstream+'/packages/utils/src/index.ts'):id.startsWith('@/')?load(upstream+'/packages/core/src/'+id.slice(2)):id.startsWith('.')?load(path.resolve(path.dirname(file),id)):require(id);
 new Function('require','module','exports',code)(local,module,module.exports);
 return module.exports;
}
const {getLocalSyncProgress}=load(upstream+'/packages/core/src/runtime/index.ts');
const {EMPTY_BLOCK_FILTER}=load(upstream+'/packages/core/src/_test/constants.ts');
const {getFragments}=load(upstream+'/packages/core/src/runtime/fragments.ts');
const zeroHash='0x'+'00'.repeat(32);
// Independent maps of the same valid JSON-RPC block: identity, not differing
// heights or request payloads, discriminates the three same-height responses.
const block={number:'0x0',hash:zeroHash,parentHash:zeroHash,timestamp:'0x0',logsBloom:'0x'+'00'.repeat(256),miner:'0x'+'00'.repeat(20),gasUsed:'0x0',gasLimit:'0x1c9c380',baseFeePerGas:'0x3b9aca00',nonce:'0x0000000000000000',mixHash:zeroHash,stateRoot:zeroHash,transactionsRoot:zeroHash,receiptsRoot:zeroHash,sha3Uncles:zeroHash,size:'0x200',difficulty:'0x0',totalDifficulty:'0x0',extraData:'0x',transactions:[],uncles:[]};
const responses=Array.from({length:3},()=>structuredClone(block));
const finalizedBlock=structuredClone(block);
const filter={...EMPTY_BLOCK_FILTER,fromBlock:0,toBlock:0};
const cachedIntervals=new Map([[filter,getFragments(filter).map(({fragment})=>({fragment,intervals:[[0,0]]}))]]);
const timeline=[],calls=[],resolvers=[];
const rpc={request(request,context){const ordinal=calls.length+1;calls.push({ordinal,request:structuredClone(request),context:structuredClone(context)});timeline.push({action:'register',ordinal});return new Promise(resolve=>resolvers.push(resolve));}};
const inputs={filter:structuredClone(filter),cache:[...cachedIntervals.values()][0],finalizedBlock:structuredClone(finalizedBlock),responses_before:structuredClone(responses)};
const pending=getLocalSyncProgress({common:{},chain:{id:1},rpc,filters:[filter],finalizedBlock,cachedIntervals});
assert.equal(calls.length,2,'start and cached requests register synchronously before awaiting responses');
resolvers[1](responses[1]);timeline.push({action:'resolve',ordinal:2});
await new Promise(setImmediate);
assert.equal(calls.length,2,'end request must not register while start remains pending');
resolvers[0](responses[0]);timeline.push({action:'resolve',ordinal:1});
await new Promise(setImmediate);
assert.equal(calls.length,3,'end registers after both diagnostic responses complete');
resolvers[2](responses[2]);timeline.push({action:'resolve',ordinal:3});
const progress=await pending;
for(const call of calls){assert.deepEqual(call.request,{method:'eth_getBlockByNumber',params:['0x0',false]});assert.deepEqual(call.context,{retryNullBlockRequest:true});}
assert.equal(progress.start,responses[0]);assert.equal(progress.current,responses[1]);assert.equal(progress.end,responses[2]);assert.equal(progress.finalized,finalizedBlock);
assert.equal(new Set([progress.start,progress.current,progress.end]).size,3);
const sha=file=>crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');
const result={source_credit:0,pristine_file:false,scope:'Actual pinned getLocalSyncProgress and eth_getBlockByNumber, unmodified source via TS CommonJS module loader; controlled RPC promises observe logical request registration, not network arrival order.',upstream_commit:'17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334',typescript:ts.version,sources:[...loaded,upstream+'/pnpm-lock.yaml',import.meta.filename].map(file=>({file:path.relative(root,file),sha256:sha(file)})),inputs,calls,timeline,result_identity:{start_response:responses.indexOf(progress.start)+1,current_response:responses.indexOf(progress.current)+1,end_response:responses.indexOf(progress.end)+1,finalized_preserved:progress.finalized===finalizedBlock,three_distinct_response_maps:new Set([progress.start,progress.current,progress.end]).size===3},state:'passed'};
const folder=path.join(root,'parity/runs/runtime-index-progress-order-source');fs.mkdirSync(folder,{recursive:true});
fs.writeFileSync(path.join(folder,'probe.json'),JSON.stringify(result,null,2)+'\n');
console.log(JSON.stringify({state:result.state,source_credit:0,calls,timeline,result_identity:result.result_identity}));
