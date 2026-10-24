// Verify the original callback-union property error with the public native API.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { spawnSync } from "node:child_process";
const root = path.resolve(import.meta.dirname, "..");
const read = file => JSON.parse(fs.readFileSync(path.join(root, file), "utf8"));
const sourceID = "865433ec410487c54e12";
const ledger = read("parity/test-ledger.json"), original = ledger.tests.find(t => t.id === sourceID);
if (!original || original.type_error_directives.length !== 1) throw Error("Expected exact original callback-union type obligation");
const materialized = read("testdata/runtime/captured-original-events.json").cases.find(c => c.id === sourceID);
const item = materialized.calls[0].args[2][0].abiItem;
if (item.type !== "function" || item.name !== "transfer" || item.outputs.length !== 0) throw Error("Expected source shallow-copy no-output transfer callback");
// Obtain the unmodified original function item from the companion trace case.
const originalItem = read("testdata/runtime/captured-original-events.json").cases.find(c => c.title === "decodeEvents() trace").calls[0].args[2][0].abiItem;
const common = `package main\nimport("encoding/json";"ponder.local/ponder-go/abi";"ponder.local/ponder-go/events")\nfunc main(){\n var original abi.Function\n if err:=json.Unmarshal([]byte(${JSON.stringify(JSON.stringify(originalItem))}),&original);err!=nil{panic(err)}\n callbacks:=[]events.EventCallback{events.ContractCallback[abi.Function]{CallbackBase:events.CallbackBase{Name:"Erc20.transfer()",Filter:events.Object{"type":"trace"}},ABIItem:original}}\n`;
const positive = common + ` narrowed:=callbacks[0].(events.ContractCallback[abi.Function])\n copied:=narrowed.ABIItem\n copied.Outputs=[]abi.Parameter{}\n narrowed.ABIItem=copied\n callbacks[0]=narrowed\n if len(original.Outputs)!=1||len(callbacks[0].(events.ContractCallback[abi.Function]).ABIItem.Outputs)!=0{panic("copy modified shared original ABI")}\n projected:=events.CallbackObjects(callbacks)\n if len(projected)!=1||projected[0]["abiItem"].(abi.Function).Name!="transfer"||len(projected[0]["abiItem"].(abi.Function).Outputs)!=0{panic("typed callback projection changed ABI")}\n}\n`;
const negative = common + ` callbacks[0].ABIItem=original\n}\n`;
const positiveFile = "events/testdata/compile/callback-union-positive.go", negativeFile = "events/testdata/compile/callback-union-negative.go.txt";
for (const [file, content] of [[positiveFile, positive], [negativeFile, negative]]) { fs.mkdirSync(path.dirname(path.join(root,file)), {recursive:true}); fs.writeFileSync(path.join(root,file),content); }
const buildDir = path.join(root,".parity-build/event-union"); fs.mkdirSync(buildDir,{recursive:true});
const negativeGo = path.join(buildDir,"negative.go");fs.writeFileSync(negativeGo,negative);
const report = {counting:"One original callback-union property-access error directive, verified by real Go compilation. Positive narrows the public union and copies the ABI before replacement; no claim of full callback/filter/static TS type equivalence.",directives:[],inputs:[],summary:{source_error_directives:1,source_error_directives_passed:0,failed:0}};
function run(command,args){const r=spawnSync(command,args,{cwd:root,encoding:"utf8",timeout:300000,maxBuffer:8*1024*1024});return {command:[command,...args],exit_code:r.status,error:r.error?.message??null,stdout:r.stdout??"",stderr:r.stderr??""};}
const buildPositive=run("go",["build","-o",path.join(buildDir,"positive"),positiveFile]);
const executePositive=buildPositive.exit_code===0?run(path.join(buildDir,"positive"),[]):null;
const buildNegative=run("go",["build","-o",path.join(buildDir,"negative"),negativeGo]);
const passed=buildPositive.exit_code===0&&!buildPositive.error&&executePositive?.exit_code===0&&!executePositive?.error&&Number.isInteger(buildNegative.exit_code)&&buildNegative.exit_code>0&&!buildNegative.error&&/callbacks\[0\]\.ABIItem undefined/.test(buildNegative.stderr);
const hashed=file=>({file,sha256:crypto.createHash("sha256").update(fs.readFileSync(path.join(root,file))).digest("hex")});
report.directives.push({source_id:sourceID,source_sha256:original.source_sha256,source_error_directive:original.type_error_directives[0],status:passed?"passed":"failed",positive:hashed(positiveFile),negative:hashed(negativeFile),build_positive:buildPositive,execute_positive:executePositive,build_negative:buildNegative});
report.inputs=["events/callbacks.go","abi/function.go","tools/event-compile-fixtures.mjs"].map(hashed);
report.summary.source_error_directives_passed=passed?1:0;report.summary.failed=passed?0:1;
fs.writeFileSync(path.join(root,"parity/runs/event-compile-fixtures.json"),JSON.stringify(report,null,2)+"\n");
console.log(report.summary);if(!passed)process.exitCode=1;
