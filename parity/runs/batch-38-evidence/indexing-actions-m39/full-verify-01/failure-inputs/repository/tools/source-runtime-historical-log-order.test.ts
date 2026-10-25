// Supplemental real-source ordering probe, not an original source case.
import fs from 'node:fs';
import {beforeEach, expect, test, vi} from 'vitest';
import {context,setupAnvil,setupCleanup,setupCommon,setupDatabaseServices,setupIsolatedDatabase} from '@/_test/setup.js';
import {getBlocksIndexingBuild,getErc20IndexingBuild,getChain,testClient} from '@/_test/utils.js';
import {eth_getBlockByNumber} from '@/rpc/actions.js';
import {createRpc} from '@/rpc/index.js';
import {getCachedIntervals,getLocalSyncProgress} from '@/runtime/index.js';
import {getLocalSyncGenerator} from '@/runtime/historical.js';
import * as syncHistorical from '@/sync-historical/index.js';
import {drainAsyncGenerator} from '@/utils/generators.js';
beforeEach(setupCommon);beforeEach(setupAnvil);beforeEach(setupIsolatedDatabase);beforeEach(setupCleanup);
test('supplemental real-source actual log RPC prefetch order',async()=>{
 const {database,syncStore}=await setupDatabaseServices();
 const chain=getChain(),rpc=createRpc({chain,common:context.common});
 const eventCallbacks=[...getBlocksIndexingBuild({interval:1}).eventCallbacks,...getErc20IndexingBuild({address:'0x0000000000000000000000000000000000000001'}).eventCallbacks];
 await testClient.mine({blocks:27});
 const cachedIntervals=await getCachedIntervals({chain,syncStore,filters:eventCallbacks.map(x=>x.filter)});
 const syncProgress=await getLocalSyncProgress({common:context.common,chain,rpc,filters:eventCallbacks.map(x=>x.filter),cachedIntervals,finalizedBlock:await eth_getBlockByNumber(rpc,['0x1b',true])});
 const timeline:any[]=[],add=(action:string,extra:any={})=>timeline.push({sequence:timeline.length,action,...extra});
 let transaction=0;
 const transactionOriginal=database.syncQB.transaction.bind(database.syncQB);
 (database.syncQB as any).transaction=async(callback:any,...rest:any[])=>{
  const id=++transaction;add('transaction_requested',{id});
  const result=await(transactionOriginal as any)(async(tx:any)=>{add('transaction_entered',{id});const result=await callback(tx);add('transaction_body_returned',{id});return result},...rest);
  add('transaction_resolved',{id});return result;
 };
 const originalCreate=syncHistorical.createHistoricalSync;
 vi.spyOn(syncHistorical,'createHistoricalSync').mockImplementation((args:any)=>{
  const value=originalCreate(args),range=value.syncBlockRangeData.bind(value),block=value.syncBlockData.bind(value);
  value.syncBlockRangeData=async(args:any)=>{add('range_started',{interval:args.interval});const value=await range(args);add('range_finished',{interval:args.interval});return value};
  value.syncBlockData=async(args:any)=>{add('block_started',{interval:args.interval});if(args.interval[0]===0){add('first_block_delay_started');await new Promise(resolve=>setTimeout(resolve,300));add('first_block_delay_finished')};const value=await block(args);add('block_finished',{interval:args.interval});return value};
  return value;
 });
 const request=rpc.request.bind(rpc);
 (rpc as any).request=async(...args:any[])=>{add('rpc_started',{method:args[0].method,params:args[0].params});const value=await(request as any)(...args);add('rpc_finished',{method:args[0].method,params:args[0].params});return value};
 const output=await drainAsyncGenerator(getLocalSyncGenerator({common:context.common,chain,rpc,eventCallbacks,childAddresses:new Map(),syncProgress,cachedIntervals,database,isCatchup:false}));
 const range2=timeline.find(x=>x.action==='range_started'&&x.interval[0]===26),commit1=timeline.find(x=>x.action==='transaction_resolved'&&x.id===1);
 const rangeRPC=timeline.find(x=>x.action==='rpc_started'&&x.method==='eth_getLogs'&&x.params[0].fromBlock==='0x1a');
 const result={range_rpc_prefetched:!!rangeRPC&&rangeRPC.sequence<commit1.sequence,source_credit:0,backend:process.env.DATABASE_URL?'postgres':'pglite',source:'packages/core/src/runtime/historical.ts#getLocalSyncGenerator',delayed_first_block_ms:300,real_database:true,transaction_mode:process.env.DATABASE_URL?'SQL transaction':'callback with retries, no SQL transaction',blocks_mined:27,timeline,output,range2_before_commit1:range2.sequence<commit1.sequence};
 const file=process.env.PONDER_RUNTIME_HISTORICAL_ORDER;if(!file)throw Error('Explicit supplemental output required');fs.writeFileSync(file,JSON.stringify(result,null,2)+'\n');
 expect(output).toEqual([25,27]);expect(transaction).toBe(2);expect(rangeRPC).toBeDefined();expect(rangeRPC.sequence).toBeLessThan(commit1.sequence);
});
