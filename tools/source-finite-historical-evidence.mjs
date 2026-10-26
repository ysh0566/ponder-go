import assert from 'node:assert/strict';
export const finiteHistoricalProbeNames=['fresh-multigroup','resume','empty-resume','missing-field-refetch','sticky-refetch','nonretryable','learned-prefetch','learned-prefetch-default'];
export function assertFiniteHistoricalCapture(capture,backend){
 assert.equal(capture.source_credit,0);assert.deepEqual(capture.reporter_errors,[]);assert.deepEqual(capture.cases.map(c=>c.name),finiteHistoricalProbeNames);assert.ok(['postgres','pglite'].includes(backend));
 for(const c of capture.cases){
  assert.equal(c.source_credit,0);for(const key of ['probe_error','drain_error','cleanup_error'])assert.equal(c[key],undefined);assert.equal(c.backend,backend);assert.equal(c.production_function,'runMultichain');assert.equal(c.drained.rpcInflight,0);
  assert.ok(c.identities.length>0&&c.identities.every(i=>i.args&&i.context&&i.db));assert.ok(!c.timeline.some(t=>t.kind==='forbidden-cutover'));assert.equal(c.after.meta.find(m=>m.key==='app').value.is_ready,0);
  assert.ok(c.rpc.every(r=>r.response!==undefined||r.error));assert.match(c.native_difference,/callback once without SQL transaction or automatic retry/);
  assert.equal(c.runs.length,['resume','empty-resume'].includes(c.name)?2:1);
  for(const [index,r] of c.runs.entries()){for(const key of ['capture_error','drain_error','snapshot_error','indexingArgs'])assert.equal(r[key],undefined);
   assert.deepEqual(r.setup,index===0?[1,2]:[]);assert.ok(r.bootstrap.length===2&&r.bootstrap.every(b=>/^\d{75}$/.test(b.start)&&/^\d{75}$/.test(b.end)&&b.end<=b.finalized));
   for(const kind of ['row-prefetch','rpc-prefetch']){const calls=c.timeline.filter(t=>t.run===index+1&&t.kind===kind);assert.equal(calls.length,r.groups.length);assert.ok(calls.every(t=>t.txDepth===0));assert.deepEqual(calls.map(t=>t.events),r.groups.map(g=>g.events.length));}
   assert.ok(r.groups.every(g=>g.events.every(e=>e.chain.id===g.chainId&&e.checkpoint<=g.checkpoint)));
   if(r.error_is_harness_stop){assert.equal(r.error.message,'M43 real finite historical consumer stopped');assert.equal(r.harness_stop.brand,true);assert.equal(r.harness_stop.outside_transaction,true);assert.equal(r.harness_stop.emitted,r.groups.length);assert.equal(r.iterator_returned,true);}
   const clears=c.timeline.filter(t=>t.run===index+1&&t.kind==='rpc-clear');assert.ok(clears.every(t=>t.txDepth===0));assert.equal(clears.length,r.transactions.filter(t=>t.returned).length-(index===0?1:0));
  }
  if(['fresh-multigroup','resume','empty-resume','learned-prefetch-default'].includes(c.name)||(c.name==='learned-prefetch'&&backend==='postgres')){
   assert.ok(c.runs.every(r=>r.error_is_harness_stop));assert.ok(c.runs[0].groups.length>1);assert.equal(new Set(c.runs.flatMap(r=>r.groups.map(g=>g.chainId))).size,2);assert.ok(c.runs.every(r=>r.transactions.every(t=>t.callbacks===1)));
   const seen=new Set();for(const r of c.runs)for(const g of r.groups)for(const e of g.events){const key=e.chain.id+':'+e.checkpoint;assert.ok(!seen.has(key),'Committed event replayed');seen.add(key)}
  }
  if(['resume','empty-resume'].includes(c.name)){
   const r=c.runs[1];assert.deepEqual(r.crash,c.runs[0].after.checkpoints.map(p=>({chainId:p.chainId,checkpoint:p.latestCheckpoint})));assert.ok(r.groups.every(g=>g.events.every(e=>e.checkpoint>r.crash.find(p=>p.chainId===g.chainId).checkpoint)));
   if(c.name==='empty-resume'){assert.ok(r.groups.length>0);assert.ok(r.groups.every(g=>g.events.length===0));assert.equal(r.attempts.length,0);assert.deepEqual(r.after.business,c.runs[0].after.business);}
  }
  if(['missing-field-refetch','sticky-refetch'].includes(c.name)){
   assert.equal(c.refetch.length,1);const refetch=c.refetch[0],r=c.runs[0];assert.equal(refetch.txDepth,1);assert.equal(refetch.includeIdentity.shared,true);assert.deepEqual(refetch.includeIdentity.replaced,[true,true]);assert.ok(refetch.before.length>0);assert.ok(!Object.hasOwn(refetch.before[0].event.block,'gasUsed'));assert.ok(Object.hasOwn(refetch.after[0].event.block,'gasUsed'));assert.equal(refetch.after.length,refetch.before.length);
   if(backend==='postgres'){assert.equal(r.error_is_harness_stop,true);assert.equal(r.transactions[1].callbacks,2);assert.equal(r.attempts.length,r.groups[0].events.length+1)}else{assert.equal(r.error_is_invalid,true);assert.equal(r.error.name,'InvalidEventAccessError');assert.equal(r.transactions[1].callbacks,1);assert.equal(r.attempts.length,1)}
   if(c.name==='sticky-refetch'){assert.equal(r.caught.length,1);assert.equal(r.caught[0].name,'InvalidEventAccessError');assert.notEqual(r.error.message,'secondary callback error must not replace sticky invalid access')}
  }
  if(c.name==='nonretryable'){const r=c.runs[0];assert.equal(r.error.message,'M43 nonretryable handler');assert.equal(r.error.name,'NonRetryableUserError');assert.equal(r.error_is_harness_stop,false);assert.equal(r.attempts.length,1);assert.ok(r.transactions.every(t=>t.callbacks===1));assert.equal(c.refetch.length,0)}
  if(c.name==='fresh-multigroup'){assertDecodedFieldOrder(c.decodedFieldOrder);assertFailureCapture(c.failureCapture);}
  if(c.name.startsWith('learned-prefetch'))assertFiniteLearningCase(c,backend);
 }
 return {cases:capture.cases.length,source_credit:0,source_scope:finiteHistoricalSourceScope(capture,backend)};
}

