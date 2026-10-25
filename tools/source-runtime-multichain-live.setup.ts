// Observe the complete unchanged original file. Only actual inputs and raw RPC
// responses may seed native replay; generated events and physical rows are checks.
import fs from 'node:fs';
import path from 'node:path';
import {beforeEach, afterEach, afterAll, onTestFinished, vi} from 'vitest';
import * as setup from '@/_test/setup.js';
import * as historical from '@/runtime/historical.js';
import * as events from '@/runtime/events.js';
import * as tables from '@/sync-store/schema.js';
import {createRealtimeRPCObserver} from './source-realtime-rpc-observer.mjs';
const root=process.env.PONDER_PORT_ROOT, capture=process.env.PONDER_RUNTIME_MULTICHAIN_CAPTURE;
if(!root||!capture)throw Error('Explicit runtime historical capture required');
const ledger=JSON.parse(fs.readFileSync(path.join(root,'parity/test-ledger.json'),'utf8'));
const selected=ledger.tests.filter((t:any)=>t.source_file==='packages/core/src/runtime/historical.test.ts');
if(selected.length!==14||selected.reduce((n:number,t:any)=>n+t.assertions.length,0)!==29||selected.some((t:any)=>t.type_error_directives.length))throw Error('Runtime historical census changed');
let current:any, ids:WeakMap<object,number>, serial=0;
const completed:any[]=[];
function mark(value:any):any {
 const expanded=new Set<object>();
 function walk(v:any):any{
  if(v===undefined)return {$undefined:true};
  if(typeof v==='bigint')return {$bigint:String(v)};
  if(typeof v==='number'&&!Number.isFinite(v))return {$number:String(v)};
  if(typeof v==='function')return {$function:v.name};
  if(v instanceof Error)return {$error:{name:v.name,message:v.message}};
  if(Array.isArray(v))return v.map(walk);
  if(v instanceof Map)return {$map:[...v].map(([k,value])=>[walk(k),walk(value)])};
  if(v instanceof Set)return {$set:[...v].map(walk)};
  if(v&&typeof v==='object'){let id=ids.get(v);if(id===undefined){id=++serial;ids.set(v,id)}if(expanded.has(v))return {$ref:id};expanded.add(v);return {$id:id,$object:Object.entries(v).map(([k,value])=>[k,walk(value)])}}
  return v;
 }
 return walk(value);
}
const physicalTables=['intervals','factories','factoryAddresses','blocks','logs','transactions','transactionReceipts','traces','rpcRequestResults'] as const;
async function physical(){
 if(!current.qb)throw Error('Original database has not been observed');
 const result:any={};
 for(const name of physicalTables){
  const rows=await current.qb.wrap((db:any)=>db.select().from(tables[name]));
  result[name]=rows.map((row:any)=>Object.fromEntries(Object.entries(row).map(([key,value])=>[key,typeof value==='bigint'?{$bigint:String(value)}:value])));
 }
 return result;
}
function progress(value:any){return {start:value.start,end:value.end,current:value.current,finalized:value.finalized}}
function callbacks(values:any[]){return values.map(v=>({name:v.name,type:v.type,filter:v.filter,abiItem:v.abiItem,chain:{id:v.chain.id,name:v.chain.name}}))}
function inputs(args:any){
 return {chain:{id:args.chain.id,name:args.chain.name,cacheRpcRequests:args.chain.cacheRpcRequests,ethGetLogsBlockRange:args.chain.ethGetLogsBlockRange},options:{command:args.common.options.command,factoryAddressCountThreshold:args.common.options.factoryAddressCountThreshold},eventCallbacks:callbacks(args.eventCallbacks),childAddresses:args.childAddresses,syncProgress:progress(args.syncProgress),cachedIntervals:args.cachedIntervals,from:args.from,to:args.to,limit:args.limit,isCatchup:args.isCatchup};
}
const originals={...historical}, originalSetup=setup.setupDatabaseServices, originalBuild=events.buildEvents;
beforeEach(ctx=>{
 const source=selected.find((t:any)=>t.full_name===ctx.task.name);
 if(!source)throw Error('Unknown runtime historical original '+ctx.task.name);
 current={...source,calls:[],targeted:selected.indexOf(source)>=11};ids=new WeakMap();serial=0;
 const observe=createRealtimeRPCObserver((request:any[])=>{if(!current?.active)return null;const record:any={request:mark(request)};current.active.providers.push(record);return record},(record:any,value:any,error:any)=>{if(!record)return;if(error)record.error=mark(error);else record.response=mark(value)});
 vi.spyOn(setup,'setupDatabaseServices').mockImplementation(async(...args:any[])=>{const result=await(originalSetup as any)(...args);current.qb=result.database.syncQB;return result});
 vi.spyOn(events,'buildEvents').mockImplementation((args:any)=>{
  const result=originalBuild(args);
  if(current.active)current.active.event_builds.push({args:mark(args),result:mark(result)});
  return result;
 });
 for(const operation of ['getLocalEventGenerator','getLocalInMemoryEventGenerator','getLocalSyncGenerator'] as const){
  vi.spyOn(historical,operation).mockImplementation((args:any)=>{
   const entry:any={operation,args:mark(inputs(args)),providers:[],steps:[],results:[],event_builds:[]};
   current.calls.push(entry);
   if(observe(args.rpc)!==args.rpc)throw Error('Original RPC identity changed');
   const iterator=(originals[operation] as any)(args);
   let initialized=false,finished=false;
   async function finish(){if(finished)return;finished=true;entry.after=mark(inputs(args));entry.physical_after=await physical();current.active=undefined}
   return {
    async next(...values:any[]){
     if(!initialized){initialized=true;entry.physical_before=await physical()}
     current.active=entry;
     entry.steps.push({operation:'next',args:mark(values)});
     try{const step=await iterator.next(...values);entry.results.push(mark(step));if(step.done)await finish();return step}
     catch(error){entry.error=mark(error);await finish();throw error}
    },
    async return(...values:any[]){entry.steps.push({operation:'return',args:mark(values)});try{return await iterator.return(...values)}finally{await finish()}},
    async throw(error:any){entry.steps.push({operation:'throw',args:mark(error)});try{return await iterator.throw(error)}finally{await finish()}},
    [Symbol.asyncIterator](){return this},
   };
  });
 }
 // The original final three calls retain their original input and assertions.
 vi.spyOn(historical,'getHistoricalEventsMultichain').mockImplementation((args:any)=>{
  const chainView=(chain:any)=>chain;
  const coordinatorInputs=()=>({
   chains:args.indexingBuild.chains.map(chainView),
   eventCallbacks:args.indexingBuild.eventCallbacks,
   crashRecoveryCheckpoint:args.crashRecoveryCheckpoint,
   options:{command:args.common.options.command,factoryAddressCountThreshold:args.common.options.factoryAddressCountThreshold,syncEventsQuerySize:args.common.options.syncEventsQuerySize},
   perChainSync:[...args.perChainSync].map(([chain,value]:any)=>({chain:chainView(chain),syncProgress:progress(value.syncProgress),childAddresses:value.childAddresses,cachedIntervals:value.cachedIntervals})),
  });
  const entry:any={operation:'getHistoricalEventsMultichain',args:mark(coordinatorInputs()),providers:[],steps:[],results:[],event_builds:[],clocks:[]};current.calls.push(entry);
  args.indexingBuild.rpcs.forEach((rpc:any,index:number)=>{
   const observeChain=createRealtimeRPCObserver((request:any[])=>{if(current?.active!==entry)return null;const record:any={chainId:args.indexingBuild.chains[index].id,request:mark(request)};entry.providers.push(record);return record},(record:any,value:any,error:any)=>{if(!record)return;if(error)record.error=mark(error);else record.response=mark(value)});
   if(observeChain(rpc)!==rpc)throw Error('Original RPC identity changed');
  });
  const iterator=originals.getHistoricalEventsMultichain(args);let initialized=false,finished=false;const originalNow=Date.now;Date.now=function observedCoordinatorClock(){const value=originalNow();if(new Error().stack?.split('\n')[2]?.includes('/runtime/historical.ts:'))entry.clocks.push(value);return value};
  async function finish(){if(finished)return;finished=true;entry.after=mark(coordinatorInputs());entry.physical_after=await physical();current.active=undefined;Date.now=originalNow}
  return {
   async next(...values:any[]){if(!initialized){initialized=true;entry.physical_before=await physical()};current.active=entry;entry.steps.push({operation:'next',args:mark(values)});try{const step=await iterator.next(...values);entry.results.push(mark(step));if(step.done)await finish();return step}catch(error){entry.error=mark(error);await finish();throw error}},
   async return(...values:any[]){entry.steps.push({operation:'return',args:mark(values)});try{return await(iterator.return as any)(...values)}finally{await finish()}},
   async throw(error:any){entry.steps.push({operation:'throw',args:mark(error)});try{return await iterator.throw(error)}finally{await finish()}},
   [Symbol.asyncIterator](){return this},
  };
 });
});
afterEach(()=>{if(!current||!current.calls.length){onTestFinished(()=>{throw Error('Original historical case had no captured public call')});if(!current)return};const {qb,active,...record}=current;completed.push(record);current=undefined});
afterAll(()=>{fs.writeFileSync(capture,JSON.stringify({source_credit:0,state:'Complete original historical.test.ts unchanged; final three coordinator cases captured without changing inputs or assertions. Outputs and database rows are targets, never replay seeds.',cases:completed},null,2)+'\n');if(completed.length!==14)throw Error('Incomplete original runtime historical file')});
