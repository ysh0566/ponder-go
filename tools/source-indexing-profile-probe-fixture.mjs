import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {gzipSync} from 'node:zlib';
import {assertIndexingProfileProbeCapture,indexingProfileProbeTitles} from './source-indexing-profile-evidence.mjs';
import {validateContextEntry} from './loopback-context.mjs';
const root=path.resolve(import.meta.dirname,'..'),arg=name=>process.argv.find(a=>a.startsWith('--'+name+'='))?.slice(name.length+3);
const output=arg('output');if(!output||!path.resolve(root,output).startsWith(root+'/testdata/indexing/source-indexing-profile-')||!output.endsWith('.json.gz'))throw Error('Explicit scoped output required');
const read=f=>JSON.parse(fs.readFileSync(path.resolve(root,f)));
const hash=f=>({file:path.relative(root,path.resolve(root,f)),sha256:crypto.createHash('sha256').update(fs.readFileSync(path.resolve(root,f))).digest('hex')});
const backend_cases={},captures={};
for(const backend of ['postgres','pglite']){
 const input=arg(backend);if(!input||!['indexing-profile-m40','independent-storage-m41'].some(stage=>path.resolve(root,input).startsWith(root+'/parity/runs/'+stage+'/')))throw Error('Explicit scoped both backend inputs required');
 const result=read(input+'/result.json'),capture=read(input+'/calls.json'),original=read(input+'/original.json');
 if(!result.complete||!result.success||result.backend!==backend||result.exit_code!==0||!result.adapter_verified_after||result.counts.passed!==2||result.counts.failed||result.counts.pending||!original.success||original.testResults.length!==1||original.testResults[0].assertionResults.length!==2||!indexingProfileProbeTitles.every(title=>original.testResults[0].assertionResults.some(t=>t.fullName===title&&t.status==='passed')))throw Error('Complete exact real probe execution required');
 for(const ref of [...result.inputs,result.context_archive])if(hash(ref.file).sha256!==ref.sha256)throw Error('Current probe inputs/context changed '+ref.file);
 const context=read(result.context_archive.file);if(!Object.values(context.entries).every(e=>validateContextEntry({file:e.executed_file,sha256:e.sha256},context)))throw Error('Archived source bytes changed');
 assertIndexingProfileProbeCapture(capture);backend_cases[backend]=capture.cases;captures[backend]={result:hash(input+'/result.json'),capture:hash(input+'/calls.json'),original:hash(input+'/original.json'),inputs:result.inputs,context_archive:result.context_archive};
}
fs.writeFileSync(path.resolve(root,output),gzipSync(JSON.stringify({format:'indexing-profile-probes-v1',source_credit:0,real_rpc:false,real_database:false,backend_cases,captures})+'\n'));
console.log({source_credit:0,fixture:hash(output)});
