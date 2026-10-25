export const indexingActionsTargets=['9614c3868c45f1ab5064','f62a1b73602a98909575','8244ed823366e5a1da54','ee3742b77c929d94a8ed','4dfbaabe2e3b99f00b8d','175d1d5d0afe91cc20a1','de123b064923c7ffd321','4204e282e98522f9eefa'];
const equal=(a,b)=>JSON.stringify(a)===JSON.stringify(b);
export function assertIndexingActionsCapture(capture,declarations){
 const check=(v,m)=>{if(!v)throw Error(m)};
 check(capture.source_credit===0&&capture.format==='indexing-actions-source-v1'&&capture.cases.length===23,'Whole original23 capture required');
 check(equal(capture.cases.filter(c=>c.targeted).map(c=>c.id),indexingActionsTargets),'Exact final8 original actions required');
 let providers=0,rows=0;
 for(const c of capture.cases){const d=declarations.find(d=>d.id===c.id);check(d&&['id','source_file','full_name','assertions','type_error_directives'].every(k=>equal(c[k],d[k])),'Original declaration mismatch '+c.id);if(!c.targeted)continue;
  check(c.actions.length===1&&c.clients.length===1&&c.actions[0].args.length===1&&!c.actions[0].error,'Exactly one successful original action required');
  const a=c.actions[0],expected=c.id==='175d1d5d0afe91cc20a1'?2:1;
  check(a.providers.length===expected&&a.providers.every(p=>p.request.method&&p.request.params&&Object.hasOwn(p,'response')),'Exact genuine RPC boundary count '+c.id);
  check(c.physical_after.length===1,'One deduplicated persisted cache row required '+c.id);
  check(c.store.some(s=>s.operation==='getRpcRequestResults')&&c.store.some(s=>s.operation==='insertRpcRequestResults'&&!s.error),'Actual cache get/put required');
  if(a.operation==='multicall')check(a.providers[0].request.params[0].data.startsWith('0x82ad56cb')&&a.result.length===2,'Genuine aggregate3 and duplicate results required');
  if(c.id==='175d1d5d0afe91cc20a1')check(a.providers[0].response==='0x'&&a.providers[1].response!=='0x','Original one-zero-data retry required');
  providers+=a.providers.length;rows+=c.physical_after.length;
 }
 const targets=declarations.filter(d=>indexingActionsTargets.includes(d.id));check(targets.length===8&&targets.reduce((n,c)=>n+c.assertions.length,0)===9&&targets.every(c=>c.type_error_directives.length===0),'Exact9 assertion0directive census required');
 return {targeted:8,assertions:9,type_directives:0,providers,rows};
}
export const indexingActionsProbeTitle='actual actions partial cache duplicates order failure and immutable vectors';
export function assertIndexingActionsProbeCapture(capture){
 const check=(v,m)=>{if(!v)throw Error(m)};
 check(capture.source_credit===0&&capture.real_rpc===true&&capture.real_database===true&&capture.cases?.length===1,'Actual zero-credit DB/RPC probe required');
 const c=capture.cases[0],a=c.actions;
 check(c.source_credit===0&&c.name==='actual-partial-cache-duplicates-failure'&&a?.length===11,'Exact eleven-action probe required');
 check(equal(a.map(x=>x.operation),['readContract','multicall','multicall','multicall','multicall','multicall','readContract','getCode','getCode','multicall','multicall']),'Exact probe action order required');
 check(equal(a.map(x=>x.providers.length),[1,1,0,1,1,0,1,1,1,2,1]),'Exact actual RPC counts required');
 check(equal(a.map(x=>x.physical_after.length),[1,2,2,2,2,2,3,3,3,4,4]),'Only successful nonempty children persisted required');
 check(equal(a[1].result,[{$bigint:'2000000000000000000'},{$bigint:'1000000000000000000'},{$bigint:'2000000000000000000'}])&&equal(a[2].result,[{$bigint:'1000000000000000000'},{$bigint:'2000000000000000000'},{$bigint:'1000000000000000000'}]),'Partial cache duplicate ordering required');
 check(equal(a[3].result.map(r=>r.status),['success','failure','success'])&&a[3].result[1].error?.$error&&a[4].error?.$error,'Real aggregate child failure both allowFailure modes required');
 for(const i of [1,3,4]){const data=a[i].providers[0].request.params[0].data;check(data.startsWith('0x82ad56cb'),'Real aggregate3 request required');check(Number(BigInt('0x'+data.slice(74,138)))===(i===1?2:1),'Only uncached children admitted to aggregate3 required');}
 check(equal(a[5].result,[])&&a[6].providers[0].request.params[1]==='latest','Empty calls and immutable override required');
 for(const i of [7,8])check(a[i].result.$undefined===true&&a[i].providers[0].response==='0x','Empty code never cached required');
 check(a[9].args[0].batchSize===4&&a[9].args[0].blockNumber.$bigint==='4'&&a[9].error?.$error?.cause_name==='ContractFunctionRevertedError','Failed first chunk with explicit fresh block required');
 for(const p of a[9].providers)check(p.request.params[1]==='0x4'&&p.request.params[0].data.startsWith('0x82ad56cb')&&Number(BigInt('0x'+p.request.params[0].data.slice(74,138)))===1,'Both individual aggregate chunks must execute');
 check(a[10].result[0].status==='failure'&&a[10].result[0].error.$error.cause_name==='ContractFunctionZeroDataError'&&a[10].result[0].error.$error.short_message.includes('returned no data'),'Encoder placeholder must surface wrapped zero-data, not original encode error');
 for(const e of [a[3].result[1].error,a[4].error,a[9].error])check(e.$error.name==='ContractFunctionExecutionError'&&e.$error.cause_name==='ContractFunctionRevertedError','Typed source revert cause required');
 return {cases:1,actions:11,providers:10,source_credit:0};
}
