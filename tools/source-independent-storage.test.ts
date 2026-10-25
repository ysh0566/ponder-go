// Supplemental real source lifecycle probe. No original-test credit.
// Production modules remain byte-identical. The two table schema identifiers
// alone are fixture-local, so this never adopts or drops preexisting namespaces.
import fs from 'node:fs';
import crypto from 'node:crypto';
import {test,expect} from 'vitest';
import pg from 'pg';
import {PGlite} from '@electric-sql/pglite';
import {drizzle as pgDrizzle} from 'drizzle-orm/node-postgres';
import {drizzle as liteDrizzle} from 'drizzle-orm/pglite';
import {encodeFunctionResult} from 'viem';
import {mainnet} from 'viem/chains';
import {setupCommon,context} from '@/_test/setup.js';
import {createQB} from '@/database/queryBuilder.js';
import {onchainTable} from '@/drizzle/onchain.js';
import {createSyncStore} from '@/sync-store/index.js';
import {rpcRequestResults} from '@/sync-store/schema.js';
import {createCachedViemClient} from '@/indexing/client.js';
import {createIndexingCache} from '@/indexing-store/cache.js';
import {createIndexingStore} from '@/indexing-store/index.js';
import {createIndexing,createColumnAccessPattern,getEventCount} from '@/indexing/index.js';
import {NonRetryableUserError} from '@/internal/errors.js';
import {encodeCheckpoint,ZERO_CHECKPOINT} from '@/utils/checkpoint.js';
const output=process.env.PONDER_INDEPENDENT_SOURCE_CAPTURE;
if(!output)throw Error('Explicit capture required');
const enc=(v:any):any=>typeof v==='bigint'?{$bigint:String(v)}:Array.isArray(v)?v.map(enc):v&&typeof v==='object'?Object.fromEntries(Object.entries(v).map(([k,x])=>[k,enc(x)])):v;

