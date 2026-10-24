// Derive closed app references from actual source chain definitions/ABI, and
// exercise the generated production constructor instead of toy missing map keys.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import {spawnSync} from "node:child_process";
import {createRequire} from "node:module";
const root=path.resolve(import.meta.dirname,".."),read=file=>JSON.parse(fs.readFileSync(path.join(root,file),"utf8"));
const write=process.argv.includes("--write"),outputArgument=process.argv.slice(2).find(x=>!x.startsWith("--"));
const output=outputArgument?path.resolve(outputArgument):path.join(root,"parity/runs/config-types-m29/literal-compile-fixtures.json");
const ledger=read("parity/test-ledger.json"),previous=read("parity/runs/config-live-verification.json"),capture=read(previous.executions[0].capture.file);
const directory=path.join(root,".parity-build/config-types/literals"),fixtures=path.join(root,"config/testdata/compile/literals");fs.mkdirSync(directory,{recursive:true});fs.mkdirSync(fixtures,{recursive:true});
const hash=file=>({file:path.relative(root,file),sha256:crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex")});
function run(command,args,options={}){const r=spawnSync(command,args,{cwd:root,encoding:"utf8",...options});return{command:[command,...args],exit_code:r.status,error:r.error?.message??null,stdout:r.stdout,stderr:r.stderr};}
function fixture(file,text){if(write){fs.mkdirSync(path.dirname(file),{recursive:true});if(!fs.existsSync(file)||fs.readFileSync(file,"utf8")!==text)fs.writeFileSync(file,text);}else if(!fs.existsSync(file)||fs.readFileSync(file,"utf8")!==text)throw Error("Prepare native literal fixture before execution with --write: "+file);}
function plain(value){if(Array.isArray(value))return value.map(plain);if(value&&typeof value==="object"){if(value.$object)return Object.fromEntries(value.$object.map(([k,v])=>[k,plain(v)]));throw Error("Unexpected config special marker");}return value;}
const rows=[['chain','af7907267becd4bf907c'],['event','b40ef8e8610beacf808b']].map(([kind,id])=>{const source=ledger.tests.find(t=>t.id===id),original=capture.cases.find(t=>t.id===id);if(!source||source.source_sha256!==original?.source_sha256||source.test_source!==original.test_source)throw Error("Stale original literal source");const call=original.calls.find(c=>c.operation==="safeBuildIndexingFunctions");const input=plain(call.args.$object.find(([k])=>k==="config")[1]);return {kind,id,source,input};});
const manifest={Package:"sourcebindings",Chains:Object.keys(rows[0].input.chains),ABI:rows[0].input.contracts.a.abi};
if(JSON.stringify(manifest.Chains)!==JSON.stringify(Object.keys(rows[1].input.chains))||JSON.stringify(manifest.ABI)!==JSON.stringify(rows[1].input.contracts.a.abi))throw Error("The exact source literal controls no longer share this actual chain/ABI definition");
const manifestFile=path.join(directory,"bindings.json");fs.writeFileSync(manifestFile,JSON.stringify(manifest,null,2)+"\n");
const cli=path.join(directory,"source-codegen"),buildCLI=run("go",["build","-o",cli,"./cmd/source-codegen"]);if(buildCLI.exit_code!==0)throw Error(buildCLI.stderr);
const generatedTemporary=path.join(directory,"bindings.go"),generation=run(cli,[manifestFile,generatedTemporary]);if(generation.exit_code!==0)throw Error(generation.stderr);
const generatedFile=path.join(root,"config/testdata/sourcebindings/bindings.go");fixture(generatedFile,fs.readFileSync(generatedTemporary,"utf8"));
const report={state:"Compile-only controls; actual original live native positive execution required before source credit",definition:manifest,generated:hash(generatedFile),generator:{build:buildCLI,execute:generation},directives:[],inputs:["config/source_bindings.go","config/typed_source.go","cmd/source-codegen/main.go","tools/config-literal-compile-fixtures.mjs"].map(f=>hash(path.join(root,f)))};
for(const row of rows){
 const {kind,id,source,input}=row;
 const common=`package main\nimport("context";"fmt";"io";"os";"strings";"ponder.local/ponder-go/config";bindings "ponder.local/ponder-go/config/testdata/sourcebindings")\nfunc main(){raw,err:=io.ReadAll(os.Stdin);if err!=nil{panic(err)};base,err:=config.ParseSourceConfiguration(raw);if err!=nil{panic(err)};input:=base.Value()["contracts"].(map[string]any)["a"].(map[string]any);_=strings.Contains\n`;
 function text(negative){
  const chain=kind==="chain"&&negative?"Mainnetz":"Mainnet",event=kind==="event"&&negative?"Event2":"Event0";
  const extras=kind==="chain"?`Address:config.SourceSome[any](input["address"]),`:`Filter:&bindings.Filter{Event:bindings.Events.${event},Args:input["filter"].(map[string]any)["args"]},`;
  const build=common+`typed,err:=bindings.Create(base,"a",bindings.Contract[config.SourceUndefinedBound,config.SourceUndefinedBound]{Chain:bindings.Chains.${chain},${extras}});if err!=nil{panic(err)}\nbuilt,err:=config.BuildTypedSourceConfig(typed,config.SourceBuildOptions{});if err!=nil{panic(err)}\nindexing,err:=config.SafeBuildTypedSourceIndexing(context.Background(),typed,built,[]config.SourceIndexingFunction{{Name:"a:Event0",Fn:func(){}}})\n`;
  return build+(kind==="chain"?`if err!=nil{panic(err)};if len(indexing.EventCallbacks())!=1{panic("actual generated chain reference lost")};fmt.Println("actual generated chain pipeline passed")\n}\n`:`_=indexing;if input["filter"].(map[string]any)["args"].(map[string]any)["arg"]!="0x"{panic("original short byte arg changed")};if err==nil||!strings.Contains(err.Error(),"bytes argument size must be 32"){panic(fmt.Sprintf("expected remaining runtime byte-size error, got %v",err))};fmt.Println("valid generated event reached expected runtime bytes32 validation")\n}\n`);
 }
 const positive=path.join(fixtures,kind+"-positive.go"),negative=path.join(fixtures,kind+"-negative.go.txt"),format=run("gofmt",[],{input:text(false)});if(format.exit_code!==0)throw Error(format.stderr);
 fixture(positive,format.stdout);fixture(negative,text(true));const negativeGo=path.join(directory,kind+"-negative.go");fs.copyFileSync(negative,negativeGo);
 const positiveBinary=path.join(directory,kind+"-positive"),buildPositive=run("go",["build","-o",positiveBinary,positive]),buildNegative=run("go",["build","-o",path.join(directory,kind+"-negative"),negativeGo]);
 const correctDiagnostic=kind==="chain"?/bindings\.Chains\.Mainnetz undefined/.test(buildNegative.stderr):/bindings\.Events\.Event2 undefined/.test(buildNegative.stderr);
 const valid=buildPositive.exit_code===0&&!buildPositive.error&&buildNegative.exit_code>0&&!buildNegative.error&&correctDiagnostic;
 report.directives.push({source_id:id,source_sha256:source.source_sha256,source_error_directive:source.type_error_directives[0],source_directive_index:0,mode:"literal-"+kind,status:valid?"compiled_runtime_pending":"failed",positive:hash(positive),negative:hash(negative),positive_binary:positiveBinary,original_input:input,positive_control:kind==="chain"?"Only unknown generated Mainnetz reference becomes actual Mainnet derived from original chain definitions":"Only Event2 becomes actual Event0 from original ABI; original args.arg=0x is preserved and must still produce runtime byte-size validation",build_positive:buildPositive,build_negative:buildNegative});
}
// Actual pinned viem is a primary dependency of the source buildTopics helper.
const require=createRequire(path.resolve(root,"../ponder-upstream/packages/core/package.json")),viem=require("viem");let actualSourceError;
try{viem.encodeEventTopics({abi:manifest.ABI,eventName:"Event0",args:{arg:"0x"}});}catch(error){actualSourceError={name:error.name,message:error.message};}
if(!actualSourceError)throw Error("Corrected Event0 original short arg unexpectedly no longer errors in pinned source viem");report.corrected_event_source_error=actualSourceError;
report.summary={source_error_directives:2,compiled:report.directives.filter(d=>d.status==="compiled_runtime_pending").length,failed:report.directives.filter(d=>d.status==="failed").length,positive_runtime_verified:0};
fs.writeFileSync(output,JSON.stringify(report,null,2)+"\n");console.log(report.summary);if(report.summary.failed)process.exitCode=1;
