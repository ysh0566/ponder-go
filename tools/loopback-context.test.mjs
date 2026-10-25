import {test}from'node:test';import assert from'node:assert/strict';import fs from'node:fs';import os from'node:os';import path from'node:path';
import {createAdaptedUpstream,setupPath}from'./loopback-adapter.mjs';
import {persistAdaptedContext,validateContextEntry,persistInterruptedContext}from'./loopback-context.mjs';
const root=path.resolve(import.meta.dirname,'..');
test('durable exact context survives disposal and fails closed on tampering',()=>{
 const copy=createAdaptedUpstream({artifactsFrom:process.env.PONDER_ADAPTER_ARTIFACTS});
 const out=fs.mkdtempSync(path.join(os.tmpdir(),'loopback-context-test-'));
 try {
  const {archive,path:archivePath,sha256}=persistAdaptedContext(copy,out);
  assert.match(sha256,/^[0-9a-f]{64}$/);assert.ok(fs.existsSync(archivePath));
  const entry=file=>({file:path.relative(root,path.join(copy.directory,file)),sha256:archive.entries[file].sha256});
  const original=entry('packages/core/src/sync-realtime/index.test.ts'),startup=entry(setupPath),artifact=entry(copy.manifest.generated_artifacts.files[0].file);
  for(const value of [original,startup,artifact])assert.equal(validateContextEntry(value,archive),true);
  assert.equal(Object.values(archive.entries).filter(e=>e.kind!=='unchanged-tracked').length,1+copy.manifest.generated_artifacts.files.length);
  const postOut=path.join(out,'interrupted');const post=persistInterruptedContext(copy.directory,postOut);assert.equal(post.archive.verification_phase,'post-interruption');
  copy.dispose();
  for(const value of [original,startup,artifact])assert.equal(validateContextEntry(value,archive),true);
  assert.equal(validateContextEntry({...startup,sha256:'0'.repeat(64)},archive),false);
  assert.equal(validateContextEntry({file:path.relative(root,path.join(copy.directory,'unknown.ts')),sha256:startup.sha256},archive),false);
  assert.equal(validateContextEntry({file:path.join(copy.directory,'src')+'/../'+setupPath,sha256:startup.sha256},archive),false);
  const escaped=structuredClone(archive);escaped.entries[setupPath].persistent_file=path.join(out,'../escaped');assert.equal(validateContextEntry(startup,escaped),false);
  fs.appendFileSync(archive.entries[setupPath].persistent_file,'changed');assert.equal(validateContextEntry(startup,archive),false);
  fs.rmSync(archive.entries[copy.manifest.generated_artifacts.files[0].file].persistent_file);assert.equal(validateContextEntry(artifact,archive),false);
  assert.throws(()=>persistAdaptedContext(copy,out));
 }finally{copy.dispose();fs.rmSync(out,{recursive:true,force:true});}
});
