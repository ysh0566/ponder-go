// Canonical current proofs, in dependency order; never rely on collector defaults.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {spawnSync} from 'node:child_process';
const root=path.resolve(import.meta.dirname,'..');
const runArgument=process.argv.find(a=>a.startsWith('--run-dir='))?.slice(10);
if(!runArgument)throw Error('Explicit completed M42 run required');
const directory=path.resolve(root,runArgument),relative=path.relative(root,directory);
if(!relative.startsWith('parity/runs/multichain-atomic-m42/'))throw Error('Scoped M42 run required');
const full=JSON.parse(fs.readFileSync(directory+'/result.json'));
if(!full.complete||!full.success)throw Error('Complete successful aggregate required');
const destination=directory+'/canonical-collectors.json';
if(fs.existsSync(destination))throw Error('Canonical collector receipt already exists; preserve previous attempt');
const report={complete:false,success:false,run_dir:relative,started_utc:new Date().toISOString(),commands:[]};
const save=()=>fs.writeFileSync(destination,JSON.stringify(report,null,2)+'\n');
const collectors=[
 ['source-sync','source-sync-results','source-sync-verification',true],
 ['source-historical','source-historical-results','source-historical-verification',true],
 ['source-realtime','source-realtime-results','source-realtime-verification',true],
 ['config-live','config-live-results','config-live-verification',true],
 ['sql-source','sql-source-results','sql-source-verification',true],
 ['source-runtime-index','source-runtime-index-results','source-runtime-index-verification',false],
 ['source-runtime-historical','source-runtime-historical-results','source-runtime-historical-verification',false],
 ['source-runtime-multichain','source-runtime-multichain-results','source-runtime-multichain-verification',false],
 ['source-indexing-service','source-indexing-service-results','source-indexing-service-verification',false],
 ['source-indexing-actions','source-indexing-actions-results','source-indexing-actions-verification',false],
 ['source-indexing-profile','source-indexing-profile-results','source-indexing-profile-verification',false],
 ['independent-storage','independent-storage-results','independent-storage-verification',false],
 ['multichain-atomic','multichain-atomic-results','multichain-atomic-verification',false],
];
save();
try{
 for(const [name,tool,target,context] of collectors){
  const output='parity/runs/'+target+'.json',args=['tools/'+tool+'.mjs','--run-dir='+relative,...(context?['--context-archive='+relative+'/context-archive.json']:[]),'--output='+output];
  const log=relative+'/canonical-'+name+'.log',entry={name,command:[process.execPath,...args],log,started_utc:new Date().toISOString()};report.commands.push(entry);save();
  const fd=fs.openSync(root+'/'+log,'w');let result;
  try{result=spawnSync(process.execPath,args,{cwd:root,env:process.env,stdio:['ignore',fd,fd]})}finally{fs.closeSync(fd)}
  Object.assign(entry,{exit_code:result.status,signal:result.signal,error:result.error?.message??null,finished_utc:new Date().toISOString()});save();
  if(result.status!==0)throw Error('Canonical collector failed: '+name);
  entry.output={file:output,sha256:crypto.createHash('sha256').update(fs.readFileSync(root+'/'+output)).digest('hex')};save();
 }
 report.success=true;
}catch(error){report.error=String(error.stack??error);process.exitCode=1}finally{report.complete=true;report.finished_utc=new Date().toISOString();save();console.log(JSON.stringify(report))}
