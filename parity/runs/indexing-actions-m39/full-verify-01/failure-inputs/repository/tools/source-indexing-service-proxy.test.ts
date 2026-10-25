// Supplemental zero-credit reflection evidence. No replacement proxy implementation.
import fs from 'node:fs';
import util from 'node:util';
import {test,expect} from 'vitest';
import {createEventProxy} from '@/indexing/index.js';
import {defaultTransactionInclude} from '@/runtime/filter.js';
const destination=process.env.PONDER_INDEXING_PROXY_CAPTURE;
if(!destination)throw Error('Explicit proxy capture required');
const encode=(v:any):any=>v===undefined?{$undefined:true}:typeof v==='bigint'?{$bigint:String(v)}:Array.isArray(v)?v.map(encode):v&&typeof v==='object'?Object.fromEntries(Object.entries(v).map(([k,value])=>[k,encode(value)])):v;
test('actual source event proxy reflection and invalidation',()=>{
 const profile:any={count:123,resolved:true,block:new Set(),transaction:new Set(),transactionReceipt:new Set(),trace:new Set()};
 const profiles=new Map([['Event',profile]]),resets:any[]=[],reads:any[]=[],steps:any[]=[];
 const add=profile.transaction.add;profile.transaction.add=function(key:any){reads.push(key);return add.call(this,key)};
 let sticky:any;
 const handler:any={getRetryableError:()=>sticky,setRetryableError:(error:any)=>{sticky=error}};
 const facade=createEventProxy(profiles,'transaction',handler,name=>{resets.push(name);profile.count=0});
 const proxy=facade.proxy,first:any={gas:10n,maxFeePerGas:null,maxPriorityFeePerGas:undefined,extra:'untracked'};
 let underlying=first;facade.eventName='Event';facade.underlying=underlying;
 const run=(operation:string,fn:()=>any)=>{const n=reads.length,r=resets.length;let value,error;try{value=fn()}catch(e:any){error={name:e.name,message:e.message,meta:e.meta}}
  const step={operation,...(error?{error}:{value:encode(value)}),reads:reads.slice(n),resets:resets.slice(r),profile:{count:profile.count,resolved:profile.resolved,transaction:[...profile.transaction]},sticky:sticky?{name:sticky.name,message:sticky.message,meta:sticky.meta}:null,underlying:encode(underlying)};steps.push(step);return step};
 expect(run('get-present',()=>proxy.gas).value).toEqual({$bigint:'10'});
 expect(run('get-null',()=>proxy.maxFeePerGas).value).toBeNull();
 expect(run('get-present-undefined',()=>proxy.maxPriorityFeePerGas).value).toEqual({$undefined:true});
 expect(run('get-untracked',()=>Reflect.get(proxy,'extra')).reads).toEqual([]);
 run('has-present',()=>Reflect.has(proxy,'gas'));run('has-missing-included',()=>Reflect.has(proxy,'hash'));run('has-untracked',()=>Reflect.has(proxy,'extra'));
 run('ownKeys',()=>Reflect.ownKeys(proxy));run('objectKeys',()=>Object.keys(proxy));
 expect(run('set-present',()=>Reflect.set(proxy,'gas',12n)).value).toBe(true);
 expect(run('delete-present',()=>Reflect.deleteProperty(proxy,'gas')).value).toBe(true);
 expect(run('get-missing',()=>proxy.gas).error?.name).toBe('InvalidEventAccessError');
 const firstError=sticky;
 run('set-missing',()=>Reflect.set(proxy,'gas',15n));run('delete-missing',()=>Reflect.deleteProperty(proxy,'gas'));
 run('set-untracked',()=>Reflect.set(proxy,'newExtra',99));run('delete-untracked',()=>Reflect.deleteProperty(proxy,'extra'));
 run('inspect-missing',()=>util.inspect(proxy));
 const complete:any=Object.fromEntries(defaultTransactionInclude.map(key=>[key,null]));complete.gas=22n;
 underlying=complete;facade.underlying=underlying;
 expect(facade.proxy).toBe(proxy);
 run('identity-after-underlying-replacement',()=>({sameProxy:facade.proxy===proxy,value:proxy.gas,originalMissing:!('gas' in first),stickyPreserved:sticky!==undefined}));
 run('inspect-complete',()=>util.inspect(proxy));
 globalThis.DISABLE_EVENT_PROXY=true;
 try{run('disabled-get-missing',()=>Reflect.get(proxy,'notIncluded'));run('disabled-set-missing-included',()=>Reflect.set(proxy,'gasPrice',undefined));run('disabled-delete-included',()=>Reflect.deleteProperty(proxy,'gas'));run('disabled-has-missing-included',()=>Reflect.has(proxy,'gas'))}finally{globalThis.DISABLE_EVENT_PROXY=false}
 expect(firstError.name).toBe('InvalidEventAccessError');
 fs.writeFileSync(destination,JSON.stringify({source_credit:0,source:'packages/core/src/indexing/index.ts:createEventProxy',real_source:true,initial_underlying:encode({gas:10n,maxFeePerGas:null,maxPriorityFeePerGas:undefined,extra:'untracked'}),replacement_underlying:encode(Object.fromEntries(defaultTransactionInclude.map(key=>[key,key==='gas'?22n:null]))),default_include:defaultTransactionInclude,steps},null,2)+'\n');
});
