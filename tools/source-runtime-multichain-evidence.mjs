// M37 preserves immutable original declarations. Supplemental two-chain probes
// are mandatory safety/behavior evidence, but never original-case credit.
import {sameOriginalDeclaration} from './source-runtime-historical-evidence.mjs';
export {sameOriginalDeclaration,assertOriginalDeclaration,sourceDeclarationFields} from './source-runtime-historical-evidence.mjs';
export const multichainTargets=Object.freeze(['d1f4e702f072c3b5eae1','983c1f8526efc67c34aa','a2f204a8ab7598691e12']);
export function assertMultichainOriginalCapture(capture,declarations){
 const check=(value,message)=>{if(!value)throw Error(message)};
 check(declarations.length===14&&declarations.reduce((n,c)=>n+c.assertions.length,0)===29&&declarations.every(c=>c.type_error_directives.length===0),'Pinned historical declaration census changed');
 check(capture?.cases?.length===14&&new Set(capture.cases.map(c=>c.id)).size===14,'Complete unique original capture required');
 let calls=0,providers=0,clocks=0,assertions=0;
 for(const declaration of declarations){
  const item=capture.cases.find(c=>c.id===declaration.id),targeted=multichainTargets.includes(declaration.id);
  check(sameOriginalDeclaration(item,declaration),'Immutable original declaration changed: '+declaration.id);
  check(item.targeted===targeted&&item.calls.length>0,'Exact target selection and original calls required');
  if(!targeted){check(item.calls.every(call=>['getLocalEventGenerator','getLocalInMemoryEventGenerator','getLocalSyncGenerator'].includes(call.operation)),'Earlier local calls belong to separate M36 proof');continue}
  assertions+=declaration.assertions.length;check(item.calls.length===1,'One original coordinator call per target required');
  for(const call of item.calls){
   check(call.operation==='getHistoricalEventsMultichain'&&!call.unimplemented&&!call.error&&call.args&&call.after,'Complete original multichain call required');
   check(call.steps?.length>0&&call.steps.length===call.results?.length&&call.steps.every(step=>step.operation==='next'),'Complete original next/result sequence required');
   check(Object.keys(call.physical_before??{}).length===9&&Object.keys(call.physical_after??{}).length===9&&Object.values(call.physical_before).every(rows=>Array.isArray(rows)&&rows.length===0),'Native starts empty; all nine tables must be observed');
   check(Array.isArray(call.providers)&&call.providers.every(p=>p.chainId!==undefined&&p.request!==undefined&&p.response!==undefined&&!p.error),'Original chain-tagged raw providers required');
   check(Array.isArray(call.clocks)&&call.clocks.length===2&&call.clocks.every(Number.isFinite),'Original Date.now admission/threshold observations required');
   calls++;providers+=call.providers.length;clocks+=call.clocks.length;
  }
 }
 check(calls===3&&providers===7&&clocks===6&&assertions===5,'Pinned three-case/five-assertion/call/RPC/clock census changed');
 return {calls,providers,clocks,assertions};
}

