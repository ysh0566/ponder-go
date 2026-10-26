// Source-only collector. Never creates formal coverage credit.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {gunzipSync} from 'node:zlib';
import {assertIndexingProfileCapture} from './source-indexing-profile-evidence.mjs';
import {validateContextEntry} from './loopback-context.mjs';
const root=path.resolve(import.meta.dirname,'..'),arg=name=>process.argv.find(a=>a.startsWith('--'+name+'='))?.slice(name.length+3);
const hash=file=>({file:path.relative(root,path.resolve(root,file)),sha256:crypto.createHash('sha256').update(fs.readFileSync(path.resolve(root,file))).digest('hex')});
const read=file=>JSON.parse(fs.readFileSync(path.resolve(root,file)));
const check=(value,message)=>{if(!value)throw Error(message)};
const fixturePath=arg('fixture'),output=arg('output');
check(fixturePath&&output&&arg('postgres')&&arg('pglite'),'Explicit --postgres --pglite --fixture --output required');
check(['indexing-profile-m40','independent-storage-m41','multichain-atomic-m42','finite-historical-m43'].some(stage=>path.resolve(root,output).startsWith(root+'/parity/runs/'+stage+'/'))&&!fs.existsSync(path.resolve(root,output)),'New scoped collector output required');
const fixture=JSON.parse(gunzipSync(fs.readFileSync(path.resolve(root,fixturePath))));
const declarations=read('parity/test-ledger.json').tests.filter(t=>['packages/core/src/indexing/profile.test.ts','packages/core/src/indexing/client.test.ts'].includes(t.source_file));
const backends={};
for(const backend of ['postgres','pglite']){
 const directory=path.resolve(root,arg(backend));check(['indexing-profile-m40','independent-storage-m41','multichain-atomic-m42','finite-historical-m43'].some(stage=>directory.startsWith(root+'/parity/runs/'+stage+'/')),'Scoped original source required');
 const result=read(directory+'/result.json'),original=read(directory+'/original.json'),capture=JSON.parse(gunzipSync(fs.readFileSync(directory+'/calls.json.gz')));
 check(result.complete&&result.success&&result.exit_code===0&&result.backend===backend&&result.environment_adapted&&result.pristine_file===false&&result.adapter_verified_after&&result.counts.passed===24&&!result.counts.failed&&!result.counts.pending,'Complete unchanged original source execution required');
 for(const ref of [...result.inputs,result.context_archive])check(hash(ref.file).sha256===ref.sha256,'Current context/input differs: '+ref.file);
 const context=read(result.context_archive.file);check(Object.values(context.entries).every(e=>validateContextEntry({file:e.executed_file,sha256:e.sha256},context)),'Persisted source bytes changed');
 const executed=original.testResults.flatMap(t=>t.assertionResults);check(original.success&&executed.length===24&&declarations.every(d=>executed.filter(t=>t.fullName===d.full_name&&t.status==='passed').length===1),'Exact unchanged original labels and pass/skip states required');
 const census=assertIndexingProfileCapture(capture,declarations);
 check(JSON.stringify(fixture.backend_cases[backend])===JSON.stringify(capture.cases),'Fixture differs from actual capture');
 const refs=fixture.captures[backend];for(const ref of [refs.result,refs.original,refs.capture])check(hash(ref.file).sha256===ref.sha256,'Fixture source hash changed');
 check(refs.capture.file===path.relative(root,directory+'/calls.json.gz'),'Fixture came from a different source run');
 backends[backend]={...census,result:hash(directory+'/result.json'),capture:hash(directory+'/calls.json.gz'),original:hash(directory+'/original.json'),context_archive:result.context_archive};
}
const report={complete:true,success:true,source_credit:0,state:'Source-only evidence, no native parity or type-equivalence credit.',fixture:hash(fixturePath),backends};
fs.mkdirSync(path.dirname(path.resolve(root,output)),{recursive:true});fs.writeFileSync(path.resolve(root,output),JSON.stringify(report,null,2)+'\n');console.log({success:true,source_credit:0,output});
