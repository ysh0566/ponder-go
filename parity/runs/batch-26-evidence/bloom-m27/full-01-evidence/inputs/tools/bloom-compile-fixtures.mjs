// Real native positive/run/negative proof for both original topic0=undefined errors.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import {spawnSync} from "node:child_process";
const root=path.resolve(import.meta.dirname,".."), read=file=>JSON.parse(fs.readFileSync(path.join(root,file),"utf8"));
const captures=read("testdata/bloom/captured-original-blooms.json"), ledger=read("parity/test-ledger.json");
const cases=captures.cases.filter(c=>c.type_error_directives.length);
if(cases.length!==2||cases.some(c=>c.type_error_directives.length!==1))throw Error("Expected both exact original topic0 undefined obligations");
const directory=path.join(root,".parity-build/bloom-types");fs.mkdirSync(directory,{recursive:true});
const report={counting:"Two original topic0=undefined errors. Public LogFilter.Topic0 accepts single HexTopic and is used by actual runtime bloom precomputation. Go omitted/zero-value fields and string conversion, plus dynamic address/topic/include fields, remain broader than TypeScript.",directives:[],inputs:[],summary:{source_error_directives:2,source_error_directives_passed:0,failed:0}};
const hashed=file=>({file,sha256:crypto.createHash("sha256").update(fs.readFileSync(path.join(root,file))).digest("hex")});
const run=(command,args)=>{const r=spawnSync(command,args,{cwd:root,encoding:"utf8",timeout:300000,maxBuffer:8*1024*1024});return{command:[command,...args],exit_code:r.status,error:r.error?.message??null,stdout:r.stdout??"",stderr:r.stderr??""};};
for(const c of cases){
 const original=ledger.tests.find(t=>t.id===c.id);if(!original||original.source_sha256!==c.source_sha256)throw Error("Stale original case");
 const input=c.calls.find(x=>x.operation==="getLogFilterBloom")?.args[0];if(!input?.topic0?.$undefined)throw Error("Expected original invalid undefined input");
 const address=Array.isArray(input.address)?`[]string{${input.address.map(JSON.stringify).join(",")}}`:input.address?.$undefined?"filter.Undefined":JSON.stringify(input.address);
 const common=`package main\nimport("ponder.local/ponder-go/filter";"ponder.local/ponder-go/bloom")\nfunc main(){\n f:=filter.LogFilter{ChainID:1,SourceID:"native-bloom-types",Address:${address},Topic0:filter.HexTopic("0x02c69be41d0b7e40352fc85be1cd65eb03d40ef8427a0ca4596b1ead9a00e9fc")}\n`;
 const positive=common+` compiled,err:=bloom.GetLogFilterBloom(f)\n if err!=nil{panic(err)}\n if len(compiled.Topics[0])!=1||f.Object()["topic0"]!=string(f.Topic0){panic("typed runtime projection")}\n}\n`;
 const negative=common+` f.Topic0=filter.Undefined\n _,_=bloom.GetLogFilterBloom(f)\n}\n`;
 const positiveFile=`bloom/testdata/compile/${c.id}-positive.go`,negativeFile=`bloom/testdata/compile/${c.id}-negative.go.txt`;
 for(const[file,text]of[[positiveFile,positive],[negativeFile,negative]]){fs.mkdirSync(path.dirname(path.join(root,file)),{recursive:true});fs.writeFileSync(path.join(root,file),text);}
 const negativeGo=path.join(directory,c.id+"-negative.go");fs.writeFileSync(negativeGo,negative);
 const positiveBinary=path.join(directory,c.id+"-positive");
 const buildPositive=run("go",["build","-o",positiveBinary,positiveFile]),executePositive=buildPositive.exit_code===0?run(positiveBinary,[]):null,buildNegative=run("go",["build","-o",path.join(directory,c.id+"-negative"),negativeGo]);
 const diagnostic=/cannot use filter\.Undefined.*as filter\.HexTopic/.test(buildNegative.stderr);
 const passed=buildPositive.exit_code===0&&!buildPositive.error&&executePositive?.exit_code===0&&!executePositive?.error&&Number.isInteger(buildNegative.exit_code)&&buildNegative.exit_code>0&&!buildNegative.error&&diagnostic;
 report.directives.push({source_id:c.id,source_sha256:c.source_sha256,source_error_directive:c.type_error_directives[0],status:passed?"passed":"failed",positive:hashed(positiveFile),negative:hashed(negativeFile),build_positive:buildPositive,execute_positive:executePositive,build_negative:buildNegative});
 if(passed)report.summary.source_error_directives_passed++;else report.summary.failed++;
}
report.inputs=["filter/log_filter.go","bloom/bloom.go","runtime/bloom.go","tools/bloom-compile-fixtures.mjs","testdata/bloom/captured-original-blooms.json"].map(hashed);
fs.writeFileSync(path.join(root,"parity/runs/bloom-compile-fixtures.json"),JSON.stringify(report,null,2)+"\n");console.log(report.summary);if(report.summary.failed)process.exitCode=1;
