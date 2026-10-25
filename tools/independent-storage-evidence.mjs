// Validate actual source probe observations, without manufacturing native parity.
import crypto from 'node:crypto';
const ordered=v=>Array.isArray(v)?v.map(ordered):v&&typeof v==='object'?Object.fromEntries(Object.keys(v).sort().map(k=>[k,ordered(v[k])])):v;
export function assertIndependentSourceCapture(c,backend){
 const check=(v,m)=>{if(!v)throw Error(m)};
 check(c?.source_credit===0&&c.real_database===true&&c.real_rpc===false&&c.backend===backend,'Honest source probe labels required');
 check(JSON.stringify(c.batches?.map(b=>[b.name,b.rollback]))===JSON.stringify([['setup',false],['cache-hit',false],['rolled-back',true],['pending-rollback',true]]),'Exact binding sequence required');
 check(c.batches[2].error==='intentional rollback'&&c.batches[3].error==='intentional rollback','Actual intended failures required');
 check(c.identities?.length===4&&c.identities.every(v=>['args','context','db','client'].every(k=>v[k]===true)),'Stable source identities required');
 const rpc=c.calls.filter(x=>x.operation==='rpc'),writes=c.calls.filter(x=>x.operation==='cache-write-admit');
 check(rpc.length===6&&writes.length===6&&writes.every(x=>x.settled===true),'RPC/cache admission and settlement missing');
 check(JSON.stringify(rpc.map(x=>[x.request.method,x.request.params[1]]))===JSON.stringify([7,8,9].flatMap(b=>[['eth_getBalance','0x'+b.toString(16)],['eth_call','0x'+b.toString(16)]])),'Actual RPC method/tag sequence differs');
 check(c.pending_before?.cache?.length===4&&c.final?.cache?.length===6,'Admitted pending cache must survive callback rollback');
 const expected=rpc.map(c=>({request_hash:crypto.createHash('md5').update(JSON.stringify(ordered(c.request)).toLowerCase()).digest('hex'),chain_id:'1',block_number:String(parseInt(c.request.params[1],16)),result:JSON.stringify(c.request.method==='eth_getBalance'?'0x2a':'0x'+(41).toString(16).padStart(64,'0'))}));
 const normalize=rows=>rows.map(r=>({request_hash:r.request_hash,chain_id:String(r.chain_id),block_number:String(r.block_number),result:r.result})).sort((a,b)=>a.request_hash.localeCompare(b.request_hash));
 check(JSON.stringify(normalize(c.final.cache))===JSON.stringify(normalize(expected)),'Actual final cache chain/block/hash/result correspondence differs');
 check(c.final_cache_reads?.length===6&&c.final_cache_reads.every((r,i)=>JSON.stringify(r.request)===JSON.stringify(rpc[i].request)&&JSON.stringify(r.result)===JSON.stringify([expected[i].result])),'Actual source final persistent cache hits differ');
 check(JSON.stringify(normalize(c.pending_before.cache))===JSON.stringify(normalize(expected.filter(r=>r.block_number!=='9'))),'Pending cache became visible too early or prior rows changed');

 const ids=c.final?.business?.map(r=>r.id).sort();
 check(JSON.stringify(ids)===JSON.stringify((backend==='postgres'?['cache-hit','setup']:['cache-hit','pending-rollback','rolled-back','setup']).sort()),'Intentional source PG/PGlite SQL rollback distinction differs');
 check(c.final.business.every(r=>String(r.balance)==='42'),'Actual business values differ');
 check(typeof c.intentional_native_difference==='string'&&c.intentional_native_difference.includes('two owned database directories')&&c.intentional_native_difference.includes('callback-only'),'Native topology/atomicity difference must be disclosed');
 return true;
}

// Walk every nested hash reference, not just top-level inputs.
export function allEvidenceReferencesValid(value,valid){if(Array.isArray(value))return value.every(v=>allEvidenceReferencesValid(v,valid));if(value&&typeof value==='object'){if(Object.hasOwn(value,'file')&&Object.hasOwn(value,'sha256')&&!valid(value))return false;return Object.values(value).every(v=>allEvidenceReferencesValid(v,valid))}return true}
export function independentProofReferencesValid(proof,valid){
 const ref=r=>!!r&&typeof r.file==='string'&&typeof r.sha256==='string'&&valid(r);
 if(!proof||!['full_verification','prepared_native_inputs','context_archive','previous_profile_proof'].every(k=>ref(proof[k])))return false;
 if(!proof.postgres_fixture||proof.postgres_fixture.data_directory_verified!==true||typeof proof.postgres_fixture.directory!=='string'||typeof proof.postgres_fixture.receipt!=='string'||!ref(proof.postgres_fixture.preflight)||!Array.isArray(proof.postgres_fixture.inputs)||proof.postgres_fixture.inputs.length!==6||!proof.postgres_fixture.inputs.every(ref))return false;
 if(!Array.isArray(proof.inputs)||proof.inputs.length===0||!proof.inputs.every(ref))return false;
 if(!Array.isArray(proof.source)||proof.source.length!==2||JSON.stringify(proof.source.map(s=>s.backend).sort())!==JSON.stringify(['pglite','postgres']))return false;
 if(!proof.source.every(s=>['result','original','capture','context_archive'].every(k=>ref(s[k]))))return false;
 if(!Array.isArray(proof.executions)||proof.executions.length!==2||JSON.stringify(proof.executions.map(e=>e.mode).sort())!==JSON.stringify(['normal','race']))return false;
 if(!proof.executions.every(e=>['log','manifest'].every(k=>ref(e[k]))&&e.source_credit===0&&Array.isArray(e.tests)&&e.tests.length>0))return false;
 return allEvidenceReferencesValid(proof,valid);
}
