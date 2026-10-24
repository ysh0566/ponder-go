// Snapshot the prepared inputs before any native test execution. Full verify
// refreshes original captures first; their observed data/provenance then stays
// fixed for normal/race runs. Outputs are outside the native source inventory.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import {spawnSync} from "node:child_process";
import {sourceFingerprint} from "./go-source-fingerprint.mjs";
const root=path.resolve(import.meta.dirname,".."),fingerprint=sourceFingerprint(root);
const prefixes=["abi/","bloom/","bridge/","checkpoint/","cmd/","config/","events/","examples/","factory/","filter/","finality/","interval/","orm/","query/","rpc/","runtime/","schema/","sdk/","sqlquery/","store/","testdata/","third_party/"];
const listed=spawnSync("git",["ls-files","-z","--cached","--others","--exclude-standard"],{cwd:root,encoding:"utf8",maxBuffer:8*1024*1024});
if(listed.error||listed.status!==0)throw Error("Native input inventory failed");
const files=[...new Set(listed.stdout.split("\0").filter(f=>f&&(prefixes.some(p=>f.startsWith(p))||["go.mod","go.sum","parity/sync-migration-assets.json"].includes(f))))].sort();
const entries=files.map(file=>[file,crypto.createHash("sha256").update(fs.readFileSync(path.join(root,file))).digest("hex")]);
if(entries.length!==fingerprint.files||crypto.createHash("sha256").update(JSON.stringify(entries)).digest("hex")!==fingerprint.sha256)throw Error("Native inventory differs from execution driver's inventory");
const report={recorded_utc:new Date().toISOString(),method:"Recorded after original capture/code generation and before native Go tests. Inputs must remain identical through normal/race and acceptance.",fingerprint,files:entries.map(([file,sha256])=>({file,sha256})),tool:{file:"tools/record-native-inputs.mjs",sha256:crypto.createHash("sha256").update(fs.readFileSync(import.meta.filename)).digest("hex")}};
fs.writeFileSync(path.join(root,"parity/runs/prepared-native-inputs.json"),JSON.stringify(report,null,2)+"\n");console.log({prepared_native_inputs:fingerprint});