export function assertFiniteLearningCase(c,backend){
 const pressure=c.name==='learned-prefetch',expectedError=pressure&&backend==='pglite',r=c.runs[0],budget=c.cacheBudget;
 assert.equal(budget.source,'buildOptions_v8_heap_default');assert.equal(budget.environmentOverrideAbsent,true);assert.equal(budget.allRuntimeOptionsDefault,false);assert.equal(budget.syncEventsQuerySize,0);assert.ok(Number.isSafeInteger(budget.heapSizeLimit)&&budget.heapSizeLimit>0);assert.equal(budget.originalBytes,Math.ceil(budget.heapSizeLimit/1024/1024/5/64)*64*1024*1024);assert.equal(budget.configuredBytes,pressure?1:budget.originalBytes);
 assert.deepEqual(c.deployments.map(d=>d.chainId),[1,2]);for(const d of c.deployments){assert.equal(d.height,'0x1');assert.equal(d.receipt.status,'0x1');assert.equal(d.receipt.blockNumber,'0x1');assert.match(d.receipt.contractAddress,/^0x[0-9a-f]{40}$/);assert.equal(d.code,'0x600160005260206000f3');assert.equal(d.call,'0x'+'0'.repeat(63)+'1')}
 const completed=expectedError?r.groups.slice(0,-1):r.groups;
 assert.deepEqual(r.transactions.filter(t=>t.groupIndex>=0).map(t=>({index:t.groupIndex,returned:t.returned,callbacks:t.callbacks})),completed.map((_,index)=>({index,returned:true,callbacks:1})));
 assert.deepEqual(r.attempts,completed.flatMap(g=>g.events.map(e=>({chainId:g.chainId,block:e.event.block.number.$bigint}))));
 assert.deepEqual(r.after,c.after);assert.equal(c.after.meta.find(m=>m.key==='app').value.is_ready,0);
 for(const chain of [1,2]){const groups=completed.filter(g=>g.chainId===chain),events=groups.flatMap(g=>g.events),boot=r.bootstrap.find(b=>b.chainId===chain);assert.deepEqual(r.after.business.find(row=>row.id==='shared'+chain).balance,{$bigint:String(events.length)});assert.equal(r.after.checkpoints.find(p=>p.chainId===chain).latestCheckpoint,groups.at(-1)?.checkpoint??[boot.start,boot.finalized].sort()[0]);if(!expectedError){assert.deepEqual(events.map(e=>Number(e.event.block.number.$bigint)),Array.from({length:120},(_,i)=>i+1));assert.equal(r.after.checkpoints.find(p=>p.chainId===chain).latestCheckpoint,boot.end)}}
 assert.ok(c.rpc.every(item=>!item.error&&item.response!==undefined));assert.ok(c.timeline.filter(t=>t.kind==='rpc-prefetch').every(t=>t.settled==='fulfilled'));
 const rows=c.timeline.filter(t=>t.kind==='row-prefetch'),failed=rows.filter(t=>t.settled==='rejected');assert.ok(rows.every(t=>['fulfilled','rejected'].includes(t.settled)));
 if(expectedError){
  assert.equal(r.error.name,'Error');assert.equal(r.error.constructorName,'Error');assert.equal(r.error.message,'Invalid input for string type');assert.equal(typeof r.error.stack,'string');assert.ok(r.error.stack.includes(r.error.message));assert.equal(r.error_is_first_row_error,true);assert.equal(r.shutdown_killed_at_error,false);assert.equal(r.error_is_harness_stop,false);assert.equal(r.error_is_invalid,false);
  assert.equal(failed.length,1);const f=failed[0];assert.equal(f.groupIndex,r.groups.length-1);assert.equal(f.run,1);assert.equal(f.txDepth,0);assert.equal(f.error_is_first_row_error,true);assert.equal(f.error.message,r.error.message);assert.equal(f.error.name,'Error');assert.equal(f.error.constructorName,'Error');assert.equal(f.error.stack,r.error.stack);
  assert.deepEqual(c.retryWarnings.map(w=>w.retry_count),Array.from({length:10},(_,i)=>i));assert.deepEqual(c.retryWarnings.slice(0,9).map(w=>w.retry_delay),Array.from({length:9},(_,i)=>125*2**i));assert.equal(Object.hasOwn(c.retryWarnings[9],'retry_delay'),false);
  for(const w of c.retryWarnings){assert.equal(w.groupIndex,f.groupIndex);assert.equal(w.run,1);assert.equal(w.txDepth,0);assert.equal(w.error.name,'Error');assert.equal(w.error.constructorName,'Error');assert.equal(w.error.message,'Invalid input for string type');assert.equal(typeof w.error.stack,'string');assert.equal(typeof w.observedAt,'number')}
  assert.equal(c.retryWarnings[0].error.stack,r.error.stack);assert.ok(c.retryWarnings[9].observedAt-c.retryWarnings[0].observedAt>60_000);assert.ok(r.attempts.length>0&&r.attempts.length<240);assert.equal(r.iterator_returned,true);assert.notEqual(r.exhausted,true);
 }else{
  assert.equal(r.error_is_harness_stop,true);assert.equal(r.exhausted,true);assert.equal(r.attempts.length,240);assert.deepEqual(failed,[]);assert.deepEqual(c.retryWarnings,[]);assert.equal(c.resolvedIncludeIdentity.separate,true);assert.deepEqual(c.resolvedIncludeIdentity.replaced,[true,true]);assert.ok(r.eventCount['Blocks:block']>100);assert.equal(r.columnAccessPattern.find(p=>p[0]==='Blocks:block')[1].resolved,true);
 }
 assert.ok(c.rpcPrefetchMetric.values.reduce((n,v)=>n+v.value,0)>0);const rowPrefetchPredictions=c.rowPrefetchMetric.values.filter(v=>v.labels.type==='prefetch').reduce((n,v)=>n+v.value,0);if(pressure)assert.ok(rowPrefetchPredictions>0);else assert.equal(rowPrefetchPredictions,0);
 assert.deepEqual(c.learningOutcome,{configuredEvents:240,completed:!expectedError,outcome:expectedError?'expected_upstream_error':'completed',sourceErrorObserved:expectedError,diagnosticAbort:false,fullBackoffTerminalObserved:expectedError,rowPrefetchPredictions});
 const p=c.textProfileRecovery;assert.equal(p.source_credit,0);assert.equal(p.scope,'actual_source_profile_recovery_and_column_cache_key');assert.deepEqual(p.key,{id:'4'});assert.deepEqual(p.pattern,{id:{type:'derived',value:['block','number']}});assert.deepEqual(p.recovered,{id:{$bigint:'4'}});assert.equal(p.hintSameIdentity,true);assert.deepEqual(p.mismatch,{$undefined:true});assert.deepEqual(p.driver,{$bigint:'4'});assert.equal(p.cacheKey,'4');assert.equal(p.public_crud_coercion_claim,false);
}
export function finiteHistoricalSourceScope(capture,backend){
 const pressure=capture.cases.find(c=>c.name==='learned-prefetch'),defaults=capture.cases.find(c=>c.name==='learned-prefetch-default');
 return {backend,default_completed:defaults.learningOutcome.completed,default_cache_budget:defaults.cacheBudget,pressure_configured_events:pressure.learningOutcome.configuredEvents,pressure_completed:pressure.learningOutcome.completed,pressure_outcome:pressure.learningOutcome.outcome,source_error_observed:pressure.learningOutcome.sourceErrorObserved,diagnostic_abort:pressure.learningOutcome.diagnosticAbort,full_backoff_terminal_observed:pressure.learningOutcome.fullBackoffTerminalObserved};
}

