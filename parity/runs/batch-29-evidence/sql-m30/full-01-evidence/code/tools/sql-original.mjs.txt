// External draft: explicit --output and PONDER_PORT_ROOT are required here.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { spawnSync } from "node:child_process";

const root = process.env.PONDER_PORT_ROOT ?? path.resolve(import.meta.dirname, "..");
const upstream = path.resolve(root, "../ponder-upstream");
const argument = name => process.argv.find(a => a.startsWith(`--${name}=`))?.slice(name.length + 3);
const destination = argument("output");
const capture = process.argv.includes("--capture");
if (!destination) throw Error("A new --output=directory is required");
const output = path.resolve(root, destination);
if (!output.startsWith(path.join(root, "parity/runs/")) || fs.existsSync(output)) throw Error("Preserve existing task evidence");
const sourceFile = "packages/core/src/utils/sql-parse.test.ts";
const fixture = argument("fixture");
if (fixture && (!capture || fixture !== "testdata/sql/captured-original-sql.json")) throw Error("Unexpected SQL fixture destination");
const hash = file => ({file, sha256:crypto.createHash("sha256").update(fs.readFileSync(path.resolve(root,file))).digest("hex")});
function command(args, options={}) {
  const result = spawnSync(args[0],args.slice(1),{cwd:root,encoding:"utf8",...options});
  if (result.error || result.status !== 0) throw Error(`Command failed: ${args.join(" ")}: ${result.error?.message ?? result.stderr}`);
  return result.stdout.trim();
}
if (command(["git","-C",upstream,"rev-parse","HEAD"]) !== "17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334") throw Error("Wrong upstream revision");
if (command(["git","-C",upstream,"status","--porcelain","--untracked-files=no"])) throw Error("Tracked original sources changed");
for (const directory of [root,upstream,path.join(upstream,"packages/core")]) {
  if (fs.existsSync(path.join(directory,".env.local"))) throw Error("Unexpected .env.local");
}
fs.mkdirSync(output,{recursive:true});
const report = {
  source_file:sourceFile,
  started_utc:new Date().toISOString(),
  scope:capture?"complete_original_with_call_through":"complete_pristine_original",
  complete:false,
  success:false,
};
const save=()=>fs.writeFileSync(path.join(output,"result.json"),JSON.stringify(report,null,2)+"\n");
save();
try {
  const config = capture ? path.join(import.meta.dirname,"sql-capture.config.mjs") : path.join(root,"tools/full-oracle.config.mjs");
  const args = ["run","--config",config,"--configLoader","native","src/utils/sql-parse.test.ts","--reporter=json",`--outputFile=${path.join(output,"original.json")}`];
  report.command=["vitest",...args];
  const env={...process.env,CI:"1",PONDER_PORT_ROOT:root,PONDER_SQL_CAPTURE_OUTPUT:path.join(output,"calls.json"),PONDER_BIND_AUDIT:path.join(output,"bindings.jsonl"),NODE_OPTIONS:`${process.env.NODE_OPTIONS??""} --import=${path.join(root,"tools/loopback-only.mjs")}`.trim()};
  delete env.DATABASE_URL;
  const fd=fs.openSync(path.join(output,"output.txt"),"w");
  let child;
  try {child=spawnSync(path.join(upstream,"packages/core/node_modules/.bin/vitest"),args,{cwd:root,env,stdio:["ignore",fd,fd]});}
  finally {fs.closeSync(fd);}
  Object.assign(report,{exit_code:child.status,signal:child.signal,error:child.error?.message??null});
  save();
  if(child.error||child.status!==0)throw Error("Complete original SQL file failed");
  const original=JSON.parse(fs.readFileSync(path.join(output,"original.json"),"utf8"));
  if(!original.success||original.numPassedTests!==14||original.numPendingTests!==0||original.numFailedTests!==0)throw Error("Incomplete source SQL acceptance");
  report.raw_oracle=hash(path.relative(root,path.join(output,"original.json")));
  report.source_context=[
    "../ponder-upstream/"+sourceFile,
    "../ponder-upstream/packages/core/src/utils/sql-parse.ts",
    "../ponder-upstream/packages/core/src/index.ts",
    "../ponder-upstream/packages/client/src/index.ts",
    "../ponder-upstream/packages/core/vite.config.ts",
    "../ponder-upstream/packages/core/src/_test/globalSetup.ts",
    "../ponder-upstream/packages/core/src/_test/setup.ts",
    "../ponder-upstream/packages/core/src/_test/generated.ts",
    "../ponder-upstream/pnpm-lock.yaml",
    "tools/full-oracle.config.mjs","tools/loopback-only.mjs",
    "../ponder-upstream/packages/core/node_modules/pg-query-emscripten/pg_query.js",
    "../ponder-upstream/packages/core/node_modules/pg-query-emscripten/package.json",
    path.relative(root,import.meta.filename),
    ...(capture?[path.relative(root,path.join(import.meta.dirname,"sql-capture.config.mjs")),path.relative(root,path.join(import.meta.dirname,"sql-capture.setup.ts"))]:[]),
  ].map(hash);
  if(capture){
    const calls=JSON.parse(fs.readFileSync(path.join(output,"calls.json"),"utf8"));
    const selected=JSON.parse(fs.readFileSync(path.join(root,"parity/test-ledger.json"),"utf8")).tests.filter(t=>t.source_file===sourceFile);
    const source=fs.readFileSync(path.join(upstream,sourceFile),"utf8"),sourceHash=hash("../ponder-upstream/"+sourceFile).sha256;
    if(calls.cases.length!==14||selected.length!==14)throw Error("Missing captured SQL declarations");
    for(const c of calls.cases){
      const t=selected.find(t=>t.id===c.id);
      if(!t||c.source_sha256!==sourceHash||!source.includes(c.test_source)||!c.calls.length||JSON.stringify(c.assertions)!==JSON.stringify(t.assertions))throw Error("Stale SQL source/input/assertion capture");
    }
    calls.capture_proof={scope:report.scope,pristine_original_acceptance:false,source_context:report.source_context,raw_oracle:report.raw_oracle,original_passed:14,original_pending:0};
    fs.writeFileSync(path.join(output,"calls.json"),JSON.stringify(calls,null,2)+"\n");
    if(fixture){fs.mkdirSync(path.dirname(path.join(root,fixture)),{recursive:true});fs.copyFileSync(path.join(output,"calls.json"),path.join(root,fixture));}
    report.capture=hash(path.relative(root,path.join(output,"calls.json")));
    report.captured_operations=calls.cases.reduce((n,c)=>n+c.calls.length,0);
    report.static_assertion_expressions=calls.cases.reduce((n,c)=>n+c.assertions.length,0);
  }
  Object.assign(report,{complete:true,success:true,finished_utc:new Date().toISOString(),original_passed:14,original_pending:0});
}catch(error){report.error=error.message;process.exitCode=1;}finally{save();}
console.log(report);
