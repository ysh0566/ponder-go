// Mapping only the eleven fully executed local-generator source cases. Three
// multichain/orchestrator cases remain unimplemented despite original execution.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {sourceFingerprint} from './go-source-fingerprint.mjs';
const root=path.resolve(import.meta.dirname,'..'),read=f=>JSON.parse(fs.readFileSync(root+'/'+f,'utf8'));
const hash=file=>crypto.createHash('sha256').update(fs.readFileSync(path.resolve(root,file))).digest('hex');
const valid=ref=>ref&&hash(ref.file)===ref.sha256;
const proof=read('parity/runs/source-runtime-historical-verification.json'),full=read(proof.full_verification.file);
if(!proof.complete||!proof.success||proof.scope!=='complete_module_source_runtime_historical'||!full.complete||!full.success||full.finished_native_inputs.sha256!==sourceFingerprint(root).sha256||proof.summary.complete_mapping_candidates!==11||proof.summary.unimplemented_source_cases!==3||proof.summary.targeted_assertions!==24)throw Error('Verified complete current M36 run required');
if(!valid(proof.full_verification)||!proof.inputs.every(valid)||!full.harness_inputs.every(valid))throw Error('Verified source/harness reference changed');
if(proof.executions.length!==4||['normal','race'].some(mode=>['postgres','pglite'].some(backend=>proof.executions.filter(e=>e.mode===mode&&e.backend===backend&&e.complete).length!==1)))throw Error('All four unique native backend/mode executions required');
for(const execution of proof.executions)if(!['log','capture','original','result','context_archive','source_order_probe','source_order_result','source_order_original','source_order_context'].every(key=>valid(execution[key])))throw Error('Native/source execution reference changed');
const targets=['1337ad403051f00c5d99','dd482eb7065250d54888','b259864fbc99fca7585d','6932328b1d777888e031','35500a04eb3407d46aa8','055e33151c56724c5459','eb47df94258c65be3a2e','1644fb814d5a0df1b23c','4a23b3323e9c3992e535','d685ca5ec1b8a49be6b3','42b8ee0fbd39ff105e51'];
const ledger=read('parity/test-ledger.json'),mappings=read('parity/mappings.json'),selected=ledger.tests.filter(t=>t.source_file==='packages/core/src/runtime/historical.test.ts');
if(selected.length!==14||selected.some(t=>t.type_error_directives.length)||selected.filter(t=>targets.includes(t.id)).length!==11||selected.filter(t=>targets.includes(t.id)).reduce((n,t)=>n+t.assertions.length,0)!==24)throw Error('Pinned M36 census changed');
for(const test of selected){
 if(!targets.includes(test.id)){if(mappings.some(m=>m.source_id===test.id))throw Error('Unimplemented later scheduler case must remain unmapped');continue;}
 const names=Object.fromEntries(['postgres','pglite'].map(backend=>[backend,'TestSourceRuntimeHistoricalOriginal/'+backend+'/'+test.title.replaceAll(/\s/g,'_')]));
 if(!proof.executions.every(e=>e.cases.length===11&&e.cases.filter(c=>c.source_id===test.id&&c.go_test===names[e.backend]&&c.runtime_passed&&c.native_type_obligations===0).length===1))throw Error('Four current original-case executions required');
 const mapping={source_file:test.source_file,source_name:test.full_name,source_id:test.id,source_line:test.line,assertions_sha256:crypto.createHash('sha256').update(test.assertions.map(assertion=>assertion.text).join('\n')).digest('hex'),original_assertions:test.assertions.length,target_file:'runtime/source_local_historical_test.go',go_package:'ponder.local/ponder-go/runtime',go_test:names.postgres,backend_companion:names.pglite,source_fixture:'testdata/runtime/source-runtime-historical-live.json',source_verification:'parity/runs/source-runtime-historical-verification.json',assertion_policy:'Complete pinned historical.test.ts executes all14 unchanged original declarations/hooks/inputs/assertions with explicit loopback startup adaptation, not pristine. These11 local generator cases replay14 actual calls and24 actual provider requests on each real database backend, normal and race, comparing generator steps/outputs, progress identity, checkpoints and physical database state. Native state starts empty and sequential calls execute production behavior; recorded outputs/rows never seed it. Three later scheduler cases remain unmapped; misnamed original omnichain case is not true omnichain coverage. Supplemental actual-RPC prefetch, rollback, cancellation and error controls add zero source credit. Native PGlite atomicity/cancellation/error joining are documented safety extensions beyond pinned source boundaries.'};
 const existing=mappings.find(m=>m.source_id===test.id);if(existing&&JSON.stringify(existing)!==JSON.stringify(mapping))throw Error('Conflicting existing mapping');if(!existing)mappings.push(mapping);
}
fs.writeFileSync(root+'/parity/mappings.json',JSON.stringify(mappings,null,2)+'\n');console.log({added_or_verified:11,unimplemented_source_cases:3});
