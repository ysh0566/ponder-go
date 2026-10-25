// Evaluate actual pinned source bloom predicates for native real-EVM headers.
// Supplementary integration oracle only; no original source-test credit.
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import crypto from "node:crypto";
import {createRequire} from "node:module";
const root=path.resolve(import.meta.dirname,".."),upstream=path.resolve(root,"../ponder-upstream");
const require=createRequire(path.join(upstream,"packages/core/package.json")),ts=require("typescript"),viem=require("viem"),sources=[];
function load(file,deps={},names){
 let text=fs.readFileSync(path.join(upstream,file),"utf8");sources.push({file,sha256:crypto.createHash("sha256").update(text).digest("hex")});
 if(names){const sf=ts.createSourceFile(file,text,99,true);text=sf.statements.filter(n=>ts.isVariableStatement(n)&&n.declarationList.declarations.some(d=>names.includes(d.name.getText(sf)))).map(n=>n.getText(sf)).join("\n");}
 const exports={};vm.runInNewContext(ts.transpileModule(text,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText,{exports,Uint8Array,require:name=>{if(!(name in deps))throw Error("Unexpected source dependency "+name);return deps[name];}},{filename:file,timeout:10000});return exports;
}
const filter=load("packages/core/src/runtime/filter.ts",{},["isAddressFactory"]),bloom=load("packages/core/src/sync-realtime/bloom.ts",{viem,"@/runtime/filter.js":filter});
const input=JSON.parse(fs.readFileSync(0,"utf8"));if(!Array.isArray(input.blooms)||input.blooms.length!==3)throw Error("Three actual EVM header blooms required");
const compiled=bloom.getLogFilterBloom(input.filter);const matches=input.blooms.map(value=>bloom.isFilterInBloom({bloom:viem.hexToBytes(value),filter:compiled}));
process.stdout.write(JSON.stringify({matches,sources,counting:"Actual pinned source functions on native real-EVM header inputs; supplementary, zero source-test credit"})+"\n");
