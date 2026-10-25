// Supplemental calls into the pinned production source with two independent real
// Anvil instances and a real backend. These are not original test-case credit.
import fs from 'node:fs';
import {beforeEach,afterAll,expect,test,vi} from 'vitest';
import {createTestClient,http,zeroAddress} from 'viem';
import {context,setupAnvil,setupCleanup,setupCommon,setupDatabaseServices,setupIsolatedDatabase} from '@/_test/setup.js';
import {getAccountsIndexingBuild,getBlocksIndexingBuild,getChain,testClient,anvil,TEST_POOL_ID} from '@/_test/utils.js';
import {eth_getBlockByNumber} from '@/rpc/actions.js';
import {createRpc} from '@/rpc/index.js';
import {getCachedIntervals,getChildAddresses,getLocalSyncProgress} from '@/runtime/index.js';
import {getHistoricalEventsMultichain,getHistoricalEventsOmnichain,getHistoricalEventsIsolated,refetchHistoricalEvents} from '@/runtime/historical.js';
import {getOmnichainCheckpoint} from '@/runtime/omnichain.js';
import {MAX_CHECKPOINT,encodeCheckpoint} from '@/utils/checkpoint.js';
import * as tables from '@/sync-store/schema.js';
import * as syncStoreModule from '@/sync-store/index.js';
import {createRealtimeRPCObserver} from './source-realtime-rpc-observer.mjs';
const otherID=TEST_POOL_ID+1;
const otherClient=createTestClient({chain:{...anvil,id:2,rpcUrls:{default:{http:[`http://127.0.0.1:8545/${otherID}`]}}},mode:'anvil',transport:http()});
beforeEach(setupCommon);beforeEach(setupAnvil);beforeEach(async()=>{await otherClient.request({method:'anvil_setChainId' as any,params:[2] as any});const id=await otherClient.snapshot();return async()=>{await otherClient.revert({id})}});beforeEach(setupIsolatedDatabase);beforeEach(setupCleanup);
const reports:any[]=[];
let identity:WeakMap<object,number>,serial:number;
function mark(value:any):any{const seen=new Set<object>();function walk(v:any):any{if(v===undefined)return {$undefined:true};if(typeof v==='bigint')return {$bigint:String(v)};if(typeof v==='function')return {$function:v.name};if(v instanceof Error)return {$error:{name:v.name,message:v.message}};if(v instanceof Map)return {$map:[...v].map(([k,x])=>[walk(k),walk(x)])};if(v instanceof Set)return {$set:[...v].map(walk)};if(Array.isArray(v))return v.map(walk);if(v&&typeof v==='object'){let id=identity.get(v);if(id===undefined){id=++serial;identity.set(v,id)};if(seen.has(v))return {$ref:id};seen.add(v);return {$id:id,$object:Object.entries(v).map(([k,x])=>[k,walk(x)])}}return v};return walk(value)}
function progress(x:any){return {start:x.start,end:x.end,current:x.current,finalized:x.finalized}}
function input(p:any){return {chains:p.indexingBuild.chains,eventCallbacks:p.indexingBuild.eventCallbacks,crashRecoveryCheckpoint:p.crashRecoveryCheckpoint,options:{command:p.common.options.command,factoryAddressCountThreshold:p.common.options.factoryAddressCountThreshold,syncEventsQuerySize:p.common.options.syncEventsQuerySize},perChainSync:[...p.perChainSync].map(([chain,x]:any)=>({chain,syncProgress:progress(x.syncProgress),childAddresses:x.childAddresses,cachedIntervals:x.cachedIntervals}))}}
async function physical(database:any){const result:any={};for(const name of ['intervals','factories','factoryAddresses','blocks','logs','transactions','transactionReceipts','traces','rpcRequestResults'])result[name]=await database.syncQB.wrap((db:any)=>db.select().from((tables as any)[name]));return mark(result)}
async function fixture(options:any={}){
 identity=new WeakMap();serial=0;
 const {database,syncStore}=await setupDatabaseServices();
 const chains=[getChain({reorgWindow:0,cacheRpcRequests:options.cache?.[0]??true}),{...getChain({reorgWindow:0,cacheRpcRequests:options.cache?.[1]??true}),id:2,name:'second',rpc:`http://127.0.0.1:8545/${otherID}`}];
 const rpcs=chains.map(chain=>createRpc({chain,common:context.common}));
 const clients=[testClient,otherClient], snapshots:any[]=[];
 const genesis=await Promise.all(rpcs.map(rpc=>eth_getBlockByNumber(rpc,['0x0',false])));
 const timestamp=Math.max(...genesis.map(b=>Number(BigInt(b.timestamp))))+100;
 for(let i=0;i<2;i++){
  await clients[i].setNextBlockTimestamp({timestamp:BigInt(timestamp)});await clients[i].mine({blocks:1});snapshots[i]=await clients[i].snapshot();
  await clients[i].mine({blocks:BigInt((options.tip??3)-1),interval:1});
 }
 const callbacks=chains.map(chain=>getBlocksIndexingBuild({interval:1}).eventCallbacks.flatMap(c=>{const callback={...c,chain,filter:{...c.filter,chainId:chain.id,...(options.end!==undefined?{toBlock:options.end}:{})}};return options.duplicate?[callback,{...callback,name:'Second:block',filter:{...callback.filter,sourceId:'Second'}}]:[callback]}));
 const perChainSync=new Map();
 for(let i=0;i<2;i++){
  const chain=chains[i],rpc=rpcs[i],filters=callbacks[i].map(c=>c.filter);
  const cachedIntervals=await getCachedIntervals({chain,syncStore,filters});
  const syncProgress=await getLocalSyncProgress({common:context.common,chain,rpc,filters,cachedIntervals,finalizedBlock:await eth_getBlockByNumber(rpc,[`0x${(options.finalized?.[i]??options.tip??3).toString(16)}`,true])});
  perChainSync.set(chain,{syncProgress,cachedIntervals,childAddresses:await getChildAddresses({chain,syncStore,filters})});
 }
 return {common:context.common,indexingBuild:{chains,rpcs,eventCallbacks:callbacks},perChainSync,database,syncStore,clients,snapshots};
}
async function capture(name:string,p:any,mode:string,options:any={}){
 const entry:any={name,mode,source_credit:0,real_database:true,independent_anvil_instances:true,args:mark(input(p)),providers:[],steps:[],results:[],actions:[],physical_before:await physical(p.database)};
 reports.push(entry);
 entry.control_events=[];
 const yieldedThrough=new Map<number,number>();const waiters:Array<()=>void>=[];
 async function afterYield(chainId:number,toBlock:number){while((yieldedThrough.get(chainId)??-1)<toBlock)await new Promise<void>(resolve=>waiters.push(resolve))}
 const queryGate=mode==='multichain'&&p.indexingBuild.chains.length===2&&name!=='two-chain-completion-mixed-cache';
 let querySpy:any;
 if(queryGate||options.safeQueries){if(queryGate)entry.controls={eventQueryGate:{chainId:2,afterYieldChainId:1,throughQueryToBlock:true}};const create=syncStoreModule.createSyncStore;querySpy=vi.spyOn(syncStoreModule,'createSyncStore').mockImplementation((args:any)=>{const store=create(args),get=store.getEventData.bind(store);if(options.safeQueries){const safe=store.getSafeCrashRecoveryBlock.bind(store);store.getSafeCrashRecoveryBlock=async(params:any)=>{const result=await safe(params);options.safeQueries.push({args:mark(params),result:mark(result)});return result}};store.getEventData=async(params:any)=>{const result=await get(params);if(queryGate&&params.chainId===2){entry.control_events.push({kind:'query_response_held',chainId:2,toBlock:params.toBlock});await afterYield(1,params.toBlock);entry.control_events.push({kind:'query_response_released',chainId:2,toBlock:params.toBlock})}return result};return store})}
 const originalRequests=p.indexingBuild.rpcs.map((rpc:any)=>rpc.request);
 if(name==='two-chain-completion-mixed-cache'){
  const throughBlock=Number(BigInt(p.perChainSync.get(p.indexingBuild.chains[1]).syncProgress.finalized.number));entry.controls={firstBlockResponseGate:{chainId:1,afterYieldChainId:2,throughBlock}};let first=true;
  const rpc=p.indexingBuild.rpcs[0],request=rpc.request;rpc.request=async function(...args:any[]){const result=await Reflect.apply(request,this,args);if(first&&args[0].method==='eth_getBlockByNumber'){first=false;entry.control_events.push({kind:'block_response_held',chainId:1,throughBlock});await afterYield(2,throughBlock);entry.control_events.push({kind:'block_response_released',chainId:1,throughBlock})}return result};
 }
 let releaseHeld:(()=>void)|undefined;
 if(options.latestFirstError){entry.controls={...entry.controls,latestFirstError:{heldChainId:1,errorChainId:2,message:'M37 injected latest failure'}};const held=new Promise<void>(resolve=>releaseHeld=resolve);p.indexingBuild.rpcs.forEach((rpc:any,index:number)=>{const request=rpc.request;rpc.request=async function(...args:any[]){const latest=args[0].method==='eth_getBlockByNumber'&&args[0].params[0]==='latest';if(latest){entry.control_events.push({kind:'latest_begin',chainId:index+1});if(index===1){entry.control_events.push({kind:'latest_error',chainId:2});throw Error('M37 injected latest failure')}}const result=await Reflect.apply(request,this,args);if(latest){await held;entry.control_events.push({kind:'latest_released',chainId:1})}return result}})}
 if(options.memoryFirstError){entry.controls={...entry.controls,memoryFirstError:{chainId:1,blockNumber:0,message:'M37 injected trace failure'}};const held=new Promise<void>(resolve=>releaseHeld=resolve);const rpc=p.indexingBuild.rpcs[0],request=rpc.request;rpc.request=async function(...args:any[]){const zero=args[0].params[0]==='0x0',block=zero&&args[0].method==='eth_getBlockByNumber',trace=zero&&args[0].method==='debug_traceBlockByNumber';if(block)entry.control_events.push({kind:'block_begin',chainId:1,blockNumber:0});if(trace){entry.control_events.push({kind:'trace_begin',chainId:1,blockNumber:0});entry.control_events.push({kind:'trace_error',chainId:1,blockNumber:0});throw Error('M37 injected trace failure')}const result=await Reflect.apply(request,this,args);if(block){await held;entry.control_events.push({kind:'block_released',chainId:1,blockNumber:0})}return result}}
 let inflight=0;let onDrained:(()=>void)|undefined;
 p.indexingBuild.rpcs.forEach((rpc:any,index:number)=>{const observe=createRealtimeRPCObserver((request:any[])=>{inflight++;const r:any={chainId:p.indexingBuild.chains[index].id,request:mark(request)};entry.providers.push(r);return r},(r:any,value:any,error:any)=>{if(error)r.error=mark(error);else r.response=mark(value);if(--inflight===0)onDrained?.()});observe(rpc)});
 let now=1000000;entry.clocks=[];const originalNow=Date.now;Date.now=function observedCoordinatorClock(){if(new Error().stack?.split('\n')[2]?.includes('/runtime/historical.ts:'))entry.clocks.push(now);return now};entry.clock_initial_ms=now;
 const iterator=mode==='omnichain'?getHistoricalEventsOmnichain(p):mode==='isolated'?getHistoricalEventsIsolated({...p,chain:p.indexingBuild.chains[options.chainIndex??0],...p.perChainSync.get(p.indexingBuild.chains[options.chainIndex??0])}):getHistoricalEventsMultichain(p);
 const output:any[]=[];let failure:any;
 try{
  for(let count=0;count<20;count++){
   entry.steps.push({operation:'next'});const step=await iterator.next();entry.results.push(mark(step));if(step.done)break;output.push(step.value);
   if(step.value.chainId!==undefined){yieldedThrough.set(step.value.chainId,step.value.blockRange[1]);entry.control_events.push({kind:'consumer_yield',chainId:step.value.chainId,throughBlock:step.value.blockRange[1]});for(const release of waiters.splice(0))release()}
   if(count===0&&options.catchup){now+=31000;entry.actions.push({kind:'advance_clock',to:now})}
   if(count===0&&options.backward){await p.clients[0].revert({id:p.snapshots[0]});entry.actions.push({kind:'revert',chainId:1,toBlock:1});now+=31000;entry.actions.push({kind:'advance_clock',to:now})}
   if(options.stop&&count===0){entry.steps.push({operation:'return'});entry.return_result=mark(await iterator.return(undefined as any));break}
   if(count===19)throw Error('Unexpected unbounded catchup');
  }
 }catch(error){failure=error;entry.error=mark(error);if(options.latestFirstError||options.memoryFirstError)entry.control_events.push({kind:'iterator_error'})}finally{if(releaseHeld){entry.control_events.push({kind:'cleanup_release'});releaseHeld()}if(inflight)await new Promise<void>(resolve=>onDrained=resolve);Date.now=originalNow;p.indexingBuild.rpcs.forEach((rpc:any,index:number)=>{rpc.request=originalRequests[index]});querySpy?.mockRestore();entry.after=mark(input(p));entry.physical_after=await physical(p.database)}
 entry.summary=output.map(x=>x.type==='pending'?{type:x.type,pending:x.result.map((e:any)=>({chainId:e.chain.id,checkpoint:e.checkpoint,number:String(e.event.block.number)}))}:x.type==='events'?{type:x.type,groups:x.result.map((g:any)=>({chainId:g.chainId,checkpoint:g.checkpoint,numbers:g.events.map((e:any)=>String(e.event.block.number))}))}:{chainId:x.chainId,checkpoint:x.checkpoint,numbers:x.events.map((e:any)=>String(e.event.block.number))});
 return {entry,output,error:failure};
}
test('two independent chains: completion order and mixed cache',async()=>{
 const p=await fixture({cache:[true,false]});
 const {output,error,entry}=await capture('two-chain-completion-mixed-cache',p,'multichain');expect(error).toBeUndefined();expect(output[0].chainId).toBe(2);expect(output.flatMap(x=>x.events).filter(x=>x.chain.id===1)).toHaveLength(4);expect(output.flatMap(x=>x.events).filter(x=>x.chain.id===2)).toHaveLength(4);
});
test('two chains: omnichain pending boundary',async()=>{
 const p=await fixture({finalized:[1,3]});const {output,error}=await capture('two-chain-pending',p,'omnichain');expect(error).toBeUndefined();const pending=output.at(-1);expect(pending.type).toBe('pending');expect(pending.result.length).toBeGreaterThan(0);expect(pending.result.every((e:any)=>e.chain.id===2)).toBe(true);
});
test('two chains: omnichain catchup releases pending prefix',async()=>{
 const p=await fixture({finalized:[1,3]});const {output,error}=await capture('two-chain-pending-catchup',p,'omnichain',{catchup:true});expect(error).toBeUndefined();const emitted=output.filter(x=>x.type==='events').flatMap(x=>x.result).flatMap(x=>x.events);expect(emitted.some((e:any)=>e.chain.id===2&&e.event.block.number===2n)).toBe(true);expect(new Set(emitted.map((e:any)=>e.checkpoint)).size).toBe(emitted.length);expect(output.at(-1).type).toBe('pending');
});
test('two chains: multichain catchup',async()=>{const p=await fixture({finalized:[1,1]});const {output,error}=await capture('two-chain-multichain-catchup',p,'multichain',{catchup:true});expect(error).toBeUndefined();for(const id of [1,2])expect(output.flatMap(x=>x.events).filter(x=>x.chain.id===id)).toHaveLength(4)});
for(const mode of ['multichain','omnichain','isolated'])test(`real finalized rollback: ${mode}`,async()=>{
 const p=await fixture({tip:2});p.indexingBuild.chains=p.indexingBuild.chains.slice(0,1);p.indexingBuild.rpcs=p.indexingBuild.rpcs.slice(0,1);p.indexingBuild.eventCallbacks=p.indexingBuild.eventCallbacks.slice(0,1);p.perChainSync=new Map([...p.perChainSync].slice(0,1));
 const {output,error}=await capture('real-finalized-backward-'+mode,p,mode,{backward:true});if(mode==='omnichain')expect(error?.message).toContain('cannot move backwards');else expect(error).toBeUndefined();expect(output.length).toBeGreaterThan(0);
});
test('two chains: refetch preserves original cross-chain positions',async()=>{
 const p=await fixture();const {output,error,entry}=await capture('two-chain-refetch',p,'multichain');expect(error).toBeUndefined();const byChain=[1,2].map(id=>output.flatMap(x=>x.events).filter(x=>x.chain.id===id));const original=byChain[0].flatMap((e,i)=>[e,byChain[1][i]]);const result=await refetchHistoricalEvents({...p,events:original});entry.refetch={input:mark(original),result:mark(result)};expect(result.map(e=>e.chain.id)).toEqual(original.map(e=>e.chain.id));expect(result.map(e=>e.checkpoint)).toEqual(original.map(e=>e.checkpoint));expect(result).toHaveLength(8);
});
test('isolated in-memory and consumer early return',async()=>{
 const p=await fixture({cache:[false,true]});const {output,error,entry}=await capture('isolated-memory-stop',p,'isolated',{stop:true});expect(error).toBeUndefined();expect(output).toHaveLength(1);expect(output[0].chainId).toBe(1);
 const callback=getAccountsIndexingBuild({address:zeroAddress}).eventCallbacks[2];p.indexingBuild.eventCallbacks[0]=[{...callback,chain:p.indexingBuild.chains[0]}];const chain=p.indexingBuild.chains[0],filters=p.indexingBuild.eventCallbacks[0].map((x:any)=>x.filter),state=p.perChainSync.get(chain);state.cachedIntervals=await getCachedIntervals({chain,syncStore:p.syncStore,filters});state.childAddresses=await getChildAddresses({chain,syncStore:p.syncStore,filters});
 const failed=await capture('memory-block-trace-first-error',p,'isolated',{memoryFirstError:true});reports.pop();entry.memory_error_call=failed.entry;expect(failed.error?.message).toBe('M37 injected trace failure');expect(failed.entry.control_events.findIndex((x:any)=>x.kind==='iterator_error')).toBeLessThan(failed.entry.control_events.findIndex((x:any)=>x.kind==='cleanup_release'));expect(failed.entry.providers.every((x:any)=>x.response!==undefined||x.error!==undefined)).toBe(true);
});
test('refetch all filters includes extras and assigns positionally',async()=>{
 const p=await fixture({duplicate:true});const {output,error,entry}=await capture('refetch-all-filters-extras',p,'multichain');expect(error).toBeUndefined();const byChain=[1,2].map(id=>output.flatMap(x=>x.events).filter(x=>x.chain.id===id));const original=[byChain[0][1],byChain[1][1],byChain[0][3],byChain[1][3]];
 const queries:any[]=[];const get=p.syncStore.getEventData.bind(p.syncStore);p.syncStore.getEventData=async(args:any)=>{queries.push(mark(args));return get(args)};
 const result=await refetchHistoricalEvents({...p,events:original});entry.refetch={input:mark(original),result:mark(result),queries};
 expect(result.map(e=>e.chain.id)).toEqual([1,2,1,2]);expect(result.map(e=>e.event.block.number)).toEqual([0n,0n,0n,0n]);expect(result.map(e=>e.checkpoint)).not.toEqual(original.map(e=>e.checkpoint));
});
test('refetch no matching filters retains sparse chain slots',async()=>{
 const p=await fixture();const {output,error,entry}=await capture('refetch-sparse-slots',p,'multichain');expect(error).toBeUndefined();const original=output.flatMap(x=>x.events);const eventCallbacks=p.indexingBuild.eventCallbacks.map((xs:any[])=>xs.map(c=>({...c,filter:{...c.filter,fromBlock:100,toBlock:101}})));
 const result=await refetchHistoricalEvents({...p,indexingBuild:{...p.indexingBuild,eventCallbacks},events:original});entry.refetch={input:mark(original),eventCallbacks:mark(eventCallbacks),result:mark(result),presentIndices:Object.keys(result)};expect(result).toHaveLength(original.length);expect(Object.keys(result)).toHaveLength(0);
});
test('aggregate checkpoints exclude completed chains and bound end',async()=>{
 const p=await fixture();const {entry,error}=await capture('aggregate-checkpoint-tags',p,'multichain');expect(error).toBeUndefined();const state=[...p.perChainSync.values()];const cases:any[]=[];function sample(name:string){const outputs:any={};for(const tag of ['start','end','current','finalized'])outputs[tag]=getOmnichainCheckpoint({perChainSync:p.perChainSync,tag:tag as any});cases.push({name,input:mark(input(p)),outputs:mark(outputs)});return outputs}
 const first=sample('unbounded');expect(first.end).toBeUndefined();
 state[0].syncProgress.end=state[0].syncProgress.finalized;state[0].syncProgress.current=state[0].syncProgress.finalized;state[1].syncProgress.current=state[1].syncProgress.start;
 const partial=sample('one-complete');expect(partial.current).toBe(state[1].syncProgress.getCheckpoint({tag:'current'}));expect(partial.end).toBeUndefined();
 state[1].syncProgress.end=state[1].syncProgress.finalized;state[1].syncProgress.current=state[1].syncProgress.finalized;
 const all=sample('all-complete');const finals=state.map(x=>x.syncProgress.getCheckpoint({tag:'finalized'}));expect(all.current).toBe(finals.sort().at(-1));expect(all.end).toBe(finals.sort().at(-1));entry.aggregate=cases;
});
for(const cache of [true,false])test(`cross-chain crash safe-block with cache ${cache}`,async()=>{
 const p=await fixture();const prime=await capture('cross-chain-crash-prime',p,'multichain');expect(prime.error).toBeUndefined();reports.pop();
 p.indexingBuild.chains[0].cacheRpcRequests=cache;
 const block=await eth_getBlockByNumber(p.indexingBuild.rpcs[1],['0x1',false]);
 p.crashRecoveryCheckpoint=[{chainId:1,checkpoint:encodeCheckpoint({...MAX_CHECKPOINT,blockTimestamp:BigInt(block.timestamp),blockNumber:BigInt(block.number),chainId:2n})}];
 const safeQueries:any[]=[];
 const result=await capture(cache?'cross-chain-crash-safe-cache':'cross-chain-crash-uncached-start',p,'multichain',{safeQueries});
 result.entry.setup_calls=[prime.entry];result.entry.safeQueries=safeQueries;expect(result.error).toBeUndefined();expect(safeQueries).toHaveLength(cache?1:0);
 const firstChain=result.output.flatMap((x:any)=>x.events).filter((x:any)=>x.chain.id===1);expect(firstChain.map((e:any)=>e.event.block.number)).toEqual([2n,3n]);
});
test('two chains: bounded end and no matching event chain',async()=>{
 const p=await fixture({end:1});
 // Preserve actual finalized source blocks but make chain2's input callbacks
 // impossible in the bounded range. Empty batches must not stall other chains.
 for(const callback of p.indexingBuild.eventCallbacks[1]){callback.filter.interval=100;callback.filter.offset=99}
 const {output,error,entry}=await capture('bounded-end-empty-chain',p,'multichain');expect(error).toBeUndefined();
 const events=output.flatMap(x=>x.events);expect(events.filter(e=>e.chain.id===1).map(e=>e.event.block.number)).toEqual([0n,1n]);expect(events.filter(e=>e.chain.id===2)).toHaveLength(0);entry.boundary={end:1,emptyChainId:2};
});
test('latest refresh admits both chains then reverse completion',async()=>{
 const p=await fixture({finalized:[1,1]});const baseRequests=p.indexingBuild.rpcs.map((rpc:any)=>rpc.request);const timeline:any[]=[];let releaseSecond!:()=>void;const secondFinished=new Promise<void>(resolve=>releaseSecond=resolve);
 p.indexingBuild.rpcs.forEach((rpc:any,index:number)=>{const request=rpc.request.bind(rpc);rpc.request=async(...args:any[])=>{const latest=args[0].method==='eth_getBlockByNumber'&&args[0].params[0]==='latest';if(latest){timeline.push({kind:'begin',chainId:index+1});};const value=await request(...args);if(latest){if(index===0)await secondFinished;timeline.push({kind:'finish',chainId:index+1});if(index===1)releaseSecond()};return value}});
 const {error,entry}=await capture('latest-concurrent-reverse-completion',p,'multichain',{catchup:true});entry.latestAdmission={timeline,responseGate:{chainId:1,afterResponseChainId:2}};expect(error).toBeUndefined();expect(timeline).toEqual([{kind:'begin',chainId:1},{kind:'begin',chainId:2},{kind:'finish',chainId:2},{kind:'finish',chainId:1}]);
 p.indexingBuild.rpcs.forEach((rpc:any,index:number)=>rpc.request=baseRequests[index]);
 const failed=await capture('latest-first-error',p,'multichain',{catchup:true,latestFirstError:true});reports.pop();entry.error_call=failed.entry;expect(failed.error?.message).toBe('M37 injected latest failure');expect(failed.entry.control_events.findIndex((x:any)=>x.kind==='iterator_error')).toBeLessThan(failed.entry.control_events.findIndex((x:any)=>x.kind==='cleanup_release'));expect(failed.entry.providers.every((x:any)=>x.response!==undefined||x.error!==undefined)).toBe(true);
});
afterAll(()=>{const target=process.env.PONDER_RUNTIME_MULTICHAIN_PROBES;if(!target)throw Error('Explicit probe output required');fs.writeFileSync(target,JSON.stringify({source_credit:0,backend:process.env.DATABASE_URL?'postgres':'pglite',cases:reports},null,2)+'\n')});
