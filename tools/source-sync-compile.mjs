// Preparation-only native type controls. No runtime or source credit on compile.
import fs from'node:fs';import path from'node:path';import crypto from'node:crypto';import{spawnSync}from'node:child_process';import{pathToFileURL}from'node:url';
const root=process.env.PONDER_PORT_ROOT??path.resolve(import.meta.dirname,'..');const upstream=path.resolve(root,'../ponder-upstream');
const{orderObject}=await import(pathToFileURL(upstream+'/packages/core/src/utils/order.ts'));
const ledger=JSON.parse(fs.readFileSync(root+'/parity/test-ledger.json','utf8')),selected=ledger.tests.filter(t=>t.source_file==='packages/core/src/sync-store/index.test.ts'&&t.type_error_directives.length);if(selected.length!==4||selected.reduce((n,t)=>n+t.type_error_directives.length,0)!==9)throw Error('Wrong exact source obligations');
const directory=path.join(root,'store/testdata/compile/source-sync'),build=path.join(root,'.parity-build/source-sync-types');fs.mkdirSync(directory,{recursive:true});fs.mkdirSync(build,{recursive:true});
const write=process.argv.includes('--write');const fixture=(file,text)=>{if(write){if(!fs.existsSync(file)||fs.readFileSync(file,'utf8')!==text)fs.writeFileSync(file,text)}else if(!fs.existsSync(file)||fs.readFileSync(file,'utf8')!==text)throw Error('Prepare immutable type fixture before native tests: '+file)};
const run=args=>{const p=spawnSync('go',args,{cwd:root,encoding:'utf8'});return{command:['go',...args],exit_code:p.status,stdout:p.stdout,stderr:p.stderr,error:p.error?.message??null}};
const hash=file=>({file:path.relative(root,file),sha256:crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')});
const positiveRequest={method:'eth_call',params:[{}]},expectedHash=crypto.createHash('md5').update(JSON.stringify(orderObject(positiveRequest)).toLowerCase()).digest('hex');const directives=[];
for(const test of selected){
 const fragments=[...test.test_source.matchAll(/@ts-expect-error\s*\n\s*(request:\s*)?\{\s*method:\s*"eth_call",\s*params:\s*\[\s*"(0x[0-9]+)"\s*\]/g)];if(fragments.length!==test.type_error_directives.length)throw Error('Changed original request literals: '+test.id);
 for(const[index,fragment]of fragments.entries()){
  const bad=fragment[2],mode=fragment[1]?'insert':'get',block=test.title==='pruneRpcRequestResult'?Number(BigInt(bad)):1,storageResult=test.title==='pruneRpcRequestResult'?bad:'0x1';
  const source=`package main
import("context";"encoding/json";"fmt";"net/url";"os";"strings";"ponder.local/ponder-go/rpc";"ponder.local/ponder-go/schema";"ponder.local/ponder-go/store")
func main(){
 var input struct{Backend,DatabaseURL,RPCURL,Namespace string};if err:=json.NewDecoder(os.Stdin).Decode(&input);err!=nil{panic(err)}
 if !strings.HasPrefix(input.Namespace,"ponder_go_src_type_"){panic("task namespace required")}
 endpoint,err:=url.Parse(input.RPCURL);if err!=nil||(endpoint.Hostname()!="127.0.0.1"&&endpoint.Hostname()!="localhost"){panic("task loopback RPC required")}
 ctx:=context.Background();var backend store.Backend
 if input.Backend=="postgres"{database,parseErr:=url.Parse(input.DatabaseURL);if parseErr!=nil||database.Hostname()!="127.0.0.1"||(database.Port()!="53398"&&!(os.Getenv("PONDER_UPSTREAM_ROOT")!=""&&database.Port()=="55432"))||database.Path!="/ponder_test"{panic("explicit task PostgreSQL required")};backend,err=store.OpenPostgres(ctx,input.DatabaseURL)}else if input.Backend=="pglite"{backend,err=store.OpenPGlite(ctx,"memory://")}else{panic("explicit backend required")};if err!=nil{panic(err)};defer backend.Close()
 model:=schema.Schema{Tables:[]schema.Table{{Name:"type_probe",Columns:[]schema.Column{{Name:"id",Type:"text",Primary:true}}}}}
 db,err:=store.New(ctx,backend,model,input.Namespace,schema.Multichain);if err!=nil{panic(err)};cache,err:=db.EnableProtocolCache(ctx);if err!=nil{panic(err)};source,err:=cache.SourceSyncStore(0);if err!=nil{panic(err)}
 req:=rpc.SourceCallRequest{Params:rpc.SourceCallParameters{Transaction:map[string]any{}}}
 client:=rpc.New(input.RPCURL);client.ChainID=1;client.RequestsPerSecond=0;var rpcResult string;if err=client.CallSource(ctx,req,&rpcResult);err!=nil{panic(err)};if rpcResult!="0x"{panic("remaining empty transaction RPC result changed: "+rpcResult)}
 block:=uint64(${block});if err=source.InsertRPCRequestResults(ctx,1,[]store.SourceRPCResult{{Request:req,BlockNumber:&block,Result:${JSON.stringify(storageResult)}}});err!=nil{panic(err)}
 values,err:=source.GetRPCRequestResults(ctx,1,[]rpc.SourceRequest{req});if err!=nil||len(values)!=1||values[0]==nil||*values[0]!=${JSON.stringify(storageResult)}{panic(fmt.Sprint("actual typed storage mismatch ",values," ",err))}
 rows,err:=backend.Query(ctx,"SELECT request_hash FROM "+schema.Quote(cache.Namespace())+".rpc_request_results WHERE chain_id=1");if err!=nil||len(rows.Rows)!=1||rows.Rows[0]["request_hash"]!=${JSON.stringify(expectedHash)}{panic(fmt.Sprint("actual source positive hash mismatch ",rows," ",err))}

 if err=source.PruneRPCRequestResults(ctx,1,[]uint64{block});err!=nil{panic(err)};values,err=source.GetRPCRequestResults(ctx,1,[]rpc.SourceRequest{req});if err!=nil||len(values)!=1||values[0]!=nil{panic("actual typed prune mismatch")}
 if err=json.NewEncoder(os.Stdout).Encode(map[string]any{"source_id":${JSON.stringify(test.id)},"directive_index":${index},"rpc_result":rpcResult,"request_hash":${JSON.stringify(expectedHash)},"storage_result":${JSON.stringify(storageResult)},"passed":true});err!=nil{panic(err)}
}
`;
  const key=test.id+'-'+index,positive=path.join(directory,key+'-positive.go'),negative=path.join(directory,key+'-negative.go.txt');const negativeText=source.replace('Params:rpc.SourceCallParameters{Transaction:map[string]any{}}','Params:[]string{'+JSON.stringify(bad)+'}');if(source===negativeText)throw Error('Native negative did not preserve actual wrong tuple');
  const format=text=>{const p=spawnSync('gofmt',[],{cwd:root,encoding:'utf8',input:text});if(p.status!==0)throw Error(p.stderr);return p.stdout};fixture(positive,format(source));fixture(negative,format(negativeText));
  const actualNegative=path.join(build,key+'-negative.go');fs.copyFileSync(negative,actualNegative);const binary=path.join(build,key+'-positive');const good=run(['build','-o',binary,positive]),badBuild=run(['build','-o',path.join(build,key+'-negative'),actualNegative]);
  const diagnostic=/cannot use \[\]string.*as rpc\.SourceCallParameters value/.test(badBuild.stderr);const valid=good.exit_code===0&&!good.error&&badBuild.exit_code>0&&!badBuild.error&&diagnostic;
  directives.push({source_id:test.id,source_sha256:test.source_sha256,source_directive_index:index,source_error_directive:test.type_error_directives[index],original_request:{method:'eth_call',params:[bad]},positive_request:positiveRequest,control_note:'Only invalid transaction slot changes to empty object; independent real typed storage/RPC lifecycle, raw original fullcase retains all badrequestliterals/otherinputs/assertions. Full generic RPC schema not claimed.',mode,positive:hash(positive),negative:hash(negative),positive_binary:binary,positive_binary_sha256:good.exit_code===0?hash(binary).sha256:null,build_positive:good,build_negative:badBuild,status:valid?'compiled_runtime_pending':'failed'});
 }
}
const report={state:'Compiled controls only; everypositive must execute against original live Anvil andactualPG/PGlite before native type/source credit.',summary:{source_error_directives:9,compiled:directives.filter(d=>d.status==='compiled_runtime_pending').length,failed:directives.filter(d=>d.status==='failed').length,positive_runtime_verified:0},directives,inputs:['rpc/source_request.go','store/source_rpc.go','store/source_rpc_cache.go','store/source_rpc_hash.go','store/source_intervals.go'].map(f=>hash(path.join(root,f))),source_context:[import.meta.filename,upstream+'/packages/core/src/sync-store/index.test.ts',upstream+'/packages/core/src/utils/order.ts',upstream+'/pnpm-lock.yaml'].map(hash)};
const destination=process.argv.find(a=>a.startsWith('--output='))?.slice(9)??path.join(root,'parity/runs/sync-store-m32/type-compile.json');fs.writeFileSync(destination,JSON.stringify(report,null,2)+'\n');console.log(report.summary);if(report.summary.failed)process.exitCode=1;
