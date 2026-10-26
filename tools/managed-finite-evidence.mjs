import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {managedFiniteNativeFiles, managedFiniteDualBackendTests, managedFinitePostgresTests, managedFinitePGliteTests} from './managed-finite-acceptance.mjs';
export const check = (condition, message) => { if (!condition) throw Error(message); };
export const equal = (a,b) => JSON.stringify(a) === JSON.stringify(b);
export function scopedPath(root, value, prefix, mustExist = false) {
  check(typeof value === 'string' && value.length && !value.includes('\\') && !value.includes('\0'), 'Explicit canonical path required');
  const target = path.resolve(root,value), relative = path.relative(root,target);
  check(relative.startsWith(prefix+'/') && !relative.split('/').includes('..'), 'Scoped '+prefix+' path required');
  let probe = target;
  while (!fs.existsSync(probe)) { const parent=path.dirname(probe); check(parent!==probe,'Existing ancestor required'); probe=parent; }
  check(fs.realpathSync(probe)===probe,'Symlink/alias paths rejected');
  if (mustExist) check(fs.existsSync(target),'Required evidence missing');
  return target;
}
export function hash(root,file) { const target=path.resolve(root,file); return {file:path.relative(root,target),sha256:crypto.createHash('sha256').update(fs.readFileSync(target)).digest('hex')}; }
export function inventory(root) {
  return managedFiniteNativeFiles.flatMap(file => {
    const names=[...fs.readFileSync(path.join(root,file),'utf8').matchAll(/^func (Test\w+)\(/gm)].map(m=>m[1]);
    check(names.length>0,'Empty managed native file '+file);
    return names.map(name=>{
      const scope=managedFiniteDualBackendTests.includes(name)?'postgres_and_persistent_pglite':managedFinitePostgresTests.includes(name)?'postgres_only':managedFinitePGliteTests.includes(name)?'persistent_pglite_only':
        (file==='historical/managed_plan_test.go'||file==='indexing/client_managed_test.go'||file==='indexing/client_managed_fallback_test.go'||['TestManagedFiniteOptionsAndSchemaSnapshot','TestManagedCanceledCloseErrorClassification','TestManagedFiniteActivationBusyPreservesCancellation','TestManagedFiniteActivationHeldStoreRespectsDeadline'].includes(name))?'pure':null;
      check(scope,'Explicit backend classification required for '+name);
      return {file,package:'ponder.local/ponder-go/'+file.split('/')[0],name,scope};
    });
  });
}
export function validateManagedEvents(events, tests) {
  check(events.length>0,'Empty native events');
  check(!events.some(e=>e.Action==='fail'||e.Action==='skip'),'Native failures/skips cannot establish acceptance');
  const passed=(pkg,name)=>events.filter(e=>e.Package===pkg&&e.Test===name&&e.Action==='pass').length===1;
  const executions=[];
  for (const test of tests) {
    check(passed(test.package,test.name),'Missing/duplicate managed top-level pass '+test.name);
    const names=[...new Set(events.filter(e=>e.Package===test.package&&e.Action==='run'&&(e.Test===test.name||e.Test?.startsWith(test.name+'/'))).map(e=>e.Test))];
    const leaves=names.filter(n=>!names.some(other=>other.startsWith(n+'/'))).sort();
    check(leaves.length && leaves.every(n=>passed(test.package,n)),'Managed leaf pass missing '+test.name);
    if(test.scope==='postgres_and_persistent_pglite') for(const backend of ['postgres','pglite']) check(leaves.some(n=>n.startsWith(test.name+'/'+backend+'/')||n===test.name+'/'+backend),'Backend leaf missing '+test.name+'/'+backend);
    executions.push({...test,leaves});
  }
  return executions;
}
export function validateManagedCloseout(c) {
 check(c?.complete===true&&c.success===true,'Successful independent closeout required');
 check(Number.isInteger(c.launch_pgid)&&c.launch_pgid>1&&c.launch_exit_code===0&&!c.launch_signal&&!c.interrupted,'Launch did not finish normally');
 check(Array.isArray(c.joined_processes)&&c.joined_processes.length===0&&Array.isArray(c.current_processes)&&c.current_processes.length===0,'Launch group not drained');
 check(c.fixture_exit_code===0&&c.postgres_status?.exit_code===3&&!c.postgres_status.error&&c.postmaster_pid_present===false,'Actual fixture PostgreSQL shutdown required');
 check(typeof c.shutdown_log==='string'&&c.shutdown_log.includes('server stopped'),'Shutdown completion receipt missing');
 const databases=['ponder_test','postgres','template0','template1'];
 for(const inventory of [c.initial_inventory,c.final_inventory])check(inventory&&equal([...inventory.databases].sort(),databases)&&Array.isArray(inventory.roles)&&inventory.roles.every(r=>r==='agent'||r.startsWith('pg_')),'Fresh/final fixture inventory differs');
 check(equal(c.initial_inventory,c.final_inventory),'Fixture left unexpected databases/roles');
 check(Number.isFinite(Date.parse(c.observed_utc)),'Closeout observation time required');
 return true;
}
export function validateManagedSnapshotList(entries, allowlist) {
 check(Array.isArray(entries)&&equal(entries.map(e=>e.file),allowlist)&&new Set(entries.map(e=>e.file)).size===allowlist.length,'Exact ordered unique previous derived snapshot list required');
}
export function validateManagedSummary(summary, baseline, fullRef, counts) {
 check(summary?.run_dir===baseline&&equal(summary.full_verification,fullRef)&&summary.environment_adapted===true&&summary.pristine_file===false,'Current summary/full run linkage required');
 const p=summary.parity;
 check(p?.expanded_static_records===905&&p.ported===543&&p.passed===543&&p.failed===0&&p.blocked===0&&p.unimplemented===362&&p.current_original_oracle_passed===543&&p.historical_only_original_oracles===0,'Current summary source statuses/counts differ');
 for(const [mode,key] of [['normal','native_go'],['race','race']]) {
  const x=summary[key],stem=mode==='normal'?'go-test':'go-race';
  check(x?.file===baseline+'/native/'+stem+'.jsonl'&&x.leaf_cases===counts[mode]&&x.pass===counts[mode]&&x.fail===0&&x.skip===0,'Current summary native count/status differs '+mode);
 }
}
export function validateManagedReferences(refs, expectedFiles, valid) {
 check(Array.isArray(refs)&&equal(refs.map(r=>r?.file),expectedFiles)&&new Set(refs.map(r=>r?.file)).size===expectedFiles.length&&refs.every(valid),'Missing, duplicated, stale or changed exact input references');
}
export function managedBaselinePath(root,value,mustExist=false) {
 const target=scopedPath(root,value,'parity/runs/finite-historical-m43',mustExist);
 check(path.dirname(target)===root+'/parity/runs/finite-historical-m43'&&path.basename(target).startsWith('m44-regression-'),'Direct new M44 regression child required');
 return target;
}
export function validateManagedObserver(o) {
 check(o?.format==='managed-finite-observed-descendants-v1'&&o.complete===true&&o.success===true,'Successful observed-descendant receipt required');
 check(o.exit_code===0&&!o.signal,'Observed launcher did not exit normally');
 check(Array.isArray(o.interrupted)&&o.interrupted.length===0&&!o.error&&!o.drain_error,'Observer was interrupted or failed');
 const c=o.capacity;
 check(c&&c.floor_bytes===5*1024**3&&Number.isSafeInteger(c.budget_bytes)&&c.budget_bytes>0&&c.budget_bytes<=4*1024**3&&Number.isSafeInteger(c.initial_free_bytes)&&Number.isSafeInteger(c.final_free_bytes)&&c.initial_free_bytes-c.budget_bytes>=c.floor_bytes&&c.final_free_bytes>=c.floor_bytes&&c.initial_free_bytes-c.final_free_bytes<=c.budget_bytes,'Observer capacity floor/budget was not preserved');

 check(Number.isFinite(o.sampling_interval_ms)&&o.sampling_interval_ms>0&&o.sampling_interval_ms<=1000,'Recorded bounded observation interval required');
 const identity=x=>x&&Number.isInteger(x.pid)&&x.pid>1&&Number.isInteger(x.pgid)&&x.pgid>1&&typeof x.start_ticks==='string'&&/^\d+$/.test(x.start_ticks);
 check(identity(o.root_identity)&&Array.isArray(o.observed)&&o.observed.length>0&&o.observed.every(identity),'Actual recorded process identities required');
 check(new Set(o.observed.map(x=>x.pid+':'+x.start_ticks)).size===o.observed.length&&o.observed.some(x=>x.pid===o.root_identity.pid&&x.start_ticks===o.root_identity.start_ticks&&x.pgid===o.root_identity.pgid),'Unique observed root/descendant identities required');
 check(Array.isArray(o.final_remaining_identities)&&o.final_remaining_identities.length===0&&Array.isArray(o.final_remaining_groups)&&o.final_remaining_groups.length===0,'Observed task descendants/groups remain');
 check(Number.isFinite(Date.parse(o.started_utc))&&Number.isFinite(Date.parse(o.finished_utc))&&Date.parse(o.started_utc)<=Date.parse(o.finished_utc),'Ordered observer timestamps required');
}
