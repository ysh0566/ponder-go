// Reconstructed supplemental oracle after the environment loss. No original-test
// credit: decode real ABI bytes, observe live Object.keys, then profile that exact
// decoded Event. Never rebuild decoded objects from JSON to infer field identity.
import assert from 'node:assert/strict';
import {encodeAbiParameters,encodeEventTopics,encodeFunctionData,encodeFunctionResult,zeroAddress} from 'viem';
import {decodeEvents} from '@/runtime/events.js';
import {recordProfilePattern as recordRowPattern} from '@/indexing-store/profile.js';
import {recordProfilePattern as recordRpcPattern} from '@/indexing/profile.js';

export function observeDecodedFieldOrder(common:any,chain:any,table:any){
 const observations:any[]=[];
 const block={hash:'0x'+'1'.repeat(64),number:50n,timestamp:5000n,miner:zeroAddress};
 const transaction={hash:'0x'+'2'.repeat(64),from:zeroAddress,to:zeroAddress,transactionIndex:0};
 const primaryKeys:any=new Map([[table,[['id',table.id]]]]);
 const rpcAbi:any=[{type:'function',name:'lookup',stateMutability:'view',inputs:[{name:'id',type:'uint256'}],outputs:[{type:'bool'}]}];
 const base=()=>({chainId:chain.id,eventCallbackIndex:0,checkpoint:'0'.repeat(75),block,transaction});
 const observe=(name:string,type:'log'|'trace',abiItem:any,raw:any,keys:string[],path:string[]|null,target=7n)=>{
  const callback:any={type:'contract',chain,name:'Observed:'+name,abiItem,filter:{type,chainId:chain.id}};
  const decoded=decodeEvents(common,chain,[callback],[raw]);assert.equal(decoded.length,1,name);
  const event:any=decoded[0],subject=type==='log'?event.event.args:event.event.result;
  const actualKeys=Object.keys(subject??{}),argsKeys=Object.keys(event.event.args??{});
  assert.deepEqual(actualKeys,keys,name+' actual decoded keys');
  const rowPattern=recordRowPattern(event,table,{id:String(target)},[],primaryKeys);
  const rpcPattern=recordRpcPattern({event,args:{address:zeroAddress,abi:rpcAbi,functionName:'lookup',args:[target]} as any,hints:[]});
  const expected=path?{type:'derived',value:path}:undefined;
  assert.deepEqual(rowPattern,expected?{id:expected}:undefined,name+' row first match');
  assert.deepEqual(rpcPattern?.pattern.args?.[0],expected??{type:'constant',value:target},name+' RPC first match');
  observations.push({name,type,target,actualKeys,argsKeys,argsIsArray:Array.isArray(event.event.args),resultIsArray:Array.isArray(event.event.result),rowPattern,rpcArgPattern:rpcPattern?.pattern.args?.[0],decodedArgs:event.event.args,decodedResult:event.event.result});
 };
 const log=(name:string,inputs:any[],values:bigint[],keys:string[],path:string[]|null,target=7n)=>{
  const abiItem:any={type:'event',name:'Observed',inputs};
  const args=Object.fromEntries(inputs.flatMap((input,i)=>input.indexed?[[input.name,values[i]]]:[]));
  const topics=encodeEventTopics({abi:[abiItem],eventName:'Observed',args} as any);
  const nonIndexed=inputs.filter(input=>!input.indexed),data=encodeAbiParameters(nonIndexed,values.filter((_,i)=>!inputs[i].indexed));
  observe(name,'log',abiItem,{...base(),log:{address:zeroAddress,logIndex:0,topics,data}},keys,path,target);
 };
 const uint=(name:string,indexed=false)=>({name,type:'uint256',indexed});
 log('log-indexed-first',[uint('a'),uint('z',true)],[7n,7n],['z','a'],['args','z']);
 log('log-nonindexed',[uint('z'),uint('a')],[7n,7n],['z','a'],['args','z']);
 const numericNames=['10','2','z','4294967295','01','4294967294','a'];
 const numericKeys=['2','10','4294967294','z','4294967295','01','a'];
 log('log-integer-keys',numericNames.map(name=>uint(name)),numericNames.map(()=>7n),numericKeys,['args','2']);
 log('log-duplicate-overwrite',[uint('dup'),uint('z'),uint('dup',true)],[9n,7n,7n],['dup','z'],['args','dup'],9n);
 log('log-unnamed-array',[uint(''),uint('a')],[7n,7n],['0','1'],null);
 const trace=(name:string,outputs:any[],result:any,keys:string[],path:string[]|null,target=7n)=>{
  const abiItem:any={type:'function',name:'observed',stateMutability:'view',inputs:[{name:'input',type:'uint256'}],outputs};
  const input=encodeFunctionData({abi:[abiItem],functionName:'observed',args:[7n]} as any);
  const output=encodeFunctionResult({abi:[abiItem],functionName:'observed',result} as any);
  observe(name,'trace',abiItem,{...base(),trace:{type:'CALL',from:zeroAddress,to:zeroAddress,input,output,value:0n,gas:0n,gasUsed:0n,traceAddress:[],blockNumber:50,transactionIndex:0}},keys,path,target);
 };
 const components=(names:string[])=>names.map(name=>({name,type:'uint256'}));
 const tuple=(names:string[])=>[{name:'result',type:'tuple',components:components(names)}];
 trace('trace-named-tuple',tuple(['z','a']),[7n,7n],['z','a'],['result','z']);
 trace('trace-integer-tuple',tuple(numericNames),numericNames.map(()=>7n),numericKeys,['result','2']);
 trace('trace-duplicate-tuple',tuple(['z','a','z']),[7n,7n,9n],['z','a'],['result','z'],9n);
 trace('trace-unnamed-tuple-array',tuple(['','a']),[7n,7n],['0','1'],null);
 trace('trace-multiple-output-array',components(['z','a']),[7n,7n],['0','1'],null);
 trace('trace-args-array',[{name:'ok',type:'bool'}],false,[],null);
 return {source_credit:0,scope:'actual_decodeEvents_Object.keys_row_and_RPC_profile_order',override_boundary:'native_only_no_source_override_API',observations};
}
