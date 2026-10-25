import {indexingActionsSupplementalTests,indexingActionsAdmissionScenarios,indexingActionsStoreTests,indexingActionsStoreScenarios} from './indexing-actions-acceptance.mjs';
// Exactly eight unchanged original declarations; supplemental tests earn no credit.
import fs from 'node:fs';import path from 'node:path';import crypto from 'node:crypto';
import {sourceFingerprint} from './go-source-fingerprint.mjs';
import {indexingActionsTargets} from './source-indexing-actions-evidence.mjs';
const root=path.resolve(import.meta.dirname,'..'),read=f=>JSON.parse(fs.readFileSync(path.resolve(root,f))),hash=f=>crypto.createHash('sha256').update(fs.readFileSync(path.resolve(root,f))).digest('hex');
const valid=r=>r&&fs.existsSync(path.resolve(root,r.file))&&hash(r.file)===r.sha256;
const file=process.argv.find(a=>a.startsWith('--proof='))?.slice(8);
if(!file||path.resolve(root,file)!==root+'/parity/runs/source-indexing-actions-verification.json')throw Error('Explicit canonical M39 proof required');
const proof=read(file),full=read(proof.full_verification.file),s=proof.summary;
if(!proof.complete||!proof.success||proof.scope!=='complete_module_source_indexing_actions'||!proof.environment_adapted||proof.pristine_file!==false||s.source_cases!==23||s.source_assertions!==42||s.targeted_cases!==8||s.targeted_assertions!==9||s.source_error_directives!==0||s.complete_mapping_candidates!==8||s.supplementary_source_credit!==0)throw Error('Complete eight original case proof required');
if(!full.complete||!full.success||full.finished_native_inputs.sha256!==sourceFingerprint(root).sha256||![proof.full_verification,proof.prepared_native_inputs,proof.context_archive,proof.previous_handler_proof,proof.source_only,...proof.inputs,...full.harness_inputs].every(valid))throw Error('Current immutable complete proof required');
if(proof.executions.length!==4||['normal','race'].some(mode=>['postgres','pglite'].some(backend=>proof.executions.filter(e=>e.mode===mode&&e.backend===backend&&e.complete).length!==1)))throw Error('Four unique mode/backend executions required');
if(JSON.stringify(proof.supplemental_tests)!==JSON.stringify(indexingActionsSupplementalTests)||JSON.stringify(proof.admission_scenarios)!==JSON.stringify(indexingActionsAdmissionScenarios)||JSON.stringify(proof.store_tests)!==JSON.stringify(indexingActionsStoreTests)||JSON.stringify(proof.store_scenarios)!==JSON.stringify(indexingActionsStoreScenarios))throw Error('Exact supplemental census required');
const ledger=read('parity/test-ledger.json'),mappings=read('parity/mappings.json'),selected=ledger.tests.filter(t=>indexingActionsTargets.includes(t.id));
if(selected.length!==8||selected.reduce((n,t)=>n+t.assertions.length,0)!==9||selected.some(t=>t.type_error_directives.length!==0))throw Error('Exact pinned declarations required');
for(const e of proof.executions){
 if(![e.log,e.native,e.artifact,e.probe_artifact,e.capture,e.original,e.result,e.context_archive,...e.source_context,...Object.values(e.probe).filter(v=>v?.file),...e.probe.source_context].every(valid)||e.cases.length!==8)throw Error('Current complete execution references required');
 const events=fs.readFileSync(path.resolve(root,e.log.file),'utf8').trim().split('\n').map(JSON.parse),passed=(n,pkg='ponder.local/ponder-go/indexing')=>events.filter(x=>x.Package===pkg&&x.Test===n&&x.Action==='pass').length===1;
 for(const t of selected){const cases=e.cases.filter(c=>c.source_id===t.id),c=cases[0],name='TestSourceIndexingActions/'+e.backend+'/'+t.id;if(cases.length!==1||c.go_test!==name||!c.runtime_passed||!c.formal_credit||c.native_type_obligations!==0||!passed(name))throw Error('Exact original pass required');}
 for(const name of indexingActionsSupplementalTests)if(!passed(name))throw Error('Current supplemental missing');
 for(const name of indexingActionsAdmissionScenarios)if(!passed('TestClientRealCacheAdmissionLifecycle/'+e.backend+'/'+name))throw Error('Current real admission lifecycle missing');
 for(const name of indexingActionsStoreTests)if(!passed(name,'ponder.local/ponder-go/store'))throw Error('Current store supplemental missing');
 for(const name of indexingActionsStoreScenarios)if(!passed('TestSourceRPCAsyncBackends/'+e.backend+'/'+name,'ponder.local/ponder-go/store'))throw Error('Current actual store lifecycle missing');
 if(!passed('TestSourceIndexingActionsProbes/'+e.backend))throw Error('Real supplemental probe required');
}
for(const t of selected){
 const mapping={source_file:t.source_file,source_name:t.full_name,source_id:t.id,source_line:t.line,assertions_sha256:crypto.createHash('sha256').update(t.assertions.map(a=>a.text).join('\n')).digest('hex'),original_assertions:t.assertions.length,target_file:'indexing/source_actions_test.go',go_package:'ponder.local/ponder-go/indexing',go_test:'TestSourceIndexingActions/postgres/'+t.id,backend_companion:'TestSourceIndexingActions/pglite/'+t.id,source_fixture:'testdata/indexing/source-indexing-actions-live.json.gz',source_verification:'parity/runs/source-indexing-actions-verification.json',assertion_policy:'Complete unchanged 23-case indexing/index.test.ts executes on both source backends; final eight public action cases and nine assertions replay in complete native normal/race on PostgreSQL/PGlite from original ABI/options/event inputs and actual RPC boundary tapes. Actual ABI encode/decode, miss-only aggregate3 requests, provider counts/params, return/error outcomes and persistent child cache rows are checked. Real partial-cache/duplicates/order/errors/immutable probes, cache admission lifecycle and descriptor reflection are zero-credit supplements. No cachedViemClient profiling/prefetch/LRU, full indexing/client.test.ts, or three prior TS-only static obligations claimed.'};
 const existing=mappings.find(m=>m.source_id===t.id);if(existing&&JSON.stringify(existing)!==JSON.stringify(mapping))throw Error('Conflicting existing mapping');if(!existing)mappings.push(mapping);
}
fs.writeFileSync(root+'/parity/mappings.json',JSON.stringify(mappings,null,2)+'\n');console.log({added_or_verified:8,supplementary_source_credit:0});
