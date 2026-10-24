// Validate source preparation separately from module-wide native acceptance.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";

const root=path.resolve(import.meta.dirname,"..");
const read=file=>JSON.parse(fs.readFileSync(path.resolve(root,file),"utf8"));
const hash=file=>({file,sha256:crypto.createHash("sha256").update(fs.readFileSync(path.resolve(root,file))).digest("hex")});
const valid=entry=>hash(entry.file).sha256===entry.sha256;
const ledger=read("parity/test-ledger.json");
const file="packages/core/src/utils/sql-parse.test.ts";
const source=ledger.tests.filter(t=>t.source_file===file);
if(source.length!==14||source.reduce((n,c)=>n+c.assertions.length,0)!==19||source.some(c=>c.type_error_directives.length||c.source_state!=="active"))throw Error("Unexpected original SQL declarations");
const fixture=read("testdata/sql/captured-original-sql.json");
if(fixture.baseline.revision!==ledger.baseline.revision||fixture.cases.length!==14||fixture.capture_proof?.scope!=="complete_original_with_call_through"||!fixture.capture_proof.source_context.every(valid)||!valid(fixture.capture_proof.raw_oracle))throw Error("Incomplete/stale SQL capture context");
const captureOracle=read(fixture.capture_proof.raw_oracle.file);
if(!captureOracle.success||captureOracle.numPassedTests!==14||captureOracle.numPendingTests!==0||captureOracle.numFailedTests!==0)throw Error("Incomplete captured original file");
for(const c of fixture.cases){
  const original=source.find(t=>t.id===c.id);
  if(!original||c.source_sha256!==original.source_sha256||c.test_source!==original.test_source||JSON.stringify(c.assertions)!==JSON.stringify(original.assertions)||!c.calls.length)throw Error("Changed SQL declaration/input/assertion: "+c.id);
}
const calls=fixture.cases.reduce((n,c)=>n+c.calls.length,0);
const relational=fixture.cases.find(c=>c.id==="a0e8f19f1c34af2d3d6c");
if(calls!==38||relational.calls.length!==8)throw Error("Missing original SQL calls/actual Drizzle relational inputs");
const pristinePath=process.argv.find(a=>a.startsWith("--pristine="))?.slice(11)??"parity/runs/sql-original-pristine.json";
const pristine=read(pristinePath);
if(!pristine.complete||!pristine.success||pristine.scope!=="complete_pristine_original"||!pristine.source_context.every(valid)||!valid(pristine.raw_oracle))throw Error("Incomplete/stale pristine SQL oracle");
const pristineOracle=read(pristine.raw_oracle.file);
if(!pristineOracle.success||pristineOracle.numPassedTests!==14||pristineOracle.numPendingTests!==0||pristineOracle.numFailedTests!==0)throw Error("Pristine original SQL assertions failed");
const vectors=read("testdata/sql/source-vectors.json");
if(vectors.cases.length!==61||vectors.source_parser_version!==160001||!vectors.sources.every(valid))throw Error("Incomplete/stale actual source SQL vectors");
const nested=vectors.cases.find(c=>c.sql==="SELECT (SELECT private_task.bump_cached_balance()) AS value");
if(!nested||nested.readonly!==false||nested.first_validation.error?.message!=="SubLink not supported")throw Error("Missing source conservative nested-query classification");
const ruleProof=read("parity/runs/sql-rules.json");
if(!valid(ruleProof.rules)||ruleProof.source_sha256!==hash("../ponder-upstream/"+ruleProof.source).sha256)throw Error("Native SQL rules differ from source");
const proof={
  baseline:ledger.baseline,
  counting:"14 exact source declarations/19assertions,38calls.61 supplementary source vectors and native cache/HTTP/transaction regressions add zero source credit. Native execution status comes only from complete unchanged-input Go/race evidence.",
  source_file:file,
  source_sha256:source[0].source_sha256,
  summary:{source_cases:14,source_assertions:19,source_error_directives:0,captured_operations:38,actual_relational_inputs:8,source_vectors:61,source_parser_version:160001},
  pristine:{report:hash(pristinePath),raw_oracle:pristine.raw_oracle,source_context:pristine.source_context},
  capture:{fixture:hash("testdata/sql/captured-original-sql.json"),raw_oracle:fixture.capture_proof.raw_oracle,source_context:fixture.capture_proof.source_context},
  vectors:{fixture:hash("testdata/sql/source-vectors.json"),sources:vectors.sources},
  rules:hash("parity/runs/sql-rules.json"),
  inputs:["sqlquery/sql.go","sqlquery/cache.go","sqlquery/upstream_test.go","sqlquery/cache_test.go","sqlquery/differential_test.go","sqlquery/rules.json","store/sql.go","store/sql_parser_cache_test.go","query/sql_validation_test.go","tools/sql-source-results.mjs"].map(hash),
};
fs.writeFileSync(path.join(root,"parity/runs/sql-source-verification.json"),JSON.stringify(proof,null,2)+"\n");
console.log(proof.summary);
