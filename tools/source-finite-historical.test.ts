// Zero-credit calls to the unchanged outer coordinator. Only its consumer
// boundary is capped; extraction, decoding, handlers, prefetch and SQL are real.
import fs from 'node:fs';
import v8 from 'node:v8';
import {beforeEach,afterAll,expect,test,vi} from 'vitest';
import {createTestClient,http,zeroAddress} from 'viem';
import {context,setupAnvil,setupCleanup,setupCommon,setupDatabaseServices,setupIsolatedDatabase} from '@/_test/setup.js';
import {getBlocksIndexingBuild,getChain,testClient,anvil,TEST_POOL_ID} from '@/_test/utils.js';
import {onchainTable} from '@/drizzle/onchain.js';
import {getPonderCheckpointTable,getPonderMetaTable} from '@/database/index.js';
import {eth_getBlockByNumber} from '@/rpc/actions.js';
import {createRpc} from '@/rpc/index.js';
import {runMultichain} from '@/runtime/multichain.js';
import * as historical from '@/runtime/historical.js';
import * as indexingModule from '@/indexing/index.js';
import * as cacheModule from '@/indexing-store/cache.js';
import {recordProfilePattern as recordRowPattern,recoverProfilePattern as recoverRowPattern} from '@/indexing-store/profile.js';
import {getCacheKey as getRowCacheKey} from '@/indexing-store/utils.js';
import * as clientModule from '@/indexing/client.js';
import * as syncModule from '@/sync-store/index.js';
import * as actions from '@/database/actions.js';
import * as tables from '@/sync-store/schema.js';
import {InvalidEventAccessError,NonRetryableUserError} from '@/internal/errors.js';
import {requiredBlockFilterInclude} from '@/runtime/filter.js';
import {observeDecodedFieldOrder} from './source-finite-field-order.js';
import {encodeFiniteEvidence as enc,createFiniteIndexingObservation,observeCaptureFailureSafety} from './source-finite-capture.js';
const otherID=TEST_POOL_ID+1;
const otherClient=createTestClient({chain:{...anvil,id:2,rpcUrls:{default:{http:[`http://127.0.0.1:8545/${otherID}`]}}},mode:'anvil',transport:http()});
beforeEach(setupCommon);beforeEach(setupAnvil);
beforeEach(async()=>{await otherClient.request({method:'anvil_setChainId' as any,params:[2] as any});const id=await otherClient.snapshot();return async()=>{await otherClient.revert({id})}});
beforeEach(setupIsolatedDatabase);beforeEach(setupCleanup);
const account=onchainTable('account',p=>({id:p.text().primaryKey(),balance:p.bigint().notNull()}));
const schema={account},reports:any[]=[],observations:ReturnType<typeof createFiniteIndexingObservation>[]=[];
const profileRuntime='0x600160005260206000f3' as const;
const profileInit=('0x600a600c600039600a6000f3'+profileRuntime.slice(2)) as `0x${string}`;
const profileAbi=[{type:'function',name:'value',stateMutability:'view',inputs:[],outputs:[{type:'uint256'}]}] as const;

