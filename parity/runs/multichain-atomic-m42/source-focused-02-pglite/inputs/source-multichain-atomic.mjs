import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {spawnSync} from 'node:child_process';
import {createAdaptedUpstream} from './loopback-adapter.mjs';
import {persistAdaptedContext} from './loopback-context.mjs';
const root=path.resolve(import.meta.dirname,'..');
const arg=name=>process.argv.find(a=>a.startsWith('--'+name+'='))?.slice(name.length+3);
const backend=arg('backend'), destination=arg('output');
if(!['postgres','pglite'].includes(backend)||!destination)throw Error('Explicit backend and new output required');
const output=path.resolve(root,destination);
if(!['multichain-atomic-m42'].some(stage=>output.startsWith(root+'/parity/runs/'+stage+'/'))||fs.existsSync(output+'/result.json'))throw Error('New indexing-service evidence directory required');
fs.mkdirSync(output,{recursive:true});
const report={complete:false,success:false,source_credit:0,backend,pristine_file:false,environment_adapted:true,execution:'zero-credit unchanged runMultichain selected real historical iterator prefix; real two-chain Anvil RPC and handlers/database, consumer-boundary branded stop before cutover',started_utc:new Date().toISOString()};
const save=()=>fs.writeFileSync(output+'/result.json',JSON.stringify(report,null,2)+'\n');save();
let copy;
try{
 copy=createAdaptedUpstream();
 fs.writeFileSync(output+'/adapter-manifest.json',JSON.stringify(copy.manifest,null,2)+'\n');
 fs.writeFileSync(output+'/adapter.diff',copy.diff);
 const env={...process.env,CI:'1',PONDER_PORT_ROOT:root,PONDER_UPSTREAM_ROOT:copy.directory,PONDER_MULTICHAIN_ATOMIC_CAPTURE:output+'/calls.json'};
 if(backend==='postgres'){
  const url=new URL(env.PONDER_TEST_POSTGRES??'');
  if(url.hostname!=='127.0.0.1'||url.port!=='55432'||url.pathname!=='/ponder_test')throw Error('Only dedicated task PostgreSQL accepted');
  const receipt=env.PONDER_M42_POSTGRES_RECEIPT,directory=env.PONDER_M42_POSTGRES_DIRECTORY;
  if(!receipt||!directory)throw Error('New M42 fixture receipt required before legacy source test DDL');
  const owned=fs.readFileSync(receipt+'/owned-directory.txt','utf8').trim();
  if(directory!==owned+'/data'||fs.realpathSync(directory)!==directory||!owned.startsWith(path.resolve(env.TMPDIR)+'/ponder-m42-pg-'))throw Error('Canonical new M42 fixture required');
  const initial=JSON.parse(fs.readFileSync(receipt+'/initial-inventory.json','utf8'));
  if(JSON.stringify([...initial.databases].sort())!==JSON.stringify(['ponder_test','postgres','template0','template1'])||!initial.roles.every(r=>r==='agent'||r.startsWith('pg_')))throw Error('Expected fresh inventory');
  const check=spawnSync('psql',['-XAt','-d',env.PONDER_TEST_POSTGRES,'-c','SHOW data_directory'],{encoding:'utf8'});
  if(check.status!==0||check.stdout.trim()!==directory||fs.readFileSync(receipt+'/show-data-directory.txt','utf8').trim()!==directory)throw Error('Connected database differs from scoped fresh fixture');
  report.postgres_fixture={directory,receipt:path.relative(root,receipt),data_directory_verified:true};
  env.DATABASE_URL=env.PONDER_TEST_POSTGRES;
 }else delete env.DATABASE_URL;
 const args=['run','--config',root+'/tools/source-multichain-atomic.config.mjs','--configLoader','native','--reporter=json','--outputFile='+output+'/original.json'];
 report.command=['vitest',...args];save();
 const fd=fs.openSync(output+'/output.txt','w');let result;
 try{result=spawnSync(path.join(copy.directory,'packages/core/node_modules/.bin/vitest'),args,{cwd:root,env,stdio:['ignore',fd,fd]})}finally{fs.closeSync(fd)}
 report.exit_code=result.status;report.signal=result.signal;report.error=result.error?.message??null;
 if(fs.existsSync(output+'/original.json')){
  const original=JSON.parse(fs.readFileSync(output+'/original.json','utf8'));
  report.counts={passed:original.numPassedTests,failed:original.numFailedTests,pending:original.numPendingTests,total:original.numTotalTests};
  report.success=result.status===0&&original.success&&original.numPassedTests===4&&original.numFailedTests===0&&original.numPendingTests===0;
 }
 copy.verify();report.adapter_verified_after=true;
 const persisted=persistAdaptedContext(copy,output);report.context_archive={file:path.relative(root,persisted.path),sha256:persisted.sha256};
 report.inputs=['source-multichain-atomic.mjs','source-multichain-atomic.test.ts','source-multichain-atomic.config.mjs','loopback-adapter.mjs','loopback-context.mjs'].map(name=>({file:'tools/'+name,sha256:crypto.createHash('sha256').update(fs.readFileSync(root+'/tools/'+name)).digest('hex')}));
}catch(error){report.error=String(error.stack??error);report.success=false;}finally{if(copy)copy.dispose();report.complete=true;report.finished_utc=new Date().toISOString();save();}
console.log({backend,success:report.success,counts:report.counts,error:report.error,evidence:path.relative(root,output)});
if(!report.success)process.exitCode=1;