export const multichainProbeNames=Object.freeze(['two-chain-completion-mixed-cache','two-chain-pending','two-chain-pending-catchup','two-chain-multichain-catchup','real-finalized-backward-multichain','real-finalized-backward-omnichain','real-finalized-backward-isolated','two-chain-refetch','isolated-memory-stop','refetch-all-filters-extras','refetch-sparse-slots','aggregate-checkpoint-tags','cross-chain-crash-safe-cache','cross-chain-crash-uncached-start','bounded-end-empty-chain','latest-concurrent-reverse-completion']);
export function assertMultichainProbeCapture(capture,backend){
 const check=(value,message)=>{if(!value)throw Error(message)};
 check(capture?.source_credit===0&&capture.backend===backend&&JSON.stringify(capture.cases?.map(c=>c.name))===JSON.stringify(multichainProbeNames),"Exact supplemental names/order/backend required");
 const modes={'two-chain-pending':'omnichain','two-chain-pending-catchup':'omnichain','real-finalized-backward-omnichain':'omnichain','real-finalized-backward-isolated':'isolated','isolated-memory-stop':'isolated','memory-block-trace-first-error':'isolated'};
 const observation=(c,firstError=false)=>{
  check(c.source_credit===0&&c.real_database===true&&c.independent_anvil_instances===true&&c.args&&c.after&&c.physical_before&&c.physical_after&&Array.isArray(c.providers)&&(c.providers.length>0||c.name==='cross-chain-crash-safe-cache')&&c.steps?.length>0&&Array.isArray(c.results)&&(firstError?c.steps.length===c.results.length+1:c.results.length>0)&&c.clocks?.length>0&&c.clocks.every(Number.isFinite),"Incomplete real-source probe: "+c.name);
  const object=value=>value?.$object?Object.fromEntries(value.$object):value;
  const tables=['intervals','factories','factoryAddresses','blocks','logs','transactions','transactionReceipts','traces','rpcRequestResults'];
  for(const snapshot of [c.physical_before,c.physical_after]){const rows=object(snapshot);check(rows&&Object.keys(rows).length===9&&tables.every(table=>Array.isArray(rows[table])),"All nine real database table snapshots required");}
  const chains=object(c.args)?.chains;
  check(Array.isArray(chains)&&chains.length>0,"Original chain input list required");
  const chainIDs=chains.map(chain=>object(chain)?.id);
  check(chainIDs.every(id=>Number.isSafeInteger(id)&&id>0)&&new Set(chainIDs).size===chainIDs.length,"Unique original chain identities required");
  check(c.providers.every(p=>chainIDs.includes(p.chainId)&&Array.isArray(p.request)&&p.request.length>0&&typeof object(p.request[0])?.method==='string'&&((Object.hasOwn(p,'response')&&p.response!==undefined&&!p.error)||(firstError&&((firstError==='memory'&&p.chainId===1&&object(p.request[0]).method==='debug_traceBlockByNumber'&&object(p.request[0]).params?.length===2&&object(p.request[0]).params[0]==='0x0'&&JSON.stringify(object(object(p.request[0]).params[1]))===JSON.stringify({tracer:'callTracer'})&&p.error?.$error?.name==='Error'&&p.error.$error.message==='M37 injected trace failure')||(firstError===true&&p.chainId===2&&object(p.request[0]).method==='eth_getBlockByNumber'&&object(p.request[0]).params?.[0]==='latest'&&p.error?.$error?.name==='Error'&&p.error.$error.message==='M37 injected latest failure'))&&!Object.hasOwn(p,'response')))),"Completed chain-tagged raw provider requests required");
  check(c.mode===(modes[c.name]??'multichain'),"Source probe mode changed");
  check(Boolean(c.error)===(c.name==="real-finalized-backward-omnichain"||Boolean(firstError)),"Unexpected source error state");
 };
 for(const c of capture.cases){
  observation(c);
  if(['cross-chain-crash-safe-cache','cross-chain-crash-uncached-start'].includes(c.name)){
   check(c.setup_calls?.length===1&&c.setup_calls[0].name==='cross-chain-crash-prime',"Actual crash setup call required");observation(c.setup_calls[0]);
   // Separate physical snapshots allocate fresh identity IDs; compare every stored value.
   check(JSON.stringify(c.setup_calls[0].physical_after,(key,value)=>key==='$id'?undefined:value)===JSON.stringify(c.physical_before,(key,value)=>key==='$id'?undefined:value),"Crash state must continue from actual setup output");
  }else check(!c.setup_calls?.length,"Unexpected setup calls");
  if(c.name==='latest-concurrent-reverse-completion')check(JSON.stringify(c.latestAdmission?.timeline)===JSON.stringify([{kind:'begin',chainId:1},{kind:'begin',chainId:2},{kind:'finish',chainId:2},{kind:'finish',chainId:1}]),"Actual parallel admission/reverse completion required");
  if(c.name==='latest-concurrent-reverse-completion'){
   const e=c.error_call;check(e?.name==='latest-first-error',"Concrete first-error subcall required");observation(e,true);
   check(JSON.stringify(c.physical_after,(key,value)=>key==='$id'?undefined:value)===JSON.stringify(e.physical_before,(key,value)=>key==='$id'?undefined:value),"First-error subcall must continue from actual main-call state");
   check(e.error?.$error?.name==='Error'&&e.error.$error.message==='M37 injected latest failure',"Exact injected iterator error required");
   check(JSON.stringify(e.controls?.latestFirstError)===JSON.stringify({heldChainId:1,errorChainId:2,message:'M37 injected latest failure'}),"Exact first-error input control required");
   const events=e.control_events,one=(kind,chainId)=>{const found=events?.map((x,i)=>({x,i})).filter(({x})=>x.kind===kind&&(chainId===undefined||x.chainId===chainId));check(found?.length===1,'Unique first-error event required: '+kind);return found[0].i};
   const begin1=one('latest_begin',1),begin2=one('latest_begin',2),failed=one('latest_error',2),caught=one('iterator_error'),cleanup=one('cleanup_release'),released=one('latest_released',1);
   check(begin1<failed&&begin2<failed&&failed<caught&&caught<cleanup&&cleanup<released,"First error must reject before held sibling cleanup release");
   const unwrap=value=>value?.$object?Object.fromEntries(value.$object):value;
   check(e.providers.filter(p=>p.chainId===1&&unwrap(p.request[0]).method==='eth_getBlockByNumber'&&unwrap(p.request[0]).params?.[0]==='latest'&&Object.hasOwn(p,'response')&&!p.error).length===1,"Held sibling raw RPC must complete during cleanup");
   check(e.providers.filter(p=>p.error).length===1,"Exactly one injected provider error required");
  }else check(!c.error_call,"Unexpected error subcall");
  if(c.name==='isolated-memory-stop'){
   const e=c.memory_error_call;check(e?.name==='memory-block-trace-first-error',"Concrete memory first-error subcall required");observation(e,'memory');
   check(e.steps.length===1&&e.steps[0].operation==='next'&&e.results.length===0,"Memory first next must reject before any result");
   check(e.error?.$error?.name==='Error'&&e.error.$error.message==='M37 injected trace failure',"Exact injected memory iterator error required");
   check(JSON.stringify(e.controls?.memoryFirstError)===JSON.stringify({chainId:1,blockNumber:0,message:'M37 injected trace failure'}),"Exact memory first-error input control required");
   check(JSON.stringify(c.physical_after,(key,value)=>key==='$id'?undefined:value)===JSON.stringify(e.physical_before,(key,value)=>key==='$id'?undefined:value),"Memory error must continue from actual original stop state");
   const events=e.control_events,one=kind=>{const found=events?.map((x,i)=>({x,i})).filter(({x})=>x.kind===kind);check(found?.length===1,'Unique memory error event required: '+kind);return found[0].i};
   const block=one('block_begin'),trace=one('trace_begin'),failed=one('trace_error'),caught=one('iterator_error'),cleanup=one('cleanup_release'),released=one('block_released');
   check([block,trace,failed,released].every(i=>events[i].chainId===1&&events[i].blockNumber===0),"Exact block-zero chain-one causal events required");
   check(block<failed&&trace<failed&&failed<caught&&caught<cleanup&&cleanup<released,"Trace error must reject before held block cleanup release");
   const unwrap=value=>value?.$object?Object.fromEntries(value.$object):value;
   check(e.providers.filter(p=>p.chainId===1&&unwrap(p.request[0]).method==='eth_getBlockByNumber'&&JSON.stringify(unwrap(p.request[0]).params)===JSON.stringify(['0x0',true])&&Object.hasOwn(p,'response')&&!p.error).length===1,"Held block response must complete during cleanup");
   check(e.providers.filter(p=>p.error).length===1,"Exactly one injected trace provider error required");
  }else check(!c.memory_error_call,"Unexpected memory error subcall");


 }
}

