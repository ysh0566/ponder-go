// Read-only original compiler audit. Annotations are removed in memory only.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import {createRequire} from "node:module";

const root=path.resolve(import.meta.dirname,".."), upstream=path.resolve(root,"../ponder-upstream");
const require=createRequire(upstream+"/packages/core/package.json"), ts=require("typescript");
const file=upstream+"/packages/core/src/sync-store/index.test.ts", original=fs.readFileSync(file,"utf8");
const ledger=JSON.parse(fs.readFileSync(root+"/parity/test-ledger.json","utf8"));
const selected=ledger.tests.filter(t=>t.source_file==="packages/core/src/sync-store/index.test.ts"&&t.type_error_directives.length);
if(selected.length!==4||selected.reduce((n,t)=>n+t.type_error_directives.length,0)!==9)throw Error("Changed exact source obligations");
const parsed=ts.getParsedCommandLineOfConfigFile(upstream+"/packages/core/tsconfig.json",{noEmit:true,incremental:false},{...ts.sys,onUnRecoverableConfigFileDiagnostic:d=>{throw Error(ts.flattenDiagnosticMessageText(d.messageText,"\n"))}});
const options={...parsed.options,rootDir:upstream,paths:{...parsed.options.paths,"@ponder/utils":[upstream+"/packages/utils/src/index.ts"]}};
const hash=file=>({file:path.relative(root,file),sha256:crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex")});
function audit(remove){
 // Keep annotation byte lengths, so diagnostics retain original source offsets.
 const text=remove?original.replaceAll("@ts-expect-error","@port-type-audit"):original;
 const host=ts.createCompilerHost(options), read=host.readFile;
 host.readFile=f=>path.resolve(f)===file?text:read(f);
 const program=ts.createProgram({rootNames:[file],options,host});
 return ts.getPreEmitDiagnostics(program).map(d=>({file:d.file?path.relative(root,d.file.fileName):null,start:d.start??null,code:d.code,message:ts.flattenDiagnosticMessageText(d.messageText,"\n"),source_id:d.file?.fileName===file?selected.find(t=>d.start>=t.start&&d.start<t.end)?.id:null}));
}
const pristine=audit(false), stripped=audit(true);
const complete=pristine.length===0&&stripped.length===9&&stripped.every(d=>d.code===2322&&d.source_id&&d.message.includes("TransactionRequest"))&&selected.every(t=>stripped.filter(d=>d.source_id===t.id).length===t.type_error_directives.length);
const report={complete,success:complete,typescript:ts.version,method:"Original TypeScript compiler context; pristine zero diagnostics, nine annotations removed only in memory. Explicit original utils source entry resolves the monorepo alias; no source files changed.",effective_options:{rootDir:upstream,utils_source_entry:upstream+"/packages/utils/src/index.ts"},pristine,stripped,source_context:[import.meta.filename,file,upstream+"/packages/core/tsconfig.json",upstream+"/tsconfig.base.json",upstream+"/packages/utils/src/index.ts",upstream+"/packages/core/src/sync-store/index.ts",upstream+"/pnpm-lock.yaml"].filter(fs.existsSync).map(hash)};
fs.writeFileSync(root+"/parity/runs/source-sync-types.json",JSON.stringify(report,null,2)+"\n");
console.log({complete,pristine:pristine.length,stripped:stripped.length});
if(!complete)process.exitCode=1;
