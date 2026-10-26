// Evidence-only helpers. Live coordinator arguments are private scratch, never
// inserted in reports, including when a test fails before its awaited cleanup.
import assert from 'node:assert/strict';
import {InvalidEventAccessError} from '@/internal/errors.js';
export function encodeFiniteEvidence(value:any):any {
 const ancestors=new WeakMap<object,string>();
 const encode=(v:any,path:string):any=>{
  if(v===undefined)return {$undefined:true};
  if(typeof v==='bigint')return {$bigint:String(v)};
  if(typeof v==='function')return {$function:v.name};
  if(v===null||typeof v!=='object')return v;
  if(ancestors.has(v))return {$cycleReference:ancestors.get(v)};
  ancestors.set(v,path);
  try{
   if(v instanceof Error){
    const properties=Object.fromEntries(Object.getOwnPropertyNames(v).map(key=>[key,encode(v[key],path+'.'+key)]));
    return {...properties,name:v.name,message:v.message,stack:encode(v.stack,path+'.stack'),constructorName:Object.getPrototypeOf(v)?.constructor?.name,status:encode(v.status,path+'.status'),code:encode(v.code,path+'.code')};
   }
   if(v instanceof Map)return [...v].map(([k,x],i)=>[encode(k,path+'['+i+'][0]'),encode(x,path+'['+i+'][1]')]);
   if(v instanceof Set)return [...v].map((x,i)=>encode(x,path+'['+i+']'));
   if(Array.isArray(v))return v.map((x,i)=>encode(x,path+'['+i+']'));
   return Object.fromEntries(Object.entries(v).map(([k,x])=>[k,encode(x,path+'.'+k)]));
  }finally{ancestors.delete(v)}
 };
 return encode(value,'$');
}
export function createFiniteIndexingObservation(run:any){
 let liveArgs:any;
 return {
  capture(args:any){liveArgs=args;},
  snapshot(){if(liveArgs){run.eventCount=encodeFiniteEvidence(liveArgs.eventCount);run.columnAccessPattern=encodeFiniteEvidence(liveArgs.columnAccessPattern);}},
  release(){liveArgs=undefined;},
 };
}
export function observeCaptureFailureSafety(common:any){
 const error=new InvalidEventAccessError('block.gasUsed','supplemental capture failure control');
 (error as any).status=418;(error as any).code='M43_CAPTURE_CONTROL';(error as any).cause=new Error('original supplemental cause');(error as any).cause.cause=error;
 const originalStack=error.stack;
 const run:any={timeline:[{kind:'supplemental-failure-control',stage:'handler',status:'failed'}],error:encodeFiniteEvidence(error),error_is_invalid:error instanceof InvalidEventAccessError};
 const scratch={common,eventCount:{'Blocks:block':188},columnAccessPattern:new Map([['Blocks:block',{resolved:true,count:188}]])};
 assert.throws(()=>JSON.stringify(scratch),/circular/i);
 const observation=createFiniteIndexingObservation(run);observation.capture(scratch);
 // Simulate failure before ordinary per-run completion. Snapshot remains safe,
 // while deliberately restoring the former live-graph leak must be rejected.
 observation.snapshot();
 const fixture={source_credit:0,cases:[{name:'supplemental-failure-control',runs:[run]}]};
 const before=JSON.parse(JSON.stringify(fixture));
 run.snapshot_error=encodeFiniteEvidence(new Error('supplemental downstream snapshot failure'));observation.snapshot();
 const after=JSON.parse(JSON.stringify(fixture)),decoded=after.cases[0].runs[0];
 assert.deepEqual(decoded.error,before.cases[0].runs[0].error);assert.equal(decoded.snapshot_error.message,'supplemental downstream snapshot failure');
 assert.equal(decoded.error.name,error.name);assert.equal(decoded.error.constructorName,'InvalidEventAccessError');assert.equal(decoded.error.message,error.message);assert.equal(decoded.error.stack,originalStack);assert.equal(decoded.error.status,418);assert.equal(decoded.error.code,'M43_CAPTURE_CONTROL');assert.equal(decoded.error.key,'block.gasUsed');assert.equal(decoded.error.cause.message,'original supplemental cause');assert.deepEqual(decoded.error.cause.cause,{$cycleReference:'$'});assert.equal(decoded.error_is_invalid,true);assert.deepEqual(decoded.timeline,run.timeline);assert.deepEqual(decoded.eventCount,{'Blocks:block':188});assert.deepEqual(decoded.columnAccessPattern,[['Blocks:block',{resolved:true,count:188}]]);
 assert.throws(()=>JSON.stringify({source_credit:0,cases:[{runs:[{...run,indexingArgs:scratch}]}]}),/circular/i);
 observation.release();scratch.eventCount['Blocks:block']=999;observation.snapshot();assert.deepEqual(run.eventCount,{'Blocks:block':188});
 return {scope:'supplemental_observer_failure_serialization_control',source_credit:0,actual_registry_cycle_rejected:true,deliberate_scratch_leak_rejected:true,original_callback_failure_claim:false,observation:decoded};
}
