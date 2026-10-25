// Observe unchanged original tests and genuine client, store, and RPC implementations.
import fs from 'node:fs';
import path from 'node:path';
import {gzipSync} from 'node:zlib';
import {beforeEach,afterEach,afterAll,vi} from 'vitest';
import * as clients from '@/indexing/client.js';
import * as setup from '@/_test/setup.js';
import * as tables from '@/sync-store/schema.js';
import {createRealtimeRPCObserver} from './source-realtime-rpc-observer.mjs';
const root=process.env.PONDER_PORT_ROOT,capture=process.env.PONDER_INDEXING_ACTIONS_CAPTURE;
if(!root||!capture)throw Error('Explicit action capture required');
const all=JSON.parse(fs.readFileSync(path.join(root,'parity/test-ledger.json'),'utf8')).tests.filter((t:any)=>t.source_file==='packages/core/src/indexing/index.test.ts');
const selected=all.slice(15);
if(all.length!==23||selected.length!==8||selected.reduce((n:number,t:any)=>n+t.assertions.length,0)!==9||selected.some((t:any)=>t.type_error_directives.length))throw Error('Pinned action census changed');
const encode=(v:any):any=>v===undefined?{$undefined:true}:typeof v==='bigint'?{$bigint:String(v)}:v instanceof Error?{$error:{name:v.name,message:v.message}}:Array.isArray(v)?v.map(encode):v&&typeof v==='object'?Object.fromEntries(Object.entries(v).map(([k,value])=>[k,encode(value)])):v;
const completed:any[]=[];let current:any,observer:any;
const originalClient=clients.createCachedViemClient,originalSetup=setup.setupDatabaseServices;
beforeEach(ctx=>{
 const source=all.find((t:any)=>t.full_name===ctx.task.name);if(!source)throw Error('Unknown original '+ctx.task.name);
 current={...source,targeted:selected.includes(source),clients:[],actions:[],store:[],timeline:[],pending:[]};
 observer=createRealtimeRPCObserver((requests:any[])=>{if(!current?.active)return null;const r:any={request:encode(requests[0])};current.active.providers.push(r);return r},(r:any,result:any,error:any)=>{if(r){if(error)r.error=encode(error);else r.response=encode(result)}});
 vi.spyOn(setup,'setupDatabaseServices').mockImplementation(async(...args:any[])=>{
  const result=await(originalSetup as any)(...args);if(!current.targeted)return result;
  current.qb=result.database.syncQB;
  for(const name of ['getRpcRequestResults','insertRpcRequestResults'] as const){
   const original=result.syncStore[name].bind(result.syncStore);
   (result.syncStore as any)[name]=function(...values:any[]){
    const op:any={operation:name,args:encode(values[0])};current.store.push(op);current.timeline.push(name+':invoke');
    // Invoke directly, preserving all original synchronous validation/queue admission.
    const returned=(original as any)(...values);
    const completion=returned.then((v:any)=>{op.result=encode(v);return v},(e:any)=>{op.error=encode(e);throw e});
    if(name==='insertRpcRequestResults')current.pending.push(completion.catch(()=>{}));
    return completion;
   };
  }
  return result;
 });
 vi.spyOn(clients,'createCachedViemClient').mockImplementation((args:any)=>{
  const cached=originalClient(args);if(!current.targeted)return cached;
  current.clients.push({chains:encode(args.indexingBuild.chains.map((c:any)=>({id:c.id,name:c.name,cacheRpcRequests:c.cacheRpcRequests}))),eventCount:encode(args.eventCount)});
  let observedEvent:any;const eventDescriptor=Object.getOwnPropertyDescriptor(cached,'event')!;Object.defineProperty(cached,'event',{...eventDescriptor,set(value){observedEvent=value;eventDescriptor.set!.call(cached,value)}});
  const get=cached.getClient.bind(cached),seen=new WeakSet();
  cached.getClient=(chain:any)=>{const actual=get(chain);if(!seen.has(actual)){seen.add(actual);
   for(const name of ['getBalance','getCode','getStorageAt','readContract','multicall'] as const){
    const original=actual[name];(actual as any)[name]=async function(...values:any[]){
     const event:any=observedEvent;
     const op:any={operation:name,args:encode(values),chainId:chain.id,event:encode({type:event.type,block:event.type==='setup'?event.block:event.event.block.number}),providers:[]};current.actions.push(op);current.active=op;
     for(const rpc of args.indexingBuild.rpcs)observer(rpc);
     current.timeline.push(name+':invoke');
     try{const result=await Reflect.apply(original,this,values);op.result=encode(result);current.timeline.push(name+':return');return result}catch(error){op.error=encode(error);throw error}finally{current.active=undefined}
    };
   }
  }return actual};return cached;
 });
});
afterEach(async()=>{
 if(current.targeted){await Promise.all(current.pending);current.physical_after=encode(await current.qb.wrap((db:any)=>db.select().from(tables.rpcRequestResults)));}
 const {qb,pending,active,...record}=current;completed.push(record);current=undefined;
});
afterAll(()=>{
 fs.writeFileSync(capture,gzipSync(JSON.stringify({source_credit:0,format:'indexing-actions-source-v1',state:'Whole unchanged original 23 tests; only final eight actions observed. Actual ABI inputs/RPC boundary/store operations and physical cache rows. Post-test quiescence awaits already-started cache writes for observation only.',cases:completed},null,2)+'\n'));
 if(completed.length!==23||completed.filter(c=>c.targeted).length!==8)throw Error('Incomplete original capture');
});