export function assertDecodedFieldOrder(c){
 assert.equal(c.source_credit,0);assert.equal(c.scope,'actual_decodeEvents_Object.keys_row_and_RPC_profile_order');assert.equal(c.override_boundary,'native_only_no_source_override_API');
 const numeric=['2','10','4294967294','z','4294967295','01','a'];
 const cases=[['log-indexed-first',['z','a'],['args','z']],['log-nonindexed',['z','a'],['args','z']],['log-integer-keys',numeric,['args','2']],['log-duplicate-overwrite',['dup','z'],['args','dup'],9],['log-unnamed-array',['0','1'],null],['trace-named-tuple',['z','a'],['result','z']],['trace-integer-tuple',numeric,['result','2']],['trace-duplicate-tuple',['z','a'],['result','z'],9],['trace-unnamed-tuple-array',['0','1'],null],['trace-multiple-output-array',['0','1'],null],['trace-args-array',[],null]];
 assert.deepEqual(c.observations.map(o=>o.name),cases.map(([name])=>name));
 for(const [i,[name,keys,path,target=7]] of cases.entries()){
  const o=c.observations[i],pattern=path?{type:'derived',value:path}:undefined;assert.equal(o.name,name);assert.deepEqual(o.actualKeys,keys);assert.deepEqual(o.target,{$bigint:String(target)});
  assert.deepEqual(o.rowPattern,pattern?{id:pattern}:{$undefined:true});assert.deepEqual(o.rpcArgPattern,pattern??{type:'constant',value:{$bigint:String(target)}});
  if(name.startsWith('trace-')){assert.equal(o.type,'trace');assert.equal(o.argsIsArray,true);assert.deepEqual(o.argsKeys,['0'])}else{assert.equal(o.type,'log');assert.equal(o.argsIsArray,name==='log-unnamed-array')}
  assert.equal(o.resultIsArray,['trace-unnamed-tuple-array','trace-multiple-output-array'].includes(name));
 }
 assert.deepEqual(c.observations.find(o=>o.name==='log-duplicate-overwrite').decodedArgs,{dup:{$bigint:'9'},z:{$bigint:'7'}});
 assert.deepEqual(c.observations.find(o=>o.name==='trace-duplicate-tuple').decodedResult,{z:{$bigint:'9'},a:{$bigint:'7'}});
 return {cases:cases.length,source_credit:0};
}

