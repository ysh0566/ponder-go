import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";

const root=path.resolve(import.meta.dirname,"..");
const digest=file=>({file,sha256:crypto.createHash("sha256").update(fs.readFileSync(path.resolve(root,file))).digest("hex")});
const fixture=JSON.parse(fs.readFileSync(path.join(root,"testdata/abi/topics-source.json"),"utf8"));
if(fixture.cases.length!==50||fixture.sources.some(f=>digest(f.file).sha256!==f.sha256))throw Error("Incomplete/stale actual indexed-topic source vectors");
for(const type of ["bytes32","address","uint256","string","bytes"]){
  const cases=fixture.cases.filter(c=>c.name.startsWith(type+"/"));
  if(cases.length!==10)throw Error("Missing source parameter-type coverage");
  for(const mode of ["undefined","positional_undefined","null","positional_null","missing"]){
    const c=cases.find(c=>c.name===type+"/"+mode);
    if(!c||c.error||c.topics.topic1!==null)throw Error("Original scalar nullish wildcard not established");
  }
  for(const mode of ["or_null","or_undefined"]){
    const c=cases.find(c=>c.name===type+"/"+mode);
    if(type==="string"||type==="bytes"){
      if(c.error||!Array.isArray(c.topics.topic1)||c.topics.topic1.length!==1)throw Error("Missing actual dynamic OR conversion");
    }else if(!c.error)throw Error("Missing actual static OR rejection");
  }
}
const report={
  counting:"50 actual pinned source vectors and new native/runtime regressions add zero original mappings. Exact original mappings are counted separately by the ledger. Native values and error occurrence are compared; complete JS/Go error class/message equivalence and all coercions are not claimed.",
  summary:{source_vectors:50,source_expected_errors:fixture.cases.filter(c=>c.error).length,new_exact_source_records:0,parameter_types:5},
  fixture:digest("testdata/abi/topics-source.json"),
  source_context:fixture.sources,
  inputs:["abi/topics.go","abi/topics_source_test.go","runtime/indexed_topic_source_test.go","runtime/indexed_topic_dynamic_test.go","tools/topic-fixtures.mjs","tools/topic-source-results.mjs"].map(digest),
  native_acceptance:"Required separately from complete unchanged-input module normal/race; focused runtime32 and source50 are supplementary only.",
};
fs.writeFileSync(path.join(root,"parity/runs/topic-source-verification.json"),JSON.stringify(report,null,2)+"\n");
console.log(report.summary);
