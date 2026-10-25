// Capture-only observers around the unchanged original indexing service test file.
// Callback bodies, original inputs/assertions, and service/store/client code run intact.
import fs from 'node:fs';
import path from 'node:path';
import {gzipSync} from 'node:zlib';
import {beforeEach, afterEach, afterAll, vi} from 'vitest';
import {getTableName, getTableColumns} from 'drizzle-orm';
import * as indexing from '@/indexing/index.js';
import * as clients from '@/indexing/client.js';
import * as stores from '@/indexing-store/index.js';
import * as setup from '@/_test/setup.js';
import * as tables from '@/sync-store/schema.js';
import {createRealtimeRPCObserver} from './source-realtime-rpc-observer.mjs';
const root=process.env.PONDER_PORT_ROOT, capture=process.env.PONDER_INDEXING_SERVICE_CAPTURE;
if(!root||!capture)throw Error('Explicit indexing service capture required');
const ledger=JSON.parse(fs.readFileSync(path.join(root,'parity/test-ledger.json'),'utf8'));
const all=ledger.tests.filter((t:any)=>t.source_file==='packages/core/src/indexing/index.test.ts');
const selected=all.slice(0,15);
if(all.length!==23||selected.reduce((n:number,t:any)=>n+t.assertions.length,0)!==33||selected.reduce((n:number,t:any)=>n+t.type_error_directives.length,0)!==3)throw Error('Indexing service census changed');
let current:any, ids:WeakMap<object,number>, serial=0, observer:any;
const completed:any[]=[];
function identity(v:any){if(v===undefined||v===null)return v;if(typeof v!=='object'&&typeof v!=='function')return v;let id=ids.get(v);if(id===undefined){id=++serial;ids.set(v,id)}return id}
function mark(value:any):any{
 const expanded=new Set<object>();
 function walk(v:any):any{
  if(v===undefined)return {$undefined:true};
  if(typeof v==='bigint')return {$bigint:String(v)};
  if(typeof v==='number'&&!Number.isFinite(v))return {$number:String(v)};
  if(typeof v==='function')return {$function:v.name,$id:identity(v)};
  if(v instanceof Error)return {$error:{name:v.name,message:v.message,meta:(v as any).meta}};
  if(Array.isArray(v))return v.map(walk);
  if(v instanceof Map)return {$map:[...v].map(([k,value])=>[walk(k),walk(value)])};
  if(v instanceof Set)return {$set:[...v].map(walk)};
  if(v&&typeof v==='object'){const id=identity(v);if(expanded.has(v))return {$ref:id};expanded.add(v);return {$id:id,$object:Object.entries(v).map(([k,value])=>[k,walk(value)])}}
  return v;
 }
 return walk(value);
}
function schemaView(schema:any){return Object.fromEntries(Object.entries(schema).map(([key,table]:any)=>[key,{name:getTableName(table),columns:Object.fromEntries(Object.entries(getTableColumns(table)).map(([js,c]:any)=>[js,{name:c.name,dataType:c.dataType,columnType:c.columnType,notNull:c.notNull,primary:c.primary,hasDefault:c.hasDefault,sqlType:c.getSQLType()}]))}]))}
function callbackView(c:any){return {name:c.name,type:c.type,chain:c.chain&&{id:c.chain.id,name:c.chain.name},filter:c.filter,abiItem:c.abiItem,block:c.block,fn_source:Function.prototype.toString.call(c.fn)}}
function eventView(e:any){return {type:e.type,chain:{id:e.chain.id,name:e.chain.name},eventCallback:callbackView(e.eventCallback),checkpoint:e.checkpoint,event:e.event,block:e.block}}
function state(args:any){return {eventCount:args.eventCount,columnAccessPattern:args.columnAccessPattern,filters:args.indexingBuild.eventCallbacks.map((cs:any[])=>cs.map(c=>({name:c.name,filter:c.filter}))),hasError:args.common.metrics.hasError,retryableError:args.indexingErrorHandler.getRetryableError(),cacheEvent:args.indexingCache.event&&{type:args.indexingCache.event.type,checkpoint:args.indexingCache.event.checkpoint},clientEvent:args.client.event&&{type:args.client.event.type,checkpoint:args.client.event.checkpoint,block:args.client.event.block}}}
const physicalTables=['intervals','factories','factoryAddresses','blocks','logs','transactions','transactionReceipts','traces','rpcRequestResults'] as const;
async function physical(){const result:any={};if(!current.syncQB)return result;for(const name of physicalTables){const rows=await current.syncQB.wrap((db:any)=>db.select().from(tables[name]));result[name]=rows.map((row:any)=>Object.fromEntries(Object.entries(row).map(([key,value])=>[key,typeof value==='bigint'?{$bigint:String(value)}:value])))}return result}
function recordOperation(operation:string,args:any){const item={operation,args:mark(args)};if(current?.activeCallback)current.activeCallback.operations.push(item);else if(current?.active)current.active.operations.push(item);else current?.external_operations.push(item);return item as any}
const originalIndexing=indexing.createIndexing, originalClient=clients.createCachedViemClient, originalStore=stores.createIndexingStore, originalSetup=setup.setupDatabaseServices;
beforeEach(ctx=>{
 const source=all.find((t:any)=>t.full_name===ctx.task.name);if(!source)throw Error('Unknown indexing source '+ctx.task.name);
 current={...source,targeted:selected.includes(source),calls:[],external_operations:[],rpcs:[],schemas:[]};ids=new WeakMap();serial=0;
 observer=createRealtimeRPCObserver((requests:any[])=>{if(!current?.active)return null;const r:any={request:mark(requests)};current.active.providers.push(r);return r},(r:any,value:any,error:any)=>{if(!r)return;if(error)r.error=mark(error);else r.response=mark(value)});
 vi.spyOn(setup,'setupDatabaseServices').mockImplementation(async(...args:any[])=>{const result=await(originalSetup as any)(...args);if(current.targeted){current.syncQB=result.database.syncQB;current.userQB=result.database.userQB}return result});
 vi.spyOn(clients,'createCachedViemClient').mockImplementation((args:any)=>{
  const client=originalClient(args);if(!current.targeted)return client;
  current.rpcs.push(...args.indexingBuild.rpcs);const get=client.getClient.bind(client);const observed=new WeakSet();
  client.getClient=(chain:any)=>{const actual=get(chain);if(!observed.has(actual)){observed.add(actual);const balance=actual.getBalance;actual.getBalance=async function(...values:any[]){const op=recordOperation('client.getBalance',{chainId:chain.id,args:values});try{const v=await Reflect.apply(balance,this,values);op.result=mark(v);return v}catch(error){op.error=mark(error);throw error}}}return actual};return client;
 });
 vi.spyOn(stores,'createIndexingStore').mockImplementation((args:any)=>{
  const store=originalStore(args);if(!current.targeted)return store;
  current.schemas.push(schemaView(args.schemaBuild.schema));
  const insert=store.db.insert.bind(store.db);
  store.db.insert=((table:any)=>{const builder=insert(table);const values=builder.values.bind(builder);builder.values=((input:any)=>{const op=recordOperation('db.insert',{table:getTableName(table),values:input});const returned=values(input);op.builder_identity=identity(returned);return returned}) as any;return builder}) as any;
  const find=store.db.find.bind(store.db);store.db.find=(async(table:any,key:any)=>{const op=recordOperation('db.find',{table:getTableName(table),key});try{const v=await find(table,key);op.result=mark(v);return v}catch(error){op.error=mark(error);throw error}}) as any;
  return store;
 });
 vi.spyOn(indexing,'createIndexing').mockImplementation((args:any)=>{
  if(!current.targeted)return originalIndexing(args);
  const build=args.indexingBuild;
  const entry:any={operation:'createIndexing',args:mark({chains:build.chains.map((c:any)=>({id:c.id,name:c.name,cacheRpcRequests:c.cacheRpcRequests})),contracts:build.contracts,indexingFunctions:build.indexingFunctions,setupCallbacks:build.setupCallbacks.map((cs:any[])=>cs.map(callbackView)),eventCallbacks:build.eventCallbacks.map((cs:any[])=>cs.map(callbackView)),eventCount:args.eventCount,columnAccessPattern:args.columnAccessPattern}),methods:[],schemas:current.schemas};current.calls.push(entry);
  for(const [eventName,profile] of args.columnAccessPattern){for(const kind of ['block','transaction','trace','transactionReceipt']){const set=profile[kind];const add=set.add;Object.defineProperty(set,'add',{configurable:true,value:function(prop:any){if(current?.activeCallback)recordOperation('event.access',{eventName,kind,property:prop});return Reflect.apply(add,this,[prop])}})}}
  for(const callback of [...build.setupCallbacks.flat(),...build.eventCallbacks.flat()]){
   const fn=callback.fn;callback.fn=new Proxy(fn,{apply(target,thisArg,values){
    const arg=values[0], op:any={name:callback.name,fn_source:Function.prototype.toString.call((target as any).getMockImplementation?.()??target),argument_identity:identity(arg),context_identity:identity(arg.context),event_identity:arg.event===undefined?{$undefined:true}:identity(arg.event),context:mark({chain:arg.context.chain,contracts:arg.context.contracts}),client_identity:identity(arg.context.client),db_identity:identity(arg.context.db),operations:[]};
    // Never enumerate a historical event proxy: observation must not itself add reads.
    if(arg.event!==undefined)op.event_fields=Object.keys(arg.event).map(k=>({key:k,identity:identity(arg.event[k])}));
    current.active.callbacks.push(op);current.activeCallback=op;
    try{return Promise.resolve(Reflect.apply(target,thisArg,values)).then(v=>{op.result=mark(v);return v},error=>{op.error=mark(error);throw error}).finally(()=>{op.event_after_keys=arg.event===undefined?[]:Object.keys(arg.event);current.activeCallback=undefined})}catch(error){op.error=mark(error);current.activeCallback=undefined;throw error}
   }});
  }
  const duration=args.common.metrics.ponder_indexing_function_duration.observe.bind(args.common.metrics.ponder_indexing_function_duration);
  args.common.metrics.ponder_indexing_function_duration.observe=(...values:any[])=>{if(current?.active)current.active.metrics.push({operation:'duration',labels:mark(values[0]),duration:values[1]});return duration(...values)};
  const increment=args.common.metrics.incrementIndexingCompletedEvents.bind(args.common.metrics);args.common.metrics.incrementIndexingCompletedEvents=(...values:any[])=>{if(current?.active)current.active.metrics.push({operation:'completed',args:mark(values)});return increment(...values)};
  const service=originalIndexing(args);entry.result_defined=service!==undefined;
  for(const name of ['processSetupEvents','processRealtimeEvents','processHistoricalEvents'] as const){const original=service[name].bind(service);(service as any)[name]=async(...values:any[])=>{
   const invocation:any={operation:name,args:mark(name==='processSetupEvents'?[]:{events:values[0].events.map(eventView)}),before:mark(state(args)),providers:[],callbacks:[],operations:[],metrics:[],updates:[]};entry.methods.push(invocation);current.active=invocation;
   for(const rpc of current.rpcs)if(observer(rpc)!==rpc)throw Error('RPC identity changed');
   invocation.physical_before=await physical();
   if(name==='processHistoricalEvents'){const update=values[0].updateIndexingSeconds;values[0]={...values[0],updateIndexingSeconds:(event:any,chain:any)=>{invocation.updates.push({event_identity:identity(event),chainId:chain.id,checkpoint:event.checkpoint});return update(event,chain)}}}
   try{const result=await(original as any)(...values);invocation.result=mark(result);return result}catch(error){invocation.error=mark(error);throw error}finally{invocation.after=mark(state(args));invocation.physical_after=await physical();invocation.event_inputs_after=name==='processSetupEvents'?[]:mark(values[0].events.map(eventView));current.active=undefined;current.activeCallback=undefined}
  }}
  return service;
 });
});
afterEach(()=>{const {syncQB,userQB,active,activeCallback,rpcs,...record}=current;completed.push(record);current=undefined});
afterAll(()=>{fs.writeFileSync(capture,gzipSync(JSON.stringify({source_credit:0,state:'Unchanged original indexing/index.test.ts. Original callbacks execute intact; no historical event getter is read by capture; profile Set.add observes actual access. Outputs/rows are checks, not execution seeds.',cases:completed},null,2)+'\n'));if(completed.length!==23||completed.filter(c=>c.targeted).length!==15)throw Error('Incomplete indexing original capture')});
