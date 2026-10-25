// Zero-credit bounded prefix of unchanged runMultichain, with real extraction,
// real two-chain RPC, handlers and SQL. The only iterator control stops the
// consumer on its next pull, outside any business transaction and before cutover.
import fs from 'node:fs';
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
import * as syncModule from '@/sync-store/index.js';
import * as actions from '@/database/actions.js';
import * as tables from '@/sync-store/schema.js';
import {NonRetryableUserError} from '@/internal/errors.js';
import {min} from '@/utils/checkpoint.js';
const otherID=TEST_POOL_ID+1;
const otherClient=createTestClient({chain:{...anvil,id:2,rpcUrls:{default:{http:[`http://127.0.0.1:8545/${otherID}`]}}},mode:'anvil',transport:http()});
beforeEach(setupCommon);beforeEach(setupAnvil);
beforeEach(async()=>{await otherClient.request({method:'anvil_setChainId' as any,params:[2] as any});const id=await otherClient.snapshot();return async()=>{await otherClient.revert({id})}});
beforeEach(setupIsolatedDatabase);beforeEach(setupCleanup);
const account=onchainTable('account',p=>({id:p.text().primaryKey(),balance:p.bigint().notNull()}));
const schema={account};
const reports:any[]=[];
function enc(v:any):any {if(v===undefined)return {$undefined:true};if(typeof v==='bigint')return {$bigint:String(v)};if(typeof v==='function')return {$function:v.name};if(v instanceof Error)return {name:v.name,message:v.message};if(v instanceof Map)return [...v].map(([k,x])=>[enc(k),enc(x)]);if(v instanceof Set)return [...v].map(enc);if(Array.isArray(v))return v.map(enc);if(v&&typeof v==='object')return Object.fromEntries(Object.entries(v).map(([k,x])=>[k,enc(x)]));return v}
class HarnessStop extends NonRetryableUserError {readonly harnessStop=true;constructor(message:string){super(message);Object.setPrototypeOf(this,HarnessStop.prototype)}}
for(const mode of ['setup-success','setup-second-chain-failure','historical-success','historical-second-event-failure'])test(`actual runMultichain bounded ${mode}`,async()=>{
 const {common}=context;
 const record:any={name:mode,source_credit:0,backend:process.env.DATABASE_URL?'postgres':'pglite',production_function:'runMultichain',timeline:[],rpc:[],groups:[],identities:[],attempts:[],transactions:[],pruning:'Source historical setup/groups do not install native MAX undo segments; no journal attribution equivalence claimed.'};reports.push(record);
 const chains=[getChain({reorgWindow:0,cacheRpcRequests:true}),{...getChain({reorgWindow:0,cacheRpcRequests:true}),id:2,name:'second',rpc:`http://127.0.0.1:8545/${otherID}`}];
 const rpcs=chains.map(chain=>createRpc({chain,common}));
 const clients=[testClient,otherClient];
 for(const client of clients)await client.mine({blocks:3});
 const finalizedBlocks=await Promise.all(rpcs.map(rpc=>eth_getBlockByNumber(rpc,['0x3',true])));
 const {database}=await setupDatabaseServices({schemaBuild:{schema},indexingBuild:{chains,finalizedBlocks}});
 const checkpoints=getPonderCheckpointTable('public'),meta=getPonderMetaTable('public');
 const snapshot=async()=>enc({business:await database.userQB.wrap((db:any)=>db.select().from(account)),checkpoints:await database.userQB.wrap((db:any)=>db.select().from(checkpoints)),meta:await database.userQB.wrap((db:any)=>db.select().from(meta)),rpcCache:await database.syncQB.wrap((db:any)=>db.select().from(tables.rpcRequestResults))});
 record.before=await snapshot();
 let inflight=0,drain:(()=>void)|undefined;const pending:Promise<any>[]=[];
 for(let i=0;i<rpcs.length;i++){const rpc=rpcs[i],request=rpc.request;rpc.request=async function(...args:any[]){const item:any={chainId:chains[i].id,request:enc(args)};record.rpc.push(item);inflight++;try{const value=await Reflect.apply(request,this,args);item.response=enc(value);return value}catch(e){item.error=enc(e);throw e}finally{if(--inflight===0)drain?.()}}}
 const createSync=syncModule.createSyncStore;
 vi.spyOn(syncModule,'createSyncStore').mockImplementation((args:any)=>{const store=createSync(args);const insert=store.insertRpcRequestResults;store.insertRpcRequestResults=(...xs:any[])=>{const p=Reflect.apply(insert,store,xs);pending.push(p);return p};return store});
 let indexingArgs:any,cache:any,firstArgs:any,firstContext:any,firstDB:any;
 const createIndexing=indexingModule.createIndexing;
 vi.spyOn(indexingModule,'createIndexing').mockImplementation((args:any)=>{indexingArgs=args;return createIndexing(args)});
 const createCache=cacheModule.createIndexingCache;
 vi.spyOn(cacheModule,'createIndexingCache').mockImplementation((args:any)=>{cache=createCache(args);const flush=cache.flush;cache.flush=async(...xs:any[])=>{record.timeline.push({kind:'flush-begin'});try{const result=await Reflect.apply(flush,cache,xs);record.timeline.push({kind:'flush-end'});return result}catch(e){record.timeline.push({kind:'flush-error',error:enc(e)});throw e}};return cache});
 for(const name of ['createViews','createIndexes','createTriggers','createLiveQueryTriggers'] as const){const original=actions[name];vi.spyOn(actions,name).mockImplementation((...xs:any[])=>{record.timeline.push({kind:'forbidden-cutover',name});return Reflect.apply(original,actions,xs)})}
 let txDepth=0;
 const transaction=database.userQB.transaction;
 database.userQB.transaction=(async(...args:any[])=>{const callbackIndex=typeof args[0]==='function'?0:1;const callback=args[callbackIndex];const txRecord:any={callbacks:0};record.transactions.push(txRecord);args[callbackIndex]=async(tx:any)=>{txDepth++;txRecord.callbacks++;record.timeline.push({kind:'transaction-begin'});const wrap=tx.wrap;tx.wrap=async(...xs:any[])=>{if(xs[0]?.label==='update_checkpoints')record.timeline.push({kind:'metadata-begin'});const result=await Reflect.apply(wrap,tx,xs);if(xs[0]?.label==='update_checkpoints')record.timeline.push({kind:'metadata-end'});return result};try{return await callback(tx)}finally{tx.wrap=wrap;txDepth--;record.timeline.push({kind:'transaction-callback-end'})}};try{const result=await Reflect.apply(transaction,database.userQB,args);txRecord.result='returned';record.timeline.push({kind:'transaction-return'});return result}catch(e){txRecord.error=enc(e);record.timeline.push({kind:'transaction-error',error:enc(e)});throw e}}) as any;
 const identity=(args:any)=>{if(!firstArgs){firstArgs=args;firstContext=args.context;firstDB=args.context.db}record.identities.push({args:args===firstArgs,context:args.context===firstContext,db:args.context.db===firstDB,chainId:args.context.chain.id})};
 const setup=(name:string,chain:any,value:bigint,block:number)=>({name,chain,block,fn:async(args:any)=>{identity(args);record.timeline.push({kind:'setup-handler',name,chainId:chain.id});expect(args.context.chain.id).toBe(chain.id);await args.context.client.getBalance({address:zeroAddress});if(mode==='setup-second-chain-failure'&&chain.id===2)throw new NonRetryableUserError('M42 setup second chain failure');await args.context.db.insert(account).values({id:'shared',balance:value}).onConflictDoUpdate({balance:value})}});
 const setups=[setup('First:setup',chains[0],1n,2),setup('Second:setup',chains[1],2n,1),setup('Again:setup',chains[0],3n,3)];
 let eventAttempts=0;
 const callbacks=chains.map(chain=>getBlocksIndexingBuild({interval:1}).eventCallbacks.map(c=>({...c,chain,filter:{...c.filter,chainId:chain.id},fn:async(args:any)=>{identity(args);eventAttempts++;record.attempts.push({chainId:chain.id,block:String(args.event.block.number),attempt:eventAttempts});record.timeline.push({kind:'historical-handler',chainId:chain.id,attempt:eventAttempts});await args.context.client.getBalance({address:zeroAddress});if(mode==='historical-second-event-failure'&&eventAttempts===2)throw new NonRetryableUserError('M42 history second event failure');await args.context.db.insert(account).values({id:`${chain.id}:${args.event.block.number}`,balance:BigInt(eventAttempts)})}})));
 const indexingBuild:any={chains,rpcs,finalizedBlocks,contracts:[{},{}],indexingFunctions:[...setups.map(({name,fn})=>({name,fn})),{name:'Blocks:block',fn:callbacks[0][0].fn}],setupCallbacks:[[setups[0],setups[1]],[setups[2]]],eventCallbacks:callbacks};
 common.metrics.initializeIndexingMetrics({indexingBuild,schemaBuild:{schema,statements:[]} as any});
 record.initialCompletedMetric=enc(await common.metrics.ponder_indexing_completed_events.get());
 const originalIterator=historical.getHistoricalEventsMultichain;const prefix=mode.startsWith('setup-')?0:1;
 vi.spyOn(historical,'getHistoricalEventsMultichain').mockImplementation((args:any)=>{
  record.initialCheckpoints=[...args.perChainSync].map(([chain,state]:any)=>({chainId:chain.id,start:state.syncProgress.getCheckpoint({tag:'start'}),finalized:state.syncProgress.getCheckpoint({tag:'finalized'}),initial:min(state.syncProgress.getCheckpoint({tag:'start'}),state.syncProgress.getCheckpoint({tag:'finalized'}))}));
  const iterator=originalIterator(args);let emitted=0;
  return (async function*(){try{while(emitted<prefix){const next=await iterator.next();if(next.done)throw Error('real iterator ended before selected prefix');record.groups.push(enc(next.value));record.timeline.push({kind:'consumer-yield',chainId:next.value.chainId,txDepth});emitted++;yield next.value}expect(txDepth).toBe(0);record.harness_stop={brand:true,outside_transaction:true,emitted};record.timeline.push({kind:'harness-stop',txDepth});throw new HarnessStop('M42 selected real prefix exhausted')}finally{await iterator.return(undefined as any);record.iterator_returned=true}})();
 });
 try{
  try{await runMultichain({common,preBuild:{ordering:'multichain',databaseConfig:context.databaseConfig},namespaceBuild:{schema:'public',viewsSchema:undefined},schemaBuild:{schema,statements:[]},indexingBuild,crashRecoveryCheckpoint:undefined,database} as any);throw Error('runner unexpectedly reached cutover')}catch(error){record.error=enc(error);record.error_is_harness_stop=error instanceof HarnessStop;if(mode.endsWith('success'))expect(error).toBeInstanceOf(HarnessStop);else expect((error as Error).message).toBe(mode.startsWith('setup-')?'M42 setup second chain failure':'M42 history second event failure')}
  await Promise.allSettled(pending);if(inflight)await new Promise<void>(resolve=>drain=resolve);
  common.metrics.flushIndexingMetrics();
  record.completedMetric=enc(await common.metrics.ponder_indexing_completed_events.get());record.eventCount=enc(indexingArgs.eventCount);record.columnAccessPattern=enc(indexingArgs.columnAccessPattern);
  record.after=await snapshot();
  expect(record.identities.every((x:any)=>x.args&&x.context&&x.db)).toBe(true);
  expect(record.timeline.some((x:any)=>x.kind==='forbidden-cutover')).toBe(false);
  expect(record.transactions.every((x:any)=>x.callbacks===1)).toBe(true);
  if(mode!=='setup-second-chain-failure'){
   expect(record.timeline.filter((x:any)=>x.kind==='setup-handler').map((x:any)=>x.chainId)).toEqual([1,2,1]);
   const setupState=record.initialCheckpoints;
   expect(setupState).toHaveLength(2);
   if(mode==='setup-success'){
    expect(record.after.business).toEqual([{id:'shared',balance:{$bigint:'3'}}]);
    for(const expected of setupState){const state=record.after.checkpoints.find((x:any)=>x.chainId===expected.chainId);expect(state.latestCheckpoint).toBe(expected.initial);expect(state.safeCheckpoint).toBe(expected.initial);expect(state.finalizedCheckpoint).toBe(expected.initial)}
   }
  }else expect(record.after.checkpoints).toEqual(record.before.checkpoints);
  if(mode==='historical-second-event-failure'){expect(eventAttempts).toBe(2);expect(indexingArgs.eventCount['Blocks:block']).toBe(1);expect(indexingArgs.columnAccessPattern.get('Blocks:block').count).toBe(1)}
  record.native_difference='Native PGlite owns independent business/sync directories and uses true SQL transaction rollback. Pinned source uses callback-only PGlite userQB transaction; this report records its actual outcomes, not native rollback equivalence.';
 }finally{await Promise.allSettled(pending);if(inflight)await new Promise<void>(resolve=>drain=resolve);database.userQB.transaction=transaction;record.drained={rpcInflight:inflight,cacheWritesSettled:pending.length};await common.shutdown.kill()}
});
afterAll(()=>{const output=process.env.PONDER_MULTICHAIN_ATOMIC_CAPTURE;if(!output)throw Error('Explicit capture required');fs.writeFileSync(output,JSON.stringify({source_credit:0,cases:reports},null,2)+'\n')});
