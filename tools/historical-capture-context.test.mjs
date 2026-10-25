import test from 'node:test';import assert from 'node:assert/strict';import fs from 'node:fs';import crypto from 'node:crypto';import {execFileSync} from 'node:child_process';
import {historicalCaptureContext,validateHistoricalLink,validateHistoricalFixture,manifestFile} from './historical-capture-context.mjs';
const manifest=JSON.parse(fs.readFileSync(manifestFile));
const sha=b=>crypto.createHash('sha256').update(b).digest('hex');
for(const link of manifest.entries)test('exact published fixture and archive: '+link.fixture.file,()=>{
 const fixtureBytes=fs.readFileSync(link.fixture.file),fixture=JSON.parse(fixtureBytes);
 assert.equal(sha(execFileSync('git',['show',manifest.commit+':'+link.fixture.file])),sha(fixtureBytes));
 const args={fixtureFile:link.fixture.file,fixtureBytes,rawBytes:fs.readFileSync(fixture.capture_proof.raw_oracle.file),entry:link.original_ref,manifest,frozenBytes:fs.readFileSync(link.frozen_ref.file)};
 assert.equal(validateHistoricalLink(args).file,link.frozen_ref.file);
 assert.equal(historicalCaptureContext(link.fixture.file).filter(x=>x.historical_commit).length,1);
 for(const kind of ['removed','renamed','duplicate']){
  const changed=structuredClone(fixture),refs=changed.capture_proof.source_context;
  const i=refs.findIndex(x=>x.file==='tools/full-oracle.config.mjs');
  if(kind==='removed')refs.splice(i,1);
  if(kind==='renamed')refs[i].file='tools/other.config.mjs';
  if(kind==='duplicate')refs.push({...refs[i]});
  assert.throws(()=>validateHistoricalFixture(link.fixture.file,Buffer.from(JSON.stringify(changed))));
 }
 for(const mutation of [
  {frozenBytes:Buffer.concat([args.frozenBytes,Buffer.from('tamper')])},
  {fixtureBytes:Buffer.concat([fixtureBytes,Buffer.from(' ')])},
  {rawBytes:Buffer.concat([args.rawBytes,Buffer.from(' ')])},
  {manifest:{...manifest,commit:'0'.repeat(40)}},
  {manifest:{...manifest,blob:'0'.repeat(40)}},
  {entry:{...link.original_ref,sha256:'0'.repeat(64)}},
  {entry:{...link.original_ref,file:'tools/../tools/full-oracle.config.mjs'}},
  {fixtureFile:'parity/runs/runtime-index-m35/full-verify-04/result.json'},
 ])assert.throws(()=>validateHistoricalLink({...args,...mutation}));
 const wrong=structuredClone(manifest);wrong.entries.find(x=>x.fixture.file===link.fixture.file).frozen_ref.file='tools/full-oracle.config.mjs';assert.throws(()=>validateHistoricalLink({...args,manifest:wrong}));
});
test('current contexts and unknown capture paths are never accepted',()=>{
 assert.throws(()=>historicalCaptureContext('tools/full-oracle.config.mjs'));
 assert.throws(()=>historicalCaptureContext('parity/runs/runtime-index-m35/full-verify-04/context-archive.json'));
});
