// Supplementary vectors call actual pinned source exports; no source-test credit.
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import crypto from "node:crypto";
import {createRequire} from "node:module";
const root=path.resolve(import.meta.dirname,".."),upstream=path.resolve(root,"../ponder-upstream"),require=createRequire(path.join(upstream,"packages/core/package.json"));
const ts=require("typescript"),viem=require("viem"),sources=[];
function load(file,deps={},names){
 let text=fs.readFileSync(path.join(upstream,file),"utf8");sources.push({file,sha256:crypto.createHash("sha256").update(text).digest("hex")});
 if(names){const sf=ts.createSourceFile(file,text,99,true);text=sf.statements.filter(n=>ts.isVariableStatement(n)&&n.declarationList.declarations.some(d=>names.includes(d.name.getText(sf)))).map(n=>n.getText(sf)).join("\n");}
 const exports={};vm.runInNewContext(ts.transpileModule(text,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText,{exports,Uint8Array,require:name=>{if(!(name in deps))throw Error("Unexpected dependency "+name);return deps[name];}},{filename:file,timeout:10000});return exports;
}
const filters=load("packages/core/src/runtime/filter.ts",{},["isAddressFactory"]);
const source=load("packages/core/src/sync-realtime/bloom.ts",{viem,"@/runtime/filter.js":filters});
function mark(value){if(value===undefined)return{$undefined:true};if(value instanceof Uint8Array)return{$uint8Array:Array.from(value)};if(Array.isArray(value))return Array.from(value,mark);if(value&&typeof value==="object")return Object.fromEntries(Object.keys(value).sort().map(key=>[key,mark(value[key])]));return value;}
const cases=[];
function add(name,operation,args){const before=mark(args);try{const result=source[operation](...args);cases.push({name,operation,args:before,args_after:mark(args),result:mark(result)});}catch(error){cases.push({name,operation,args:before,args_after:mark(args),error:{name:error.name,message:error.message}});}}
const hex=(name,bytes)=>"0x"+crypto.createHash("sha256").update(name).digest("hex").slice(0,bytes*2);
const a=hex("a",20),b=hex("b",20),topic=hex("topic",32),other=hex("other",32);
function bloomContaining(values){const bloom=new Uint8Array(256);for(const value of values){const bits=source.getBloomBits(value);for(let i=0;i<3;i++)bloom[bits["byte"+i]]|=bits["mask"+i];}return bloom;}
const all=new Uint8Array(256).fill(255),empty=new Uint8Array(256),contains=bloomContaining([a,topic]),unrelated=bloomContaining([b,other]);
for(let i=0;i<32;i++){
 const value=hex("value"+i,i%2?20:32),bits=source.getBloomBits(value),matched=bloomContaining([value]);
 add("hash/"+i,"getBloomBits",[value]);
 for(const[label,bloom]of[["present",matched],["empty",empty],["unrelated",unrelated]]){
  add(`membership/${i}/${label}`,"isInBloom",[bloom,value]);add(`bits/${i}/${label}`,"isBloomBitsInBloom",[bloom,bits]);
 }
 add("short/"+i,"isBloomBitsInBloom",[matched.slice(0,i*7),bits]);
}
for(const value of ["0x","0x0","0xabc","0xABC","hello","0Xab"]){add("hash/runtime-adapter/"+value,"getBloomBits",[value]);}
const factory={type:"log",id:"supplemental",chainId:1,sourceId:"factory",address:a,eventSelector:topic,childAddressLocation:"topic1",fromBlock:undefined,toBlock:undefined};
for(const [addressIndex,address]of [undefined,null,[],a,[a],[a,b],factory].entries()){
 for(const [topicIndex,topic0]of [topic,other,undefined,null,[],[topic,other]].entries()){
  const filter={type:"log",chainId:1,sourceId:"supplemental",address,topic0,topic1:null,topic2:undefined,topic3:null,fromBlock:undefined,toBlock:undefined,hasTransactionReceipt:false,include:[]};
  add(`log/${addressIndex}/${topicIndex}`,"getLogFilterBloom",[filter]);const compiled=source.getLogFilterBloom(filter);
  for(const[label,bloom]of[["contains",contains],["unrelated",unrelated],["empty",empty],["all",all]])add(`log/${addressIndex}/${topicIndex}/${label}`,"isFilterInBloom",[{bloom,filter:compiled}]);
 }
}
for(const[addressIndex,address]of[undefined,[],a,[a],[a,b]].entries()){
 const input={...factory,address};add("factory/"+addressIndex,"getFactoryBloom",[input]);const compiled=source.getFactoryBloom(input);
 for(const[label,bloom]of[["contains",contains],["unrelated",unrelated],["empty",empty],["all",all]])add(`factory/${addressIndex}/${label}`,"isFactoryInBloom",[{bloom,factory:compiled}]);
}
const output={baseline:"17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334",counting:"Supplementary actual-source vectors; zero exact source declaration credit. Explicit dynamic adapter cases do not claim valid typed topic0 inputs.",sources,cases};
fs.mkdirSync(path.join(root,"testdata/bloom"),{recursive:true});fs.writeFileSync(path.join(root,"testdata/bloom/differential.json"),JSON.stringify(output,null,2)+"\n");console.log({vectors:cases.length,errors:cases.filter(c=>c.error).length});
