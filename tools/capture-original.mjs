import fs from "node:fs";
import path from "node:path";
import net from "node:net";
import crypto from "node:crypto";
import { spawnSync } from "node:child_process";

const root = path.resolve(import.meta.dirname, ".."), upstream = path.resolve(root, "../ponder-upstream");
const argument = name => process.argv.find(value => value.startsWith(`--${name}=`))?.slice(name.length+3);
const mode = argument("mode"), destination = argument("output");
if (!["events", "filters"].includes(mode) || !destination) throw Error("Explicit --mode=events|filters and a new --output=... are required");
const output = path.resolve(root, destination);
const fixture = argument("fixture");
const expectedFixture = `testdata/runtime/captured-original-${mode}.json`;
if (fixture && fixture !== expectedFixture) throw Error("Unexpected native fixture destination");
if (fs.existsSync(output)) throw Error("Preserve existing evidence; choose a new output directory");
for (const directory of [root, upstream, path.join(upstream,"packages/core")]) if (fs.existsSync(path.join(directory,".env.local"))) throw Error("Unexpected .env.local: " + directory);
const probe = net.createServer();
await new Promise((resolve,reject) => {probe.once("error",reject);probe.listen(8545,"127.0.0.1",() => probe.close(resolve));});
fs.mkdirSync(output,{recursive:true});
const file = `packages/core/src/runtime/${mode === "events" ? "events" : "filter"}.test.ts`;
const dataPath = path.join(output,"calls.json"), oraclePath = path.join(output,"original.json");
const env = {...process.env,CI:"1",PONDER_CAPTURE_MODE:mode,PONDER_CAPTURE_OUTPUT:dataPath,PONDER_BIND_AUDIT:path.join(output,"bindings.jsonl"),NODE_OPTIONS:`${process.env.NODE_OPTIONS??""} --import=${path.join(root,"tools/loopback-only.mjs")}`.trim()};
delete env.DATABASE_URL;
const args=["run","--config","tools/capture-original.config.mjs","--configLoader","native",file.replace("packages/core/",""),"--reporter=json",`--outputFile=${oraclePath}`];
const report={mode,file,started_utc:new Date().toISOString(),complete:false,success:false,method:"Full original file with original setup/helpers/generated ABI and assertions. Export call-through spies only; captured fixture evidence is distinct from pristine original acceptance.",command:["vitest",...args]};
const save=()=>fs.writeFileSync(path.join(output,"result.json"),JSON.stringify(report,null,2)+"\n");
const digest=file=>crypto.createHash("sha256").update(fs.readFileSync(path.join(root,file))).digest("hex");
try {
 const revision=spawnSync("git",["rev-parse","HEAD"],{cwd:upstream,encoding:"utf8",timeout:15000});
 if(revision.error||revision.status!==0||revision.stdout.trim()!=="17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334") throw Error("Wrong or unavailable upstream revision");
 const pristine=spawnSync("git",["status","--porcelain","--untracked-files=no"],{cwd:upstream,encoding:"utf8",timeout:15000});
 if(pristine.error||pristine.status!==0||pristine.stdout.trim()) throw Error("Original tracked sources must remain unchanged");
 const version=spawnSync("anvil",["--version"],{encoding:"utf8",timeout:15000});
 if(version.error||version.status!==0) throw Error("Cannot record task Anvil version");
 report.anvil_version=version.stdout.trim();
 const fd=fs.openSync(path.join(output,"output.txt"),"w");
 let child;
 try {child=spawnSync(path.join(upstream,"packages/core/node_modules/.bin/vitest"),args,{cwd:root,env,stdio:["ignore",fd,fd]});} finally {fs.closeSync(fd);}
 Object.assign(report,{exit_code:child.status,signal:child.signal,error:child.error?.message??null});
 save();
 if(child.error||child.status!==0) throw Error("Original capture test run failed");
 const oracle=JSON.parse(fs.readFileSync(oraclePath,"utf8")),calls=JSON.parse(fs.readFileSync(dataPath,"utf8"));
 if(!oracle.success||oracle.numFailedTests!==0||oracle.numPassedTests!==(mode==="events"?11:23)||calls.cases.length!==(mode==="events"?6:9)) throw Error("Incomplete original file or selected capture");
 const sourceHash=digest("../ponder-upstream/"+file),source=fs.readFileSync(path.join(upstream,file),"utf8");
 if(calls.cases.some(c=>c.source_sha256!==sourceHash||!source.includes(c.test_source)||!c.calls.length)) throw Error("Stale source or missing captured calls");
 const contexts=[
  "../ponder-upstream/packages/core/vite.config.ts","../ponder-upstream/packages/core/src/_test/globalSetup.ts",
  "../ponder-upstream/packages/core/src/_test/setup.ts","../ponder-upstream/packages/core/src/_test/utils.ts",
  "../ponder-upstream/packages/core/src/_test/simulate.ts","../ponder-upstream/packages/core/src/_test/constants.ts",
  "../ponder-upstream/packages/core/src/_test/generated.ts","../ponder-upstream/packages/core/src/runtime/events.ts",
  "../ponder-upstream/packages/core/src/runtime/filter.ts","../ponder-upstream/"+file,
  "../ponder-upstream/packages/core/src/rpc/actions.ts","../ponder-upstream/packages/core/src/rpc/index.ts",
  "../ponder-upstream/packages/core/src/utils/decodeEventLog.ts","../ponder-upstream/packages/core/src/utils/decodeAbiParameters.ts",
  "../ponder-upstream/packages/core/src/utils/lowercase.ts","../ponder-upstream/packages/core/src/utils/checkpoint.ts",
  "../ponder-upstream/packages/core/src/build/factory.ts","../ponder-upstream/packages/core/src/config/address.ts",
  "../ponder-upstream/pnpm-lock.yaml",
  "tools/full-oracle.config.mjs","tools/loopback-only.mjs","tools/capture-original.config.mjs",
  "tools/capture-original.setup.ts","tools/capture-original.mjs",
 ];
 calls.capture_proof={scope:"instrumented_original_file_with_call_through",pristine_original_acceptance:false,anvil_version:report.anvil_version,source_context:contexts.map(file=>({file,sha256:digest(file)})),raw_oracle:{file:path.relative(root,oraclePath),sha256:digest(path.relative(root,oraclePath))},original_passed:oracle.numPassedTests,original_pending:oracle.numPendingTests};
 fs.writeFileSync(dataPath,JSON.stringify(calls,null,2)+"\n");
 if(fixture) fs.copyFileSync(dataPath,path.join(root,fixture));
 Object.assign(report,{complete:true,success:true,finished_utc:new Date().toISOString(),original_passed:oracle.numPassedTests,selected_source_cases:calls.cases.length,static_assertion_expressions:calls.cases.reduce((n,c)=>n+c.assertions.length,0),captured_operations:calls.cases.reduce((n,c)=>n+c.calls.length,0),capture_file:path.relative(root,dataPath)});
} catch(error){report.error=error.message;process.exitCode=1;} finally {save();}
console.log(report);
