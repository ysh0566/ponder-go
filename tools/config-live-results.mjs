// Validate actual executions in both complete Go logs; never substitute a
// previously captured Go result for running the native source builder.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
const root=path.resolve(import.meta.dirname,".."),read=file=>JSON.parse(fs.readFileSync(path.resolve(root,file),"utf8"));
const hash=file=>({file:path.relative(root,path.resolve(root,file)),sha256:crypto.createHash("sha256").update(fs.readFileSync(path.resolve(root,file))).digest("hex")});
const validHash=entry=>fs.existsSync(path.resolve(root,entry.file))&&hash(entry.file).sha256===entry.sha256;
const ledger=read("parity/test-ledger.json"),selected=ledger.tests.filter(t=>t.source_file==="packages/core/src/build/config.test.ts");
if(selected.length!==40)throw Error("Expected 40 complete expanded source records");
const executions=[];
const typedModes=[];
for(const mode of ["go-test","go-race"]){
 const lines=fs.readFileSync(path.join(root,`parity/runs/${mode}.jsonl`),"utf8").trim().split("\n").map(JSON.parse);
 const location=lines.filter(x=>x.Test==="TestUpstreamConfigRuntime"&&x.Output?.includes("Live original/native evidence: "));
 if(location.length!==1||!lines.some(x=>x.Test==="TestUpstreamConfigRuntime"&&x.Action==="pass"))throw Error("Missing actual successful complete native config execution: "+mode);
 const directory=location[0].Output.split("Live original/native evidence: ")[1].trim();
 if(!directory.startsWith(path.join(root,"parity/runs/config-types-m29/live-")))throw Error("Unexpected native evidence directory");
 const file=path.join(directory,"native.json"),native=read(file),capture=read(path.join(directory,"calls.json")),original=read(path.join(directory,"original.json")),compile=read(path.join(directory,"block-filter-compile.json"));
 if(native.cases.length!==39||capture.cases.length!==39||native.calls.length!==69||native.calls.some(c=>c.differences?.length))throw Error("Incomplete source calls or native differences: "+mode);
 if(!original.success||original.numPassedTests!==39||original.numPendingTests!==1||original.numFailedTests!==0)throw Error("Incomplete unchanged source assertions");
 if(!native.source_context?.length||!native.source_context.every(validHash)||!compile.inputs.every(validHash)||!validHash(compile.positive)||!validHash(compile.negative)||!compile.compile_passed)throw Error("Stale source or compiler context");
 for(const c of capture.cases){const source=selected.find(t=>t.id===c.id);if(!source||source.source_sha256!==c.source_sha256||source.test_source!==c.test_source||JSON.stringify(source.assertions)!==JSON.stringify(c.assertions)||!c.calls.length)throw Error("Incomplete original declaration: "+c.id);}

 const typedCompile=read(path.join(directory,"typed-compile.json")),literalCompile=read(path.join(directory,"literal-compile.json"));
 const extraDirectives=[...typedCompile.directives,...literalCompile.directives],controls=native.calls.flatMap(c=>c.typed_controls??[]);
 if(extraDirectives.length!==9||controls.length!==9||!validHash(literalCompile.generated)||![...typedCompile.inputs,...literalCompile.inputs].every(validHash))throw Error("Actual typed/generated production controls missing or stale");
 for(const directive of extraDirectives){
  const control=controls.find(c=>c.source_id===directive.source_id&&c.mode===directive.mode);
  if(!control||control.exit_code!==0||!control.output||!validHash(directive.positive)||!validHash(directive.negative)||directive.build_positive.exit_code!==0||directive.build_negative.exit_code<=0)throw Error("Typed directive lacks positive/build/native-negative proof: "+directive.mode);
 }
 typedModes.push({mode,directives:extraDirectives,controls,compile:hash(path.join(directory,"typed-compile.json")),literal_compile:hash(path.join(directory,"literal-compile.json")),generated:literalCompile.generated,corrected_event_source_error:literalCompile.corrected_event_source_error});
 const proofs=native.calls.filter(c=>c.type_proof);
 if(proofs.length!==1||proofs[0].case_id!==compile.source_id||proofs[0].type_proof.passed!==true)throw Error("Missing actual public filter positive execution");
 const output=JSON.parse(proofs[0].type_proof.positive_output);if(output.interval!==1||output.filter.fromBlock!==16370000||output.filter.toBlock!==16370020||output.callback!=="a:block")throw Error("Positive compiler fixture did not execute original materialized block input");
 const metadataChecks=native.calls.reduce((n,c)=>n+(c.metadata_identity_checks??0),0),hyperliquidChecks=native.calls.reduce((n,c)=>n+(c.hyperliquid_identity_checks??0),0);
 if(!metadataChecks||hyperliquidChecks!==2)throw Error("Original Hyperliquid toBe requires actual canonical native object identity checks");
 const rpcChecks=native.calls.reduce((n,c)=>n+(c.rpc_identity_checks??0),0),callbackChecks=native.calls.reduce((n,c)=>n+(c.callback_identity_checks??0),0);
 if(!rpcChecks||!callbackChecks)throw Error("Actual client/callback identity checks missing");
 executions.push({mode,complete:true,native:hash(file),capture:hash(path.join(directory,"calls.json")),original:hash(path.join(directory,"original.json")),compile:hash(path.join(directory,"block-filter-compile.json")),source_context:native.source_context,original_passed:39,original_pending:1,actual_native_calls:69,rpc_identity_checks:rpcChecks,callback_identity_checks:callbackChecks,metadata_identity_checks:metadataChecks,hyperliquid_identity_checks:hyperliquidChecks,positive_execution:{source_id:compile.source_id,exit_code:0,input:proofs[0].type_proof.input,output},compile_report:compile});
}
const first=executions[0].compile_report;
const directive={source_id:first.source_id,source_sha256:first.source_sha256,source_error_directive:first.source_error_directive,status:"passed",positive:first.positive,negative:first.negative,build_positive:first.build_positive,build_negative:first.build_negative,execute_positive:{exit_code:0,method:"Original materialized config input on the actual original Anvil during both complete native Go runs",normal:executions[0].positive_execution,race:executions[1].positive_execution}};

