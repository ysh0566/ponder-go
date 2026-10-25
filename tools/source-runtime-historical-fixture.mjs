// Prepare immutable-source capture input for native replay. Never derive native
// database state or expected implementation behavior from generator outputs.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {sameOriginalDeclaration} from './source-runtime-historical-evidence.mjs';
const root=path.resolve(import.meta.dirname,'..');
const arg=name=>process.argv.find(a=>a.startsWith('--'+name+'='))?.slice(name.length+3);
const hash=file=>({file:path.relative(root,path.resolve(root,file)),sha256:crypto.createHash('sha256').update(fs.readFileSync(path.resolve(root,file))).digest('hex')});
const ledger=JSON.parse(fs.readFileSync(root+'/parity/test-ledger.json','utf8'));
const declarations=ledger.tests.filter(t=>t.source_file==='packages/core/src/runtime/historical.test.ts');
if(declarations.length!==14||declarations.reduce((n,t)=>n+t.assertions.length,0)!==29||declarations.some(t=>t.type_error_directives.length))throw Error('Original historical census changed');
const backend_cases={},captures={};
for(const backend of ['postgres','pglite']){
 const input=arg(backend);if(!input)throw Error('Explicit both backend capture directories required');
 const directory=path.resolve(root,input);if(!['runtime-historical-m36','runtime-multichain-m37','indexing-service-m38','indexing-actions-m39','indexing-profile-m40','independent-storage-m41'].some(stage=>directory.startsWith(root+'/parity/runs/'+stage+'/')))throw Error('Scoped historical evidence required');
 const result=JSON.parse(fs.readFileSync(directory+'/result.json','utf8')),original=JSON.parse(fs.readFileSync(directory+'/original.json','utf8')),capture=JSON.parse(fs.readFileSync(directory+'/calls.json','utf8'));
 if(!result.complete||!result.success||result.backend!==backend||!result.environment_adapted||result.pristine_file!==false||!result.adapter_verified_after||result.counts.passed!==14||result.counts.failed||result.counts.pending||!original.success||capture.cases.length!==14)throw Error('Complete unchanged original backend execution required');
 for(const ref of [...result.inputs,result.context_archive])if(hash(ref.file).sha256!==ref.sha256)throw Error('Capture context changed: '+ref.file);
 for(let i=0;i<declarations.length;i++){
  const expected=declarations[i],actual=capture.cases.find(c=>c.id===expected.id);
  if(!sameOriginalDeclaration(actual,expected))throw Error('Original declaration changed: '+expected.id);
  if(!actual.calls.length||actual.targeted!==(i<11))throw Error('Missing actual generator call');
  if(i<11){
   for(const [index,call] of actual.calls.entries()){
    if(!call.physical_before||!call.physical_after||!call.after||call.error||!call.results.length)throw Error('Incomplete original generator capture');
    if(index===0&&Object.values(call.physical_before).some(rows=>rows.length))throw Error('Initial original database must be empty; never seed native from expected state');
   }
  }
 }
 const targeted=capture.cases.slice(0,11);if(targeted.reduce((n,c)=>n+c.calls.length,0)!==14)throw Error('Original targeted call census changed');
 backend_cases[backend]=capture.cases;captures[backend]={result:hash(directory+'/result.json'),capture:hash(directory+'/calls.json'),original:hash(directory+'/original.json'),context_archive:result.context_archive,inputs:result.inputs};
}
const fixture={source_credit:0,state:'Actual unchanged original historical.test.ts captures. First eleven cases have native candidates; final three remain unimplemented. Generator outputs/event-build inputs/physical rows are comparison targets, never state seeds.',cases:backend_cases.postgres,backend_cases,captures};
fs.writeFileSync(root+'/testdata/runtime/source-runtime-historical-live.json',JSON.stringify(fixture,null,2)+'\n');
fs.writeFileSync(root+'/parity/source-runtime-historical-declarations.json',JSON.stringify({source_credit:0,cases:declarations},null,2)+'\n');
console.log({source_cases:14,targeted_cases:11,targeted_generator_calls:14,assertions:29,targeted_assertions:24,type_directives:0});