export function assertFailureCapture(c){
 assert.equal(c.source_credit,0);assert.equal(c.scope,'supplemental_observer_failure_serialization_control');assert.equal(c.actual_registry_cycle_rejected,true);assert.equal(c.deliberate_scratch_leak_rejected,true);assert.equal(c.original_callback_failure_claim,false);
 const r=c.observation,e=r.error;assert.equal(r.indexingArgs,undefined);assert.equal(r.error_is_invalid,true);assert.equal(e.name,'InvalidEventAccessError');assert.equal(e.constructorName,'InvalidEventAccessError');assert.equal(e.message,'supplemental capture failure control');assert.equal(typeof e.stack,'string');assert.ok(e.stack.includes(e.message));assert.equal(e.status,418);assert.equal(e.code,'M43_CAPTURE_CONTROL');assert.equal(e.key,'block.gasUsed');assert.equal(e.cause.name,'Error');assert.equal(e.cause.constructorName,'Error');assert.equal(e.cause.message,'original supplemental cause');assert.equal(typeof e.cause.stack,'string');assert.ok(e.cause.stack.includes(e.cause.message));assert.deepEqual(e.cause.cause,{$cycleReference:'$'});
 assert.equal(r.snapshot_error.message,'supplemental downstream snapshot failure');assert.equal(typeof r.snapshot_error.stack,'string');assert.deepEqual(r.timeline,[{kind:'supplemental-failure-control',stage:'handler',status:'failed'}]);assert.deepEqual(r.eventCount,{'Blocks:block':188});assert.deepEqual(r.columnAccessPattern,[['Blocks:block',{resolved:true,count:188}]]);
 return {source_credit:0,original_callback_failure_claim:false};
}