const extraDirectives=typedModes[0].directives.map(directive=>{
 const normal=typedModes[0].controls.find(c=>c.source_id===directive.source_id&&c.mode===directive.mode),race=typedModes[1].controls.find(c=>c.source_id===directive.source_id&&c.mode===directive.mode);
 return {...directive,status:"passed",execute_positive:{exit_code:0,method:"Actual production typed/generated API program on the original live Anvil during both native Go modes",normal,race}};
});
const pristine=read("parity/runs/config-oracle.json");if(!pristine.complete||!pristine.success||!validHash(pristine.raw_oracle)||!pristine.source_context.every(validHash))throw Error("Pristine original config acceptance missing or stale");
const report={baseline:ledger.baseline,counting:"39 active declarations executed on both sides; all39 fully mapped with10 actual native compiler obligations. One original skip retained,unmapped. Supplemental runtime/backend cases add zero source credit.",summary:{source_cases:40,active_runtime_cases:39,original_skip:1,source_assertion_expressions:94,active_assertion_expressions:92,fully_mapped:39,mapped_assertion_expressions:92,source_error_directives:10,source_error_directives_passed:10,source_error_directives_remaining:0},executions,pristine:hash("parity/runs/config-oracle.json"),directives:[directive,...extraDirectives],typed_executions:typedModes,inputs:["config/source_native.go","config/typed_source.go","config/source_bindings.go","cmd/source-codegen/main.go","config/source_upstream_test.go","runtime/source.go","tools/config-live-results.mjs","tools/config-compile-fixtures.mjs","tools/config-typed-compile-fixtures.mjs","tools/config-literal-compile-fixtures.mjs"].map(hash)};
fs.writeFileSync(path.join(root,"parity/runs/config-live-verification.json"),JSON.stringify(report,null,2)+"\n");
console.log(report.summary);
