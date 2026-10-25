import {sameOriginalDeclaration} from './source-runtime-historical-evidence.mjs';
export {sameOriginalDeclaration} from './source-runtime-historical-evidence.mjs';
export const indexingProfileTargets=['2f670307cc3e54bf3901','465e8823325559c8cfa1','e7d2206043a345533ec0','fcafee958f55fe65dc77','fb137834371e07d16724','732ac50bf3a96196de3e','4c57d8f36ec9b90f69ac','d8952ac4024fd02802a7','0173bc5e83b0843a8d4e','ddf6318af43ddc28cd80','ff30deb8b9aca842519b','4ace7b36bca44245e4b7'];
const eq=(a,b)=>JSON.stringify(a)===JSON.stringify(b);
export function assertIndexingProfileCapture(capture,declarations){
 const check=(v,m)=>{if(!v)throw Error(m)};
 check(capture.source_credit===0&&capture.format==='indexing-profile-source-v1'&&capture.cases.length===24,'Whole original24 capture required');
 check(new Set(capture.cases.map(c=>c.id)).size===24,'Unique declarations required');
 check(eq(capture.cases.filter(c=>c.targeted).map(c=>c.id),indexingProfileTargets),'Exact profile10/client2 order required');
 let profileCalls=0,providers=0;
 for(const c of capture.cases){const d=declarations.find(d=>d.id===c.id);check(sameOriginalDeclaration(c,d),'Original declaration mismatch '+c.id);if(!c.targeted)continue;
  if(c.source_file.endsWith('/profile.test.ts')){
   const records=c.profile_ops.filter(o=>o.operation==='recordProfilePattern'),recovers=c.profile_ops.filter(o=>o.operation==='recoverProfilePattern');
   check(records.length===(['d8952ac4024fd02802a7','0173bc5e83b0843a8d4e'].includes(c.id)?2:1)&&recovers.length===1,'Exact original record/recover operations required '+c.id);
   for(const op of records)check(op.args.abi.length===1&&op.event.event&&Array.isArray(op.hints)&&Object.hasOwn(op,'result'),'Complete actual profile call');
   for(const op of recovers)check(op.pattern_record_index>=0&&Object.hasOwn(op,'result'),'Recovery must refer to actual recorded pattern');
   if(c.id==='d8952ac4024fd02802a7')check(records[1].result_hint_index===0,'Original hint identity required');
   if(c.id==='0173bc5e83b0843a8d4e')check(records[1].result_hint_index===-1,'Original differing hint identity required');
   profileCalls+=c.profile_ops.length;
  }else{
   check(c.clients.length===1&&eq(c.operations.map(o=>o.operation),['setEvent','readContract','setEvent','prefetch','readContract']),'Original prefetch sequence required');
   const disabled=c.id==='4ace7b36bca44245e4b7';
   check(c.providers.length===(disabled?2:1)&&c.providers.every(p=>p.request.method==='eth_call'&&Object.hasOwn(p,'response')),'Actual original initial and predictive RPC required');
   check(eq(c.providers.map(p=>p.operation_index),disabled?[1,3]:[1]),'Final action must consume prefetch without RPC');
   check(eq(c.operations[1].result,{$bigint:'1000000000000000000'})&&eq(c.operations[4].result,c.operations[1].result),'Both exact original values required');
   check(c.operations[2].event.event.block.number.$bigint==='2','Original event mutation required');
   if(c.id==='4ace7b36bca44245e4b7')check(c.store.length===0&&c.physical_after.length===0&&!c.clients[0].chains[0].cacheRpcRequests,'Disabled persistent cache exact no get/insert');
   else check(c.store.some(o=>o.operation==='getRpcRequestResults'&&o.operation_index===3)&&!c.store.some(o=>o.operation==='getRpcRequestResults'&&o.operation_index===4),'Prefetch DB probe and final no DB get');
   providers+=c.providers.length;
  }
 }
 const targets=declarations.filter(d=>indexingProfileTargets.includes(d.id));check(targets.length===12&&targets.reduce((n,c)=>n+c.assertions.length,0)===30&&targets.every(c=>c.type_error_directives.length===0),'Exact12/30/0 census required');
 return {targeted:12,assertions:30,type_directives:0,profile_calls:profileCalls,providers};
}
export const indexingProfileProbeTitles=['source profile derivation and hint edge vectors','source predictive client causal boundary vectors'];
export function assertIndexingProfileProbeCapture(capture){
 const check=(v,m)=>{if(!v)throw Error(m)};
 check(capture.source_credit===0&&capture.real_rpc===false&&capture.real_database===false&&capture.cases?.length===2,'Explicit controlled source boundaries, no realDB/RPC or credit required');
 const [profile,client]=capture.cases;
 check(profile.name==='profile-edges'&&profile.vectors.length===21&&client.name==='client-boundaries'&&client.scenarios.length===21,'Exact supplemental census required');
 const expected=['threshold-db-exact','threshold-db-above','threshold-rpc-exact','threshold-rpc-above','sample-excluded','setup-excluded','explicit-excluded','sample-included','multicall-cutoff-9','multicall-cutoff-10','constant-lru-refresh','dedupe-before-chain','causal-clear-disabled-true','causal-clear-disabled-false','late-rejection','empty-prefetch','immutable-db-hit','pending-chunk-clear','aggregate-rejection-no-write','crosschain-held-db','crosschain-failfast-sibling'];
 check(eq(client.scenarios.map(x=>x.name),expected),'Exact causal scenario list required');
 for(const v of profile.vectors)check(v.event&&v.args&&Array.isArray(v.hints)&&Object.hasOwn(v,'result')&&!v.error,'Actual profile call result required');
 const both=profile.vectors.find(v=>v.name==='trace-both-args-result');check(both.result.pattern.args.length===2&&eq(both.result.pattern.address.value,['result','last']),'Preserve trace double-match source quirk');
 check(profile.vectors.find(v=>v.name==='hint-noargs-cache-mismatch').result_hint_index===0,'Preserve noargs hint cache quirk');
 for(const s of client.scenarios){
  check(s.operations.length>=2,'Actual source operations required');
  if(!s.causal)check(s.operations.every(o=>Array.isArray(o.rpc)&&Array.isArray(o.store)&&Array.isArray(o.timeline)),'Real source API operations and boundary traces required');
  else check(Array.isArray(s.rpc)&&Array.isArray(s.store)&&s.operations.every(o=>o.snapshot&&['rpc','reads','writes','pending_rpc','pending_db'].every(k=>Number.isSafeInteger(o.snapshot[k])&&o.snapshot[k]>=0)),'Causal source cumulative boundary snapshots required');
 }
 const last=name=>client.scenarios.find(s=>s.name===name).operations.at(-1);
 check(last('threshold-db-exact').store[0].args.requests.length===0&&last('threshold-db-above').store[0].args.requests.length===1,'Strict DB threshold');
 check(last('threshold-rpc-exact').rpc.length===0&&last('threshold-rpc-above').rpc.length===1,'Strict RPC threshold');
 for(const name of ['sample-excluded','setup-excluded','explicit-excluded','multicall-cutoff-10'])check(last(name).rpc.length===0,'Exclusion '+name);
 check(last('sample-included').rpc.length===1&&last('multicall-cutoff-9').rpc.length===1&&last('multicall-cutoff-9').rpc[0].request.params[1]==='0x8','Sample and cutoff9, outer immutable ignored');
 check(last('constant-lru-refresh').rpc.length===10&&!last('constant-lru-refresh').rpc.some(r=>r.request.params[0].to==='0x0000000000000000000000000000000000000002'),'LRU refresh/eviction');
 check(eq(last('dedupe-before-chain').rpc.map(r=>r.chainId),[1]),'Dedupe before chain grouping source quirk');
 for(const name of ['causal-clear-disabled-true','causal-clear-disabled-false']){const s=client.scenarios.find(s=>s.name===name),held=s.operations.find(o=>o.operation==='prefetch');check(held.pending_rpc===1&&held.timeline.indexOf('prefetch:return')<held.timeline.indexOf('rpc:resolve'),'Prefetch returns before admitted RPC completes');check(last(name).rpc.length===1,'clear retains profiles');if(name.endsWith('false'))check(s.operations.every(o=>o.store.length===0),'Disabled cache never reads/writes DB');}
 check(last('late-rejection').error&&last('late-rejection').rpc.length===0,'Late rejection consumed without new RPC');
 check(last('empty-prefetch').error&&last('empty-prefetch').store.some(o=>o.operation==='insertRpcRequestResults'&&o.args.requests[0].result==='"0x"'),'Source encoded empty future insertion quirk');
 check(last('immutable-db-hit').rpc.length===0&&last('immutable-db-hit').store[0].args.requests[0].params[1]==='latest','Immutable recovered latest DB hit');
 const causal=name=>{const s=client.scenarios.find(s=>s.name===name);check(s?.causal===true&&s.initial_event&&s.initial_event_count?.sample===1,'Genuine causal source scenario required');check(s.rpc.every(r=>r.request?.method==='eth_call'&&(Object.hasOwn(r,'response')!==Object.hasOwn(r,'error'))),'Every causal RPC must settle explicitly');return s};
 const operations=(s,names)=>check(eq(s.operations.map(o=>o.operation),names),'Exact causal operation sequence required: '+s.name);
 const step=(s,name)=>s.operations.find(o=>o.operation===name);
 const pending=causal('pending-chunk-clear');operations(pending,['readContract','mode','prefetch','event','mode','startMulticall','waitRPC','waitWrites','clear','releaseRPC','awaitAction']);
 check(pending.rpc.length===3&&step(pending,'waitRPC').snapshot.rpc===3&&step(pending,'waitRPC').snapshot.pending_rpc===1&&pending.rpc[2].request.params[0].data.startsWith('0x82ad56cb'),'Later multicall chunk must be admitted before held prefetched first chunk release');
 check(step(pending,'waitWrites').args===2&&step(pending,'waitWrites').snapshot.writes===2&&step(pending,'waitWrites').snapshot.pending_rpc===1,'Later chunk persistent write admission must precede first chunk release');
 check(step(pending,'clear').snapshot.pending_rpc===1&&eq(pending.operations.at(-1).result,[{$bigint:'41'},{$bigint:'41'}])&&pending.operations.at(-1).snapshot.writes===3&&pending.operations.every(o=>!o.error),'Clear must not invalidate already bound pending chunk result');
 const rejected=causal('aggregate-rejection-no-write');operations(rejected,['readContract','mode','prefetch','event','mode','startMulticall','waitRPC','releaseRPC','awaitAction']);
 check(rejected.rpc.length===3&&rejected.rpc[2].error&&step(rejected,'waitRPC').snapshot.pending_rpc===1&&step(rejected,'waitRPC').snapshot.writes===1&&rejected.operations.at(-1).error&&rejected.operations.at(-1).snapshot.writes===1&&rejected.store.filter(s=>s.operation==='insertRpcRequestResults').reduce((n,s)=>n+s.args.requests.length,0)===1,'Aggregate rejection must not persist earlier prefetched success');
 const held=causal('crosschain-held-db');operations(held,['readContract','dbMode','startPrefetch','waitRPC','releaseDB','awaitAction','waitRPC']);
 check(held.rpc.length===3&&eq(held.rpc.map(r=>r.chainId),[1,2,1])&&step(held,'waitRPC').snapshot.rpc===2&&step(held,'waitRPC').snapshot.pending_db===1&&held.operations.at(-1).snapshot.pending_db===0&&held.operations.every(o=>!o.error),'Independent chain2 RPC admission must precede held chain1 database release');
 const failed=causal('crosschain-failfast-sibling');operations(failed,['readContract','dbMode','startPrefetch','awaitAction','releaseDB','waitRPC']);
 check(failed.rpc.length===2&&eq(failed.rpc.map(r=>r.chainId),[1,1])&&step(failed,'awaitAction').error?.$error.message==='causal database rejection'&&step(failed,'awaitAction').snapshot.pending_db===1&&failed.operations.at(-1).snapshot.rpc===2&&failed.operations.at(-1).snapshot.pending_db===0,'Failfast lookup rejection must leave sibling effects alive after release');
 return {cases:2,profile_vectors:21,client_scenarios:21,operations:client.scenarios.reduce((n,s)=>n+s.operations.length,0),source_credit:0};
}
