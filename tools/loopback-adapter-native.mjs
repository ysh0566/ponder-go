// Explicit opt-in adapted harness for the four existing native live comparisons.
import fs from 'node:fs';import path from 'node:path';import {spawnSync} from 'node:child_process';
import {createAdaptedUpstream,resolveOracleRoot} from './loopback-adapter.mjs';
const root=path.resolve(import.meta.dirname,'..');
const output=process.env.PONDER_ADAPTER_REPORT??path.join(root,'parity/runs/loopback-native-'+new Date().toISOString().replaceAll(/[:.]/g,'-'));
fs.mkdirSync(output,{recursive:false});
const report={method:'Existing native live comparisons with explicitly environment-adapted startup; NOT pristine execution',source_credit:0,pristine_file:false,complete:false,success:false};
const save=()=>fs.writeFileSync(path.join(output,'result.json'),JSON.stringify(report,null,2)+'\n');save();
let copy;
try{
 if(process.env.NODE_OPTIONS)throw Error('Unexpected NODE_OPTIONS');
 const url=new URL(process.env.PONDER_TEST_POSTGRES??'');if(url.hostname!=='127.0.0.1'||url.port!=='55432'||url.pathname!=='/ponder_test')throw Error('Explicit task-only PostgreSQL 55432 required');
 copy=createAdaptedUpstream({artifactsFrom:process.env.PONDER_ADAPTER_ARTIFACTS});
 fs.writeFileSync(path.join(output,'source-manifest.json'),JSON.stringify(copy.manifest,null,2)+'\n');fs.writeFileSync(path.join(output,'startup.patch'),copy.diff);
 process.env.PONDER_UPSTREAM_ROOT=copy.directory;resolveOracleRoot();
 // Rebuild actual positive/negative control programs; never reuse stale baseline binaries.
 const controls=['parity/runs/sync-store-m32/type-compile.json','testdata/runtime/source-historical-types.json'];
 for(const [i,file] of controls.entries())if(fs.existsSync(path.join(root,file)))fs.copyFileSync(path.join(root,file),path.join(output,'previous-control-'+i+'.json'));
 report.preparation=[];
 for(const [i,args] of [['tools/source-sync-compile.mjs','--write'],['tools/source-historical-types/compile.mjs']].entries()) {
   const fd=fs.openSync(path.join(output,'type-preparation-'+i+'.txt'),'w');let result;
   try{result=spawnSync(process.execPath,args,{cwd:root,env:process.env,stdio:['ignore',fd,fd]});}finally{fs.closeSync(fd);}
   report.preparation.push({command:[process.execPath,...args],exit_code:result.status,error:result.error?.message});save();
   if(result.status!==0)throw Error('Actual type control preparation failed: '+args[0]);
 }
 for(const [i,file] of controls.entries())fs.copyFileSync(path.join(root,file),path.join(output,'prepared-control-'+i+'.json'));

 const args=['test','-json','-count=1','-p=1','./config','./store','./runtime','-run','^(TestUpstreamConfigRuntime|TestUpstreamSourceSyncStore|TestUpstreamSourceHistorical|TestUpstreamSourceRealtime)$'];
 report.command=['go',...args];save();const fd=fs.openSync(path.join(output,'go-test.jsonl'),'w');let result;
 try{result=spawnSync('go',args,{cwd:root,env:process.env,stdio:['ignore',fd,fd]});}finally{fs.closeSync(fd);}
 report.exit_code=result.status;report.signal=result.signal;report.error=result.error?.message;
 const events=fs.readFileSync(path.join(output,'go-test.jsonl'),'utf8').split('\n').flatMap(line=>{try{return[JSON.parse(line)]}catch{return[]}});
 report.tests=events.filter(e=>e.Test&&['pass','fail','skip'].includes(e.Action)).map(e=>({package:e.Package,test:e.Test,action:e.Action,elapsed:e.Elapsed}));
 const names=['TestUpstreamConfigRuntime','TestUpstreamSourceSyncStore','TestUpstreamSourceHistorical','TestUpstreamSourceRealtime'];
 report.success=result.status===0&&names.every(name=>report.tests.some(t=>t.test===name&&t.action==='pass'))&&!report.tests.some(t=>t.action==='fail'||t.action==='skip');if(!report.success)process.exitCode=1;
}catch(error){report.error={message:error.message,stack:error.stack};process.exitCode=1;}
finally{if(copy){try{copy.verify();report.original_sources_unchanged=true;}catch(error){report.original_sources_unchanged=false;report.success=false;report.verification_error=error.message;process.exitCode=1;}finally{copy.dispose();}}report.complete=true;report.finished_utc=new Date().toISOString();save();console.log(JSON.stringify({output,...report}));}
