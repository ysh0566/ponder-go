// Complete module verification under the explicitly recorded startup adapter.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {spawnSync} from 'node:child_process';
import {createAdaptedUpstream} from './loopback-adapter.mjs';
import {persistAdaptedContext} from './loopback-context.mjs';
import {sourceFingerprint} from './go-source-fingerprint.mjs';
import {assertPinnedTypechecker} from './pinned-typecheck-preflight.mjs';
const root=path.resolve(import.meta.dirname,'..');
const arg=process.argv.find(x=>x.startsWith('--output='));
if(!arg)throw Error('Explicit new full verification output required');
const output=path.resolve(root,arg.slice(9));
if(!output.startsWith(root+'/parity/runs/runtime-historical-m36/')||fs.existsSync(output))throw Error('New scoped full verification directory required');
fs.mkdirSync(output,{recursive:true});
const report={complete:false,success:false,environment_adapted:true,pristine_file:false,source_credit:0,scope:'All module Go tests, fuzz seeds and examples in disjoint serial batches, normal and race; actual source oracles, SDK, compiler controls, vet and CLI builds.',started_utc:new Date().toISOString(),commands:[]};
const save=()=>fs.writeFileSync(output+'/result.json',JSON.stringify(report,null,2)+'\n');save();
let copy;
function run(name,command,args,options={}){
 const capacity=spawnSync('python3',['/workspace/scratch/fac5abfa4c9e/ponder-cloud/check-capacity.py'],{cwd:root,encoding:'utf8'});
 fs.appendFileSync(output+'/capacity.jsonl',JSON.stringify({name,exit_code:capacity.status,report:capacity.stdout,error:capacity.stderr})+'\n');
 if(capacity.status!==0)throw Error('Capacity hard gate failed before '+name);
 const record={name,command:[command,...args],started_utc:new Date().toISOString()};report.commands.push(record);save();
 const fd=fs.openSync(output+'/'+name+'.log','w');let result;
 try{result=spawnSync(command,args,{cwd:options.cwd??root,env:options.env??process.env,stdio:['ignore',fd,fd]})}finally{fs.closeSync(fd)}
 Object.assign(record,{exit_code:result.status,signal:result.signal,error:result.error?.message??null,finished_utc:new Date().toISOString()});save();
 console.log(JSON.stringify({name,exit_code:result.status}));
 if(result.status!==0&&!options.keepGoing)throw Error('Verification failed: '+name);
 return result.status===0;
}
try{
 if(process.env.NODE_OPTIONS)throw Error('Unexpected NODE_OPTIONS');
 report.typechecker_preflight=assertPinnedTypechecker(path.resolve(root,'../ponder-upstream'));save();
 const url=new URL(process.env.PONDER_TEST_POSTGRES??'');if(url.hostname!=='127.0.0.1'||url.port!=='55432'||url.pathname!=='/ponder_test')throw Error('Explicit task PostgreSQL 55432 required');
 copy=createAdaptedUpstream();fs.writeFileSync(output+'/adapter-manifest.json',JSON.stringify(copy.manifest,null,2)+'\n');fs.writeFileSync(output+'/adapter.diff',copy.diff);
 process.env.PONDER_UPSTREAM_ROOT=copy.directory;process.env.CI='1';process.env.PONDER_RUN_DIR=output+'/native';fs.mkdirSync(process.env.PONDER_RUN_DIR,{recursive:true});
 run('runtime-historical-evidence-controls',process.execPath,['--test','tools/source-runtime-historical-evidence.test.mjs']);
 const remainingOriginalFiles=['database/actions','database/index','indexing-store/cache','indexing-store/index','indexing-store/profile','runtime/events','runtime/filter','sync-realtime/bloom','utils/sql-parse'].map(name=>'src/'+name+'.test.ts');
 for(const backend of ['postgres','pglite']){
  const env={...process.env};if(backend==='postgres')env.DATABASE_URL=process.env.PONDER_TEST_POSTGRES;else delete env.DATABASE_URL;
  run('remaining-mapped-original-'+backend,root+'/../ponder-upstream/packages/core/node_modules/.bin/vitest',['run','--config','tools/full-oracle.config.mjs','--configLoader','native',...remainingOriginalFiles,'--reporter=json','--outputFile='+output+'/remaining-mapped-original-'+backend+'.json'],{env});
 }
 fs.copyFileSync(root+'/testdata/runtime/source-runtime-index-live.json',output+'/previous-runtime-index-fixture.json');
 const sourceCaptures={};
 for(const backend of ['postgres','pglite']){
  const directory=output+'/source-'+backend;
  run('runtime-index-source-'+backend,process.execPath,['tools/source-runtime-index-original.mjs','--backend='+backend,'--output='+directory]);
  const source=JSON.parse(fs.readFileSync(directory+'/result.json'));
  if(!source.complete||!source.success||source.counts.passed!==15||!source.environment_adapted||source.pristine_file!==false)throw Error('Complete runtime source capture required');
  sourceCaptures[backend]=JSON.parse(fs.readFileSync(directory+'/calls.json'));
 }
 const fixture={...sourceCaptures.postgres,backend_cases:Object.fromEntries(Object.entries(sourceCaptures).map(([backend,value])=>[backend,value.cases])),native_replay_method:'Each real Go database backend consumes its corresponding complete source backend capture. Expected outputs are comparison-only.'};
 fs.writeFileSync(root+'/testdata/runtime/source-runtime-index-live.json',JSON.stringify(fixture,null,2)+'\n');
 fs.copyFileSync(root+'/testdata/runtime/source-runtime-historical-live.json',output+'/previous-runtime-historical-fixture.json');
 for(const backend of ['postgres','pglite']){
  run('runtime-historical-source-'+backend,process.execPath,['tools/source-runtime-historical-original.mjs','--backend='+backend,'--output='+output+'/historical-source-'+backend]);
  run('runtime-historical-log-order-'+backend,process.execPath,['tools/source-runtime-historical-log-order.mjs','--backend='+backend,'--output='+output+'/historical-log-order-'+backend]);
 }
 run('runtime-historical-fixture',process.execPath,['tools/source-runtime-historical-fixture.mjs','--postgres='+output+'/historical-source-postgres','--pglite='+output+'/historical-source-pglite']);
 for(const [i,file] of ['parity/runs/sync-store-m32/type-compile.json','testdata/runtime/source-historical-types.json'].entries())if(fs.existsSync(root+'/'+file))fs.copyFileSync(root+'/'+file,output+'/previous-type-control-'+i+'.json');
 run('sync-type-controls',process.execPath,['tools/source-sync-compile.mjs','--write']);
 run('historical-type-controls',process.execPath,['tools/source-historical-types/compile.mjs']);
 run('sdk-client-build',root+'/sdk/typescript/client/node_modules/.bin/tsup',[],{cwd:root+'/sdk/typescript/client'});
 run('prepared-native-inputs',process.execPath,['tools/record-native-inputs.mjs']);
 fs.copyFileSync(root+'/parity/runs/prepared-native-inputs.json',output+'/prepared-native-inputs.json');
 report.prepared_native_inputs={file:path.relative(root,output+'/prepared-native-inputs.json'),sha256:crypto.createHash('sha256').update(fs.readFileSync(output+'/prepared-native-inputs.json')).digest('hex')};
 report.native_inputs=sourceFingerprint(root);save();
 const beforeFiles=['tools/go-tests.mjs','tools/runtime-historical-full-verify.mjs','tools/pinned-typecheck-preflight.mjs','tools/loopback-adapter.mjs','tools/loopback-context.mjs','tools/client-compat-launch.mjs','tools/client-compat-provenance.mjs','tools/source-runtime-index-live.setup.ts','tools/source-runtime-index-original.mjs','tools/source-runtime-historical-original.mjs','tools/source-runtime-historical-live.config.mjs','tools/source-runtime-historical-live.setup.ts','tools/source-runtime-historical-fixture.mjs','tools/source-runtime-historical-evidence.mjs','tools/source-runtime-historical-evidence.test.mjs','tools/source-runtime-historical-log-order.mjs','tools/source-runtime-historical-log-order.config.mjs','tools/source-runtime-historical-log-order.test.ts'].map(file=>({file,sha256:crypto.createHash('sha256').update(fs.readFileSync(root+'/'+file)).digest('hex')}));report.harness_inputs=beforeFiles;save();
 run('abi-generated-normal','go',['test','-count=1','-json','./abi/testdata/compile/generated']);
 run('abi-generated-race','go',['test','-count=1','-race','-json','./abi/testdata/compile/generated']);
 const normal=run('go-normal',process.execPath,['tools/go-tests.mjs'],{keepGoing:true});
 const race=run('go-race',process.execPath,['tools/go-tests.mjs','--race'],{keepGoing:true});
 run('go-vet','go',['vet','./...']);
 fs.mkdirSync(root+'/bin',{recursive:true});run('cli-build','go',['build','-o','bin/ponder','./cmd/ponder']);run('erc20-build','go',['build','-o','bin/erc20','./examples/erc20']);
 run('core-oracle',root+'/../ponder-upstream/packages/core/node_modules/.bin/vitest',['run','--config','tools/oracle.config.mjs','--configLoader','native','--maxWorkers=2','src/utils/checkpoint.test.ts','src/utils/interval.test.ts','src/utils/finality.test.ts','src/build/schema.test.ts','src/utils/decodeEventLog.test.ts','src/utils/decodeAbiParameters.test.ts','src/build/factory.test.ts','src/utils/offset.test.ts','src/indexing-store/utils.test.ts','src/runtime/fragments.test.ts','src/rpc/actions.test.ts','--reporter=json','--outputFile='+output+'/core-oracle.json']);
 run('parameters-types',process.execPath,['tools/parameters-typecheck.mjs']);
 run('sync-types',process.execPath,['tools/source-sync-types.mjs']);
 run('historical-types',process.execPath,['tools/source-historical-types.mjs']);
 run('runtime-index-types',process.execPath,['tools/source-runtime-index-types.mjs'],{env:{...process.env,PONDER_UPSTREAM_ROOT:path.resolve(root,'../ponder-upstream')}});
 run('sdk-client-tests',root+'/sdk/typescript/client/node_modules/.bin/vitest',['run','--configLoader','native','--maxWorkers=2','--reporter=json','--outputFile='+output+'/sdk-client.json'],{cwd:root+'/sdk/typescript/client'});
 run('sdk-react-types',root+'/sdk/typescript/react/node_modules/.bin/vitest',['run','--typecheck.only','--configLoader','native','--maxWorkers=2','--reporter=json','--outputFile='+output+'/sdk-react.json'],{cwd:root+'/sdk/typescript/react'});
 report.finished_native_inputs=sourceFingerprint(root);
 if(report.native_inputs.sha256!==report.finished_native_inputs.sha256)throw Error('Native inputs changed across aggregate verification');
 for(const entry of beforeFiles)if(crypto.createHash('sha256').update(fs.readFileSync(root+'/'+entry.file)).digest('hex')!==entry.sha256)throw Error('Verification harness changed: '+entry.file);
 report.success=normal&&race;
}catch(error){report.error=String(error.stack??error);report.success=false;process.exitCode=1;}finally{
 if(copy){try{copy.verify();const persisted=persistAdaptedContext(copy,output);report.context_archive={file:path.relative(root,persisted.path),sha256:persisted.sha256};report.source_manifest_verified_after=true}catch(error){report.error=String(error.stack??error);report.success=false;process.exitCode=1}finally{copy.dispose()}}
 report.complete=true;report.finished_utc=new Date().toISOString();save();console.log(JSON.stringify({output,success:report.success,error:report.error}));if(!report.success)process.exitCode=1;
}
