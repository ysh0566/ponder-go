// Mapping is gated by complete current runtime evidence; pending native type
// obligations remain unmapped rather than being converted into fabricated skips.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {sourceFingerprint} from './go-source-fingerprint.mjs';
const root=path.resolve(import.meta.dirname,'..'),read=f=>JSON.parse(fs.readFileSync(root+'/'+f,'utf8'));
const proof=read('parity/runs/source-runtime-index-verification.json'),full=read(proof.full_verification.file);
if(!proof.complete||!proof.success||proof.scope!=='complete_module_source_runtime_index'||!full.complete||!full.success||full.finished_native_inputs.sha256!==sourceFingerprint(root).sha256||proof.summary.complete_mapping_candidates!==11||proof.summary.native_type_pending_cases!==4)throw Error('Verified complete current run required before mapping');
const actualHash=crypto.createHash('sha256').update(fs.readFileSync(root+'/'+proof.full_verification.file)).digest('hex');
if(actualHash!==proof.full_verification.sha256)throw Error('Full verification reference changed');
const ledger=read('parity/test-ledger.json'),mappings=read('parity/mappings.json'),selected=ledger.tests.filter(t=>t.source_file==='packages/core/src/runtime/index.test.ts');
if(selected.length!==15||selected.filter(t=>t.type_error_directives.length===0).length!==11)throw Error('Pinned census changed');
for(const test of selected){
 if(test.type_error_directives.length){if(mappings.some(m=>m.source_id===test.id))throw Error('Pending native type case must remain unmapped');continue;}
 const names=Object.fromEntries(['postgres','pglite'].map(backend=>[backend,'TestSourceRuntimeIndexFullFile/'+backend+'/'+test.title.replaceAll(/\s/g,'_')]));
 if(!proof.executions.every(e=>e.cases.some(c=>c.source_id===test.id&&c.go_test===names[e.backend]&&c.runtime_passed)))throw Error('Four current case executions required');
 const mapping={source_file:test.source_file,source_name:test.full_name,source_id:test.id,source_line:test.line,assertions_sha256:crypto.createHash('sha256').update(test.assertions.map(assertion=>assertion.text).join('\n')).digest('hex'),original_assertions:test.assertions.length,target_file:'runtime/source_index_live_test.go',go_package:'ponder.local/ponder-go/runtime',go_test:names.postgres,backend_companion:names.pglite,source_fixture:'testdata/runtime/source-runtime-index-live.json',source_verification:'parity/runs/source-runtime-index-verification.json',assertion_policy:'Complete pinned original file and unchanged hooks/input declarations/assertions with explicit loopback startup adaptation, never called pristine. Each native backend replays its own full-file capture, actual provider inputs, cache SQL, event construction and iterator operations, comparing returned values, own properties, input mutation and object identity. Full normal/race use the same prepared input inventory. Supplemental concurrency, cancellation, retry and tamper controls add zero source credit. Four separate original cases with six source-language Map inference directives remain unmapped pending native type treatment; no invented negative controls.'};
 const existing=mappings.find(m=>m.source_id===test.id);if(existing&&JSON.stringify(existing)!==JSON.stringify(mapping))throw Error('Conflicting existing mapping');if(!existing)mappings.push(mapping);
}
fs.writeFileSync(root+'/parity/mappings.json',JSON.stringify(mappings,null,2)+'\n');console.log({added_or_verified:11,native_type_pending_cases:4});
