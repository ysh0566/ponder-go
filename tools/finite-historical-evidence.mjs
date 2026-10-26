import {allEvidenceReferencesValid} from './independent-storage-evidence.mjs';
import {atomicMultichainProofReferencesValid} from './multichain-atomic-evidence.mjs';
export function finiteHistoricalProofReferencesValid(proof,valid){
 if(proof?.scope!=='finite_default_multichain_historical_runner'||proof.source_credit!==0||!proof.previous_atomic_proof||proof.finite_finalized_ranges_only!==true)return false;
 if(!finiteHistoricalSourceScopesValid(proof.source_scopes))return false;
 if(proof.readiness_cutover_completed!==false||proof.cross_domain_atomic_rewind!==false)return false;
 const predecessor={...proof,scope:'atomic_multichain_setup_and_historical_group_primitives',previous_independent_proof:proof.previous_atomic_proof};
 return atomicMultichainProofReferencesValid(predecessor,valid)&&allEvidenceReferencesValid(proof,valid);
}

export function finiteHistoricalSourceScopesValid(scopes){
 if(!Array.isArray(scopes)||scopes.length!==2)return false;
 for(const [index,backend] of ['postgres','pglite'].entries()){
  const s=scopes[index],expected=backend==='pglite',b=s?.default_cache_budget;
  if(s?.backend!==backend||s.default_completed!==true||s.pressure_configured_events!==240||s.pressure_completed!==!expected||s.pressure_outcome!==(expected?'expected_upstream_error':'completed')||s.source_error_observed!==expected||s.diagnostic_abort!==false||s.full_backoff_terminal_observed!==expected)return false;
  if(b?.source!=='buildOptions_v8_heap_default'||b.environmentOverrideAbsent!==true||b.allRuntimeOptionsDefault!==false||b.syncEventsQuerySize!==0||!Number.isSafeInteger(b.heapSizeLimit)||b.heapSizeLimit<=0||b.originalBytes!==Math.ceil(b.heapSizeLimit/1024/1024/5/64)*64*1024*1024||b.configuredBytes!==b.originalBytes)return false;
 }
 return true;
}
