import fs from 'node:fs';import path from 'node:path';import crypto from 'node:crypto';import {createRequire} from 'node:module';
const root=process.cwd(),driver='tools/indexing-service-full-verify.mjs',output=root+'/parity/runs/indexing-service-m38/full-verify-02';
const require=createRequire(root+'/../ponder-upstream/package.json'),ts=require('typescript');
const driverText=fs.readFileSync(driver,'utf8'),ast=ts.createSourceFile(driver,driverText,ts.ScriptTarget.Latest,true,ts.ScriptKind.JS);
const wrappers=['source-runtime-index-original','source-runtime-historical-original','source-runtime-historical-log-order','source-runtime-historical-fixture','source-runtime-multichain-original','source-runtime-multichain-probes','source-runtime-multichain-fixture','source-runtime-multichain-probe-fixture'];
const collectors=['source-runtime-index-results','source-runtime-historical-results','source-runtime-multichain-results'];
const hash=file=>crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');
const calls=new Map();function visit(node){if(ts.isCallExpression(node)&&node.expression.getText(ast)==='run'){const args=node.arguments[2];if(args&&ts.isArrayLiteralExpression(args)){const first=args.elements[0];if(first&&ts.isStringLiteral(first)&&wrappers.some(name=>first.text==='tools/'+name+'.mjs')){if(calls.has(first.text))throw Error('duplicate wrapper stage');calls.set(first.text,{label:node.arguments[0].getText(ast),args:args.getText(ast)});}}}ts.forEachChild(node,visit)}visit(ast);
if(calls.size!==8)throw Error('missing wrapper command');
const records=[];
for(const name of [...wrappers,...collectors]){
 const file='tools/'+name+'.mjs',text=fs.readFileSync(file,'utf8');const lines=text.split('\n').filter(line=>line.includes('startsWith(')&&line.includes(')throw Error('));
 const line=lines.find(line=>line.includes('parity/runs/'));if(!line)throw Error('missing actual guard '+file);const begin=line.indexOf('if(')+3,finish=line.indexOf(')throw Error',begin);const condition=line.slice(begin,finish);const reject=Function('root','output','directory','relative','fs','return ('+condition+');');
 const guarded=(value,existing=false)=>reject(root,value,value,path.relative(root,value),{existsSync:()=>existing});
 const checks=[];const check=(label,value,expected,existing=false)=>{const actual=!!guarded(value,existing);if(actual!==expected)throw Error(file+' '+label+' rejected='+actual);checks.push({label,path:path.relative(root,value),existing_result:existing,rejected:actual});};
 const call=calls.get(file);let fullArgs=[];
 if(call){for(const backend of ['postgres','pglite']){const directory=output+'/source-'+backend;const args=Function('output','directory','backend','return '+call.args)(output,directory,backend);fullArgs.push(args);for(const arg of args.filter(a=>/^--(output|postgres|pglite)=/.test(a))){check('exact-full-argument',path.resolve(root,arg.slice(arg.indexOf('=')+1)),false);}}}
 else check('exact-M38-collector-run-dir',output,false);
 for(const stage of ['runtime-index-m35','runtime-historical-m36','runtime-multichain-m37']){if(condition.includes("'"+stage+"'"))check('preserved-'+stage,root+'/parity/runs/'+stage+'/preflight/evidence',false)}
 for(const value of [root+'/parity/runs/arbitrary/evidence',root+'/parity/runs/indexing-service-m380/evidence',root+'/parity/runs/indexing-service-m38-sibling/evidence',root+'/parity/runs/indexing-service-m38',root+'/other/indexing-service-m38/evidence',path.resolve(root,'parity/runs/indexing-service-m38/../arbitrary/evidence')])check('reject-unapproved-or-sibling-prefix',value,true);
 const creates=condition.includes('fs.existsSync');if(creates)check('reject-reused-output',output+'/preflight-existing',true,true);
 records.push({file,sha256:hash(file),guard:condition,full_command:call??null,full_arguments:fullArgs,checks,reused_output_guard:creates?'verified':'not_applicable_readonly_input_or_canonical_collector'});
}
const report={complete:true,success:true,source_credit:0,mode:'static AST extraction of exact full driver wrapper arguments; evaluate actual source guard expressions with controlled existence boundary, no wrapper/test/database execution',script:{file:path.relative(root,import.meta.filename),sha256:hash(import.meta.filename)},driver:{file:driver,sha256:hash(driver)},records,summary:{guards:records.length,wrapper_commands:calls.size,checks:records.reduce((n,r)=>n+r.checks.length,0)}};
const destination='parity/runs/indexing-service-m38/path-preflight-02.json';if(fs.existsSync(destination))throw Error('new evidence required');fs.writeFileSync(destination,JSON.stringify(report,null,2)+'\n');console.log({destination,...report.summary});
