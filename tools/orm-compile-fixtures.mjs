import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import os from 'node:os';
import {spawnSync} from 'node:child_process';
import {buildGenerator} from './go-codegen.mjs';
const root=path.resolve(import.meta.dirname,'..');
const runDirectory=path.resolve(root,process.env.PONDER_RUN_DIR??'parity/runs');
const hash=value=>crypto.createHash('sha256').update(value).digest('hex');
function command(args){const r=spawnSync('go',args,{cwd:root,encoding:'utf8',timeout:45000,maxBuffer:8*1024*1024});if(r.error)throw r.error;return r;}
const codegen=buildGenerator(root,'./cmd/ponder','ponder-codegen',path.join(runDirectory,'orm-codegen-stages.json'));
for(const fixture of ['strict','nullable'])codegen(fixture,['codegen','--typed','--schema',`orm/testdata/${fixture}/model.json`,'--out',`orm/testdata/${fixture}/models.go`,'--package',fixture]);
const ledger=JSON.parse(fs.readFileSync(path.join(root,'parity/test-ledger.json'),'utf8'));
const zero='0x0000000000000000000000000000000000000000';
const alice='0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266';
const strict='ponder.local/ponder-go/orm/testdata/strict',nullable='ponder.local/ponder-go/orm/testdata/nullable';
const cases=[];
function directive(title,index,imports,setup,good,bad,diagnostic,adaptation){
 const source=ledger.tests.find(x=>x.source_file.endsWith('indexing-store/index.test.ts')&&x.title===title);
 if(!source?.type_error_directives[index])throw Error('Missing source directive '+title+':'+index);
 cases.push({name:source.id+'_'+index,source_id:source.id,source_sha256:source.source_sha256,directive_index:index,source_name:source.full_name,imports,setup,good,bad,diagnostic,adaptation});
}
directive('update throw error when primary key is updated',0,[strict],'',`_ = strict.AccountUpdate{}`,`_ = strict.AccountUpdate{Address:${JSON.stringify(alice)}}`,'unknown field Address','Generated update omits primary keys. Runtime intentionally invalid Row calls retain both original error assertions separately.');
directive('update throw error when primary key is updated',1,['math/big','ponder.local/ponder-go/orm',strict],'',`_ = strict.AccountUpdate{Balance:orm.Set(*big.NewInt(20))}`,`_ = strict.AccountUpdate{Address:${JSON.stringify(zero)},Balance:orm.Set(*big.NewInt(20))}`,'unknown field Address','Even assigning the unchanged PK is a type error, matching the second source directive. Dynamic runtime case still permits same-PK values.');
directive('missing rows',0,['math/big',strict],'_ = big.NewInt(10)',`_ = strict.NewAccountInsert(${JSON.stringify(zero)},*big.NewInt(10))`,`_ = strict.NewAccountInsert(${JSON.stringify(zero)})`,'not enough arguments','Required insert constructor rejects omitted balance. Go zero values cannot be forbidden syntactically and are runtime-guarded; no struct field-presence claim.');
directive('onchain table',0,['ponder.local/ponder-go/schema',strict],'table := schema.Table{Name:"account",Columns:[]schema.Column{{Name:"address",Type:"text",Primary:true},{Name:"balance",Type:"integer",NotNull:true}}}; _ = table; _ = strict.Account',`_ = strict.Account.Bind(nil)`,`_ = table.Bind(nil)`,'has no field or method Bind','Plain SQL schema descriptors have no typed onchain binding method.');
directive('notNull',0,[strict,nullable],'_ = strict.Account; model := nullable.Account; _ = model',`model = nullable.Account`,`model = strict.Account`,'cannot use','Nullable and non-null generated model definitions have different key/insert/update/row types, rejecting source model reassignment.');
directive('sql',0,['context','math/big',strict],'_ = big.NewInt(10)',`_,_ = strict.Account.Bind(nil).SQL().Insert(context.Background(),strict.NewAccountInsert("0x0000000000000000000000000000000000000001",*big.NewInt(10)))`,`_,_ = strict.Account.Bind(nil).SQL().Insert(context.Background(),strict.NewAccountInsert("0x0000000000000000000000000000000000000001",nil))`,'cannot use nil','Eager SQL Insert uses the generated required balance big.Int constructor. Source undefined is rejected by the real Go compiler; dynamic SQLInsert separately executes NULL and duplicate-key constraints with the original savepoint sequence.');
const supplemental=[
 {name:'wrong_balance_type',imports:['ponder.local/ponder-go/orm',strict],setup:'_ = orm.Set(1)',good:'_ = strict.AccountUpdate{}',bad:'_ = strict.AccountUpdate{Balance:orm.Set("20")}',diagnostic:'cannot use'},
 {name:'unknown_update_column',imports:[strict],setup:'',good:'_ = strict.AccountUpdate{}',bad:'_ = strict.AccountUpdate{Unknown:1}',diagnostic:'unknown field Unknown'},
 {name:'wrong_insert_model',imports:['context',strict,nullable],setup:'_ = strict.Account; _ = nullable.Account',good:`_,_ = nullable.Account.Bind(nil).Insert(context.Background(),nullable.NewAccountInsert(${JSON.stringify(zero)}))`,bad:`_,_ = strict.Account.Bind(nil).Insert(context.Background(),nullable.NewAccountInsert(${JSON.stringify(zero)}))`,diagnostic:'cannot use'},
 {name:'null_not_null_value',imports:['math/big',strict],setup:'_ = big.NewInt(10)',good:`_ = strict.NewAccountInsert(${JSON.stringify(zero)},*big.NewInt(10))`,bad:`_ = strict.NewAccountInsert(${JSON.stringify(zero)},nil)`,diagnostic:'cannot use nil'},
 {name:'wrong_key_value',imports:[strict],setup:'',good:`_ = strict.NewAccountKey(${JSON.stringify(zero)})`,bad:'_ = strict.NewAccountKey(123)',diagnostic:'cannot use'},
 {name:'missing_key',imports:[strict],setup:'',good:`_ = strict.NewAccountKey(${JSON.stringify(zero)})`,bad:'_ = strict.NewAccountKey()',diagnostic:'not enough arguments'},
];
for(const c of supplemental)cases.push({...c,supplemental:true});
const scratch=fs.mkdtempSync(path.join(os.tmpdir(),'ponder-orm-compile-'));
const report={baseline:ledger.baseline,tool:command(['version']).stdout.trim(),inputs:[],method:'Real Go build of independent public typed model API calls. Required constructor arity, nominal model identity, update column sets and value types are compiler-enforced; no runtime error counts as compilation rejection. Go zero-value syntax is explicitly runtime guarded.',directives:[],supplementary:[],generated:[],summary:{source_error_directives:0,source_error_directives_passed:0,supplemental_negative_controls:0,supplemental_rejected:0,failed:0}};
try{
 for(const c of cases){
  const fixtures={};
  for(const kind of ['positive','negative']){
   const folder=kind==='positive'?'pass':'fail',file=`orm/testdata/compile/${folder}/${c.name}.go`;
   const source=`package compilefixture\nimport (${c.imports.map(x=>JSON.stringify(x)).join(';')})\nfunc check(){ ${c.setup}; ${kind==='positive'?c.good:c.bad} }\n`;
   fs.writeFileSync(path.join(root,file),source);
   const result=command(['build','-o',path.join(scratch,c.name+'.a'),file]);
   const expected=kind==='positive'?result.status===0:result.status!==0&&(result.stdout+result.stderr).includes(c.diagnostic);
   fixtures[kind]={file,sha256:hash(source),exit_code:result.status,expected,diagnostics:(result.stdout+result.stderr).trim()};
  }
  const status=fixtures.positive.expected&&fixtures.negative.expected?'passed':'failed';
  const item={name:c.name,source_id:c.source_id,source_sha256:c.source_sha256,directive_index:c.directive_index,source_name:c.source_name,status,adaptation:c.adaptation,...fixtures};
  if(c.supplemental){report.supplementary.push(item);report.summary.supplemental_negative_controls++;if(status==='passed')report.summary.supplemental_rejected++;}else{report.directives.push(item);report.summary.source_error_directives++;if(status==='passed')report.summary.source_error_directives_passed++;}
  if(status!=='passed')report.summary.failed++;
 }
 for(const file of ['orm/orm.go','orm/generate.go','cmd/ponder/main.go','tools/orm-compile-fixtures.mjs','tools/go-codegen.mjs'])report.inputs.push({file,sha256:hash(fs.readFileSync(path.join(root,file)))});
 for(const fixture of ['strict','nullable']){const file=`orm/testdata/${fixture}/models.go`;report.generated.push({file,sha256:hash(fs.readFileSync(path.join(root,file)))});}
 fs.writeFileSync(path.join(runDirectory,'orm-compile-fixtures.json'),JSON.stringify(report,null,2)+'\n');
 console.log(report.summary);
 if(report.summary.failed)throw Error('ORM compile fixtures failed');
}finally{fs.rmSync(scratch,{recursive:true,force:true});}
