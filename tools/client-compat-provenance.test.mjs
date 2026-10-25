import {test}from'node:test';import assert from'node:assert/strict';import fs from'node:fs';import os from'node:os';import path from'node:path';
import {pinnedClientCommit,sha256,assertClientProvenance,verifyFingerprints,verifyClientProvenance}from'./client-compat-provenance.mjs';
test('reject unpinned, dirty or different client source before execution',()=>{
 const good={head:pinnedClientCommit,status:'',sdk:Buffer.from('original'),original:Buffer.from('original')};
 assertClientProvenance(good);
 assert.throws(()=>assertClientProvenance({...good,head:'0'.repeat(40)}),/not pinned/);
 assert.throws(()=>assertClientProvenance({...good,status:' M packages/client/src/index.ts'}),/tracked changes/);
 assert.throws(()=>assertClientProvenance({...good,sdk:Buffer.from('modified')}),/differs/);
});
test('reject changed or missing external dependency after execution',()=>{
 const dir=fs.mkdtempSync(path.join(os.tmpdir(),'client-provenance-'));
 try{const file=path.join(dir,'dependency.mjs');fs.writeFileSync(file,'original');const entries=[{file,sha256:sha256('original')}];verifyFingerprints(entries);fs.writeFileSync(file,'changed');assert.throws(()=>verifyFingerprints(entries),/input changed/);fs.rmSync(file);assert.throws(()=>verifyFingerprints(entries),/ENOENT/);}finally{fs.rmSync(dir,{recursive:true,force:true});}
});
test('actual SDK source matches clean pinned original',()=>{const proof=verifyClientProvenance(path.resolve(import.meta.dirname,'..'));assert.equal(proof.sdk_matches_original,true);assert.equal(proof.pinned_commit,pinnedClientCommit);});
