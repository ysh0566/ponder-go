import {test} from 'node:test';import assert from 'node:assert/strict';import fs from 'node:fs';import path from 'node:path';
import {adaptSetup,createAdaptedUpstream,resolveOracleRoot,setupPath,validatePinnedCheckout,pinnedCommit} from './loopback-adapter.mjs';
test('fail closed on unknown startup shape',()=>assert.throws(()=>adaptSetup('changed startup'),/shape changed/));
test('complete copy changes only startup, rejects changed test body and removes own temporary tree',()=>{
 const previous=process.env.PONDER_UPSTREAM_ROOT;
 const copy=createAdaptedUpstream({artifactsFrom:process.env.PONDER_ADAPTER_ARTIFACTS});
 try {
  process.env.PONDER_UPSTREAM_ROOT=copy.directory;
  assert.equal(resolveOracleRoot(),copy.directory);
  assert.deepEqual(copy.manifest.changed_files,[setupPath]);
  const body=path.join(copy.directory,'packages/core/src/sync-realtime/index.test.ts');
  const original=fs.readFileSync(body);
  fs.appendFileSync(body,'\n// tamper-detection test only\n');
  assert.throws(()=>resolveOracleRoot(),/hash mismatch/);
  assert.throws(()=>copy.verify(),/Adapted source changed/);
  fs.writeFileSync(body,original);
  copy.verify();
 }finally{if(previous===undefined)delete process.env.PONDER_UPSTREAM_ROOT;else process.env.PONDER_UPSTREAM_ROOT=previous;copy.dispose();}
 assert.equal(fs.existsSync(copy.directory),false);
});

test('reject unpinned or dirty original checkout state',()=>{assert.throws(()=>validatePinnedCheckout('0'.repeat(40),''),/Pinned upstream/);assert.throws(()=>validatePinnedCheckout(pinnedCommit,' M src/file.ts'),/Pinned upstream/);validatePinnedCheckout(pinnedCommit,'');});
