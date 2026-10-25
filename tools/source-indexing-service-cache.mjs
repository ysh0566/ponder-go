import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {spawnSync} from 'node:child_process';
import {createAdaptedUpstream} from './loopback-adapter.mjs';
import {persistAdaptedContext} from './loopback-context.mjs';
const root=path.resolve(import.meta.dirname,'..');
const arg=name=>process.argv.find(a=>a.startsWith('--'+name+'='))?.slice(name.length+3);
const destination=arg('output');
if(!destination)throw Error('Explicit new output required');
const output=path.resolve(root,destination);
if(!['indexing-service-m38','indexing-actions-m39'].some(stage=>output.startsWith(root+'/parity/runs/'+stage+'/'))||fs.existsSync(output+'/result.json'))throw Error('New indexing-service evidence directory required');
fs.mkdirSync(output,{recursive:true});
const report={complete:false,success:false,source_credit:0,probe:'source-client-cache-and-error-brand',pristine_file:false,environment_adapted:true,execution:'zero-credit actual cached client with real Anvil RPC and gated cache persistence; actual indexing error branding',started_utc:new Date().toISOString()};
const save=()=>fs.writeFileSync(output+'/result.json',JSON.stringify(report,null,2)+'\n');save();
let copy;
try{
 copy=createAdaptedUpstream();
 fs.writeFileSync(output+'/adapter-manifest.json',JSON.stringify(copy.manifest,null,2)+'\n');
 fs.writeFileSync(output+'/adapter.diff',copy.diff);
 const env={...process.env,CI:'1',PONDER_PORT_ROOT:root,PONDER_UPSTREAM_ROOT:copy.directory,PONDER_INDEXING_CACHE_CAPTURE:output+'/calls.json'};
 const args=['run','--config',root+'/tools/source-indexing-service-cache.config.mjs','--configLoader','native','--reporter=json','--outputFile='+output+'/original.json'];
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
 report.inputs=['source-indexing-service-cache.mjs','source-indexing-service-cache.test.ts','source-indexing-service-cache.config.mjs','loopback-adapter.mjs','loopback-context.mjs'].map(name=>({file:'tools/'+name,sha256:crypto.createHash('sha256').update(fs.readFileSync(root+'/tools/'+name)).digest('hex')}));
}catch(error){report.error=String(error.stack??error);report.success=false;}finally{if(copy)copy.dispose();report.complete=true;report.finished_utc=new Date().toISOString();save();}
console.log({success:report.success,counts:report.counts,error:report.error,evidence:path.relative(root,output)});
if(!report.success)process.exitCode=1;
