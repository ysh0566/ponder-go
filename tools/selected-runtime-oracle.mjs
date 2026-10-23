import fs from 'node:fs';import path from 'node:path';import crypto from 'node:crypto';import net from 'node:net';import {spawn} from 'node:child_process';import {createRequire} from 'node:module';
const root=path.resolve(import.meta.dirname,'..'),up=path.resolve(root,'../ponder-upstream'),require=createRequire(path.join(up,'packages/core/package.json')),ts=require('typescript');
const ledger=JSON.parse(fs.readFileSync(path.join(root,'parity/test-ledger.json'))),sources=[];
function select(file,names){const raw=fs.readFileSync(path.join(up,file),'utf8'),sf=ts.createSourceFile(file,raw,99,true);sources.push({file,sha256:crypto.createHash('sha256').update(raw).digest('hex')});return sf.statements.filter(n=>(ts.isVariableStatement(n)&&n.declarationList.declarations.some(d=>names.includes(d.name.getText(sf))))||(ts.isFunctionDeclaration(n)&&names.includes(n.name?.getText(sf)))).map(n=>n.getText(sf)).join('\n');}
const includeEth=process.argv.includes('--include-eth'),mappedOnly=process.argv.includes('--mapped-only');
const filterNames=mappedOnly?ledger.tests.filter(t=>t.source_file.endsWith('/runtime/filter.test.ts')&&t.mapping).map(t=>t.title):includeEth?['isTransactionFilterMatched()','isTransactionFilterMatched() with null transaction.to','isTransferFilterMatched()']:[];
const eventNames=mappedOnly?['splitEvents()']:['decodeEvents() block','decodeEvents() transfer','decodeEvents() transaction','buildEvents() transaction matches receipt by transaction index'];
const selected=ledger.tests.filter(t=>(t.source_file.endsWith('/runtime/filter.test.ts')&&filterNames.includes(t.title))||(t.source_file.endsWith('/runtime/events.test.ts')&&eventNames.includes(t.title)));
if(selected.length!==(mappedOnly?15:includeEth?7:4))throw Error('Missing exact source selection');
const setup=select('packages/core/src/_test/setup.ts',['setupCommon','setupCleanup','setupAnvil']);
const utils=select('packages/core/src/_test/utils.ts',['TEST_POOL_ID','IS_BUN_TEST','anvil','testClient','publicClient','getChain','getAccountsIndexingBuild','getBlocksIndexingBuild']);
const transfer=select('packages/core/src/_test/simulate.ts',['transferEth']);
for(const file of ['packages/core/src/runtime/events.ts','packages/core/src/runtime/filter.ts','packages/core/src/rpc/actions.ts','packages/core/src/rpc/index.ts'])select(file,[]);
const mappedGlobals=mappedOnly?select('packages/core/src/runtime/filter.test.ts',['ADDRESS_A','ADDRESS_B','TOPIC1_ALICE','TOPIC1_BOB','baseLogFilter','REQUEST_ADDRESS_A','REQUEST_ADDRESS_B','REQUEST_TOPIC_A','REQUEST_TOPIC_B','REQUEST_TOPIC_C','REQUEST_FACTORY']):'';
const meta=selected.map(t=>({id:t.id,source_file:t.source_file,source_sha256:t.source_sha256,title:t.title,source_name:t.full_name,source_state:t.source_state,contexts:t.contexts,inputs:t.inputs,assertions:t.assertions,test_source:t.test_source}));
const imports=`import fs from 'node:fs';import {beforeEach,afterEach,afterAll,describe,test,expect,vi} from 'vitest';
import * as viem from 'viem';import {mainnet} from 'viem/chains';
import {buildOptions} from '@/internal/options.js';import {createLogger} from '@/internal/logger.js';import {MetricsService} from '@/internal/metrics.js';import {createShutdown} from '@/internal/shutdown.js';
import {toLowerCase} from '@/utils/lowercase.js';import {ALICE,BOB} from '@/_test/constants.js';import {ZERO_CHECKPOINT_STRING} from '@/utils/checkpoint.js';
import * as predicates from '@/runtime/filter.js';import * as events from '@/runtime/events.js';import * as actions from '@/rpc/actions.js';import {createRpc as sourceCreateRpc} from '@/rpc/index.js';
const {createPublicClient,createTestClient,createWalletClient,parseEther,zeroAddress,toHex}=viem;
const endpoint=process.env.PONDER_SELECTED_RPC_URL!;if(!endpoint)throw Error('Explicit task RPC required');
// Endpoint adapter only: preserve source chain metadata, transport/helper bodies,
// no-mining behavior and all test bodies/expectations. Never bind shared8545.
const http=(url,...args)=>viem.http(url??endpoint,...args);
const createRpc=input=>sourceCreateRpc({...input,chain:{...input.chain,rpc:endpoint}});
const {defaultBlockFilterInclude,defaultTransactionFilterInclude,defaultTransferFilterInclude}=predicates;
const {debug_traceBlockByHash,eth_getBlockByNumber}=actions;
const context={};let current;const results=[];
function mark(v){if(v===undefined)return {$undefined:true};if(typeof v==='bigint')return {$bigint:String(v)};if(typeof v==='function')return {$function:true};if(v instanceof Map)return {$map:[...v.entries()].map(mark)};if(Array.isArray(v))return v.map(mark);if(v&&typeof v==='object')return Object.fromEntries(Object.keys(v).sort().map(k=>[k,mark(v[k])]));return v;}
function capture(name,fn){return (...args)=>{const input=mark(name==='decodeEvents'?[{$source_common_fixture:true},...args.slice(1)]:args);try{const result=fn(...args);current.calls.push({operation:name,args:input,result:mark(result)});return result;}catch(e){current.calls.push({operation:name,args:input,error:{name:e.name,message:e.message}});throw e;}};}
const isTransactionFilterMatched=capture('isTransactionFilterMatched',predicates.isTransactionFilterMatched),isTransferFilterMatched=capture('isTransferFilterMatched',predicates.isTransferFilterMatched),decodeEvents=capture('decodeEvents',events.decodeEvents),buildEvents=capture('buildEvents',events.buildEvents);
const metadata=${JSON.stringify(meta)};
beforeEach(ctx=>{current={...metadata.find(t=>t.title===ctx.task.name),calls:[]};if(!current.id)throw Error('Unmapped selected declaration');});
afterEach(async ctx=>{current.instrumentation_after_each_state=ctx.task.result?.state??'unknown';results.push(current);await context.common?.shutdown.kill();});
afterAll(()=>fs.writeFileSync(process.env.PONDER_SELECTED_FIXTURE!,JSON.stringify({baseline:${JSON.stringify(ledger.baseline)},method:'Selective unchanged source declarations and actual source helper implementations; loopback endpoint adaptation and existing Anvil0.2.0 explicitly recorded. Not the full original suite or Foundry1.8.5 acceptance.',sources:${JSON.stringify(sources)},cases:results},null,2)+'\\n'));
`;
const mappedImports=mappedOnly?`import {EMPTY_LOG_FILTER} from '@/_test/constants.js';const {zeroHash}=viem;const {ADDRESS_CHUNK_SIZE}=actions;const {getChildAddress,isBlockFilterMatched,mergeLogFiltersToBlockRequest}=predicates;const {splitEvents}=events;` : '';
const code=imports+mappedImports+mappedGlobals+setup+'\n'+utils+'\n'+transfer+((includeEth||mappedOnly)?"\ndescribe('selected filter',()=>{beforeEach(setupCommon);beforeEach(setupAnvil);\n"+selected.filter(t=>t.source_file.endsWith('filter.test.ts')).map(t=>t.test_source).join('\n')+"\n});":"")+"\ndescribe('selected events',()=>{beforeEach(setupCommon);\n"+selected.filter(t=>t.source_file.endsWith('events.test.ts')).map(t=>t.test_source).join('\n')+'\n});\n';
const rootLink=path.join(root,'node_modules');fs.mkdirSync(rootLink,{recursive:true});
for(const name of ['viem','vitest']){let pkg=path.dirname(require.resolve(name));while(!fs.existsSync(path.join(pkg,'package.json'))||JSON.parse(fs.readFileSync(path.join(pkg,'package.json'),'utf8')).name!==name)pkg=path.dirname(pkg);const link=path.join(rootLink,name);if(!fs.existsSync(link))fs.symlinkSync(pkg,link,'dir');}
const folder=path.join(root,'testdata/oracle');fs.mkdirSync(folder,{recursive:true});const link=path.join(folder,'node_modules');if(!fs.existsSync(link))fs.symlinkSync(path.join(up,'packages/core/node_modules'),link,'dir');
const generatedName=mappedOnly?'selected-mapped.test.ts':includeEth?'selected-with-eth.test.ts':'selected-runtime.test.ts';fs.writeFileSync(path.join(folder,generatedName),code);fs.writeFileSync(path.join(root,mappedOnly?'parity/selected-mapped-declarations.json':includeEth?'parity/selected-with-eth-declarations.json':'parity/selected-runtime-declarations.json'),JSON.stringify({baseline:ledger.baseline,generated_file:'testdata/oracle/'+generatedName,method:'Unchanged selected test declarations; actual imported source functions, actual setupCommon/setupAnvil/transferEth/getAccountsIndexingBuild, endpoint adapter only.',sources,generated_sha256:crypto.createHash('sha256').update(code).digest('hex'),cases:meta},null,2)+'\n');
const port=await new Promise((resolve,reject)=>{const s=net.createServer();s.once('error',reject);s.listen(0,'127.0.0.1',()=>{const port=s.address().port;s.close(()=>resolve(port));});});
const anvil=spawn('anvil',['--silent','--no-mining','--host','127.0.0.1','--port',String(port),'--chain-id','1'],{stdio:['ignore','ignore','pipe']});let stderr='';anvil.stderr.on('data',b=>stderr+=b);const endpoint=`http://127.0.0.1:${port}`;
try{let ready=false;for(let i=0;i<150;i++){if(anvil.exitCode!==null)throw Error('Existing Anvil failed: '+stderr);try{const r=await fetch(endpoint,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:1,method:'eth_chainId',params:[]})});if((await r.json()).result==='0x1'){ready=true;break}}catch{}await new Promise(r=>setTimeout(r,20));}if(!ready)throw Error('Task Anvil startup timed out');
 const env={...process.env,CI:'1',PONDER_SELECTED_RPC_URL:endpoint,PONDER_SELECTED_FIXTURE:path.join(root,mappedOnly?'testdata/runtime/selected-mapped-events.json':includeEth?'testdata/runtime/selected-with-eth-events.json':'testdata/runtime/selected-eth-events.json')};
 const child=spawn(path.join(up,'packages/core/node_modules/.bin/vitest'),['run','--config','tools/selected-oracle.config.mjs','--configLoader','native','testdata/oracle/'+generatedName,'--reporter=json','--outputFile='+path.join(root,mappedOnly?'parity/runs/selected-mapped-oracle.json':includeEth?'parity/runs/selected-with-eth-oracle.json':'parity/runs/selected-runtime-oracle.json')],{cwd:root,env,stdio:'inherit'});
 const exit=await new Promise((resolve,reject)=>{child.on('error',reject);child.on('exit',resolve)});process.exitCode=exit??1;
}finally{anvil.kill();await new Promise(r=>anvil.once('exit',r));}
