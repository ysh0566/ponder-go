// Only the final three original declarations earn credit; supplemental probes earn none.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {multichainTargets,multichainProbeNames} from './source-runtime-multichain-evidence.mjs';
import {sourceFingerprint} from './go-source-fingerprint.mjs';
const root=path.resolve(import.meta.dirname,'..'),read=f=>JSON.parse(fs.readFileSync(root+'/'+f,'utf8'));
const hash=file=>crypto.createHash('sha256').update(fs.readFileSync(path.resolve(root,file))).digest('hex');
const valid=ref=>ref&&hash(ref.file)===ref.sha256;
const proof=read('parity/runs/source-runtime-multichain-verification.json'),full=read(proof.full_verification.file);
if(!proof.complete||!proof.success||proof.environment_adapted!==true||proof.pristine_file!==false||proof.summary.source_cases!==14||proof.summary.source_assertions!==29||proof.summary.targeted_cases!==3||proof.summary.source_error_directives!==0||proof.scope!=='complete_module_source_runtime_multichain'||!full.complete||!full.success||full.finished_native_inputs.sha256!==sourceFingerprint(root).sha256||proof.summary.complete_mapping_candidates!==3||proof.summary.earlier_separately_mapped_cases!==11||proof.summary.targeted_assertions!==5)throw Error('Verified complete current M37 run required');
if(!valid(proof.full_verification)||!proof.inputs.every(valid)||!full.harness_inputs.every(valid))throw Error('Verified source/harness reference changed');
if(proof.executions.length!==4||['normal','race'].some(mode=>['postgres','pglite'].some(backend=>proof.executions.filter(e=>e.mode===mode&&e.backend===backend&&e.complete).length!==1)))throw Error('All four unique native backend/mode executions required');
for(const execution of proof.executions)if(!['log','capture','original','result','context_archive','supplemental_probe','supplemental_result','supplemental_original','supplemental_context'].every(key=>valid(execution[key])))throw Error('Native/source execution reference changed');
if(proof.summary.supplemental_probe_cases!==multichainProbeNames.length||proof.summary.supplementary_source_credit!==0)throw Error('Exact zero-credit supplemental proof required');
for(const e of proof.executions){
 if(![...e.source_context,...e.supplemental_source_context].every(valid))throw Error('Current source driver changed');
 if(JSON.stringify(e.supplemental_cases?.map(c=>c.name))!==JSON.stringify(multichainProbeNames))throw Error('Supplemental census changed');
 const events=fs.readFileSync(path.resolve(root,e.log.file),'utf8').trim().split('\n').map(JSON.parse);
 for(const c of [...e.cases,...e.supplemental_cases])if(events.filter(event=>event.Package==='ponder.local/ponder-go/runtime'&&event.Test===c.go_test&&event.Action==='pass').length!==1)throw Error('Current native leaf pass required');
 if(e.supplemental_cases.some(c=>c.source_credit!==0||c.go_test!=='TestSourceRuntimeMultichainProbes/'+e.backend+'/'+c.name.replaceAll(/\s/g,'_')))throw Error('Supplemental identity changed');
}
const targets=multichainTargets;
const ledger=read('parity/test-ledger.json'),mappings=read('parity/mappings.json'),selected=ledger.tests.filter(t=>t.source_file==='packages/core/src/runtime/historical.test.ts');
if(selected.length!==14||selected.some(t=>t.type_error_directives.length)||selected.filter(t=>targets.includes(t.id)).length!==3||selected.filter(t=>targets.includes(t.id)).reduce((n,t)=>n+t.assertions.length,0)!==5)throw Error('Pinned M37 census changed');
for(const test of selected){
 if(!targets.includes(test.id))continue;
 const names=Object.fromEntries(['postgres','pglite'].map(backend=>[backend,'TestSourceRuntimeMultichainOriginal/'+backend+'/'+test.title.replaceAll(/\s/g,'_')]));
 if(!proof.executions.every(e=>e.cases.length===3&&e.cases.filter(c=>c.source_id===test.id&&c.go_test===names[e.backend]&&c.runtime_passed&&c.native_type_obligations===0).length===1))throw Error('Four current original-case executions required');
 const mapping={source_file:test.source_file,source_name:test.full_name,source_id:test.id,source_line:test.line,assertions_sha256:crypto.createHash('sha256').update(test.assertions.map(assertion=>assertion.text).join('\n')).digest('hex'),original_assertions:test.assertions.length,target_file:'runtime/source_runtime_multichain_test.go',go_package:'ponder.local/ponder-go/runtime',go_test:names.postgres,backend_companion:names.pglite,source_fixture:'testdata/runtime/source-runtime-multichain-live.json',source_verification:'parity/runs/source-runtime-multichain-verification.json',assertion_policy:'Complete pinned historical.test.ts executes all 14 unchanged declarations and 29 assertions under explicit loopback startup adaptation. Only these three original cases and five assertions earn M37 credit. Each backend replays three actual coordinator calls, seven provider requests and six direct clock observations in full normal/race runs. All three originals are single-chain Multichain calls, including the misleadingly named omnichain case. Mandatory supplemental genuine multichain, omnichain, isolated, refetch and checkpoint probes earn zero source credit. Native state is produced from original inputs and raw RPC; outputs and physical snapshots are verification-only. Outer run-loop transactions, realtime cutover and readiness are not covered. Native PGlite atomicity is a documented safety extension, not identical source transaction failure behavior.'};
 const existing=mappings.find(m=>m.source_id===test.id);if(existing&&JSON.stringify(existing)!==JSON.stringify(mapping))throw Error('Conflicting existing mapping');if(!existing)mappings.push(mapping);
}
fs.writeFileSync(root+'/parity/mappings.json',JSON.stringify(mappings,null,2)+'\n');console.log({added_or_verified:3,supplementary_source_credit:0});
