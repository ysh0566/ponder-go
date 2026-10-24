// Real public filter union, consumed by the source builder/native runtime.
// Positive execution uses the original materialized input and live Anvil from
// config-live.setup.ts. The original bad access differs only by narrowing.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import {spawnSync} from "node:child_process";
const root=path.resolve(import.meta.dirname,".."),directory=path.join(root,".parity-build/config-types");
const output=process.argv[2]?path.resolve(process.argv[2]):path.join(root,"parity/runs/config-m28/block-filter-compile.json");
fs.mkdirSync(directory,{recursive:true});
const ledger=JSON.parse(fs.readFileSync(path.join(root,"parity/test-ledger.json"),"utf8"));
const original=ledger.tests.find(t=>t.source_file==="packages/core/src/build/config.test.ts"&&t.title==="buildIndexingFunctions() block source");
if(original?.type_error_directives.length!==1)throw Error("Expected the exact filter-union interval-read obligation");
const hash=file=>({file,sha256:crypto.createHash("sha256").update(fs.readFileSync(path.join(root,file))).digest("hex")});
const run=args=>{const r=spawnSync("go",args,{cwd:root,encoding:"utf8"});return {command:["go",...args],exit_code:r.status,error:r.error?.message??null,stdout:r.stdout,stderr:r.stderr};};
const positive="config/testdata/compile/block-filter-positive.go",negative="config/testdata/compile/block-filter-negative.go.txt";
const temporary=path.join(directory,"block-filter-negative.go");fs.copyFileSync(path.join(root,negative),temporary);
const buildPositive=run(["build","-o",path.join(directory,"block-filter-positive"),positive]);
const buildNegative=run(["build","-o",path.join(directory,"block-filter-negative"),temporary]);
const passed=buildPositive.exit_code===0&&!buildPositive.error&&buildNegative.exit_code>0&&!buildNegative.error&&/Filter\.Interval undefined.*config\.SourceFilter has no field or method Interval/.test(buildNegative.stderr);
const report={source_id:original.id,source_sha256:original.source_sha256,source_error_directive:original.type_error_directives[0],compile_passed:passed,positive_execution:"required separately during actual original block-source case on the live shared Anvil; compile success alone is insufficient",positive:hash(positive),negative:hash(negative),inputs:["config/source_native.go","runtime/source.go","tools/config-compile-fixtures.mjs"].map(hash),build_positive:buildPositive,build_negative:buildNegative};
fs.writeFileSync(output,JSON.stringify(report,null,2)+"\n");if(!passed)process.exitCode=1;
