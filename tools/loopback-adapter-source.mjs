// Full source files, original test bodies and hooks, adapted Anvil startup only.
// This establishes source behavior, NOT native Go parity or pristine-file credit.
import fs from 'node:fs';
import path from 'node:path';
import {spawnSync} from 'node:child_process';
import {createAdaptedUpstream} from './loopback-adapter.mjs';
const root=path.resolve(import.meta.dirname,'..');
const output=process.env.PONDER_ADAPTER_REPORT??path.join(root,'parity/runs/loopback-source-'+new Date().toISOString().replaceAll(/[:.]/g,'-'));
fs.mkdirSync(output,{recursive:false});
const backend=process.env.PONDER_ADAPTER_BACKEND??'pglite';
const report={method:'Complete original source files with environment-adapted startup, NOT pristine execution or native parity',source_credit:0,pristine_file:false,backend,complete:false,success:false};
const save=()=>fs.writeFileSync(path.join(output,'result.json'),JSON.stringify(report,null,2)+'\n');save();
let copy;
try {
 if(!['pglite','postgres'].includes(backend))throw Error('Backend must be explicit pglite or postgres');
 const env={...process.env,CI:'1'};
 if(backend==='postgres') {
   const url=new URL(env.PONDER_TEST_POSTGRES??'');
   if(url.hostname!=='127.0.0.1'||url.port!=='55432'||url.pathname!=='/ponder_test')throw Error('Only task-owned loopback ponder_test allowed');
   env.DATABASE_URL=env.PONDER_TEST_POSTGRES;
 } else delete env.DATABASE_URL;
 if(env.NODE_OPTIONS)throw Error('Unexpected NODE_OPTIONS: do not inherit monkey patches or loader hooks');
 copy=createAdaptedUpstream({artifactsFrom:env.PONDER_ADAPTER_ARTIFACTS});
 fs.writeFileSync(path.join(output,'source-manifest.json'),JSON.stringify(copy.manifest,null,2)+'\n');
 fs.writeFileSync(path.join(output,'startup.patch'),copy.diff);
 const files=['src/build/config.test.ts','src/sync-store/index.test.ts','src/sync-historical/in-memory.test.ts','src/sync-historical/index.test.ts','src/sync-realtime/index.test.ts'];
 report.files=files;
 const args=['run','--config','vite.config.ts','--configLoader','native','--maxWorkers=2',...files,'--reporter=json','--outputFile='+path.join(output,'vitest.json')];
 report.command=['vitest',...args];save();
 const fd=fs.openSync(path.join(output,'output.txt'),'w');
 let result;
 try {result=spawnSync(path.join(copy.directory,'packages/core/node_modules/.bin/vitest'),args,{cwd:path.join(copy.directory,'packages/core'),env,stdio:['ignore',fd,fd]});}finally{fs.closeSync(fd);}
 report.exit_code=result.status;report.signal=result.signal;report.error=result.error?.message;
 if(fs.existsSync(path.join(output,'vitest.json'))){
  const raw=JSON.parse(fs.readFileSync(path.join(output,'vitest.json'),'utf8'));
  report.counts={passed:raw.numPassedTests,failed:raw.numFailedTests,pending:raw.numPendingTests,total:raw.numTotalTests};
  report.success=result.status===0&&raw.success&&raw.numPassedTests===147&&raw.numFailedTests===0&&raw.numPendingTests===1;
 }
 if(!report.success)process.exitCode=1;
} catch(error){report.error={message:error.message,stack:error.stack};process.exitCode=1;}
finally {
 if(copy){try{copy.verify();report.original_sources_unchanged=true;}catch(error){report.original_sources_unchanged=false;report.success=false;report.verification_error=error.message;process.exitCode=1;}finally{copy.dispose();}}
 report.complete=true;report.finished_utc=new Date().toISOString();save();console.log(JSON.stringify({output,...report}));
}
