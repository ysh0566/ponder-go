// Zero-credit controls: real Anvil/RPC and actual cached client/service. Only
// cache persistence completion is gated at its dependency boundary.
import fs from 'node:fs';
import {beforeEach,afterAll,test,expect} from 'vitest';
import {zeroAddress} from 'viem';
import {BOB} from '@/_test/constants.js';
import {context,setupCommon,setupAnvil,setupCleanup} from '@/_test/setup.js';
import {getChain,getErc20IndexingBuild} from '@/_test/utils.js';
import {createRpc} from '@/rpc/index.js';
import {createCachedViemClient} from '@/indexing/client.js';
import {createIndexing,createColumnAccessPattern,getEventCount} from '@/indexing/index.js';
import {InvalidEventAccessError,IndexingFunctionError,BaseError} from '@/internal/errors.js';
const output=process.env.PONDER_INDEXING_CACHE_CAPTURE;if(!output)throw Error('Explicit cache probe capture required');
const cases:any[]=[];
const encode=(v:any):any=>v===undefined?{$undefined:true}:typeof v==='bigint'?{$bigint:String(v)}:Array.isArray(v)?v.map(encode):v&&typeof v==='object'?Object.fromEntries(Object.entries(v).map(([k,value])=>[k,encode(value)])):v;
beforeEach(setupCommon);beforeEach(setupAnvil);beforeEach(setupCleanup);
for(const lateFailure of [false,true])test('actual cached getBalance returns before '+(lateFailure?'failed':'successful')+' insert',async()=>{
 const timeline:string[]=[],providers:any[]=[],cacheCalls:any[]=[];
 const chain=getChain(),rpc=createRpc({chain,common:context.common});
 const request=rpc.request.bind(rpc);rpc.request=async(...args:any[])=>{timeline.push('rpc_begin');const record:any={request:encode(args[0])};providers.push(record);try{const result=await(request as any)(...args);record.response=encode(result);timeline.push('rpc_return');return result}catch(error:any){record.error={name:error.name,message:error.message};throw error}};
 let release!:()=>void;const gate=new Promise<void>(r=>release=r);let settled=false;
 const syncStore:any={getRpcRequestResults:async(args:any)=>{timeline.push('cache_lookup');cacheCalls.push({operation:'get',args:encode(args)});return []},insertRpcRequestResults:async(args:any)=>{timeline.push('insert_begin');cacheCalls.push({operation:'insert',args:encode(args)});await gate;settled=true;timeline.push(lateFailure?'insert_reject':'insert_resolve');if(lateFailure)throw Error('injected late cache insert failure')}};
 const client=createCachedViemClient({common:context.common,indexingBuild:{chains:[chain],rpcs:[rpc]},syncStore,eventCount:{'Erc20:setup':0}});
 client.event={type:'setup',chain,setupCallback:{name:'Erc20:setup'},block:0n} as any;
 let timer:any;try{
  const result=await Promise.race([client.getClient(chain).getBalance({address:BOB}),new Promise<never>((_,reject)=>{timer=setTimeout(()=>reject(Error('getBalance waited for blocked insert')),2000)})]);
  timeline.push('getBalance_return');expect(settled).toBe(false);expect(timeline).toContain('insert_begin');expect(typeof result).toBe('bigint');
  timeline.push('release_insert');release();await gate;await new Promise(r=>setTimeout(r,0));expect(settled).toBe(true);
  cases.push({name:lateFailure?'delayed-cache-insert-failure':'delayed-cache-insert-success',source_credit:0,real_rpc:true,input:{chain:{id:chain.id,name:chain.name,cacheRpcRequests:chain.cacheRpcRequests},event:encode({type:'setup',block:0n}),address:BOB,lateFailure},timeline,providers,cacheCalls,result:encode(result),settled});
 }finally{clearTimeout(timer);release();await new Promise(r=>setTimeout(r,0))}
});
for(const branded of [false,true])test('actual indexing '+(branded?'BaseError':'name-spoofed user error')+' classification',async()=>{
 const common=context.common,chain=getChain(),rpc=createRpc({chain,common});
 const {eventCallbacks,setupCallbacks,indexingFunctions,contracts}=getErc20IndexingBuild({address:zeroAddress});
 const eventCount=getEventCount(indexingFunctions),syncStore:any={getRpcRequestResults:async()=>[],insertRpcRequestResults:async()=>{}};
 const client=createCachedViemClient({common,indexingBuild:{chains:[chain],rpcs:[rpc]},syncStore,eventCount});
 const input:any=branded?new InvalidEventAccessError('transaction.gas'):Object.assign(new Error('user spoof'),{name:'InvalidEventAccessError'});
 setupCallbacks[0]!.fn=async()=>{throw input};
 const service=createIndexing({common,indexingBuild:{chains:[chain],eventCallbacks:[eventCallbacks],setupCallbacks:[setupCallbacks],indexingFunctions,contracts:[contracts]},indexingStore:{db:{}} as any,indexingCache:{} as any,client,indexingErrorHandler:{getRetryableError:()=>undefined,clearRetryableError:()=>{},setRetryableError:()=>{}} as any,columnAccessPattern:createColumnAccessPattern({indexingBuild:{indexingFunctions}}),eventCount});
 let caught:any;try{await service.processSetupEvents()}catch(error){caught=error}
 expect(caught).toBeDefined();expect(caught instanceof BaseError).toBe(true);expect(caught===input).toBe(branded);expect(caught instanceof IndexingFunctionError).toBe(!branded);
 cases.push({name:branded?'actual-base-error-identity':'user-error-name-is-not-brand',source_credit:0,input:{name:input.name,message:input.message,isBaseError:input instanceof BaseError},result:{name:caught.name,message:caught.message,isBaseError:caught instanceof BaseError,isIndexingFunctionError:caught instanceof IndexingFunctionError,sameIdentity:caught===input},hasError:common.metrics.hasError});
});
afterAll(()=>fs.writeFileSync(output,JSON.stringify({source_credit:0,cases},null,2)+'\n'));