class HarnessStop extends NonRetryableUserError {readonly harnessStop=true;constructor(){super('M43 real finite historical consumer stopped');Object.setPrototypeOf(this,HarnessStop.prototype)}}
for(const mode of ['fresh-multigroup','resume','empty-resume','missing-field-refetch','sticky-refetch','nonretryable','learned-prefetch','learned-prefetch-default'])test(`actual runMultichain finite ${mode}`,async()=>{
 const {common}=context;common.options.syncEventsQuerySize=0;
 const learning=mode==='learned-prefetch'||mode==='learned-prefetch-default',pressure=mode==='learned-prefetch';
 const originalCacheBudget=common.options.indexingCacheMaxBytes,heapSizeLimit=v8.getHeapStatistics().heap_size_limit;
 const refetchMode=mode==='missing-field-refetch'||mode==='sticky-refetch';
 if(pressure)common.options.indexingCacheMaxBytes=1;
 const backend=process.env.DATABASE_URL?'postgres':'pglite';
 const record:any={name:mode,source_credit:0,backend,production_function:'runMultichain',timeline:[],rpc:[],runs:[],identities:[],refetch:[],retryWarnings:[]};reports.push(record);
 if(learning){expect(process.env.PONDER_CACHE_BYTES).toBeUndefined();expect(originalCacheBudget).toBe(Math.ceil(heapSizeLimit/1024/1024/5/64)*64*1024*1024);record.cacheBudget={source:'buildOptions_v8_heap_default',environmentOverrideAbsent:true,heapSizeLimit,originalBytes:originalCacheBudget,configuredBytes:common.options.indexingCacheMaxBytes,allRuntimeOptionsDefault:false,syncEventsQuerySize:0};}
 const chains=[getChain({reorgWindow:0,cacheRpcRequests:true}),{...getChain({reorgWindow:0,cacheRpcRequests:true}),id:2,name:'second',rpc:`http://127.0.0.1:8545/${otherID}`}];
 const rpcs=chains.map(chain=>createRpc({chain,common})),clients=[testClient,otherClient];
 const profileAddresses=new Map<number,`0x${string}`>();
 if(mode==='fresh-multigroup'){record.decodedFieldOrder=enc(observeDecodedFieldOrder(common,chains[0],account));record.failureCapture=observeCaptureFailureSafety(common);}
 const last=learning?120:18;
 for(const [index,client] of clients.entries()){
  if(learning){
   const [from]=await client.request({method:'eth_accounts'} as any) as `0x${string}`[];
   const transaction=await client.request({method:'eth_sendTransaction',params:[{from,data:profileInit,gas:'0x200000'}]} as any);
   await client.mine({blocks:1});
   const receipt:any=await client.request({method:'eth_getTransactionReceipt',params:[transaction]} as any);
   expect(receipt.status).toBe('0x1');expect(receipt.blockNumber).toBe('0x1');expect(receipt.contractAddress).toMatch(/^0x[0-9a-f]{40}$/);
   const address=receipt.contractAddress as `0x${string}`;
   const code=await client.request({method:'eth_getCode',params:[address,'0x1']} as any);
   const call=await client.request({method:'eth_call',params:[{to:address,data:'0x3fa4f245'},'0x1']} as any);
   expect(code).toBe(profileRuntime);expect(call).toBe('0x'+'0'.repeat(63)+'1');profileAddresses.set(chains[index].id,address);
   (record.deployments??=[]).push({chainId:chains[index].id,transaction,receipt:enc(receipt),code,call,height:'0x1'});
   await client.mine({blocks:last-1});
  }else await client.mine({blocks:last});
 }
 const finalizedBlocks=await Promise.all(rpcs.map(rpc=>eth_getBlockByNumber(rpc,[`0x${last.toString(16)}`,true])));
 const {database}=await setupDatabaseServices({schemaBuild:{schema},indexingBuild:{chains,finalizedBlocks}});
 const checkpoints=getPonderCheckpointTable('public'),meta=getPonderMetaTable('public');
 const snapshot=async()=>enc({business:await database.userQB.wrap((db:any)=>db.select().from(account)),checkpoints:await database.userQB.wrap((db:any)=>db.select().from(checkpoints)),meta:await database.userQB.wrap((db:any)=>db.select().from(meta)),rpcCache:await database.syncQB.wrap((db:any)=>db.select().from(tables.rpcRequestResults))});
 record.before=await snapshot();
 let txDepth=0,inflight=0,drain:(()=>void)|undefined,run:any,firstArgs:any,firstContext:any,firstDB:any,observation:ReturnType<typeof createFiniteIndexingObservation>;
 const pending:Promise<any>[]=[],firstRowErrors=new Map<string,unknown>();
 const child=common.logger.child;
 common.logger.child=function(bindings:any){const logger=Reflect.apply(child,this,[bindings]);if(bindings.action==='prefetch_database_rows'){const warn=logger.warn,key=record.runs.length+':'+(run.groups.length-1),groupIndex=run.groups.length-1,runIndex=record.runs.length;logger.warn=function(entry:any){if(entry.retry_count===0&&!firstRowErrors.has(key))firstRowErrors.set(key,entry.error);record.retryWarnings.push({...enc(entry),run:runIndex,groupIndex,txDepth,observedAt:performance.now()});return Reflect.apply(warn,this,[entry])}}return logger} as any;
 const observePrefetch=(entry:any,promise:any)=>{if(!promise||typeof promise.then!=='function')throw Error('Actual prefetch must return a Promise');promise.then(()=>{entry.settled='fulfilled'},(error:any)=>{entry.settled='rejected';entry.error=enc(error);entry.error_is_first_row_error=error===firstRowErrors.get(entry.run+':'+entry.groupIndex)});return promise};
 const identity=(args:any)=>{if(!firstArgs){firstArgs=args;firstContext=args.context;firstDB=args.context.db}record.identities.push({args:args===firstArgs,context:args.context===firstContext,db:args.context.db===firstDB,chainId:args.context.chain.id,run:record.runs.length})};
 for(let i=0;i<rpcs.length;i++){const rpc=rpcs[i],request=rpc.request;rpc.request=async function(...args:any[]){const item:any={chainId:chains[i].id,request:enc(args),txDepth,run:record.runs.length};record.rpc.push(item);inflight++;try{const value=await Reflect.apply(request,this,args);item.response=enc(value);return value}catch(e){item.error=enc(e);throw e}finally{if(--inflight===0)drain?.()}}}
 const createSync=syncModule.createSyncStore;vi.spyOn(syncModule,'createSyncStore').mockImplementation((args:any)=>{const store=createSync(args);const insert=store.insertRpcRequestResults;store.insertRpcRequestResults=(...xs:any[])=>{const p=Reflect.apply(insert,store,xs);pending.push(p);return p};return store});
 const createIndexing=indexingModule.createIndexing;vi.spyOn(indexingModule,'createIndexing').mockImplementation((args:any)=>{observation.capture(args);return createIndexing(args)});
 const createCache=cacheModule.createIndexingCache;vi.spyOn(cacheModule,'createIndexingCache').mockImplementation((args:any)=>{const cache=createCache(args);for(const name of ['prefetch','flush','invalidate','clear'] as const){const fn=cache[name];cache[name]=((...xs:any[])=>{const entry:any={kind:'row-'+name,txDepth,run:record.runs.length,groupIndex:run.groups.length-1,events:xs[0]?.events?.length};record.timeline.push(entry);const result=Reflect.apply(fn,cache,xs);return name==='prefetch'?observePrefetch(entry,result):result}) as any}return cache});
 const createClient=clientModule.createCachedViemClient;vi.spyOn(clientModule,'createCachedViemClient').mockImplementation((args:any)=>{const client=createClient(args);for(const name of ['prefetch','clear'] as const){const fn=client[name];client[name]=((...xs:any[])=>{const entry:any={kind:'rpc-'+name,txDepth,run:record.runs.length,groupIndex:run.groups.length-1,events:xs[0]?.events?.length};record.timeline.push(entry);const result=Reflect.apply(fn,client,xs);return name==='prefetch'?observePrefetch(entry,result):result}) as any}return client});
 const refetch=historical.refetchHistoricalEvents;vi.spyOn(historical,'refetchHistoricalEvents').mockImplementation(async(args:any)=>{const entry:any={txDepth,before:enc(args.events),filters:enc(args.indexingBuild.eventCallbacks.map((cs:any[])=>cs.map(c=>c.filter.include)))};record.refetch.push(entry);const includes=callbacks.map(cs=>cs[0].filter.include);entry.includeIdentity={shared:includes[0]===includes[1],replaced:includes.map((value,i)=>value!==initialIncludes[i])};expect(entry.includeIdentity.shared).toBe(true);expect(entry.includeIdentity.replaced).toEqual([true,true]);const result=await refetch(args);entry.after=enc(result);return result});
 for(const name of ['createViews','createIndexes','createTriggers','createLiveQueryTriggers'] as const){const fn=actions[name];vi.spyOn(actions,name).mockImplementation((...xs:any[])=>{record.timeline.push({kind:'forbidden-cutover',name});return Reflect.apply(fn,actions,xs)})}
 const transaction=database.userQB.transaction;
 database.userQB.transaction=(async(...args:any[])=>{const i=typeof args[0]==='function'?0:1,callback=args[i],entry:any={callbacks:0,groupIndex:run.groups.length-1};run.transactions.push(entry);args[i]=async(tx:any)=>{txDepth++;entry.callbacks++;record.timeline.push({kind:'transaction-begin',txDepth,run:record.runs.length});try{return await callback(tx)}finally{txDepth--;record.timeline.push({kind:'transaction-callback-end',txDepth,run:record.runs.length})}};try{const result=await Reflect.apply(transaction,database.userQB,args);entry.returned=true;record.timeline.push({kind:'transaction-return',txDepth,run:record.runs.length});return result}catch(e){entry.error=enc(e);throw e}}) as any;
 const setup=chains.map(chain=>({name:`Setup${chain.id}:setup`,chain,block:0,fn:async(args:any)=>{identity(args);run.setup.push(chain.id);await args.context.db.insert(account).values({id:`shared${chain.id}`,balance:0n})}}));
 const callbacks=chains.map(chain=>getBlocksIndexingBuild({interval:1}).eventCallbacks.map(c=>({...c,chain,filter:{...c.filter,chainId:chain.id,fromBlock:learning?1:0,toBlock:last,...(refetchMode?{include:[...requiredBlockFilterInclude]}:{})},fn:async(args:any)=>{
  identity(args);run.attempts.push({chainId:chain.id,block:String(args.event.block.number)});
  if(mode==='nonretryable')throw new NonRetryableUserError('M43 nonretryable handler');
  if(refetchMode){if(mode==='sticky-refetch'){try{void args.event.block.gasUsed}catch(e){run.caught.push(enc(e));throw new Error('secondary callback error must not replace sticky invalid access')}}else void args.event.block.gasUsed}
  await args.context.client.getBalance({address:zeroAddress});
  if(learning){expect(await args.context.client.readContract({address:profileAddresses.get(chain.id)!,abi:profileAbi,functionName:'value'})).toBe(1n);expect(await args.context.db.find(account,{id:String(args.event.block.number)})).toBeNull()}
  const row=await args.context.db.find(account,{id:`shared${chain.id}`});await args.context.db.update(account,{id:`shared${chain.id}`}).set({balance:row!.balance+1n});
 }})));
 const initialIncludes=callbacks.map(cs=>cs[0].filter.include);
 if(learning){
  const event:any={type:'block',chain:chains[0],checkpoint:'0'.repeat(75),eventCallback:callbacks[0][0],event:{id:'profile-observation',block:{number:4n,timestamp:400n,hash:'0xprofile'}}};
  const primaryKeys:any=new Map([[account,[['id',account.id]]]]);
  const pattern=recordRowPattern(event,account,{id:'4'},[],primaryKeys)!;
  const recovered=recoverRowPattern(pattern,event),hint=recordRowPattern(event,account,{id:'4'},[pattern],primaryKeys),miss=recordRowPattern(event,account,{id:'3'},[pattern],primaryKeys);
  const driver=(account.id as any).mapToDriverValue(4n),cacheKey=getRowCacheKey(account,recovered,primaryKeys);
  expect(pattern).toEqual({id:{type:'derived',value:['block','number']}});expect(recovered).toEqual({id:4n});expect(hint).toBe(pattern);expect(miss).toBeUndefined();expect(driver).toBe(4n);expect(cacheKey).toBe('4');
  record.textProfileRecovery={source_credit:0,scope:'actual_source_profile_recovery_and_column_cache_key',key:{id:'4'},event:enc(event),pattern:enc(pattern),recovered:enc(recovered),hintSameIdentity:hint===pattern,mismatch:enc(miss),driver:enc(driver),cacheKey,public_crud_coercion_claim:false};
 }
 const indexingBuild:any={chains,rpcs,finalizedBlocks,contracts:[{},{}],indexingFunctions:[...setup.map(({name,fn})=>({name,fn})),{name:'Blocks:block',fn:callbacks[0][0].fn}],setupCallbacks:setup.map(s=>[s]),eventCallbacks:callbacks};
 common.metrics.initializeIndexingMetrics({indexingBuild,schemaBuild:{schema,statements:[]} as any});
 const originalIterator=historical.getHistoricalEventsMultichain;
 vi.spyOn(historical,'getHistoricalEventsMultichain').mockImplementation((args:any)=>{
  run.bootstrap=[...args.perChainSync].map(([chain,state]:any)=>({chainId:chain.id,start:state.syncProgress.getCheckpoint({tag:'start'}),finalized:state.syncProgress.getCheckpoint({tag:'finalized'}),end:state.syncProgress.getCheckpoint({tag:'end'}),cachedIntervals:enc(state.cachedIntervals),childAddresses:enc(state.childAddresses)}));
  const iterator=originalIterator(args);let emitted=0;
  return (async function*(){try{while(emitted<run.prefix){const next=await iterator.next();expect(txDepth).toBe(0);if(next.done){run.exhausted=true;break}run.groups.push(enc(next.value));record.timeline.push({kind:'consumer-yield',chainId:next.value.chainId,txDepth,run:record.runs.length});emitted++;yield next.value}run.harness_stop={brand:true,outside_transaction:txDepth===0,emitted};throw new HarnessStop()}finally{await iterator.return(undefined as any);run.iterator_returned=true}})();
 });
 const invoke=async(crash:any,prefix:number)=>{run={crash:enc(crash),prefix,groups:[],setup:[],attempts:[],caught:[],transactions:[]};record.runs.push(run);observation=createFiniteIndexingObservation(run);observations.push(observation);firstArgs=firstContext=firstDB=undefined;
  try{await runMultichain({common,preBuild:{ordering:'multichain',databaseConfig:context.databaseConfig},namespaceBuild:{schema:'public',viewsSchema:undefined},schemaBuild:{schema,statements:[]},indexingBuild,crashRecoveryCheckpoint:crash,database} as any);throw Error('run reached forbidden cutover')}catch(e){run.error=enc(e);run.error_is_harness_stop=e instanceof HarnessStop;run.error_is_invalid=e instanceof InvalidEventAccessError;run.error_is_first_row_error=e===firstRowErrors.get(record.runs.length+':'+(run.groups.length-1));run.shutdown_killed_at_error=common.shutdown.isKilled}finally{try{observation.snapshot()}catch(e){run.capture_error=enc(e);throw e}finally{observation.release()}}
  try{await Promise.allSettled(pending);if(inflight)await new Promise<void>(resolve=>drain=resolve);common.metrics.flushIndexingMetrics()}catch(e){run.drain_error=enc(e);throw e}
  try{run.after=await snapshot()}catch(e){run.snapshot_error=enc(e);throw e}
 };
 try{
  await invoke(undefined,mode==='resume'?2:refetchMode||mode==='nonretryable'?1:1e6);
  if(mode==='resume'||mode==='empty-resume'){const durable=record.runs[0].after.checkpoints.map((c:any)=>({chainId:c.chainId,checkpoint:c.latestCheckpoint}));await invoke(durable,1e6)}
  record.after=await snapshot();record.rpcPrefetchMetric=enc(await common.metrics.ponder_indexing_rpc_prefetch_total.get());record.rowPrefetchMetric=enc(await common.metrics.ponder_indexing_cache_requests_total.get());
  expect(record.identities.every((x:any)=>x.args&&x.context&&x.db)).toBe(true);expect(record.timeline.some((x:any)=>x.kind==='forbidden-cutover')).toBe(false);
  expect(record.timeline.filter((x:any)=>x.kind==='row-prefetch'||x.kind==='rpc-prefetch').every((x:any)=>x.txDepth===0)).toBe(true);
  for(const r of record.runs){expect(record.timeline.filter((x:any)=>x.run===record.runs.indexOf(r)+1&&x.kind==='row-prefetch')).toHaveLength(r.groups.length);expect(record.timeline.filter((x:any)=>x.run===record.runs.indexOf(r)+1&&x.kind==='rpc-prefetch')).toHaveLength(r.groups.length)}
  if(mode==='nonretryable'){expect(run.error.message).toBe('M43 nonretryable handler');expect(run.attempts).toHaveLength(1);expect(run.transactions.every((t:any)=>t.callbacks===1)).toBe(true)}
  else if(refetchMode){expect(record.refetch).toHaveLength(1);expect(record.refetch[0].txDepth).toBe(1);expect(record.refetch[0].before[0].event.block).not.toHaveProperty('gasUsed');expect(record.refetch[0].after[0].event.block).toHaveProperty('gasUsed');if(backend==='postgres'){expect(run.error_is_harness_stop).toBe(true);expect(run.transactions[1].callbacks).toBe(2)}else{expect(run.error_is_invalid).toBe(true);expect(run.transactions[1].callbacks).toBe(1)}if(mode==='sticky-refetch')expect(run.caught[0].name).toBe('InvalidEventAccessError')}
  else if(pressure&&backend==='pglite'){expect(run.error.name).toBe('Error');expect(run.error.message).toBe('Invalid input for string type');expect(run.error_is_first_row_error).toBe(true);expect(run.shutdown_killed_at_error).toBe(false);expect(run.error_is_harness_stop).toBe(false);expect(run.error_is_invalid).toBe(false)}
  else {expect(record.runs.every((r:any)=>r.error_is_harness_stop)).toBe(true);expect(record.runs[0].groups.length).toBeGreaterThan(1);expect(new Set(record.runs.flatMap((r:any)=>r.groups.map((g:any)=>g.chainId))).size).toBe(2)}
  if(mode==='resume'||mode==='empty-resume'){expect(record.runs[1].setup).toEqual([]);for(const g of record.runs[1].groups)expect(g.events.every((e:any)=>e.checkpoint>record.runs[1].crash.find((c:any)=>c.chainId===g.chainId).checkpoint)).toBe(true);if(mode==='empty-resume')expect(record.runs[1].groups.every((g:any)=>g.events.length===0)).toBe(true)}
  if(learning){
   const expectedError=pressure&&backend==='pglite',prefetches=record.timeline.filter((t:any)=>t.kind==='row-prefetch'),failed=prefetches.filter((t:any)=>t.settled==='rejected');
   const completedGroups=expectedError?run.groups.slice(0,-1):run.groups;
   expect(run.transactions.filter((t:any)=>t.groupIndex>=0).map((t:any)=>({index:t.groupIndex,returned:t.returned,callbacks:t.callbacks}))).toEqual(completedGroups.map((_:any,index:number)=>({index,returned:true,callbacks:1})));
   expect(run.attempts).toEqual(completedGroups.flatMap((g:any)=>g.events.map((e:any)=>({chainId:g.chainId,block:e.event.block.number.$bigint}))));
   for(const chain of chains){const groups=completedGroups.filter((g:any)=>g.chainId===chain.id),events=groups.flatMap((g:any)=>g.events),boot=run.bootstrap.find((b:any)=>b.chainId===chain.id);expect(run.after.business.find((r:any)=>r.id===`shared${chain.id}`).balance).toEqual({$bigint:String(events.length)});expect(run.after.checkpoints.find((p:any)=>p.chainId===chain.id).latestCheckpoint).toBe(groups.at(-1)?.checkpoint??[boot.start,boot.finalized].sort()[0]);if(!expectedError){expect(events.map((e:any)=>Number(e.event.block.number.$bigint))).toEqual(Array.from({length:120},(_,i)=>i+1));expect(run.after.checkpoints.find((p:any)=>p.chainId===chain.id).latestCheckpoint).toBe(boot.end)}}
   expect(record.rpc.every((r:any)=>!r.error&&r.response!==undefined)).toBe(true);expect(record.timeline.filter((t:any)=>t.kind==='rpc-prefetch').every((t:any)=>t.settled==='fulfilled')).toBe(true);
   if(expectedError){
    expect(failed).toHaveLength(1);expect(failed[0].groupIndex).toBe(run.groups.length-1);expect(failed[0].txDepth).toBe(0);expect(failed[0].error_is_first_row_error).toBe(true);expect(failed[0].error.message).toBe('Invalid input for string type');
    expect(record.retryWarnings.map((w:any)=>w.retry_count)).toEqual(Array.from({length:10},(_,i)=>i));expect(record.retryWarnings.slice(0,9).map((w:any)=>w.retry_delay)).toEqual(Array.from({length:9},(_,i)=>125*2**i));expect(record.retryWarnings[9]).not.toHaveProperty('retry_delay');expect(record.retryWarnings.every((w:any)=>w.groupIndex===failed[0].groupIndex&&w.run===1&&w.txDepth===0&&w.error.name==='Error'&&w.error.constructorName==='Error'&&w.error.message==='Invalid input for string type')).toBe(true);
    expect(run.attempts.length).toBeGreaterThan(0);expect(run.attempts.length).toBeLessThan(240);expect(run.iterator_returned).toBe(true);expect(run.exhausted).not.toBe(true);
   }else{
    expect(failed).toEqual([]);expect(record.retryWarnings).toEqual([]);expect(run.attempts).toHaveLength(240);expect(run.exhausted).toBe(true);
    const includes=callbacks.map(cs=>cs[0].filter.include);record.resolvedIncludeIdentity={separate:includes[0]!==includes[1],replaced:includes.map((value,i)=>value!==initialIncludes[i])};expect(record.resolvedIncludeIdentity.separate).toBe(true);expect(record.resolvedIncludeIdentity.replaced).toEqual([true,true]);expect(run.eventCount['Blocks:block']).toBeGreaterThan(100);expect(run.columnAccessPattern.find((x:any)=>x[0]==='Blocks:block')[1].resolved).toBe(true);
   }
   expect(record.rpcPrefetchMetric.values.reduce((n:number,v:any)=>n+v.value,0)).toBeGreaterThan(0);const rowPrefetchPredictions=record.rowPrefetchMetric.values.filter((v:any)=>v.labels.type==='prefetch').reduce((n:number,v:any)=>n+v.value,0);if(pressure)expect(rowPrefetchPredictions).toBeGreaterThan(0);else expect(rowPrefetchPredictions).toBe(0);
   record.learningOutcome={configuredEvents:240,completed:!expectedError,outcome:expectedError?'expected_upstream_error':'completed',sourceErrorObserved:expectedError,diagnosticAbort:false,fullBackoffTerminalObserved:expectedError,rowPrefetchPredictions};
  }
  record.native_difference='Native PGlite owns independent persistent sync/business directories with actual SQL rollback. Source PGlite transaction invokes the callback once without SQL transaction or automatic retry; PostgreSQL retries InvalidEventAccess after actual in-transaction refetch.';
 }catch(e){record.probe_error=enc(e);throw e}finally{try{await Promise.allSettled(pending);if(inflight)await new Promise<void>(resolve=>drain=resolve)}catch(e){record.drain_error=enc(e);throw e}finally{database.userQB.transaction=transaction;common.logger.child=child;record.drained={rpcInflight:inflight,cacheWritesSettled:pending.length};try{await common.shutdown.kill()}catch(e){record.cleanup_error=enc(e);throw e}}}
},mode==='learned-prefetch'&&!process.env.DATABASE_URL?90_000:mode.startsWith('learned-prefetch')?60_000:undefined);
afterAll(()=>{const reporterErrors:any[]=[];for(const observation of observations){try{observation.snapshot()}catch(e){reporterErrors.push(enc(e))}finally{observation.release()}}const output=process.env.PONDER_FINITE_HISTORICAL_CAPTURE;if(!output)throw Error('Explicit capture required');fs.writeFileSync(output,JSON.stringify({source_credit:0,cases:reports,reporter_errors:reporterErrors},null,2)+'\n');if(reporterErrors.length)throw new AggregateError(reporterErrors,'M43 evidence snapshot failed')});
