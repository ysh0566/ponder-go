import fs from 'node:fs';import path from 'node:path';import {spawn,spawnSync} from 'node:child_process';
import {check,hash,scopedPath,managedBaselinePath} from './managed-finite-evidence.mjs';
const root=path.resolve(import.meta.dirname,'..'),arg=n=>process.argv.find(a=>a.startsWith('--'+n+'='))?.slice(n.length+3);
const output=scopedPath(root,arg('output'),'parity/runs/managed-finite-m44'),baseline=managedBaselinePath(root,arg('baseline'));
check(!fs.existsSync(output)&&!fs.existsSync(baseline)&&path.dirname(baseline)===root+'/parity/runs/finite-historical-m43'&&path.basename(baseline).startsWith('m44-regression-'),'Fresh exact M44 paths required');
const directory=output+'-launch';check(!fs.existsSync(directory),'Launch evidence already exists');fs.mkdirSync(directory,{recursive:true});
const ready=directory+'/admit',command=['bash','tools/finite-historical-postgres-fixture.sh',baseline+'-fixture',process.execPath,'tools/managed-finite-full-verify.mjs','--output='+output,'--baseline='+baseline,'--collect-canonical'];
const log=fs.openSync(directory+'/launch.log','wx');
const child=spawn('setsid',['bash','-c','ready=$1; shift; while [[ ! -f "$ready" ]]; do sleep 0.05; done; exec "$@"','managed-launch',ready,...command],{cwd:root,env:process.env,stdio:['ignore',log,log]});
let launchError;child.on('error',e=>{launchError=e;});
const completion=new Promise(resolve=>child.on('close',(code,signal)=>resolve({code,signal})));
const ps=()=>spawnSync('ps',['-eo','pid=,pgid=,stat=,comm='],{encoding:'utf8'});
const census=()=>{const r=ps();check(r.status===0,'Cannot observe launch process group');return r.stdout.trim().split('\n').filter(Boolean).map(l=>{const [pid,pgid,state,...command]=l.trim().split(/\s+/);return{pid:Number(pid),pgid:Number(pgid),state,command:command.join(' ')};});};
let identity,startedTicks,interrupted=null,joinedChild=false;
for(const signal of ['SIGINT','SIGTERM'])process.on(signal,()=>{
 interrupted=signal;
 try { if(!identity){child.kill(signal);return;} check(stat()===startedTicks&&census().some(p=>p.pid===child.pid&&p.pgid===child.pid),'Refusing signal to changed launch identity');process.kill(-child.pid,signal); } catch(error){fs.appendFileSync(directory+'/signal-errors.log',String(error)+'\n');}
});
const stat=()=>{const raw=fs.readFileSync('/proc/'+child.pid+'/stat','utf8');return raw.slice(raw.lastIndexOf(')')+2).split(' ')[19];};
try {
for(let n=0;n<100;n++){if(launchError)throw launchError;identity=census().find(p=>p.pid===child.pid&&p.pgid===child.pid);if(identity)break;await new Promise(r=>setTimeout(r,50));}
check(identity,'setsid child group identity not established');
startedTicks=stat();
check(!interrupted,'Launch canceled before fixture admission');
const launch={format:'managed-finite-launch-v1',recorded_utc:new Date().toISOString(),run_dir:path.relative(root,output),baseline_run:path.relative(root,baseline),command,pid:child.pid,pgid:child.pid,proc_start_ticks:startedTicks,scope:'One exact setsid launch group; independently detached stage groups are not inferred.',launcher:hash(root,import.meta.filename)};
fs.writeFileSync(directory+'/launch.json',JSON.stringify(launch,null,2)+'\n',{flag:'wx'});
fs.writeFileSync(ready,'admitted after launch identity receipt\n',{flag:'wx'});
const result=await completion;joinedChild=true;fs.closeSync(log);
const final={observed_utc:new Date().toISOString(),launch:hash(root,directory+'/launch.json'),exit_code:result.code,signal:result.signal,interrupted,pgid:child.pid,processes:census().filter(p=>p.pgid===child.pid)};
fs.writeFileSync(directory+'/joined.json',JSON.stringify(final,null,2)+'\n',{flag:'wx'});
if(result.code!==0||result.signal||interrupted||final.processes.length){process.exitCode=1;console.error('Launch/fixture failed or group not drained; retained evidence at '+directory);}else{
 const args=['tools/managed-finite-closeout.mjs','--run-dir='+output],started=new Date().toISOString();
 const r=spawnSync(process.execPath,args,{cwd:root,env:process.env,encoding:'utf8',maxBuffer:4*1024*1024});
 fs.writeFileSync(directory+'/closeout-command.json',JSON.stringify({command:[process.execPath,...args],started_utc:started,finished_utc:new Date().toISOString(),exit_code:r.status,signal:r.signal,error:r.error?.message??null,stdout:r.stdout,stderr:r.stderr},null,2)+'\n',{flag:'wx'});
 if(r.stdout)process.stdout.write(r.stdout);if(r.stderr)process.stderr.write(r.stderr);process.exitCode=r.status??1;
}

} finally {
 // Even pre-admission failure retains and joins the exact spawned child.
 if(!joinedChild){child.kill('SIGTERM');await completion;fs.closeSync(log);}
}
