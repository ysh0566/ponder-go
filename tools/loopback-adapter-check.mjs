// Supplemental infrastructure check, not source-case credit.
import fs from 'node:fs';
import path from 'node:path';
import net from 'node:net';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {pathToFileURL} from 'node:url';
import {createAdaptedUpstream,setupPath} from './loopback-adapter.mjs';
const root=path.resolve(import.meta.dirname,'..');
const output=process.env.PONDER_ADAPTER_REPORT??path.join(root,'parity/runs/loopback-adapter-'+new Date().toISOString().replaceAll(/[:.]/g,'-'));
fs.mkdirSync(output,{recursive:false});
const report={method:'Environment-adapted startup infrastructure verification; NOT pristine source test execution',source_credit:0,checks:[],complete:false,success:false};
const save=()=>fs.writeFileSync(path.join(output,'result.json'),JSON.stringify(report,null,2)+'\n');
save();
let copy,shutdown,occupied;
const bind=port=>new Promise((resolve,reject)=>{const s=net.createServer();s.once('error',reject);s.listen(port,'127.0.0.1',()=>resolve(s));});
const close=s=>new Promise((resolve,reject)=>s.close(e=>e?reject(e):resolve()));
const request=async(id,method,params=[])=>{
 const res=await fetch(`http://127.0.0.1:8545/${id}`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:1,method,params}),signal:AbortSignal.timeout(15000)});
 return res.json();
};
const rpc=async(id,method,params=[])=>{const x=await request(id,method,params);assert.equal(x.error,undefined);assert.ok('result'in x,JSON.stringify(x));return x.result;};
const route=async(id,action)=>(await fetch(`http://127.0.0.1:8545/${id}/${action}`,{signal:AbortSignal.timeout(15000)})).json();
try {
 if(process.env.DATABASE_URL)throw Error('Infrastructure probe must not inherit DATABASE_URL; full source runs separately retain DB hooks');
 copy=createAdaptedUpstream({artifactsFrom:process.env.PONDER_ADAPTER_ARTIFACTS});
 fs.writeFileSync(path.join(output,'source-manifest.json'),JSON.stringify(copy.manifest,null,2)+'\n');
 fs.writeFileSync(path.join(output,'startup.patch'),copy.diff);
 const require=createRequire(path.join(copy.directory,'packages/core/package.json'));
 const ts=require('typescript');
 const source=fs.readFileSync(path.join(copy.directory,setupPath),'utf8');
 const compiled=ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.ESNext,target:ts.ScriptTarget.ES2022}}).outputText;
 const entry=path.join(copy.directory,'packages/core/src/_test/adapter-globalSetup.mjs');fs.writeFileSync(entry,compiled);
 const {default:setup}=await import(pathToFileURL(entry));
 shutdown=await setup();
 assert.deepEqual(await Promise.all([rpc(1,'eth_chainId'),rpc(2,'eth_chainId')]),['0x1','0x1']);
 report.checks.push('Two independent worker instances start through unchanged official proxy');
 await rpc(1,'anvil_mine',['0x2']);
 assert.equal(await rpc(1,'eth_blockNumber'),'0x2');assert.equal(await rpc(2,'eth_blockNumber'),'0x0');
 assert.equal(await rpc(1,'eth_blockNumber'),'0x2');
 report.checks.push('Unique fixed ports isolate concurrent worker state; repeated same ID retains state');
 const snapshot=await rpc(1,'evm_snapshot');
 await rpc(1,'anvil_mine',['0x1']);
 const before=await rpc(1,'eth_getBlockByNumber',['latest',false]);
 assert.equal(await rpc(1,'evm_revert',[snapshot]),true);
 assert.equal(await rpc(1,'eth_blockNumber'),'0x2');
 await rpc(1,'evm_setNextBlockTimestamp',[Number(BigInt(before.timestamp))+10]);await rpc(1,'anvil_mine',['0x1']);
 const after=await rpc(1,'eth_getBlockByNumber',['latest',false]);
 assert.equal(before.number,after.number);assert.notEqual(before.hash,after.hash);
 report.checks.push('Real snapshot/revert and replacement block at same height preserve reorg mechanics');
 const WebSocket=require('ws');
 await new Promise((resolve,reject)=>{const ws=new WebSocket('ws://127.0.0.1:8545/2');const t=setTimeout(()=>{ws.terminate();reject(Error('WS timeout'));},10000);ws.on('error',reject);ws.on('open',()=>ws.send(JSON.stringify({jsonrpc:'2.0',id:1,method:'eth_chainId',params:[]})));ws.on('message',b=>{try{assert.equal(JSON.parse(b).result,'0x1');ws.close();}catch(e){reject(e)}});ws.on('close',()=>{clearTimeout(t);resolve()});});
 report.checks.push('Official WebSocket proxy path returns chain ID');
 assert.equal((await route(1,'stop')).success,true);
 assert.equal(await rpc(1,'eth_blockNumber'),'0x0');
 report.checks.push('Stop then lazy restart resets instance and reuses its own port');
 assert.equal((await route(2,'stop')).success,true);
 occupied=await bind(18547);
 const failed=await route(2,'start');report.expected_collision_response=failed;assert.equal(failed.success,false);
 assert.equal((await route(2,'stop')).success,true);
 await close(occupied);occupied=undefined;
 assert.equal((await route(2,'start')).success,true);
 assert.equal(await rpc(2,'eth_blockNumber'),'0x0');
 report.checks.push('Occupied explicit worker port fails without fallback; failed pool entry can be stopped and restarted');
 await shutdown();shutdown=undefined;
 for(const port of [8545,18546,18547]){const s=await bind(port);await close(s);}
 report.checks.push('Shutdown releases proxy and both Anvil listener ports');
 copy.verify(); report.checks.push('All original tracked sources remain hash-identical');
 report.success=true;
} catch(error) {report.error={message:error.message,stack:error.stack};process.exitCode=1;}
finally {
 try {if(occupied)await close(occupied);if(shutdown)await shutdown();if(copy)copy.verify();}
 catch(error){report.cleanup_error=error.message;report.success=false;process.exitCode=1;}
 finally{if(copy)copy.dispose();}
 report.complete=true;report.finished_utc=new Date().toISOString();save();console.log(JSON.stringify({output,...report}));
}