export const multichainProbeTitles=Object.freeze(["two independent chains: completion order and mixed cache", "two chains: omnichain pending boundary", "two chains: omnichain catchup releases pending prefix", "two chains: multichain catchup", "real finalized rollback: multichain", "real finalized rollback: omnichain", "real finalized rollback: isolated", "two chains: refetch preserves original cross-chain positions", "isolated in-memory and consumer early return", "refetch all filters includes extras and assigns positionally", "refetch no matching filters retains sparse chain slots", "aggregate checkpoints exclude completed chains and bound end", "cross-chain crash safe-block with cache true", "cross-chain crash safe-block with cache false", "two chains: bounded end and no matching event chain", "latest refresh admits both chains then reverse completion"]);
export function assertMultichainProbeOriginal(original,expectedFile){
 const check=(value,message)=>{if(!value)throw Error(message)};
 check(original?.success===true&&original.numTotalTests===16&&original.numPassedTests===16&&original.numFailedTests===0&&original.numPendingTests===0&&original.numTodoTests===0,'Complete exact supplemental original census required');
 check(original.testResults?.length===1&&original.testResults[0].name===expectedFile&&original.testResults[0].status==='passed','Exact supplemental source test file required');
 const cases=original.testResults[0].assertionResults;
 check(cases?.length===16&&multichainProbeTitles.every(title=>cases.filter(c=>c.title===title&&c.fullName===title&&c.status==='passed'&&Array.isArray(c.ancestorTitles)&&c.ancestorTitles.length===0&&Array.isArray(c.failureMessages)&&c.failureMessages.length===0).length===1),'Exact unique passed supplemental declarations required');
}
