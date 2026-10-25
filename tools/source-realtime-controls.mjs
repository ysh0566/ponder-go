import fs from'node:fs';import path from'node:path';import crypto from'node:crypto';import{spawnSync}from'node:child_process';
import{realtime,root,upstream,loaded}from'./source-realtime-module-loader.mjs';
const pinned=spawnSync('git',['-C',upstream,'rev-parse','HEAD'],{encoding:'utf8'});if(pinned.status||pinned.stdout.trim()!=='17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334')throw Error('Pinned source changed');
const hex=n=>'0x'+n.toString(16),hash=n=>'0x'+n.toString(16).padStart(64,'0'),block=n=>({number:hex(n),hash:hash(n),parentHash:hash(n-1<0?0:n-1),timestamp:hex(n),logsBloom:'0x'+'00'.repeat(256),transactions:[]}),logFilter={type:'log',chainId:1,hasTransactionReceipt:false},chain={id:1,name:'controls',reorgWindow:1000,pollingInterval:60000};
const cases=[];
async function run(name,kind,extra={}){
 const input={kind,chain:{...chain,...extra.chain},finalized:block(0),blocks:extra.blocks??[block(1),block(2)],filters:extra.logFilter?[logFilter]:[],attempts:extra.attempts??0,killed:!!extra.killed,provider_mode:extra.provider??'blocks'},before=JSON.parse(JSON.stringify(input));
 const warnings=[],requests=[],callbacks=[],shutdown={isKilled:input.killed};let release,started;const gate=new Promise(r=>release=r),begun=new Promise(r=>started=r),logger={debug(){},trace(){},warn(v){const x={...v};delete x.duration;if(x.error)x.error={name:x.error.name,message:x.error.message};warnings.push(x)},child(){return this}};
 const rpc={async request(query){requests.push(JSON.parse(JSON.stringify(query)));if(input.provider_mode==='wait'){started();await gate};if(input.provider_mode==='reject')throw Error('provider failure');if(query.method==='eth_getLogs')return[];if(query.method==='eth_getBlockByNumber')return block(Number(query.params[0]));throw Error('Unexpected source request '+query.method)}};
 const sync=realtime.createRealtimeSync({common:{logger,shutdown},chain:input.chain,rpc,eventCallbacks:input.filters.map(filter=>({filter})),syncProgress:{finalized:input.finalized},childAddresses:new Map()});
 const events=[],checks={};const cb=value=>callbacks.push(value),summary=e=>({type:e.type,number:Number(e.block.number),...(e.type==='block'?{has_callback:e.blockCallback===cb,matched:e.hasMatchedFilter}:{} )});
 async function drain(generator,accept=false){for await(const e of generator){events.push(summary(e));if(accept&&e.type==='block')e.blockCallback?.(true)}};
 if(kind==='callback'){await drain(sync.sync(input.blocks[0],cb),true);checks.before_duplicate=[...callbacks];await drain(sync.sync(input.blocks[0],cb));}
 else if(kind==='early_return'){const g=sync.sync(input.blocks[0],cb),first=await g.next();events.push(summary(first.value));await g.return();checks.retained_before_next=sync.unfinalizedBlocks.map(b=>Number(b.number));await drain(sync.sync(input.blocks[0],cb));await drain(sync.sync(input.blocks[1],cb),true)}
 else if(kind==='missing'){await drain(sync.sync(input.blocks[0],cb));}
 else if(kind==='retry'){checks.thrown_at=[];checks.same_error=[];checks.errors=[];const error=Error('retry error');for(let i=1;i<=input.attempts;i++){try{sync.onError(error)}catch(e){checks.thrown_at.push(i);checks.same_error.push(e===error);checks.errors.push({name:e.name,message:e.message})}}}
 else if(kind==='reset'){const error=Error('retry error');for(let i=0;i<9;i++)sync.onError(error);await drain(sync.sync(input.blocks[0],cb));checks.thrown_at=[];for(let i=1;i<=2;i++){try{sync.onError(error)}catch(e){checks.thrown_at.push(i)}}}
 else if(kind==='concurrent_duplicate'){const first=sync.sync(input.blocks[0],cb),pending=first.next();await begun;await drain(sync.sync(input.blocks[0],cb));checks.before_release=[...callbacks];release();const result=await pending;events.push(summary(result.value));result.value.blockCallback(true);await first.next();}
 else if(kind==='serialized'){const first=sync.sync(input.blocks[0],cb),a=await first.next();events.push(summary(a.value));let settled=false;const second=sync.sync(input.blocks[1],cb),pending=second.next().then(v=>{settled=true;return v});await new Promise(r=>setImmediate(r));checks.second_waited=!settled;await first.return();const b=await pending;events.push(summary(b.value));b.value.blockCallback(true);await drain(second)}
 else if(kind==='failure_duplicate'){await drain(sync.sync(input.blocks[0],cb));await drain(sync.sync(input.blocks[0],cb));}
 else throw Error('Unknown control');
 requests.sort((a,b)=>JSON.stringify(a).localeCompare(JSON.stringify(b)));
 cases.push({name,input:before,result:{events,checks,callbacks,warnings,requests,unfinalized:sync.unfinalizedBlocks.map(b=>Number(b.number))}})
}
await run('consumer_owns_acceptance_callback','callback');
await run('return_defers_finality_and_releases_lock','early_return',{chain:{reorgWindow:0}});
await run('distant_tip_fetches_49_intermediate_blocks','missing',{blocks:[block(80)]});
await run('exact_distance_50_includes_original_tip','missing',{blocks:[block(50)]});
await run('strict_ten_minute_boundary','retry',{attempts:11});
await run('minimum_ten_retries','retry',{chain:{pollingInterval:70000},attempts:10});
await run('zero_polling_interval_never_times_out','retry',{chain:{pollingInterval:0},attempts:12});
await run('shutdown_preempts_warning_and_retry','retry',{attempts:1,killed:true});
await run('successful_sync_resets_retry_count','reset');
await run('duplicate_hash_while_original_fetch_waits','concurrent_duplicate',{logFilter:true,provider:'wait'});
await run('reconcile_lock_held_across_yield','serialized',{chain:{reorgWindow:0}});
await run('provider_error_keeps_latest_fetched_hash','failure_duplicate',{logFilter:true,provider:'reject'});
const sources=[import.meta.filename,path.resolve(import.meta.dirname,'source-realtime-module-loader.mjs'),...loaded,upstream+'/pnpm-lock.yaml'].map(file=>({file:path.relative(root,file),sha256:crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')}));
const report={state:'Actual complete pinned realtime source module loaded with actual dependencies. Supplementary controls only; zero original-test credit.',source_credit:0,cases,sources};const target=process.argv[2]??path.join(root,'testdata/runtime/source-realtime-controls.json');fs.writeFileSync(target,JSON.stringify(report,null,2)+'\n');console.log({cases:cases.length,loaded:loaded.length,summary:cases.map(c=>({name:c.name,events:c.result.events.length,requests:c.result.requests.length,checks:c.result.checks}))});
