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
const root=process.env.PONDER_PORT_ROOT, capture=process.env.PONDER_RUNTIME_HISTORICAL_CAPTURE;
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
 current={...source,calls:[],targeted:selected.indexOf(source)<11};ids=new WeakMap();serial=0;
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
 // The final three original cases stay unimplemented. Capture their unchanged
 // execution without pretending lexical internal calls are independently ported.
 vi.spyOn(historical,'getHistoricalEventsMultichain').mockImplementation((args:any)=>{
  const entry:any={operation:'getHistoricalEventsMultichain',unimplemented:true,results:[]};current.calls.push(entry);
  const iterator=originals.getHistoricalEventsMultichain(args);
  return(async function*(){for await(const group of iterator){entry.results.push(mark(group));yield group}})();
 });
});
afterEach(()=>{if(!current||!current.calls.length){onTestFinished(()=>{throw Error('Original historical case had no captured public call')});if(!current)return};const {qb,active,...record}=current;completed.push(record);current=undefined});
afterAll(()=>{fs.writeFileSync(capture,JSON.stringify({source_credit:0,state:'Complete original historical.test.ts unchanged, capture-only observers; outputs and database rows are verification targets, not native input seeds.',cases:completed},null,2)+'\n');if(completed.length!==14)throw Error('Incomplete original runtime historical file')});
