import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import os from "node:os";
import { spawnSync } from "node:child_process";
const root = path.resolve(import.meta.dirname, "..");
const cases = JSON.parse(fs.readFileSync(path.join(root,"testdata/abi/parameter-cases.json")));
const obligations = cases.flatMap(c => c.assertions.filter(a=>a.matcher==="expectTypeOf").map((a,i)=>({...a,id:c.id,full_name:c.full_name,source_sha256:c.source_sha256,name:`Case${c.id}Type${i}`})));
const directory = "abi/testdata/compile", generated = directory+"/generated", positive = directory+"/pass", negative = directory+"/fail";
for (const dir of [generated,positive,negative,"parity/runs"]) fs.mkdirSync(path.join(root,dir),{recursive:true});
const hash = v => crypto.createHash("sha256").update(v).digest("hex");
function command(args, options={}) {
  const result=spawnSync("go",args,{cwd:root,encoding:"utf8",timeout:45000,maxBuffer:8*1024*1024,...options});
  if (result.error) throw result.error;
  return result;
}
function requireSuccess(args,options) { const r=command(args,options); if(r.status!==0) throw Error(r.stdout+r.stderr); return r.stdout; }
const schemas = obligations.map(a=>({package:"generated",name:a.name,params:a.input.params}));
fs.writeFileSync(path.join(root,directory,"schemas.json"),JSON.stringify(schemas,null,2)+"\n");
requireSuccess(["run","./cmd/abi-codegen",directory+"/schemas.json",generated]);
const decoders=obligations.map(a=>`${JSON.stringify(a.id)}:func()(any,error){return Decode${a.name}(${JSON.stringify(a.input.data)},abi.DecodeOptions{${a.input.format_address==="lowercase"?"FormatAddress:strings.ToLower":""}})},`).join("\n");
fs.writeFileSync(path.join(root,generated,"TypedSource_test.go"),fs.readFileSync(path.join(root,"tools/abi-typed-template.txt"),"utf8").replace("// INSERT_DECODERS",decoders));
function field(name) {
  let upper=true,out="";
  for(const ch of name) {
    if(!/[\p{L}\p{N}]/u.test(ch)){upper=true;continue;}
    if(!out && /\p{N}/u.test(ch))out="Field";
    out+=upper?ch.toUpperCase():ch;upper=false;
  }
  return out||"Field";
}
function element(p) {return {...p,type:p.type.replace(/\[\d*\]$/,"")};}
function children(p) { return p.root??p.components; }
function expectedType(t,p) {
  if(t.kind==="scalar") {
    const result={bigint:"*big.Int",number:"int64",boolean:"bool",string:"string",Address:"string",Hex:"string"}[t.type];
    if(!result)throw Error("Unresolved scalar type "+t.type);
    return result;
  }
  if(t.kind==="array") return "[]"+expectedType(t.element,element(p));
  if(t.kind==="object") return "struct {"+t.fields.map(f=>`${field(f.name)} ${expectedType(f.type,p.components?.find(c=>c.name===f.name)??{})} ${JSON.stringify('abi:'+JSON.stringify(f.name))};`).join("")+"}";
  if(t.kind==="tuple") {
    const arr=p.type?.match(/\[(\d+)\]$/);
    if(arr) {
      if(Number(arr[1])!==t.elements.length)throw Error("Fixed array length disagrees with source type");
      const types=t.elements.map(x=>expectedType(x,element(p)));
      if(new Set(types).size!==1)throw Error("Heterogeneous fixed array type");
      return `[${t.elements.length}]${types[0]}`;
    }
    return "struct {"+t.elements.map((x,i)=>`Value${i} ${expectedType(x,children(p)?.[i]??{})};`).join("")+"}";
  }
  throw Error("Unresolved expected type "+JSON.stringify(t));
}
function wrongLeaf(t,p,access="out") {
  if(t.kind==="scalar")return {expression:access,wrong:t.type==="string"||t.type==="Hex"||t.type==="Address"?"bool":"string",diagnostic:"cannot use"};
  if(t.kind==="object") {const f=t.fields[0];if(!f)return {expression:access+".Missing",wrong:"bool",diagnostic:"has no field"};return wrongLeaf(f.type,p.components?.find(c=>c.name===f.name)??{},access+"."+field(f.name));}
  if(t.kind==="array")return wrongLeaf(t.element,element(p),access+"[0]");
  if(!t.elements.length)return {expression:access+".Missing",wrong:"bool",diagnostic:"has no field"};
  return wrongLeaf(t.elements[0],p.type?.match(/\[\d+\]$/)?element(p):children(p)?.[0]??{},access+(p.type?.match(/\[\d+\]$/)?"[0]":".Value0"));
}
// TS readonly fixed tuples are assignable to readonly arrays. Go fixed arrays
// are not assignable to slices, so the matching source assertion needs explicit
// array-to-slice conversion; scalar expectations still come from source types.
function widensArray(t,p) {
  if(t.kind==="array")return /\[\d+\]$/.test(p.type??"") || widensArray(t.element,element(p));
  if(t.kind==="object")return t.fields.some(f=>widensArray(f.type,p.components?.find(c=>c.name===f.name)??{}));
  if(t.kind==="tuple")return t.elements.some((x,i)=>widensArray(x,/\[\d+\]$/.test(p.type??"")?element(p):children(p)?.[i]??{}));
  return false;
}
function widenedValue(t,p,access) {
  if(t.kind==="scalar")return access;
  if(t.kind==="array")return `func() ${expectedType(t,p)} { var result ${expectedType(t,p)}; for _, item := range ${access} { result = append(result, ${widenedValue(t.element,element(p),"item")}) }; return result }()`;
  if(t.kind==="object")return expectedType(t,p)+"{"+t.fields.map(f=>`${field(f.name)}:${widenedValue(f.type,p.components?.find(c=>c.name===f.name)??{},access+"."+field(f.name))}`).join(",")+"}";
  const arr=/\[\d+\]$/.test(p.type??"");
  if(!arr && children(p)?.length!==t.elements.length)throw Error("Tuple arity differs from expected source type");
  return expectedType(t,p)+"{"+t.elements.map((x,i)=>(arr?"":`Value${i}:`)+widenedValue(x,arr?element(p):children(p)[i],access+(arr?`[${i}]`:`.Value${i}`))).join(",")+"}";
}
const scratch=fs.mkdtempSync(path.join(os.tmpdir(),"ponder-go-compile-"));
try {
  const importcfg=requireSuccess(["list","-deps","-export","-f","{{if .Export}}packagefile {{.ImportPath}}={{.Export}}{{end}}","./abi"]);
  const cfg=path.join(scratch,"importcfg");fs.writeFileSync(cfg,importcfg);
  const report={baseline:"17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334",tool:requireSuccess(["version"]).trim(),method:"ABI inputs generate decoder signatures; independent source expectTypeOf AST generates positive assignments. Each obligation also has an intentionally incompatible leaf assignment as a negative control. Native fixed arrays/structs replace TS tuples; readonly has no Go equivalent, and template-literal Address/Hex are strings. These adaptations are explicit, not strict TS source compatibility.",obligations:[],supplementary_negative_controls:0};
  for(const a of obligations) {
    const typ=expectedType(a.expected_type.tree,{root:a.input.params});
    const imports=typ.includes("*big.Int")?'import "math/big"\n':"";
    const widening=widensArray(a.expected_type.tree,{root:a.input.params});
    const value=widening?widenedValue(a.expected_type.tree,{root:a.input.params},"out"):"out";
    const good=`package generated\n${imports}func check${a.name}() { out, _ := Decode${a.name}(""); var _ ${typ} = ${value} }\n`;
    const bad=wrongLeaf(a.expected_type.tree,{root:a.input.params});
    const badText=`package generated\nfunc check${a.name}() { out, _ := Decode${a.name}(""); var _ ${bad.wrong} = ${bad.expression} }\n`;
    const passfile=positive+"/"+a.name+".go",failfile=negative+"/"+a.name+".go",generatedfile=generated+"/"+a.name+".go";
    fs.writeFileSync(path.join(root,passfile),good);fs.writeFileSync(path.join(root,failfile),badText);
    const args=["tool","compile","-p","ponder.local/ponder-go/abi/compilefixture","-importcfg",cfg,"-o",path.join(scratch,a.name+".a"),generatedfile];
    const pass=command([...args,passfile]),fail=command([...args,failfile]);
    const passed=pass.status===0 && fail.status!==0 && (fail.stdout+fail.stderr).includes(bad.diagnostic) && (fail.stdout+fail.stderr).includes(failfile);
    report.obligations.push({source_id:a.id,source_line:a.expected_type.line,source_sha256:a.source_sha256,original_type:a.expected_type.text,adaptation:widening?"Explicit fixed-array to slice widening matching TypeScript readonly tuple assignability":"Direct assignment of ABI-generated return type to independently translated source type",status:passed?"passed":"failed",positive:{file:passfile,sha256:hash(good),exit_code:pass.status,diagnostics:pass.stdout+pass.stderr},negative_control:{file:failfile,sha256:hash(badText),exit_code:fail.status,expected_diagnostic:bad.diagnostic,diagnostics:fail.stdout+fail.stderr},generated:{file:generatedfile,sha256:hash(fs.readFileSync(path.join(root,generatedfile)))}});
    if(!passed)throw Error("Compile fixture failed: "+a.full_name+"\n"+pass.stdout+pass.stderr+fail.stdout+fail.stderr);
  }
  report.supplementary_negative_controls=report.obligations.length;
  report.summary={inline_source_type_obligations:obligations.length,positive_passed:report.obligations.filter(x=>x.positive.exit_code===0).length,negative_controls_rejected:report.obligations.filter(x=>x.negative_control.exit_code!==0).length,failed:report.obligations.filter(x=>x.status!=="passed").length};
  fs.writeFileSync(path.join(root,"parity/runs/compile-fixtures.json"),JSON.stringify(report,null,2)+"\n");
  console.log(report.summary);
} finally {fs.rmSync(scratch,{recursive:true,force:true});}
