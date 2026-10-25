import {allEvidenceReferencesValid,independentProofReferencesValid} from './independent-storage-evidence.mjs';
// Reuse the strict shape checks for shared source/native/fixture provenance,
// then require the actual M42 scope and predecessor link, never grant credit.
export function atomicMultichainProofReferencesValid(proof,valid){
 if(proof?.scope!=='atomic_multichain_setup_and_historical_group_primitives'||proof.source_credit!==0||!proof.previous_independent_proof)return false;
 if(!proof.native_controls?.result||!valid(proof.native_controls.result)||proof.native_controls.source_credit!==0||!Array.isArray(proof.native_controls.inputs)||proof.native_controls.inputs.length===0||!proof.native_controls.inputs.every(valid))return false;
 const shared={...proof,previous_profile_proof:proof.previous_independent_proof};
 return independentProofReferencesValid(shared,valid)&&allEvidenceReferencesValid(proof,valid);
}
