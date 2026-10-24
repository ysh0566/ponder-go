// Generate pinned metadata from the actual source ESM chain namespace.
// --write updates the checked-in asset; the default verifies exact contents.
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import crypto from "node:crypto";
import {createRequire} from "node:module";
import {pathToFileURL} from "node:url";
const root=path.resolve(import.meta.dirname,".."),upstream=path.resolve(root,"../ponder-upstream"),require=createRequire(path.join(upstream,"packages/core/package.json"));
const ts=require("typescript"),viemRoot=path.resolve(path.dirname(require.resolve("viem")),"..");
const chains=await import(pathToFileURL(path.join(viemRoot,"_esm/chains/index.js"))),utils=await import(pathToFileURL(path.join(viemRoot,"_esm/utils/index.js")));
const file="packages/core/src/utils/chains.ts",text=fs.readFileSync(path.join(upstream,file),"utf8"),exports={};
vm.runInNewContext(ts.transpileModule(text,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText,{exports,require:name=>{if(name==="viem/chains")return chains;if(name==="viem/utils")return utils;throw Error("Unexpected "+name);}},{filename:file,timeout:10000});
const functionIDs=new WeakMap();let nextFunctionID=1;
function mark(value,ancestors=new Set()){
 if(value===undefined)return{$undefined:true};if(typeof value==="bigint")return{$bigint:String(value)};
 if(typeof value==="function"){if(!functionIDs.has(value))functionIDs.set(value,nextFunctionID++);return{$function:functionIDs.get(value)};}
 if(typeof value==="number"){if(Number.isNaN(value))return{$number:"NaN"};if(!Number.isFinite(value))return{$number:value>0?"+Infinity":"-Infinity"};if(Object.is(value,-0))return{$number:"-0"};return value;}
 if(Array.isArray(value))return value.map(item=>mark(item,ancestors));
 if(value&&typeof value==="object"){
  if(ancestors.has(value))throw Error("Unexpected cyclic chain metadata");const next=new Set(ancestors);next.add(value);
  return{$object:Object.keys(value).map(key=>[key,mark(value[key],next)])};
 }
 return value;
}
const records=[],seen=new Set();
for(const[name,value]of Object.entries(exports.chains)){
 if(!value||!("id" in value)||seen.has(value.id))continue;
 seen.add(value.id);const descriptor=value.id===999?exports.hyperliquidEvm:value;
 records.push({id:descriptor.id,first_source_export:name,explicit_source_override:value.id===999,descriptor:mark(descriptor)});
}
if(!seen.has(999))records.push({id:999,first_source_export:null,explicit_source_override:true,descriptor:mark(exports.hyperliquidEvm)});
const destination=path.join(root,"config/chain_catalog.json");
const digest=file=>crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex");
const result={baseline:"17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334",state:"Pinned metadata for the source config builder. Functions remain opaque descriptors; viem formatter/serializer behavior is not ported. Metadata alone adds zero source-test credit",sources:[{file,sha256:digest(path.join(upstream,file))},{file:"pnpm-lock.yaml",sha256:digest(path.join(upstream,"pnpm-lock.yaml"))}],viem_version:JSON.parse(fs.readFileSync(path.join(viemRoot,"package.json"),"utf8")).version,ordering:"Actual ESM namespace Object.entries order; first id match, source999 override",opaque_function_scope:"catalog-specific opaque descriptors; native handlers retain separate real Go function identity",opaque_functions:nextFunctionID-1,chains:records};
const data=JSON.stringify(result,null,2)+"\n";
if(process.argv.includes("--write"))fs.writeFileSync(destination,data);
else if(fs.readFileSync(destination,"utf8")!==data)throw Error("Chain catalog differs from pinned source; review before explicit --write");console.log({destination,viem_version:result.viem_version,chains:records.length,opaque_functions:result.opaque_functions,hyperliquid_override:records.find(c=>c.id===999)?.explicit_source_override,source_credit:0});
