import fs from 'node:fs';
import path from 'node:path';
import {AsyncLocalStorage} from 'node:async_hooks';
import {spawnSync} from 'node:child_process';
import {beforeEach,afterEach,afterAll,onTestFinished,vi} from 'vitest';
import * as realtime from '@/sync-realtime/index.js';
import {createRealtimeRPCObserver} from './source-realtime-rpc-observer.mjs';
const root=process.env.PONDER_PORT_ROOT,endpoint=process.env.PONDER_REALTIME_ENDPOINT,capture=process.env.PONDER_REALTIME_CAPTURE;
if(!root||!endpoint||!capture)throw Error('Explicit realtime native task context required');
const ledger=JSON.parse(fs.readFileSync(path.join(root,'parity/test-ledger.json'),'utf8')),selected=ledger.tests.filter((t:any)=>t.source_file==='packages/core/src/sync-realtime/index.test.ts');
if(selected.length!==30||selected.reduce((n:number,t:any)=>n+t.assertions.length,0)!==203)throw Error('Complete original realtime inventory changed');
let current:any,objects:WeakMap<object,number>,nextID=0;const completed:any[]=[],callScope=new AsyncLocalStorage<any>();
function id(value:object){let found=objects.get(value);if(found===undefined){found=++nextID;objects.set(value,found)};return found}
function mark(value:any):any{
 const expanded=new Set<object>();
 function walk(v:any):any{
  if(v===undefined)return{$undefined:true};if(typeof v==='bigint')return{$bigint:String(v)};if(typeof v==='number'&&!Number.isFinite(v))return{$number:String(v)};
  if(typeof v==='function')return{$function:{id:id(v),name:v.name}};
  if(v instanceof Error)return{$error:{name:v.name,message:v.message,...(Object.hasOwn(v,'code')?{code:(v as any).code}:{})}};
  if(Array.isArray(v))return v.map(walk);
  if(v instanceof Map)return{$map:[...v].map(([k,v])=>[walk(k),walk(v)])};
  if(v instanceof Set)return{$set:[...v].map(walk)};
  if(v&&typeof v==='object'){const n=id(v);if(expanded.has(v))return{$ref:n};expanded.add(v);return{$id:n,$object:Object.entries(v).map(([k,v])=>[k,walk(v)])}};
  return v
 };return walk(value)
}
function post(route:string,body:any){fs.appendFileSync(capture+'.requests.jsonl',JSON.stringify({route,body})+'\n');const program='let s="";for await(const c of process.stdin)s+=c;const r=await fetch(process.argv[1],{method:"POST",body:s});if(!r.ok)throw Error(await r.text());';const r=spawnSync(process.execPath,['--input-type=module','-e',program,endpoint+route],{input:JSON.stringify(body),encoding:'utf8'});if(r.error||r.status!==0)throw Error('Native realtime comparison failed: '+(r.error?.message??r.stderr))}
function send(operation:string,args:any,result:any,extra:any={}){const call={case_id:current.id,sequence:current.sequence++,operation,args,result,...extra};post('/call',call);current.calls.push(call)}
const createRealtime=realtime.createRealtimeSync;
beforeEach(ctx=>{
 const file=ctx.task.file.filepath.replaceAll('\\','/'),source=selected.find((t:any)=>file.endsWith(t.source_file.replace('packages/core/',''))&&t.full_name===ctx.task.name);if(!source)throw Error('Unknown original realtime case '+ctx.task.name);
 current={...source,sequence:0,calls:[],errors:[],instances:0,external_provider_inputs:[]};objects=new WeakMap();nextID=0;post('/start',{case_id:source.id});
 const observe=createRealtimeRPCObserver((args:any[])=>{const scope=callScope.getStore();if(!scope&&!current)throw Error('Realtime RPC escaped original test lifetime');const record:any={request:mark(args[0])};if(scope)scope.providers.push(record);else current.external_provider_inputs.push(record);return record},(record:any,value:any,error:any)=>{if(error)record.error={name:error.name,message:error.message,...(Object.hasOwn(error,'code')?{code:error.code}:{})};else record.response=mark(value)});
 const logged=new WeakSet();
 vi.spyOn(realtime,'createRealtimeSync').mockImplementation((args:any)=>{
  const instance=++current.instances,rpcRef=args.rpc,rpcID=id(rpcRef),childID=id(args.childAddresses),logger=args.common.logger;
  function observeRpc(){if(args.rpc!==rpcRef||observe(args.rpc)!==rpcRef)throw Error("Actual source RPC object identity changed")}
  const before=mark({instance,rpcID,childID,chain:{id:args.chain.id,name:args.chain.name,reorgWindow:args.chain.reorgWindow,pollingInterval:args.chain.pollingInterval},filters:args.eventCallbacks.map((c:any)=>c.filter),finalized:args.syncProgress.finalized,children:args.childAddresses});
  if(!logged.has(logger)){logged.add(logger);const warn=logger.warn;logger.warn=function(...values:any[]){const scope=callScope.getStore();if(scope){const fields={...values[0]};delete fields.duration;scope.warns.push(mark(fields))};return Reflect.apply(warn,this,values)}}
  const value=createRealtime(args),sync=value.sync.bind(value),onError=value.onError.bind(value);
  // Start observing at public-call entry so later mockRejectedValueOnce is
  // external provider input. Reinstalling a wrapper preserves the RPC object.
  value.sync=function(block:any,callback:any){
   const before=mark({instance,block,callback,shutdownKilled:args.common.shutdown.isKilled}),scope={providers:[],warns:[],consumerSteps:[]},original=sync(block,callback),iterator:any={
    next(...values:any[]){scope.consumerSteps.push({operation:"next",args:mark(values)});observeRpc();return callScope.run(scope,()=>original.next(...values as[]))},
    return(...values:any[]){scope.consumerSteps.push({operation:"return",args:mark(values)});observeRpc();return callScope.run(scope,()=>original.return(...values as[any]))},
    throw(...values:any[]){scope.consumerSteps.push({operation:"throw",args:mark(values)});observeRpc();return callScope.run(scope,()=>original.throw(...values as[any]))},
    [Symbol.asyncIterator](){return this},
   };
   return(async function*(){const results:any[]=[];let drained=false,failure:any;try{for await(const event of iterator){results.push(event);yield event};drained=true}catch(error){failure=error;throw error}finally{send('sync',before,mark(results),{providers:scope.providers,warnings:scope.warns,consumer_steps:scope.consumerSteps,drained,error:failure?mark(failure):undefined,after:mark({unfinalizedBlocks:value.unfinalizedBlocks,children:args.childAddresses,inputBlock:block})})}})()
  };
  value.onError=function(error:any){const before=mark({instance,error,shutdownKilled:args.common.shutdown.isKilled}),scope={providers:[],warns:[]};let failure:any;try{return callScope.run(scope,()=>onError(error))}catch(error){failure=error;throw error}finally{send('onError',before,mark(undefined),{providers:scope.providers,warnings:scope.warns,error:failure?mark(failure):undefined,after:mark({unfinalizedBlocks:value.unfinalizedBlocks,children:args.childAddresses})})}};
  if(args.rpc!==rpcRef)throw Error('Original constructor changed RPC reference');
  send('construct',before,mark(true),{rpc_identity_preserved:args.rpc===rpcRef,providers:[],warnings:[],after:mark({unfinalizedBlocks:value.unfinalizedBlocks,children:args.childAddresses})});return value
 });
});
// Native/source comparison failures are reported after original returned cleanup.
afterEach(()=>{try{if(!current||!current.calls.length)throw Error('Missing original realtime calls');post('/case',{...current,case_id:current.id});completed.push(current)}catch(error){const message=error instanceof Error?error.stack:String(error);onTestFinished(()=>{throw Error(message)})}finally{current=undefined}});
afterAll(()=>{if(completed.length!==30)throw Error('Incomplete complete original realtime file');fs.writeFileSync(capture,JSON.stringify({state:'Actual original public calls, provider inputs, Sets, Error fields, state and warning assertion observations. No native/full acceptance yet.',warning_projection:'Only nondeterministic clock duration removed from extra native comparisons; original assertions and source logging unchanged.',cases:completed},null,2)+'\n')});
