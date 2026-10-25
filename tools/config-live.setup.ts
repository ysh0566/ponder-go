// The original file, hooks, helpers, providers and assertions run unchanged.
// Only exported calls are observed. Native results never replace TS results.
import fs from "node:fs";
import path from "node:path";
import {spawnSync} from "node:child_process";
import {beforeEach,afterEach,afterAll,vi} from "vitest";
import * as source from "@/build/config.js";
const root=path.resolve(import.meta.dirname,"..");
const ledger=JSON.parse(fs.readFileSync(path.join(root,"parity/test-ledger.json"),"utf8"));
const selected=ledger.tests.filter((t:any)=>t.source_file==="packages/core/src/build/config.test.ts");
if(selected.length!==40)throw Error("Expected 40 expanded original records");
const endpoint=process.env.PONDER_CONFIG_NATIVE_ENDPOINT;
if(!endpoint)throw Error("A live native builder endpoint is required");
const functions=new WeakMap<object,number>(),rpcs=new WeakMap<object,number>();let nextFunction=0,nextRPC=0;
function mark(value:any):any{
 if(value===undefined)return {$undefined:true};
 if(typeof value==="number"){
  if(Number.isNaN(value))return {$number:"NaN"};
  if(!Number.isFinite(value))return {$number:value>0?"+Infinity":"-Infinity"};
  if(Object.is(value,-0))return {$number:"-0"};
 }
 if(typeof value==="bigint")return {$bigint:String(value)};
 if(typeof value==="function"){if(!functions.has(value))functions.set(value,++nextFunction);return {$function:functions.get(value)};}
 if(Array.isArray(value))return value.map(mark);
 if(value instanceof Error)return {$object:[["name",value.name],["message",value.message],["stack",mark(value.stack)]]};
 if(value&&typeof value==="object"){
  if(typeof value.request==="function"&&typeof value.subscribe==="function"&&Array.isArray(value.hostnames)){
   if(!rpcs.has(value))rpcs.set(value,++nextRPC);return {$rpc:{id:rpcs.get(value),hostnames:value.hostnames}};
  }
  return {$object:Object.keys(value).map(key=>[key,mark(value[key])])};
 }
 return value;
}
function post(route:string,body:any){
 const program='const body=await new Promise(r=>{let s="";process.stdin.setEncoding("utf8");process.stdin.on("data",x=>s+=x);process.stdin.on("end",()=>r(s));});const response=await fetch(process.argv[1],{method:"POST",body});if(!response.ok)throw Error(await response.text());';
 const result=spawnSync(process.execPath,["--input-type=module","-e",program,endpoint+route],{input:JSON.stringify(body),encoding:"utf8"});
 if(result.error||result.status!==0)throw Error("Native endpoint transport failed: "+(result.error?.message??result.stderr));
}
const names=["buildConfig","safeBuildConfig","buildIndexingFunctions","safeBuildIndexingFunctions"] as const;
const originals=Object.fromEntries(names.map(name=>[name,source[name]]));
const completed:any[]=[];let current:any;
function expanded(t:any){return t.title.includes("%s")?t.title.replace("%s",t.parameter?.$special??String(t.parameter)):t.title;}
function argsValue(arg:any){return {config:arg.config,common:{options:{rpcMaxConcurrency:arg.common.options.rpcMaxConcurrency}},...(arg.configBuild?{configBuild:arg.configBuild}:{}),...(arg.indexingFunctions?{indexingFunctions:arg.indexingFunctions}:{})};}
beforeEach(ctx=>{
 const original=selected.find((t:any)=>expanded(t)===ctx.task.name);
 if(!original)throw Error("Unrecognized source case: "+ctx.task.name);
 current={...original,calls:[]};
 for(const name of names)vi.spyOn(source,name).mockImplementation(((arg:any)=>{
  const before=mark(argsValue(arg));
  const save=(value:any,error?:any)=>{
   const call={case_id:current.id,operation:name,args:before,args_after:mark(argsValue(arg)),...(error?{error:{name:error.name,message:error.message}}:{result:mark(value)})};
   post("/call",call);current.calls.push(call);
  };
  if(name.includes("Indexing"))return (async()=>{try{const value=await originals[name](arg);save(value);return value;}catch(error){save(undefined,error);throw error;}})();
  try{const value=originals[name](arg);save(value);return value;}catch(error){save(undefined,error);throw error;}
 }) as any);
});
afterEach(()=>{if(current){post("/case",current);completed.push(current);current=undefined;}});
afterAll(()=>{
 if(completed.length!==39)throw Error("Expected all 39 active source cases, got "+completed.length);
 const destination=process.env.PONDER_CONFIG_CAPTURE_OUTPUT;if(!destination)throw Error("Capture output required");
 fs.writeFileSync(destination,JSON.stringify({baseline:ledger.baseline,source_file:"packages/core/src/build/config.test.ts",original_skip:selected.filter((t:any)=>t.source_state!=="active"),cases:completed},null,2)+"\n");
});
