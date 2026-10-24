// Seven exact original obligations: real production typed constructor and
// consumers. Positive runtime evidence must come from the original live Anvil.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import {spawnSync} from "node:child_process";
const root=path.resolve(import.meta.dirname,".."),read=file=>JSON.parse(fs.readFileSync(path.join(root,file),"utf8"));
const ledger=read("parity/test-ledger.json"),previous=read("parity/runs/config-live-verification.json"),capture=read(previous.executions[0].capture.file);
const ids={network:"55a01f0bc082946abd42",bounds:"03426bf3f44186f9b02b"};
const destination=path.join(root,"config/testdata/compile/typed"),binaryDirectory=path.join(root,".parity-build/config-types/typed");fs.mkdirSync(destination,{recursive:true});fs.mkdirSync(binaryDirectory,{recursive:true});
function plain(value){
 if(Array.isArray(value))return value.map(plain);
 if(value&&typeof value==="object"){
  if(value.$object)return Object.fromEntries(value.$object.map(([key,value])=>[key,plain(value)]));
  throw Error("Unexpected special value in exact source type input");
 }
 return value;
}
const hash=file=>({file:path.relative(root,file),sha256:crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex")});
const run=args=>{const r=spawnSync("go",args,{cwd:root,encoding:"utf8"});return{command:["go",...args],exit_code:r.status,error:r.error?.message??null,stdout:r.stdout,stderr:r.stderr};};
const write=process.argv.includes("--write");
function fixture(file,text){if(write){if(!fs.existsSync(file)||fs.readFileSync(file,"utf8")!==text)fs.writeFileSync(file,text);}else if(!fs.existsSync(file)||fs.readFileSync(file,"utf8")!==text){throw Error("Prepared typed fixture differs; regenerate before native execution with --write: "+file);}}
function format(text){const r=spawnSync("gofmt",[],{cwd:root,encoding:"utf8",input:text});if(r.error||r.status!==0)throw Error(r.error?.message??r.stderr);return r.stdout;}
const reports=[];
for(const[kind,id]of Object.entries(ids)){
 const source=ledger.tests.find(t=>t.id===id),original=capture.cases.find(c=>c.id===id);
 if(!source||!original||source.test_source!==original.test_source||source.source_sha256!==original.source_sha256)throw Error("Actual original input is missing or stale");
 const captured=original.calls.find(c=>c.operation==="safeBuildIndexingFunctions");if(!captured)throw Error("Expected exact unsafe input reaching original safe consumer");
 const configWire=captured.args.$object.find(([key])=>key==="config")?.[1];if(!configWire)throw Error("Original config wire object missing");
 const input=plain(configWire),contract=input.contracts.a;
 if(kind==="network"&&(!contract.network||contract.chain!==undefined))throw Error("Original network typo changed");
 if(kind==="bounds"&&(contract.startBlock!=="16370000"||contract.endBlock!=="16370100"))throw Error("Original string bounds changed");
 const selection=kind==="network"?'config.SourceOnChains(config.SourceChainOverride{Name:"mainnet"},config.SourceChainOverride{Name:"optimism"})':'config.SourceOnChains(config.SourceChainOverride{Name:"mainnet"})';
 const abi=JSON.stringify(JSON.stringify(contract.abi));
 const common=`package main\nimport("context";"encoding/json";"fmt";"io";"os";"ponder.local/ponder-go/config")\nfunc main(){\n raw,err:=io.ReadAll(os.Stdin);if err!=nil{panic(err)}\n base,err:=config.ParseSourceConfiguration(raw);if err!=nil{panic(err)}\n _=context.Background;_=json.Valid\n`;
 const fields=field=>`${field} config.SourceChainSelection;ABI json.RawMessage;Address config.SourceOption[any];StartBlock config.SourceUndefinedBound;EndBlock config.SourceUndefinedBound;Filter config.SourceOption[any];IncludeTransactionReceipts config.SourceOption[bool];IncludeCallTraces config.SourceOption[bool]`;
 const networkInput=field=>`input:=struct{${fields(field)}}{${field}:${selection},ABI:json.RawMessage(${abi})}\n`;
 const boundsInput=(start,end)=>`input:=config.SourceTypedContract[${start},${end}]{Chain:${selection},ABI:json.RawMessage(${abi}),StartBlock:${start==="string"?'"16370000"':"int64(16370000)"},EndBlock:${end==="string"?'"16370100"':"int64(16370100)"}}\n`;
 const modes=kind==="network"?["create","build","safe"]:["start","end","build","safe"];
 for(const[directiveIndex,mode]of modes.entries()){
  const positiveInput=kind==="network"?networkInput("Chain"):boundsInput("int64","int64");
  const negativeInput=kind==="network"?networkInput("Network"):boundsInput(mode==="end"?"int64":"string",mode==="start"?"int64":"string");
  const generic=kind==="network"?'[config.SourceUndefinedBound,config.SourceUndefinedBound]':'';
  const build=()=>`built,err:=config.BuildTypedSourceConfig${generic}(typed,config.SourceBuildOptions{});if err!=nil{panic(err)}\n`;
  const safe=()=>`indexing,err:=config.SafeBuildTypedSourceIndexing${generic}(context.Background(),typed,built,[]config.SourceIndexingFunction{{Name:"a:Event0",Fn:func(){}}});if err!=nil{panic(err)}\n if len(indexing.EventCallbacks())!==EXPECTED{panic("actual typed chain/callback result changed")}\n fmt.Println("actual typed source pipeline passed")\n}\n`.replace("!==EXPECTED",`!=${kind==="network"?2:1}`);
  function body(input,negative){
   if(mode==="create"||mode==="start"||mode==="end")return common+input+`typed:=config.CreateTypedSourceConfiguration${generic}(base,"a",input)\n`+build()+safe();
   const type=kind==="network"?`struct{${fields(negative?"Network":"Chain")}}`:`config.SourceTypedContract[${negative?"string,string":"int64,int64"}]`;
   const begin=common+input+`typed:=config.SourceTypedConfiguration[${type}]{Base:base,Name:"a",Contract:input}\n`;
   if(mode==="build")return begin+build()+safe();
   return begin+`built,err:=config.BuildSourceConfig(base,config.SourceBuildOptions{});if err!=nil{panic(err)}\n`+safe();
  }
  const key=`${kind}-${mode}`,positiveFile=path.join(destination,key+"-positive.go"),negativeFile=path.join(destination,key+"-negative.go.txt"),negativeGo=path.join(binaryDirectory,key+"-negative.go");
  fixture(positiveFile,format(body(positiveInput,false)));fixture(negativeFile,body(negativeInput,true));fs.copyFileSync(negativeFile,negativeGo);
  const positiveBinary=path.join(binaryDirectory,key+"-positive"),buildPositive=run(["build","-o",positiveBinary,positiveFile]),buildNegative=run(["build","-o",path.join(binaryDirectory,key+"-negative"),negativeGo]);
  const diagnostic=kind==="network"?/does not satisfy config\.SourceTypedContractShape/.test(buildNegative.stderr):/string does not satisfy config\.SourceBlockBound/.test(buildNegative.stderr);
  const valid=buildPositive.exit_code===0&&!buildPositive.error&&buildNegative.exit_code>0&&!buildNegative.error&&diagnostic;
  reports.push({source_id:id,source_sha256:source.source_sha256,source_error_directive:source.type_error_directives[directiveIndex],source_directive_index:directiveIndex,mode:key,status:valid?"compiled_runtime_pending":"failed",positive:hash(positiveFile),negative:hash(negativeFile),positive_binary:positiveBinary,original_input:input,positive_control:kind==="network"?"Only Network field becomes Chain; original ordered chain overrides/ABI and absent options retained":"Only original bad numeric strings become numeric bounds; other bad field is corrected to isolate each field directive",build_positive:buildPositive,build_negative:buildNegative});
 }
}
const outputArgument=process.argv.slice(2).find(x=>!x.startsWith("--"));
const output=outputArgument?path.resolve(outputArgument):path.join(root,"parity/runs/config-types-m29/compile-fixtures.json");
const report={state:"Compiled controls only; runtime on actual original live Anvil and real runtime integration required before source credit",directives:reports,inputs:["config/typed_source.go","tools/config-typed-compile-fixtures.mjs"].map(f=>hash(path.join(root,f))),summary:{source_error_directives:7,compiled:reports.filter(r=>r.status==="compiled_runtime_pending").length,failed:reports.filter(r=>r.status==="failed").length,positive_runtime_verified:0}};
fs.writeFileSync(output,JSON.stringify(report,null,2)+"\n");console.log(report.summary);if(report.summary.failed)process.exitCode=1;
