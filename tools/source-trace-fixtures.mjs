// External actual-source vectors. No native acceptance or original-test credit.
import fs from'node:fs';import path from'node:path';import crypto from'node:crypto';import{createRequire}from'node:module';
const root=path.resolve(import.meta.dirname,'..'),upstream=path.resolve(root,'../ponder-upstream'),require=createRequire(upstream+'/packages/core/package.json'),ts=require('typescript'),actionsFile=upstream+'/packages/core/src/rpc/actions.ts',errorsFile=upstream+'/packages/core/src/internal/errors.ts';
function declarations(file,names){const source=ts.createSourceFile(file,fs.readFileSync(file,'utf8'),ts.ScriptTarget.Latest,true),nodes=source.statements.filter(n=>(ts.isFunctionDeclaration(n)||ts.isClassDeclaration(n))?names.includes(n.name?.text):ts.isVariableStatement(n)&&n.declarationList.declarations.some(d=>names.includes(d.name.getText(source))));if(nodes.length!==names.length)throw Error('Actual source declarations changed');return ts.transpileModule(nodes.map(n=>n.getText(source)).join('\n'),{compilerOptions:{target:ts.ScriptTarget.ESNext,module:ts.ModuleKind.ESNext}}).outputText.replaceAll('export ','')}
const action=new Function(declarations(errorsFile,['BaseError','RpcProviderError'])+'\n'+declarations(actionsFile,['debug_traceBlockByHash','standardizeTrace','requestText'])+'\nreturn debug_traceBlockByHash')();
const frame=(extra={})=>({type:'CALL',from:'0x1111111111111111111111111111111111111111',to:'0x2222222222222222222222222222222222222222',input:'0x',...extra}),entry=(result,extra={})=>({txHash:'0x'+ 'a'.repeat(64),result,...extra});
const cases=[
 ['root_defaults',()=>[entry(frame())]],
 ['nested_reverted_sibling_index',()=>[entry(frame({calls:[frame({error:'revert',calls:[frame()]}),frame()]}))]],
 ['explicit_undefined_error',()=>[entry(frame({error:undefined,calls:[frame({error:undefined})]}))]],
 ['explicit_undefined_defaults',()=>[entry(frame({gas:undefined,gasUsed:undefined,calls:undefined}))]],
 ['missing_hash_mutates_before_rejection',()=>{const value=entry(frame({calls:[frame()]}));delete value.txHash;return[value]}],
 ['missing_type_completes_traversal',()=>{const value=frame({calls:[frame()]});delete value.type;return[entry(value),entry(frame())]}],
 ['missing_from_child_after_root_defaults',()=>{const child=frame();delete child.from;return[entry(frame({calls:[child,frame()]}))]}],
 ['missing_input',()=>{const value=frame();delete value.input;return[entry(value)]}],
 ['shared_frame_across_roots',()=>{const value=frame({calls:[frame()]});return[entry(value),entry(value,{txHash:'0x'+'b'.repeat(64)})]}],
 ['empty_response',()=>[]],
 ['explicit_null_error_excludes_subtree',()=>[entry(frame({error:null,calls:[frame()]}))]],
 ['null_gas_retained',()=>[entry(frame({gas:null,gasUsed:null}))]],
];
const output=[];
for(const[name,create]of cases){const ids=new WeakMap();let next=0;function id(value){let n=ids.get(value);if(n===undefined){n=++next;ids.set(value,n)};return n};function mark(value){const seen=new Set();function walk(v){if(v===undefined)return{$undefined:true};if(Array.isArray(v))return v.map(walk);if(v&&typeof v==='object'){const n=id(v);if(seen.has(v))return{$ref:n};seen.add(v);return{$id:n,$object:Object.entries(v).map(([k,v])=>[k,walk(v)])}};return v};return walk(value)}
 const input=create(),before=mark(input),request={method:'debug_traceBlockByHash',params:['0x'+'f'.repeat(64),{tracer:'callTracer'}]};let result,error;try{result=await action({async request(query){if(JSON.stringify(query)!==JSON.stringify(request))throw Error('Unexpected actual source request');return input}},request.params)}catch(e){error={name:e.name,message:e.message,meta:e.meta??[]}}
 const identities=result?.map(trace=>({hash:trace.transactionHash,frame_id:id(trace.trace),root_or_descendant_input_frame:true}));output.push({name,input:before,request,result:result?mark(result):null,error:error??null,after:mark(input),frame_identities:identities??null});
}
const sources=[import.meta.filename,actionsFile,errorsFile,upstream+'/pnpm-lock.yaml'].map(file=>({file:path.relative(root,file),sha256:crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')}));const report={state:'Actual pinned action/declarations and actual error classes, raw provider object input. Preserve in-place frames, shared identity, standardization mutation order and specific validation diagnostics. Native overlay/runtime and original test acceptance pending.',cases:output,sources,source_credit:0};const destination=path.join(root,'testdata/rpc/source-trace-identity.json');fs.writeFileSync(destination,JSON.stringify(report,null,2)+'\n');console.log({vectors:output.length,errors:output.filter(c=>c.error).length,source_credit:0});