test('actual source persistent indexing cache/client across userQB bindings',async()=>{
 setupCommon();const {common}=context;const postgres=!!process.env.DATABASE_URL;
 const ns='ponder_go_m41src_'+crypto.randomBytes(8).toString('hex'),sync=ns+'_sync';
 const client:any=postgres?new pg.Pool({connectionString:process.env.DATABASE_URL,max:4}):new PGlite();
 const query=async(sql:string)=>postgres?client.query(sql):client.query(sql);
 const schemaSymbol=Symbol.for('drizzle:Schema'),oldSync=(rpcRequestResults as any)[schemaSymbol];
 const record:any={source_credit:0,real_database:true,real_rpc:false,backend:postgres?'postgres':'pglite',topology:postgres?'source distinct QB handles, shared PG pool fixture':'source distinct QB handles, same PGlite instance',namespace_adaptation:'Only fixture table schema identifiers; production source bytes unchanged',pending_gate:'Wrapper admission delays invocation of original syncStore insertion until release; eventual SQL is real, not proof of source SQL queue admission or concurrent commit order',calls:[],batches:[],identities:[]};
 let release:undefined|(()=>void),held:Promise<void>|undefined;
 let createdBusiness=false,createdSync=false;
 const pending:Promise<any>[]=[];
 try{
  // CREATE without IF NOT EXISTS deliberately refuses collision, before touching rows.
  await query(`CREATE SCHEMA "${ns}"`);createdBusiness=true;await query(`CREATE SCHEMA "${sync}"`);createdSync=true;
  (globalThis as any).PONDER_NAMESPACE_BUILD={schema:ns};
  const account=onchainTable('account',p=>({id:p.text().primaryKey(),balance:p.bigint().notNull()}));
  delete (globalThis as any).PONDER_NAMESPACE_BUILD;
  (rpcRequestResults as any)[schemaSymbol]=sync;
  await query(`CREATE TABLE "${ns}"."account" (id text PRIMARY KEY,balance numeric(78,0) NOT NULL)`);
  await query(`CREATE TABLE "${sync}"."rpc_request_results" (request_hash text NOT NULL,chain_id bigint NOT NULL,block_number bigint,result text NOT NULL,PRIMARY KEY(chain_id,request_hash))`);
  const schema={account};
  const db=(s:any)=>postgres?pgDrizzle(client,{schema:s,casing:'snake_case'}):liteDrizzle(client,{schema:s,casing:'snake_case'});
  const userQB=createQB(db(schema) as any,{common}),syncQB=createQB(db({rpcRequestResults}) as any,{common});
  const syncStore=createSyncStore({common,qb:syncQB as any});
  const insert=syncStore.insertRpcRequestResults.bind(syncStore);
  syncStore.insertRpcRequestResults=((...args:any[])=>{const entry:any={operation:'cache-write-admit',args:enc(args[0])};record.calls.push(entry);const wait=held;const promise=(async()=>{if(wait)await wait;await (insert as any)(...args);entry.settled=true})();pending.push(promise);return promise}) as any;
  const abi=[{type:'function',name:'value',stateMutability:'view',inputs:[],outputs:[{type:'uint256'}]}] as const;
  const address='0x1111111111111111111111111111111111111111';
  const chain:any={id:1,name:'mainnet',viemChain:mainnet,cacheRpcRequests:true};
  const rpc:any={request:async(request:any)=>{record.calls.push({operation:'rpc',request:enc(request)});return request.method==='eth_getBalance'?'0x2a':encodeFunctionResult({abi,functionName:'value',result:41n})}};
  const functions:any=[{name:'Probe:setup',fn:()=>{}},{name:'Probe:block',fn:()=>{}}];
  const eventCount=getEventCount(functions),errors:any={error:undefined,getRetryableError(){return this.error},setRetryableError(e:any){this.error=e},clearRetryableError(){this.error=undefined}};
  const cached=createCachedViemClient({common,indexingBuild:{chains:[chain],rpcs:[rpc]} as any,syncStore,eventCount});
  const cache=createIndexingCache({common,schemaBuild:{schema},crashRecoveryCheckpoint:undefined,eventCount});
  const store=createIndexingStore({common,schemaBuild:{schema},indexingCache:cache,indexingErrorHandler:errors});
  let row='setup',first:any,firstClient:any;
  const handler=async(args:any)=>{if(!first){first=args;firstClient=args.context.client}record.identities.push({args:args===first,context:args.context===first.context,db:args.context.db===store.db,client:args.context.client===firstClient});expect(await args.context.client.getBalance({address})).toBe(42n);expect(await args.context.client.readContract({address,abi,functionName:'value',retryEmptyResponse:false})).toBe(41n);await args.context.db.insert(account).values({id:row,balance:42n})};
  const setup:any={name:'Probe:setup',chain,block:7,fn:handler};
  const callback:any={name:'Probe:block',chain,type:'block',fn:handler,filter:{type:'block',chainId:1,sourceId:'Probe',interval:1,offset:0,include:['block.number'],hasTransactionReceipt:false}};
  const indexing=createIndexing({common,indexingBuild:{chains:[chain],contracts:[{}],indexingFunctions:functions,eventCallbacks:[[callback]],setupCallbacks:[[setup]]} as any,indexingStore:store,indexingCache:cache,client:cached,indexingErrorHandler:errors,eventCount,columnAccessPattern:createColumnAccessPattern({indexingBuild:{indexingFunctions:functions}})});
  const event=(block:number):any=>({type:'block',chain,eventCallback:callback,checkpoint:encodeCheckpoint({...ZERO_CHECKPOINT,chainId:1n,blockNumber:BigInt(block)}),event:{block:{number:BigInt(block),timestamp:17n}}});
  const rows=async()=>({business:(await query(`SELECT * FROM "${ns}"."account" ORDER BY id`)).rows,cache:(await query(`SELECT * FROM "${sync}"."rpc_request_results" ORDER BY request_hash`)).rows});
  const batch=async(name:string,block:number,rollback=false)=>{row=name;const entry:any={name,rollback};record.batches.push(entry);try{await userQB.transaction(async(tx:any)=>{store.qb=tx;cache.qb=tx;store.isProcessingEvents=true;try{if(name==='setup')await indexing.processSetupEvents();else await indexing.processHistoricalEvents({events:[event(block)],updateIndexingSeconds:()=>{}});await cache.flush();if(rollback)throw new NonRetryableUserError('intentional rollback')}finally{store.isProcessingEvents=false}})}catch(e){entry.error=(e as Error).message;entry.error_stack=(e as Error).stack;if(!rollback)throw e;expect(entry.error).toBe('intentional rollback');cache.invalidate()}entry.after=enc(await rows())};
  await batch('setup',7);await Promise.all(pending);expect(record.calls.filter((c:any)=>c.operation==='rpc')).toHaveLength(2);
  await batch('cache-hit',7);await Promise.all(pending);expect(record.calls.filter((c:any)=>c.operation==='rpc')).toHaveLength(2);
  await batch('rolled-back',8,true);await Promise.all(pending);const afterRollback=await rows();expect(afterRollback.cache).toHaveLength(4);expect(afterRollback.business.some((r:any)=>r.id==='rolled-back')).toBe(!postgres);
  held=new Promise<void>(resolve=>{release=resolve});await batch('pending-rollback',9,true);expect(pending.length).toBe(6);const before=await rows();expect(before.cache).toHaveLength(4);release!();held=undefined;await Promise.all(pending);const after=await rows();expect(after.cache).toHaveLength(6);record.pending_before=enc(before);record.final=enc(after);record.final_cache_reads=[];for(const call of record.calls.filter((c:any)=>c.operation==='rpc')){const value=await syncStore.getRpcRequestResults({chainId:1,requests:[call.request]});expect(value).toEqual([JSON.stringify(call.request.method==='eth_getBalance'?'0x2a':encodeFunctionResult({abi,functionName:'value',result:41n}))]);record.final_cache_reads.push({request:call.request,result:value})}
  expect(record.identities.every((x:any)=>Object.values(x).every(Boolean))).toBe(true);
  record.intentional_native_difference='Native PGlite uses two owned database directories and real SQL transactions; pinned source uses one instance with callback-only QB transaction, so flushed business writes survive source rollback.';
 }finally{
  release?.();await Promise.allSettled(pending);(rpcRequestResults as any)[schemaSymbol]=oldSync;delete (globalThis as any).PONDER_NAMESPACE_BUILD;
  // These names were randomly generated and created by this exact test only.
  if(createdSync)await query(`DROP SCHEMA "${sync}" CASCADE`);if(createdBusiness)await query(`DROP SCHEMA "${ns}" CASCADE`);
  if(postgres)await client.end();else await client.close();await common.shutdown.kill();
  fs.writeFileSync(output,JSON.stringify(record,null,2)+'\n');
 }
});
