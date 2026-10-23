import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import {createRequire} from "node:module";
import crypto from "node:crypto";
const root=path.resolve(import.meta.dirname,".."),upstream=path.resolve(root,"../ponder-upstream");
const require=createRequire(path.join(upstream,"packages/core/package.json")),ts=require("typescript"),viem=require("viem");
const ledger=JSON.parse(fs.readFileSync(path.join(root,"parity/test-ledger.json")));
for(const [file,outfile] of [["packages/core/src/build/factory.test.ts","testdata/factory/build-cases.json"],["packages/core/src/utils/offset.test.ts","testdata/abi/offset-cases.json"]]) {
  const source=fs.readFileSync(path.join(upstream,file),"utf8"),hash=crypto.createHash("sha256").update(source).digest("hex"),sf=ts.createSourceFile(file,source,99,true);
  const originals=ledger.tests.filter(x=>x.source_file===file);
  if(originals.some(x=>x.source_sha256!==hash))throw Error("Stale ledger");
  const transformed=ts.transform(sf,[ctx=>function visit(n){
    if(ts.isImportDeclaration(n))return undefined;
    if(ts.isCallExpression(n)&&n.expression.getText(sf)==="test")return ts.factory.updateCallExpression(n,n.expression,n.typeArguments,[...n.arguments.map(x=>ts.visitNode(x,visit)),ts.factory.createNumericLiteral(n.getStart(sf))]);
    return ts.visitEachChild(n,visit,ctx);
  }]).transformed[0];
  const code=ts.transpileModule(ts.createPrinter().printFile(transformed),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None}}).outputText;
  const output=[];let current;
  const plain=value=>JSON.parse(JSON.stringify(value));
  vm.runInNewContext(code,{
    parseAbiItem:viem.parseAbiItem,parseAbiParameter:viem.parseAbiParameter,getEventSelector:viem.getEventSelector,
    ...Object.fromEntries(["buildLogFactory","getBytesConsumedByParam","getNestedParamOffset"].map(operation=>[operation,(...args)=>({operation,args})])),
    test:(name,fn,start)=>{
      const original=originals.find(x=>x.start===start);if(!original||original.title!==name)throw Error("Missing source case");
      current={id:original.id,line:original.line,full_name:original.full_name,source_file:file,source_sha256:hash,assertions:[]};fn();
      if(current.assertions.length!==original.assertions.length)throw Error("Lost assertion "+name);
      output.push(current);current=null;
    },
    expect:actual=>Object.fromEntries(["toBe","toEqual","toMatchObject","toThrowError"].map(matcher=>[matcher,expected=>{
      const input=typeof actual==="function"?actual():actual;if(!input?.operation)throw Error("Unexpected expression");
      current.assertions.push({input:plain(input),matcher,expected:plain(expected)});
    }]))
  },{filename:file,timeout:5000});
  if(output.length!==originals.length)throw Error("Source count mismatch");
  fs.mkdirSync(path.dirname(path.join(root,outfile)),{recursive:true});fs.writeFileSync(path.join(root,outfile),JSON.stringify(output,null,2)+"\n");
  console.log({file,source_cases:output.length,assertions:output.reduce((n,x)=>n+x.assertions.length,0),method:"Symbolic function calls; only original source inputs and expected expressions become fixtures."});
}
