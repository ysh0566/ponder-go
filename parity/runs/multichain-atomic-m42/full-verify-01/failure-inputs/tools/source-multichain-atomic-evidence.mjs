import assert from 'node:assert/strict';
export const multichainAtomicProbeNames=['setup-success','setup-finalized-first-success','setup-second-chain-failure','historical-success','historical-second-event-failure','historical-metadata-failure'];
const scalar=v=>v?.$bigint??v;
const completed=c=>c.completedMetric.values.find(x=>x.labels.event==='Blocks:block').value;
export function assertMultichainAtomicCapture(capture,backend){
 assert.equal(capture.source_credit,0);assert.deepEqual(capture.cases.map(c=>c.name),multichainAtomicProbeNames);
 assert.ok(['postgres','pglite'].includes(backend));
 for(const c of capture.cases){
  assert.equal(c.source_credit,0);assert.equal(c.backend,backend);assert.equal(c.production_function,'runMultichain');
  assert.ok(c.before&&c.after);assert.equal(c.after.meta.find(x=>x.key==='app').value.is_ready,0);assert.equal(c.after.rpcCache.length,c.drained.cacheWritesSettled);assert.ok(c.after.rpcCache.length>c.before.rpcCache.length);assert.equal(c.drained.rpcInflight,0);assert.ok(c.drained.cacheWritesSettled>=2);
  assert.ok(c.rpc.length>0);assert.ok(c.rpc.every(r=>r.response!==undefined||r.error));assert.deepEqual([...new Set(c.rpc.map(r=>r.chainId))].sort(),[1,2]);
  assert.ok(c.identities.length>=2);assert.ok(c.identities.every(x=>x.args&&x.context&&x.db));
  assert.ok(c.transactions.every(x=>x.callbacks===1));assert.ok(!c.timeline.some(x=>x.kind==='forbidden-cutover'));
  assert.match(c.native_difference,/callback-only PGlite/);
  const setup=c.timeline.filter(x=>x.kind==='setup-handler');
  assert.deepEqual(setup.map(x=>x.chainId),c.name==='setup-second-chain-failure'?[1,2]:[1,2,1]);
  if(c.name.endsWith('success')){assert.equal(c.error_is_harness_stop,true);assert.equal(c.harness_stop.brand,true);assert.equal(c.harness_stop.outside_transaction,true);assert.equal(c.iterator_returned,true);assert.equal(c.error.message,'M42 selected real prefix exhausted');assert.ok(c.timeline.filter(x=>x.kind==='harness-stop').every(x=>x.txDepth===0));}
  else {assert.equal(c.error_is_harness_stop,false);assert.equal(c.error.name,'NonRetryableUserError');assert.ok(!c.harness_stop);}
  if(c.name==='setup-second-chain-failure'){
   assert.equal(c.error.message,'M42 setup second chain failure');assert.deepEqual(c.after.checkpoints,c.before.checkpoints);assert.deepEqual(c.after.business,c.before.business);assert.equal(c.groups.length,0);continue;
  }
  assert.equal(c.initialCheckpoints.length,2);
  for(const cp of c.initialCheckpoints){assert.match(cp.initial,/^\d{75}$/);assert.equal(Number(cp.initial.slice(10,26)),cp.chainId);assert.equal(cp.initial,cp.start<cp.finalized?cp.start:cp.finalized);}
  const kinds=c.timeline.map(x=>x.kind);const flushEnd=kinds.indexOf('flush-end'),metadata=kinds.indexOf('metadata-begin');assert.ok(flushEnd>=0&&metadata>flushEnd);
  if(c.name.startsWith('setup-')){
   assert.deepEqual(c.after.business,[{id:'shared',balance:{$bigint:'3'}}]);assert.equal(c.groups.length,0);
   for(const cp of c.initialCheckpoints){const actual=c.after.checkpoints.find(x=>x.chainId===cp.chainId);for(const field of ['latestCheckpoint','safeCheckpoint','finalizedCheckpoint'])assert.equal(actual[field],cp.initial);if(c.name==='setup-finalized-first-success'){assert.ok(cp.start>cp.finalized);assert.equal(cp.initial,cp.finalized);}}
  }else{
   assert.equal(c.groups.length,1);const group=c.groups[0];assert.ok(group.events.length>=2);assert.ok(group.events.every(x=>x.chain.id===group.chainId));assert.ok(c.timeline.filter(x=>x.kind==='consumer-yield').every(x=>x.txDepth===0));
   const count=c.name==='historical-second-event-failure'?1:group.events.length;
   assert.equal(c.eventCount['Blocks:block'],count);const profile=c.columnAccessPattern.find(x=>x[0]==='Blocks:block')[1];assert.equal(profile.count,count);
   assert.equal(completed(c),c.name==='historical-success'?count:0);
   assert.equal(c.attempts.length,c.name==='historical-second-event-failure'?2:count);
   const state=c.after.checkpoints.find(x=>x.chainId===group.chainId);const expected=c.name==='historical-success'?group.checkpoint:c.initialCheckpoints.find(x=>x.chainId===group.chainId).initial;for(const field of ['latestCheckpoint','safeCheckpoint','finalizedCheckpoint'])assert.equal(state[field],expected);
   if(c.name==='historical-success')assert.equal(c.after.business.length,count+1);
   if(c.name==='historical-second-event-failure')assert.equal(c.after.business.length,1);
   if(c.name==='historical-metadata-failure'){assert.equal(c.error.message,'M42 metadata failure after flush');assert.equal(c.after.business.length,backend==='postgres'?1:count+1);assert.ok(c.timeline.find(x=>x.kind==='injected-metadata-error'));}
  }
  assert.equal(scalar(c.after.business.find(x=>x.id==='shared').balance),'3');
 }
 return {cases:capture.cases.length,source_credit:0};
}
