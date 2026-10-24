import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import {spawnSync} from "node:child_process";
const root=path.resolve(import.meta.dirname,".."),upstream=path.resolve(root,"../ponder-upstream");
const output=path.join(root,"parity/runs/config-m28",`pristine-${new Date().toISOString().replaceAll(/[:.]/g,"-")}-${process.pid}`);
fs.mkdirSync(output,{recursive:true});
for(const directory of [root,upstream,path.join(upstream,"packages/core")])if(fs.existsSync(path.join(directory,".env.local")))throw Error("Unexpected .env.local: "+directory);
const hash=file=>({file,sha256:crypto.createHash("sha256").update(fs.readFileSync(path.join(root,file))).digest("hex")});
const env={...process.env,CI:"1",PONDER_BIND_AUDIT:path.join(output,"bindings.jsonl"),NODE_OPTIONS:`${process.env.NODE_OPTIONS??""} --import=${path.join(root,"tools/loopback-only.mjs")}`.trim()};delete env.DATABASE_URL;
const args=["run","--config","tools/full-oracle.config.mjs","--configLoader","native","src/build/config.test.ts","--reporter=json",`--outputFile=${path.join(output,"original.json")}`];
const fd=fs.openSync(path.join(output,"output.txt"),"w");let child;try{child=spawnSync(path.join(upstream,"packages/core/node_modules/.bin/vitest"),args,{cwd:root,env,stdio:["ignore",fd,fd]});}finally{fs.closeSync(fd);}
const original=JSON.parse(fs.readFileSync(path.join(output,"original.json"),"utf8"));
const complete=child.status===0&&!child.error&&original.success&&original.numPassedTests===39&&original.numPendingTests===1&&original.numFailedTests===0;
const report={complete,success:complete,method:"Complete unmodified original config.test.ts, original globalSetup/setup/constants/helpers/assertions and source test timeouts. No spies/replaced exports. One original skipped declaration retained.",command:["vitest",...args],exit_code:child.status,error:child.error?.message??null,counts:{passed:original.numPassedTests,pending:original.numPendingTests,failed:original.numFailedTests},raw_oracle:hash(path.relative(root,path.join(output,"original.json"))),source_context:["../ponder-upstream/packages/core/src/build/config.test.ts","../ponder-upstream/packages/core/src/build/config.ts","../ponder-upstream/packages/core/src/_test/globalSetup.ts","../ponder-upstream/packages/core/src/_test/setup.ts","../ponder-upstream/packages/core/src/_test/utils.ts","../ponder-upstream/packages/core/src/_test/constants.ts","../ponder-upstream/packages/core/src/_test/generated.ts","../ponder-upstream/pnpm-lock.yaml","tools/full-oracle.config.mjs","tools/loopback-only.mjs","tools/config-pristine-oracle.mjs"].map(hash)};
fs.writeFileSync(path.join(root,"parity/runs/config-oracle.json"),JSON.stringify(report,null,2)+"\n");console.log(report.counts);if(!complete)process.exitCode=1;
