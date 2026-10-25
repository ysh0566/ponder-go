// Immutable original inputs/operations seed native replay; observations only check results.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {gzipSync,gunzipSync} from 'node:zlib';
import {assertIndexingActionsCapture,indexingActionsTargets} from './source-indexing-actions-evidence.mjs';
const root=path.resolve(import.meta.dirname,'..');
const arg=name=>process.argv.find(a=>a.startsWith('--'+name+'='))?.slice(name.length+3);
const hash=file=>({file:path.relative(root,path.resolve(root,file)),sha256:crypto.createHash('sha256').update(fs.readFileSync(path.resolve(root,file))).digest('hex')});
const output=arg('output'),declarationsOutput=arg('declarations');
if(!output||!declarationsOutput)throw Error('Explicit --output and --declarations required');
if(!path.resolve(root,output).startsWith(root+'/testdata/indexing/source-indexing-actions')||!output.endsWith('.json.gz')||!path.resolve(root,declarationsOutput).startsWith(root+'/parity/source-indexing-actions'))throw Error('Scoped source fixture/declarations required');
const declarations=JSON.parse(fs.readFileSync(root+'/parity/test-ledger.json')).tests.filter(t=>t.source_file==='packages/core/src/indexing/index.test.ts');
const backend_cases={},captures={};
let census;
for(const backend of ['postgres','pglite']){
 const input=arg(backend);if(!input)throw Error('Explicit both backend capture directories required');
 const directory=path.resolve(root,input);if(!['indexing-actions-m39','indexing-profile-m40'].some(stage=>directory.startsWith(root+'/parity/runs/'+stage+'/')))throw Error('Scoped original evidence required');
 const result=JSON.parse(fs.readFileSync(directory+'/result.json')),original=JSON.parse(fs.readFileSync(directory+'/original.json'));
 const captureFile=directory+'/calls.json.gz',capture=JSON.parse(gunzipSync(fs.readFileSync(captureFile)));
 if(!result.complete||!result.success||result.backend!==backend||!result.environment_adapted||result.pristine_file!==false||!result.adapter_verified_after||result.counts.passed!==23||result.counts.failed||result.counts.pending||!original.success)throw Error('Complete unchanged original execution required');
 for(const ref of [...result.inputs,result.context_archive])if(hash(ref.file).sha256!==ref.sha256)throw Error('Capture context changed: '+ref.file);
 census=assertIndexingActionsCapture(capture,declarations);
 backend_cases[backend]=capture.cases;captures[backend]={result:hash(directory+'/result.json'),capture:hash(captureFile),original:hash(directory+'/original.json'),context_archive:result.context_archive,inputs:result.inputs};
}
const fixture={source_credit:0,format:'indexing-actions-source-v1',state:'Actual unchanged original indexing/index.test.ts all23 run, final8 client action targets. Original action ABI/options/event inputs seed native execution; raw provider tapes supply only RPC boundary. Returns, store observations and physical rows are checks, never state seeds.',targets:indexingActionsTargets,backend_cases,captures};
fs.mkdirSync(path.dirname(path.resolve(root,output)),{recursive:true});
const json=Buffer.from(JSON.stringify(fixture)+'\n'),compressed=gzipSync(json,{level:9});if(!gunzipSync(compressed).equals(json))throw Error('Lossless fixture roundtrip failed');
fs.writeFileSync(path.resolve(root,output),compressed);
fs.writeFileSync(path.resolve(root,declarationsOutput),JSON.stringify({source_credit:0,targets:indexingActionsTargets,cases:declarations},null,2)+'\n');
console.log({...census,source_cases:23,fixture:hash(output),declarations:hash(declarationsOutput),uncompressed_bytes:json.length,compressed_bytes:compressed.length});
