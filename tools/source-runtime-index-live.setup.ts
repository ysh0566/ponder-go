import fs from 'node:fs';
import path from 'node:path';
import {beforeEach, afterEach, afterAll, vi} from 'vitest';
import * as runtime from '@/runtime/index.js';
import * as historical from '@/runtime/historical.js';
import * as events from '@/runtime/events.js';
import * as stores from '@/sync-store/index.js';
import {createRealtimeRPCObserver} from './source-realtime-rpc-observer.mjs';
const root=process.env.PONDER_PORT_ROOT, capture=process.env.PONDER_RUNTIME_INDEX_CAPTURE;
if(!root||!capture)throw Error('Explicit runtime index capture required');
const ledger=JSON.parse(fs.readFileSync(path.join(root,'parity/test-ledger.json'),'utf8'));
const selected=ledger.tests.filter((t:any)=>t.source_file==='packages/core/src/runtime/index.test.ts');
if(selected.length!==15||selected.reduce((n:number,t:any)=>n+t.assertions.length,0)!==30||selected.reduce((n:number,t:any)=>n+t.type_error_directives.length,0)!==6)throw Error('Runtime index census changed');
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
const originals={...runtime}, originalMerge=historical.mergeAsyncGeneratorsWithEventOrder, originalStore=stores.createSyncStore, originalLog=events.syncLogToInternal;
beforeEach(ctx=>{
 const source=selected.find((t:any)=>t.full_name===ctx.task.name);
 if(!source)throw Error('Unmapped runtime index original '+ctx.task.name);
 current={...source,calls:[]};ids=new WeakMap();serial=0;
 for(const operation of ['getCachedBlock','getRequiredIntervals','getRequiredIntervalsWithFilters'] as const){
  vi.spyOn(runtime,operation).mockImplementation((args:any)=>{
   const entry:any={operation,args:mark(args)};current.calls.push(entry);
   try{const result=(originals[operation] as any)(args);entry.result=mark(result);return result}finally{entry.after=mark(args)}
  });
 }
 vi.spyOn(runtime,'getLocalSyncProgress').mockImplementation(async(args:any)=>{
  const input={chainID:args.chain.id,filters:args.filters,finalizedBlock:args.finalizedBlock,cachedIntervals:args.cachedIntervals};
  const entry:any={operation:'getLocalSyncProgress',args:mark(input),providers:[]};current.calls.push(entry);
  const observe=createRealtimeRPCObserver((request:any[])=>{const record:any={request:mark(request)};entry.providers.push(record);return record},(record:any,value:any,error:any)=>{if(error)record.error=mark(error);else record.response=mark(value)});
  const ref=args.rpc;if(observe(ref)!==ref)throw Error('Progress RPC identity changed');
  const result=await originals.getLocalSyncProgress(args);
  entry.result=mark({start:result.start,end:result.end,current:result.current,finalized:result.finalized});entry.after=mark(input);
  return result;
 });
 vi.spyOn(runtime,'getChildAddresses').mockImplementation(async(args:any)=>{
  const input={cacheRpcRequests:args.chain.cacheRpcRequests,filters:args.filters};const entry:any={operation:'getChildAddresses',args:mark(input)};current.calls.push(entry);
  const result=await originals.getChildAddresses(args);entry.result=mark(result);entry.after=mark(input);return result;
 });
 vi.spyOn(historical,'mergeAsyncGeneratorsWithEventOrder').mockImplementation((generators:any)=>{
  const entry:any={operation:'mergeAsyncGeneratorsWithEventOrder',generators:generators.map(()=>[]),steps:[],results:[]};current.calls.push(entry);
  const observed=generators.map((iterator:any,index:number)=>({
   next(...args:any[]){entry.steps.push({index,operation:'next',args:mark(args)});return iterator.next(...args).then((step:any)=>{entry.generators[index].push(mark(step));return step})},
   return(...args:any[]){entry.steps.push({index,operation:'return',args:mark(args)});return iterator.return(...args)},
   [Symbol.asyncIterator](){return this}
  }));
  const merged=originalMerge(observed);
  return(async function*(){for await(const group of merged){entry.results.push(mark(group));yield group}})();
 });
 vi.spyOn(stores,'createSyncStore').mockImplementation((args:any)=>{
  const store=originalStore(args);
  for(const method of ['insertBlocks','insertTransactions','insertLogs','insertChildAddresses','getEventData'] as const){
   const original=(store as any)[method].bind(store);
   (store as any)[method]=async(...values:any[])=>{
    const entry:any={operation:'store.'+method,args:mark(values)};current.calls.push(entry);
    const result=await original(...values);entry.result=mark(result);entry.after=mark(values);return result;
   };
  }
  return store;
 });
 vi.spyOn(events,'syncLogToInternal').mockImplementation((args:any)=>{const entry:any={operation:'syncLogToInternal',args:mark(args)};current.calls.push(entry);const result=originalLog(args);entry.result=mark(result);entry.after=mark(args);return result});
});
afterEach(()=>{if(!current||current.calls.length===0)throw Error('Original runtime index had no captured calls');completed.push(current);current=undefined});
afterAll(()=>{fs.writeFileSync(capture,JSON.stringify({source_credit:0,state:'Complete original runtime/index test bodies and assertions with capture-only observers. Independent native replay and complete acceptance pending.',cases:completed},null,2)+'\n');if(completed.length!==15)throw Error('Incomplete runtime index file')});
