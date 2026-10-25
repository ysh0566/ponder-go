// Observe unchanged originals. No input, assertion, skip state, or implementation rewrite.
import fs from 'node:fs';
import path from 'node:path';
import {gzipSync} from 'node:zlib';
import {beforeEach,afterEach,afterAll,vi} from 'vitest';
import * as clients from '@/indexing/client.js';
import * as profiles from '@/indexing/profile.js';
import * as setup from '@/_test/setup.js';
import * as tables from '@/sync-store/schema.js';
import {createRealtimeRPCObserver} from './source-realtime-rpc-observer.mjs';
const root=process.env.PONDER_PORT_ROOT,capture=process.env.PONDER_INDEXING_PROFILE_CAPTURE;
if(!root||!capture)throw Error('Explicit profile capture required');
const all=JSON.parse(fs.readFileSync(path.join(root,'parity/test-ledger.json'),'utf8')).tests.filter((t:any)=>['packages/core/src/indexing/profile.test.ts','packages/core/src/indexing/client.test.ts'].includes(t.source_file));
const targets=['ff30deb8b9aca842519b','4ace7b36bca44245e4b7'];
if(all.length!==24||all.filter((t:any)=>t.source_file.endsWith('/profile.test.ts')).length!==10)throw Error('Pinned census changed');
const encode=(v:any):any=>v===undefined?{$undefined:true}:typeof v==='bigint'?{$bigint:String(v)}:v instanceof Error?{$error:{name:v.name,message:v.message}}:typeof v==='function'?{$function:true}:Array.isArray(v)?v.map(encode):v&&typeof v==='object'?Object.fromEntries(Object.entries(v).map(([k,value])=>[k,encode(value)])):v;
const eventInput=(e:any)=>encode({type:e.type,chain:{id:e.chain.id,name:e.chain.name},...(e.type==='setup'?{block:e.block,setupCallback:{name:e.setupCallback?.name}}:{eventCallback:{name:e.eventCallback?.name},event:e.event})});
const completed:any[]=[];let current:any,observer:any;
const originalClient=clients.createCachedViemClient,originalSetup=setup.setupDatabaseServices,originalRecord=profiles.recordProfilePattern,originalRecover=profiles.recoverProfilePattern;
beforeEach(ctx=>{
 const source=all.find((t:any)=>t.full_name===ctx.task.name);if(!source)throw Error('Unknown original '+ctx.task.name);
 current={...source,targeted:source.source_file.endsWith('/profile.test.ts')||targets.includes(source.id),clients:[],operations:[],profile_ops:[],store:[],providers:[],timeline:[],pending:[],profileObjects:[]};
 observer=createRealtimeRPCObserver((requests:any[])=>{if(!current?.targeted||!current.active)return null;const r:any={request:encode(requests[0]),operation_index:current.operations.indexOf(current.active)};current.providers.push(r);return r},(r:any,result:any,error:any)=>{if(r){if(error)r.error=encode(error);else r.response=encode(result)}});
 vi.spyOn(profiles,'recordProfilePattern').mockImplementation((input:any)=>{
  if(!current.targeted)return originalRecord(input);
  const op:any={operation:'recordProfilePattern',event:eventInput(input.event),args:encode(input.args),hints:encode(input.hints),hint_indexes:input.hints.map((h:any)=>current.profileObjects.indexOf(h)),field_order:Object.fromEntries(['args','result'].map(k=>[k,input.event.event?.[k]&&!Array.isArray(input.event.event[k])?Object.keys(input.event.event[k]):[]]))};
  const index=current.profile_ops.length;current.profile_ops.push(op);const result=originalRecord(input);op.result=encode(result);op.result_hint_index=input.hints.indexOf(result);current.profileObjects[index]=result;return result;
 });
 vi.spyOn(profiles,'recoverProfilePattern').mockImplementation((pattern:any,event:any)=>{
  if(!current.targeted)return originalRecover(pattern,event);
  const op:any={operation:'recoverProfilePattern',pattern:encode(pattern),event:eventInput(event),pattern_record_index:current.profileObjects.findIndex((p:any)=>p?.pattern===pattern)};current.profile_ops.push(op);const result=originalRecover(pattern,event);op.result=encode(result);return result;
 });
 vi.spyOn(setup,'setupDatabaseServices').mockImplementation(async(...args:any[])=>{
  const result=await(originalSetup as any)(...args);if(!current.targeted)return result;
  current.qb=result.database.syncQB;
  for(const name of ['getRpcRequestResults','insertRpcRequestResults'] as const){
   const original=result.syncStore[name].bind(result.syncStore);
   (result.syncStore as any)[name]=function(...values:any[]){
    const op:any={operation:name,args:encode(values[0]),operation_index:current.operations.indexOf(current.active)};current.store.push(op);current.timeline.push(name+':invoke');
    const returned=(original as any)(...values);
    const completion=returned.then((v:any)=>{op.result=encode(v);return v},(e:any)=>{op.error=encode(e);throw e});
    if(name==='insertRpcRequestResults')current.pending.push(completion.catch(()=>{}));return completion;
   };
  }return result;
 });
 vi.spyOn(clients,'createCachedViemClient').mockImplementation((args:any)=>{
  const cached=originalClient(args);if(!current.targeted)return cached;
  current.clients.push({chains:encode(args.indexingBuild.chains.map((c:any)=>({id:c.id,name:c.name,cacheRpcRequests:c.cacheRpcRequests}))),eventCount:encode(args.eventCount)});
  for(const rpc of args.indexingBuild.rpcs)observer(rpc);
  let observedEvent:any;const descriptor=Object.getOwnPropertyDescriptor(cached,'event')!;
  Object.defineProperty(cached,'event',{...descriptor,set(value){observedEvent=value;current.operations.push({operation:'setEvent',event:eventInput(value),eventCount:encode(args.eventCount)});descriptor.set!.call(cached,value)}});
  const prefetch=cached.prefetch.bind(cached);cached.prefetch=async(input:any)=>{const op:any={operation:'prefetch',events:input.events.map(eventInput),eventCount:encode(args.eventCount)};current.operations.push(op);current.active=op;try{const result=await prefetch(input);op.result=encode(result);return result}finally{current.active=undefined}};
  const clear=cached.clear.bind(cached);cached.clear=()=>{current.operations.push({operation:'clear'});return clear()};
  const get=cached.getClient.bind(cached);cached.getClient=(chain:any)=>{const actual=get(chain),read=actual.readContract;actual.readContract=async function(input:any){const op:any={operation:'readContract',args:encode(input),chainId:chain.id,event:eventInput(observedEvent),eventCount:encode(args.eventCount)};current.operations.push(op);current.active=op;try{const result=await Reflect.apply(read,this,[input]);op.result=encode(result);return result}catch(e){op.error=encode(e);throw e}finally{current.active=undefined}};return actual};return cached;
 });
});
afterEach(async()=>{
 if(current.qb){await Promise.all(current.pending);current.physical_after=encode(await current.qb.wrap((db:any)=>db.select().from(tables.rpcRequestResults)));}
 const {qb,pending,active,profileObjects,...record}=current;completed.push(record);current=undefined;
});
afterAll(()=>{fs.writeFileSync(capture.replace('.json.gz',completed[0].source_file.endsWith('/profile.test.ts')?'.profile.json.gz':'.client.json.gz'),gzipSync(JSON.stringify({source_credit:0,format:'indexing-profile-source-v1',cases:completed},null,2)+'\n'));});
